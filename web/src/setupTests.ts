import '@testing-library/jest-dom/vitest';
import { afterEach } from 'vitest';
import { cleanup } from '@testing-library/react';
import { MotionGlobalConfig } from 'motion/react';

// happy-dom has no real animation frames, so motion's enter/exit animations
// never settle and AnimatePresence children are never removed. Jump straight to
// the target values instead.
MotionGlobalConfig.skipAnimations = true;

afterEach(() => {
  cleanup();
});
