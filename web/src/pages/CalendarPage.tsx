import { useState } from 'react';
import { useMutation, useQuery, useQueryClient, keepPreviousData } from '@tanstack/react-query';
import { getCalendar, type CalendarEvent } from '../api/calendar';
import { markNotInterested } from '../api/notInterested';
import EventCard from '../components/EventCard';
import Spinner from '../components/Spinner';

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
  const [months, setMonths] = useState(3);

  const today = new Date();
  const end = new Date(today.getFullYear(), today.getMonth() + months, today.getDate());
  const from = isoDate(today);
  const to = isoDate(end);

  const { data, isLoading, isError } = useQuery<CalendarEvent[]>({
    queryKey: ['calendar', from, to],
    queryFn: () => getCalendar(from, to),
    placeholderData: keepPreviousData,
  });

  const events = data ?? [];

  const notInterested = useMutation({
    mutationFn: (id: string) => markNotInterested(id),
    onMutate: async (id: string) => {
      await qc.cancelQueries({ queryKey: ['calendar', from, to] });
      const prev = qc.getQueryData<CalendarEvent[]>(['calendar', from, to]);
      qc.setQueryData<CalendarEvent[]>(['calendar', from, to], (old) =>
        (old ?? []).filter((e) => e.id !== id),
      );
      return { prev };
    },
    onError: (_err, _id, ctx) => {
      if (ctx?.prev) qc.setQueryData(['calendar', from, to], ctx.prev);
    },
    onSettled: () => {
      qc.invalidateQueries({ queryKey: ['calendar'] });
    },
  });

  return (
    <div className="space-y-4">
      <header className="flex flex-wrap items-baseline justify-between gap-3">
        <h1 className="text-2xl font-semibold">Your matched calendar</h1>
        <div className="flex items-center gap-2">
          <span className="text-sm text-gray-500">Show events for next:</span>
          <div className="inline-flex p-0.5">
            {RANGE_OPTIONS.map((opt) => {
              const active = opt.months === months;
              return (
                <button
                  key={opt.months}
                  type="button"
                  onClick={() => setMonths(opt.months)}
                  aria-pressed={active}
                  className={
                    'rounded-md px-3 py-1 text-sm font-medium transition whitespace-nowrap ' +
                    (active
                      ? 'bg-blue-600 text-white shadow-sm'
                      : 'text-gray-600 hover:text-gray-900')
                  }
                >
                  {opt.label}
                </button>
              );
            })}
          </div>
        </div>
      </header>

      {isLoading ? (
        <Spinner />
      ) : isError ? (
        <div className="text-red-600">Couldn't load your calendar.</div>
      ) : events.length === 0 ? (
        <div className="bg-white shadow rounded p-8 text-center text-gray-600">
          No upcoming matches yet. Add some interests on the{' '}
          <a href="/onboarding" className="text-blue-600 underline">
            Interests
          </a>{' '}
          page or wait for the next match run.
        </div>
      ) : (
        <ul className="space-y-3">
          {events.map((e) => (
            <li key={e.id}>
              <EventCard event={e} onNotInterested={(id) => notInterested.mutate(id)} />
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
