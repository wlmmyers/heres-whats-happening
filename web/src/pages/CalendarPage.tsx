import EventCard from '../components/EventCard';
import Spinner from '../components/Spinner';
import * as s from './CalendarPage.css';
import * as c from '../styles/common.css';
import { useLocalStorageState } from '../hooks/useLocalStorageState';
import clsx from 'clsx';
import { useScreenSize } from '../hooks/useScreenSize';
import { useSpotifyStatus } from '../hooks/useSpotifyStatus';
import { useCalendar } from '../hooks/useCalendar';
import { useConnectSpotify } from '../hooks/useConnectSpotify';
import { useMarkNotInterested } from '../hooks/useMarkNotInterested';

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
  const { state: persistedDisplayStyle, actions: displayStyleActions } =
    useLocalStorageState<DisplayStyle>('calendar.displayStyle');
  const displayStyle = persistedDisplayStyle ?? DISPLAY_OPTIONS[0];
  const effectiveDisplayStyle: DisplayStyle = isPhoneWidth ? 'Full' : displayStyle;
  const today = new Date();
  const end = new Date(today.getFullYear(), today.getMonth() + 6, today.getDate());
  const from = isoDate(today);
  const to = isoDate(end);

  const { data: spotifyStatus } = useSpotifyStatus();

  const connectSpotifyMut = useConnectSpotify();

  const { data, isLoading, isError } = useCalendar(from, to);

  const events = data ?? [];

  const notInterested = useMarkNotInterested(from, to);

  return (
    <div>
      <div className={c.pageHeader}>
        <h1 className={c.pageTitle}>Your Seattle calendar</h1>
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

      {isLoading ? (
        <Spinner />
      ) : isError ? (
        <div className={s.errorBox}>Couldn't load your calendar.</div>
      ) : events.length === 0 ? (
        <div className={s.emptyState}>
          No upcoming matches yet. <br />
          <br /> Try{' '}
          {spotifyStatus && !spotifyStatus.connected && (
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
              <EventCard event={e} interactive onNotInterested={(id) => notInterested.mutate(id)} />
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
