import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import TagList from './TagList';

describe('TagList', () => {
  it('renders each value as a chip', () => {
    render(<TagList values={['jazz', 'shoegaze']} />);
    expect(screen.getByText('jazz')).toBeInTheDocument();
    expect(screen.getByText('shoegaze')).toBeInTheDocument();
  });

  it('is read-only — renders no remove buttons', () => {
    render(<TagList values={['jazz']} />);
    expect(screen.queryByRole('button')).not.toBeInTheDocument();
  });
});
