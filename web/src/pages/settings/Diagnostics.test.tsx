import { screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type { components } from '@/lib/openapi';
import { apiError, renderWithClient, stubApi } from '@/test-utils';
import { DiagnosticsPage } from './Diagnostics';

type Diag = components['schemas']['Diagnostics'];

const base = { checked_at: '2026-11-02T10:00:00Z', devices: [] };

const dbusDenied: Diag = {
  ...base,
  transport: 'local',
  local: {
    status: 'error',
    code: 'dbus_denied',
    message: 'permission denied: /run/dbus/system_bus_socket',
    fix: 'Add to compose.yml:\n  group_add:\n    - "108"',
  },
  devices: [
    {
      role: 'bike',
      address: 'AA:BB:CC:DD:EE:FF',
      state: 'idle',
      connected: false,
      rssi: -58,
      hint: 'Wake the bike by pedalling.',
    },
    { role: 'hr', address: 'AA:BB:CC:DD:EE:01', state: 'searching', connected: false },
  ],
};

const held: Diag = {
  ...base,
  transport: 'esphome',
  proxy: {
    status: 'error',
    code: 'held',
    message: 'another client holds the proxy',
    fix: 'Disable the ESPHome integration entry for this proxy in Home Assistant.',
    name: 'bt-proxy',
    version: '2026.10.0',
    mac: 'AA:BB:CC:00:11:22',
    flags: ['passive_scan', 'active_connections'],
    missing_flags: ['raw_advertisements'],
    free: 2,
    limit: 3,
    allocated: ['AA:BB:CC:DD:EE:FF'],
    subscription: 'held_by_other',
  },
};

describe('Diagnostics', () => {
  it('shows the adapter fix verbatim and copies it', async () => {
    const api = stubApi({ 'GET /api/diagnostics/bluetooth': [200, dbusDenied] });
    const user = userEvent.setup();
    const copy = vi.spyOn(navigator.clipboard, 'writeText');
    renderWithClient(<DiagnosticsPage />);
    const card = await screen.findByRole('region', { name: 'Local adapter' });
    expect(within(card).getByText('error')).toBeInTheDocument();
    expect(within(card).getByText(/group_add/)).toBeInTheDocument();
    await user.click(within(card).getByRole('button', { name: 'Copy' }));
    expect(copy).toHaveBeenCalledWith(dbusDenied.local!.fix);
    expect(screen.queryByRole('region', { name: 'ESPHome proxy' })).toBeNull();
    expect(api.calls[0].key).toBe('GET /api/diagnostics/bluetooth');
  });

  it('renders devices with signal strength and "not seen"', async () => {
    stubApi({ 'GET /api/diagnostics/bluetooth': [200, dbusDenied] });
    renderWithClient(<DiagnosticsPage />);
    const bike = await screen.findByRole('row', { name: /AA:BB:CC:DD:EE:FF/ });
    expect(bike).toHaveTextContent('-58 dBm');
    expect(bike).toHaveTextContent('Wake the bike by pedalling.');
    expect(screen.getByRole('row', { name: /AA:BB:CC:DD:EE:01/ })).toHaveTextContent('not seen');
  });

  it('shows the proxy held by another client, the fix and a red chip for a missing flag', async () => {
    stubApi({ 'GET /api/diagnostics/bluetooth': [200, held] });
    renderWithClient(<DiagnosticsPage />);
    const card = await screen.findByRole('region', { name: 'ESPHome proxy' });
    expect(within(card).getByText('Held by another client')).toBeInTheDocument();
    expect(within(card).getByText(held.proxy!.fix!)).toBeInTheDocument();
    expect(within(card).getByText('Slots: 2 of 3 free')).toBeInTheDocument();
    expect(within(card).getByText('AA:BB:CC:DD:EE:FF')).toBeInTheDocument();
    expect(within(card).getByText(/bt-proxy/)).toBeInTheDocument();
    expect(within(card).getByText('raw_advertisements')).toHaveClass('text-danger');
    expect(within(card).getByText('passive_scan')).not.toHaveClass('text-danger');
    expect(screen.queryByRole('region', { name: 'Local adapter' })).toBeNull();
  });

  it('says so when no Bluetooth is in use', async () => {
    stubApi({ 'GET /api/diagnostics/bluetooth': [200, { ...base, transport: 'none' }] });
    renderWithClient(<DiagnosticsPage />);
    expect(
      await screen.findByText('No Bluetooth in use (simulator or Home Assistant driver)'),
    ).toBeInTheDocument();
  });

  it('scans for signal strength with scan_s=4 and shows a spinner meanwhile', async () => {
    let finish!: () => void;
    const seen: string[] = [];
    stubApi({ 'GET /api/diagnostics/bluetooth': [200, dbusDenied] });
    const orig = globalThis.fetch;
    vi.stubGlobal('fetch', async (req: Request) => {
      seen.push(new URL(req.url).search);
      if (new URL(req.url).search.includes('scan_s=4')) await new Promise<void>((r) => (finish = r));
      return orig(req);
    });
    renderWithClient(<DiagnosticsPage />);
    await userEvent.click(await screen.findByRole('button', { name: 'Scan for signal strength' }));
    expect(await screen.findByRole('button', { name: /Scanning/ })).toBeDisabled();
    finish();
    expect(await screen.findByRole('button', { name: 'Scan for signal strength' })).toBeEnabled();
    expect(seen).toContain('?scan_s=0');
    expect(seen).toContain('?scan_s=4');
  });

  it('shows a request failure', async () => {
    stubApi({ 'GET /api/diagnostics/bluetooth': apiError(503, 'unavailable', 'diagnostics unavailable') });
    renderWithClient(<DiagnosticsPage />);
    expect(await screen.findByText('diagnostics unavailable')).toBeInTheDocument();
  });

  it('pauses the 10 s poll while a scan runs and warns about the proxy', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      let finish!: () => void;
      const seen: string[] = [];
      stubApi({ 'GET /api/diagnostics/bluetooth': [200, held] });
      const orig = globalThis.fetch;
      vi.stubGlobal('fetch', async (req: Request) => {
        const search = new URL(req.url).search;
        seen.push(search);
        if (search.includes('scan_s=4')) await new Promise<void>((r) => (finish = r));
        return orig(req);
      });
      const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
      renderWithClient(<DiagnosticsPage />);
      expect(await screen.findByText(/briefly takes the proxy's Bluetooth from Home Assistant/)).toBeInTheDocument();
      await user.click(screen.getByRole('button', { name: 'Scan for signal strength' }));
      await screen.findByRole('button', { name: /Scanning/ });
      await vi.advanceTimersByTimeAsync(25_000);
      expect(seen.filter((x) => x === '?scan_s=0')).toHaveLength(1);
      finish();
      await screen.findByRole('button', { name: 'Scan for signal strength' });
      await vi.advanceTimersByTimeAsync(10_500);
      expect(seen.filter((x) => x === '?scan_s=0').length).toBeGreaterThan(1);
    } finally {
      vi.useRealTimers();
    }
  });
});
