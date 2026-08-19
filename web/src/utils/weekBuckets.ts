import type { CalendarEvent } from '../api/calendar';

export type WeekBucket = { label: string; events: CalendarEvent[] };

// The Sunday that opens the week the given date falls in, in local time.
function startOfWeek(date: Date): Date {
  const start = new Date(date);
  start.setDate(start.getDate() - start.getDay());
  return start;
}

// Groups events into the week each one starts in. The week in progress is
// called out by name; the rest are labelled with the date they start on.
//
// Events arrive ordered by start time but ungrouped, and a page boundary can
// land mid-week, so a week can turn up in more than one place in the list —
// hence the lookup rather than a straight walk. Bucket order follows first
// appearance, which keeps the server's ordering.
export function bucketEventsByWeek(events: CalendarEvent[]): WeekBucket[] {
  const thisWeekStart = startOfWeek(new Date());
  const byLabel = new Map<string, WeekBucket>();

  for (const event of events) {
    const weekStart = startOfWeek(new Date(event.starts_at));
    const label =
      weekStart.toDateString() === thisWeekStart.toDateString()
        ? 'This week'
        : weekStart.toLocaleDateString();
    const bucket = byLabel.get(label) ?? { label, events: [] };
    bucket.events.push(event);
    byLabel.set(label, bucket);
  }

  return [...byLabel.values()];
}
