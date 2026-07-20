import * as s from './TagList.css';

interface Props {
  values: string[];
}

// Read-only counterpart to TagInput: renders chips with no add input and no
// remove affordance. Used for interests the user cannot edit.
export default function TagList({ values }: Props) {
  return (
    <div className={s.wrapper}>
      {values.map((v) => (
        <span key={v} className={s.tag}>
          {v}
        </span>
      ))}
    </div>
  );
}
