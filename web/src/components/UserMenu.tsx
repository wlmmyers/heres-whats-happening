import { useState } from 'react';
import { Link } from 'react-router-dom';
import { useAuth } from '../auth/useAuth';
import * as s from './UserMenu.css';

// Account-level pages, kept out of the primary nav so it fits on phones. No
// active state: the menu closes as soon as one is chosen.
const menuLinks = [
  { to: '/interests', label: 'Interests' },
  { to: '/settings', label: 'Settings' },
];

export default function UserMenu() {
  const { user, logout } = useAuth();
  const [open, setOpen] = useState(false);

  const initial = user?.email?.[0]?.toUpperCase() ?? '?';

  return (
    <div className={s.root}>
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        className={s.avatar}
        aria-label="Account menu"
        aria-expanded={open}
      >
        {initial}
      </button>
      {open && <div className={s.overlay} onClick={() => setOpen(false)} />}
      {open && (
        <div className={s.dropdown}>
          <p className={s.email}>{user?.email}</p>
          <hr className={s.divider} />
          {menuLinks.map(({ to, label }) => (
            <Link key={to} to={to} className={s.menuItem} onClick={() => setOpen(false)}>
              {label}
            </Link>
          ))}
          <hr className={s.divider} />
          <button
            type="button"
            onClick={() => {
              void logout();
              setOpen(false);
            }}
            className={s.menuItem}
          >
            Sign out
          </button>
        </div>
      )}
    </div>
  );
}
