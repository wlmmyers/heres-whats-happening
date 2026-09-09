import { useId } from 'react';
import { useNavigate } from 'react-router-dom';
import { AnimatePresence, motion } from 'motion/react';
import type { CalendarEvent } from '../api/calendar';
import { useListGoingEvents } from '../hooks/useListGoingEvents';
import { useMarkNotGoing } from '../hooks/useMarkNotGoing';
import { formatEventDate } from '../utils/eventDate';
import * as c from '../styles/common.css';
import * as s from './GoingWentList.css';
import { Skeleton } from './Skeleton';
import clsx from 'clsx';
import { useLocalStorageState } from '../hooks/useLocalStorageState';
import RotatingCaret from './RotatingCaret';

// An event with an end time is over when that end passes; one without, when its
// start does.
function endOf(event: CalendarEvent): Date {
  const end = event.ends_at ? new Date(event.ends_at) : null;
  return end && !Number.isNaN(end.getTime()) ? end : new Date(event.starts_at);
}

// The endpoint returns the whole going list, past shows included, so the two
// halves are picked apart here. The upcoming half keeps the server's order;
// history reads backwards, the show just got home from first.
function splitByTime(events: CalendarEvent[]): {
  upcoming: CalendarEvent[];
  past: CalendarEvent[];
} {
  const now = Date.now();
  const upcoming: CalendarEvent[] = [];
  const past: CalendarEvent[] = [];
  for (const event of events) {
    (endOf(event).getTime() > now ? upcoming : past).push(event);
  }
  past.sort((a, b) => new Date(b.starts_at).getTime() - new Date(a.starts_at).getTime());
  return { upcoming, past };
}

function EventRow({
  event,
  onOpen,
  onRemove,
}: {
  event: CalendarEvent;
  onOpen: () => void;
  onRemove: () => void;
}) {
  return (
    <li className={s.item} onClick={onOpen}>
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
          onRemove();
        }}
      >
        X
      </button>
    </li>
  );
}

export default function GoingList() {
  const navigate = useNavigate();
  const goingEventsQ = useListGoingEvents();
  const { mutate: markNotGoing } = useMarkNotGoing();
  const bodyId = useId();
  const { state: expandedWentList, actions: expandedWentListActions } = useLocalStorageState<
    'true' | 'false'
  >('calendar.expandedWentList');
  const isWentExpanded = expandedWentList === 'true';

  const { upcoming: going, past } = splitByTime(goingEventsQ.data ?? []);

  const openEvent = (event: CalendarEvent) => navigate(`/events/${event.id}`);
  const removeEvent = (event: CalendarEvent) => markNotGoing(event.id);

  return (
    <div className={s.goingWentList}>
      <div className={s.heading}>
        <h2 className={c.sectionTitle}>Upcoming Shows</h2>
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
              <EventRow
                key={event.id}
                event={event}
                onOpen={() => openEvent(event)}
                onRemove={() => removeEvent(event)}
              />
            ))}
          </ul>
        )}
      </div>
      <div className={clsx(s.heading, s.wentHeading)}>
        <button
          type="button"
          className={clsx(c.stripButtonStyles, s.wentToggle)}
          aria-expanded={isWentExpanded}
          aria-controls={bodyId}
          onClick={() => expandedWentListActions.setValue(isWentExpanded ? 'false' : 'true')}
        >
          <h2 className={clsx(c.sectionTitle, s.wentSectionTitle)}>Past Shows</h2>
          <RotatingCaret open={isWentExpanded} className={s.wentCaret} />
        </button>
      </div>
      <div className={clsx(s.innerContainer, { [s.noBorder]: !isWentExpanded })}>
        {/* The id stays mounted so `aria-controls` always resolves, collapsed or not. */}
        <div id={bodyId} className={s.wentBody}>
          <AnimatePresence initial={false}>
            {isWentExpanded && (
              <motion.div
                key="body"
                initial={{ height: 0, opacity: 0 }}
                animate={{ height: 'auto', opacity: 1 }}
                exit={{ height: 0, opacity: 0 }}
                transition={{ type: 'spring', stiffness: 500, damping: 40 }}
              >
                <ul>
                  {past.map((event) => (
                    <EventRow
                      key={event.id}
                      event={event}
                      onOpen={() => openEvent(event)}
                      onRemove={() => removeEvent(event)}
                    />
                  ))}
                </ul>
              </motion.div>
            )}
          </AnimatePresence>
        </div>
      </div>
    </div>
  );
}
