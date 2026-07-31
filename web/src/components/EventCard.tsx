import { useNavigate } from 'react-router-dom';
import type { CalendarEvent } from '../api/calendar';
import * as s from './EventCard.css';
import clsx from 'clsx';

export default function EventCard({
  event,
  onNotInterested,
  interactive = true,
  shorterMinHeight,
}: {
  event: CalendarEvent;
  onNotInterested?: (id: string) => void;
  interactive?: boolean;
  shorterMinHeight?: boolean;
}) {
  const navigate = useNavigate();
  const date = new Date(event.starts_at);
  const dateLabel = date.toLocaleString(undefined, {
    weekday: 'short',
    month: 'short',
    day: 'numeric',
    hour: 'numeric',
    minute: '2-digit',
    ...(date.getFullYear() > new Date().getFullYear() && { year: 'numeric' }),
  });
  const matchedBits = [...event.matched_because.performers, ...event.matched_because.genres];
  return (
    <div
      className={clsx(s.eventCard, { [s.shorterMinHeight]: shorterMinHeight })}
      onClick={interactive ? () => navigate(`/events/${event.id}`) : undefined}
    >
      {event.image_url && (
        <img src={event.image_url} alt="" data-thumbnail className={s.thumbnail} />
      )}
      <div className={s.main}>
        <h3 className={s.title}>{event.title}</h3>
        <div className={s.date}>
          {dateLabel} · {event.venue.name}
        </div>
        {matchedBits.length > 0 && (
          <div className={s.matched}>Matched because: {matchedBits.join(', ')}</div>
        )}
      </div>
      {/* City-wide events carry no match, and "0% match" reads as a bad
          match rather than as no match at all. */}
      {event.score > 0 && <span className={s.score}>{Math.round(event.score * 100)}% match</span>}
      {onNotInterested && (
        <div className={s.notInterested}>
          <button
            type="button"
            onClick={(e) => {
              e.stopPropagation();
              onNotInterested(event.id);
            }}
            className={s.notInterestedButton}
          >
            Not interested
          </button>
        </div>
      )}
    </div>
  );
}
