import { act, fireEvent, screen } from '@testing-library/react';
import { MapRegionsPage } from './MapRegions';
import { apiError, renderWithClient, settingsFixture, stubApi } from '@/test-utils';
import type { components } from '@/lib/openapi';

const m = vi.hoisted(() => ({
  setTiles: vi.fn(),
  api: null as unknown,
}));

vi.mock('@/features/map/MapView', async () => {
  const { useEffect } = await import('react');
  m.api = { setLine: vi.fn(), fit: vi.fn(), setMarker: vi.fn(), setWaypoints: vi.fn(), setTiles: m.setTiles };
  return {
    STYLE_URL: 'x',
    default: (p: { onReady(a: unknown): void }) => {
      useEffect(() => p.onReady(m.api), []); // eslint-disable-line react-hooks/exhaustive-deps
      return <div data-testid="regions-map" />;
    },
  };
});

type Tiles = components['schemas']['TilesOut'];
const tile = (name: string, over: Partial<components['schemas']['TileOut']> = {}): components['schemas']['TileOut'] => ({
  name,
  status: 'present',
  bytes: 150_000_000,
  total_bytes: 150_000_000,
  updated_at: '2026-01-01T00:00:00Z',
  ...over,
});
const tiles = (over: Partial<Tiles> = {}): Tiles => ({
  available: ['E5_N45', 'E10_N45', 'W5_N50', 'E15_N45'],
  dir: { exists: true, path: '/segments', writable: true },
  tiles: [tile('E5_N45'), tile('E10_N45', { status: 'downloading', bytes: 50, total_bytes: 100 })],
  ...over,
});

const LIST = 'GET /api/maps/tiles';
const settle = (ms = 0) => act(() => vi.advanceTimersByTimeAsync(ms));
type Feature = { properties: Record<string, unknown> };
const lastFeatures = () => (m.setTiles.mock.lastCall![0] as { features: Feature[] }).features;
const feature = (name: string) => lastFeatures().find((f) => f.properties.name === name)!.properties;

beforeEach(() => {
  vi.useFakeTimers();
  m.setTiles.mockClear();
});
afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

async function render(stubs: Parameters<typeof stubApi>[0] = {}) {
  const s = stubApi({ [LIST]: [200, tiles()], 'GET /api/settings': [200, settingsFixture()], ...stubs });
  renderWithClient(<MapRegionsPage />);
  await settle();
  return s;
}

describe('MapRegionsPage', () => {
  it('colours the grid by status with download progress', async () => {
    await render();
    expect(lastFeatures()).toHaveLength(72 * 36);
    expect(feature('E5_N45').status).toBe('present');
    expect(feature('E10_N45')).toMatchObject({ status: 'downloading', progress: 0.5 });
    expect(feature('W5_N50').status).toBe('available');
    expect(feature('E0_N0').status).toBe('unavailable');
  });

  it('selects a region on click and downloads it', async () => {
    const s = await render({ 'POST /api/maps/tiles/W5_N50': [202, tile('W5_N50', { status: 'queued' })] });
    const onTile = m.setTiles.mock.lastCall![1] as (n: string) => void;
    act(() => onTile('W5_N50'));
    expect(feature('W5_N50').status).toBe('selected');
    fireEvent.click(screen.getByRole('button', { name: 'Download 1 region' }));
    await settle();
    expect(s.calls.filter((c) => c.key === 'POST /api/maps/tiles/W5_N50')).toHaveLength(1);
    expect(screen.getByRole('button', { name: 'Download 0 regions' })).toBeDisabled();
  });

  it('does not select a downloaded region', async () => {
    await render();
    const onTile = m.setTiles.mock.lastCall![1] as (n: string) => void;
    act(() => onTile('E5_N45'));
    expect(feature('E5_N45').status).toBe('present');
    expect(screen.getByRole('button', { name: 'Download 0 regions' })).toBeDisabled();
  });

  it('adds a region by name from the keyboard form', async () => {
    await render();
    fireEvent.change(screen.getByLabelText('Region name'), { target: { value: 'e15_n45' } });
    fireEvent.click(screen.getByRole('button', { name: 'Add' }));
    expect(screen.getByRole('button', { name: 'Download 1 region' })).toBeEnabled();
    fireEvent.change(screen.getByLabelText('Region name'), { target: { value: 'nope' } });
    fireEvent.click(screen.getByRole('button', { name: 'Add' }));
    expect(screen.getByText(/Use a region name such as E5_N45/)).toBeInTheDocument();
  });

  it('deletes a present region after a second tap', async () => {
    const s = await render({ 'DELETE /api/maps/tiles/E5_N45': [204] });
    fireEvent.click(screen.getByRole('button', { name: 'Delete E5_N45' }));
    await settle();
    expect(s.calls.some((c) => c.key === 'DELETE /api/maps/tiles/E5_N45')).toBe(false);
    fireEvent.click(screen.getByRole('button', { name: 'Confirm delete E5_N45' }));
    await settle();
    expect(s.calls.filter((c) => c.key === 'DELETE /api/maps/tiles/E5_N45')).toHaveLength(1);
  });

  it('disarms the delete after 4 s', async () => {
    await render();
    fireEvent.click(screen.getByRole('button', { name: 'Delete E5_N45' }));
    await settle(4000);
    expect(screen.getByRole('button', { name: 'Delete E5_N45' })).toBeInTheDocument();
  });

  it('lets a running download be cancelled', async () => {
    const s = await render({ 'DELETE /api/maps/tiles/E10_N45': [204] });
    fireEvent.click(screen.getByRole('button', { name: 'Cancel E10_N45' }));
    await settle();
    expect(s.calls.some((c) => c.key === 'DELETE /api/maps/tiles/E10_N45')).toBe(true);
  });

  it('explains a missing segments folder', async () => {
    await render({ [LIST]: [200, tiles({ dir: { exists: false, path: '/segments', writable: false } })] });
    expect(screen.getByText(/Segments folder/)).toHaveTextContent('/segments');
    expect(screen.getByText(/not found\. Mount the brouter-segments volume/)).toBeInTheDocument();
  });

  it('explains a read-only folder and an unreadable region list', async () => {
    await render({
      [LIST]: [
        200,
        tiles({ dir: { exists: true, path: '/segments', writable: false }, available: [], available_error: 'HTTP 503' }),
      ],
    });
    await settle();
    expect(screen.getByText('Segments folder is read-only.')).toBeInTheDocument();
    expect(
      screen.getByText(/Could not load the region list from https:\/\/brouter\.de\/brouter\/segments4\/; you can still download by name\./),
    ).toBeInTheDocument();
    // With no index every region may be requested by name.
    expect(feature('E0_N0').status).toBe('available');
  });

  it('says so when BRouter is remote', async () => {
    await render({ [LIST]: apiError(409, 'no_local_regions', 'No segments directory') });
    expect(screen.getByText(/Using a remote BRouter — local map regions disabled/)).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Integrations' })).toHaveAttribute('href', '/settings/integrations');
    expect(m.setTiles).not.toHaveBeenCalled();
  });

  it('polls every second while a region downloads', async () => {
    const s = await render();
    const gets = () => s.calls.filter((c) => c.key === LIST).length;
    expect(gets()).toBe(1);
    await settle(1000);
    expect(gets()).toBe(2);
    await settle(1000);
    expect(gets()).toBe(3);
  });

  it('polls slowly when idle and stops on unmount', async () => {
    const s = stubApi({ [LIST]: [200, tiles({ tiles: [tile('E5_N45')] })], 'GET /api/settings': [200, settingsFixture()] });
    const { unmount } = renderWithClient(<MapRegionsPage />);
    await settle();
    const gets = () => s.calls.filter((c) => c.key === LIST).length;
    await settle(5000);
    expect(gets()).toBe(1);
    await settle(25_000);
    expect(gets()).toBe(2);
    unmount();
    await settle(120_000);
    expect(gets()).toBe(2);
  });

  it('shows the copy about region size', async () => {
    await render();
    expect(screen.getByText(/Each region is 5°×5° \(up to ~300 MB\)\. BRouter picks up new regions without a restart\./)).toBeInTheDocument();
  });
});
