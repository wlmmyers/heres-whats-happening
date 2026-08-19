import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act, render, screen, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter } from 'react-router-dom';
import { CalendarEventsUser } from './CalendarEventsUser';
import * as st from './SectionTitle.css';
import type { CalendarEvent } from '../api/calendar';

vi.mock('../api/calendar', () => ({ getCalendar: vi.fn() }));
vi.mock('../api/notInterested', () => ({ markNotInterested: vi.fn() }));
vi.mock('../auth/useAuth', () => ({ useAuth: vi.fn() }));

import { getCalendar } from '../api/calendar';
import { useAuth } from '../auth/useAuth';

// Wednesday, June 17 2026, in the zone vitest.config.ts pins. The week it lands
// in starts Sunday June 14; the two after start June 21 and June 28.
const NOW = new Date('2026-06-17T12:00:00-07:00');
const NEXT_WEEK_START = new Date(2026, 5, 21);
const WEEK_AFTER_START = new Date(2026, 5, 28);

// The component labels later weeks with toLocaleDateString(), which follows the
// runner's locale. Build the expectation the same way so the test doesn't
// depend on the machine's language settings.
const label = (d: Date) => d.toLocaleDateString();

// happy-dom has no IntersectionObserver, so stand in one the tests can fire to
// make LazyList ask for the next page. See LazyList.test.tsx.
class MockIntersectionObserver {
  static instances: MockIntersectionObserver[] = [];
  callback: (entries: { isIntersecting: boolean }[]) => void;
  constructor(callback: (entries: { isIntersecting: boolean }[]) => void) {
    this.callback = callback;
    MockIntersectionObserver.instances.push(this);
  }
  observe() {}
  unobserve() {}
  disconnect() {}
}

function fireNextPage() {
  const observer = MockIntersectionObserver.instances.at(-1);
  if (!observer) throw new Error('LazyList never observed a sentinel');
  act(() => observer.callback([{ isIntersecting: true }]));
}

function event(id: string, title: string, startsAt: string): CalendarEvent {
  return {
    id,
    title,
    starts_at: startsAt,
    venue: { name: 'The Bowl' },
    score: 0.5,
    matched_because: { performers: [], genres: [] },
  };
}

function renderList() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>
        <CalendarEventsUser
          gatePending={false}
          displayStyle="Full"
          spotifyConnected
          onSpotifyConnect={() => {}}
        />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

// The list interleaves two kinds of <li>: section titles and event cards. Read
// it back as a flat outline so tests can assert on the order of both together.
function listOutline(container: HTMLElement) {
  return Array.from(container.querySelectorAll('ul > li')).map((li) => {
    const section = li.querySelector(`.${st.title}`);
    return section
      ? `section: ${section.textContent}`
      : `event: ${li.querySelector('h3')?.textContent}`;
  });
}

beforeEach(() => {
  vi.resetAllMocks();
  vi.setSystemTime(NOW);
  MockIntersectionObserver.instances = [];
  vi.stubGlobal('IntersectionObserver', MockIntersectionObserver);
  vi.mocked(useAuth).mockReturnValue({
    status: 'authenticated',
    user: { id: 'u1', email: 'u@example.com', city_id: 'city-1', confirmed: true },
    login: vi.fn(),
    signup: vi.fn(),
    logout: vi.fn(),
    refreshUser: vi.fn(),
  });
});

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe('CalendarEventsUser week section titles', () => {
  it('titles the section holding events from the current week "This week"', async () => {
    vi.mocked(getCalendar).mockResolvedValue({
      events: [event('e1', 'Wednesday Show', '2026-06-17T19:00:00-07:00')],
    });

    renderList();

    await waitFor(() => expect(screen.getByText('Wednesday Show')).toBeInTheDocument());
    expect(screen.getByRole('heading', { name: 'This week' })).toBeInTheDocument();
  });

  it('titles later sections with the date their week starts on', async () => {
    vi.mocked(getCalendar).mockResolvedValue({
      events: [
        event('e1', 'Next Week Show', '2026-06-23T19:00:00-07:00'),
        event('e2', 'Week After Show', '2026-07-02T19:00:00-07:00'),
      ],
    });

    renderList();

    await waitFor(() => expect(screen.getByText('Next Week Show')).toBeInTheDocument());
    expect(screen.getByRole('heading', { name: label(NEXT_WEEK_START) })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: label(WEEK_AFTER_START) })).toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: 'This week' })).not.toBeInTheDocument();
  });

  it('renders each title in the list itself, ahead of the cards it introduces', async () => {
    vi.mocked(getCalendar).mockResolvedValue({
      events: [
        event('e1', 'Wednesday Show', '2026-06-17T19:00:00-07:00'),
        event('e2', 'Saturday Show', '2026-06-20T19:00:00-07:00'),
        event('e3', 'Next Week Show', '2026-06-23T19:00:00-07:00'),
      ],
    });

    const { container } = renderList();

    await waitFor(() => expect(screen.getByText('Next Week Show')).toBeInTheDocument());
    expect(listOutline(container)).toEqual([
      'section: This week',
      'event: Wednesday Show',
      'event: Saturday Show',
      `section: ${label(NEXT_WEEK_START)}`,
      'event: Next Week Show',
    ]);
  });

  it('gives a week one title however many events fall in it', async () => {
    vi.mocked(getCalendar).mockResolvedValue({
      events: [
        event('e1', 'Wednesday Show', '2026-06-17T19:00:00-07:00'),
        event('e2', 'Thursday Show', '2026-06-18T19:00:00-07:00'),
        event('e3', 'Saturday Show', '2026-06-20T19:00:00-07:00'),
      ],
    });

    const { container } = renderList();

    await waitFor(() => expect(screen.getByText('Saturday Show')).toBeInTheDocument());
    expect(container.querySelectorAll(`.${st.title}`)).toHaveLength(1);
    expect(screen.getByRole('heading', { name: 'This week' })).toBeInTheDocument();
  });

  // Each week renders a title element plus one element per event, and React
  // reconciles that list by key. Reused keys let it duplicate or omit cards when
  // the list changes underneath — a dismissal, or a page arriving.
  it('gives every element in the list a distinct key', async () => {
    const errors: string[] = [];
    const spy = vi.spyOn(console, 'error').mockImplementation((...args: unknown[]) => {
      errors.push(args.map(String).join(' '));
    });
    vi.mocked(getCalendar).mockResolvedValue({
      events: [
        event('e1', 'Wednesday Show', '2026-06-17T19:00:00-07:00'),
        event('e2', 'Saturday Show', '2026-06-20T19:00:00-07:00'),
        event('e3', 'Next Week Show', '2026-06-23T19:00:00-07:00'),
      ],
    });

    renderList();

    await waitFor(() => expect(screen.getByText('Next Week Show')).toBeInTheDocument());
    spy.mockRestore();
    expect(errors.filter((e) => /key/i.test(e))).toEqual([]);
  });

  // Events arrive ordered by start time but not grouped, and a page boundary
  // can land mid-week, so the same week can show up on two pages.
  it('keeps a week under one title when its events span two pages', async () => {
    vi.mocked(getCalendar)
      .mockResolvedValueOnce({
        events: [event('e1', 'Wednesday Show', '2026-06-17T19:00:00-07:00')],
        next_cursor: 'c1',
      })
      .mockResolvedValueOnce({
        events: [
          event('e2', 'Saturday Show', '2026-06-20T19:00:00-07:00'),
          event('e3', 'Next Week Show', '2026-06-23T19:00:00-07:00'),
        ],
      });

    const { container } = renderList();

    await waitFor(() => expect(screen.getByText('Wednesday Show')).toBeInTheDocument());
    fireNextPage();

    await waitFor(() => expect(screen.getByText('Next Week Show')).toBeInTheDocument());
    expect(listOutline(container)).toEqual([
      'section: This week',
      'event: Wednesday Show',
      'event: Saturday Show',
      `section: ${label(NEXT_WEEK_START)}`,
      'event: Next Week Show',
    ]);
  });
});
