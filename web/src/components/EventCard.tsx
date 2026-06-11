import { Link } from 'react-router-dom';
import type { CalendarEvent } from '../api/calendar';

export default function EventCard({
  event,
  onNotInterested,
}: {
  event: CalendarEvent;
  onNotInterested?: (id: string) => void;
}) {
  const date = new Date(event.starts_at);
  const dateLabel = date.toLocaleString(undefined, {
    weekday: 'short',
    month: 'short',
    day: 'numeric',
    hour: 'numeric',
    minute: '2-digit',
  });
  const matchedBits = [...event.matched_because.performers, ...event.matched_because.genres];

  return (
    <div className="bg-white shadow rounded p-4 hover:shadow-md transition">
      <Link to={`/events/${event.id}`} className="block">
        <div className="flex items-baseline justify-between gap-4">
          <h3 className="text-lg font-semibold text-gray-900">{event.title}</h3>
          <span className="text-sm text-gray-500 whitespace-nowrap">
            {Math.round(event.score * 100)}% match
          </span>
        </div>
        <div className="text-sm text-gray-700 mt-1">{dateLabel} · {event.venue.name}</div>
        {matchedBits.length > 0 && (
          <div className="text-xs text-blue-700 mt-2">
            Matched because: {matchedBits.join(', ')}
          </div>
        )}
      </Link>
      {onNotInterested && (
        <div className="mt-3 flex justify-end">
          <button
            type="button"
            onClick={() => onNotInterested(event.id)}
            className="text-sm text-gray-500 hover:text-red-600"
          >
            Not interested
          </button>
        </div>
      )}
    </div>
  );
}
