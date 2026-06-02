# User Menu Popover Design

**Date:** 2026-06-01
**Status:** Approved

## Summary

Replace the inline email + sign-out button in the nav bar with an avatar icon that opens a minimal popover on click.

## Context

`Layout.tsx` currently displays the signed-in user's email as a `<span>` and a bordered "Sign out" button side-by-side in the nav bar. This consolidates both into a compact avatar menu.

## Design

### Avatar

- 32×32 circular button (`rounded-full`)
- Blue background (`bg-blue-500`) with white text
- Displays the first character of `user?.email` uppercased; falls back to `?` if no user
- Positioned at the right end of the nav bar (replaces the existing `ml-auto` block)

### Popover

- Appears directly below the avatar, right-aligned to it (`position: absolute; top: 100%; right: 0`)
- Visible only when the avatar is clicked (toggled)
- Content:
  1. User's email address — small, gray
  2. Thin `<hr>` divider
  3. "Sign out" plain text button — calls `logout()` and closes the popover
- Dismissed by clicking anywhere outside the component

### Dismissal

A `mousedown` listener is attached to `document` via `useEffect`. If the event target falls outside the component's root ref, the popover closes. The listener is removed on cleanup.

## Component Structure

### New: `web/src/components/UserMenu.tsx`

- Calls `useAuth()` internally — no props required
- Owns open/closed state (`useState<boolean>`)
- Root `<div>` has `position: relative` and holds a `useRef<HTMLDivElement>` for outside-click detection
- Renders the avatar `<button>` and conditionally the popover `<div>`

### Modified: `web/src/components/Layout.tsx`

- Remove `useAuth()` call (no longer needed in Layout)
- Replace the `<div className="ml-auto ...">` block (current lines 15–24) with `<UserMenu />`
- Add `UserMenu` import

## Decisions

| Question | Decision | Reason |
|---|---|---|
| Avatar style | Blue initial-letter circle | Colorful, personal; fits the minimal UI |
| Popover layout | Minimal (email + divider + text button) | Unobtrusive; matches existing low-chrome style |
| Implementation approach | Extract to `UserMenu` component | Keeps Layout clean; component is self-contained and testable |
| Popover positioning | CSS absolute, anchored to wrapper | No external library needed; simple and reliable |
| Generic Popover primitive | Deferred | Only one popover use case exists today |
