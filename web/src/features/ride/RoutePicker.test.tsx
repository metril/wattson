import { screen, waitFor } from '@testing-library/react';
import { renderWithClient, routeFixture, stubApi } from '@/test-utils';
import { RoutePicker } from './RoutePicker';

describe('RoutePicker', () => {
  it('clears a picked id that is not in the loaded list', async () => {
    stubApi({ 'GET /api/routes': [200, [routeFixture({ id: 2, name: 'Flat' })]] });
    const onChange = vi.fn();
    renderWithClient(<RoutePicker value={9} onChange={onChange} />);
    await waitFor(() => expect(onChange).toHaveBeenCalledWith(null));
  });

  it('keeps a picked id that exists', async () => {
    stubApi({ 'GET /api/routes': [200, [routeFixture({ id: 2, name: 'Flat' })]] });
    const onChange = vi.fn();
    renderWithClient(<RoutePicker value={2} onChange={onChange} />);
    await screen.findByRole('option', { name: 'Flat' });
    expect(screen.getByLabelText('Route')).toHaveValue('2');
    expect(onChange).not.toHaveBeenCalled();
  });
});
