# Logged-out Calendar Access Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let anonymous visitors view `CalendarPage` populated from a bundled `logged-out-calendar-data.json`, with a permanent centered login dialog over non-interactive events; logging in transparently switches to the real authenticated calendar.

**Architecture:** `Layout` and `/calendar` become public; other routes stay behind per-route `RequireAuth`. `CalendarPage` reads `useAuth().status` to pick the data source (`getCalendar(from, to, loggedOut)`), toggle event interactivity, and mount a `LoginDialog`. The login UI is extracted from `LoginPage` into a shared `LoginForm` reused by both the page and the dialog. Auth-state change re-renders `CalendarPage`, refetching real data and unmounting the dialog.

**Tech Stack:** React 19, react-router-dom 7, @tanstack/react-query 5, vanilla-extract, vitest + @testing-library/react, TypeScript (bundler mode).

## Global Constraints

- Work only inside `web/`; do not touch the backend or the `logged-out-calendar-data.json` contents.
- `tsconfig.app.json` has `verbatimModuleSyntax: true` — use `import type { … }` or inline `type` for type-only imports; value imports stay plain.
- `tsconfig.app.json` has `noUnusedLocals: true` and `noUnusedParameters: true` — leave no unused imports or bindings.
- Follow existing patterns: vanilla-extract `*.css.ts` per component, tests with `vitest` + `@testing-library/react`, `vi.mock` for module boundaries.
- `CalendarEvent` shape (already defined in `web/src/api/calendar.ts`): `{ id: string; title: string; description?: string; starts_at: string; ends_at?: string; image_url?: string; url?: string; venue: { name: string; address?: string }; score: number; matched_because: { performers: string[]; genres: string[] } }`.
- `AuthState` shape (from `web/src/auth/AuthContext.tsx`): `{ user: User | null; status: 'loading' | 'authenticated' | 'anonymous'; login: (e,p)=>Promise<void>; signup: (e,p)=>Promise<void>; logout: ()=>Promise<void> }`.
- Run commands from the `web/` directory. Single-test runs: `npm test -- <path>`. Full suite: `npm test`. Typecheck+build: `npm run build`.

---

### Task 1: `getCalendar` logged-out branch + enable JSON imports

**Files:**
- Modify: `web/tsconfig.app.json` (add `resolveJsonModule`)
- Modify: `web/src/api/calendar.ts`
- Test: `web/src/api/calendar.test.ts` (create)
- Commit also: `web/src/api/logged-out-calendar-data.json` (currently untracked)

**Interfaces:**
- Consumes: nothing from earlier tasks. Reads `logged-out-calendar-data.json` (shape `{ events: CalendarEvent[] }`).
- Produces: `getCalendar(from: string, to: string, loggedOut?: boolean): Promise<CalendarEvent[]>` — when `loggedOut` is `true`, returns the bundled events and makes no network call; otherwise unchanged. `getEvent` is unchanged.

- [ ] **Step 1: Write the failing test**

Create `web/src/api/calendar.test.ts`:

```ts
import { describe, it, expect, vi, beforeEach } from 'vitest';

vi.mock('./client', () => ({
  apiFetch: vi.fn(),
}));

import { getCalendar } from './calendar';
import { apiFetch } from './client';

beforeEach(() => {
  vi.resetAllMocks();
});

describe('getCalendar', () => {
  it('returns bundled logged-out data without calling the API when loggedOut', async () => {
    const events = await getCalendar('2026-01-01', '2026-12-31', true);
    expect(events.length).toBeGreaterThan(0);
    expect(events[0]).toHaveProperty('id');
    expect(apiFetch).not.toHaveBeenCalled();
  });

  it('fetches from the API when not logged out', async () => {
    (apiFetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({ events: [{ id: 'x' }] });
    const events = await getCalendar('2026-01-01', '2026-12-31', false);
    expect(apiFetch).toHaveBeenCalledWith('/me/calendar?from=2026-01-01&to=2026-12-31');
    expect(events).toEqual([{ id: 'x' }]);
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npm test -- src/api/calendar.test.ts`
Expected: FAIL — the logged-out test fails because `getCalendar` currently ignores the third argument and always calls `apiFetch` (and/or a `tsc` JSON-import error once Step 3's import is added).

- [ ] **Step 3: Enable JSON imports and implement the branch**

In `web/tsconfig.app.json`, add `"resolveJsonModule": true,` inside `compilerOptions` (e.g. right after `"skipLibCheck": true,`):

```json
    "skipLibCheck": true,
    "resolveJsonModule": true,
```

Edit `web/src/api/calendar.ts`. Add the JSON import at the top (after the existing `import { apiFetch } from './client';`):

```ts
import loggedOutData from './logged-out-calendar-data.json';
```

Replace the existing `getCalendar` function body with:

```ts
export async function getCalendar(
  from: string,
  to: string,
  loggedOut = false,
): Promise<CalendarEvent[]> {
  if (loggedOut) {
    return (loggedOutData as { events: CalendarEvent[] }).events;
  }
  const params = new URLSearchParams({ from, to });
  const out = await apiFetch<{ events: CalendarEvent[] }>(`/me/calendar?${params.toString()}`);
  return out.events;
}
```

(If `tsc` reports "Conversion of type … may be a mistake", change the cast to `(loggedOutData as unknown as { events: CalendarEvent[] }).events`.)

- [ ] **Step 4: Run test + typecheck to verify they pass**

Run: `npm test -- src/api/calendar.test.ts`
Expected: PASS (both tests).
Run: `npm run build`
Expected: PASS — no TypeScript errors (confirms `resolveJsonModule` + the cast compile).

- [ ] **Step 5: Commit**

```bash
git add web/tsconfig.app.json web/src/api/calendar.ts web/src/api/calendar.test.ts web/src/api/logged-out-calendar-data.json
git commit -m "feat(web): getCalendar returns bundled data when logged out"
```

---

### Task 2: `EventCard` non-interactive mode

**Files:**
- Modify: `web/src/components/EventCard.tsx`
- Test: `web/src/components/EventCard.test.tsx` (add a case)

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: `EventCard` gains prop `interactive?: boolean` (default `true`). When `false`, the card body renders in a plain `<div>` (no `<Link>`, not navigable). Existing props (`event`, `onNotInterested`) unchanged.

- [ ] **Step 1: Write the failing test**

Add to `web/src/components/EventCard.test.tsx` (inside the `describe('EventCard', …)` block):

```ts
  it('renders no link when not interactive', () => {
    render(
      <MemoryRouter>
        <EventCard event={event} interactive={false} />
      </MemoryRouter>,
    );
    expect(screen.queryByRole('link')).not.toBeInTheDocument();
    expect(screen.getByText('PB Live')).toBeInTheDocument();
  });
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npm test -- src/components/EventCard.test.tsx`
Expected: FAIL — a link is still rendered (`interactive` prop not yet supported), so `queryByRole('link')` finds an element.

- [ ] **Step 3: Implement the prop**

Edit `web/src/components/EventCard.tsx`. Update the signature to accept `interactive` and swap the wrapper element. Replace the whole component with:

```tsx
import { Link } from 'react-router-dom';
import type { CalendarEvent } from '../api/calendar';
import * as s from './EventCard.css';

export default function EventCard({
  event,
  onNotInterested,
  interactive = true,
}: {
  event: CalendarEvent;
  onNotInterested?: (id: string) => void;
  interactive?: boolean;
}) {
  const date = new Date(event.starts_at);
  const dateLabel = date.toLocaleString(undefined, {
    weekday: 'short',
    month: 'short',
    day: 'numeric',
    hour: 'numeric',
    minute: '2-digit',
  });
  const matchedBits = [...event.matched_because.performers, ...event.matched_because.genres];

  const body = (
    <>
      <div className={s.titleRow}>
        <h3 className={s.title}>{event.title}</h3>
        <span className={s.score}>{Math.round(event.score * 100)}% match</span>
      </div>
      <div className={s.date}>
        {dateLabel} · {event.venue.name}
      </div>
      {matchedBits.length > 0 && (
        <div className={s.matched}>Matched because: {matchedBits.join(', ')}</div>
      )}
    </>
  );

  return (
    <div className={s.card}>
      {interactive ? (
        <Link to={`/events/${event.id}`} className={s.link}>
          {body}
        </Link>
      ) : (
        <div className={s.link}>{body}</div>
      )}
      {onNotInterested && (
        <div className={s.notInterestedRow}>
          <button
            type="button"
            onClick={() => onNotInterested(event.id)}
            className={s.notInterestedButton}
          >
            Not interested
          </button>
        </div>
      )}
    </div>
  );
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `npm test -- src/components/EventCard.test.tsx`
Expected: PASS — all four cases (links by default, no link when non-interactive, button gating both ways).

- [ ] **Step 5: Commit**

```bash
git add web/src/components/EventCard.tsx web/src/components/EventCard.test.tsx
git commit -m "feat(web): add non-interactive mode to EventCard"
```

---

### Task 3: Extract `LoginForm`, refactor `LoginPage`

**Files:**
- Create: `web/src/components/LoginForm.tsx`
- Create: `web/src/components/LoginForm.css.ts`
- Modify: `web/src/pages/LoginPage.tsx`
- Modify: `web/src/pages/LoginPage.css.ts` (keep only `page`)
- Test: `web/src/components/LoginForm.test.tsx` (create)
- Test: `web/src/pages/LoginPage.test.tsx` (must still pass unchanged)

**Interfaces:**
- Consumes: `useAuth()` from `../auth/useAuth`; `common.css` (`textInput`) and theme tokens.
- Produces: `LoginForm` default export — props `{ onSuccess?: () => void }`. Renders the logo + sign-in form; on successful `login(email, password)` calls `onSuccess?.()`. `LoginPage` renders `<div class=page><LoginForm onSuccess={navigate-to-dest} /></div>`.

- [ ] **Step 1: Write the failing test**

Create `web/src/components/LoginForm.test.tsx`:

```tsx
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';

vi.mock('../auth/useAuth', () => ({ useAuth: vi.fn() }));

import { useAuth } from '../auth/useAuth';
import LoginForm from './LoginForm';

beforeEach(() => {
  vi.resetAllMocks();
});

describe('LoginForm', () => {
  it('calls login and onSuccess on submit', async () => {
    const login = vi.fn().mockResolvedValueOnce(undefined);
    vi.mocked(useAuth).mockReturnValue({
      status: 'anonymous',
      user: null,
      login,
      signup: vi.fn(),
      logout: vi.fn(),
    });
    const onSuccess = vi.fn();
    render(
      <MemoryRouter>
        <LoginForm onSuccess={onSuccess} />
      </MemoryRouter>,
    );
    await userEvent.type(screen.getByLabelText(/email/i), 'a@x');
    await userEvent.type(screen.getByLabelText(/password/i), 'hunter22');
    await userEvent.click(screen.getByRole('button', { name: /sign in/i }));
    await waitFor(() => expect(login).toHaveBeenCalledWith('a@x', 'hunter22'));
    expect(onSuccess).toHaveBeenCalled();
  });

  it('shows an error when credentials are invalid', async () => {
    const err = Object.assign(new Error('Invalid'), { code: 'invalid_credentials' });
    const login = vi.fn().mockRejectedValueOnce(err);
    vi.mocked(useAuth).mockReturnValue({
      status: 'anonymous',
      user: null,
      login,
      signup: vi.fn(),
      logout: vi.fn(),
    });
    render(
      <MemoryRouter>
        <LoginForm />
      </MemoryRouter>,
    );
    await userEvent.type(screen.getByLabelText(/email/i), 'a@x');
    await userEvent.type(screen.getByLabelText(/password/i), 'wrong');
    await userEvent.click(screen.getByRole('button', { name: /sign in/i }));
    await waitFor(() =>
      expect(screen.getByText(/email or password is wrong/i)).toBeInTheDocument(),
    );
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npm test -- src/components/LoginForm.test.tsx`
Expected: FAIL — `Cannot find module './LoginForm'` (component not created yet).

- [ ] **Step 3: Create `LoginForm.css.ts` (move styles out of LoginPage.css)**

Create `web/src/components/LoginForm.css.ts`:

```ts
import { style } from '@vanilla-extract/css';
import { surface, errorText, buttonSubmit } from '../styles/common.css';
import { color, fontSize, fontWeight } from '../styles/theme';

export const card = style([
  surface,
  {
    width: '100%',
    maxWidth: '24rem',
    padding: '1.5rem',
    display: 'flex',
    flexDirection: 'column',
    alignItems: 'center',
  },
]);

export const logo = style({
  marginBottom: '1rem',
  '@media': {
    'screen and (max-width: 768px)': { marginBottom: '2rem' },
  },
});

export const form = style([]);

export const title = style({ ...fontSize.xl, fontWeight: fontWeight.semibold });

export const field = style({
  display: 'block',
  ...fontSize.sm,
  marginTop: '1rem',
});

export const fieldLabel = style({ color: color.gray700 });

export const error = style([errorText, { marginTop: '1rem' }]);

export const submit = style([buttonSubmit, { marginTop: '1rem' }]);

export const switchText = style({
  ...fontSize.sm,
  color: color.gray600,
  marginTop: '1rem',
});

export const switchLink = style({
  color: color.blue600,
  ':hover': { textDecorationLine: 'underline' },
});
```

- [ ] **Step 4: Create `LoginForm.tsx`**

Create `web/src/components/LoginForm.tsx`:

```tsx
import { useState, type FormEvent } from 'react';
import { Link } from 'react-router-dom';
import { useAuth } from '../auth/useAuth';
import * as s from './LoginForm.css';
import * as c from '../styles/common.css';

export default function LoginForm({ onSuccess }: { onSuccess?: () => void }) {
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const { login } = useAuth();

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    setSubmitting(true);
    try {
      await login(email, password);
      onSuccess?.();
    } catch (err) {
      const code = (err as { code?: string }).code;
      if (code === 'invalid_credentials') {
        setError('Email or password is wrong');
      } else {
        setError(err instanceof Error ? err.message : 'Login failed');
      }
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className={s.card}>
      <img src="/titleGraphic1.png" alt="Logo" style={{ width: '300px' }} className={s.logo} />
      <form onSubmit={onSubmit} className={s.form}>
        <h1 className={s.title}>Sign in</h1>

        <label className={s.field}>
          <span className={s.fieldLabel}>Email</span>
          <input
            type="email"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            autoComplete="email"
            required
            className={c.textInput}
          />
        </label>

        <label className={s.field}>
          <span className={s.fieldLabel}>Password</span>
          <input
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            autoComplete="current-password"
            required
            className={c.textInput}
          />
        </label>

        {error && <div className={s.error}>{error}</div>}

        <button type="submit" disabled={submitting} className={s.submit}>
          {submitting ? 'Signing in…' : 'Sign in'}
        </button>

        <p className={s.switchText}>
          No account?{' '}
          <Link to="/signup" className={s.switchLink}>
            Sign up
          </Link>
        </p>
      </form>
    </div>
  );
}
```

- [ ] **Step 5: Slim `LoginPage.css.ts` down to `page` only**

Replace the entire contents of `web/src/pages/LoginPage.css.ts` with:

```ts
import { style } from '@vanilla-extract/css';
import { color } from '../styles/theme';

export const page = style({
  minHeight: '100vh',
  backgroundColor: color.gray50,
  display: 'flex',
  flexDirection: 'column',
  alignItems: 'center',
  justifyContent: 'center',
  paddingInline: '1rem',
  '@media': {
    'screen and (min-width: 768px)': { flexDirection: 'row', gap: '2rem' },
  },
});
```

- [ ] **Step 6: Refactor `LoginPage.tsx` to use `LoginForm`**

Replace the entire contents of `web/src/pages/LoginPage.tsx` with:

```tsx
import { useNavigate, useLocation } from 'react-router-dom';
import LoginForm from '../components/LoginForm';
import * as s from './LoginPage.css';

interface LocationState {
  from?: { pathname?: string };
}

export default function LoginPage() {
  const navigate = useNavigate();
  const location = useLocation();
  const dest = (location.state as LocationState | null)?.from?.pathname ?? '/calendar';

  return (
    <div className={s.page}>
      <LoginForm onSuccess={() => navigate(dest, { replace: true })} />
    </div>
  );
}
```

- [ ] **Step 7: Run the relevant tests to verify they pass**

Run: `npm test -- src/components/LoginForm.test.tsx src/pages/LoginPage.test.tsx`
Expected: PASS — new `LoginForm` cases pass; existing `LoginPage` tests (submit+redirect, error) still pass unchanged.

- [ ] **Step 8: Commit**

```bash
git add web/src/components/LoginForm.tsx web/src/components/LoginForm.css.ts web/src/components/LoginForm.test.tsx web/src/pages/LoginPage.tsx web/src/pages/LoginPage.css.ts
git commit -m "refactor(web): extract LoginForm from LoginPage"
```

---

### Task 4: `LoginDialog` component

**Files:**
- Create: `web/src/components/LoginDialog.tsx`
- Create: `web/src/components/LoginDialog.css.ts`
- Test: `web/src/components/LoginDialog.test.tsx` (create)

**Interfaces:**
- Consumes: `LoginForm` (Task 3).
- Produces: `LoginDialog` default export — no props. Renders a fixed, centered, non-dismissible `role="dialog" aria-label="Sign in"` containing `<LoginForm />` (no `onSuccess`; the parent's auth-state change unmounts it). No dimming scrim.

- [ ] **Step 1: Write the failing test**

Create `web/src/components/LoginDialog.test.tsx`:

```tsx
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';

vi.mock('../auth/useAuth', () => ({ useAuth: vi.fn() }));

import { useAuth } from '../auth/useAuth';
import LoginDialog from './LoginDialog';

beforeEach(() => {
  vi.resetAllMocks();
  vi.mocked(useAuth).mockReturnValue({
    status: 'anonymous',
    user: null,
    login: vi.fn(),
    signup: vi.fn(),
    logout: vi.fn(),
  });
});

describe('LoginDialog', () => {
  it('renders a sign-in dialog with the login form', () => {
    render(
      <MemoryRouter>
        <LoginDialog />
      </MemoryRouter>,
    );
    expect(screen.getByRole('dialog', { name: /sign in/i })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /sign in/i })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: /sign up/i })).toHaveAttribute('href', '/signup');
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npm test -- src/components/LoginDialog.test.tsx`
Expected: FAIL — `Cannot find module './LoginDialog'`.

- [ ] **Step 3: Create `LoginDialog.css.ts`**

Create `web/src/components/LoginDialog.css.ts`:

```ts
import { style } from '@vanilla-extract/css';

export const wrapper = style({
  position: 'fixed',
  top: '50%',
  left: '50%',
  transform: 'translate(-50%, -50%)',
  zIndex: 50,
  width: '100%',
  maxWidth: '24rem',
  paddingInline: '1rem',
});
```

- [ ] **Step 4: Create `LoginDialog.tsx`**

Create `web/src/components/LoginDialog.tsx`:

```tsx
import LoginForm from './LoginForm';
import * as s from './LoginDialog.css';

export default function LoginDialog() {
  return (
    <div role="dialog" aria-label="Sign in" className={s.wrapper}>
      <LoginForm />
    </div>
  );
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `npm test -- src/components/LoginDialog.test.tsx`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add web/src/components/LoginDialog.tsx web/src/components/LoginDialog.css.ts web/src/components/LoginDialog.test.tsx
git commit -m "feat(web): add permanent LoginDialog component"
```

---

### Task 5: Auth-aware `Layout` header

**Files:**
- Modify: `web/src/components/Layout.tsx`
- Modify: `web/src/components/Layout.css.ts` (add `authActions`)
- Test: `web/src/components/Layout.test.tsx` (create)

**Interfaces:**
- Consumes: `useAuth()`; `UserMenu` (unchanged).
- Produces: `Layout` renders the logo always; authenticated → Calendar/Interests/Settings `NavLink`s + `UserMenu`; anonymous (or loading) → "Sign in" (`/login`) and "Sign up" (`/signup`) links.

- [ ] **Step 1: Write the failing test**

Create `web/src/components/Layout.test.tsx`:

```tsx
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import { MemoryRouter, Routes, Route } from 'react-router-dom';

vi.mock('../auth/useAuth', () => ({ useAuth: vi.fn() }));

import { useAuth } from '../auth/useAuth';
import Layout from './Layout';

function renderLayout() {
  return render(
    <MemoryRouter initialEntries={['/calendar']}>
      <Routes>
        <Route path="/" element={<Layout />}>
          <Route path="calendar" element={<div>cal</div>} />
        </Route>
      </Routes>
    </MemoryRouter>,
  );
}

beforeEach(() => {
  vi.resetAllMocks();
});

describe('Layout', () => {
  it('shows nav and account menu when authenticated', () => {
    vi.mocked(useAuth).mockReturnValue({
      status: 'authenticated',
      user: { id: 'u', email: 'a@x' },
      login: vi.fn(),
      signup: vi.fn(),
      logout: vi.fn(),
    });
    renderLayout();
    expect(screen.getByRole('link', { name: /interests/i })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /account menu/i })).toBeInTheDocument();
    expect(screen.queryByRole('link', { name: /^sign in$/i })).not.toBeInTheDocument();
  });

  it('shows sign in and sign up when anonymous', () => {
    vi.mocked(useAuth).mockReturnValue({
      status: 'anonymous',
      user: null,
      login: vi.fn(),
      signup: vi.fn(),
      logout: vi.fn(),
    });
    renderLayout();
    expect(screen.getByRole('link', { name: /sign in/i })).toHaveAttribute('href', '/login');
    expect(screen.getByRole('link', { name: /sign up/i })).toHaveAttribute('href', '/signup');
    expect(screen.queryByRole('link', { name: /interests/i })).not.toBeInTheDocument();
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npm test -- src/components/Layout.test.tsx`
Expected: FAIL — the anonymous case fails (Layout always renders the authed nav today; no "Sign in"/"Sign up" links).

- [ ] **Step 3: Add the `authActions` style**

Append to `web/src/components/Layout.css.ts`:

```ts
export const authActions = style({
  marginLeft: 'auto',
  display: 'flex',
  gap: '0.5rem',
});
```

- [ ] **Step 4: Make `Layout` auth-aware**

Replace the entire contents of `web/src/components/Layout.tsx` with:

```tsx
import { NavLink, Link, Outlet } from 'react-router-dom';
import clsx from 'clsx';
import { useAuth } from '../auth/useAuth';
import UserMenu from './UserMenu';
import * as s from './Layout.css';

const link = ({ isActive }: { isActive: boolean }) =>
  clsx(s.navLink, isActive ? s.navLinkActive : s.navLinkInactive);

export default function Layout() {
  const { status } = useAuth();
  const authed = status === 'authenticated';

  return (
    <div className={s.page}>
      <header className={s.header}>
        <div
          style={{
            backgroundImage: `url('/titleGraphic1.png')`,
            width: '280px',
            height: '40px',
          }}
          className={s.logo}
        />
        {authed ? (
          <>
            <NavLink to="/calendar" className={link}>
              Calendar
            </NavLink>
            <NavLink to="/onboarding" className={link}>
              Interests
            </NavLink>
            <NavLink to="/settings" className={link}>
              Settings
            </NavLink>
            <UserMenu />
          </>
        ) : (
          <div className={s.authActions}>
            <Link to="/login" className={clsx(s.navLink, s.navLinkInactive)}>
              Sign in
            </Link>
            <Link to="/signup" className={clsx(s.navLink, s.navLinkInactive)}>
              Sign up
            </Link>
          </div>
        )}
      </header>
      <main className={s.main}>
        <Outlet />
      </main>
    </div>
  );
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `npm test -- src/components/Layout.test.tsx`
Expected: PASS (both cases).

- [ ] **Step 6: Commit**

```bash
git add web/src/components/Layout.tsx web/src/components/Layout.css.ts web/src/components/Layout.test.tsx
git commit -m "feat(web): auth-aware Layout header"
```

---

### Task 6: `CalendarPage` logged-out mode

**Files:**
- Modify: `web/src/pages/CalendarPage.tsx`
- Test: `web/src/pages/CalendarPage.test.tsx` (add auth mock + logged-out case)

**Interfaces:**
- Consumes: `getCalendar(from, to, loggedOut)` (Task 1), `EventCard` `interactive` prop (Task 2), `LoginDialog` (Task 4), `useAuth()`.
- Produces: `CalendarPage` renders the logged-out calendar (bundled data, non-interactive cards, permanent `LoginDialog`) when `status === 'anonymous'`; a `Spinner` while `status === 'loading'`; today's authenticated behavior otherwise.

- [ ] **Step 1: Write the failing test**

Edit `web/src/pages/CalendarPage.test.tsx`. Add the auth mock near the other `vi.mock` calls (after the `vi.mock('../api/notInterested', …)` block):

```ts
vi.mock('../auth/useAuth', () => ({ useAuth: vi.fn() }));
```

Add to the imports section (with the other `import * as …` lines):

```ts
import { useAuth } from '../auth/useAuth';
```

Replace the existing `beforeEach` block with one that defaults to authenticated:

```ts
beforeEach(() => {
  vi.resetAllMocks();
  vi.mocked(useAuth).mockReturnValue({
    status: 'authenticated',
    user: null,
    login: vi.fn(),
    signup: vi.fn(),
    logout: vi.fn(),
  });
});
```

Add this new test inside the `describe('CalendarPage', …)` block:

```ts
  it('shows the login dialog and non-interactive events when logged out', async () => {
    vi.mocked(useAuth).mockReturnValue({
      status: 'anonymous',
      user: null,
      login: vi.fn(),
      signup: vi.fn(),
      logout: vi.fn(),
    });
    (calApi.getCalendar as ReturnType<typeof vi.fn>).mockResolvedValueOnce([
      {
        id: 'e1',
        title: 'PB Live',
        starts_at: '2026-06-15T20:00:00Z',
        venue: { name: 'The Bowl' },
        score: 0.82,
        matched_because: { performers: ['Phoebe Bridgers'], genres: ['indie'] },
      },
    ]);

    renderPage();

    await waitFor(() => expect(screen.getByText('PB Live')).toBeInTheDocument());
    expect(screen.getByRole('dialog', { name: /sign in/i })).toBeInTheDocument();
    expect(screen.queryByRole('link', { name: /PB Live/i })).not.toBeInTheDocument();
    expect(
      screen.queryByRole('button', { name: /not interested/i }),
    ).not.toBeInTheDocument();
    expect((calApi.getCalendar as ReturnType<typeof vi.fn>).mock.calls[0][2]).toBe(true);
  });
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npm test -- src/pages/CalendarPage.test.tsx`
Expected: FAIL — `CalendarPage` does not yet call `useAuth`, render a `LoginDialog`, mark events non-interactive, or pass a third `getCalendar` argument. (The existing tests may also error until `useAuth` is consumed and the default mock is in place.)

- [ ] **Step 3: Implement logged-out mode in `CalendarPage`**

Replace the entire contents of `web/src/pages/CalendarPage.tsx` with:

```tsx
import { useState } from 'react';
import { useMutation, useQuery, useQueryClient, keepPreviousData } from '@tanstack/react-query';
import { getCalendar, type CalendarEvent } from '../api/calendar';
import { markNotInterested } from '../api/notInterested';
import { useAuth } from '../auth/useAuth';
import EventCard from '../components/EventCard';
import LoginDialog from '../components/LoginDialog';
import Spinner from '../components/Spinner';
import clsx from 'clsx';
import * as s from './CalendarPage.css';
import * as c from '../styles/common.css';

function isoDate(d: Date): string {
  return d.toISOString().slice(0, 10);
}

const RANGE_OPTIONS = [
  { months: 1, label: '1 month' },
  { months: 3, label: '3 months' },
  { months: 6, label: '6 months' },
] as const;

export default function CalendarPage() {
  const qc = useQueryClient();
  const { status } = useAuth();
  const loggedOut = status === 'anonymous';
  const [months, setMonths] = useState(3);

  const today = new Date();
  const end = new Date(today.getFullYear(), today.getMonth() + months, today.getDate());
  const from = isoDate(today);
  const to = isoDate(end);

  const calendarKey = ['calendar', from, to, loggedOut] as const;

  const { data, isLoading, isError } = useQuery<CalendarEvent[]>({
    queryKey: calendarKey,
    queryFn: () => getCalendar(from, to, loggedOut),
    enabled: status !== 'loading',
    placeholderData: keepPreviousData,
  });

  const events = data ?? [];

  const notInterested = useMutation({
    mutationFn: (id: string) => markNotInterested(id),
    onMutate: async (id: string) => {
      await qc.cancelQueries({ queryKey: calendarKey });
      const prev = qc.getQueryData<CalendarEvent[]>(calendarKey);
      qc.setQueryData<CalendarEvent[]>(calendarKey, (old) =>
        (old ?? []).filter((e) => e.id !== id),
      );
      return { prev };
    },
    onError: (_err, _id, ctx) => {
      if (ctx?.prev) qc.setQueryData(calendarKey, ctx.prev);
    },
    onSettled: () => {
      qc.invalidateQueries({ queryKey: ['calendar'] });
    },
  });

  if (status === 'loading') {
    return <Spinner />;
  }

  return (
    <div>
      <header className={s.header}>
        <h1 className={c.pageTitle}>Your matched calendar</h1>
        <div className={s.controls}>
          <span className={s.controlLabel}>Show events for next:</span>
          <div className={s.segment}>
            {RANGE_OPTIONS.map((opt) => {
              const active = opt.months === months;
              return (
                <button
                  key={opt.months}
                  type="button"
                  onClick={() => setMonths(opt.months)}
                  aria-pressed={active}
                  className={clsx(
                    s.rangeButton,
                    active ? s.rangeButtonActive : s.rangeButtonInactive,
                  )}
                >
                  {opt.label}
                </button>
              );
            })}
          </div>
        </div>
      </header>

      {isLoading ? (
        <Spinner />
      ) : isError ? (
        <div className={s.errorBox}>Couldn't load your calendar.</div>
      ) : events.length === 0 ? (
        <div className={s.emptyState}>
          No upcoming matches yet. Add some interests on the{' '}
          <a href="/onboarding" className={s.inlineLink}>
            Interests
          </a>{' '}
          page or wait for the next match run.
        </div>
      ) : (
        <ul className={s.list}>
          {events.map((e, i) => (
            <li key={e.id} className={i > 0 ? s.listItem : undefined}>
              <EventCard
                event={e}
                interactive={!loggedOut}
                onNotInterested={loggedOut ? undefined : (id) => notInterested.mutate(id)}
              />
            </li>
          ))}
        </ul>
      )}

      {loggedOut && <LoginDialog />}
    </div>
  );
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `npm test -- src/pages/CalendarPage.test.tsx`
Expected: PASS — the logged-out case plus all existing cases (matched events, empty state, range toggle, not-interested) pass with the authenticated default mock.

- [ ] **Step 5: Commit**

```bash
git add web/src/pages/CalendarPage.tsx web/src/pages/CalendarPage.test.tsx
git commit -m "feat(web): logged-out mode for CalendarPage"
```

---

### Task 7: Public routing in `App`

**Files:**
- Modify: `web/src/App.tsx`

**Interfaces:**
- Consumes: `Layout` (now public, Task 5), `CalendarPage` (Task 6), `RequireAuth` (unchanged).
- Produces: `/` → `Layout` renders for everyone; `index` → redirect to `/calendar`; `/calendar` is public; `onboarding`, `events/:id`, `settings`, `integrations/spotify/callback` each wrapped in `RequireAuth`. `/login` and `/signup` unchanged.

- [ ] **Step 1: Update `App.tsx`**

Replace the entire contents of `web/src/App.tsx` with:

```tsx
import { Routes, Route, Navigate } from 'react-router-dom';
import RequireAuth from './auth/RequireAuth';
import Layout from './components/Layout';
import LoginPage from './pages/LoginPage';
import SignupPage from './pages/SignupPage';
import OnboardingPage from './pages/OnboardingPage';
import CalendarPage from './pages/CalendarPage';
import EventDetailPage from './pages/EventDetailPage';
import SettingsPage from './pages/SettingsPage';
import SpotifyCallbackPage from './pages/SpotifyCallbackPage';

export default function App() {
  return (
    <Routes>
      <Route path="/login" element={<LoginPage />} />
      <Route path="/signup" element={<SignupPage />} />
      <Route path="/" element={<Layout />}>
        <Route index element={<Navigate to="/calendar" replace />} />
        <Route path="calendar" element={<CalendarPage />} />
        <Route
          path="onboarding"
          element={
            <RequireAuth>
              <OnboardingPage />
            </RequireAuth>
          }
        />
        <Route
          path="events/:id"
          element={
            <RequireAuth>
              <EventDetailPage />
            </RequireAuth>
          }
        />
        <Route
          path="settings"
          element={
            <RequireAuth>
              <SettingsPage />
            </RequireAuth>
          }
        />
        <Route
          path="integrations/spotify/callback"
          element={
            <RequireAuth>
              <SpotifyCallbackPage />
            </RequireAuth>
          }
        />
      </Route>
    </Routes>
  );
}
```

- [ ] **Step 2: Run the full test suite**

Run: `npm test`
Expected: PASS — every test file, including the migrated `CalendarPage.test.tsx` and `LoginPage.test.tsx`.

- [ ] **Step 3: Typecheck, lint, and build**

Run: `npm run lint`
Expected: no errors.
Run: `npm run build`
Expected: `tsc -b` passes (JSON import + all types) and `vite build` succeeds.

- [ ] **Step 4: Manual smoke check (dev server)**

Run: `npm run dev`, then in a browser:
- Visit `/` while logged out → redirects to `/calendar`, shows the bundled events (non-clickable) behind a centered "Sign in" dialog; header shows the logo plus "Sign in"/"Sign up".
- Click an event → nothing navigates. Click "Sign up" in the header → `/signup`.
- Sign in with valid credentials in the dialog → dialog disappears, real calendar loads, header shows the full nav + account menu.
Stop the dev server when done.

- [ ] **Step 5: Commit**

```bash
git add web/src/App.tsx
git commit -m "feat(web): make Layout and /calendar publicly accessible"
```

---

## Notes / accepted trade-offs

- The range toggle stays visible when logged out but does not filter the bundled set (per design — kept simple).
- The bundled `logged-out-calendar-data.json` (~20KB) ships in the client bundle for all users (acceptable; no lazy-load).
- The title-graphic logo appears in both the header and the dialog when logged out (kept for fidelity to `LoginPage`'s contents; trivially removable from `LoginDialog` later).
- `LoginDialog` is a centered fixed card with no dimming scrim; non-clickability of events is enforced by `EventCard interactive={false}`, not by a full-screen overlay (so the header links and range toggle stay usable).
