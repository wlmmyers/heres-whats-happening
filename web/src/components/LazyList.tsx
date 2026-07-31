import { useEffect, useRef } from 'react';

type Props = {
  children: React.ReactNode;
  bufferPx?: number;
  fetchNextPage: VoidFunction;
  hasNextPage: boolean;
};

const DEFAULT_BUFFER = 600;
// Component that enables triggering loading another page of records
// when the last record in the current page nears the bottom of the
// window during a scroll event
export const LazyList = ({ children, bufferPx, fetchNextPage, hasNextPage }: Props) => {
  const sentinelRef = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    const el = sentinelRef.current;
    if (!el || !hasNextPage) return;
    const observer = new IntersectionObserver(
      ([entry]) => {
        if (entry.isIntersecting) {
          fetchNextPage();
        }
      },
      { rootMargin: `0px 0px ${bufferPx ?? DEFAULT_BUFFER}px 0px` },
    );
    observer.observe(el);
    return () => observer.disconnect();
  }, [bufferPx, fetchNextPage, hasNextPage]);

  return (
    <>
      {children}
      <div ref={sentinelRef} />
    </>
  );
};
