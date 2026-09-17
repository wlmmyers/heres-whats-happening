import { useId, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { AnimatePresence, motion } from 'motion/react';
import type { CalendarEvent } from '../api/calendar';
import type { ManualGoingEvent } from '../api/manualGoingEvents';
import { useListGoingEvents } from '../hooks/useListGoingEvents';
import { useMarkNotGoing } from '../hooks/useMarkNotGoing';
import { useManualGoingEvents } from '../hooks/useManualGoingEvents';
import { useDeleteManualGoingEvent } from '../hooks/useDeleteManualGoingEvent';
import {
  endOfLocalDay,
  formatEventDate,
  formatManualEventDate,
  parseLocalDate,
} from '../utils/eventDate';
import * as c from '../styles/common.css';
import * as s from './GoingWentList.css';
import { Skeleton } from './Skeleton';
import clsx from 'clsx';
import { useLocalStorageState } from '../hooks/useLocalStorageState';
import RotatingCaret from './RotatingCaret';
import AddManualEventDialog from './AddManualEventDialog';
import ConfirmDialog from './ConfirmDialog';

// An event with an end time is over when that end passes; one without, when its
// start does.
function endOf(event: CalendarEvent): Date {
  const end = event.ends_at ? new Date(event.ends_at) : null;
  return end && !Number.isNaN(end.getTime()) ? end : new Date(event.starts_at);
}

/**
 * One line in the sidebar, from either of the two sources behind it.
 *
 * A scraped going event and a hand-typed one share nothing but a day, a title
 * and a venue, so they are flattened to that here and merged. The `kind` is
 * what the row still needs them apart for: only a real event has a detail page
 * to open, and the two are removed through different endpoints.
 *
 * `sortAt` and `endAt` are milliseconds. They are separate because a show that
 * has started is not yet a show you went to — for a scraped event that gap is
 * its running time, and for a hand-added one it is the rest of the day.
 */
type ListRow = {
  id: string;
  title: string;
  venue: string;
  dateLabel: string;
  sortAt: number;
  endAt: number;
  isUpcoming: boolean;
} & ({ kind: 'event'; event: CalendarEvent } | { kind: 'manual' });

function eventRow(event: CalendarEvent): ListRow {
  return {
    kind: 'event',
    event,
    id: event.id,
    title: event.title,
    venue: event.venue.name,
    dateLabel: formatEventDate(event, 'short'),
    sortAt: new Date(event.starts_at).getTime(),
    endAt: endOf(event).getTime(),
    isUpcoming: endOf(event).getTime() > Date.now(),
  };
}

function manualRow(manual: ManualGoingEvent): ListRow {
  const day = parseLocalDate(manual.date);
  // The server validates the day, so an unreadable one means a row written
  // before that validation or by hand. Sink it to the far end of history
  // rather than letting NaN loose in the comparisons below, where it would
  // make the sort order depend on the input order.
  const valid = !Number.isNaN(day.getTime());
  return {
    kind: 'manual',
    id: manual.id,
    title: manual.event_name,
    venue: manual.venue_name,
    dateLabel: formatManualEventDate(manual.date),
    sortAt: valid ? day.getTime() : 0,
    endAt: valid ? endOfLocalDay(day).getTime() : 0,
    isUpcoming: valid && endOfLocalDay(day).getTime() > Date.now(),
  };
}

// The going endpoint returns the whole list, past shows included, and the
// hand-added ones arrive unsplit too, so the two halves are picked apart here.
// Upcoming runs forwards; history reads backwards, the show you just got home
// from first.
function splitByTime(rows: ListRow[]): { upcoming: ListRow[]; past: ListRow[] } {
  const upcoming: ListRow[] = [];
  const past: ListRow[] = [];
  for (const row of rows) {
    (row.isUpcoming ? upcoming : past).push(row);
  }
  upcoming.sort((a, b) => a.sortAt - b.sortAt);
  past.sort((a, b) => b.sortAt - a.sortAt);
  return { upcoming, past };
}

function EventRow({
  row,
  onOpen,
  onRemove,
}: {
  row: ListRow;
  onOpen?: () => void;
  onRemove: () => void;
}) {
  return (
    <li className={clsx(s.item, { [s.itemStatic]: !onOpen })} onClick={onOpen}>
      <div className={s.itemMain}>
        <div className={s.itemDate}>
          {row.dateLabel}
          {row.kind === 'manual' && <span className={s.manualLabel}>Entered by hand</span>}
        </div>
        <div className={s.itemTitle}>{row.title}</div>
        <div className={s.itemVenue}>{row.venue}</div>
      </div>
      <button
        type="button"
        aria-label={`Remove ${row.title} from your going list`}
        className={s.removeButton}
        onClick={(e) => {
          e.stopPropagation();
          onRemove();
        }}
      >
        X
      </button>
    </li>
  );
}

export default function GoingList() {
  const navigate = useNavigate();
  const goingEventsQ = useListGoingEvents();
  const manualEventsQ = useManualGoingEvents();
  const { mutate: markNotGoing } = useMarkNotGoing();
  const { mutate: deleteManualEvent } = useDeleteManualGoingEvent();
  const bodyId = useId();
  const [addOpen, setAddOpen] = useState(false);
  // The row awaiting confirmation before it is removed. Holding the row itself
  // rather than a flag is what lets the prompt name the show and word itself
  // for the right kind — and what keeps a second removal from inheriting the
  // subject of a cancelled one.
  const [pendingRemoval, setPendingRemoval] = useState<ListRow | null>(null);
  const { state: expandedWentList, actions: expandedWentListActions } = useLocalStorageState<
    'true' | 'false'
  >('calendar.expandedWentList');
  const isWentExpanded = expandedWentList === 'true';

  const rows = [
    ...(goingEventsQ.data ?? []).map(eventRow),
    ...(manualEventsQ.data ?? []).map(manualRow),
  ];
  const { upcoming: going, past } = splitByTime(rows);

  // Both halves of the list load together, so the skeleton stands until both
  // have answered — otherwise the merged order visibly reshuffles as the
  // second query lands.
  const isLoading = !goingEventsQ.isFetched || !manualEventsQ.isFetched;
  const isError = goingEventsQ.isError || manualEventsQ.isError;

  // A hand-added row has no event page behind it, so it gets no open handler
  // and, through that, no pointer cursor. Removing either kind is destructive,
  // so the "x" only nominates the row — the mutation waits on the prompt below.
  const renderRow = (row: ListRow) => (
    <EventRow
      key={`${row.kind}:${row.id}`}
      row={row}
      onOpen={row.kind === 'event' ? () => navigate(`/events/${row.id}`) : undefined}
      onRemove={() => setPendingRemoval(row)}
    />
  );

  // The two kinds leave through different endpoints, and cost the user
  // different amounts: un-marking a going event is reversible from the
  // calendar, while a hand-added row is the only copy of itself.
  const confirmRemoval = () => {
    if (!pendingRemoval) return;
    if (pendingRemoval.kind === 'event') markNotGoing(pendingRemoval.id);
    else deleteManualEvent(pendingRemoval.id);
    setPendingRemoval(null);
  };

  return (
    <div className={s.goingWentList}>
      <div className={s.heading}>
        <h2 className={c.sectionTitle}>Upcoming Shows</h2>
        {/* A sibling of the toggle, not a child: a button cannot nest inside a
            button, and the heading is a button end to end. */}
        <button
          type="button"
          aria-label="Add a show by hand"
          className={clsx(c.stripButtonStyles, s.addButton)}
          onClick={() => setAddOpen(true)}
        >
          Add
        </button>
      </div>
      <div className={s.innerContainer}>
        {isLoading ? (
          <>
            <Skeleton className={s.skeletonRow} />
            <Skeleton className={s.skeletonRow} />
            <Skeleton className={s.skeletonRow} />
          </>
        ) : isError ? (
          <div className={s.errorText}>Couldn't load your going list.</div>
        ) : going.length === 0 ? (
          <div className={s.emptyText}>
            No upcoming shows yet. <br />
            <aside className={s.helperText}>
              Click <i style={{ fontWeight: 'bold' }}>I'm going</i> on shows to see them here.
            </aside>
          </div>
        ) : (
          <ul>{going.map(renderRow)}</ul>
        )}
      </div>
      <div className={clsx(s.heading, s.wentHeading)}>
        <button
          type="button"
          className={clsx(c.stripButtonStyles, s.wentToggle)}
          aria-expanded={isWentExpanded}
          aria-controls={bodyId}
          onClick={() => expandedWentListActions.setValue(isWentExpanded ? 'false' : 'true')}
        >
          <h2 className={clsx(c.sectionTitle, s.wentSectionTitle)}>Past Shows</h2>
          <RotatingCaret open={isWentExpanded} className={s.wentCaret} />
        </button>
      </div>
      <div className={clsx(s.innerContainer, { [s.noBorder]: !isWentExpanded })}>
        {/* The id stays mounted so `aria-controls` always resolves, collapsed or not. */}
        <div id={bodyId} className={s.wentBody}>
          <AnimatePresence initial={false}>
            {isWentExpanded && (
              <motion.div
                key="body"
                initial={{ height: 0, opacity: 0 }}
                animate={{ height: 'auto', opacity: 1 }}
                exit={{ height: 0, opacity: 0 }}
                transition={{ type: 'spring', stiffness: 500, damping: 40 }}
              >
                <ul>{past.map(renderRow)}</ul>
              </motion.div>
            )}
          </AnimatePresence>
        </div>
      </div>
      <AddManualEventDialog
        open={addOpen}
        onClose={() => setAddOpen(false)}
        // A show that has already happened lands in the collapsed half, where
        // the user would otherwise see nothing happen at all. An upcoming one
        // is already on screen, so it is left alone.
        onCreated={(created) => {
          const end = endOfLocalDay(parseLocalDate(created.date)).getTime();
          if (end <= Date.now()) expandedWentListActions.setValue('true');
        }}
      />
      {/* Mounted even with nothing pending, so it can animate out once the
          removal is confirmed or cancelled. The copy for a null row is never
          shown: while closing, the dialog keeps the copy it last opened with. */}
      <ConfirmDialog
        open={pendingRemoval !== null}
        title={pendingRemoval?.kind === 'event' ? 'Remove event?' : 'Delete event?'}
        message={
          !pendingRemoval
            ? ''
            : pendingRemoval.kind === 'event'
              ? pendingRemoval.isUpcoming
                ? `"${pendingRemoval.title}" will be removed from this list. You can mark it again from the calendar.`
                : `"${pendingRemoval.title}" will be removed from this list. It happened in the past, so you won't be able to add it back.`
              : `"${pendingRemoval.title}" was added by hand, so this will delete it permanently.`
        }
        confirmLabel="Remove"
        onConfirm={confirmRemoval}
        onCancel={() => setPendingRemoval(null)}
      />
    </div>
  );
}
