import * as s from './CalendarPage.css';
import * as c from '../styles/common.css';
import { useLocalStorageState } from '../hooks/useLocalStorageState';
import clsx from 'clsx';
import { useScreenSize } from '../hooks/useScreenSize';
import { useSpotifyStatus } from '../hooks/useSpotifyStatus';

import { useConnectSpotify } from '../hooks/useConnectSpotify';
import { useManualInterests } from '../hooks/useManualInterests';

import { useAuth } from '../auth/useAuth';
import { CalendarEventsAllCity } from '../components/CalendarEventsAllCity';
import { CalendarEventsUser } from '../components/CalendarEventsUser';
import { InfoIcon } from '../components/InfoIcon';

const DISPLAY_OPTIONS = ['Full', 'Condensed'] as const;
type DisplayStyle = (typeof DISPLAY_OPTIONS)[number];

export default function CalendarPage() {
  const { isPhoneWidth } = useScreenSize();
  const { user } = useAuth();
  const { state: persistedDisplayStyle, actions: displayStyleActions } =
    useLocalStorageState<DisplayStyle>('calendar.displayStyle');
  const displayStyle = persistedDisplayStyle ?? DISPLAY_OPTIONS[0];
  const effectiveDisplayStyle: DisplayStyle = isPhoneWidth ? 'Full' : displayStyle;
  const connectSpotifyMut = useConnectSpotify();
  const spotifyQ = useSpotifyStatus();
  const interestsQ = useManualInterests();

  // Pending, not `data === undefined`: a failed gate query never gets data, and
  // waiting on data would leave the page spinning forever. Optional chaining
  // then keeps a failed gate on the matched calendar rather than the city list.
  const gatePending = spotifyQ.isPending || interestsQ.isPending;
  const showAllForCity =
    !gatePending &&
    spotifyQ.data?.connected === false &&
    interestsQ.data?.length === 0 &&
    !!user?.city_id;

  return (
    <div>
      <div className={c.pageHeader}>
        <h1 className={c.pageTitle}>
          {showAllForCity ? "What's happening in Seattle" : 'Your Seattle calendar'}
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

      {showAllForCity && (
        <div className={s.banner}>
          <InfoIcon />
          <div>
            Showing all events in Seattle.{' '}
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
        </div>
      )}
      {showAllForCity ? (
        <CalendarEventsAllCity displayStyle={effectiveDisplayStyle} gatePending={gatePending} />
      ) : (
        <CalendarEventsUser
          onSpotifyConnect={() => {
            connectSpotifyMut.mutate();
          }}
          spotifyConnected={!!spotifyQ.data?.connected}
          displayStyle={effectiveDisplayStyle}
          gatePending={gatePending}
        />
      )}
    </div>
  );
}
