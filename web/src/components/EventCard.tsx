import { useNavigate } from 'react-router-dom';
import type { CalendarEvent } from '../api/calendar';
import { formatEventDate } from '../utils/eventDate';
import * as s from './EventCard.css';
import * as c from '../styles/common.css';
import clsx from 'clsx';
import ArtistImage from './ArtistImage';
import { useState } from 'react';
import { useMarkGoing } from '../hooks/useMarkGoing';
import { useMarkNotGoing } from '../hooks/useMarkNotGoing';
import { useListGoing } from '../hooks/useListGoing';

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
  const isGoingEventList = useListGoing();
  const [isGoing, setIsGoing] = useState(false);
  if (isGoingEventList.isSuccess) {
    const dbIsGoing = isGoingEventList.data?.includes(event.id);
    if (isGoing !== dbIsGoing) {
      setIsGoing(dbIsGoing);
    }
  }
  const { mutate: markEventGoing } = useMarkGoing();
  const { mutate: markNotGoing } = useMarkNotGoing();
  const handleGoingClick = () => {
    if (!isGoing) {
      markEventGoing(event.id);
    } else {
      markNotGoing(event.id);
    }
    setIsGoing(!isGoing);
  };
  const dateLabel = formatEventDate(event, 'short');
  const matchedBits = [...event.matched_because.performers, ...event.matched_because.genres];

  return (
    <div
      className={clsx(s.eventCard, { [s.shorterMinHeight]: shorterMinHeight })}
      onClick={interactive ? () => navigate(`/events/${event.id}`) : undefined}
    >
      <ArtistImage event={event} className={s.thumbnail} />
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
      <div className={s.actions}>
        {onNotInterested && (
          <button
            type="button"
            disabled={isGoing}
            onClick={(e) => {
              e.stopPropagation();
              onNotInterested(event.id);
            }}
            className={c.actionButton}
          >
            Hide
          </button>
        )}
        <button
          type="button"
          onClick={(e) => {
            e.stopPropagation();
            handleGoingClick();
          }}
          className={clsx(c.actionButton, c.goingButton, {
            [c.isGoing]: isGoing,
          })}
        >
          {isGoing ? 'Going' : "I'm going!"}
        </button>
      </div>
    </div>
  );
}
