import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type { Caps } from '@/lib/view-prefs';
import { ViewMenu } from './ViewMenu';

const caps: Caps = { webgl: true, reducedMotion: false, photos: true, map: true };

function setup(over: Partial<React.ComponentProps<typeof ViewMenu>> = {}) {
  const props = { value: '2d' as const, caps, exaggeration: 1.5, onChange: vi.fn(), onExaggeration: vi.fn(), ...over };
  render(<ViewMenu {...props} />);
  return props;
}
const open = () => userEvent.click(screen.getByRole('button', { name: 'View' }));

describe('ViewMenu', () => {
  it('is closed until View is pressed', async () => {
    setup();
    expect(screen.queryByRole('radiogroup')).not.toBeInTheDocument();
    await open();
    expect(screen.getAllByRole('radio')).toHaveLength(3);
    expect(screen.getByRole('radio', { name: '2D map' })).toBeChecked();
  });

  it('reduced motion disables 3D and says why', async () => {
    setup({ caps: { ...caps, reducedMotion: true } });
    await open();
    const r = screen.getByRole('radio', { name: /3D flyover/ });
    expect(r).toBeDisabled();
    expect(r).toHaveAccessibleDescription('Off because reduced motion is on');
  });

  it('no webgl and no photos explain themselves', async () => {
    setup({ caps: { ...caps, webgl: false, photos: false } });
    await open();
    expect(screen.getByText("3D isn't supported on this display")).toBeInTheDocument();
    expect(screen.getByText('No street photos for this route')).toBeInTheDocument();
  });

  it('choosing Street photos reports it', async () => {
    const p = setup();
    await open();
    await userEvent.click(screen.getByRole('radio', { name: 'Street photos' }));
    expect(p.onChange).toHaveBeenCalledWith('photo');
  });

  it('the terrain slider shows only in 3D', async () => {
    const { unmount } = render(
      <ViewMenu value="2d" caps={caps} exaggeration={1.5} onChange={vi.fn()} onExaggeration={vi.fn()} />,
    );
    await open();
    expect(screen.queryByLabelText(/Terrain height/)).not.toBeInTheDocument();
    unmount();
    const p = setup({ value: '3d' });
    await open();
    const s = screen.getByLabelText(/Terrain height/);
    expect(s).toHaveAttribute('min', '1');
    expect(s).toHaveAttribute('max', '3');
    expect(s).toHaveAttribute('step', '0.25');
    expect(p.onExaggeration).not.toHaveBeenCalled();
  });

  it('Escape closes and returns focus to View', async () => {
    setup();
    await open();
    await userEvent.click(screen.getByRole('radio', { name: '2D map' }));
    await userEvent.keyboard('{Escape}');
    expect(screen.queryByRole('radiogroup')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'View' })).toHaveFocus();
  });

  it('arrows move focus without selecting; Enter selects', async () => {
    const p = setup();
    await open();
    expect(screen.getByRole('button', { name: 'View' })).toHaveAttribute('aria-controls');
    screen.getByRole('radio', { name: '2D map' }).focus();
    await userEvent.keyboard('{ArrowDown}');
    expect(screen.getByRole('radio', { name: /3D flyover/ })).toHaveFocus();
    expect(p.onChange).not.toHaveBeenCalled();
    await userEvent.keyboard('{ArrowDown}{ArrowDown}');
    expect(screen.getByRole('radio', { name: '2D map' })).toHaveFocus();
    await userEvent.keyboard('{ArrowUp}{Enter}');
    expect(p.onChange).toHaveBeenCalledTimes(1);
    expect(p.onChange).toHaveBeenCalledWith('photo');
  });

  it('arrows skip disabled options', async () => {
    setup({ caps: { ...caps, webgl: false } });
    await open();
    screen.getByRole('radio', { name: '2D map' }).focus();
    await userEvent.keyboard('{ArrowDown}');
    expect(screen.getByRole('radio', { name: 'Street photos' })).toHaveFocus();
  });

  it('aria-controls is absent while closed', () => {
    setup();
    expect(screen.getByRole('button', { name: 'View' })).not.toHaveAttribute('aria-controls');
  });

  it('Home and End jump to the first and last enabled option', async () => {
    setup({ caps: { ...caps, webgl: false } }); // 3D is off, so End lands on Street photos and Home on 2D
    await open();
    screen.getByRole('radio', { name: '2D map' }).focus();
    await userEvent.keyboard('{End}');
    expect(screen.getByRole('radio', { name: 'Street photos' })).toHaveFocus();
    await userEvent.keyboard('{Home}');
    expect(screen.getByRole('radio', { name: '2D map' })).toHaveFocus();
  });
});
