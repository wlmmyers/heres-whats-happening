import { Link } from 'react-router-dom';
import type { CalendarEvent } from '../api/calendar';
import * as s from './EventCard.css';

export default function EventCard({
  event,
  onNotInterested,
}: {
  event: CalendarEvent;
  onNotInterested?: (id: string) => void;
}) {
  const date = new Date(event.starts_at);
  const dateLabel = date.toLocaleString(undefined, {
    weekday: 'short',
    month: 'short',
    day: 'numeric',
    hour: 'numeric',
    minute: '2-digit',
  });
  const matchedBits = [...event.matched_because.performers, ...event.matched_because.genres];

  return (
    <div className={s.card}>
      <Link to={`/events/${event.id}`} className={s.link}>
        <div className={s.titleRow}>
          <h3 className={s.title}>{event.title}</h3>
          <span className={s.score}>
            {Math.round(event.score * 100)}% match
          </span>
        </div>
        <div className={s.date}>{dateLabel} · {event.venue.name}</div>
        {matchedBits.length > 0 && (
          <div className={s.matched}>
            Matched because: {matchedBits.join(', ')}
          </div>
        )}
      </Link>
      {onNotInterested && (
        <div className={s.notInterestedRow}>
          <button
            type="button"
            onClick={() => onNotInterested(event.id)}
            className={s.notInterestedButton}
          >
            Not interested
          </button>
        </div>
      )}
    </div>
  );
}
