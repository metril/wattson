import { render, screen, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter, useLocation } from 'react-router';
import { KioskPage } from './Kiosk';
import { stubApi } from '@/test-utils';

function Probe() {
  return <div data-testid="loc">{useLocation().pathname}</div>;
}

function renderKiosk() {
  const qc = new QueryClient();
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={['/kiosk']}>
        <Probe />
        <KioskPage />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  localStorage.clear();
  window.history.replaceState(null, '', '/');
});

describe('KioskPage', () => {
  it('stores the fragment token, scrubs the URL and goes to the ride screen', async () => {
    const stub = stubApi({});
    window.history.replaceState(null, '', '/kiosk#token=wat_abc');
    const spy = vi.spyOn(window.history, 'replaceState');
    renderKiosk();
    await waitFor(() => expect(screen.getByTestId('loc').textContent).toBe('/'));
    expect(localStorage.getItem('wattson.kiosk_token')).toBe('wat_abc');
    expect(spy).toHaveBeenCalledWith(null, '', '/kiosk');
    expect(window.location.hash).toBe('');
    expect(stub.calls.every((c) => !JSON.stringify(c).includes('wat_abc'))).toBe(true);
    const urls = (fetch as unknown as { mock: { calls: Request[][] } }).mock.calls.map((c) => c[0].url);
    expect(urls.some((u) => u.includes('wat_abc'))).toBe(false);
  });

  it('asks to scan a QR code when there is no token', () => {
    renderKiosk();
    expect(screen.getByText(/Scan the pairing QR code from Settings → Tokens/)).toBeInTheDocument();
    expect(screen.getByTestId('loc')).toHaveTextContent('/kiosk');
  });

  it('goes straight to the ride screen when already paired', async () => {
    localStorage.setItem('wattson.kiosk_token', 'wat_old');
    renderKiosk();
    await waitFor(() => expect(screen.getByTestId('loc').textContent).toBe('/'));
  });
});
