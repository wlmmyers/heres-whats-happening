import { Fragment } from 'react';
import { useCalendar } from '../hooks/useCalendar';
import EventCard from '../components/EventCard';
import { useMarkNotInterested } from '../hooks/useMarkNotInterested';
import { SkeletonEventCards } from './SkeletonEventCards';
import * as s from '../pages/CalendarPage.css';
import clsx from 'clsx';
import { LazyList } from './LazyList';
import SectionTitle from './SectionTitle';
import { bucketEventsByWeek } from '../utils/weekBuckets';

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
        {bucketEventsByWeek(events).map(({ label, events: weekEvents }) => (
          <Fragment key={label}>
            <li className={s.sectionTitleListItem}>
              <SectionTitle>{label}</SectionTitle>
            </li>
            {weekEvents.map((event) => (
              <li
                key={event.id}
                className={clsx(s.listItem, {
                  [s.listItemCondensed]: displayStyle === 'Condensed',
                })}
              >
                <EventCard
                  event={event}
                  interactive
                  onNotInterested={(id) => notInterested.mutate(id)}
                />
              </li>
            ))}
          </Fragment>
        ))}
      </ul>
    </LazyList>
  );
};
