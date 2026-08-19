import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import ExternalLink from './ExternalLink';

describe('ExternalLink', () => {
  it('renders a link to href that opens in a new window', () => {
    render(<ExternalLink href="https://example.com/setlist">View setlist</ExternalLink>);

    const link = screen.getByRole('link');
    expect(link).toHaveAttribute('href', 'https://example.com/setlist');
    expect(link).toHaveAttribute('target', '_blank');
    expect(link).toHaveAttribute('rel', 'noreferrer');
  });

  it('tells screen readers the link opens in a new window', () => {
    render(<ExternalLink href="https://example.com">View event</ExternalLink>);

    expect(
      screen.getByRole('link', { name: 'View event (opens in new window)' }),
    ).toBeInTheDocument();
  });

  it('renders the icon inside the link', () => {
    const { container } = render(
      <ExternalLink href="https://example.com">View event</ExternalLink>,
    );

    expect(container.querySelector('a > svg')).toBeInTheDocument();
  });

  it('renders children as plain text when there is no href', () => {
    render(<ExternalLink>CC BY-SA 4.0</ExternalLink>);

    expect(screen.getByText('CC BY-SA 4.0')).toBeInTheDocument();
    expect(screen.queryByRole('link')).not.toBeInTheDocument();
  });

  it('renders no icon when there is no href', () => {
    const { container } = render(<ExternalLink>CC BY-SA 4.0</ExternalLink>);

    expect(container.querySelector('svg')).not.toBeInTheDocument();
  });

  it('applies a caller-supplied className to the link', () => {
    render(
      <ExternalLink href="https://example.com" className="view-event">
        View event
      </ExternalLink>,
    );

    expect(screen.getByRole('link')).toHaveClass('view-event');
  });
});
