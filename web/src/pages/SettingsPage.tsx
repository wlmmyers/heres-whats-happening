import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { createInterest, deleteInterest, listInterests, type Interest } from '../api/interests';
import { buildSpotifyConnectURL, disconnectSpotify } from '../api/spotify';
import { createIcalToken, revokeIcalToken } from '../api/ical';
import TagInput from '../components/TagInput';

export default function SettingsPage() {
  const qc = useQueryClient();
  const { data: interests = [] } = useQuery<Interest[]>({
    queryKey: ['interests'],
    queryFn: listInterests,
  });

  const addInterest = useMutation({
    mutationFn: (value: string) => createInterest(value),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['interests'] }),
  });
  const removeInterest = useMutation({
    mutationFn: (value: string) => {
      const target = interests.find((i) => i.value === value);
      if (!target) return Promise.resolve();
      return deleteInterest(target.id);
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: ['interests'] }),
  });
  const disconnectSpotifyMut = useMutation({
    mutationFn: disconnectSpotify,
  });

  const [icalURL, setIcalURL] = useState<string | null>(null);
  const generateIcal = useMutation({
    mutationFn: createIcalToken,
    onSuccess: (data) => setIcalURL(data.url),
  });
  const revokeIcal = useMutation({
    mutationFn: revokeIcalToken,
    onSuccess: () => setIcalURL(null),
  });

  return (
    <div className="space-y-8">
      <h1 className="text-2xl font-semibold">Settings</h1>

      {/* Interests */}
      <section className="bg-white shadow rounded p-4 space-y-3">
        <h2 className="text-lg font-medium">Manual interests</h2>
        <TagInput
          values={interests.map((i) => i.value)}
          onAdd={(v) => addInterest.mutate(v)}
          onRemove={(v) => removeInterest.mutate(v)}
          placeholder="Add an interest and press Enter"
        />
      </section>

      {/* Spotify */}
      <section className="bg-white shadow rounded p-4 space-y-3">
        <h2 className="text-lg font-medium">Spotify</h2>
        <p className="text-gray-700 text-sm">
          Connect Spotify to get matches based on your top artists and genres.
        </p>
        <div className="flex gap-2">
          <a
            href={buildSpotifyConnectURL()}
            className="bg-green-600 hover:bg-green-700 text-white rounded px-4 py-2"
          >
            Connect Spotify
          </a>
          <button
            type="button"
            onClick={() => disconnectSpotifyMut.mutate()}
            className="border rounded px-4 py-2 hover:bg-gray-50"
          >
            Disconnect
          </button>
        </div>
      </section>

      {/* iCal */}
      <section className="bg-white shadow rounded p-4 space-y-3">
        <h2 className="text-lg font-medium">Calendar subscription</h2>
        <p className="text-gray-700 text-sm">
          Generate a URL you can paste into iOS Calendar, Google Calendar, or Fantastical
          to subscribe to your matched events. The URL is shown once — store it somewhere safe.
        </p>
        <div className="flex flex-wrap gap-2 items-center">
          <button
            type="button"
            onClick={() => generateIcal.mutate()}
            className="bg-blue-600 hover:bg-blue-700 text-white rounded px-4 py-2"
          >
            Generate calendar URL
          </button>
          <button
            type="button"
            onClick={() => revokeIcal.mutate()}
            className="border rounded px-4 py-2 hover:bg-gray-50"
          >
            Revoke
          </button>
        </div>
        {icalURL && (
          <code className="block bg-gray-100 rounded p-3 text-sm break-all">{icalURL}</code>
        )}
      </section>
    </div>
  );
}
