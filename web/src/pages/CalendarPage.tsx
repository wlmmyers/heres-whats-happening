import EventCard from '../components/EventCard';
import * as s from './CalendarPage.css';
import * as c from '../styles/common.css';
import { useLocalStorageState } from '../hooks/useLocalStorageState';
import clsx from 'clsx';
import { useScreenSize } from '../hooks/useScreenSize';
import { useSpotifyStatus } from '../hooks/useSpotifyStatus';
import { useCalendar } from '../hooks/useCalendar';
import { useConnectSpotify } from '../hooks/useConnectSpotify';
import { useMarkNotInterested } from '../hooks/useMarkNotInterested';
import SkeletonCard from '../components/SkeletonCard';
import { useManualInterests } from '../hooks/useManualInterests';
import { useCityCalendar } from '../hooks/useCityCalendar';
import { useAuth } from '../auth/useAuth';

// Local calendar date, not the UTC one. toISOString() would roll `from` forward
// to tomorrow every evening once local time passes midnight UTC, hiding the rest
// of today's events.
function isoDate(d: Date): string {
  const month = String(d.getMonth() + 1).padStart(2, '0');
  const day = String(d.getDate()).padStart(2, '0');
  return `${d.getFullYear()}-${month}-${day}`;
}

const DISPLAY_OPTIONS = ['Full', 'Condensed'] as const;
type DisplayStyle = (typeof DISPLAY_OPTIONS)[number];

export default function CalendarPage() {
  const { isPhoneWidth } = useScreenSize();
  const { user } = useAuth();
  const { state: persistedDisplayStyle, actions: displayStyleActions } =
    useLocalStorageState<DisplayStyle>('calendar.displayStyle');
  const displayStyle = persistedDisplayStyle ?? DISPLAY_OPTIONS[0];
  const effectiveDisplayStyle: DisplayStyle = isPhoneWidth ? 'Full' : displayStyle;
  const today = new Date();
  const end = new Date(today.getFullYear(), today.getMonth() + 6, today.getDate());
  const from = isoDate(today);
  const to = isoDate(end);

  const connectSpotifyMut = useConnectSpotify();
  const spotifyQ = useSpotifyStatus();
  const interestsQ = useManualInterests();

  // Pending, not `data === undefined`: a failed gate query never gets data, and
  // waiting on data would leave the page spinning forever. Optional chaining
  // then keeps a failed gate on the matched calendar rather than the city list.
  const gatePending = spotifyQ.isPending || interestsQ.isPending;
  const showCity =
    !gatePending && spotifyQ.data?.connected === false && interestsQ.data?.length === 0;

  const cityQ = useCityCalendar(user?.city_id, from, to, showCity);

  const { data, isLoading, isError } = useCalendar(from, to);

  const events = showCity ? (cityQ.data ?? []) : (data ?? []);
  const loading = gatePending || (showCity ? cityQ.isLoading : isLoading);
  const errored = showCity ? cityQ.isError : isError;

  const notInterested = useMarkNotInterested(from, to);

  return (
    <div>
      <div className={c.pageHeader}>
        <h1 className={c.pageTitle}>
          {showCity ? 'Everything happening in Seattle' : 'Your Seattle calendar'}
        </h1>
        <div className={s.controls}>
          <span className={s.controlLabel}>Display style:</span>
          <div className={s.segment}>
            {DISPLAY_OPTIONS.map((opt) => {
              const active = opt === displayStyle;
              return (
                <button
                  key={opt}
                  type="button"
                  onClick={() => displayStyleActions.setValue(opt)}
                  aria-pressed={active}
                  className={clsx(
                    s.rangeButton,
                    active ? s.rangeButtonActive : s.rangeButtonInactive,
                  )}
                >
                  {opt}
                </button>
              );
            })}
          </div>
        </div>
      </div>

      {showCity && (
        <div className={s.banner}>
          Showing everything in Seattle.{' '}
          <a
            href="#"
            onClick={(e) => {
              e.preventDefault();
              connectSpotifyMut.mutate();
            }}
            className={s.inlineLink}
          >
            Connect your Spotify
          </a>{' '}
          or{' '}
          <a href="/interests" className={s.inlineLink}>
            add some interests
          </a>{' '}
          to get a calendar matched to your taste.
        </div>
      )}

      {loading ? (
        <ul className={s.list}>
          {Array.from({ length: 5 }, (_, i) => ({ id: `loading-${i}` })).map((e) => (
            <li key={e.id} className={s.listItem}>
              <SkeletonCard height={150} />
            </li>
          ))}
        </ul>
      ) : errored ? (
        <div className={s.errorBox}>Couldn't load your calendar.</div>
      ) : events.length === 0 ? (
        showCity ? (
          <div className={s.emptyState}>Nothing on the calendar in Seattle right now.</div>
        ) : (
          <div className={s.emptyState}>
            No upcoming matches yet. <br />
            <br /> Try{' '}
            {spotifyQ.data && !spotifyQ.data.connected && (
              <>
                <a
                  href="#"
                  onClick={(e) => {
                    e.preventDefault();
                    connectSpotifyMut.mutate();
                  }}
                  className={s.inlineLink}
                >
                  connecting your Spotify
                </a>{' '}
                to supercharge your matches or{' '}
              </>
            )}
            <a href="/interests" className={s.inlineLink}>
              adding some interests
            </a>{' '}
            manually.
          </div>
        )
      ) : (
        <ul
          className={clsx(s.list, {
            [s.listCondensed]: effectiveDisplayStyle === 'Condensed',
          })}
        >
          {events.map((e) => (
            <li
              key={e.id}
              className={clsx(s.listItem, {
                [s.listItemCondensed]: effectiveDisplayStyle === 'Condensed',
              })}
            >
              <EventCard
                event={e}
                interactive
                onNotInterested={showCity ? undefined : (id) => notInterested.mutate(id)}
              />
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
