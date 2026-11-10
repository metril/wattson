import { render, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { rideFixture } from '@/test-rides';
import { RideStats } from './RideStats';

describe('RideStats metrics', () => {
  it('shows dashes for unknown metrics and no zone bars', () => {
    render(<RideStats ride={rideFixture()} />);
    const m = within(screen.getByRole('region', { name: 'Training metrics' }));
    expect(m.getByText('TSS').nextElementSibling).toHaveTextContent('–');
    expect(screen.queryByText('Time in power zones')).toBeNull();
  });

  it('shows computed metrics and zones', () => {
    render(
      <RideStats
        ride={rideFixture({ tss: 85.4, intensity_factor: 0.91, peak_5m_w: 310, vam_m_per_h: 600, power_zone_s: [60, 60, 0, 0, 0, 0, 0] })}
        units="imperial"
      />,
    );
    const m = within(screen.getByRole('region', { name: 'Training metrics' }));
    expect(m.getByText('TSS').nextElementSibling).toHaveTextContent('85');
    expect(m.getByText('Intensity (IF)').nextElementSibling).toHaveTextContent('0.91');
    expect(m.getByText('Best 5 min').nextElementSibling).toHaveTextContent('310W');
    expect(m.getByText('Climb rate').nextElementSibling).toHaveTextContent('ft/h');
    expect(screen.getByText('Time in power zones')).toBeInTheDocument();
  });
});
