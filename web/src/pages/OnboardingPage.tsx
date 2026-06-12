import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link, useNavigate } from 'react-router-dom';
import TagInput from '../components/TagInput';
import { createInterest, deleteInterest, listInterests, type Interest } from '../api/interests';
import * as s from './OnboardingPage.css';
import * as c from '../styles/common.css';

export default function OnboardingPage() {
  const navigate = useNavigate();
  const qc = useQueryClient();
  const { data: interests = [] } = useQuery<Interest[]>({
    queryKey: ['interests'],
    queryFn: listInterests,
  });

  const addMut = useMutation({
    mutationFn: (value: string) => createInterest(value),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['interests'] }),
  });
  const removeMut = useMutation({
    mutationFn: (value: string) => {
      const target = interests.find((i) => i.value === value);
      if (!target) return Promise.resolve();
      return deleteInterest(target.id);
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: ['interests'] }),
  });

  const values = interests.map((i) => i.value);

  return (
    <div>
      <header>
        <h1 className={c.pageTitle}>Tell us what you're into</h1>
        <p className={s.lead}>
          Add tags — genres, activities, anything. You can also{' '}
          <Link to="/settings" className={s.inlineLink}>connect Spotify</Link> for richer matches.
        </p>
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

      <button
        type="button"
        onClick={() => navigate('/calendar')}
        className={s.continueButton}
      >
        Continue
      </button>
    </div>
  );
}
