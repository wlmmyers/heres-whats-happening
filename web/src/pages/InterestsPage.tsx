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
  const [expanded, setExpanded] = useState<Set<string>>(new Set());

  const { data: interests = [] } = useQuery<Interest[]>({
    queryKey: ['interests'],
    queryFn: listManualInterests,
  });

  // Loaded independently of status: if groups arrive first we render them
  // immediately rather than blocking on the status request.
  const { data: spotifyGroups = [], isPending: spotifyInterestsPending } = useQuery<
    SpotifyInterestGroup[]
  >({
    queryKey: ['spotifyInterests'],
    queryFn: listSpotifyInterests,
  });
  const { data: spotifyStatus } = useQuery({
    queryKey: ['spotify-status'],
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

  function toggle(kind: string) {
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(kind)) next.delete(kind);
      else next.add(kind);
      return next;
    });
  }

  // This page doubles as the signup onboarding step, so a brand-new user with
  // no Spotify connection must not see a dead-end "go connect Spotify" prompt
  // mid-signup. Show the section only when it has something to say.
  const showSpotify = spotifyGroups.length > 0 || spotifyStatus?.connected === true;

  return (
    <div>
      <header>
        <h1 className={c.pageTitle}>Tell us what you're into</h1>
        <p className={s.lead}>Add tags — genres, activities, anything.</p>
      </header>

      <section className={s.section}>
        <TagInput
          values={values}
          onAdd={(v) => addMut.mutate(v)}
          onRemove={(v) => removeMut.mutate(v)}
          placeholder="Add an interest and press Enter"
        />
        {addMut.isError && <div className={s.error}>Couldn't save that tag.</div>}
      </section>

      {showSpotify && (
        <section className={s.section}>
          <h2 className={s.sectionHeading}>From your Spotify</h2>
          {spotifyGroups.length === 0 ? (
            !spotifyInterestsPending && (
              <p className={s.emptyNote}>
                We haven't pulled your listening history yet. Check back soon.
              </p>
            )
          ) : (
            spotifyGroups.map((group) => {
              const isExpanded = expanded.has(group.kind);
              const shown = isExpanded
                ? group.interests
                : group.interests.slice(0, COLLAPSE_AT);
              return (
                <div key={group.kind}>
                  <h3 className={s.groupHeading}>{group.label}</h3>
                  <TagList values={shown.map((i) => i.value)} />
                  {group.interests.length > COLLAPSE_AT && (
                    <button
                      type="button"
                      className={s.showAllButton}
                      onClick={() => toggle(group.kind)}
                    >
                      {isExpanded ? 'Show less' : `Show all (${group.interests.length})`}
                    </button>
                  )}
                </div>
              );
            })
          )}
        </section>
      )}

      <button type="button" onClick={() => navigate('/calendar')} className={s.continueButton}>
        Continue
      </button>
    </div>
  );
}
