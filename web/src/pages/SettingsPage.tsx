import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { createInterest, deleteInterest, listInterests, type Interest } from '../api/interests';
import { startSpotifyConnect, disconnectSpotify, getSpotifyStatus } from '../api/spotify';
import { createIcalToken, revokeIcalToken } from '../api/ical';
import { getMe } from '../api/auth';
import { updateMatchThreshold, MIN_THRESHOLD, MAX_THRESHOLD } from '../api/match';
import { resetNotInterested } from '../api/notInterested';
import ConfirmDialog from '../components/ConfirmDialog';
import TagInput from '../components/TagInput';
import * as s from './SettingsPage.css';
import * as c from '../styles/common.css';

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

  const [resetConfirmOpen, setResetConfirmOpen] = useState(false);
  const resetNotInterestedMut = useMutation({
    mutationFn: resetNotInterested,
    onSuccess: () => {
      setResetConfirmOpen(false);
      qc.invalidateQueries({ queryKey: ['calendar'] });
    },
  });

  return (
    <div>
      <h1 className={c.pageTitle}>Settings</h1>

      {/* Interests */}
      <section className={s.section}>
        <h2 className={c.sectionTitle}>Manual interests</h2>
        <div className={s.item}>
          <TagInput
            values={interests.map((i) => i.value)}
            onAdd={(v) => addInterest.mutate(v)}
            onRemove={(v) => removeInterest.mutate(v)}
            placeholder="Add an interest and press Enter"
          />
        </div>
      </section>

      {/* Match sensitivity */}
      <section className={s.section}>
        <h2 className={c.sectionTitle}>Match sensitivity</h2>
        <p className={s.desc}>
          Lower = more events; higher = stricter, fewer but more relevant events.
        </p>
        <div className={s.sliderRow}>
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
            className={s.slider}
          />
          <span className={s.percent}>{effectivePercent}%</span>
        </div>
        <button
          type="button"
          onClick={() => setConfirmOpen(true)}
          disabled={!dirty || saveThreshold.isPending}
          className={s.saveButton}
        >
          Save threshold
        </button>
        {saveError && (
          <p role="alert" className={s.error}>
            Could not update your threshold. Please try again.
          </p>
        )}
      </section>

      {/* Spotify */}
      <section className={s.section}>
        <h2 className={c.sectionTitle}>Spotify</h2>
        <p className={s.desc}>
          Connect Spotify to get matches based on your top artists and genres.
        </p>
        {!spotifyStatusLoading && (
          <div className={s.row}>
            {spotifyStatus?.connected ? (
              <>
                <span className={s.connectedText}>Connected.</span>
                <button
                  type="button"
                  onClick={() => disconnectSpotifyMut.mutate()}
                  disabled={disconnectSpotifyMut.isPending}
                  className={c.buttonSecondary}
                >
                  Disconnect
                </button>
              </>
            ) : (
              <button
                type="button"
                onClick={() => connectSpotifyMut.mutate()}
                disabled={connectSpotifyMut.isPending}
                className={s.connectButton}
              >
                Connect Spotify
              </button>
            )}
          </div>
        )}
      </section>

      {/* iCal */}
      <section className={s.section}>
        <h2 className={c.sectionTitle}>Calendar subscription</h2>
        <p className={s.desc}>
          Generate a URL you can paste into iOS Calendar, Google Calendar, or Fantastical
          to subscribe to your matched events. The URL is shown once — store it somewhere safe.
        </p>
        <div className={s.buttonRow}>
          <button
            type="button"
            onClick={() => generateIcal.mutate()}
            className={c.buttonPrimary}
          >
            Generate calendar URL
          </button>
          <button
            type="button"
            onClick={() => revokeIcal.mutate()}
            className={c.buttonSecondary}
          >
            Revoke
          </button>
        </div>
        {icalURL && (
          <code className={s.codeBlock}>{icalURL}</code>
        )}
      </section>

      {/* Hidden events */}
      <section className={s.section}>
        <h2 className={c.sectionTitle}>Hidden events</h2>
        <p className={s.desc}>
          Events you marked "not interested" are hidden from your calendar. Reset to show
          them all again.
        </p>
        <button
          type="button"
          onClick={() => setResetConfirmOpen(true)}
          disabled={resetNotInterestedMut.isPending}
          className={s.resetButton}
        >
          Reset not-interested list
        </button>
      </section>

      <ConfirmDialog
        open={confirmOpen}
        title="Update match threshold?"
        message="Updating your match threshold will recalculate all of your recommended events. Continue?"
        onConfirm={() => saveThreshold.mutate(effectivePercent / 100)}
        onCancel={() => setConfirmOpen(false)}
      />
      <ConfirmDialog
        open={resetConfirmOpen}
        title="Reset not-interested list?"
        message="This clears every event you've marked 'not interested'. They may reappear in your calendar. Continue?"
        onConfirm={() => resetNotInterestedMut.mutate()}
        onCancel={() => setResetConfirmOpen(false)}
      />
    </div>
  );
}
