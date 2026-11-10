import { act, fireEvent, render, screen } from '@testing-library/react';
import { FitnessChart } from './FitnessChart';
import { durLabel, PowerCurve } from './PowerCurve';
import { TotalsChart } from './TotalsChart';

const rect = () =>
  vi.spyOn(Element.prototype, 'getBoundingClientRect').mockReturnValue({
    left: 0, top: 0, right: 220, bottom: 100, width: 220, height: 100, x: 0, y: 0, toJSON() {},
  });
const move = (el: Element, clientX: number) =>
  act(() => {
    el.dispatchEvent(new MouseEvent('pointermove', { clientX, bubbles: true }));
  });

describe('durLabel', () => {
  it('formats seconds, minutes and hours', () => {
    expect([1, 45, 60, 300, 1200, 3600, 5400, 7200].map(durLabel)).toEqual([
      '1 s', '45 s', '1 min', '5 min', '20 min', '1 h', '1.5 h', '2 h',
    ]);
  });
});

describe('PowerCurve', () => {
  const pts = [
    { dur_s: 5, watts: 900, w_per_kg: 12 },
    { dur_s: 60, watts: 500, w_per_kg: 6.67 },
    { dur_s: 3600, watts: 250 },
  ];

  it('reads out the point nearest the pointer on the log axis', () => {
    rect();
    render(<PowerCurve points={pts} />);
    const box = screen.getByRole('img', { name: /best power from 5 s to 1 h/i }).parentElement!;
    move(box, 0);
    expect(screen.getByTestId('curve-readout')).toHaveTextContent('5 s: 900 W (12.00 W/kg)');
    move(box, 220);
    expect(screen.getByTestId('curve-readout')).toHaveTextContent('1 h: 250 W');
    fireEvent.pointerLeave(box);
    expect(screen.getByTestId('curve-readout')).toHaveTextContent('');
  });

  it('shows the overlaid ride beside the best', () => {
    rect();
    render(<PowerCurve points={pts} ride={[{ dur_s: 5, watts: 700 }]} rideLabel="this ride" />);
    move(screen.getByRole('img').parentElement!, 0);
    expect(screen.getByTestId('curve-readout')).toHaveTextContent('· this ride 700 W');
    expect(screen.getByText(/Dashed: this ride/)).toBeInTheDocument();
  });
});

describe('FitnessChart', () => {
  const days = [
    { date: '2026-05-01', ctl: 10, atl: 20, tsb: -10 },
    { date: '2026-05-02', ctl: 12, atl: 15, tsb: -3 },
    { date: '2026-05-03', ctl: 14, atl: 10, tsb: 4 },
  ];

  it('defaults to the latest day and follows the pointer', () => {
    rect();
    render(<FitnessChart days={days} />);
    expect(screen.getByTestId('fitness-readout')).toHaveTextContent('2026-05-03 · CTL 14 · ATL 10 · TSB 4');
    move(screen.getByRole('img').parentElement!, 0);
    expect(screen.getByTestId('fitness-readout')).toHaveTextContent('2026-05-01 · CTL 10 · ATL 20 · TSB -10');
  });
});

describe('TotalsChart', () => {
  it('reads out the bar under the pointer with the formatter', () => {
    rect();
    const bars = [
      { label: 'W1', value: 10 },
      { label: 'W2', value: 30 },
    ];
    render(<TotalsChart title="Distance per week" bars={bars} fmt={(v) => `${v} km`} />);
    move(screen.getByRole('img').parentElement!, 200);
    expect(screen.getByTestId('totals-readout')).toHaveTextContent('W2: 30 km');
  });
});
