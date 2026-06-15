# Tailwind → vanilla-extract Refactor Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace every Tailwind utility class in the `web/` React app with explicit, hand-editable styles authored in vanilla-extract, extracted at build time by `@vanilla-extract/vite-plugin`, and remove Tailwind entirely.

**Architecture:** A shared tokens module (`styles/theme.ts`) holds color/font/radius/shadow/transition constants (spacing is written inline as literal `rem`). `styles/global.css.ts` reproduces the Tailwind Preflight reset plus the app's global rules. `styles/common.css.ts` holds the de-duplicated multi-use styles (card surface, buttons, inputs, titles). Each component gets a co-located `*.css.ts` of named `style()` objects; `className` strings are swapped for those references. Conditional/active states are composed with `clsx`. The Tailwind global stylesheet stays imported during migration so unconverted components keep working, then is removed in the final task.

**Tech Stack:** React 19, Vite 8, vanilla-extract (`@vanilla-extract/css` + `@vanilla-extract/vite-plugin`), `clsx`, vitest + happy-dom, pnpm.

---

## Conventions used throughout

- Each `.css.ts` file is imported as the same path without the `.ts`: a file `Foo.css.ts` is imported as `./Foo.css`.
- Import aliases in components:
  - `import * as s from './Foo.css';` — the component's own co-located styles.
  - `import * as c from '../styles/common.css';` (pages) or `'./styles/common.css'` as appropriate — shared styles.
  - `import clsx from 'clsx';` — only when composing conditional classNames.
- **Spacing is literal `rem`** at each use site. Tailwind scale → rem:
  `0.5`→`0.125rem`, `1`→`0.25rem`, `1.5`→`0.375rem`, `2`→`0.5rem`, `2.5`→`0.625rem`, `3`→`0.75rem`, `4`→`1rem`, `6`→`1.5rem`, `8`→`2rem`, `12`→`3rem`.
- **`space-y-N`** is reproduced by baking `marginTop` into each non-first child's own style (no `& > * + *` selector). The first child has no top margin.
- **Hover/disabled/focus** → vanilla-extract pseudo-selectors `':hover'` / `':disabled'`.
- **Responsive** (`md:` = `min-width: 768px`) → `'@media': { 'screen and (min-width: 768px)': { ... } }`.
- Font sizes are stored as spreadable objects (`{ fontSize, lineHeight }`); use `...fontSize.lg` inside a `style({...})`.

## File structure

```
web/src/
  styles/
    theme.ts            # NEW — tokens (no spacing)
    global.css.ts       # NEW — reset + app globals (replaces styles.css)
    common.css.ts       # NEW — surface, buttons, input, titles, errorText
  components/
    Spinner.css.ts        Spinner.tsx
    TagInput.css.ts       TagInput.tsx
    ConfirmDialog.css.ts  ConfirmDialog.tsx
    EventCard.css.ts      EventCard.tsx
    UserMenu.css.ts       UserMenu.tsx
    Layout.css.ts         Layout.tsx
  pages/
    LoginPage.css.ts      LoginPage.tsx
    SignupPage.css.ts     SignupPage.tsx
    OnboardingPage.css.ts OnboardingPage.tsx
    CalendarPage.css.ts   CalendarPage.tsx
    EventDetailPage.css.ts EventDetailPage.tsx
    SpotifyCallbackPage.css.ts SpotifyCallbackPage.tsx
    SettingsPage.css.ts   SettingsPage.tsx
  main.tsx              # import swap
  styles.css            # DELETED in final task
web/vite.config.ts      # add vanillaExtractPlugin()
web/postcss.config.js   # DELETED in final task
web/package.json        # deps changed
```

All commands run from `web/` (`cd web` first). Tests are class-agnostic (role/text based), so every task's gate is "the existing suite stays green."

---

### Task 1: Add dependencies and wire the Vite plugin

**Files:**
- Modify: `web/package.json`
- Modify: `web/vite.config.ts`

- [ ] **Step 1: Install runtime + build deps**

Run (from `web/`):
```bash
pnpm add @vanilla-extract/css clsx
pnpm add -D @vanilla-extract/vite-plugin
```
Expected: `package.json` gains `@vanilla-extract/css` + `clsx` under `dependencies` and `@vanilla-extract/vite-plugin` under `devDependencies`; pnpm completes with no errors. (Leave `tailwindcss` / `@tailwindcss/postcss` in place for now.)

- [ ] **Step 2: Register the plugin in Vite**

Edit `web/vite.config.ts` — add the import and put `vanillaExtractPlugin()` first in `plugins`:
```ts
import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import { vanillaExtractPlugin } from '@vanilla-extract/vite-plugin';

export default defineConfig({
  plugins: [vanillaExtractPlugin(), react()],
  server: {
    port: 5173,
    host: '127.0.0.1',
    strictPort: true,
    proxy: {
      '/api': {
        target: 'http://localhost:8080',
        changeOrigin: true,
        rewrite: (path) => path.replace(/^\/api/, ''),
      },
    },
  },
  build: {
    outDir: 'dist',
    sourcemap: true,
  },
});
```

- [ ] **Step 3: Verify the suite still passes (plugin is inert with no `.css.ts` yet)**

Run: `pnpm test`
Expected: `Test Files 13 passed (13)`, `Tests 42 passed`.

- [ ] **Step 4: Commit**

```bash
git add web/package.json web/pnpm-lock.yaml web/vite.config.ts
git commit -m "build(web): add vanilla-extract + clsx, register vite plugin"
```

---

### Task 2: Create the tokens module `styles/theme.ts`

**Files:**
- Create: `web/src/styles/theme.ts`

- [ ] **Step 1: Write the full tokens file**

Create `web/src/styles/theme.ts`:
```ts
// Design tokens extracted from Tailwind's default theme. Spacing is intentionally
// NOT tokenized — padding/margin/gap are written as literal rem values at each
// use site. Font sizes are spreadable objects: `style({ ...fontSize.lg })`.

export const color = {
  white: '#ffffff',
  black: '#000000',
  blackA40: 'rgb(0 0 0 / 0.4)',
  gray50: '#f9fafb',
  gray100: '#f3f4f6',
  gray200: '#e5e7eb',
  gray500: '#6b7280',
  gray600: '#4b5563',
  gray700: '#374151',
  gray800: '#1f2937',
  gray900: '#111827',
  blue50: '#eff6ff',
  blue100: '#dbeafe',
  blue500: '#3b82f6',
  blue600: '#2563eb',
  blue700: '#1d4ed8',
  blue800: '#1e40af',
  blue900: '#1e3a8a',
  green600: '#16a34a',
  green700: '#15803d',
  red600: '#dc2626',
} as const;

export const fontSize = {
  xs: { fontSize: '0.75rem', lineHeight: '1rem' },
  sm: { fontSize: '0.875rem', lineHeight: '1.25rem' },
  base: { fontSize: '1rem', lineHeight: '1.5rem' },
  lg: { fontSize: '1.125rem', lineHeight: '1.75rem' },
  xl: { fontSize: '1.25rem', lineHeight: '1.75rem' },
  '2xl': { fontSize: '1.5rem', lineHeight: '2rem' },
  '3xl': { fontSize: '1.875rem', lineHeight: '2.25rem' },
} as const;

export const fontWeight = {
  medium: 500,
  semibold: 600,
  bold: 700,
} as const;

export const radius = {
  sm: '0.25rem',
  md: '0.375rem',
  full: '9999px',
} as const;

export const shadow = {
  sm: '0 1px 2px 0 rgb(0 0 0 / 0.05)',
  base: '0 1px 3px 0 rgb(0 0 0 / 0.1), 0 1px 2px -1px rgb(0 0 0 / 0.1)',
  md: '0 4px 6px -1px rgb(0 0 0 / 0.1), 0 2px 4px -2px rgb(0 0 0 / 0.1)',
  lg: '0 10px 15px -3px rgb(0 0 0 / 0.1), 0 4px 6px -4px rgb(0 0 0 / 0.1)',
} as const;

// Tailwind's `transition` utility. Spread into a style: `style({ ...transition })`.
export const transition = {
  transitionProperty:
    'color, background-color, border-color, text-decoration-color, fill, stroke, opacity, box-shadow, transform, filter, backdrop-filter',
  transitionTimingFunction: 'cubic-bezier(0.4, 0, 0.2, 1)',
  transitionDuration: '150ms',
} as const;
```

- [ ] **Step 2: Typecheck**

Run: `pnpm exec tsc -b`
Expected: completes with no errors.

- [ ] **Step 3: Commit**

```bash
git add web/src/styles/theme.ts
git commit -m "style(web): add vanilla-extract design tokens"
```

---

### Task 3: Create `styles/global.css.ts` (reset + globals) and import it

**Files:**
- Create: `web/src/styles/global.css.ts`
- Modify: `web/src/main.tsx`

> `styles.css` (with the Tailwind import) stays imported alongside for now so unconverted components keep their utilities. Both are removed in Task 18.

- [ ] **Step 1: Write the global stylesheet**

Create `web/src/styles/global.css.ts`:
```ts
import { globalStyle } from '@vanilla-extract/css';

// --- Preflight-equivalent reset (the part of Tailwind that is not a className) ---
globalStyle('*, ::before, ::after', {
  boxSizing: 'border-box',
  borderWidth: 0,
  borderStyle: 'solid',
  borderColor: 'currentColor',
});

globalStyle('body', {
  margin: 0,
  // App global: font carried over from the old styles.css.
  fontFamily: "'Raleway', sans-serif",
  fontOpticalSizing: 'auto',
});

globalStyle('h1, h2, h3, h4, h5, h6', {
  fontSize: 'inherit',
  fontWeight: 'inherit',
  margin: 0,
});

globalStyle('p, figure, blockquote', { margin: 0 });

globalStyle('a', { color: 'inherit', textDecoration: 'inherit' });

globalStyle('button, input, optgroup, select, textarea', {
  font: 'inherit',
  color: 'inherit',
  margin: 0,
});

// App global: carried over from styles.css (cursor + weight) merged with the reset.
globalStyle('button', {
  cursor: 'pointer',
  fontWeight: 600,
  backgroundColor: 'transparent',
  backgroundImage: 'none',
});

globalStyle('img, svg, video, canvas', {
  display: 'block',
  maxWidth: '100%',
  height: 'auto',
});

globalStyle('ol, ul', { listStyle: 'none', margin: 0, padding: 0 });

globalStyle('hr', { borderTopWidth: '1px' });

globalStyle('code, pre', { fontFamily: 'ui-monospace, monospace' });
```

- [ ] **Step 2: Import it in `main.tsx` (keep the Tailwind sheet for now)**

Edit `web/src/main.tsx` — add the global import directly after the existing `import './styles.css';` line:
```ts
import './styles.css';
import './styles/global.css.ts';
```

- [ ] **Step 3: Verify build + tests**

Run: `pnpm test`
Expected: `Tests 42 passed`. (vanilla-extract now actually transforms a `.css.ts`; confirm the transform works under vitest.)

- [ ] **Step 4: Commit**

```bash
git add web/src/styles/global.css.ts web/src/main.tsx
git commit -m "style(web): add reset + global styles in vanilla-extract"
```

---

### Task 4: Create `styles/common.css.ts` (shared styles)

**Files:**
- Create: `web/src/styles/common.css.ts`

- [ ] **Step 1: Write the shared styles**

Create `web/src/styles/common.css.ts`:
```ts
import { style } from '@vanilla-extract/css';
import { color, radius, shadow, fontSize, fontWeight } from './theme';

// bg-white shadow rounded
export const surface = style({
  backgroundColor: color.white,
  boxShadow: shadow.base,
  borderRadius: radius.sm,
});

// bg-blue-600 hover:bg-blue-700 text-white rounded px-4 py-2 (+ disabled:opacity-60)
export const buttonPrimary = style({
  backgroundColor: color.blue600,
  color: color.white,
  borderRadius: radius.sm,
  paddingInline: '1rem',
  paddingBlock: '0.5rem',
  selectors: {
    '&:hover': { backgroundColor: color.blue700 },
    '&:disabled': { opacity: 0.6 },
  },
});

// border rounded px-4 py-2 hover:bg-gray-50 (+ disabled:opacity-60)
export const buttonSecondary = style({
  borderWidth: '1px',
  borderStyle: 'solid',
  borderRadius: radius.sm,
  paddingInline: '1rem',
  paddingBlock: '0.5rem',
  selectors: {
    '&:hover': { backgroundColor: color.gray50 },
    '&:disabled': { opacity: 0.6 },
  },
});

// w-full ... rounded py-2 disabled:opacity-50 (auth submit buttons)
export const buttonSubmit = style({
  width: '100%',
  backgroundColor: color.blue600,
  color: color.white,
  borderRadius: radius.sm,
  paddingBlock: '0.5rem',
  selectors: {
    '&:hover': { backgroundColor: color.blue700 },
    '&:disabled': { opacity: 0.5 },
  },
});

// mt-1 w-full border rounded px-2 py-1.5 (text fields)
export const textInput = style({
  marginTop: '0.25rem',
  width: '100%',
  borderWidth: '1px',
  borderStyle: 'solid',
  borderRadius: radius.sm,
  paddingInline: '0.5rem',
  paddingBlock: '0.375rem',
});

// text-2xl font-semibold
export const pageTitle = style({ ...fontSize['2xl'], fontWeight: fontWeight.semibold });

// text-lg font-medium
export const sectionTitle = style({ ...fontSize.lg, fontWeight: fontWeight.medium });

// text-red-600 text-sm
export const errorText = style({ ...fontSize.sm, color: color.red600 });
```

> Note: vanilla-extract requires `&:hover`/`&:disabled` to live under `selectors`, not as bare `':hover'` keys, **when** combined with other simple pseudo logic — both forms are valid, but this plan uses the `selectors` form consistently for pseudo-classes that pair with `:disabled`.

- [ ] **Step 2: Typecheck**

Run: `pnpm exec tsc -b`
Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add web/src/styles/common.css.ts
git commit -m "style(web): add shared vanilla-extract styles (surface, buttons, input, titles)"
```

---

### Task 5: Convert `Spinner.tsx`

**Files:**
- Create: `web/src/components/Spinner.css.ts`
- Modify: `web/src/components/Spinner.tsx`
- Test: `web/src/components/EventCard.test.tsx` / `CalendarPage.test.tsx` exercise Spinner indirectly; gate on full suite.

- [ ] **Step 1: Create `Spinner.css.ts`**
```ts
import { style } from '@vanilla-extract/css';
import { color } from '../styles/theme';

// flex items-center justify-center p-8 text-gray-500
export const root = style({
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'center',
  padding: '2rem',
  color: color.gray500,
});
```

- [ ] **Step 2: Swap classNames in `Spinner.tsx`**

Add `import * as s from './Spinner.css';` at the top. Replace:
- `className="flex items-center justify-center p-8 text-gray-500"` → `className={s.root}`

- [ ] **Step 3: Run tests**

Run: `pnpm test`
Expected: `Tests 42 passed`.

- [ ] **Step 4: Commit**
```bash
git add web/src/components/Spinner.css.ts web/src/components/Spinner.tsx
git commit -m "style(web): migrate Spinner to vanilla-extract"
```

---

### Task 6: Convert `TagInput.tsx`

**Files:**
- Create: `web/src/components/TagInput.css.ts`
- Modify: `web/src/components/TagInput.tsx`
- Test: `web/src/components/TagInput.test.tsx`

- [ ] **Step 1: Create `TagInput.css.ts`**
```ts
import { style } from '@vanilla-extract/css';
import { color, radius, fontSize } from '../styles/theme';

// border rounded p-2 flex flex-wrap gap-2 items-center
export const wrapper = style({
  borderWidth: '1px',
  borderStyle: 'solid',
  borderRadius: radius.sm,
  padding: '0.5rem',
  display: 'flex',
  flexWrap: 'wrap',
  gap: '0.5rem',
  alignItems: 'center',
});

// inline-flex items-center bg-blue-100 text-blue-800 rounded-full px-3 py-1 text-sm
export const tag = style({
  display: 'inline-flex',
  alignItems: 'center',
  backgroundColor: color.blue100,
  color: color.blue800,
  borderRadius: radius.full,
  paddingInline: '0.75rem',
  paddingBlock: '0.25rem',
  ...fontSize.sm,
});

// ml-2 text-blue-700 hover:text-red-600
export const removeButton = style({
  marginLeft: '0.5rem',
  color: color.blue700,
  ':hover': { color: color.red600 },
});

// flex-1 min-w-[120px] border-0 outline-none p-1 text-sm
export const input = style({
  flex: '1 1 0%',
  minWidth: '120px',
  borderWidth: 0,
  outline: 'none',
  padding: '0.25rem',
  ...fontSize.sm,
});
```

- [ ] **Step 2: Swap classNames in `TagInput.tsx`**

Add `import * as s from './TagInput.css';`. Replace:
- wrapper div `className="border rounded p-2 flex flex-wrap gap-2 items-center"` → `className={s.wrapper}`
- tag span `className="inline-flex items-center bg-blue-100 text-blue-800 rounded-full px-3 py-1 text-sm"` → `className={s.tag}`
- remove button `className="ml-2 text-blue-700 hover:text-red-600"` → `className={s.removeButton}`
- input `className="flex-1 min-w-[120px] border-0 outline-none p-1 text-sm"` → `className={s.input}`

- [ ] **Step 3: Run tests**

Run: `pnpm test src/components/TagInput.test.tsx`
Expected: `Tests 3 passed`.

- [ ] **Step 4: Commit**
```bash
git add web/src/components/TagInput.css.ts web/src/components/TagInput.tsx
git commit -m "style(web): migrate TagInput to vanilla-extract"
```

---

### Task 7: Convert `ConfirmDialog.tsx`

**Files:**
- Create: `web/src/components/ConfirmDialog.css.ts`
- Modify: `web/src/components/ConfirmDialog.tsx`
- Test: `web/src/components/ConfirmDialog.test.tsx`

- [ ] **Step 1: Create `ConfirmDialog.css.ts`**
```ts
import { style } from '@vanilla-extract/css';
import { sectionTitle } from '../styles/common.css';
import { color, radius, shadow, fontSize } from '../styles/theme';

// fixed inset-0 z-50 flex items-center justify-center bg-black/40
export const backdrop = style({
  position: 'fixed',
  inset: 0,
  zIndex: 50,
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'center',
  backgroundColor: color.blackA40,
});

// w-[400px] max-w-[90%] rounded bg-white p-6 shadow-lg
export const dialog = style({
  width: '400px',
  maxWidth: '90%',
  borderRadius: radius.sm,
  backgroundColor: color.white,
  padding: '1.5rem',
  boxShadow: shadow.lg,
});

// mb-2 text-lg font-medium  (= sectionTitle + mb-2)
export const title = style([sectionTitle, { marginBottom: '0.5rem' }]);

// text-sm text-gray-700
export const message = style({ ...fontSize.sm, color: color.gray700 });

// mt-6 flex justify-center gap-3
export const actions = style({
  marginTop: '1.5rem',
  display: 'flex',
  justifyContent: 'center',
  gap: '0.75rem',
});
```

- [ ] **Step 2: Swap classNames in `ConfirmDialog.tsx`**

Add `import * as s from './ConfirmDialog.css';` and `import * as c from '../styles/common.css';`. Replace:
- backdrop div `className="fixed inset-0 z-50 flex items-center justify-center bg-black/40"` → `className={s.backdrop}`
- dialog div `className="w-[400px] max-w-[90%] rounded bg-white p-6 shadow-lg"` → `className={s.dialog}`
- h2 `className="mb-2 text-lg font-medium"` → `className={s.title}`
- p `className="text-sm text-gray-700"` → `className={s.message}`
- actions div `className="mt-6 flex justify-center gap-3"` → `className={s.actions}`
- cancel button `className="border rounded px-4 py-2 hover:bg-gray-50"` → `className={c.buttonSecondary}`
- confirm button `className="bg-blue-600 hover:bg-blue-700 text-white rounded px-4 py-2"` → `className={c.buttonPrimary}`

- [ ] **Step 3: Run tests**

Run: `pnpm test src/components/ConfirmDialog.test.tsx`
Expected: `Tests 4 passed`.

- [ ] **Step 4: Commit**
```bash
git add web/src/components/ConfirmDialog.css.ts web/src/components/ConfirmDialog.tsx
git commit -m "style(web): migrate ConfirmDialog to vanilla-extract"
```

---

### Task 8: Convert `EventCard.tsx`

**Files:**
- Create: `web/src/components/EventCard.css.ts`
- Modify: `web/src/components/EventCard.tsx`
- Test: `web/src/components/EventCard.test.tsx`

- [ ] **Step 1: Create `EventCard.css.ts`**
```ts
import { style } from '@vanilla-extract/css';
import { surface } from '../styles/common.css';
import { color, fontSize, fontWeight, shadow, transition } from '../styles/theme';

// bg-white shadow rounded p-4 hover:shadow-md transition  (= surface + ...)
export const card = style([
  surface,
  {
    padding: '1rem',
    ...transition,
    ':hover': { boxShadow: shadow.md },
  },
]);

// block
export const link = style({ display: 'block' });

// flex items-baseline justify-between gap-4
export const titleRow = style({
  display: 'flex',
  alignItems: 'baseline',
  justifyContent: 'space-between',
  gap: '1rem',
});

// text-lg font-semibold text-gray-900
export const title = style({
  ...fontSize.lg,
  fontWeight: fontWeight.semibold,
  color: color.gray900,
});

// text-sm text-gray-500 whitespace-nowrap
export const score = style({
  ...fontSize.sm,
  color: color.gray500,
  whiteSpace: 'nowrap',
});

// text-sm text-gray-700 mt-1
export const date = style({ ...fontSize.sm, color: color.gray700, marginTop: '0.25rem' });

// text-xs text-blue-700 mt-2
export const matched = style({ ...fontSize.xs, color: color.blue700, marginTop: '0.5rem' });

// mt-3 flex justify-end
export const notInterestedRow = style({
  marginTop: '0.75rem',
  display: 'flex',
  justifyContent: 'flex-end',
});

// text-sm text-gray-500 hover:text-red-600
export const notInterestedButton = style({
  ...fontSize.sm,
  color: color.gray500,
  ':hover': { color: color.red600 },
});
```

- [ ] **Step 2: Swap classNames in `EventCard.tsx`**

Add `import * as s from './EventCard.css';`. Replace:
- root div `className="bg-white shadow rounded p-4 hover:shadow-md transition"` → `className={s.card}`
- Link `className="block"` → `className={s.link}`
- title row div `className="flex items-baseline justify-between gap-4"` → `className={s.titleRow}`
- h3 `className="text-lg font-semibold text-gray-900"` → `className={s.title}`
- score span `className="text-sm text-gray-500 whitespace-nowrap"` → `className={s.score}`
- date div `className="text-sm text-gray-700 mt-1"` → `className={s.date}`
- matched div `className="text-xs text-blue-700 mt-2"` → `className={s.matched}`
- not-interested wrapper `className="mt-3 flex justify-end"` → `className={s.notInterestedRow}`
- not-interested button `className="text-sm text-gray-500 hover:text-red-600"` → `className={s.notInterestedButton}`

- [ ] **Step 3: Run tests**

Run: `pnpm test src/components/EventCard.test.tsx`
Expected: `Tests 3 passed`.

- [ ] **Step 4: Commit**
```bash
git add web/src/components/EventCard.css.ts web/src/components/EventCard.tsx
git commit -m "style(web): migrate EventCard to vanilla-extract"
```

---

### Task 9: Convert `UserMenu.tsx`

**Files:**
- Create: `web/src/components/UserMenu.css.ts`
- Modify: `web/src/components/UserMenu.tsx`
- Test: covered by full suite (no dedicated test); gate on `pnpm test`.

- [ ] **Step 1: Create `UserMenu.css.ts`**
```ts
import { style } from '@vanilla-extract/css';
import { color, radius, shadow, fontSize, fontWeight } from '../styles/theme';

// relative ml-auto
export const root = style({ position: 'relative', marginLeft: 'auto' });

// w-8 h-8 rounded-full bg-blue-500 text-white text-sm font-semibold flex items-center justify-center cursor-pointer
export const avatar = style({
  width: '2rem',
  height: '2rem',
  borderRadius: radius.full,
  backgroundColor: color.blue500,
  color: color.white,
  ...fontSize.sm,
  fontWeight: fontWeight.semibold,
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'center',
  cursor: 'pointer',
});

// fixed inset-0 z-10
export const overlay = style({ position: 'fixed', inset: 0, zIndex: 10 });

// absolute top-full right-0 mt-1 z-20 min-w-max rounded border border-gray-200 bg-white px-3 py-2 shadow-md
export const dropdown = style({
  position: 'absolute',
  top: '100%',
  right: 0,
  marginTop: '0.25rem',
  zIndex: 20,
  minWidth: 'max-content',
  borderRadius: radius.sm,
  borderWidth: '1px',
  borderStyle: 'solid',
  borderColor: color.gray200,
  backgroundColor: color.white,
  paddingInline: '0.75rem',
  paddingBlock: '0.5rem',
  boxShadow: shadow.md,
});

// mb-2 text-xs text-gray-500
export const email = style({ marginBottom: '0.5rem', ...fontSize.xs, color: color.gray500 });

// mb-2 border-gray-100
export const divider = style({ marginBottom: '0.5rem', borderColor: color.gray100 });

// cursor-pointer border-0 bg-transparent p-0 text-sm text-gray-700
export const signOut = style({
  cursor: 'pointer',
  borderWidth: 0,
  backgroundColor: 'transparent',
  padding: 0,
  ...fontSize.sm,
  color: color.gray700,
});
```

- [ ] **Step 2: Swap classNames in `UserMenu.tsx`**

Add `import * as s from './UserMenu.css';`. Replace:
- root div `className="relative ml-auto"` → `className={s.root}`
- avatar button `className="w-8 h-8 rounded-full bg-blue-500 text-white text-sm font-semibold flex items-center justify-center cursor-pointer"` → `className={s.avatar}`
- overlay div `className="fixed inset-0 z-10"` → `className={s.overlay}`
- dropdown div `className="absolute top-full right-0 mt-1 z-20 min-w-max rounded border border-gray-200 bg-white px-3 py-2 shadow-md"` → `className={s.dropdown}`
- email p `className="mb-2 text-xs text-gray-500"` → `className={s.email}`
- hr `className="mb-2 border-gray-100"` → `className={s.divider}`
- sign-out button `className="cursor-pointer border-0 bg-transparent p-0 text-sm text-gray-700"` → `className={s.signOut}`

- [ ] **Step 3: Run tests**

Run: `pnpm test`
Expected: `Tests 42 passed`.

- [ ] **Step 4: Commit**
```bash
git add web/src/components/UserMenu.css.ts web/src/components/UserMenu.tsx
git commit -m "style(web): migrate UserMenu to vanilla-extract"
```

---

### Task 10: Convert `Layout.tsx` (incl. NavLink active state via clsx)

**Files:**
- Create: `web/src/components/Layout.css.ts`
- Modify: `web/src/components/Layout.tsx`
- Test: covered by full suite; gate on `pnpm test`.

- [ ] **Step 1: Create `Layout.css.ts`**
```ts
import { style } from '@vanilla-extract/css';
import { color, radius } from '../styles/theme';

// min-h-screen bg-gray-50
export const page = style({ minHeight: '100vh', backgroundColor: color.gray50 });

// bg-white border-b px-4 py-3 flex items-center gap-2
export const header = style({
  backgroundColor: color.white,
  borderBottomWidth: '1px',
  paddingInline: '1rem',
  paddingBlock: '0.75rem',
  display: 'flex',
  alignItems: 'center',
  gap: '0.5rem',
});

// bg-cover bg-center bg-no-repeat (logo div; backgroundImage stays inline)
export const logo = style({
  backgroundSize: 'cover',
  backgroundPosition: 'center',
  backgroundRepeat: 'no-repeat',
});

// max-w-5xl mx-auto px-4 py-6
export const main = style({
  maxWidth: '64rem',
  marginInline: 'auto',
  paddingInline: '1rem',
  paddingBlock: '1.5rem',
});

// NavLink base: px-3 py-2 rounded
export const navLink = style({
  paddingInline: '0.75rem',
  paddingBlock: '0.5rem',
  borderRadius: radius.sm,
});

// active: bg-blue-100 text-blue-800
export const navLinkActive = style({ backgroundColor: color.blue100, color: color.blue800 });

// inactive: text-gray-700 hover:bg-gray-100
export const navLinkInactive = style({
  color: color.gray700,
  ':hover': { backgroundColor: color.gray100 },
});
```

- [ ] **Step 2: Rewrite `Layout.tsx` styling**

Replace imports/`link` helper and classNames:
```ts
import { NavLink, Outlet } from 'react-router-dom';
import clsx from 'clsx';
import UserMenu from './UserMenu';
import * as s from './Layout.css';

const link = ({ isActive }: { isActive: boolean }) =>
  clsx(s.navLink, isActive ? s.navLinkActive : s.navLinkInactive);
```
Then swap in the JSX:
- outer div `className="min-h-screen bg-gray-50"` → `className={s.page}`
- header `className="bg-white border-b px-4 py-3 flex items-center gap-2"` → `className={s.header}`
- logo div `className="bg-cover bg-center bg-no-repeat"` → `className={s.logo}` (keep the inline `style={{ backgroundImage, width, height }}`)
- main `className="max-w-5xl mx-auto px-4 py-6"` → `className={s.main}`
- (the three `<NavLink className={link}>` stay as-is — `link` now returns the composed class.)

- [ ] **Step 3: Run tests**

Run: `pnpm test`
Expected: `Tests 42 passed`.

- [ ] **Step 4: Commit**
```bash
git add web/src/components/Layout.css.ts web/src/components/Layout.tsx
git commit -m "style(web): migrate Layout + nav active state to vanilla-extract"
```

---

### Task 11: Convert `LoginPage.tsx` (with the noted cleanup)

**Files:**
- Create: `web/src/pages/LoginPage.css.ts`
- Modify: `web/src/pages/LoginPage.tsx`
- Test: `web/src/pages/LoginPage.test.tsx`

> Cleanup: the outer `<div>` currently carries conflicting `bg-gray-50`+`bg-white` and `shadow rounded` on the full-screen container. Resolve to match `SignupPage`: the page background/centering stays on the outer div, and the card (`surface`) moves to the `<form>`.

- [ ] **Step 1: Create `LoginPage.css.ts`**
```ts
import { style } from '@vanilla-extract/css';
import { surface } from '../styles/common.css';
import { color, fontSize, fontWeight } from '../styles/theme';

// min-h-screen bg-gray-50 flex flex-col items-center justify-center px-4  (md: row + gap-8)
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

// mb-8 (logo image)
export const logo = style({ marginBottom: '2rem' });

// w-full max-w-sm p-6 space-y-4  (+ surface card, matching SignupPage)
export const form = style([
  surface,
  { width: '100%', maxWidth: '24rem', padding: '1.5rem' },
]);

// text-xl font-semibold
export const title = style({ ...fontSize.xl, fontWeight: fontWeight.semibold });

// block text-sm  (+ space-y-4 marginTop)
export const field = style({ display: 'block', ...fontSize.sm, marginTop: '1rem' });

// text-gray-700 (label text)
export const fieldLabel = style({ color: color.gray700 });

// text-sm text-gray-600  (+ space-y-4 marginTop)
export const switchText = style({ ...fontSize.sm, color: color.gray600, marginTop: '1rem' });

// text-blue-600 hover:underline
export const switchLink = style({
  color: color.blue600,
  ':hover': { textDecorationLine: 'underline' },
});
```
Also create the composed error/submit using common in this same file:
```ts
import { errorText, buttonSubmit } from '../styles/common.css';

// text-red-600 text-sm  (+ space-y-4 marginTop)
export const error = style([errorText, { marginTop: '1rem' }]);

// w-full ... py-2 disabled:opacity-50  (+ space-y-4 marginTop)
export const submit = style([buttonSubmit, { marginTop: '1rem' }]);
```
(Merge the two import groups from `../styles/common.css` into one import line: `import { surface, errorText, buttonSubmit } from '../styles/common.css';`.)

- [ ] **Step 2: Swap classNames in `LoginPage.tsx`**

Add `import * as s from './LoginPage.css';` (and `import * as c from '../styles/common.css';` for the text inputs). Replace:
- outer div `className="min-h-screen bg-gray-50 flex flex-col md:flex-row items-center justify-center px-4 bg-white shadow rounded md:gap-8"` → `className={s.page}`
- logo `<img>` `className="mb-8"` → `className={s.logo}` (keep `style={{ width: '300px' }}`)
- form `className="w-full max-w-sm p-6 space-y-4"` → `className={s.form}`
- h1 `className="text-xl font-semibold"` → `className={s.title}`
- both labels `className="block text-sm"` → `className={s.field}`
- both label spans `className="text-gray-700"` → `className={s.fieldLabel}`
- both inputs `className="mt-1 w-full border rounded px-2 py-1.5"` → `className={c.textInput}`
- error div `className="text-red-600 text-sm"` → `className={s.error}`
- submit button `className="w-full bg-blue-600 hover:bg-blue-700 text-white rounded py-2 disabled:opacity-50"` → `className={s.submit}`
- p `className="text-sm text-gray-600"` → `className={s.switchText}`
- Link `className="text-blue-600 hover:underline"` → `className={s.switchLink}`

- [ ] **Step 3: Run tests**

Run: `pnpm test src/pages/LoginPage.test.tsx`
Expected: `Tests 2 passed`.

- [ ] **Step 4: Commit**
```bash
git add web/src/pages/LoginPage.css.ts web/src/pages/LoginPage.tsx
git commit -m "style(web): migrate LoginPage to vanilla-extract (+ resolve bg/card conflict)"
```

---

### Task 12: Convert `SignupPage.tsx`

**Files:**
- Create: `web/src/pages/SignupPage.css.ts`
- Modify: `web/src/pages/SignupPage.tsx`
- Test: `web/src/pages/SignupPage.test.tsx`

- [ ] **Step 1: Create `SignupPage.css.ts`**
```ts
import { style } from '@vanilla-extract/css';
import { surface, errorText, buttonSubmit } from '../styles/common.css';
import { color, fontSize, fontWeight } from '../styles/theme';

// min-h-screen bg-gray-50 flex items-center justify-center px-4
export const page = style({
  minHeight: '100vh',
  backgroundColor: color.gray50,
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'center',
  paddingInline: '1rem',
});

// w-full max-w-sm bg-white shadow rounded p-6 space-y-4
export const form = style([
  surface,
  { width: '100%', maxWidth: '24rem', padding: '1.5rem' },
]);

// text-xl font-semibold
export const title = style({ ...fontSize.xl, fontWeight: fontWeight.semibold });

// block text-sm  (+ space-y-4 marginTop)
export const field = style({ display: 'block', ...fontSize.sm, marginTop: '1rem' });

// text-gray-700
export const fieldLabel = style({ color: color.gray700 });

// text-red-600 text-sm  (+ space-y-4 marginTop)
export const error = style([errorText, { marginTop: '1rem' }]);

// w-full ... py-2 disabled:opacity-50  (+ space-y-4 marginTop)
export const submit = style([buttonSubmit, { marginTop: '1rem' }]);

// text-sm text-gray-600  (+ space-y-4 marginTop)
export const switchText = style({ ...fontSize.sm, color: color.gray600, marginTop: '1rem' });

// text-blue-600 hover:underline
export const switchLink = style({
  color: color.blue600,
  ':hover': { textDecorationLine: 'underline' },
});
```

- [ ] **Step 2: Swap classNames in `SignupPage.tsx`**

Add `import * as s from './SignupPage.css';` and `import * as c from '../styles/common.css';`. Replace:
- outer div `className="min-h-screen bg-gray-50 flex items-center justify-center px-4"` → `className={s.page}`
- form `className="w-full max-w-sm bg-white shadow rounded p-6 space-y-4"` → `className={s.form}`
- h1 `className="text-xl font-semibold"` → `className={s.title}`
- both labels `className="block text-sm"` → `className={s.field}`
- both label spans `className="text-gray-700"` → `className={s.fieldLabel}`
- both inputs `className="mt-1 w-full border rounded px-2 py-1.5"` → `className={c.textInput}`
- error div `className="text-red-600 text-sm"` → `className={s.error}`
- submit button `className="w-full bg-blue-600 hover:bg-blue-700 text-white rounded py-2 disabled:opacity-50"` → `className={s.submit}`
- p `className="text-sm text-gray-600"` → `className={s.switchText}`
- Link `className="text-blue-600 hover:underline"` → `className={s.switchLink}`

- [ ] **Step 3: Run tests**

Run: `pnpm test src/pages/SignupPage.test.tsx`
Expected: `Tests 2 passed`.

- [ ] **Step 4: Commit**
```bash
git add web/src/pages/SignupPage.css.ts web/src/pages/SignupPage.tsx
git commit -m "style(web): migrate SignupPage to vanilla-extract"
```

---

### Task 13: Convert `OnboardingPage.tsx`

**Files:**
- Create: `web/src/pages/OnboardingPage.css.ts`
- Modify: `web/src/pages/OnboardingPage.tsx`
- Test: `web/src/pages/OnboardingPage.test.tsx`

- [ ] **Step 1: Create `OnboardingPage.css.ts`**
```ts
import { style } from '@vanilla-extract/css';
import { surface, buttonPrimary, errorText } from '../styles/common.css';
import { color } from '../styles/theme';

// header: space-y-2 (h1 first, p gets marginTop). header itself is first child of root → no top margin.
// (root space-y-6: section + button get marginTop 1.5rem; header is first child.)

// text-gray-600  (+ space-y-2 marginTop on the lead paragraph)
export const lead = style({ color: color.gray600, marginTop: '0.5rem' });

// text-blue-600 underline (inline link in lead)
export const inlineLink = style({ color: color.blue600, textDecorationLine: 'underline' });

// bg-white shadow rounded p-4 space-y-3  (+ space-y-6 marginTop)
export const section = style([surface, { padding: '1rem', marginTop: '1.5rem' }]);

// text-red-600 text-sm  (+ space-y-3 marginTop, since it follows TagInput in the section)
export const error = style([errorText, { marginTop: '0.75rem' }]);

// bg-blue-600 ... px-4 py-2  (Continue; + space-y-6 marginTop)
export const continueButton = style([buttonPrimary, { marginTop: '1.5rem' }]);
```

- [ ] **Step 2: Swap classNames in `OnboardingPage.tsx`**

Add `import * as s from './OnboardingPage.css';` and `import * as c from '../styles/common.css';`. Replace:
- root div `className="space-y-6"` → remove the `className` (spacing handled by children's marginTop). The `<div>` stays without a className.
- header `className="space-y-2"` → remove the `className` (h1 has no margin; lead `<p>` carries `marginTop`). The `<header>` stays without a className.
- h1 `className="text-2xl font-semibold"` → `className={c.pageTitle}`
- p `className="text-gray-600"` → `className={s.lead}`
- inner Link `className="text-blue-600 underline"` → `className={s.inlineLink}`
- section `className="bg-white shadow rounded p-4 space-y-3"` → `className={s.section}`
- error div `className="text-red-600 text-sm"` → `className={s.error}`
- Continue button `className="bg-blue-600 hover:bg-blue-700 text-white rounded px-4 py-2"` → `className={s.continueButton}`

- [ ] **Step 3: Run tests**

Run: `pnpm test src/pages/OnboardingPage.test.tsx`
Expected: `Tests 2 passed`.

- [ ] **Step 4: Commit**
```bash
git add web/src/pages/OnboardingPage.css.ts web/src/pages/OnboardingPage.tsx
git commit -m "style(web): migrate OnboardingPage to vanilla-extract"
```

---

### Task 14: Convert `CalendarPage.tsx` (incl. range buttons via clsx)

**Files:**
- Create: `web/src/pages/CalendarPage.css.ts`
- Modify: `web/src/pages/CalendarPage.tsx`
- Test: `web/src/pages/CalendarPage.test.tsx`

> `space-y-4` accepted deviation: the content branches (error box, empty state, list) each carry `marginTop: '1rem'`. The transient `<Spinner/>` loading branch is left without the top margin (loading view only).

- [ ] **Step 1: Create `CalendarPage.css.ts`**
```ts
import { style } from '@vanilla-extract/css';
import { surface, pageTitle } from '../styles/common.css';
import { color, radius, shadow, fontSize, fontWeight, transition } from '../styles/theme';

// flex flex-wrap items-baseline justify-between gap-3
export const header = style({
  display: 'flex',
  flexWrap: 'wrap',
  alignItems: 'baseline',
  justifyContent: 'space-between',
  gap: '0.75rem',
});

// flex items-center gap-2
export const controls = style({ display: 'flex', alignItems: 'center', gap: '0.5rem' });

// text-sm text-gray-500
export const controlLabel = style({ ...fontSize.sm, color: color.gray500 });

// inline-flex p-0.5
export const segment = style({ display: 'inline-flex', padding: '0.125rem' });

// rounded-md px-3 py-1 text-sm font-medium transition whitespace-nowrap (range button base)
export const rangeButton = style({
  borderRadius: radius.md,
  paddingInline: '0.75rem',
  paddingBlock: '0.25rem',
  ...fontSize.sm,
  fontWeight: fontWeight.medium,
  ...transition,
  whiteSpace: 'nowrap',
});

// active: bg-blue-600 text-white shadow-sm
export const rangeButtonActive = style({
  backgroundColor: color.blue600,
  color: color.white,
  boxShadow: shadow.sm,
});

// inactive: text-gray-600 hover:text-gray-900
export const rangeButtonInactive = style({
  color: color.gray600,
  ':hover': { color: color.gray900 },
});

// text-red-600  (+ space-y-4 marginTop)
export const errorBox = style({ color: color.red600, marginTop: '1rem' });

// bg-white shadow rounded p-8 text-center text-gray-600  (+ space-y-4 marginTop)
export const emptyState = style([
  surface,
  { padding: '2rem', textAlign: 'center', color: color.gray600, marginTop: '1rem' },
]);

// text-blue-600 underline (inline link in empty state)
export const inlineLink = style({ color: color.blue600, textDecorationLine: 'underline' });

// ul: space-y-3 list  (+ space-y-4 marginTop)
export const list = style({ marginTop: '1rem' });

// li spacing (space-y-3) — applied to every li except the first
export const listItem = style({ marginTop: '0.75rem' });

export { pageTitle };
```

- [ ] **Step 2: Swap classNames in `CalendarPage.tsx`**

Add `import * as s from './CalendarPage.css';`, `import * as c from '../styles/common.css';`, and `import clsx from 'clsx';`. Replace:
- root div `className="space-y-4"` → remove the `className` (children carry their own `marginTop`).
- header `className="flex flex-wrap items-baseline justify-between gap-3"` → `className={s.header}`
- h1 `className="text-2xl font-semibold"` → `className={c.pageTitle}`
- controls div `className="flex items-center gap-2"` → `className={s.controls}`
- label span `className="text-sm text-gray-500"` → `className={s.controlLabel}`
- segmented div `className="inline-flex p-0.5"` → `className={s.segment}`
- range button — replace the string-concat `className={ 'rounded-md px-3 py-1 text-sm font-medium transition whitespace-nowrap ' + (active ? 'bg-blue-600 text-white shadow-sm' : 'text-gray-600 hover:text-gray-900') }` with:
  ```tsx
  className={clsx(s.rangeButton, active ? s.rangeButtonActive : s.rangeButtonInactive)}
  ```
- error div `className="text-red-600"` → `className={s.errorBox}`
- empty-state div `className="bg-white shadow rounded p-8 text-center text-gray-600"` → `className={s.emptyState}`
- inner `<a>` `className="text-blue-600 underline"` → `className={s.inlineLink}`
- `<ul>` `className="space-y-3"` → `className={s.list}`
- `<li>` — add per-item spacing: change `events.map((e) => (` to `events.map((e, i) => (` and set `<li key={e.id} className={i > 0 ? s.listItem : undefined}>`

- [ ] **Step 3: Run tests**

Run: `pnpm test src/pages/CalendarPage.test.tsx`
Expected: `Tests 4 passed`.

- [ ] **Step 4: Commit**
```bash
git add web/src/pages/CalendarPage.css.ts web/src/pages/CalendarPage.tsx
git commit -m "style(web): migrate CalendarPage + range toggle to vanilla-extract"
```

---

### Task 15: Convert `EventDetailPage.tsx`

**Files:**
- Create: `web/src/pages/EventDetailPage.css.ts`
- Modify: `web/src/pages/EventDetailPage.tsx`
- Test: `web/src/pages/EventDetailPage.test.tsx`

> `space-y-4` on the `<article>`: the back link is first (no top margin); every following child carries `marginTop: '1rem'`. `space-y-1` inside `<header>`: h1 first (no margin), date + venue carry `marginTop: '0.25rem'`.

- [ ] **Step 1: Create `EventDetailPage.css.ts`**
```ts
import { style } from '@vanilla-extract/css';
import { buttonPrimary } from '../styles/common.css';
import { color, radius, fontSize, fontWeight } from '../styles/theme';

// block text-sm text-blue-600 hover:underline font-bold mb-2.5
export const backLink = style({
  display: 'block',
  ...fontSize.sm,
  color: color.blue600,
  fontWeight: fontWeight.bold,
  marginBottom: '0.625rem',
  ':hover': { textDecorationLine: 'underline' },
});

// w-full max-h-96 object-cover rounded  (+ space-y-4 marginTop)
export const cover = style({
  width: '100%',
  maxHeight: '24rem',
  objectFit: 'cover',
  borderRadius: radius.sm,
  marginTop: '1rem',
});

// header: space-y-1  (+ space-y-4 marginTop on the header itself)
export const header = style({ marginTop: '1rem' });

// text-3xl font-semibold
export const title = style({ ...fontSize['3xl'], fontWeight: fontWeight.semibold });

// text-gray-700  (+ space-y-1 marginTop)
export const date = style({ color: color.gray700, marginTop: '0.25rem' });

// text-gray-600  (+ space-y-1 marginTop)
export const venue = style({ color: color.gray600, marginTop: '0.25rem' });

// text-sm text-gray-500  (+ space-y-4 marginTop)
export const score = style({ ...fontSize.sm, color: color.gray500, marginTop: '1rem' });

// bg-blue-50 text-blue-900 rounded p-3 text-sm  (+ space-y-4 marginTop)
export const matched = style({
  backgroundColor: color.blue50,
  color: color.blue900,
  borderRadius: radius.sm,
  padding: '0.75rem',
  ...fontSize.sm,
  marginTop: '1rem',
});

// text-gray-800 whitespace-pre-wrap  (+ space-y-4 marginTop)
export const description = style({
  color: color.gray800,
  whiteSpace: 'pre-wrap',
  marginTop: '1rem',
});

// font-bold inline-block bg-blue-600 ... px-4 py-2  (View event; + space-y-4 marginTop)
export const viewEvent = style([
  buttonPrimary,
  { fontWeight: fontWeight.bold, display: 'inline-block', marginTop: '1rem' },
]);

// text-gray-700 (isError "Event not found")
export const notFound = style({ color: color.gray700 });
```

- [ ] **Step 2: Swap classNames in `EventDetailPage.tsx`**

Add `import * as s from './EventDetailPage.css';`. Replace:
- isError div `className="text-gray-700"` → `className={s.notFound}`
- article `className="space-y-4"` → remove the `className` (children carry `marginTop`).
- back Link `className="block text-sm text-blue-600 hover:underline font-bold mb-2.5"` → `className={s.backLink}`
- cover img `className="w-full max-h-96 object-cover rounded"` → `className={s.cover}`
- header `className="space-y-1"` → `className={s.header}`
- h1 `className="text-3xl font-semibold"` → `className={s.title}`
- date div `className="text-gray-700"` → `className={s.date}`
- venue div `className="text-gray-600"` → `className={s.venue}`
- score div `className="text-sm text-gray-500"` → `className={s.score}`
- matched div `className="bg-blue-50 text-blue-900 rounded p-3 text-sm"` → `className={s.matched}`
- description p `className="text-gray-800 whitespace-pre-wrap"` → `className={s.description}`
- View-event a `className="font-bold inline-block bg-blue-600 hover:bg-blue-700 text-white rounded px-4 py-2"` → `className={s.viewEvent}`

- [ ] **Step 3: Run tests**

Run: `pnpm test src/pages/EventDetailPage.test.tsx`
Expected: `Tests 2 passed`.

- [ ] **Step 4: Commit**
```bash
git add web/src/pages/EventDetailPage.css.ts web/src/pages/EventDetailPage.tsx
git commit -m "style(web): migrate EventDetailPage to vanilla-extract"
```

---

### Task 16: Convert `SpotifyCallbackPage.tsx`

**Files:**
- Create: `web/src/pages/SpotifyCallbackPage.css.ts`
- Modify: `web/src/pages/SpotifyCallbackPage.tsx`
- Test: `web/src/pages/SpotifyCallbackPage.test.tsx`

- [ ] **Step 1: Create `SpotifyCallbackPage.css.ts`**
```ts
import { style } from '@vanilla-extract/css';
import { buttonSecondary } from '../styles/common.css';
import { color, fontSize, fontWeight } from '../styles/theme';

// text-center py-12
export const container = style({ textAlign: 'center', paddingBlock: '3rem' });

// text-xl font-semibold
export const title = style({ ...fontSize.xl, fontWeight: fontWeight.semibold });

// text-gray-600 mt-2
export const message = style({ color: color.gray600, marginTop: '0.5rem' });

// mt-4 border rounded px-4 py-2 hover:bg-gray-50  (back to settings)
export const backButton = style([buttonSecondary, { marginTop: '1rem' }]);
```

- [ ] **Step 2: Swap classNames in `SpotifyCallbackPage.tsx`** (three return branches)

Add `import * as s from './SpotifyCallbackPage.css';`. In all three branches replace:
- container div `className="text-center py-12"` → `className={s.container}`
- h1 `className="text-xl font-semibold"` → `className={s.title}`
- p `className="text-gray-600 mt-2"` → `className={s.message}`
- (error branch) back button `className="mt-4 border rounded px-4 py-2 hover:bg-gray-50"` → `className={s.backButton}`

- [ ] **Step 3: Run tests**

Run: `pnpm test src/pages/SpotifyCallbackPage.test.tsx`
Expected: `Tests 3 passed`.

- [ ] **Step 4: Commit**
```bash
git add web/src/pages/SpotifyCallbackPage.css.ts web/src/pages/SpotifyCallbackPage.tsx
git commit -m "style(web): migrate SpotifyCallbackPage to vanilla-extract"
```

---

### Task 17: Convert `SettingsPage.tsx`

**Files:**
- Create: `web/src/pages/SettingsPage.css.ts`
- Modify: `web/src/pages/SettingsPage.tsx`
- Test: `web/src/pages/SettingsPage.test.tsx`

> `space-y-8` on root: h1 first (no margin); every `<section>` carries `marginTop: '2rem'` (baked into `section`). `space-y-3` inside each section: the `<h2>` is first (uses `c.sectionTitle`, no margin); each subsequent direct child carries `marginTop: '0.75rem'`. Buttons nested inside a row `<div>` do NOT get the section margin (the row carries it); buttons that are direct section children do.

- [ ] **Step 1: Create `SettingsPage.css.ts`**
```ts
import { style } from '@vanilla-extract/css';
import { surface, buttonPrimary, buttonSecondary, errorText } from '../styles/common.css';
import { color, radius, fontSize } from '../styles/theme';

// bg-white shadow rounded p-4 space-y-3  (+ space-y-8 marginTop)
export const section = style([surface, { padding: '1rem', marginTop: '2rem' }]);

// text-gray-700 text-sm  (+ space-y-3 marginTop) — the descriptive paragraphs
export const desc = style({ ...fontSize.sm, color: color.gray700, marginTop: '0.75rem' });

// wrapper to give the TagInput (a child component) its space-y-3 top margin
export const item = style({ marginTop: '0.75rem' });

// --- Match sensitivity section ---
// flex items-center gap-3  (+ space-y-3 marginTop)
export const sliderRow = style({
  display: 'flex',
  alignItems: 'center',
  gap: '0.75rem',
  marginTop: '0.75rem',
});
// flex-1 (range input)
export const slider = style({ flex: '1 1 0%' });
// w-12 text-right text-sm tabular-nums
export const percent = style({
  width: '3rem',
  textAlign: 'right',
  ...fontSize.sm,
  fontVariantNumeric: 'tabular-nums',
});
// Save button: buttonPrimary + space-y-3 marginTop (direct section child)
export const saveButton = style([buttonPrimary, { marginTop: '0.75rem' }]);
// saveError: errorText + space-y-3 marginTop
export const error = style([errorText, { marginTop: '0.75rem' }]);

// --- Spotify section ---
// flex gap-2 items-center  (+ space-y-3 marginTop)
export const row = style({
  display: 'flex',
  gap: '0.5rem',
  alignItems: 'center',
  marginTop: '0.75rem',
});
// text-sm text-gray-700 ("Connected.")
export const connectedText = style({ ...fontSize.sm, color: color.gray700 });
// bg-green-600 hover:bg-green-700 disabled:opacity-60 text-white rounded px-4 py-2
export const connectButton = style({
  backgroundColor: color.green600,
  color: color.white,
  borderRadius: radius.sm,
  paddingInline: '1rem',
  paddingBlock: '0.5rem',
  selectors: {
    '&:hover': { backgroundColor: color.green700 },
    '&:disabled': { opacity: 0.6 },
  },
});

// --- iCal section ---
// flex flex-wrap gap-2 items-center  (+ space-y-3 marginTop)
export const buttonRow = style({
  display: 'flex',
  flexWrap: 'wrap',
  gap: '0.5rem',
  alignItems: 'center',
  marginTop: '0.75rem',
});
// block bg-gray-100 rounded p-3 text-sm break-all  (+ space-y-3 marginTop)
export const codeBlock = style({
  display: 'block',
  backgroundColor: color.gray100,
  borderRadius: radius.sm,
  padding: '0.75rem',
  ...fontSize.sm,
  wordBreak: 'break-all',
  marginTop: '0.75rem',
});

// --- Hidden events section ---
// Reset button: buttonSecondary + space-y-3 marginTop (direct section child)
export const resetButton = style([buttonSecondary, { marginTop: '0.75rem' }]);
```

- [ ] **Step 2: Swap classNames in `SettingsPage.tsx`**

Add `import * as s from './SettingsPage.css';` and `import * as c from '../styles/common.css';`. Replace:
- root div `className="space-y-8"` → remove the `className` (sections carry `marginTop`).
- h1 `className="text-2xl font-semibold"` → `className={c.pageTitle}`
- **every** `<section className="bg-white shadow rounded p-4 space-y-3">` (5 of them) → `className={s.section}`
- **every** `<h2 className="text-lg font-medium">` (5 of them) → `className={c.sectionTitle}`
- Interests section: wrap the `<TagInput …/>` in `<div className={s.item}> … </div>`.
- Match sensitivity section:
  - p `className="text-gray-700 text-sm"` → `className={s.desc}`
  - div `className="flex items-center gap-3"` → `className={s.sliderRow}`
  - range input `className="flex-1"` → `className={s.slider}`
  - span `className="w-12 text-right text-sm tabular-nums"` → `className={s.percent}`
  - Save button `className="bg-blue-600 hover:bg-blue-700 disabled:opacity-60 text-white rounded px-4 py-2"` → `className={s.saveButton}`
  - saveError p `className="text-sm text-red-600"` → `className={s.error}`
- Spotify section:
  - p `className="text-gray-700 text-sm"` → `className={s.desc}`
  - div `className="flex gap-2 items-center"` → `className={s.row}`
  - span `className="text-sm text-gray-700"` → `className={s.connectedText}`
  - Disconnect button `className="border rounded px-4 py-2 hover:bg-gray-50 disabled:opacity-60"` → `className={c.buttonSecondary}`
  - Connect button `className="bg-green-600 hover:bg-green-700 disabled:opacity-60 text-white rounded px-4 py-2"` → `className={s.connectButton}`
- iCal section:
  - p `className="text-gray-700 text-sm"` → `className={s.desc}`
  - div `className="flex flex-wrap gap-2 items-center"` → `className={s.buttonRow}`
  - Generate button `className="bg-blue-600 hover:bg-blue-700 text-white rounded px-4 py-2"` → `className={c.buttonPrimary}`
  - Revoke button `className="border rounded px-4 py-2 hover:bg-gray-50"` → `className={c.buttonSecondary}`
  - code `className="block bg-gray-100 rounded p-3 text-sm break-all"` → `className={s.codeBlock}`
- Hidden events section:
  - p `className="text-gray-700 text-sm"` → `className={s.desc}`
  - Reset button `className="border rounded px-4 py-2 hover:bg-gray-50 disabled:opacity-60"` → `className={s.resetButton}`

- [ ] **Step 3: Run tests**

Run: `pnpm test src/pages/SettingsPage.test.tsx`
Expected: `Tests 7 passed`.

- [ ] **Step 4: Commit**
```bash
git add web/src/pages/SettingsPage.css.ts web/src/pages/SettingsPage.tsx
git commit -m "style(web): migrate SettingsPage to vanilla-extract"
```

---

### Task 18: Remove Tailwind entirely + final verification

**Files:**
- Delete: `web/src/styles.css`, `web/postcss.config.js`
- Modify: `web/src/main.tsx`, `web/package.json`

- [ ] **Step 1: Confirm no Tailwind classNames remain**

Run (from `web/`):
```bash
grep -rn "className=" src --include=*.tsx | grep -vE "className=\{" || echo "no string-literal classNames remain"
```
Expected: `no string-literal classNames remain` (every `className` is now `={...}` referencing a style). If any literal remains, convert it before continuing.

- [ ] **Step 2: Drop the Tailwind stylesheet import**

Edit `web/src/main.tsx` — remove the `import './styles.css';` line, keeping `import './styles/global.css.ts';`.

- [ ] **Step 3: Delete Tailwind files**
```bash
git rm web/src/styles.css web/postcss.config.js
```

- [ ] **Step 4: Remove Tailwind deps**
```bash
cd web && pnpm remove tailwindcss @tailwindcss/postcss
```
Expected: `package.json` no longer lists `tailwindcss` or `@tailwindcss/postcss`; lockfile updates.

- [ ] **Step 5: Full build**

Run: `pnpm build`
Expected: `tsc -b` passes and `vite build` completes; `dist/` contains a hashed `.css` asset (the extracted vanilla-extract output). No errors mentioning `tailwind` or `postcss`.

- [ ] **Step 6: Full test suite + lint**

Run: `pnpm test && pnpm lint`
Expected: `Tests 42 passed`; lint clean.

- [ ] **Step 7: Final grep for residual Tailwind**
```bash
grep -rn "tailwind" src vite.config.ts package.json 2>/dev/null || echo "no tailwind references"
ls postcss.config.js 2>/dev/null || echo "postcss.config.js removed"
```
Expected: `no tailwind references` and `postcss.config.js removed`.

- [ ] **Step 8: Visual verification**

Run: `pnpm dev`, open `http://127.0.0.1:5173`, and walk: Login → Signup → Onboarding → Calendar (toggle 1/3/6-month range) → an Event detail → Settings (each section, slider, Spotify/iCal/reset buttons) → UserMenu dropdown. Compare against the pre-refactor appearance, paying attention to the spec's fuzzy spots: card/dialog/dropdown **shadows**, secondary-button & input **borders** (currentColor), and **hover transitions**. Adjust token values in `styles/theme.ts` if anything reads off, then re-run `pnpm build`.

- [ ] **Step 9: Commit**
```bash
git add -A
git commit -m "build(web): remove Tailwind; styles fully migrated to vanilla-extract"
```

---

## Self-Review

**Spec coverage:**
- Build/deps + vite plugin → Task 1 ✓; remove Tailwind/postcss → Task 18 ✓
- `theme.ts` tokens, no spacing constant → Task 2 ✓
- Preflight reset + globals (`global.css.ts`), delete `styles.css`, main.tsx swap → Tasks 3 & 18 ✓
- `common.css.ts` shared styles (surface/buttons/input/titles/errorText) → Task 4 ✓
- All 13 className-bearing files co-located conversions → Tasks 5–17 ✓
- `space-y-*` via child `marginTop` (no `& > * + *`) → encoded per file ✓
- Spacing as inline rem → all tasks use literal rem ✓
- `clsx` for conditional/active (Layout nav, Calendar range) → Tasks 10 & 14 ✓
- Inline logo `style` retained (Layout, Login) → Tasks 10 & 11 ✓
- LoginPage bg/card cleanup → Task 11 ✓
- Fuzzy-spot visual verification (shadow scale, currentColor border, transition) → Task 18 Step 8 ✓
- Tests stay green (class-agnostic) → gate on every task ✓

**Placeholder scan:** No TBD/TODO; every `.css.ts` has full code; every JSX edit lists exact old→new classNames.

**Type/name consistency:** Token names (`color.*`, `fontSize.*`, `radius.*`, `shadow.*`, `fontWeight.*`, `transition`) match `theme.ts`. Shared style names (`surface`, `buttonPrimary`, `buttonSecondary`, `buttonSubmit`, `textInput`, `pageTitle`, `sectionTitle`, `errorText`) match `common.css.ts` and their consumers. Per-file `s.*` references match each file's exports. Import alias `c` = common, `s` = co-located, used consistently.
