import { useCalendar } from '../hooks/useCalendar';
import EventCard from '../components/EventCard';
import { useMarkNotInterested } from '../hooks/useMarkNotInterested';
import { SkeletonEventCards } from './SkeletonEventCards';
import * as s from '../pages/CalendarPage.css';
import clsx from 'clsx';
import { LazyList } from './LazyList';

type Props = {
  gatePending: boolean;
  displayStyle: 'Full' | 'Condensed';
  spotifyConnected: boolean;
  onSpotifyConnect: VoidFunction;
};

export const CalendarEventsUser = ({
  gatePending,
  displayStyle,
  spotifyConnected,
  onSpotifyConnect,
}: Props) => {
  const { data, fetchNextPage, hasNextPage, isLoading, isError } = useCalendar();
  const notInterested = useMarkNotInterested();
  const events = data?.pages.map((p) => p.events).flat() || [];
  const loading = gatePending || isLoading;
  const errored = isError;

  return loading ? (
    <SkeletonEventCards count={5} />
  ) : errored ? (
    <div className={s.errorBox}>Couldn't load your calendar, please try again.</div>
  ) : events.length === 0 ? (
    <div className={s.emptyState}>
      No upcoming matches yet.
      {!spotifyConnected && (
        <>
          <br />
          <br /> Try{' '}
          <a
            href="#"
            onClick={(e) => {
              e.preventDefault();
              onSpotifyConnect();
            }}
            className={s.inlineLink}
          >
            connecting your Spotify
          </a>{' '}
          to supercharge your matches or{' '}
          <a href="/interests" className={s.inlineLink}>
            adding some interests
          </a>{' '}
          manually.
        </>
      )}
    </div>
  ) : (
    <LazyList fetchNextPage={fetchNextPage} hasNextPage={hasNextPage}>
      <ul
        className={clsx(s.list, {
          [s.listCondensed]: displayStyle === 'Condensed',
        })}
      >
        {events.map((e) => (
          <li
            key={e.id}
            className={clsx(s.listItem, {
              [s.listItemCondensed]: displayStyle === 'Condensed',
            })}
          >
            <EventCard event={e} interactive onNotInterested={(id) => notInterested.mutate(id)} />
          </li>
        ))}
      </ul>
    </LazyList>
  );
};
