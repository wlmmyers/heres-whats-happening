import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useNavigate } from 'react-router-dom';
import TagInput from '../components/TagInput';
import TagList from '../components/TagList';
import {
  createManualInterest,
  deleteManualInterest,
  listManualInterests,
  type Interest,
} from '../api/manualInterests';
import { listSpotifyInterests, type SpotifyInterestGroup } from '../api/spotifyInterests';
import { getSpotifyStatus } from '../api/spotify';
import * as s from './InterestsPage.css';
import * as c from '../styles/common.css';

const COLLAPSE_AT = 20;

export default function InterestsPage() {
  const navigate = useNavigate();
  const qc = useQueryClient();
  const [spotifyInterestsExpanded, setSpotifyInterestsExpanded] = useState(false);

  const { data: interests = [] } = useQuery<Interest[]>({
    queryKey: ['interests'],
    queryFn: listManualInterests,
  });

  // Loaded independently of status: if groups arrive first we render them
  // immediately rather than blocking on the status request.
  const { data: spotifyGroups = [] } = useQuery<SpotifyInterestGroup[]>({
    queryKey: ['spotifyInterests'],
    queryFn: listSpotifyInterests,
  });
  const { data: spotifyStatus } = useQuery({
    queryKey: ['spotifyStatus'],
    queryFn: getSpotifyStatus,
  });

  const addMut = useMutation({
    mutationFn: (value: string) => createManualInterest(value),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['interests'] }),
  });
  const removeMut = useMutation({
    mutationFn: (value: string) => {
      const target = interests.find((i) => i.value === value);
      if (!target) return Promise.resolve();
      return deleteManualInterest(target.id);
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: ['interests'] }),
  });

  const values = interests.map((i) => i.value);

  function toggleExpanded() {
    setSpotifyInterestsExpanded((prev) => !prev);
  }

  // This page doubles as the signup onboarding step, so a brand-new user with
  // no Spotify connection must not see a dead-end "go connect Spotify" prompt
  // mid-signup. Show the section only when it has something to say.
  const showSpotify = spotifyGroups.length > 0 || spotifyStatus?.connected === true;
  const spotifyAllInterests = spotifyGroups.flatMap((g) => g.interests);
  const spotifyUniqueArtists = new Set(spotifyAllInterests.map((i) => i.value));

  return (
    <div>
      <header>
        <h1 className={c.pageTitle}>Your interests</h1>
      </header>

      <section className={c.section}>
        <h2 className={s.sectionHeading}>Tell us what you're into</h2>
        <p className={s.lead}>Add genres and artists you like</p>
        <TagInput
          values={values}
          onAdd={(v) => addMut.mutate(v)}
          onRemove={(v) => removeMut.mutate(v)}
          placeholder="Add an interest and press Enter"
        />
        {addMut.isError && <div className={s.error}>Couldn't save that tag.</div>}
      </section>

      {showSpotify && (
        <section className={c.section}>
          <h2 className={s.sectionHeading}>From your Spotify</h2>
          {spotifyGroups.length === 0 ? (
            <p className={s.emptyNote}>
              We haven't pulled your listening history yet. Check back soon.
            </p>
          ) : (
            <div>
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
                <button type="button" className={s.showAllButton} onClick={() => toggleExpanded()}>
                  {spotifyInterestsExpanded ? 'Show fewer' : 'Show all'}
                </button>
              )}
            </div>
          )}
        </section>
      )}

      <button type="button" onClick={() => navigate('/calendar')} className={s.continueButton}>
        Go to Calendar
      </button>
    </div>
  );
}
