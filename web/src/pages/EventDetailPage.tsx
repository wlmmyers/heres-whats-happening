import { useQuery } from '@tanstack/react-query';
import { Link, useParams } from 'react-router-dom';
import { getEvent, type CalendarEvent } from '../api/calendar';
import Spinner from '../components/Spinner';
import * as s from './EventDetailPage.css';

export default function EventDetailPage() {
  const { id } = useParams<{ id: string }>();
  const { data, isLoading, isError } = useQuery<CalendarEvent>({
    queryKey: ['event', id],
    queryFn: () => getEvent(id!),
    enabled: !!id,
  });

  if (isLoading) return <Spinner />;
  if (isError) return <div className={s.notFound}>Event not found.</div>;
  if (!data) return null;

  const date = new Date(data.starts_at);
  const dateLabel = date.toLocaleString(undefined, {
    weekday: 'long',
    month: 'long',
    day: 'numeric',
    year: 'numeric',
    hour: 'numeric',
    minute: '2-digit',
  });
  const matchedBits = [...data.matched_because.performers, ...data.matched_because.genres];

  return (
    <article>
      <Link to="/calendar" className={s.backLink}>
        {`< Calendar`}
      </Link>

      {data.image_url && (
        <img
          src={data.image_url}
          alt={data.title}
          className={s.cover}
        />
      )}

      <header className={s.header}>
        <h1 className={s.title}>{data.title}</h1>
        <div className={s.date}>{dateLabel}</div>
        <div className={s.venue}>
          {data.venue.name}
          {data.venue.address && <> · {data.venue.address}</>}
        </div>
      </header>

      <div className={s.score}>{Math.round(data.score * 100)}% match</div>

      {matchedBits.length > 0 && (
        <div className={s.matched}>
          Matched because: {matchedBits.join(', ')}
        </div>
      )}

      {data.description && <p className={s.description}>{data.description}</p>}

      {data.url && (
        <a
          href={data.url}
          target="_blank"
          rel="noreferrer"
          className={s.viewEvent}
        >
          View event
        </a>
      )}
    </article>
  );
}
