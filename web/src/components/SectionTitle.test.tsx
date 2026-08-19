import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import SectionTitle from './SectionTitle';
import * as s from './SectionTitle.css';

describe('SectionTitle', () => {
  it('renders its label as a heading', () => {
    render(<SectionTitle>This week</SectionTitle>);
    expect(screen.getByRole('heading', { name: 'This week' })).toBeInTheDocument();
  });

  // The rules are what make the title read as a divider between groups rather
  // than as a heading sitting on top of one.
  it('flanks the label with a rule on either side', () => {
    const { container } = render(<SectionTitle>This week</SectionTitle>);
    const heading = screen.getByRole('heading', { name: 'This week' });
    expect(container.querySelectorAll(`hr.${s.rule}`)).toHaveLength(2);
    expect(heading.firstElementChild?.tagName).toBe('HR');
    expect(heading.lastElementChild?.tagName).toBe('HR');
  });
});
