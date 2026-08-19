import { describe, it, expect } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import CollapsableSection from './CollapsableSection';

const toggle = () => screen.getByRole('button', { name: /About the artist/i });

describe('CollapsableSection', () => {
  it('renders the title as a heading', () => {
    render(
      <CollapsableSection title="About the artist">
        <p>Bio text</p>
      </CollapsableSection>,
    );
    expect(screen.getByRole('heading', { name: 'About the artist' })).toBeInTheDocument();
  });

  it('shows its children expanded by default', () => {
    render(
      <CollapsableSection title="About the artist">
        <p>Bio text</p>
      </CollapsableSection>,
    );
    expect(screen.getByText('Bio text')).toBeInTheDocument();
    expect(toggle()).toHaveAttribute('aria-expanded', 'true');
  });

  it('hides its children when the caret button is clicked', async () => {
    render(
      <CollapsableSection title="About the artist">
        <p>Bio text</p>
      </CollapsableSection>,
    );
    await userEvent.click(toggle());
    expect(toggle()).toHaveAttribute('aria-expanded', 'false');
    await waitFor(() => expect(screen.queryByText('Bio text')).toBeNull());
  });

  it('shows its children again when the caret button is clicked twice', async () => {
    render(
      <CollapsableSection title="About the artist">
        <p>Bio text</p>
      </CollapsableSection>,
    );
    await userEvent.click(toggle());
    await waitFor(() => expect(screen.queryByText('Bio text')).toBeNull());
    await userEvent.click(toggle());
    expect(screen.getByText('Bio text')).toBeInTheDocument();
    expect(toggle()).toHaveAttribute('aria-expanded', 'true');
  });

  it('keeps the title visible while collapsed', async () => {
    render(
      <CollapsableSection title="About the artist">
        <p>Bio text</p>
      </CollapsableSection>,
    );
    await userEvent.click(toggle());
    expect(screen.getByRole('heading', { name: 'About the artist' })).toBeInTheDocument();
  });

  // The header row used to be a click-handling <div>, which no keyboard could
  // reach.
  it('toggles from the keyboard', async () => {
    render(
      <CollapsableSection title="About the artist">
        <p>Bio text</p>
      </CollapsableSection>,
    );
    await userEvent.tab();
    expect(toggle()).toHaveFocus();
    await userEvent.keyboard('{Enter}');
    await waitFor(() => expect(screen.queryByText('Bio text')).toBeNull());
    await userEvent.keyboard(' ');
    expect(screen.getByText('Bio text')).toBeInTheDocument();
  });

  it('points the toggle at the body it controls', () => {
    render(
      <CollapsableSection title="About the artist">
        <p>Bio text</p>
      </CollapsableSection>,
    );
    const bodyId = toggle().getAttribute('aria-controls');
    expect(bodyId).toBeTruthy();
    expect(document.getElementById(bodyId!)).toContainElement(screen.getByText('Bio text'));
  });
});
