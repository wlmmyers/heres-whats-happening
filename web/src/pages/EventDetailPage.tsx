import { Link, useParams } from 'react-router-dom';
import { useEvent } from '../hooks/useEvent';
import * as s from './EventDetailPage.css';
import * as c from '../styles/common.css';
import { Skeleton } from '../components/Skeleton';

export default function EventDetailPage() {
  const { id } = useParams<{ id: string }>();
  const { data, isLoading, isError } = useEvent(id);

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
      <Link to="/calendar/seattle" className={s.backLink}>
        {`< Calendar`}
      </Link>

      <div className={c.cardTranslucent}>
        <div className={s.detail}>
          {isLoading ? (
            <Skeleton />
          ) : (
            <>
              {' '}
              {data.image_url && <img src={data.image_url} alt="" className={s.thumbnail} />}
              <div className={s.detailText}>
                <h1 className={s.title}>{data.title}</h1>
                <div className={s.date}>{dateLabel}</div>
                <div className={s.venue}>
                  {data.venue.name}
                  {data.venue.address && <> · {data.venue.address}</>}
                </div>
                <div className={s.score}>{Math.round(data.score * 100)}% match</div>
              </div>
            </>
          )}
        </div>
      </div>

      {matchedBits.length > 0 && (
        <div className={s.matched}>Matched because: {matchedBits.join(', ')}</div>
      )}
      {data.description && (
        <section className={c.section}>
          <h2 className={c.sectionTitle}>From the venue</h2>
          <p className={s.description}>{data.description}</p>
        </section>
      )}
      {data.url && (
        <a href={data.url} target="_blank" rel="noreferrer" className={s.viewEvent}>
          View event
        </a>
      )}
    </article>
  );
}
