import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Route, Routes, useLocation } from 'react-router';
import { RouteSyntheticPage } from './RouteSynthetic';
import { renderWithClient, routeFixture, stubApi } from '@/test-utils';

afterEach(() => vi.unstubAllGlobals());

const Where = () => <p data-testid="where">{useLocation().pathname}</p>;

describe('RouteSyntheticPage', () => {
  it('totals the segments and saves exactly the segment list', async () => {
    const stub = stubApi({ 'POST /api/routes/synthetic': [201, routeFixture({ id: 7 })] });
    renderWithClient(
      <Routes>
        <Route path="/routes/new/synthetic" element={<RouteSyntheticPage />} />
        <Route path="*" element={<Where />} />
      </Routes>,
      '/routes/new/synthetic',
    );
    const save = screen.getByRole('button', { name: 'Save route' });
    expect(save).toBeDisabled();

    await userEvent.type(screen.getByLabelText('Name'), 'Climb');
    const len1 = screen.getByLabelText('Segment 1 length (m)');
    await userEvent.clear(len1);
    await userEvent.type(len1, '1000');
    await userEvent.click(screen.getByRole('button', { name: 'Add segment' }));
    const len2 = screen.getByLabelText('Segment 2 length (m)');
    const grade2 = screen.getByLabelText('Segment 2 grade (%)');
    await userEvent.clear(len2);
    await userEvent.type(len2, '500');
    await userEvent.clear(grade2);
    await userEvent.type(grade2, '6');

    expect(screen.getByText('1.50 km · +30 m')).toBeInTheDocument();
    await userEvent.click(save);
    await waitFor(() => expect(screen.getByTestId('where')).toHaveTextContent('/routes/7'));
    expect(stub.bodies('POST /api/routes/synthetic')).toEqual([
      {
        name: 'Climb',
        segments: [
          { length_m: 1000, grade_pct: 0 },
          { length_m: 500, grade_pct: 6 },
        ],
      },
    ]);
  });

  it('removes a segment but keeps at least one', async () => {
    stubApi({});
    renderWithClient(<RouteSyntheticPage />);
    await userEvent.click(screen.getByRole('button', { name: 'Add segment' }));
    await userEvent.click(screen.getByRole('button', { name: 'Remove segment 1' }));
    expect(screen.queryByLabelText('Segment 2 length (m)')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Remove segment 1' })).toBeDisabled();
  });

  it('blocks saving an out-of-range grade', async () => {
    stubApi({});
    renderWithClient(<RouteSyntheticPage />);
    await userEvent.type(screen.getByLabelText('Name'), 'X');
    await userEvent.clear(screen.getByLabelText('Segment 1 grade (%)'));
    await userEvent.type(screen.getByLabelText('Segment 1 grade (%)'), '40');
    expect(screen.getByRole('button', { name: 'Save route' })).toBeDisabled();
  });
});
