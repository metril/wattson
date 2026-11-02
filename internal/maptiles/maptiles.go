// Package maptiles downloads BRouter routing segments (*.rd5 tiles) into the
// segments directory, one at a time, and reports what is present.
package maptiles

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/metril/wattson/internal/route"
	"github.com/metril/wattson/internal/settings"
)

// Status is the state of one tile.
type Status string

const (
	StatusPresent     Status = "present"
	StatusQueued      Status = "queued"
	StatusDownloading Status = "downloading"
	StatusError       Status = "error"
)

// Tile is one 5 degree segment file and its download state.
type Tile struct {
	Name       string
	Status     Status
	Bytes      int64 // bytes on disk (present) or received so far (downloading)
	TotalBytes int64 // size announced by the server; 0 when unknown
	Error      string
	UpdatedAt  time.Time
}

// DirInfo describes the segments directory.
type DirInfo struct {
	Path     string
	Exists   bool
	Writable bool
}

var (
	// ErrDirUnavailable means the segments directory is missing or not writable.
	ErrDirUnavailable = errors.New("maptiles: segments directory unavailable")
	// ErrClosed is returned after Close.
	ErrClosed = errors.New("maptiles: closed")
)

const (
	maxTileBytes  = 1 << 30 // no real tile is larger; refuse anything bigger
	maxIndexBytes = 8 << 20 // the remote directory listing
	indexTTL      = 24 * time.Hour
	indexTimeout  = 30 * time.Second
	idleTimeout   = 60 * time.Second // abort a download that receives nothing for this long
	writeProbe    = ".wattson-write-test"
)

var hrefRE = regexp.MustCompile(`href="([EW]\d+_[NS]\d+)\.rd5"`)

type job struct {
	Tile
	cancel context.CancelFunc // set while downloading
	done   chan struct{}      // closed when the worker has finished with the job; set while downloading
}

// Manager downloads and lists segment tiles. Safe for concurrent use.
type Manager struct {
	cfg    func(context.Context) (settings.Settings, error)
	client *http.Client
	idle   time.Duration
	now    func() time.Time

	mu      sync.Mutex
	jobs    map[string]*job // queued, downloading and failed tiles
	queue   []string
	started bool
	closed  bool
	wake    chan struct{}
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup

	idxMu  sync.Mutex
	idxURL string
	idx    []string
	idxAt  time.Time
}

// New returns a Manager reading its directory and server from cfg on every
// operation. The client must not impose an overall timeout (tiles are large);
// a stalled transfer is aborted by the manager itself. nil uses a default client.
func New(cfg func(context.Context) (settings.Settings, error), client *http.Client) *Manager {
	if client == nil {
		client = &http.Client{}
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{cfg: cfg, client: client, idle: idleTimeout, now: time.Now,
		jobs: map[string]*job{}, wake: make(chan struct{}, 1), ctx: ctx, cancel: cancel}
}

func dirInfo(dir string) DirInfo {
	di := DirInfo{Path: dir}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return di
	}
	di.Exists = true
	probe := filepath.Join(dir, writeProbe)
	if f, err := os.OpenFile(probe, os.O_WRONLY|os.O_CREATE, 0o644); err == nil {
		_ = f.Close()
		_ = os.Remove(probe)
		di.Writable = true
	}
	return di
}

func tileFile(dir, name string) string { return filepath.Join(dir, name+".rd5") }
func partFile(dir, name string) string { return filepath.Join(dir, "."+name+".rd5.part") }

func (m *Manager) dir(ctx context.Context) (string, settings.Settings, error) {
	s, err := m.cfg(ctx)
	if err != nil {
		return "", s, err
	}
	return s.BRouter.SegmentsDir, s, nil
}

// onDisk reports whether dir holds the finished tile.
func onDisk(dir, name string) (Tile, bool) {
	st, err := os.Stat(tileFile(dir, name))
	if err != nil || !st.Mode().IsRegular() {
		return Tile{}, false
	}
	return Tile{Name: name, Status: StatusPresent, Bytes: st.Size(), TotalBytes: st.Size(), UpdatedAt: st.ModTime().UTC()}, true
}

// List returns the directory state and the present tiles plus every queued,
// downloading and failed job, sorted by name.
func (m *Manager) List(ctx context.Context) (DirInfo, []Tile, error) {
	dir, _, err := m.dir(ctx)
	if err != nil {
		return DirInfo{}, nil, err
	}
	di := dirInfo(dir)
	byName := map[string]Tile{}
	if di.Exists {
		ents, err := os.ReadDir(dir)
		if err != nil {
			return di, nil, err
		}
		for _, e := range ents {
			name, ok := strings.CutSuffix(e.Name(), ".rd5")
			if !ok || !e.Type().IsRegular() {
				continue
			}
			if _, _, err := route.ParseTile(name); err != nil {
				continue
			}
			if t, ok := onDisk(dir, name); ok {
				byName[name] = t
			}
		}
	}
	m.mu.Lock()
	for name, j := range m.jobs {
		if _, ok := byName[name]; !ok {
			byName[name] = j.Tile
		}
	}
	m.mu.Unlock()
	out := make([]Tile, 0, len(byName))
	for _, t := range byName {
		out = append(out, t)
	}
	sort.Slice(out, func(i, k int) bool { return out[i].Name < out[k].Name })
	return di, out, nil
}

// Has reports whether the tile file is present and whether the directory exists.
func (m *Manager) Has(ctx context.Context, name string) (present, dirExists bool, err error) {
	if _, _, err := route.ParseTile(name); err != nil {
		return false, false, err
	}
	dir, _, err := m.dir(ctx)
	if err != nil {
		return false, false, err
	}
	st, err := os.Stat(dir)
	if err != nil || !st.IsDir() {
		return false, false, nil
	}
	_, ok := onDisk(dir, name)
	return ok, true, nil
}

// Enqueue queues name for download. A present or already queued tile returns
// its current state without a new job; a failed one is queued again.
func (m *Manager) Enqueue(ctx context.Context, name string) (Tile, error) {
	if _, _, err := route.ParseTile(name); err != nil {
		return Tile{}, err
	}
	dir, _, err := m.dir(ctx)
	if err != nil {
		return Tile{}, err
	}
	if di := dirInfo(dir); !di.Exists || !di.Writable {
		return Tile{}, ErrDirUnavailable
	}
	if t, ok := onDisk(dir, name); ok {
		return t, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return Tile{}, ErrClosed
	}
	if j := m.jobs[name]; j != nil && j.Status != StatusError {
		return j.Tile, nil
	}
	j := &job{Tile: Tile{Name: name, Status: StatusQueued, UpdatedAt: m.now().UTC()}}
	m.jobs[name] = j
	m.queue = append(m.queue, name)
	if !m.started {
		m.started = true
		m.wg.Add(1)
		go m.run()
	}
	select {
	case m.wake <- struct{}{}:
	default:
	}
	return j.Tile, nil
}

// Remove cancels the tile's job and deletes the file and any partial file.
func (m *Manager) Remove(ctx context.Context, name string) error {
	if _, _, err := route.ParseTile(name); err != nil {
		return err
	}
	m.mu.Lock()
	var done chan struct{}
	if j := m.jobs[name]; j != nil {
		delete(m.jobs, name)
		if j.cancel != nil {
			j.cancel()
			done = j.done
		}
	}
	m.mu.Unlock()
	if done != nil {
		select {
		case <-done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	dir, _, err := m.dir(ctx)
	if err != nil {
		return err
	}
	for _, p := range []string{tileFile(dir, name), partFile(dir, name)} {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

// Close cancels the worker and any running download and waits for them.
func (m *Manager) Close() {
	m.mu.Lock()
	m.closed = true
	m.mu.Unlock()
	m.cancel()
	m.wg.Wait()
}

func (m *Manager) run() {
	defer m.wg.Done()
	for {
		j, ctx := m.next()
		if j == nil {
			return
		}
		err := m.fetch(ctx, j)
		m.mu.Lock()
		close(j.done)
		j.cancel()
		if m.jobs[j.Name] == j { // not removed meanwhile
			if err == nil {
				delete(m.jobs, j.Name)
			} else {
				j.Status, j.Error, j.UpdatedAt = StatusError, err.Error(), m.now().UTC()
			}
		}
		m.mu.Unlock()
	}
}

// next blocks for the next queued job and marks it downloading; nil on Close.
func (m *Manager) next() (*job, context.Context) {
	for {
		m.mu.Lock()
		for len(m.queue) > 0 {
			name := m.queue[0]
			m.queue = m.queue[1:]
			j := m.jobs[name]
			if j == nil || j.Status != StatusQueued {
				continue
			}
			ctx, cancel := context.WithCancel(m.ctx)
			j.Status, j.cancel, j.done, j.UpdatedAt = StatusDownloading, cancel, make(chan struct{}), m.now().UTC()
			m.mu.Unlock()
			return j, ctx
		}
		m.mu.Unlock()
		select {
		case <-m.wake:
		case <-m.ctx.Done():
			return nil, nil
		}
	}
}

// progress records received bytes for j.
func (m *Manager) progress(j *job, n, total int64) {
	m.mu.Lock()
	j.Bytes, j.UpdatedAt = n, m.now().UTC()
	if total > 0 {
		j.TotalBytes = total
	}
	m.mu.Unlock()
}

// fetch downloads j to its .part file and renames it into place. On failure
// the .part file is removed.
func (m *Manager) fetch(ctx context.Context, j *job) (err error) {
	dir, s, err := m.dir(ctx)
	if err != nil {
		return errors.New("settings unavailable")
	}
	if di := dirInfo(dir); !di.Exists || !di.Writable {
		return errors.New("segments directory unavailable")
	}
	part, final := partFile(dir, j.Name), tileFile(dir, j.Name)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stalled := false
	var smu sync.Mutex
	timer := time.AfterFunc(m.idle, func() {
		smu.Lock()
		stalled = true
		smu.Unlock()
		cancel()
	})
	defer timer.Stop()
	fail := func(err error) error {
		smu.Lock()
		defer smu.Unlock()
		if stalled {
			return errors.New("download stalled")
		}
		if ctx.Err() != nil {
			return errors.New("cancelled")
		}
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err // the request URL is not part of the message
		}
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.BRouter.SegmentsURL+j.Name+".rd5", nil)
	if err != nil {
		return errors.New("invalid segments URL")
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return fail(fmt.Errorf("download failed: %w", err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	total := resp.ContentLength
	if total > maxTileBytes {
		return errors.New("file too large")
	}
	m.progress(j, 0, max(total, 0))

	f, err := os.OpenFile(part, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return errors.New("cannot write the segments directory")
	}
	defer func() {
		if err != nil {
			_ = f.Close()
			_ = os.Remove(part)
		}
	}()
	buf := make([]byte, 64<<10)
	var n int64
	for {
		timer.Reset(m.idle)
		k, rerr := resp.Body.Read(buf)
		if k > 0 {
			if n += int64(k); n > maxTileBytes {
				return errors.New("file too large")
			}
			if _, werr := f.Write(buf[:k]); werr != nil {
				return errors.New("cannot write the segments directory")
			}
			m.progress(j, n, 0)
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return fail(fmt.Errorf("download failed: %w", rerr))
		}
	}
	if total >= 0 && n != total {
		return errors.New("download incomplete")
	}
	if n == 0 {
		return errors.New("empty file")
	}
	if err := f.Sync(); err != nil {
		return errors.New("cannot write the segments directory")
	}
	if err := f.Close(); err != nil {
		return errors.New("cannot write the segments directory")
	}
	if ctx.Err() != nil { // removed or shut down just as the last byte arrived
		return fail(ctx.Err())
	}
	if err := os.Rename(part, final); err != nil {
		return errors.New("cannot write the segments directory")
	}
	if d, err := os.Open(dir); err == nil { // make the rename durable
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

// Available lists the tile names the segments server offers, parsed from its
// index page and cached for 24 hours.
func (m *Manager) Available(ctx context.Context) ([]string, error) {
	_, s, err := m.dir(ctx)
	if err != nil {
		return nil, err
	}
	base := s.BRouter.SegmentsURL
	m.idxMu.Lock()
	defer m.idxMu.Unlock()
	if m.idx != nil && m.idxURL == base && m.now().Sub(m.idxAt) < indexTTL {
		return append([]string(nil), m.idx...), nil
	}
	ctx, cancel := context.WithTimeout(ctx, indexTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base, nil)
	if err != nil {
		return nil, errors.New("invalid segments URL")
	}
	resp, err := m.client.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return nil, fmt.Errorf("segments server not reachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("segments server answered HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxIndexBytes))
	if err != nil {
		return nil, fmt.Errorf("segments server not reachable: %w", err)
	}
	seen := map[string]bool{}
	names := []string{}
	for _, mm := range hrefRE.FindAllStringSubmatch(string(body), -1) {
		if _, _, err := route.ParseTile(mm[1]); err == nil && !seen[mm[1]] {
			seen[mm[1]] = true
			names = append(names, mm[1])
		}
	}
	sort.Strings(names)
	m.idx, m.idxURL, m.idxAt = names, base, m.now()
	return append([]string(nil), names...), nil
}
