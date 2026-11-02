import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { FitButton } from './FitButton';

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  localStorage.clear();
});

describe('FitButton', () => {
  it('is a plain download link without a kiosk token', () => {
    render(<FitButton rideId={7} />);
    const a = screen.getByRole('link', { name: 'Download FIT' });
    expect(a).toHaveAttribute('href', '/api/rides/7/fit');
    expect(a).toHaveAttribute('download');
  });

  it('fetches with the kiosk token and saves the blob under the server filename', async () => {
    localStorage.setItem('wattson.kiosk_token', 'wat_k');
    const fetchMock = vi.fn(
      async (_url: string, _init?: RequestInit) =>
        new Response('FIT', {
          status: 200,
          headers: { 'content-disposition': 'attachment; filename="wattson-7-20261102-0505.fit"' },
        }),
    );
    vi.stubGlobal('fetch', fetchMock);
    const create = vi.fn(() => 'blob:x');
    const revoke = vi.fn();
    URL.createObjectURL = create;
    URL.revokeObjectURL = revoke;
    const saved: { download: string; href: string }[] = [];
    vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(function (this: HTMLAnchorElement) {
      saved.push({ download: this.download, href: this.href });
    });
    render(<FitButton rideId={7} />);
    await userEvent.click(screen.getByRole('button', { name: 'Download FIT' }));
    await waitFor(() => expect(saved).toHaveLength(1));
    expect(new Headers(fetchMock.mock.calls[0][1]?.headers).get('Authorization')).toBe('Bearer wat_k');
    expect(fetchMock.mock.calls[0][0]).toBe('/api/rides/7/fit');
    expect(create).toHaveBeenCalledTimes(1);
    expect(saved[0]).toEqual({ download: 'wattson-7-20261102-0505.fit', href: 'blob:x' });
    await waitFor(() => expect(revoke).toHaveBeenCalledWith('blob:x'));
  });

  it('shows the server error', async () => {
    localStorage.setItem('wattson.kiosk_token', 'wat_k');
    vi.stubGlobal(
      'fetch',
      vi.fn(
        async () =>
          new Response(JSON.stringify({ error: { code: 'ride_in_progress', message: 'Ride still recording' } }), {
            status: 409,
            headers: { 'content-type': 'application/json' },
          }),
      ),
    );
    render(<FitButton rideId={7} />);
    await userEvent.click(screen.getByRole('button', { name: 'Download FIT' }));
    expect(await screen.findByText('Ride still recording')).toBeInTheDocument();
  });
});
