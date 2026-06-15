# Refactor `web/` styling: Tailwind CSS → vanilla-extract

**Date:** 2026-06-11
**Status:** Design — awaiting review

## Goal

Remove the dependency on Tailwind CSS in the `web/` React app and replace every
Tailwind utility class with explicit styles authored using
[vanilla-extract](https://vanilla-extract.style/). Styles become hand-editable
plain CSS-in-TS, with no Tailwind knowledge required to read or change them.

CSS is extracted at build time via `@vanilla-extract/vite-plugin`.

## Decisions (confirmed with user)

1. **File layout:** co-located — each component `Foo.tsx` gets a sibling
   `Foo.css.ts`. Shared cross-component styles live under `web/src/styles/`.
2. **Design values:** a shared tokens module for `color`, `fontSize`,
   `fontWeight`, `radius`, `shadow`, `transition`. **Spacing is NOT tokenized** —
   padding/margin/gap are written as literal `rem` values inline in each style.
3. **Tailwind removal:** complete — drop `tailwindcss` + `@tailwindcss/postcss`
   deps, delete `postcss.config.js`, remove `@import 'tailwindcss'`.
4. **Fidelity:** faithful 1:1 translation of rendered appearance, with minor
   cleanup of obvious duplication and clearly-unintended inconsistencies.
5. **`space-y-*`:** do NOT use the `& > * + *` child-combinator trick. Apply an
   explicit `marginTop` to each non-first child element's own style.
6. **Conditional classes:** use `clsx` to compose active/state classNames.

## Scope

13 `.tsx` files use `className` (133 occurrences). All are standard Tailwind v4
utilities plus a few arbitrary values (`w-[400px]`, `max-w-[90%]`,
`min-w-[120px]`, `bg-black/40`). Two inline `style={{ backgroundImage }}` logo
usages stay inline (dynamic asset URLs). Tests are role/text-based and reference
no classNames, so they should remain green.

Files with styles to convert:
`App.tsx` (none), `main.tsx` (import swap), `components/{Layout, UserMenu,
EventCard, ConfirmDialog, TagInput, Spinner}.tsx`, `pages/{Login, Signup,
Onboarding, Calendar, EventDetail, SpotifyCallback, Settings}Page.tsx`.

## Architecture

### Build & dependencies

- **Add:** `@vanilla-extract/css`, `@vanilla-extract/vite-plugin`, `clsx`.
- **`web/vite.config.ts`:** add `vanillaExtractPlugin()` to `plugins`
  (with `react()`). Vitest reads this config, so `.css.ts` transforms apply in
  the test run too.
- **Remove:** `tailwindcss`, `@tailwindcss/postcss` from `package.json`; delete
  `web/postcss.config.js`.
- **`web/src/styles.css`:** deleted. Its global rules move into
  `styles/global.css.ts`. `main.tsx` imports `./styles/global.css.ts`.

### `web/src/styles/theme.ts` (plain TS constants)

Named values extracted from Tailwind defaults. **No `space`/spacing object.**

```
color:      white, black, blackA40 (rgb(0 0 0 / 0.4)),
            gray50/100/200/500/600/700/800/900,
            blue50/100/500/600/700/800/900, green600/700, red600
fontSize:   xs/sm/base/lg/xl/2xl/3xl  → [fontSize, lineHeight] tuples
fontWeight: medium(500), semibold(600), bold(700)
radius:     sm(0.25rem), md(0.375rem), full(9999px)
shadow:     sm, base, md, lg  (standard Tailwind box-shadow strings)
transition: the Tailwind `transition` shorthand value
```

Color hex values are the standard Tailwind palette (e.g. `blue600 #2563eb`,
`gray700 #374151`, `red600 #dc2626`). Spacing values are written inline at each
use site using the Tailwind scale (`1`=`0.25rem`, `2`=`0.5rem`, `3`=`0.75rem`,
`4`=`1rem`, `6`=`1.5rem`, `8`=`2rem`, `12`=`3rem`, fractional `0.5`=`0.125rem`,
`1.5`=`0.375rem`, `2.5`=`0.625rem`).

### `web/src/styles/global.css.ts` (`globalStyle`)

Two parts:

1. **Preflight-equivalent reset.** Removing Tailwind removes its Preflight base
   layer, which the app's appearance depends on (zeroed heading sizes/margins,
   `box-sizing`, button/input font inheritance, block images). We reproduce the
   essential subset:
   - `*, ::before, ::after { box-sizing: border-box; border-width: 0;
     border-style: solid; }`
   - `body { margin: 0; }`
   - `h1..h6 { font-size: inherit; font-weight: inherit; margin: 0; }`
     (the app sets explicit sizes per-heading)
   - `p, figure, blockquote { margin: 0; }`
   - `a { color: inherit; text-decoration: inherit; }`
   - `button, input, optgroup, select, textarea { font: inherit; color:
     inherit; margin: 0; }`
   - `button { background-color: transparent; background-image: none; }`
   - `img, svg, video, canvas { display: block; max-width: 100%; height: auto; }`
   - `ol, ul { list-style: none; margin: 0; padding: 0; }`
   - `hr { border-top-width: 1px; }`
   - `code, pre { font-family: ui-monospace, monospace; }`
2. **App globals** (carried over from `styles.css`):
   - `body { font-family: 'Raleway', sans-serif; font-optical-sizing: auto; }`
   - `button { cursor: pointer; font-family: inherit; font-weight: 600; }`

### `web/src/styles/common.css.ts` (shared, multi-use styles)

Consolidates the obvious duplication ("minor cleanup"). Each consumer adds
local overrides where it genuinely differs (composed via `clsx` with a
co-located style, or via vanilla-extract `style([common.x, {...}])`).

- `surface` — `bg-white shadow rounded`: white bg, base shadow, `radius.sm`.
  (≈8 uses; padding/spacing stay at each call site since they vary: p-4/p-6/p-8.)
- `buttonPrimary` — `bg-blue-600 hover:bg-blue-700 text-white rounded px-4 py-2`.
- `buttonSecondary` — `border rounded px-4 py-2 hover:bg-gray-50` (1px solid
  border; faithful to current rendered border color).
- `textInput` — `mt-1 w-full border rounded px-2 py-1.5` (Login/Signup fields).
- `pageTitle` — `text-2xl font-semibold`; `sectionTitle` — `text-lg font-medium`.

Disabled state: `:disabled { opacity }` — note the existing code uses
`disabled:opacity-60` on Settings buttons and `disabled:opacity-50` on the
auth submit buttons; these stay distinct (submit buttons get a local style).

### Co-located component styles (`Foo.css.ts`)

Each file's remaining one-off styles are named semantically (`card`,
`matchBadge`, `navLink`, `rangeButton`, …) and exported from its sibling
`.css.ts`. Markup swaps `className="..."` → `className={s.name}`.

Conventions:
- **Hover/disabled/focus** → vanilla-extract `:hover` / `:disabled` selectors.
- **Spacing** → inline `rem` values per decision 2.
- **`space-y-*`** → explicit `marginTop` on each non-first child's style
  (decision 5).
- **Responsive** (`md:flex-row`, `md:gap-8`) →
  `'@media': { 'screen and (min-width: 768px)': { ... } }`.
- **Conditional/active** → base style + composed variant styles, selected with
  `clsx(...)` (decision 6). Two spots:
  - `Layout.tsx` `NavLink` render prop: `navLink` + `navLinkActive`
    (`bg-blue-100 text-blue-800`) / `navLinkInactive`
    (`text-gray-700 hover:bg-gray-100`).
  - `CalendarPage.tsx` range buttons: `rangeButton` + `rangeButtonActive`
    (`bg-blue-600 text-white shadow-sm`) / `rangeButtonInactive`
    (`text-gray-600 hover:text-gray-900`).

## Per-file translation notes

| File | Notable styles |
|------|----------------|
| `main.tsx` | swap `import './styles.css'` → `import './styles/global.css.ts'` |
| `Spinner.tsx` | flex center, p-8, gray-500 text |
| `ConfirmDialog.tsx` | fixed full-screen `bg-black/40` backdrop (z-50), centered dialog `w-[400px] max-w-[90%]` `surface`+shadow-lg+p-6; uses `buttonPrimary`/`buttonSecondary` |
| `TagInput.tsx` | bordered wrapper (flex-wrap), pill tags (`bg-blue-100 text-blue-800 rounded-full`), borderless transparent `<input>` (`flex-1 min-w-[120px] outline-none`) |
| `UserMenu.tsx` | avatar button (`w-8 h-8 rounded-full bg-blue-500`), absolute dropdown (`top-full right-0 z-20 min-w-max` bordered surface + shadow-md), full-screen z-10 click-catcher, `<hr>`, plain text button |
| `Layout.tsx` | `min-h-screen bg-gray-50`; header bar (`bg-white border-b`); logo div keeps inline `backgroundImage`, gets `bg-cover bg-center bg-no-repeat`; `navLink` variants; `main` (`max-w-5xl mx-auto`) |
| `EventCard.tsx` | `surface` card + `hover:shadow-md transition` + p-4; title/score/date/match rows; "Not interested" text button (`hover:text-red-600`) |
| `CalendarPage.tsx` | space-y-4 stack; header (flex-wrap, items-baseline); `rangeButton` variants; empty-state card; `<ul>` list with `marginTop` between items |
| `EventDetailPage.tsx` | space-y-4 article; back link (`hover:underline`); cover image (`w-full max-h-96 object-cover`); title `text-3xl`; matched-because callout (`bg-blue-50 text-blue-900`); description `whitespace-pre-wrap`; "View event" primary button (inline-block) |
| `OnboardingPage.tsx` | space-y-6; header space-y-2; `surface` section; `buttonPrimary` Continue |
| `SpotifyCallbackPage.tsx` | centered `py-12` states; `text-xl` headings; `buttonSecondary` back button |
| `LoginPage.tsx` | **cleanup:** outer div currently has conflicting `bg-gray-50`+`bg-white` and `shadow rounded` on the full-screen container. Resolve to match `SignupPage`: outer = `min-h-screen bg-gray-50` centered (`md:flex-row md:gap-8` for logo+form), form = `surface` card. Logo `<img>` keeps inline width. Uses `textInput`; full-width submit (`w-full py-2 disabled:opacity-50`) as a local style. |
| `SignupPage.tsx` | outer `min-h-screen bg-gray-50` centered; form `surface` p-6 space-y-4; `textInput` fields; full-width submit |
| `SettingsPage.tsx` | top `space-y-8` stack; five `surface` sections each `space-y-3`; `pageTitle`/`sectionTitle`; range slider row; `buttonPrimary` (Save, Generate), green Connect-Spotify button (`bg-green-600 hover:bg-green-700`), `buttonSecondary` (Disconnect/Revoke/Reset); `<code>` block (`bg-gray-100 break-all`); error text (`text-red-600`) |

## Fuzzy spots to verify visually

- Tailwind v4 renamed the shadow scale (`shadow-sm`→`shadow-xs`, etc.). We map to
  the standard rendered box-shadow values; confirm cards/dialogs/dropdown look
  right.
- Tailwind v4's default `border` color is `currentColor`. The `border`-only
  buttons/inputs render with a text-colored border today; we reproduce that.
  Confirm secondary buttons and inputs look unchanged.
- `transition` shorthand: confirm hover transitions on cards/range buttons.

## Verification

1. `pnpm --dir web build` (runs `tsc -b && vite build`) — TS + vanilla-extract
   compile clean, no Tailwind references remain.
2. `pnpm --dir web test` — existing vitest suite stays green (class-agnostic).
3. `pnpm --dir web lint`.
4. Visual check: run `pnpm --dir web dev`, walk Login → Signup → Onboarding →
   Calendar (range toggle) → Event detail → Settings → UserMenu dropdown, and
   compare against current Tailwind rendering (esp. the fuzzy spots above).
5. Grep confirms zero residual Tailwind: no `@import 'tailwindcss'`, no
   `tailwind`/`postcss` config, no utility-class strings in `className`.

## Out of scope

- No visual redesign beyond resolving the noted LoginPage conflict and DRYing
  duplicated styles.
- No runtime theming / dark mode (tokens are plain constants, not a
  `createGlobalTheme` CSS-variable contract).
- No changes to non-style logic, API layer, or tests.

## Execution order (for the implementation plan)

1. Deps + `vite.config.ts` plugin + remove Tailwind config.
2. `styles/theme.ts`.
3. `styles/global.css.ts` (reset + app globals); delete `styles.css`; update
   `main.tsx`.
4. `styles/common.css.ts` (shared styles).
5. Per-component conversions (leaf components first: Spinner, TagInput,
   ConfirmDialog, EventCard, UserMenu, Layout; then pages). Largely independent.
6. Remove `tailwindcss`/`@tailwindcss/postcss` from `package.json`; reinstall.
7. Verify (build, test, lint, visual, grep).
