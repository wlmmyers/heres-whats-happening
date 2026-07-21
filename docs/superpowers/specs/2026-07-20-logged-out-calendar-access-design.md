# Logged-out access to CalendarPage

**Date:** 2026-07-20
**Status:** Approved (design)
**Area:** `web/` frontend (React 19, react-router-dom 7, @tanstack/react-query 5, vanilla-extract)

## Goal

Let anonymous (logged-out) visitors view the `CalendarPage` populated from a
static `logged-out-calendar-data.json` file, with a permanent, centered login
dialog floating over the calendar. Logged-out events are non-interactive. Once
the visitor logs in, the app transparently switches to the authenticated
calendar backed by the real API.

## Decisions (from brainstorming)

1. **Landing:** Anonymous visits to `/` and `/calendar` render the logged-out
   `CalendarPage` with the login dialog overlaid. The standalone `/login` and
   `/signup` pages remain for direct links and the signup flow.
2. **Dialog style:** No dimming scrim — the calendar stays bright behind a solid,
   centered card with a drop shadow (a "teaser"). The dialog is permanent
   (non-dismissible).
3. **Header:** Logo always shown. Anonymous → "Sign in" / "Sign up" links (to
   `/login`, `/signup`). Authenticated → existing Calendar/Interests/Settings nav
   + `UserMenu`.
4. **Range toggle:** Stays visible when logged out but does not filter — the
   logged-out data is returned as a single static set (kept simple, per request).
5. **Data source:** `getCalendar` returns the JSON file's events when logged out.
6. **Interactivity:** Events are not clickable when logged out; no "Not
   interested" action.
7. **Login UI reuse:** The login dialog contains the same contents as
   `LoginPage`, extracted into a shared `LoginForm` (logo kept, per approval).

## Current architecture (baseline)

- `App.tsx`: `/login`, `/signup` are public. Everything under `/` is wrapped in
  `<RequireAuth>` → `<Layout>` (nav + `UserMenu`) → `<Outlet>`. Anonymous users
  hitting `/calendar` are redirected to `/login`.
- `RequireAuth`: `loading` → `Spinner`; `anonymous` → redirect to `/login`;
  otherwise renders children.
- `CalendarPage`: `useQuery(['calendar', from, to], () => getCalendar(from, to))`;
  renders `EventCard`s; owns the `markNotInterested` mutation and the 1/3/6-month
  range toggle. Does **not** currently use `useAuth`.
- `EventCard`: wraps its body in `<Link to="/events/:id">`; shows "Not
  interested" only when an `onNotInterested` prop is passed.
- `LoginPage`: full-viewport centered `card` (logo + sign-in form + "No account?
  Sign up" link). On success: `useAuth().login()` then `navigate(dest)`.
- `ConfirmDialog`: fixed `backdrop` + centered `dialog` with
  `role="dialog" aria-modal="true"` — the existing dialog pattern.
- `useAuth().status`: `'loading' | 'authenticated' | 'anonymous'`.
- `api/calendar.ts`: `getCalendar(from, to)` calls `apiFetch('/me/calendar?…')`,
  returning `{ events: CalendarEvent[] }.events`.
- Data file: `web/src/api/logged-out-calendar-data.json` exists (untracked),
  shape `{ "events": CalendarEvent[] }`, 23 events already matching the
  `CalendarEvent` interface (`id, title, description, starts_at, image_url, url,
  venue, score, matched_because`).

## Target architecture

### 1. Routing — `App.tsx`

Move `Layout` out from behind `RequireAuth` (Layout renders for everyone). Make
the calendar public; guard the other child routes individually so anonymous
users still redirect to `/login` when they attempt them.

```tsx
<Routes>
  <Route path="/login" element={<LoginPage />} />
  <Route path="/signup" element={<SignupPage />} />
  <Route path="/" element={<Layout />}>
    <Route index element={<Navigate to="/calendar" replace />} />
    <Route path="calendar" element={<CalendarPage />} />
    <Route path="onboarding" element={<RequireAuth><OnboardingPage /></RequireAuth>} />
    <Route path="events/:id" element={<RequireAuth><EventDetailPage /></RequireAuth>} />
    <Route path="settings" element={<RequireAuth><SettingsPage /></RequireAuth>} />
    <Route path="integrations/spotify/callback" element={<RequireAuth><SpotifyCallbackPage /></RequireAuth>} />
  </Route>
</Routes>
```

`RequireAuth` is unchanged; it now guards individual elements. While auth is
`loading` it renders `Spinner` as before.

### 2. `CalendarPage.tsx`

- Read `useAuth().status`; `const loggedOut = status === 'anonymous'`.
- If `status === 'loading'`, render `<Spinner />` and do not run the query. This
  prevents an anonymous `/me/calendar` request and avoids flashing the dialog
  before auth resolves.
- Query: `queryKey: ['calendar', from, to, loggedOut]`,
  `queryFn: () => getCalendar(from, to, loggedOut)`. Including `loggedOut` in the
  key makes the calendar refetch real data the instant login flips auth to
  authenticated.
- Logged out: render each `EventCard` with `interactive={false}` and **no**
  `onNotInterested`; render a permanent `<LoginDialog />`. The range toggle stays
  visible (no-op against the static set).
- Authenticated: unchanged from today (interactive cards, `onNotInterested`, no
  dialog).

### 3. `api/calendar.ts`

```ts
import loggedOutData from './logged-out-calendar-data.json';

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

- Ignores `from`/`to` in the logged-out branch (returns the full static set).
- Static JSON import ships the ~20KB file in the client bundle — acceptable for
  this feature. Requires `resolveJsonModule` in `tsconfig.app.json`; verify and
  enable if absent.

### 4. `EventCard.tsx`

Add `interactive?: boolean` (default `true`). When `false`, render the card body
in a plain `<div>` (reusing the existing `s.link` class or an equivalent
non-anchor wrapper) instead of `<Link>`, so it is not clickable and does not
navigate. "Not interested" is already gated on `onNotInterested`, so it remains
hidden logged-out. Default behavior (interactive) is unchanged.

### 5. Login UI: shared `LoginForm`, refactored `LoginPage`, new `LoginDialog`

- **`components/LoginForm.tsx`** (new): the current inner contents of
  `LoginPage`'s card — logo (title graphic), email/password fields, error state,
  submit button, and the "No account? Sign up" link. Owns the email/password
  state and the `useAuth().login()` call. Prop: `onSuccess?: () => void`, invoked
  after a successful login. Reuses the existing `LoginPage.css` styles (shared or
  moved as appropriate).
- **`pages/LoginPage.tsx`** (refactor): renders
  `<div className={s.page}><LoginForm onSuccess={() => navigate(dest, { replace: true })} /></div>`.
  Keeps the `dest`/`location` logic and full-viewport centering. Behavior
  identical to today.
- **`components/LoginDialog.tsx`** (new): a fixed, centered container with **no**
  dimming scrim (solid card, drop shadow) rendering `<LoginForm />` with no
  `onSuccess`. `role="dialog"`, `aria-label="Sign in"`, non-dismissible (no close
  button, no backdrop-close). On successful login, the auth-state flip re-renders
  `CalendarPage`, which unmounts the dialog and refetches real data — the dialog
  does not navigate. New `LoginDialog.css.ts` for the fixed centering.

Note: `LoginForm` keeps the title-graphic logo, so logged-out users see the logo
in both the header and the dialog. Kept for fidelity per approval; trivially
removable from the dialog later.

### 6. `Layout.tsx`

Read `useAuth().status`. Logo always shown. Authenticated → Calendar / Interests
/ Settings `NavLink`s + `UserMenu` (today's behavior). Anonymous (or loading) →
"Sign in" / "Sign up" links to `/login` and `/signup` on the right.

## Data flow: login from the dialog

1. User submits the form in `LoginDialog` → `LoginForm` calls `useAuth().login()`.
2. `AuthContext` stores the access token, sets `user`, sets `status` →
   `'authenticated'`.
3. `CalendarPage` (a `useAuth` consumer) re-renders: `loggedOut = false` → the
   query key changes → refetch of real `/me/calendar`; `EventCard`s become
   interactive; `LoginDialog` is no longer rendered.
4. `Layout` re-renders with the full authenticated nav + `UserMenu`.

## Testing (TDD)

- `getCalendar(from, to, true)` returns the JSON file's events and performs no
  `apiFetch`.
- `EventCard` with `interactive={false}` renders no anchor/link and no "Not
  interested" button; default remains a link.
- `LoginForm`: renders fields, submits (calls `login`), shows the error message
  on `invalid_credentials`. `LoginPage` still redirects to `dest` on success.
  `LoginDialog` renders the sign-in form.
- `CalendarPage`:
  - Logged-out (mock `useAuth` → `anonymous`): renders static events, the login
    dialog, and non-interactive cards; no network call.
  - Authenticated (mock `useAuth` → `authenticated`): existing assertions hold.
  - **Migration:** the existing `CalendarPage.test.tsx` renders `CalendarPage`
    without an `AuthProvider`; since `CalendarPage` now calls `useAuth`, the test
    must provide a mocked `useAuth` (or wrap in `AuthProvider`). Update it.
- `Layout`: shows nav + `UserMenu` when authenticated; "Sign in" / "Sign up" when
  anonymous.

## Out of scope / non-goals

- No date filtering of the logged-out data by the range toggle.
- No lazy-loading of the JSON (static import is acceptable).
- No changes to the auth API, backend, or the `logged-out-calendar-data.json`
  contents.
- No new signup dialog — signup remains the standalone `/signup` page.
