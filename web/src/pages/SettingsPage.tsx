import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { createInterest, deleteInterest, listInterests, type Interest } from '../api/interests';
import { startSpotifyConnect, disconnectSpotify, getSpotifyStatus } from '../api/spotify';
import { createIcalToken, revokeIcalToken } from '../api/ical';
import { getMe } from '../api/auth';
import { updateMatchThreshold, MIN_THRESHOLD, MAX_THRESHOLD } from '../api/match';
import ConfirmDialog from '../components/ConfirmDialog';
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
  const { data: spotifyStatus, isLoading: spotifyStatusLoading } = useQuery({
    queryKey: ['spotify-status'],
    queryFn: getSpotifyStatus,
  });
  const connectSpotifyMut = useMutation({
    mutationFn: startSpotifyConnect,
    onSuccess: (authorizeURL) => {
      window.location.assign(authorizeURL);
    },
  });
  const disconnectSpotifyMut = useMutation({
    mutationFn: disconnectSpotify,
    onSuccess: () => qc.invalidateQueries({ queryKey: ['spotify-status'] }),
  });

  const { data: me } = useQuery({ queryKey: ['me'], queryFn: getMe });
  const loadedPercent = Math.round((me?.score_threshold ?? 0.3) * 100);
  const [percent, setPercent] = useState<number | null>(null);
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [saveError, setSaveError] = useState(false);
  const effectivePercent = percent ?? loadedPercent;

  const saveThreshold = useMutation({
    mutationFn: (threshold: number) => updateMatchThreshold(threshold),
    onSuccess: () => {
      setConfirmOpen(false);
      setPercent(null);
      setSaveError(false);
      qc.invalidateQueries({ queryKey: ['me'] });
      qc.invalidateQueries({ queryKey: ['calendar'] });
    },
    onError: () => {
      setConfirmOpen(false);
      setSaveError(true);
    },
  });

  const minPercent = Math.round(MIN_THRESHOLD * 100);
  const maxPercent = Math.round(MAX_THRESHOLD * 100);
  const dirty = percent !== null && percent !== loadedPercent;

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

      {/* Match sensitivity */}
      <section className="bg-white shadow rounded p-4 space-y-3">
        <h2 className="text-lg font-medium">Match sensitivity</h2>
        <p className="text-gray-700 text-sm">
          Lower = more events; higher = stricter, fewer but more relevant events.
        </p>
        <div className="flex items-center gap-3">
          <input
            type="range"
            aria-label="Match sensitivity"
            min={minPercent}
            max={maxPercent}
            step={1}
            value={effectivePercent}
            onChange={(e) => {
              setPercent(Number(e.target.value));
              setSaveError(false);
            }}
            className="flex-1"
          />
          <span className="w-12 text-right text-sm tabular-nums">{effectivePercent}%</span>
        </div>
        <button
          type="button"
          onClick={() => setConfirmOpen(true)}
          disabled={!dirty || saveThreshold.isPending}
          className="bg-blue-600 hover:bg-blue-700 disabled:opacity-60 text-white rounded px-4 py-2"
        >
          Save threshold
        </button>
        {saveError && (
          <p role="alert" className="text-sm text-red-600">
            Could not update your threshold. Please try again.
          </p>
        )}
      </section>

      {/* Spotify */}
      <section className="bg-white shadow rounded p-4 space-y-3">
        <h2 className="text-lg font-medium">Spotify</h2>
        <p className="text-gray-700 text-sm">
          Connect Spotify to get matches based on your top artists and genres.
        </p>
        {!spotifyStatusLoading && (
          <div className="flex gap-2 items-center">
            {spotifyStatus?.connected ? (
              <>
                <span className="text-sm text-gray-700">Connected.</span>
                <button
                  type="button"
                  onClick={() => disconnectSpotifyMut.mutate()}
                  disabled={disconnectSpotifyMut.isPending}
                  className="border rounded px-4 py-2 hover:bg-gray-50 disabled:opacity-60"
                >
                  Disconnect
                </button>
              </>
            ) : (
              <button
                type="button"
                onClick={() => connectSpotifyMut.mutate()}
                disabled={connectSpotifyMut.isPending}
                className="bg-green-600 hover:bg-green-700 disabled:opacity-60 text-white rounded px-4 py-2"
              >
                Connect Spotify
              </button>
            )}
          </div>
        )}
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

      <ConfirmDialog
        open={confirmOpen}
        title="Update match threshold?"
        message="Updating your match threshold will recalculate all of your recommended events. Continue?"
        onConfirm={() => saveThreshold.mutate(effectivePercent / 100)}
        onCancel={() => setConfirmOpen(false)}
      />
    </div>
  );
}
