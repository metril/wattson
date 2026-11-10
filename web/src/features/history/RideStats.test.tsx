import { render, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { rideFixture } from '@/test-rides';
import { RideHero, RideStats } from './RideStats';

describe('RideHero', () => {
  it('shows the headline figures and hides TSS, IF and VI without values', () => {
    render(<RideHero ride={rideFixture()} />);
    const h = within(screen.getByRole('region', { name: 'Summary' }));
    expect(h.getByText('Avg power').nextElementSibling).toHaveTextContent('150W');
    expect(h.queryByText('TSS')).toBeNull();
    expect(h.queryByText(/^IF /)).toBeNull();
  });

  it('shows TSS and the IF/VI badges when computed', () => {
    render(<RideHero ride={rideFixture({ tss: 85.4, intensity_factor: 0.91, variability_index: 1.04 })} />);
    expect(screen.getByText('TSS').nextElementSibling).toHaveTextContent('85');
    expect(screen.getByText('IF 0.91')).toBeInTheDocument();
    expect(screen.getByText('VI 1.04')).toBeInTheDocument();
  });
});

describe('RideStats', () => {
  it('leaves out groups and figures that have no value', () => {
    render(<RideStats ride={rideFixture()} />);
    expect(screen.getByRole('region', { name: 'Effort' })).toBeInTheDocument();
    expect(screen.queryByRole('region', { name: 'Power bests' })).toBeNull();
    expect(screen.queryByText('Avg HR')).toBeNull();
    expect(screen.queryByText('Decoupling')).toBeNull();
  });

  it('shows computed metrics in their groups, in the chosen units', () => {
    render(<RideStats ride={rideFixture({ peak_5m_w: 310, vam_m_per_h: 600, ascent_m: 100, avg_hr_bpm: 140, efficiency_factor: 1.5 })} units="imperial" />);
    const bests = within(screen.getByRole('region', { name: 'Power bests' }));
    expect(bests.getByText('Best 5 min').nextElementSibling).toHaveTextContent('310W');
    const terrain = within(screen.getByRole('region', { name: 'Terrain and speed' }));
    expect(terrain.getByText('Climb rate').nextElementSibling).toHaveTextContent('ft/h');
    expect(screen.getByText('Avg HR')).toBeInTheDocument();
    expect(screen.getByText('Efficiency (EF)').nextElementSibling).toHaveTextContent('1.50');
  });
});
