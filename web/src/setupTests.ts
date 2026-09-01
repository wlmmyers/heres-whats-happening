import '@testing-library/jest-dom/vitest';
import { afterEach, beforeEach, vi } from 'vitest';
import { cleanup } from '@testing-library/react';
import { MotionGlobalConfig } from 'motion/react';

// happy-dom has no real animation frames, so motion's enter/exit animations
// never settle and AnimatePresence children are never removed. Jump straight to
// the target values instead.
MotionGlobalConfig.skipAnimations = true;

// Nothing in the suite should touch the network -- every api/ module is mocked
// at its own boundary. A call that slips through used to dial the dev server on
// :3000 and spray ECONNREFUSED across the run, from whichever test happened to
// be in flight. Reject it here instead, naming the URL, so the missing mock is
// obvious. Tests that exercise apiFetch itself (api/client.test.ts) install
// their own fetch inside the test body and still work.
beforeEach(() => {
  vi.stubGlobal(
    'fetch',
    vi.fn((input: RequestInfo | URL) => {
      const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url;
      return Promise.reject(
        new Error(`Unmocked fetch to ${url} -- mock the api/ module that makes this call`),
      );
    }),
  );
});

afterEach(() => {
  cleanup();
});
