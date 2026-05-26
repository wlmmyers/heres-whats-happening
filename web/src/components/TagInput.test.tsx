import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import TagInput from './TagInput';

describe('TagInput', () => {
  it('calls onAdd when Enter is pressed', async () => {
    const onAdd = vi.fn();
    const onRemove = vi.fn();
    render(<TagInput values={['jazz']} onAdd={onAdd} onRemove={onRemove} placeholder="add" />);
    await userEvent.type(screen.getByPlaceholderText('add'), 'rock{enter}');
    expect(onAdd).toHaveBeenCalledWith('rock');
  });

  it('does not call onAdd for empty input', async () => {
    const onAdd = vi.fn();
    render(<TagInput values={[]} onAdd={onAdd} onRemove={vi.fn()} placeholder="add" />);
    await userEvent.type(screen.getByPlaceholderText('add'), '{enter}');
    expect(onAdd).not.toHaveBeenCalled();
  });

  it('renders chips for each value and calls onRemove', async () => {
    const onRemove = vi.fn();
    render(<TagInput values={['jazz', 'rock']} onAdd={vi.fn()} onRemove={onRemove} placeholder="add" />);
    const removeJazz = screen.getByRole('button', { name: /remove jazz/i });
    await userEvent.click(removeJazz);
    expect(onRemove).toHaveBeenCalledWith('jazz');
  });
});
