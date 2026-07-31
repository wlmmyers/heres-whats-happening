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
