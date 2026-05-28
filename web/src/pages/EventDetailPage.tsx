import { useQuery } from '@tanstack/react-query';
import { Link, useParams } from 'react-router-dom';
import { getEvent, type CalendarEvent } from '../api/calendar';
import Spinner from '../components/Spinner';

export default function EventDetailPage() {
  const { id } = useParams<{ id: string }>();
  const { data, isLoading, isError } = useQuery<CalendarEvent>({
    queryKey: ['event', id],
    queryFn: () => getEvent(id!),
    enabled: !!id,
  });

  if (isLoading) return <Spinner />;
  if (isError) return <div className="text-gray-700">Event not found.</div>;
  if (!data) return null;

  const date = new Date(data.starts_at);
  const dateLabel = date.toLocaleString(undefined, {
    weekday: 'long',
    month: 'long',
    day: 'numeric',
    year: 'numeric',
    hour: 'numeric',
    minute: '2-digit',
  });
  const matchedBits = [...data.matched_because.performers, ...data.matched_because.genres];

  return (
    <article className="space-y-4">
      <Link to="/calendar" className="text-sm text-blue-600 hover:underline">← Calendar</Link>

      {data.image_url && (
        <img
          src={data.image_url}
          alt={data.title}
          className="w-full max-h-96 object-cover rounded"
        />
      )}

      <header className="space-y-1">
        <h1 className="text-3xl font-semibold">{data.title}</h1>
        <div className="text-gray-700">{dateLabel}</div>
        <div className="text-gray-600">
          {data.venue.name}
          {data.venue.address && <> · {data.venue.address}</>}
        </div>
      </header>

      <div className="text-sm text-gray-500">{Math.round(data.score * 100)}% match</div>

      {matchedBits.length > 0 && (
        <div className="bg-blue-50 text-blue-900 rounded p-3 text-sm">
          Matched because: {matchedBits.join(', ')}
        </div>
      )}

      {data.description && <p className="text-gray-800 whitespace-pre-wrap">{data.description}</p>}

      {data.url && (
        <a
          href={data.url}
          target="_blank"
          rel="noreferrer"
          className="inline-block bg-blue-600 hover:bg-blue-700 text-white rounded px-4 py-2"
        >
          View event
        </a>
      )}
    </article>
  );
}
