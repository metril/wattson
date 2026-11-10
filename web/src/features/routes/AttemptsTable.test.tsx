import { render, screen, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import { AttemptsTable, type Attempt } from './AttemptsTable';

const a = (over: Partial<Attempt>): Attempt => ({
  ride_id: 1,
  started_at: '2026-05-01T08:00:00Z',
  elapsed_s: 125,
  avg_speed_kmh: 36,
  np_w: 210,
  avg_power_w: 200,
  avg_hr_bpm: 0,
  completed: true,
  is_pr: false,
  ...over,
});

describe('AttemptsTable', () => {
  it('lists newest first, marks the PR and partial rides', () => {
    render(
      <MemoryRouter>
        <AttemptsTable attempts={[a({ ride_id: 1, is_pr: true }), a({ ride_id: 2, completed: false })]} />
      </MemoryRouter>,
    );
    const rows = screen.getAllByRole('row').slice(1);
    expect(rows).toHaveLength(2);
    expect(within(rows[0]).getByText('Partial')).toBeInTheDocument();
    expect(within(rows[0]).getByRole('link')).toHaveAttribute('href', '/history/2');
    expect(within(rows[1]).getByText('PR')).toBeInTheDocument();
    expect(within(rows[1]).getByText('00:02:05')).toBeInTheDocument();
    expect(within(rows[1]).getByText('36.0 km/h')).toBeInTheDocument();
  });

  it('shows an empty note', () => {
    render(
      <MemoryRouter>
        <AttemptsTable attempts={[]} />
      </MemoryRouter>,
    );
    expect(screen.getByText('No rides on this route yet.')).toBeInTheDocument();
  });
});
