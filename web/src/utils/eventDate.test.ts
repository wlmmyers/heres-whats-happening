import { describe, it, expect, afterEach, vi } from 'vitest';
import { formatEventDate } from './eventDate';

// Tests run pinned to America/Los_Angeles (see vitest.config.ts), so the UTC
// instants below are 1:00 PM / 4:30 PM / 1:00 AM local.
const start = '2026-06-15T20:00:00Z';
const sameDayEnd = '2026-06-15T23:30:00Z';
const nextDayEnd = '2026-06-16T08:00:00Z';

// ICU pads its range patterns with thin and narrow no-break spaces (around the
// en dash, and before AM/PM). Keep that typography in the rendered output, but
// compare against plain spaces so the expectations below stay readable.
const label = (...args: Parameters<typeof formatEventDate>) =>
  formatEventDate(...args).replace(/[\u2009\u202f\u00a0]/g, ' ');

afterEach(() => {
  vi.useRealTimers();
});

describe('formatEventDate', () => {
  it('formats the start alone when the event has no end', () => {
    expect(label({ starts_at: start }, 'short')).toBe('Mon, Jun 15, 1:00 PM');
  });

  it('collapses the date for a range that starts and ends on the same day', () => {
    expect(label({ starts_at: start, ends_at: sameDayEnd }, 'short')).toBe(
      'Mon, Jun 15, 1:00 – 4:30 PM',
    );
  });

  it('repeats the date for a range that runs past midnight', () => {
    expect(label({ starts_at: start, ends_at: nextDayEnd }, 'short')).toBe(
      'Mon, Jun 15, 1:00 PM – Tue, Jun 16, 1:00 AM',
    );
  });

  it('spells the range out in the long style', () => {
    expect(label({ starts_at: start, ends_at: sameDayEnd }, 'long')).toBe(
      'Monday, June 15, 2026, 1:00 – 4:30 PM',
    );
  });

  it('adds the year in the short style for an event in a later year', () => {
    vi.setSystemTime(new Date('2025-06-15T20:00:00Z'));
    expect(label({ starts_at: start, ends_at: sameDayEnd }, 'short')).toBe(
      'Mon, Jun 15, 2026, 1:00 – 4:30 PM',
    );
  });

  it('adds the year in the short style when only the end crosses into a later year', () => {
    vi.setSystemTime(new Date('2026-06-15T20:00:00Z'));
    const newYears = label(
      { starts_at: '2026-12-31T22:00:00-08:00', ends_at: '2027-01-01T02:00:00-08:00' },
      'short',
    );
    expect(newYears).toBe('Thu, Dec 31, 2026, 10:00 PM – Fri, Jan 1, 2027, 2:00 AM');
  });

  // Ends_at is scraped per-source and is not always trustworthy; a bad end
  // should degrade to the start time rather than render "Invalid Date" or a
  // backwards range.
  it('falls back to the start alone when the end is unparseable', () => {
    expect(label({ starts_at: start, ends_at: 'not a date' }, 'short')).toBe(
      'Mon, Jun 15, 1:00 PM',
    );
  });

  it('falls back to the start alone when the end precedes the start', () => {
    expect(label({ starts_at: start, ends_at: '2026-06-15T18:00:00Z' }, 'short')).toBe(
      'Mon, Jun 15, 1:00 PM',
    );
  });

  it('falls back to the start alone when the end equals the start', () => {
    expect(label({ starts_at: start, ends_at: start }, 'short')).toBe('Mon, Jun 15, 1:00 PM');
  });
});
