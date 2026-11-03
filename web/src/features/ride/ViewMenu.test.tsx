import { cleanup, render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type { Caps } from '@/lib/view-prefs';
import { ViewMenu } from './ViewMenu';

const caps: Caps = { webgl: true, reducedMotion: false, photos: true, map: true };

function setup(over: Partial<React.ComponentProps<typeof ViewMenu>> = {}) {
  const props: React.ComponentProps<typeof ViewMenu> = {
    value: '2d',
    caps,
    exaggeration: 1.5,
    onChange: vi.fn(),
    onExaggeration: vi.fn(),
    tier: 'full',
    onTier: vi.fn(),
    headingUp: false,
    onHeadingUp: vi.fn(),
    comboMap: '2d',
    onComboMap: vi.fn(),
    ...over,
  };
  render(<ViewMenu {...props} />);
  return props;
}
const open = () => userEvent.click(screen.getByRole('button', { name: 'View' }));

describe('ViewMenu', () => {
  it('is closed until View is pressed', async () => {
    setup();
    expect(screen.queryByRole('radiogroup')).not.toBeInTheDocument();
    await open();
    expect(within(screen.getByRole('radiogroup', { name: 'Ride view' })).getAllByRole('radio')).toHaveLength(5);
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
    expect(screen.getAllByText('No street photos for this route')).toHaveLength(3);
  });

  it('choosing Street photos reports it', async () => {
    const p = setup();
    await open();
    await userEvent.click(screen.getByRole('radio', { name: 'Street photos' }));
    expect(p.onChange).toHaveBeenCalledWith('photo');
  });

  it('the combined views are offered, report their choice and need photos', async () => {
    const p = setup();
    await open();
    await userEvent.click(screen.getByRole('radio', { name: 'Photos + map' }));
    expect(p.onChange).toHaveBeenCalledWith('photo-inset');
    await userEvent.click(screen.getByRole('radio', { name: 'Photos | map' }));
    expect(p.onChange).toHaveBeenCalledWith('split');
  });

  it('the combined views are disabled with the photo reason while photos are not ready', async () => {
    setup({ caps: { ...caps, photos: false, photoNote: 'Finding street photos…' } });
    await open();
    for (const name of ['Street photos', 'Photos + map', 'Photos | map']) {
      const r = screen.getByRole('radio', { name: new RegExp(name.replace(/[+|]/g, '\\$&')) });
      expect(r).toBeDisabled();
      expect(r).toHaveAccessibleDescription('Finding street photos…');
    }
  });

  it('the terrain slider shows only in 3D', async () => {
    setup();
    await open();
    expect(screen.queryByLabelText(/Terrain height/)).not.toBeInTheDocument();
    cleanup();
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
    const p = setup({ value: 'photo' });
    await open();
    expect(screen.getByRole('button', { name: 'View' })).toHaveAttribute('aria-controls');
    screen.getByRole('radio', { name: '2D map' }).focus();
    await userEvent.keyboard('{ArrowDown}');
    expect(screen.getByRole('radio', { name: /3D flyover/ })).toHaveFocus();
    expect(p.onChange).not.toHaveBeenCalled();
    await userEvent.keyboard('{ArrowDown}{ArrowDown}{ArrowDown}{ArrowDown}');
    expect(screen.getByRole('radio', { name: '2D map' })).toHaveFocus();
    await userEvent.keyboard('{ArrowUp}{Enter}');
    expect(p.onChange).toHaveBeenCalledTimes(1);
    expect(p.onChange).toHaveBeenCalledWith('split');
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
    setup({ value: 'photo', caps: { ...caps, webgl: false } }); // 3D is off, so End lands on Street photos and Home on 2D
    await open();
    screen.getByRole('radio', { name: '2D map' }).focus();
    await userEvent.keyboard('{End}');
    expect(screen.getByRole('radio', { name: 'Photos | map' })).toHaveFocus();
    await userEvent.keyboard('{Home}');
    expect(screen.getByRole('radio', { name: '2D map' })).toHaveFocus();
  });

  it('3D detail shows with 3D, reflects the effective tier and reports the choice', async () => {
    setup();
    await open();
    expect(screen.queryByRole('radiogroup', { name: '3D detail' })).not.toBeInTheDocument();
    cleanup();
    const p = setup({ value: '3d', tier: 'lite' });
    await open();
    expect(screen.getByRole('radio', { name: 'Lite' })).toBeChecked();
    await userEvent.click(screen.getByRole('radio', { name: 'Full (buildings)' }));
    expect(p.onTier).toHaveBeenCalledWith('full');
  });

  it('map orientation is offered for a 2D map only and reports heading up', async () => {
    const p = setup({ headingUp: true });
    await open();
    expect(screen.getByRole('radio', { name: 'Heading up' })).toBeChecked();
    await userEvent.click(screen.getByRole('radio', { name: 'North up' }));
    expect(p.onHeadingUp).toHaveBeenCalledWith(false);
    cleanup();
    setup({ value: 'photo' });
    await open();
    expect(screen.queryByRole('radiogroup', { name: 'Map orientation' })).not.toBeInTheDocument();
    cleanup();
    setup({ value: 'photo-inset', comboMap: '3d' }); // a 3D map has no orientation
    await open();
    expect(screen.queryByRole('radiogroup', { name: 'Map orientation' })).not.toBeInTheDocument();
  });

  it('the map style of the combined views is chosen there, and 3D can be unavailable', async () => {
    const p = setup({ value: 'split' });
    await open();
    await userEvent.click(screen.getByRole('radio', { name: 'Terrain (3D)' }));
    expect(p.onComboMap).toHaveBeenCalledWith('3d');
    expect(screen.queryByRole('radiogroup', { name: '3D detail' })).not.toBeInTheDocument();
    cleanup();
    setup({ value: 'photo-inset', comboMap: '3d' });
    await open();
    expect(screen.getByRole('radio', { name: 'Terrain (3D)' })).toBeChecked();
    expect(screen.getByRole('radiogroup', { name: '3D detail' })).toBeInTheDocument();
    cleanup();
    setup({ value: 'split', caps: { ...caps, reducedMotion: true } });
    await open();
    expect(screen.getByRole('radio', { name: 'Terrain (3D)' })).toBeDisabled();
  });

  it('arrows walk through every group, but leave the slider its own keys', async () => {
    setup({ value: '3d' });
    await open();
    screen.getByRole('radio', { name: 'Photos | map' }).focus();
    await userEvent.keyboard('{ArrowDown}');
    expect(screen.getByRole('radio', { name: 'Full (buildings)' })).toHaveFocus();
    const slider = screen.getByLabelText(/Terrain height/);
    slider.focus();
    await userEvent.keyboard('{ArrowRight}');
    expect(slider).toHaveFocus();
  });
});
