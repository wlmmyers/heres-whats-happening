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
    const buckets = bucketEventsByWeek([event('e1', '2026-06-19T19:00:00-07:00')], undefined);
    expect(ids(buckets)).toEqual([['This week', ['e1']]]);
  });

  // The week runs Sunday to Saturday, so a day that has already passed is still
  // this week — the calendar shows the rest of today, not the rest of the week.
  it('counts an earlier day of the current week as this week', () => {
    vi.setSystemTime(new Date('2026-06-17T12:00:00-07:00'));
    const buckets = bucketEventsByWeek([event('e1', '2026-06-15T19:00:00-07:00')], undefined);
    expect(ids(buckets)).toEqual([['This week', ['e1']]]);
  });

  it('calls the week after this one "Next week"', () => {
    vi.setSystemTime(new Date('2026-06-17T12:00:00-07:00'));
    const buckets = bucketEventsByWeek([event('e1', '2026-06-23T19:00:00-07:00')], undefined);
    expect(ids(buckets)).toEqual([['Next week', ['e1']]]);
  });

  it('labels later weeks with the date their week starts on', () => {
    vi.setSystemTime(new Date('2026-06-17T12:00:00-07:00'));
    const buckets = bucketEventsByWeek(
      [event('e1', '2026-06-30T19:00:00-07:00'), event('e2', '2026-07-07T19:00:00-07:00')],
      undefined,
    );
    expect(ids(buckets)).toEqual([
      [new Date(2026, 5, 28).toLocaleDateString(), ['e1']],
      [new Date(2026, 6, 5).toLocaleDateString(), ['e2']],
    ]);
  });

  // A week is keyed by the Sunday it starts on, which for the first days of
  // January falls in the year before.
  it('labels a week that starts in the previous year', () => {
    vi.setSystemTime(new Date('2026-12-16T12:00:00-08:00'));
    const buckets = bucketEventsByWeek([event('e1', '2027-01-01T19:00:00-08:00')], undefined);
    expect(ids(buckets)).toEqual([[new Date(2026, 11, 27).toLocaleDateString(), ['e1']]]);
  });

  it('starts a new week on Sunday', () => {
    vi.setSystemTime(new Date('2026-06-17T12:00:00-07:00'));
    const buckets = bucketEventsByWeek(
      [
        event('saturday', '2026-06-20T19:00:00-07:00'),
        event('sunday', '2026-06-21T19:00:00-07:00'),
      ],
      undefined,
    );
    expect(ids(buckets)).toEqual([
      ['This week', ['saturday']],
      ['Next week', ['sunday']],
    ]);
  });

  // Pages arrive separately and a page boundary can land mid-week, so the same
  // week can turn up twice in one list.
  it('merges events from the same week that are not next to each other', () => {
    vi.setSystemTime(new Date('2026-06-17T12:00:00-07:00'));
    const buckets = bucketEventsByWeek(
      [
        event('e1', '2026-06-17T19:00:00-07:00'),
        event('e2', '2026-06-23T19:00:00-07:00'),
        event('e3', '2026-06-20T19:00:00-07:00'),
      ],
      undefined,
    );
    expect(ids(buckets)).toEqual([
      ['This week', ['e1', 'e3']],
      ['Next week', ['e2']],
    ]);
  });

  it('buckets nothing when there are no events', () => {
    vi.setSystemTime(new Date('2026-06-17T12:00:00-07:00'));
    expect(bucketEventsByWeek([], undefined)).toEqual([]);
  });

  // Events the user marked not interested are kept out of the bucket's event
  // list and counted in its label instead, so a week never silently shrinks.
  // A week whose events are all hidden renders nothing at all, so its count has
  // to carry forward to the next week that does render.
  describe('hidden events', () => {
    it('leaves the label alone when nothing in the week is hidden', () => {
      vi.setSystemTime(new Date('2026-06-17T12:00:00-07:00'));
      const buckets = bucketEventsByWeek(
        [event('e1', '2026-06-19T19:00:00-07:00'), event('e2', '2026-06-20T19:00:00-07:00')],
        ['some-other-event'],
      );
      expect(ids(buckets)).toEqual([['This week', ['e1', 'e2']]]);
    });

    it('appends the hidden count to the label of the week the hidden events fall in', () => {
      vi.setSystemTime(new Date('2026-06-17T12:00:00-07:00'));
      const buckets = bucketEventsByWeek(
        [
          event('e1', '2026-06-18T19:00:00-07:00'),
          event('e2', '2026-06-19T19:00:00-07:00'),
          event('e3', '2026-06-20T19:00:00-07:00'),
        ],
        ['e2', 'e3'],
      );
      expect(ids(buckets)).toEqual([['This week (+ 2 hidden)', ['e1']]]);
    });

    it('carries a fully hidden week’s count onto the next week that has events', () => {
      vi.setSystemTime(new Date('2026-06-17T12:00:00-07:00'));
      const buckets = bucketEventsByWeek(
        [
          event('e1', '2026-06-19T19:00:00-07:00'),
          event('e2', '2026-06-23T19:00:00-07:00'),
          event('e3', '2026-06-30T19:00:00-07:00'),
        ],
        ['e2'],
      );
      expect(ids(buckets)).toEqual([
        ['This week', ['e1']],
        ['Next week (+ 1 hidden)', []],
        [`${new Date(2026, 5, 28).toLocaleDateString()} (+ 1 hidden from prev weeks)`, ['e3']],
      ]);
    });

    it('adds up the counts of several fully hidden weeks in a row', () => {
      vi.setSystemTime(new Date('2026-06-17T12:00:00-07:00'));
      const buckets = bucketEventsByWeek(
        [
          event('e1', '2026-06-19T19:00:00-07:00'),
          event('e2', '2026-06-23T19:00:00-07:00'),
          event('e3', '2026-06-24T19:00:00-07:00'),
          event('e4', '2026-06-30T19:00:00-07:00'),
        ],
        ['e1', 'e2', 'e3'],
      );
      expect(ids(buckets)).toEqual([
        ['This week (+ 1 hidden)', []],
        ['Next week (+ 2 hidden, + 1 hidden from prev weeks)', []],
        [`${new Date(2026, 5, 28).toLocaleDateString()} (+ 3 hidden from prev weeks)`, ['e4']],
      ]);
    });

    it('stops carrying the count forward once a week with events has shown it', () => {
      vi.setSystemTime(new Date('2026-06-17T12:00:00-07:00'));
      const buckets = bucketEventsByWeek(
        [
          event('e1', '2026-06-19T19:00:00-07:00'),
          event('e2', '2026-06-20T19:00:00-07:00'),
          event('e3', '2026-06-23T19:00:00-07:00'),
        ],
        ['e2'],
      );
      expect(ids(buckets)).toEqual([
        ['This week (+ 1 hidden)', ['e1']],
        ['Next week', ['e3']],
      ]);
    });
  });
});
