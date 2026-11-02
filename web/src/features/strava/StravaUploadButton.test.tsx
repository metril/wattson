import { act, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { StravaUploadButton } from './StravaUploadButton';
import { apiError, stubApi } from '@/test-utils';
import { stravaNone } from '@/test-rides';

const done = { ...stravaNone, status: 'done' as const, activity_id: 777, activity_url: 'https://www.strava.com/activities/777' };
const uploading = { ...stravaNone, status: 'uploading' as const, upload_id: 4 };

function ui(initial = stravaNone) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return (
    <QueryClientProvider client={qc}>
      <StravaUploadButton rideId={7} initial={initial} />
    </QueryClientProvider>
  );
}

beforeEach(() => vi.useFakeTimers({ shouldAdvanceTime: true }));
afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe('StravaUploadButton', () => {
  it('uploads, polls every 2 s and turns into a Strava link', async () => {
    let gets = 0;
    const api = stubApi({
      'POST /api/rides/7/strava': [202, uploading],
      'GET /api/rides/7/strava': () => [200, gets++ === 0 ? uploading : done],
    });
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
    render(ui());
    await user.click(screen.getByRole('button', { name: 'Upload to Strava' }));
    expect(await screen.findByText('Uploading…')).toBeInTheDocument();
    expect(api.calls.filter((c) => c.key === 'GET /api/rides/7/strava')).toHaveLength(0);
    await act(() => vi.advanceTimersByTimeAsync(2000));
    await act(() => vi.advanceTimersByTimeAsync(2000));
    const a = await screen.findByRole('link', { name: 'View on Strava' });
    expect(a).toHaveAttribute('href', 'https://www.strava.com/activities/777');
    expect(a).toHaveAttribute('target', '_blank');
    expect(a).toHaveAttribute('rel', 'noopener');
    const n = api.calls.filter((c) => c.key === 'GET /api/rides/7/strava').length;
    await act(() => vi.advanceTimersByTimeAsync(10000));
    expect(api.calls.filter((c) => c.key === 'GET /api/rides/7/strava')).toHaveLength(n);
  });

  it('stops polling on unmount', async () => {
    const api = stubApi({ 'GET /api/rides/7/strava': [200, uploading] });
    const { unmount } = render(ui(uploading));
    await act(() => vi.advanceTimersByTimeAsync(2000));
    const n = api.calls.length;
    expect(n).toBeGreaterThan(0);
    unmount();
    await act(() => vi.advanceTimersByTimeAsync(20000));
    expect(api.calls).toHaveLength(n);
  });

  it('tells the rider to connect Strava first', async () => {
    stubApi({ 'POST /api/rides/7/strava': apiError(409, 'strava_not_connected', 'x') });
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
    render(ui());
    await user.click(screen.getByRole('button', { name: 'Upload to Strava' }));
    expect(await screen.findByText('Connect Strava for this rider on the Riders page.')).toBeInTheDocument();
  });

  it('shows the failure and retries', async () => {
    const api = stubApi({ 'POST /api/rides/7/strava': [202, uploading] });
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
    render(ui({ ...stravaNone, status: 'error', error: 'Strava rate limit hit' }));
    expect(screen.getByText('Strava rate limit hit')).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Retry' }));
    expect(await screen.findByText('Uploading…')).toBeInTheDocument();
    expect(api.calls.filter((c) => c.key === 'POST /api/rides/7/strava')).toHaveLength(1);
  });

  it('words a configuration failure like the Riders card', () => {
    stubApi({});
    render(ui({ ...stravaNone, status: 'error', error: "Strava isn't configured on this server" }));
    expect(screen.getByText("Strava isn't set up on this server — ask an admin.")).toBeInTheDocument();
  });

  it('turns a failed status poll into a Retry state', async () => {
    stubApi({ 'GET /api/rides/7/strava': apiError(404, 'not_found', 'Ride not found') });
    render(ui(uploading));
    await act(() => vi.advanceTimersByTimeAsync(2000));
    expect(await screen.findByText('Ride not found')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Retry' })).toBeInTheDocument();
    expect(screen.queryByText('Uploading…')).not.toBeInTheDocument();
  });

  it('shows an already uploaded ride as a link without a request', () => {
    const api = stubApi({});
    render(ui(done));
    expect(screen.getByRole('link', { name: 'View on Strava' })).toBeInTheDocument();
    expect(api.calls).toHaveLength(0);
  });
});
