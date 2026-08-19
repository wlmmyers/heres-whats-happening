import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import ArtistImage from './ArtistImage';
import type { CalendarEvent, ImageCredit } from '../api/calendar';

const credit: ImageCredit = {
  file: 'Phoebe Bridgers 2022.jpg',
  description_url: 'https://commons.wikimedia.org/wiki/File:Phoebe_Bridgers_2022.jpg',
  artist: 'Jane Photographer',
  credit: 'Own work',
  license: 'Creative Commons Attribution-Share Alike 4.0',
  license_short_name: 'CC BY-SA 4.0',
  license_url: 'https://creativecommons.org/licenses/by-sa/4.0',
  usage_terms: 'Creative Commons Attribution-Share Alike 4.0',
  attribution_required: true,
};

const event: CalendarEvent = {
  id: 'e1',
  title: 'PB Live',
  starts_at: '2026-06-15T20:00:00Z',
  venue: { name: 'The Bowl' },
  score: 0.82,
  matched_because: { performers: [], genres: [] },
};

const withArtistImage = (overrides?: Partial<ImageCredit>): CalendarEvent => ({
  ...event,
  artist: {
    name: 'Phoebe Bridgers',
    image: {
      url: 'https://commons.test/pb.jpg',
      width: 800,
      height: 800,
      credit: { ...credit, ...overrides },
    },
  },
});

const openCredit = () => fireEvent.click(screen.getByRole('button', { name: /image credit/i }));

describe('ArtistImage', () => {
  it('renders nothing when the event has no image', () => {
    const { container } = render(<ArtistImage event={event} />);
    expect(container).toBeEmptyDOMElement();
  });

  it('renders the artist image when the event has no venue image', () => {
    render(<ArtistImage event={withArtistImage()} />);
    expect(screen.getByRole('presentation')).toHaveAttribute('src', 'https://commons.test/pb.jpg');
  });

  it('prefers the venue image over the artist image', () => {
    const withBoth = { ...withArtistImage(), image_url: 'https://cdn.test/promo.jpg' };
    render(<ArtistImage event={withBoth} />);
    expect(screen.getByRole('presentation')).toHaveAttribute('src', 'https://cdn.test/promo.jpg');
  });

  it('renders no credit button when the artist image has no credit', () => {
    const noCredit = { ...event, artist: { name: 'PB', image: { url: 'x', width: 1, height: 1 } } };
    render(<ArtistImage event={noCredit as CalendarEvent} />);
    expect(screen.queryByRole('button', { name: /image credit/i })).not.toBeInTheDocument();
  });

  it('renders no credit button when the venue image is the one displayed', () => {
    const withBoth = { ...withArtistImage(), image_url: 'https://cdn.test/promo.jpg' };
    render(<ArtistImage event={withBoth} />);
    expect(screen.queryByRole('button', { name: /image credit/i })).not.toBeInTheDocument();
  });

  it('shows no credit dialog until the credit button is clicked', () => {
    render(<ArtistImage event={withArtistImage()} />);
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  it('shows the credit fields in a dialog when the credit button is clicked', () => {
    render(<ArtistImage event={withArtistImage()} />);
    openCredit();
    const dialog = screen.getByRole('dialog');
    expect(dialog).toHaveTextContent('Jane Photographer');
    expect(dialog).toHaveTextContent('Own work');
    expect(dialog).toHaveTextContent('CC BY-SA 4.0');
    expect(dialog).toHaveTextContent('Phoebe Bridgers 2022.jpg');
  });

  it('links the license to its license url', () => {
    render(<ArtistImage event={withArtistImage()} />);
    openCredit();
    expect(
      screen.getByRole('link', { name: 'CC BY-SA 4.0 (opens in new window)' }),
    ).toHaveAttribute('href', 'https://creativecommons.org/licenses/by-sa/4.0');
  });

  it('links the file to its description page', () => {
    render(<ArtistImage event={withArtistImage()} />);
    openCredit();
    expect(
      screen.getByRole('link', { name: 'Phoebe Bridgers 2022.jpg (opens in new window)' }),
    ).toHaveAttribute('href', 'https://commons.wikimedia.org/wiki/File:Phoebe_Bridgers_2022.jpg');
  });

  it('omits credit fields the enrichment left empty', () => {
    render(<ArtistImage event={withArtistImage({ artist: undefined, credit: undefined })} />);
    openCredit();
    expect(screen.getByRole('dialog')).not.toHaveTextContent('Photographer');
    expect(screen.getByRole('dialog')).toHaveTextContent('CC BY-SA 4.0');
  });

  it('closes the dialog when the close button is clicked', () => {
    render(<ArtistImage event={withArtistImage()} />);
    openCredit();
    fireEvent.click(screen.getByRole('button', { name: /close/i }));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  it('closes the dialog when the backdrop is clicked', () => {
    render(<ArtistImage event={withArtistImage()} />);
    openCredit();
    fireEvent.click(screen.getByTestId('image-credit-backdrop'));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  // The image sits inside a click-to-navigate EventCard, so every click the
  // credit UI owns has to stop short of the card.
  it('does not click through to the containing card', () => {
    const onCardClick = vi.fn();
    render(
      <div onClick={onCardClick}>
        <ArtistImage event={withArtistImage()} />
      </div>,
    );
    openCredit();
    fireEvent.click(screen.getByRole('dialog'));
    fireEvent.click(screen.getByTestId('image-credit-backdrop'));
    expect(onCardClick).not.toHaveBeenCalled();
  });
});
