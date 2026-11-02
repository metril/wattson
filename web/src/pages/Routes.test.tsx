import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Route, Routes, useLocation } from 'react-router';
import { RoutesPage } from './Routes';
import { apiError, renderWithClient, routeFixture, stubApi } from '@/test-utils';

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

const Where = () => <p data-testid="where">{useLocation().pathname}</p>;
const ui = (
  <Routes>
    <Route path="/routes" element={<RoutesPage />} />
    <Route path="*" element={<Where />} />
  </Routes>
);

const file = () => new File(['<gpx/>'], 'ramp.gpx', { type: 'application/gpx+xml' });

describe('RoutesPage', () => {
  it('lists routes with distance and ascent', async () => {
    stubApi({ 'GET /api/routes': [200, [routeFixture()]] });
    renderWithClient(ui, '/routes');
    expect(await screen.findByRole('link', { name: 'Ramp' })).toHaveAttribute('href', '/routes/1');
    expect(screen.getByText('1.00 km')).toBeInTheDocument();
    expect(screen.getByText('50 m')).toBeInTheDocument();
    expect(screen.getByText('GPX file')).toBeInTheDocument();
  });

  it('offers Draw and Synthetic', async () => {
    stubApi({ 'GET /api/routes': [200, []] });
    renderWithClient(ui, '/routes');
    expect(await screen.findByText('No routes yet')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Draw' })).toHaveAttribute('href', '/routes/new/draw');
    expect(screen.getByRole('link', { name: 'Synthetic' })).toHaveAttribute('href', '/routes/new/synthetic');
  });

  it('uploads a file as multipart and opens the new route', async () => {
    const append = vi.spyOn(FormData.prototype, 'append');
    const stub = stubApi({
      'GET /api/routes': [200, []],
      'POST /api/routes/import': [201, routeFixture({ id: 1 })],
    });
    renderWithClient(ui, '/routes');
    const input = await screen.findByLabelText(/Route file/);
    expect(input).toHaveAttribute('accept', '.gpx,.tcx,.fit');
    await userEvent.upload(input, file());
    await waitFor(() => expect(screen.getByTestId('where')).toHaveTextContent('/routes/1'));
    expect(stub.calls.some((c) => c.key === 'POST /api/routes/import')).toBe(true);
    expect(append).toHaveBeenCalledWith('file', expect.objectContaining({ name: 'ramp.gpx' }));
  });

  it.each([
    ['unsupported_format', 400, 'whatever', 'Unsupported file. Use GPX, TCX or FIT.'],
    ['too_large', 413, 'whatever', 'File is larger than 20 MB.'],
    ['no_elevation', 422, 'Enable OpenTopoData in Settings.', 'Enable OpenTopoData in Settings.'],
  ])('maps %s to its message', async (code, status, serverMsg, want) => {
    stubApi({
      'GET /api/routes': [200, []],
      'POST /api/routes/import': apiError(status, code, serverMsg),
    });
    renderWithClient(ui, '/routes');
    await userEvent.upload(await screen.findByLabelText(/Route file/), file());
    expect(await screen.findByRole('alert')).toHaveTextContent(want);
    expect(screen.queryByTestId('where')).not.toBeInTheDocument();
  });
});
