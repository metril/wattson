import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { SecretInput } from './SecretInput';

describe('SecretInput', () => {
  it('shows a saved placeholder, never a value, and stays silent untouched', () => {
    const onChange = vi.fn();
    render(<SecretInput label="Token" isSet onChange={onChange} />);
    const input = screen.getByLabelText('Token');
    expect(input).toHaveValue('');
    expect(input).toHaveAttribute('placeholder', expect.stringContaining('saved'));
    expect(onChange).not.toHaveBeenCalled();
  });

  it('reports typed text', async () => {
    const onChange = vi.fn();
    render(<SecretInput label="Token" isSet onChange={onChange} />);
    await userEvent.type(screen.getByLabelText('Token'), 'abc');
    expect(onChange).toHaveBeenLastCalledWith('abc');
  });

  it('reports undefined again when typed text is erased (blank = keep)', async () => {
    const onChange = vi.fn();
    render(<SecretInput label="Token" isSet onChange={onChange} />);
    await userEvent.type(screen.getByLabelText('Token'), 'a{Backspace}');
    expect(onChange).toHaveBeenLastCalledWith(undefined);
  });

  it('Clear sends an empty string and Undo goes back to keep', async () => {
    const onChange = vi.fn();
    render(<SecretInput label="Token" isSet onChange={onChange} />);
    await userEvent.click(screen.getByRole('button', { name: /clear/i }));
    expect(onChange).toHaveBeenLastCalledWith('');
    expect(screen.getByLabelText('Token')).toBeDisabled();
    await userEvent.click(screen.getByRole('button', { name: /undo/i }));
    expect(onChange).toHaveBeenLastCalledWith(undefined);
  });

  it('has no Clear button when nothing is stored', () => {
    render(<SecretInput label="Token" isSet={false} onChange={() => {}} />);
    expect(screen.queryByRole('button', { name: /clear/i })).toBeNull();
    expect(screen.getByLabelText('Token')).not.toHaveAttribute('placeholder', expect.stringContaining('saved'));
  });
});
