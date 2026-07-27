import { useNavigate } from 'react-router-dom';
import type { CalendarEvent } from '../api/calendar';
import * as s from './EventCard.css';

export default function EventCard({
  event,
  onNotInterested,
  interactive = true,
}: {
  event: CalendarEvent;
  onNotInterested?: (id: string) => void;
  interactive?: boolean;
}) {
  const navigate = useNavigate();
  const date = new Date(event.starts_at);
  const dateLabel = date.toLocaleString(undefined, {
    weekday: 'short',
    month: 'short',
    day: 'numeric',
    hour: 'numeric',
    minute: '2-digit',
  });
  const matchedBits = [...event.matched_because.performers, ...event.matched_because.genres];

  const content = (
    <>
      {event.image_url && <img src={event.image_url} alt="" className={s.thumbnail} />}
      <div className={s.body}>
        <div className={s.titleRow}>
          <h3 className={s.title}>{event.title}</h3>
          <span className={s.score}>{Math.round(event.score * 100)}% match</span>
        </div>
        <div className={s.date}>
          {dateLabel} · {event.venue.name}
        </div>
        {matchedBits.length > 0 && (
          <div className={s.matched}>Matched because: {matchedBits.join(', ')}</div>
        )}
      </div>
    </>
  );

  return (
    <div
      className={s.eventCard}
      onClick={interactive ? () => navigate(`/events/${event.id}`) : undefined}
    >
      <div className={s.link}>{content}</div>
      {onNotInterested && (
        <div className={s.notInterestedRow}>
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
