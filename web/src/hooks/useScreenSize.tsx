import { useEffect, useState } from 'react';

export const SCREEN_SIZES = {
  Desktop: 'desktop',
  Phone: 'phone',
} as const;

export type ScreenSizes = (typeof SCREEN_SIZES)[keyof typeof SCREEN_SIZES];

const MEDIA_QUERIES: [ScreenSizes, string][] = [
  [SCREEN_SIZES.Desktop, 'width >= 768px'],
  [SCREEN_SIZES.Phone, 'width < 768px'],
];

const getScreenSize = (): ScreenSizes => {
  for (const [size, query] of MEDIA_QUERIES) {
    const mql = window.matchMedia(`(${query})`);
    if (mql.matches) {
      return size;
    }
  }
  // shouldn't happen
  return SCREEN_SIZES.Desktop;
};

// Returns the current screen size of the browser as defined by MEDIA_QUERIES
// This is useful for rendering decisions based on the screen size BUT we should
// always prefer to use CSS media queries when possible
export const useScreenSize = () => {
  const [screenSize, setScreenSize] = useState<ScreenSizes>(getScreenSize());

  useEffect(() => {
    const handlers: Map<MediaQueryList, (mql: MediaQueryListEvent) => void> = new Map();
    for (const [size, query] of MEDIA_QUERIES) {
      const mql = window.matchMedia(`(${query})`);
      const handler = (mql: MediaQueryList | MediaQueryListEvent) => {
        if (mql.matches) {
          setScreenSize(size);
        }
      };
      mql.addEventListener('change', handler);
      handler(mql);
      handlers.set(mql, handler);
    }

    return () => {
      handlers.forEach((handler, mql) => {
        mql.removeEventListener('change', handler);
      });
    };
  }, []);

  return {
    screenSize,
    isPhoneWidth: screenSize === SCREEN_SIZES.Phone,
  };
};
