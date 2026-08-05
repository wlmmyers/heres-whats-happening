import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';

import AboutPage from './AboutPage';

describe('AboutPage', () => {
  it('leads with the rotating logo', () => {
    render(<AboutPage />);
    expect(screen.getByRole('img', { name: /logo/i })).toBeInTheDocument();
  });

  it('lists the user-facing features', () => {
    render(<AboutPage />);
    expect(screen.getByText('Rich ingestion of events')).toBeInTheDocument();
    expect(screen.getByText('Interests from your listening history and more')).toBeInTheDocument();
  });

  it('renders every feature and roadmap item', () => {
    render(<AboutPage />);
    expect(screen.getAllByRole('listitem')).toHaveLength(8);
  });
});
