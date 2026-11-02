import { render, screen } from '@testing-library/react';
import { WorkoutChart } from './WorkoutChart';

const rider = { offset_level: 5, min_level: 1, max_level: 16, ftp_w: 300 };

describe('WorkoutChart', () => {
  it('draws one rect per block, sized by duration and level', () => {
    const { container } = render(
      <WorkoutChart type="interval" rider={rider} blocks={[{ duration_s: 60, level: 4 }, { duration_s: 120, level: 8 }]} />,
    );
    const rects = container.querySelectorAll('rect');
    expect(rects).toHaveLength(2);
    const num = (el: Element, a: string) => Number(el.getAttribute(a));
    expect(num(rects[1], 'width')).toBeCloseTo(2 * num(rects[0], 'width'));
    expect(num(rects[1], 'height')).toBeCloseTo(2 * num(rects[0], 'height'));
    expect(screen.getByRole('img', { name: 'Workout preview, 2 blocks' })).toBeInTheDocument();
  });

  it('titles ERG bars with resolved watts', () => {
    render(<WorkoutChart type="erg" rider={rider} blocks={[{ duration_s: 60, ftp_pct: 50 }]} />);
    expect(screen.getByText('150 W')).toBeInTheDocument();
  });

  it('titles interval bars with the resolved level', () => {
    render(<WorkoutChart type="interval" rider={rider} blocks={[{ duration_s: 60, level_offset: 3 }]} />);
    expect(screen.getByText('Level 8')).toBeInTheDocument();
  });

  it('puts the cursor at its share of the total', () => {
    render(<WorkoutChart type="interval" rider={rider} cursorS={90} blocks={[{ duration_s: 60, level: 4 }, { duration_s: 120, level: 8 }]} />);
    const c = screen.getByTestId('workout-cursor');
    // viewBox is 1000 wide: 90 s of 180 s is half way.
    expect(Number(c.getAttribute('x1'))).toBe(500);
  });

  it('has no cursor unless asked, and hides the compact variant from assistive tech', () => {
    const { container } = render(<WorkoutChart compact type="erg" rider={rider} blocks={[{ duration_s: 60, power_w: 200 }]} />);
    expect(screen.queryByTestId('workout-cursor')).not.toBeInTheDocument();
    expect(container.querySelector('svg')).toHaveAttribute('aria-hidden', 'true');
    expect(screen.queryByRole('img')).not.toBeInTheDocument();
  });
});
