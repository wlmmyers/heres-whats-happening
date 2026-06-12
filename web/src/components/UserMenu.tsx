import { useState } from 'react';
import { useAuth } from '../auth/useAuth';
import * as s from './UserMenu.css';

export default function UserMenu() {
  const { user, logout } = useAuth();
  const [open, setOpen] = useState(false);

  const initial = user?.email?.[0]?.toUpperCase() ?? '?';

  return (
    <div className={s.root}>
      <button
        type="button"
        onClick={() => setOpen(o => !o)}
        className={s.avatar}
        aria-label="Account menu"
      >
        {initial}
      </button>
      {open && (
        <div className={s.overlay} onClick={() => setOpen(false)} />
      )}
      {open && (
        <div className={s.dropdown}>
          <p className={s.email}>{user?.email}</p>
          <hr className={s.divider} />
          <button
            type="button"
            onClick={() => { void logout(); setOpen(false); }}
            className={s.signOut}
          >
            Sign out
          </button>
        </div>
      )}
    </div>
  );
}
