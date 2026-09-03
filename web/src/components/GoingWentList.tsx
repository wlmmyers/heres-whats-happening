import { useNavigate } from 'react-router-dom';
import type { CalendarEvent } from '../api/calendar';
import { useListGoingEvents } from '../hooks/useListGoingEvents';
import { useMarkNotGoing } from '../hooks/useMarkNotGoing';
import { formatEventDate } from '../utils/eventDate';
import * as c from '../styles/common.css';
import * as s from './GoingWentList.css';
import { Skeleton } from './Skeleton';

// The endpoint returns the whole going list, past shows included, so the
// upcoming half is picked out here. An event with an end time counts as
// upcoming until it is over; one without, until its start passes.
function upcomingEvents(events: CalendarEvent[]): CalendarEvent[] {
  const now = Date.now();
  return events.filter((event) => {
    const end = event.ends_at ? new Date(event.ends_at) : null;
    const over = end && !Number.isNaN(end.getTime()) ? end : new Date(event.starts_at);
    return over.getTime() > now;
  });
}

export default function GoingList() {
  const navigate = useNavigate();
  const goingEventsQ = useListGoingEvents();
  const { mutate: markNotGoing } = useMarkNotGoing();

  const going = upcomingEvents(goingEventsQ.data ?? []);

  return (
    <div className={s.goingWentList}>
      <div className={s.heading}>
        <h2 className={c.sectionTitle}>Your Shows</h2>
        {going.length > 0 && (
          <span className={s.count}>
            {going.length} upcoming {going.length === 1 ? 'show' : 'shows'}
          </span>
        )}
      </div>
      <div className={s.innerContainer}>
        {!goingEventsQ.isFetched ? (
          <>
            <Skeleton className={s.skeletonRow} />
            <Skeleton className={s.skeletonRow} />
            <Skeleton className={s.skeletonRow} />
          </>
        ) : goingEventsQ.isError ? (
          <div className={s.errorText}>Couldn't load your going list.</div>
        ) : going.length === 0 ? (
          <div className={s.emptyText}>
            No upcoming shows yet. <br />
            <aside className={s.helperText}>
              Click <i style={{ fontWeight: 'bold' }}>I'm going</i> on shows to see them here.
            </aside>
          </div>
        ) : (
          <ul>
            {going.map((event) => (
              <li key={event.id} className={s.item} onClick={() => navigate(`/events/${event.id}`)}>
                <div className={s.itemMain}>
                  <div className={s.itemDate}>{formatEventDate(event, 'short')}</div>
                  <div className={s.itemTitle}>{event.title}</div>
                  <div className={s.itemVenue}>{event.venue.name}</div>
                </div>
                <button
                  type="button"
                  aria-label={`Remove ${event.title} from your going list`}
                  className={s.removeButton}
                  onClick={(e) => {
                    e.stopPropagation();
                    markNotGoing(event.id);
                  }}
                >
                  X
                </button>
              </li>
            ))}
          </ul>
        )}
      </div>
    </div>
  );
}
