import { screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import { render } from '@testing-library/react';
import { rideFixture } from '@/test-rides';
import { ZonesCard } from './ZonesCard';

const wrap = (ui: React.ReactElement) => render(<MemoryRouter>{ui}</MemoryRouter>);

describe('ZonesCard', () => {
  it('prompts to set FTP when the ride has none', () => {
    wrap(<ZonesCard ride={rideFixture()} />);
    expect(screen.getByText(/Set FTP to see zones/)).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Open Riders' })).toHaveAttribute('href', '/riders');
  });

  it('shows ranges for power and heart rate and the max HR note', () => {
    wrap(<ZonesCard ride={rideFixture({ ftp_w: 200, power_zone_s: [60, 60, 0, 0, 0, 0, 0], hr_zone_s: [0, 30, 30, 0, 0] })} maxHr={190} />);
    expect(screen.getByText('110–149 W · 55–75% FTP')).toBeInTheDocument();
    expect(screen.getByText('114–132 bpm · 60–70% max HR')).toBeInTheDocument();
    expect(screen.getByText(/based on current max HR \(190 bpm\)/)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'About zones' })).toBeInTheDocument();
  });

  it('renders nothing with FTP set but no zone data', () => {
    const { container } = wrap(<ZonesCard ride={rideFixture({ ftp_w: 200 })} />);
    expect(container).toBeEmptyDOMElement();
  });
});
