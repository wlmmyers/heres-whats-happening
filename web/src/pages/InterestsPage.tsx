import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import TagInput from '../components/TagInput';
import TagList from '../components/TagList';
import { useManualInterests } from '../hooks/useManualInterests';
import { useSpotifyStatus } from '../hooks/useSpotifyStatus';
import { useSpotifyInterests } from '../hooks/useSpotifyInterests';
import { useConnectSpotify } from '../hooks/useConnectSpotify';
import { useCreateManualInterest } from '../hooks/useCreateManualInterest';
import { useDeleteManualInterest } from '../hooks/useDeleteManualInterest';
import * as s from './InterestsPage.css';
import * as c from '../styles/common.css';

const COLLAPSE_AT = 20;

export default function InterestsPage() {
  const navigate = useNavigate();
  const [spotifyInterestsExpanded, setSpotifyInterestsExpanded] = useState(false);
  const [spotifyGenresExpanded, setSpotifyGenresExpanded] = useState(false);

  const { data: interests = [] } = useManualInterests();

  const { data: spotifyStatus } = useSpotifyStatus();
  const connectSpotifyMut = useConnectSpotify();

  // Loaded independently of status: if groups arrive first we render them
  // immediately rather than blocking on the status request.
  const { data: spotifyGroups = [], isPending: spotifyGroupsPending } = useSpotifyInterests();

  const addMut = useCreateManualInterest();
  const removeMut = useDeleteManualInterest();

  const values = interests.map((i) => i.value);

  // This page doubles as the signup onboarding step, so a brand-new user with
  // no Spotify connection must not see a dead-end "go connect Spotify" prompt
  // mid-signup. Show the section only when it has something to say.
  const showSpotifyInterests = spotifyGroups.length > 0 || spotifyStatus?.connected === true;
  const spotifyAllArtistInterests = spotifyGroups
    .filter((i) => i.kind !== 'spotify_top_genre')
    .flatMap((g) => g.interests);
  const spotifyGenreInterests = spotifyGroups
    .filter((i) => i.kind === 'spotify_top_genre')
    .flatMap((g) => g.interests);
  const spotifyUniqueArtists = new Set(spotifyAllArtistInterests.map((i) => i.value));
  const spotifyUniqueGenres = new Set(spotifyGenreInterests.map((i) => i.value));

  return (
    <div>
      <div className={c.pageHeader}>
        <h1 className={c.pageTitle}>Your interests</h1>
      </div>

      <section className={c.section}>
        <h2 className={s.sectionHeading}>Tell us what you're into</h2>
        <p className={s.lead}>Add genres and artists you like</p>
        <TagInput
          values={values}
          onAdd={(v) => addMut.mutate(v)}
          onRemove={(v) => removeMut.mutate(v)}
          placeholder="Add an interest and press Enter"
        />
        {addMut.isError && (
          <div className={s.error}>Couldn't save that tag. Did you already add it?</div>
        )}
      </section>

      {showSpotifyInterests ? (
        <section className={c.section}>
          <h2 className={s.sectionHeading}>From your Spotify</h2>
          {spotifyGroupsPending ? null : spotifyGroups.length === 0 ? (
            <p className={s.emptyNote}>
              We haven't pulled your listening history yet. Check back soon.
            </p>
          ) : (
            <div>
              <section className={c.bodySection}>
                <p className={s.lead}>
                  Spotify-derived artists from your top artists, top tracks, and liked songs
                </p>
                <TagList
                  values={
                    spotifyInterestsExpanded
                      ? Array.from(spotifyUniqueArtists)
                      : Array.from(spotifyUniqueArtists).slice(0, COLLAPSE_AT)
                  }
                />
                {spotifyUniqueArtists.size > COLLAPSE_AT && (
                  <button
                    type="button"
                    className={s.showAllButton}
                    onClick={() => {
                      setSpotifyInterestsExpanded((prev) => !prev);
                    }}
                  >
                    {spotifyInterestsExpanded
                      ? 'Show fewer'
                      : `Show all (${spotifyUniqueArtists.size})`}
                  </button>
                )}
              </section>
              <section className={c.bodySection}>
                <p className={s.lead}>Spotify-derived genre interests</p>
                <TagList
                  values={
                    spotifyGenresExpanded
                      ? Array.from(spotifyUniqueGenres)
                      : Array.from(spotifyUniqueGenres).slice(0, COLLAPSE_AT)
                  }
                />
                {spotifyUniqueGenres.size > COLLAPSE_AT && (
                  <button
                    type="button"
                    className={s.showAllButton}
                    onClick={() => {
                      setSpotifyGenresExpanded((prev) => !prev);
                    }}
                  >
                    {spotifyGenresExpanded
                      ? 'Show fewer'
                      : `Show all (${spotifyUniqueGenres.size})`}
                  </button>
                )}
              </section>
            </div>
          )}
        </section>
      ) : (
        spotifyStatus?.connected !== true && (
          <section className={c.section}>
            <h2 className={c.sectionTitle}>Spotify</h2>
            <p className={s.lead}>Connect Spotify to see your Spotify-derived interests.</p>

            <button
              type="button"
              onClick={() => connectSpotifyMut.mutate()}
              disabled={connectSpotifyMut.isPending}
              className={s.connectButton}
            >
              Connect Spotify
            </button>
          </section>
        )
      )}
      <p className={s.continueSection}>
        <button
          type="button"
          onClick={() => navigate('/calendar/seattle')}
          className={s.continueButton}
        >
          Go to Calendar
        </button>
      </p>
    </div>
  );
}
