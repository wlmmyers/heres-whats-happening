import { useState } from 'react';
import { useAuth } from '../auth/useAuth';

export default function UserMenu() {
  const { user, logout } = useAuth();
  const [open, setOpen] = useState(false);

  const initial = user?.email?.[0]?.toUpperCase() ?? '?';

  return (
    <div className="relative ml-auto">
      <button
        type="button"
        onClick={() => setOpen(o => !o)}
        className="w-8 h-8 rounded-full bg-blue-500 text-white text-sm font-semibold flex items-center justify-center cursor-pointer"
        aria-label="Account menu"
      >
        {initial}
      </button>
      {open && (
        <div className="fixed inset-0 z-10" onClick={() => setOpen(false)} />
      )}
      {open && (
        <div className="absolute top-full right-0 mt-1 z-20 min-w-max rounded border border-gray-200 bg-white px-3 py-2 shadow-md">
          <p className="mb-2 text-xs text-gray-500">{user?.email}</p>
          <hr className="mb-2 border-gray-100" />
          <button
            type="button"
            onClick={() => { void logout(); setOpen(false); }}
            className="cursor-pointer border-0 bg-transparent p-0 text-sm text-gray-700"
          >
            Sign out
          </button>
        </div>
      )}
    </div>
  );
}
