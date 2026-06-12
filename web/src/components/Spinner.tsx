import * as s from './Spinner.css';

export default function Spinner() {
  return (
    <div className={s.root} role="status" aria-live="polite">
      Loading…
    </div>
  );
}
