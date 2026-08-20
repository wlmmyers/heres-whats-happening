import { Link, useParams } from 'react-router-dom';
import { useEvent } from '../hooks/useEvent';
import { useMe } from '../hooks/useMe';
import * as s from './EventDetailPage.css';
import * as c from '../styles/common.css';
import { Skeleton } from '../components/Skeleton';
import type { CalendarEvent } from '../api/calendar';
import { formatEventDate } from '../utils/eventDate';
import ArtistImage from '../components/ArtistImage';
import ExternalLink from '../components/ExternalLink';
import CollapsableSection from '../components/CollapsableSection';

const EventContent = ({ event }: { event: CalendarEvent }) => {
  const dateLabel = formatEventDate(event, 'long');
  const matchedBits = [...event.matched_because.performers, ...event.matched_because.genres];
  const hasSetlist = !!(
    event.artist?.tour?.setlist_url || (event.artist?.tour?.songs?.length ?? 0) > 0
  );
  const { data: me } = useMe();
  // Setlists are spoilers, so they stay hidden unless the user has opted in.
  // An unresolved /me counts as opted out, so the songs never flash into view
  // in the window before the preference loads.
  const songs = event.artist?.tour?.songs ?? [];
  const hideSetlist = !me?.show_setlists && songs.length > 0;
  return (
    <>
      <div className={c.cardTranslucent}>
        <div className={s.detail}>
          {' '}
          <ArtistImage event={event} className={s.thumbnail} />
          <div className={s.detailText}>
            <h1 className={s.title}>{event.title}</h1>
            <div className={s.date}>{dateLabel}</div>
            <div className={s.venue}>
              {event.venue.name}
              {event.venue.address && <> · {event.venue.address}</>}
            </div>
            {event.score > 0 && (
              <div className={s.matched}>
                {Math.round(event.score * 100)}% match
                {matchedBits.length > 0 && <> - matched because: {matchedBits.join(', ')}</>}
              </div>
            )}
          </div>
        </div>
      </div>
      {event.url && (
        <div className={s.viewEventSection}>
          <a href={event.url} target="_blank" rel="noreferrer" className={s.viewEventLink}>
            Buy tickets
          </a>
        </div>
      )}
      {event.artist?.bio && (
        <CollapsableSection title="About the artist">
          <p>{event.artist.bio.text}</p>
        </CollapsableSection>
      )}
      {event.artist?.tour && (
        <CollapsableSection title="Tour info">
          <p>{event.artist.tour.blurb}</p>
          {hasSetlist && (
            <>
              <h2 className={s.setlistTitle}>Setlist</h2>
              {event.artist.tour.observed && (
                <div className={s.setlistObserved}>
                  Observed on {event.artist.tour.observed.date} at{' '}
                  {event.artist.tour.observed.venue}
                  {event.artist.tour.observed.city && ` in ${event.artist.tour.observed.city}`}
                </div>
              )}
              <div className={hideSetlist ? s.setlistGuard : undefined}>
                <div className={`${s.setlistInset} ${hideSetlist ? s.setlistHidden : ''}`.trim()}>
                  {songs.length > 0 && (
                    <ol className={s.setlistSongList} aria-hidden={hideSetlist || undefined}>
                      {songs.map((song) => (
                        <li key={song.name}>{song.name}</li>
                      ))}
                    </ol>
                  )}
                </div>
                {hideSetlist && (
                  <div className={s.setlistOverlay}>
                    <span className={s.setlistOverlayText}>Setlist hidden to avoid spoilers</span>
                    <Link to="/settings" className={s.setlistOverlayLink}>
                      Show setlists in Settings
                    </Link>
                  </div>
                )}
              </div>
              {event.artist.tour.setlist_url && (
                <ExternalLink href={event.artist.tour.setlist_url} className={s.setlistLink}>
                  View on setlist.fm
                </ExternalLink>
              )}
            </>
          )}
        </CollapsableSection>
      )}
      {event.description && (
        <CollapsableSection title="From the venue" defaultOpen={false}>
          <p>{event.description}</p>
        </CollapsableSection>
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
