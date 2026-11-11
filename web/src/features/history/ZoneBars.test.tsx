import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { ZoneBars } from './ZoneBars';

describe('ZoneBars', () => {
  it('renders nothing without data', () => {
    const { container } = render(<ZoneBars title="Power zones" kind="power" seconds={null} />);
    expect(container).toBeEmptyDOMElement();
    const empty = render(<ZoneBars title="Power zones" kind="power" seconds={[0, 0, 0]} />);
    expect(empty.container).toBeEmptyDOMElement();
  });

  it('scales bars to the longest zone and shows shares', () => {
    render(<ZoneBars title="Power zones" kind="power" seconds={[600, 1200, 0, 0, 0, 0, 1200]} />);
    expect(screen.getByRole('region', { name: 'Power zones' })).toBeInTheDocument();
    expect(screen.getByTestId('zone-power-1')).toHaveStyle({ width: '50%' });
    expect(screen.getByTestId('zone-power-2')).toHaveStyle({ width: '100%' });
    expect(screen.getByTestId('zone-power-3')).toHaveStyle({ width: '0%' });
    expect(screen.getAllByText(/40%/)).toHaveLength(2);
  });

  it('shows watt ranges and percent of FTP when the base is known', () => {
    render(<ZoneBars title="Power zones" kind="power" seconds={[60, 60, 0, 0, 0, 0, 0]} base={200} />);
    expect(screen.getByText('<110 W · <55% FTP')).toBeInTheDocument();
    expect(screen.getByText('110–149 W · 55–75% FTP')).toBeInTheDocument();
    expect(screen.getByText('Active recovery')).toBeInTheDocument();
  });

  it('names heart-rate zones', () => {
    render(<ZoneBars title="HR zones" kind="hr" seconds={[10, 20, 30, 40, 50]} />);
    expect(screen.getByText('Maximum')).toBeInTheDocument();
  });
});
