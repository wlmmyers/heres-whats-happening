import { useQuery } from '@tanstack/react-query';
import { getCalendar, type CalendarEvent } from '../api/calendar';
import EventCard from '../components/EventCard';
import Spinner from '../components/Spinner';

function isoDate(d: Date): string {
  return d.toISOString().slice(0, 10);
}

export default function CalendarPage() {
  const today = new Date();
  const sixtyOut = new Date(today.getTime() + 60 * 24 * 60 * 60 * 1000);
  const from = isoDate(today);
  const to = isoDate(sixtyOut);

  const { data, isLoading, isError } = useQuery<CalendarEvent[]>({
    queryKey: ['calendar', from, to],
    queryFn: () => getCalendar(from, to),
  });

  if (isLoading) return <Spinner />;
  if (isError) return <div className="text-red-600">Couldn't load your calendar.</div>;

  const events = data ?? [];

  return (
    <div className="space-y-4">
      <header className="flex items-baseline justify-between">
        <h1 className="text-2xl font-semibold">Your matched calendar</h1>
        <span className="text-sm text-gray-500">{from} → {to}</span>
      </header>

      {events.length === 0 ? (
        <div className="bg-white shadow rounded p-8 text-center text-gray-600">
          No upcoming matches yet. Add some interests on the{' '}
          <a href="/onboarding" className="text-blue-600 underline">Interests</a> page
          or wait for the next match run.
        </div>
      ) : (
        <ul className="space-y-3">
          {events.map((e) => (
            <li key={e.id}>
              <EventCard event={e} />
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
