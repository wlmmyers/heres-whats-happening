import { describe, it, expect, afterEach, vi } from 'vitest';
import { bucketEventsByWeek } from './weekBuckets';
import type { CalendarEvent } from '../api/calendar';

function event(id: string, startsAt: string): CalendarEvent {
  return {
    id,
    title: id,
    starts_at: startsAt,
    venue: { name: 'The Bowl' },
    score: 0,
    matched_because: { performers: [], genres: [] },
  };
}

const ids = (buckets: ReturnType<typeof bucketEventsByWeek>) =>
  buckets.map((b) => [b.label, b.events.map((e) => e.id)]);

afterEach(() => {
  vi.useRealTimers();
});

describe('bucketEventsByWeek', () => {
  it('calls the week containing today "This week"', () => {
    // Wednesday, June 17 2026.
    vi.setSystemTime(new Date('2026-06-17T12:00:00-07:00'));
    const buckets = bucketEventsByWeek([event('e1', '2026-06-19T19:00:00-07:00')]);
    expect(ids(buckets)).toEqual([['This week', ['e1']]]);
  });

  // The week runs Sunday to Saturday, so a day that has already passed is still
  // this week — the calendar shows the rest of today, not the rest of the week.
  it('counts an earlier day of the current week as this week', () => {
    vi.setSystemTime(new Date('2026-06-17T12:00:00-07:00'));
    const buckets = bucketEventsByWeek([event('e1', '2026-06-15T19:00:00-07:00')]);
    expect(ids(buckets)).toEqual([['This week', ['e1']]]);
  });

  it('labels other weeks with the date their week starts on', () => {
    vi.setSystemTime(new Date('2026-06-17T12:00:00-07:00'));
    const buckets = bucketEventsByWeek([
      event('e1', '2026-06-23T19:00:00-07:00'),
      event('e2', '2026-07-02T19:00:00-07:00'),
    ]);
    expect(ids(buckets)).toEqual([
      [new Date(2026, 5, 21).toLocaleDateString(), ['e1']],
      [new Date(2026, 5, 28).toLocaleDateString(), ['e2']],
    ]);
  });

  // A week is keyed by the Sunday it starts on, which for the first days of
  // January falls in the year before.
  it('labels a week that starts in the previous year', () => {
    vi.setSystemTime(new Date('2026-12-16T12:00:00-08:00'));
    const buckets = bucketEventsByWeek([event('e1', '2027-01-01T19:00:00-08:00')]);
    expect(ids(buckets)).toEqual([[new Date(2026, 11, 27).toLocaleDateString(), ['e1']]]);
  });

  it('starts a new week on Sunday', () => {
    vi.setSystemTime(new Date('2026-06-17T12:00:00-07:00'));
    const buckets = bucketEventsByWeek([
      event('saturday', '2026-06-20T19:00:00-07:00'),
      event('sunday', '2026-06-21T19:00:00-07:00'),
    ]);
    expect(ids(buckets)).toEqual([
      ['This week', ['saturday']],
      [new Date(2026, 5, 21).toLocaleDateString(), ['sunday']],
    ]);
  });

  // Pages arrive separately and a page boundary can land mid-week, so the same
  // week can turn up twice in one list.
  it('merges events from the same week that are not next to each other', () => {
    vi.setSystemTime(new Date('2026-06-17T12:00:00-07:00'));
    const buckets = bucketEventsByWeek([
      event('e1', '2026-06-17T19:00:00-07:00'),
      event('e2', '2026-06-23T19:00:00-07:00'),
      event('e3', '2026-06-20T19:00:00-07:00'),
    ]);
    expect(ids(buckets)).toEqual([
      ['This week', ['e1', 'e3']],
      [new Date(2026, 5, 21).toLocaleDateString(), ['e2']],
    ]);
  });

  it('buckets nothing when there are no events', () => {
    vi.setSystemTime(new Date('2026-06-17T12:00:00-07:00'));
    expect(bucketEventsByWeek([])).toEqual([]);
  });
});
