import { useState } from 'react';
import { useMutation, useQuery, useQueryClient, keepPreviousData } from '@tanstack/react-query';
import { getCalendar, type CalendarEvent } from '../api/calendar';
import { markNotInterested } from '../api/notInterested';
import EventCard from '../components/EventCard';
import Spinner from '../components/Spinner';
import clsx from 'clsx';
import * as s from './CalendarPage.css';
import * as c from '../styles/common.css';
import { getSpotifyStatus, startSpotifyConnect } from '../api/spotify';
import { useAuth } from '../auth/useAuth';

function isoDate(d: Date): string {
  return d.toISOString().slice(0, 10);
}

const RANGE_OPTIONS = [
  { months: 1, label: '1 month' },
  { months: 3, label: '3 months' },
  { months: 6, label: '6 months' },
] as const;

export default function CalendarPage() {
  const qc = useQueryClient();
  const { user } = useAuth();
  const [months, setMonths] = useState(3);

  const today = new Date();
  const end = new Date(today.getFullYear(), today.getMonth() + months, today.getDate());
  const from = isoDate(today);
  const to = isoDate(end);

  const calendarKey = ['calendar', user?.id, from, to] as const;

  const { data: spotifyStatus } = useQuery({
    queryKey: ['spotify-status', user?.id],
    queryFn: getSpotifyStatus,
  });

  const connectSpotifyMut = useMutation({
    mutationFn: startSpotifyConnect,
    onSuccess: (authorizeURL) => {
      window.location.assign(authorizeURL);
    },
  });

  const { data, isLoading, isError } = useQuery<CalendarEvent[]>({
    queryKey: calendarKey,
    queryFn: () => getCalendar(from, to),
    placeholderData: keepPreviousData,
  });

  const events = data ?? [];

  const notInterested = useMutation({
    mutationFn: (id: string) => markNotInterested(id),
    onMutate: async (id: string) => {
      await qc.cancelQueries({ queryKey: calendarKey });
      const prev = qc.getQueryData<CalendarEvent[]>(calendarKey);
      qc.setQueryData<CalendarEvent[]>(calendarKey, (old) =>
        (old ?? []).filter((e) => e.id !== id),
      );
      return { prev };
    },
    onError: (_err, _id, ctx) => {
      if (ctx?.prev) qc.setQueryData(calendarKey, ctx.prev);
    },
    onSettled: () => {
      qc.invalidateQueries({ queryKey: ['calendar'] });
    },
  });

  return (
    <div>
      <div className={c.pageHeader}>
        <h1 className={c.pageTitle}>Your matched calendar</h1>
        <div className={s.controls}>
          <span className={s.controlLabel}>Show events for next:</span>
          <div className={s.segment}>
            {RANGE_OPTIONS.map((opt) => {
              const active = opt.months === months;
              return (
                <button
                  key={opt.months}
                  type="button"
                  onClick={() => setMonths(opt.months)}
                  aria-pressed={active}
                  className={clsx(
                    s.rangeButton,
                    active ? s.rangeButtonActive : s.rangeButtonInactive,
                  )}
                >
                  {opt.label}
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
          No upcoming matches yet. <br /> Try{' '}
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
        <ul className={s.list}>
          {events.map((e) => (
            <li key={e.id} className={s.listItem}>
              <EventCard event={e} interactive onNotInterested={(id) => notInterested.mutate(id)} />
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
