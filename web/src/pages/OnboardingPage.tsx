import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link, useNavigate } from 'react-router-dom';
import TagInput from '../components/TagInput';
import { createInterest, deleteInterest, listInterests, type Interest } from '../api/interests';

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
    <div className="space-y-6">
      <header className="space-y-2">
        <h1 className="text-2xl font-semibold">Tell us what you're into</h1>
        <p className="text-gray-600">
          Add tags — genres, activities, anything. You can also{' '}
          <Link to="/settings" className="text-blue-600 underline">connect Spotify</Link> for richer matches.
        </p>
      </header>

      <section className="bg-white shadow rounded p-4 space-y-3">
        <TagInput
          values={values}
          onAdd={(v) => addMut.mutate(v)}
          onRemove={(v) => removeMut.mutate(v)}
          placeholder="Add an interest and press Enter"
        />
        {addMut.isError && <div className="text-red-600 text-sm">Couldn't save that tag.</div>}
      </section>

      <button
        type="button"
        onClick={() => navigate('/calendar')}
        className="bg-blue-600 hover:bg-blue-700 text-white rounded px-4 py-2"
      >
        Continue
      </button>
    </div>
  );
}
