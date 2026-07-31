import { describe, it, expect, vi, afterEach } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import { MemoryRouter, Routes, Route } from 'react-router-dom';
import EventCard from './EventCard';
import * as s from './EventCard.css';
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

// ICU pads its range patterns with thin and narrow no-break spaces; compare
// against plain spaces so the expectations stay readable.
function dateLine(container: HTMLElement) {
  return container.querySelector(`.${s.date}`)?.textContent?.replace(/[\u2009\u202f\u00a0]/g, ' ');
}

afterEach(() => {
  vi.useRealTimers();
});

describe('EventCard', () => {
  it('shows only the start time when the event has no ends_at', () => {
    vi.setSystemTime(new Date('2026-06-01T00:00:00Z'));
    const { container } = renderCard();
    expect(dateLine(container)).toBe('Mon, Jun 15, 1:00 PM · The Bowl');
  });

  it('shows the start–end range when the event has an ends_at', () => {
    vi.setSystemTime(new Date('2026-06-01T00:00:00Z'));
    const { container } = renderCard(undefined, { ends_at: '2026-06-15T23:30:00Z' });
    expect(dateLine(container)).toBe('Mon, Jun 15, 1:00 – 4:30 PM · The Bowl');
  });

  it('navigates to the event detail page when clicked', () => {
    render(
      <MemoryRouter initialEntries={['/']}>
        <Routes>
          <Route path="/" element={<EventCard event={event} />} />
          <Route path="/events/:id" element={<div>Event detail page</div>} />
        </Routes>
      </MemoryRouter>,
    );
    fireEvent.click(screen.getByRole('heading', { name: 'PB Live' }));
    expect(screen.getByText('Event detail page')).toBeInTheDocument();
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

  it('shows the match score for a matched event', () => {
    renderCard();
    expect(screen.getByText(/82% match/)).toBeInTheDocument();
  });

  it('hides the match score for an unmatched city event', () => {
    renderCard(undefined, { score: 0, matched_because: { performers: [], genres: [] } });
    expect(screen.queryByText(/% match/)).not.toBeInTheDocument();
  });
});
