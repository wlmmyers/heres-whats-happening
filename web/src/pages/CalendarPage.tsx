import * as s from './CalendarPage.css';
import * as c from '../styles/common.css';
import { useSpotifyStatus } from '../hooks/useSpotifyStatus';

import { useConnectSpotify } from '../hooks/useConnectSpotify';
import { useManualInterests } from '../hooks/useManualInterests';

import { useAuth } from '../auth/useAuth';
import { CalendarEventsAllCity } from '../components/CalendarEventsAllCity';
import { CalendarEventsUser } from '../components/CalendarEventsUser';
import GoingWentList from '../components/GoingWentList';
import { InfoIcon } from '../components/InfoIcon';
import { useEffect, useState } from 'react';
import Layout from '../components/Layout';
import SearchDialog from '../components/SearchDialog';
import { useLocalStorageState } from '../hooks/useLocalStorageState';

// const DISPLAY_OPTIONS = ['Full', 'Condensed'] as const;
// type DisplayStyle = (typeof DISPLAY_OPTIONS)[number];

export default function CalendarPage() {
  // const { isPhoneWidth } = useScreenSize();
  const { user } = useAuth();
  // const { state: persistedDisplayStyle, actions: displayStyleActions } =
  //   useLocalStorageState<DisplayStyle>('calendar.displayStyle');
  // const displayStyle = persistedDisplayStyle ?? DISPLAY_OPTIONS[0];
  // const effectiveDisplayStyle: DisplayStyle = isPhoneWidth ? 'Full' : displayStyle;
  const connectSpotifyMut = useConnectSpotify();
  const spotifyQ = useSpotifyStatus();
  const interestsQ = useManualInterests();
  const { state: toggledAllCity, actions: toggledAllCityActions } = useLocalStorageState<
    'true' | 'false'
  >('calendar.toggledAllCity');
  const [searchOpen, setSearchOpen] = useState(false);
  // Pending, not `data === undefined`: a failed gate query never gets data, and
  // waiting on data would leave the page spinning forever. Optional chaining
  // then keeps a failed gate on the matched calendar rather than the city list.
  const gatePending = spotifyQ.isPending || interestsQ.isPending;
  const noInterestsKnown =
    !gatePending &&
    spotifyQ.data?.connected === false &&
    interestsQ.data?.length === 0 &&
    !!user?.city_id;
  const isShowingAllCityCalendar = noInterestsKnown || toggledAllCity === 'true';

  // const displayItems = DISPLAY_OPTIONS.map((opt) => ({
  //   key: opt,
  //   // Active item is left uncolored so it inherits the white text that fill mode
  //   // applies over the blue fill; inactive items keep their muted color.
  //   content: (
  //     <span className={clsx(s.rangeButton, opt !== displayStyle && s.rangeButtonInactive)}>
  //       {opt}
  //     </span>
  //   ),
  // }));

  useEffect(() => {
    const listener = (e: KeyboardEvent) => {
      // Bare-letter shortcut: it must not fire while the user is typing into a
      // field, or every 'c' in a search term toggles the calendar behind the
      // dialog. Pre-existing bug -- the Add Manual Event dialog has it too --
      // but the search box is where users actually hit it.
      const t = e.target as HTMLElement | null;
      if (
        searchOpen ||
        t?.isContentEditable ||
        t?.tagName === 'INPUT' ||
        t?.tagName === 'TEXTAREA'
      ) {
        return;
      }
      if (e.key === 'c') {
        toggledAllCityActions.setValue(toggledAllCity === 'true' ? 'false' : 'true');
      }
    };
    window.addEventListener('keypress', listener);
    return () => {
      window.removeEventListener('keypress', listener);
    };
  }, [toggledAllCity, toggledAllCityActions, searchOpen]);

  return (
    <Layout wide>
      <div>
        {noInterestsKnown && (
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
        <SearchDialog
          open={searchOpen}
          onClose={() => setSearchOpen(false)}
          cityId={user?.city_id}
        />
        <div className={s.calendarWithSidebar}>
          <div className={s.calendarColumn}>
            <div className={c.pageHeader}>
              <h1 className={c.pageTitle}>
                {isShowingAllCityCalendar ? `What's happening in Seattle` : `Your Seattle calendar`}
              </h1>
              <button
                type="button"
                className={c.buttonSecondary}
                onClick={() => setSearchOpen(true)}
                // user.city_id gates useEventSearch too (see useEventSearch's
                // `enabled`): staying disabled here keeps the dialog from ever
                // opening with an undefined city, rather than opening it into a
                // search that silently never runs and reads as "no results".
                disabled={!user?.city_id}
              >
                Search
              </button>
              {/* Hiding this for now
              <div>
                <div className={s.controls}>
                  <span className={s.controlLabel}>Display style:</span>
                  <HorizontalSelector
                    itemStyle="fill"
                    items={displayItems}
                    activeKey={displayStyle}
                    onSelect={(key) => displayStyleActions.setValue(key as DisplayStyle)}
                  />
                </div>
              </div> */}
            </div>
            {isShowingAllCityCalendar ? (
              <CalendarEventsAllCity displayStyle={'Full'} gatePending={gatePending} />
            ) : (
              <CalendarEventsUser
                onSpotifyConnect={() => {
                  connectSpotifyMut.mutate();
                }}
                spotifyConnected={!!spotifyQ.data?.connected}
                displayStyle={'Full'}
                gatePending={gatePending}
              />
            )}
          </div>
          <div className={s.sidebarColumn}>
            <GoingWentList />
          </div>
        </div>
      </div>
    </Layout>
  );
}
