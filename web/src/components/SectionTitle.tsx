import type { ReactNode } from 'react';
import * as s from './SectionTitle.css';

export default function SectionTitle({ children }: { children: ReactNode }) {
  return (
    <h3 className={s.title}>
      <hr className={s.rule} />
      {children}
      <hr className={s.rule} />
    </h3>
  );
}
