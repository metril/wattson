import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Toggle } from './Toggle';

describe('Toggle', () => {
  it.each([false, true])('hit area is at least 48 px (srOnly=%s)', async (srOnly) => {
    const onChange = vi.fn();
    render(<Toggle label="Admin" srOnly={srOnly} checked={false} onChange={onChange} />);
    const sw = screen.getByRole('switch', { name: 'Admin' });
    expect(sw.closest('label')).toHaveClass('min-h-touch', 'min-w-touch');
    await userEvent.click(sw.closest('label')!);
    expect(onChange).toHaveBeenCalledWith(true);
  });
});
