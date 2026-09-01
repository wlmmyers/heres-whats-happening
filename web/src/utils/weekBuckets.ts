import type { CalendarEvent } from '../api/calendar';

export type WeekBucket = { label: string; events: CalendarEvent[]; notInterestedCount: number };

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
export function bucketEventsByWeek(
  events: CalendarEvent[],
  notInterestedIds?: string[] | undefined,
): WeekBucket[] {
  const thisWeekStart = startOfWeek(new Date());
  const nextWeekStart = new Date(thisWeekStart);
  nextWeekStart.setDate(nextWeekStart.getDate() + 7);
  const byLabel = new Map<string, WeekBucket>();

  for (const event of events) {
    const eventWeekStart = startOfWeek(new Date(event.starts_at));
    const isNotInterested = notInterestedIds?.includes(event.id);
    const label =
      eventWeekStart.toDateString() === thisWeekStart.toDateString()
        ? 'This week'
        : eventWeekStart.toDateString() === nextWeekStart.toDateString()
          ? 'Next week'
          : eventWeekStart.toLocaleDateString();
    const bucket = byLabel.get(label) ?? { label, events: [], notInterestedCount: 0 };
    if (isNotInterested) {
      bucket.notInterestedCount++;
    } else {
      bucket.events.push(event);
    }
    byLabel.set(label, bucket);
  }

  // Add not interested event counts to bucket labels
  const buckets = [...byLabel.values()];
  for (let i = 0; i < buckets.length; i++) {
    let prevWeeksNICount = 0;
    // Iterate backwards through prior buckets, accumulating not interested event counts for buckets with no events
    for (let p = i - 1; p >= 0; p--) {
      if (buckets[p].events.length > 1) {
        break;
      }
      prevWeeksNICount += buckets[p].notInterestedCount;
    }
    const thisWeekNICount = buckets[i].notInterestedCount;
    if (thisWeekNICount > 0 || prevWeeksNICount > 0) {
      const prevWeekEventCount = i > 0 ? buckets[i - 1].events.length : 0;
      const thisWeekNIText = thisWeekNICount > 0 ? `+ ${thisWeekNICount} hidden` : '';
      // Only show previous week's not interested label text on this week's label if the previous week has no events of its own
      const prevWeeksNIText =
        prevWeekEventCount === 0
          ? prevWeeksNICount > 0
            ? `+ ${prevWeeksNICount} hidden from prev weeks`
            : ''
          : '';
      if (thisWeekNIText && prevWeeksNIText) {
        buckets[i].label += ` (${thisWeekNIText}, ${prevWeeksNIText})`;
      } else if (thisWeekNIText || prevWeeksNIText) {
        buckets[i].label += ` (${thisWeekNIText || prevWeeksNIText})`;
      }
    }
  }

  return buckets;
}
