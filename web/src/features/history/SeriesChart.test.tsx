import { act, fireEvent, render, screen } from '@testing-library/react';
import { SeriesChart } from './SeriesChart';

describe('SeriesChart', () => {
  it('reads out the sample nearest the pointer', () => {
    vi.spyOn(Element.prototype, 'getBoundingClientRect').mockReturnValue({
      left: 0, top: 0, right: 220, bottom: 100, width: 220, height: 100, x: 0, y: 0, toJSON() {},
    });
    render(<SeriesChart x={[0, 1, 2]} y={[100, 200, 150]} label="Power" unit="W" color="#fff" />);
    const box = screen.getByRole('img', { name: /power/i }).parentElement!;
    // jsdom has no PointerEvent; a MouseEvent of that type carries clientX.
    act(() => {
      box.dispatchEvent(new MouseEvent('pointermove', { clientX: 110, bubbles: true }));
    });
    expect(screen.getByTestId('chart-readout')).toHaveTextContent('200 W @ 1.00 km');
    fireEvent.pointerLeave(box);
    expect(screen.getByTestId('chart-readout')).toHaveTextContent('');
  });
});
