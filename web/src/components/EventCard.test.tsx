import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import EventCard from './EventCard';
import type { CalendarEvent } from '../api/calendar';

const event: CalendarEvent = {
  id: 'e1',
  title: 'PB Live',
  starts_at: '2026-06-15T20:00:00Z',
  venue: { name: 'The Bowl' },
  score: 0.82,
  matched_because: { performers: ['Phoebe Bridgers'], genres: ['indie'] },
};

function renderCard(onNotInterested?: (id: string) => void, overrides?: Partial<CalendarEvent>) {
  return render(
    <MemoryRouter>
      <EventCard event={{ ...event, ...overrides }} onNotInterested={onNotInterested} />
    </MemoryRouter>,
  );
}

describe('EventCard', () => {
  it('links to the event detail page', () => {
    renderCard();
    expect(screen.getByRole('link')).toHaveAttribute('href', '/events/e1');
  });

  it('renders an image tile when the event has an image_url', () => {
    const { container } = renderCard(undefined, { image_url: 'https://cdn.test/pb.jpg' });
    expect(container.querySelector('img')).toHaveAttribute('src', 'https://cdn.test/pb.jpg');
  });

  it('renders no image tile when the event has no image_url', () => {
    const { container } = renderCard();
    expect(container.querySelector('img')).toBeNull();
  });

  it('renders no Not interested button without the callback', () => {
    renderCard();
    expect(screen.queryByRole('button', { name: /not interested/i })).not.toBeInTheDocument();
  });

  it('calls onNotInterested with the event id when clicked', () => {
    const onNotInterested = vi.fn();
    renderCard(onNotInterested);
    fireEvent.click(screen.getByRole('button', { name: /not interested/i }));
    expect(onNotInterested).toHaveBeenCalledWith('e1');
  });

  it('renders no link when not interactive', () => {
    render(
      <MemoryRouter>
        <EventCard event={event} interactive={false} />
      </MemoryRouter>,
    );
    expect(screen.queryByRole('link')).not.toBeInTheDocument();
    expect(screen.getByText('PB Live')).toBeInTheDocument();
  });
});
