import { useState } from 'react';
import type { ManualGoingEvent } from '../api/manualGoingEvents';
import Dialog from './Dialog';
import { useCreateManualGoingEvent } from '../hooks/useCreateManualGoingEvent';
import { parseLocalDate } from '../utils/eventDate';
import * as c from '../styles/common.css';
import * as s from './AddManualEventDialog.css';

interface Props {
  open: boolean;
  onClose: () => void;
  // Fired with the saved row before the dialog closes, so the list behind it
  // can react to what was added — revealing the section the new show landed in.
  onCreated?: (event: ManualGoingEvent) => void;
}

interface FieldErrors {
  date?: string;
  eventName?: string;
  venueName?: string;
}

// Validates what the user typed. The date input hands back a YYYY-MM-DD string
// — or an empty one, since a partially typed date is reported as empty rather
// than as a fragment — so an unparseable value here means a day that isn't on
// the calendar (Feb 30th) or a browser that fell back to a plain text box.
function validate(date: string, eventName: string, venueName: string): FieldErrors {
  const errors: FieldErrors = {};
  if (!date.trim()) {
    errors.date = 'Pick a date for the show.';
  } else if (Number.isNaN(parseLocalDate(date).getTime())) {
    errors.date = 'Enter a valid date.';
  }
  if (!eventName.trim()) errors.eventName = 'Enter an event name.';
  if (!venueName.trim()) errors.venueName = 'Enter a venue name.';
  return errors;
}

/**
 * Adds a show the scrapers never saw. Opened from the "+" in the calendar
 * sidebar's Past Shows heading; the row it creates is merged into the same
 * Upcoming/Past lists as the real going events.
 *
 * The form lives in a child that only exists while the dialog is open, so a
 * cancelled entry is discarded by unmounting rather than by clearing each
 * field back to blank on the way in.
 */
export default function AddManualEventDialog({ open, onClose, onCreated }: Props) {
  const [date, setDate] = useState('');
  const [eventName, setEventName] = useState('');
  const [venueName, setVenueName] = useState('');
  const [errors, setErrors] = useState<FieldErrors>({});
  const { mutateAsync: createEvent, isPending, isError } = useCreateManualGoingEvent();

  async function onSubmit(e: React.SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    const found = validate(date, eventName, venueName);
    setErrors(found);
    if (Object.keys(found).length > 0) return;
    try {
      const created = await createEvent({
        date: date.trim(),
        event_name: eventName.trim(),
        venue_name: venueName.trim(),
      });
      onCreated?.(created);
      onClose();
    } catch {
      // Left open on purpose: the entry is still in the fields, so the user can
      // retry without retyping it. isError drives the message below.
    }
  }

  return (
    <Dialog topOnPhone open={open} heading="Add an event by hand" onClose={onClose}>
      <p className={s.description}>
        It will appear in your Upcoming/Past lists, but won't be matched to your interests or appear
        in the calendar.
      </p>
      {/* noValidate so our own messages are what the user sees, rather than
          the browser's bubble stopping submit before validate() runs. */}
      <form onSubmit={onSubmit} className={s.form} noValidate>
        <label className={c.field}>
          <span className={c.fieldLabel}>Date</span>
          <input
            type="date"
            value={date}
            onChange={(e) => setDate(e.target.value)}
            className={c.textInput}
          />
          {errors.date && <span className={c.fieldError}>{errors.date}</span>}
        </label>

        <label className={c.field}>
          <span className={c.fieldLabel}>Event name</span>
          <input
            type="text"
            value={eventName}
            onChange={(e) => setEventName(e.target.value)}
            maxLength={200}
            className={c.textInput}
          />
          {errors.eventName && <span className={c.fieldError}>{errors.eventName}</span>}
        </label>

        <label className={c.field}>
          <span className={c.fieldLabel}>Venue name</span>
          <input
            type="text"
            value={venueName}
            onChange={(e) => setVenueName(e.target.value)}
            maxLength={200}
            className={c.textInput}
          />
          {errors.venueName && <span className={c.fieldError}>{errors.venueName}</span>}
        </label>

        {isError && <div className={c.formError}>Couldn&rsquo;t add that show. Try again.</div>}

        <div className={c.dialogActions}>
          <button type="button" onClick={onClose} className={c.buttonSecondary}>
            Cancel
          </button>
          <button type="submit" disabled={isPending} className={c.buttonPrimary}>
            {isPending ? 'Adding…' : 'Add event'}
          </button>
        </div>
      </form>
    </Dialog>
  );
}
