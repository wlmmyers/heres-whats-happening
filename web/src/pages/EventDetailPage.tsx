import { Link, useParams } from 'react-router-dom';
import { useEvent } from '../hooks/useEvent';
import * as s from './EventDetailPage.css';
import * as c from '../styles/common.css';
import { Skeleton } from '../components/Skeleton';
import type { CalendarEvent } from '../api/calendar';

const EventContent = ({ event }: { event: CalendarEvent }) => {
  const date = new Date(event.starts_at);
  const dateLabel = date.toLocaleString(undefined, {
    weekday: 'long',
    month: 'long',
    day: 'numeric',
    year: 'numeric',
    hour: 'numeric',
    minute: '2-digit',
  });
  const matchedBits = [...event.matched_because.performers, ...event.matched_because.genres];
  return (
    <>
      <div className={c.cardTranslucent}>
        <div className={s.detail}>
          {' '}
          {event.image_url && <img src={event.image_url} alt="" className={s.thumbnail} />}
          <div className={s.detailText}>
            <h1 className={s.title}>{event.title}</h1>
            <div className={s.date}>{dateLabel}</div>
            <div className={s.venue}>
              {event.venue.name}
              {event.venue.address && <> · {event.venue.address}</>}
            </div>
            {event.score > 0 && (
              <div className={s.score}>{Math.round(event.score * 100)}% match</div>
            )}
          </div>
        </div>
      </div>

      {matchedBits.length > 0 && (
        <div className={s.matched}>Matched because: {matchedBits.join(', ')}</div>
      )}
      {event.description && (
        <section className={c.section}>
          <h2 className={c.sectionTitle}>From the venue</h2>
          <p className={s.description}>{event.description}</p>
        </section>
      )}
      {event.url && (
        <a href={event.url} target="_blank" rel="noreferrer" className={s.viewEvent}>
          View event
        </a>
      )}
    </>
  );
};

export default function EventDetailPage() {
  const { id } = useParams<{ id: string }>();
  const { data, isError } = useEvent(id);

  return (
    <article>
      <Link to="/calendar/seattle" className={s.backLink}>
        {`< Calendar`}
      </Link>
      {data ? (
        <EventContent event={data} />
      ) : isError ? (
        <div>Event Not Found</div>
      ) : (
        <div className={c.cardTranslucent}>
          <div className={s.detail}>
            <Skeleton />
          </div>
        </div>
      )}
    </article>
  );
}
