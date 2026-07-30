import { useState } from 'react';
import { MIN_THRESHOLD, MAX_THRESHOLD } from '../api/match';
import ConfirmDialog from '../components/ConfirmDialog';
import { useSpotifyStatus } from '../hooks/useSpotifyStatus';
import { useMe } from '../hooks/useMe';
import { useConnectSpotify } from '../hooks/useConnectSpotify';
import { useDisconnectSpotify } from '../hooks/useDisconnectSpotify';
import { useUpdateMatchThreshold } from '../hooks/useUpdateMatchThreshold';
import { useCreateIcalToken } from '../hooks/useCreateIcalToken';
import { useRevokeIcalToken } from '../hooks/useRevokeIcalToken';
import { useResetNotInterested } from '../hooks/useResetNotInterested';
import * as s from './SettingsPage.css';
import * as c from '../styles/common.css';

export default function SettingsPage() {
  const { data: spotifyStatus, isLoading: spotifyStatusLoading } = useSpotifyStatus();
  const connectSpotifyMut = useConnectSpotify();
  const disconnectSpotifyMut = useDisconnectSpotify();

  const { data: me } = useMe();
  const loadedPercent = Math.round((me?.score_threshold ?? 0.3) * 100);
  const [percent, setPercent] = useState<number | null>(null);
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [saveError, setSaveError] = useState(false);
  const effectivePercent = percent ?? loadedPercent;

  const saveThreshold = useUpdateMatchThreshold();

  const minPercent = Math.round(MIN_THRESHOLD * 100);
  const maxPercent = Math.round(MAX_THRESHOLD * 100);
  const dirty = percent !== null && percent !== loadedPercent;

  const [icalURL, setIcalURL] = useState<string | null>(null);
  const generateIcal = useCreateIcalToken();
  const revokeIcal = useRevokeIcalToken();

  const [resetConfirmOpen, setResetConfirmOpen] = useState(false);
  const resetNotInterestedMut = useResetNotInterested();

  return (
    <div>
      <div className={c.pageHeader}>
        <h1 className={c.pageTitle}>Settings</h1>
      </div>
      <div>
        {/* Match sensitivity */}
        <section className={c.section}>
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
        <section className={c.section}>
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
        <section className={c.section}>
          <h2 className={c.sectionTitle}>Calendar subscription</h2>
          <p className={s.desc}>
            Generate a URL you can paste into iOS Calendar, Google Calendar, or Fantastical to
            subscribe to your matched events.
          </p>
          <div className={s.buttonRow}>
            <button
              type="button"
              onClick={() =>
                generateIcal.mutate(undefined, { onSuccess: (data) => setIcalURL(data.url) })
              }
              className={c.buttonPrimary}
            >
              Generate calendar URL
            </button>
            <button
              type="button"
              onClick={() => revokeIcal.mutate(undefined, { onSuccess: () => setIcalURL(null) })}
              className={c.buttonSecondary}
            >
              Revoke
            </button>
          </div>
          {icalURL && <code className={s.codeBlock}>{icalURL}</code>}
        </section>

        {/* Hidden events */}
        <section className={c.section}>
          <h2 className={c.sectionTitle}>Hidden events</h2>
          <p className={s.desc}>
            Events you marked "not interested" are hidden from your calendar. Reset to show them all
            again.
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
          onConfirm={() =>
            saveThreshold.mutate(effectivePercent / 100, {
              onSuccess: () => {
                setConfirmOpen(false);
                setPercent(null);
                setSaveError(false);
              },
              onError: () => {
                setConfirmOpen(false);
                setSaveError(true);
              },
            })
          }
          onCancel={() => setConfirmOpen(false)}
        />
        <ConfirmDialog
          open={resetConfirmOpen}
          title="Reset not-interested list?"
          message="This clears every event you've marked 'not interested'. They may reappear in your calendar. Continue?"
          onConfirm={() =>
            resetNotInterestedMut.mutate(undefined, { onSuccess: () => setResetConfirmOpen(false) })
          }
          onCancel={() => setResetConfirmOpen(false)}
        />
      </div>
    </div>
  );
}
