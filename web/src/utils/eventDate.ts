export type EventDateStyle = 'short' | 'long';

const baseOptions: Record<EventDateStyle, Intl.DateTimeFormatOptions> = {
  // Cards are tight, so the year is added only when it isn't obvious.
  short: {
    weekday: 'short',
    month: 'short',
    day: 'numeric',
    hour: 'numeric',
    minute: '2-digit',
  },
  long: {
    weekday: 'long',
    month: 'long',
    day: 'numeric',
    year: 'numeric',
    hour: 'numeric',
    minute: '2-digit',
  },
};

/**
 * Renders an event's time for display: a range when the event has a usable
 * `ends_at`, the start alone otherwise. `Intl.formatRange` handles the
 * collapsing — a same-day range keeps one date ("Mon, Jun 15, 1:00 – 4:30 PM"),
 * one that runs past midnight spells out both ends.
 *
 * `ends_at` is scraped per source and is not always trustworthy, so an end that
 * is unparseable or not after the start degrades to the start time rather than
 * rendering "Invalid Date" or a backwards range.
 */
export function formatEventDate(
  event: { starts_at: string; ends_at?: string },
  style: EventDateStyle,
): string {
  const start = new Date(event.starts_at);
  const end = event.ends_at ? new Date(event.ends_at) : null;
  const hasRange = end !== null && !Number.isNaN(end.getTime()) && end > start;

  const options = { ...baseOptions[style] };
  if (style === 'short') {
    const thisYear = new Date().getFullYear();
    const latest = hasRange ? end : start;
    if (latest.getFullYear() > thisYear) options.year = 'numeric';
  }

  const format = new Intl.DateTimeFormat(undefined, options);
  return hasRange ? format.formatRange(start, end) : format.format(start);
}

const DATE_ONLY = /^(\d{4})-(\d{2})-(\d{2})$/;

const manualDateOptions: Intl.DateTimeFormatOptions = {
  weekday: 'short',
  month: 'short',
  day: 'numeric',
};

/**
 * Reads a date-only string ("2026-03-10") as midnight in the viewer's own
 * timezone.
 *
 * `new Date('2026-03-10')` would parse it as UTC midnight, which is the
 * evening of the 9th anywhere west of Greenwich — enough to render a manually
 * added show on the wrong day, sort it against the wrong neighbours, and file
 * it under Past when it hasn't happened yet.
 *
 * Returns an Invalid Date for anything that isn't a real calendar day. The
 * component-wise check is what catches an overflowing day: `new Date(2026, 1,
 * 30)` silently rolls forward to March 2nd rather than refusing.
 */
export function parseLocalDate(value: string): Date {
  const match = DATE_ONLY.exec(value.trim());
  if (!match) return new Date(NaN);
  const year = Number(match[1]);
  const month = Number(match[2]);
  const day = Number(match[3]);
  const date = new Date(year, month - 1, day);
  if (date.getFullYear() !== year || date.getMonth() !== month - 1 || date.getDate() !== day) {
    return new Date(NaN);
  }
  return date;
}

/**
 * The last instant of the day a date falls on. A date-only event has no end
 * time of its own, so this stands in as one: a show today stays under Upcoming
 * until the day is actually over, rather than moving to Past at 00:00.
 */
export function endOfLocalDay(date: Date): Date {
  if (Number.isNaN(date.getTime())) return date;
  const end = new Date(date);
  end.setHours(23, 59, 59, 999);
  return end;
}

/**
 * Renders a manually added show's day — no time, because the user never
 * entered one. The year is appended whenever the show falls outside the
 * current one; unlike the scraped calendar, this list is mostly history, so
 * "Sat, Aug 3" alone would be ambiguous.
 *
 * An unparseable value renders as itself rather than as "Invalid Date".
 */
export function formatManualEventDate(value: string): string {
  const date = parseLocalDate(value);
  if (Number.isNaN(date.getTime())) return value;
  const options = { ...manualDateOptions };
  if (date.getFullYear() !== new Date().getFullYear()) options.year = 'numeric';
  return new Intl.DateTimeFormat(undefined, options).format(date);
}
