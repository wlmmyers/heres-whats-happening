import { useId, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import Dialog from './Dialog';
import { useAuth } from '../auth/useAuth';
import * as c from '../styles/common.css';
import * as s from './DeleteAccountDialog.css';

const CONFIRM_PHRASE = 'delete account';

interface Props {
  open: boolean;
  onClose: () => void;
}

/**
 * Confirms a permanent account deletion. The delete button stays disabled until
 * the user types the confirm phrase, so a stray click can't take out an
 * account. On success the user is signed out and sent to the login screen.
 */
export default function DeleteAccountDialog({ open, onClose }: Props) {
  const messageId = useId();
  return (
    <Dialog
      topOnPhone
      open={open}
      heading="Delete your account?"
      onClose={onClose}
      aria-describedby={messageId}
    >
      <DeleteAccountForm messageId={messageId} onClose={onClose} />
    </Dialog>
  );
}

/**
 * A child of the dialog rather than part of it, so the typed phrase is
 * discarded when the dialog closes and a reopened dialog starts empty.
 */
function DeleteAccountForm({ messageId, onClose }: { messageId: string; onClose: () => void }) {
  const { deleteAccount } = useAuth();
  const navigate = useNavigate();
  const [phrase, setPhrase] = useState('');
  const [pending, setPending] = useState(false);
  const [failed, setFailed] = useState(false);
  const matches = phrase.trim() === CONFIRM_PHRASE;

  async function onSubmit(e: React.SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    if (!matches || pending) return;
    setPending(true);
    setFailed(false);
    try {
      await deleteAccount();
    } catch {
      setPending(false);
      setFailed(true);
      return;
    }
    // pending stays set: the page is about to go away, and the button must not
    // come back to life for a second delete in the meantime.
    navigate('/login', { replace: true });
  }

  return (
    <>
      <p id={messageId} className={s.message}>
        This permanently deletes your account and everything saved to it: your interests, Spotify
        connection, going and hidden events, the shows you added, and your calendar subscription. It
        can&rsquo;t be undone.
      </p>
      <form onSubmit={onSubmit} className={s.form}>
        <label className={c.field}>
          <span className={c.fieldLabel}>
            Type <strong>{CONFIRM_PHRASE}</strong> to confirm
          </span>
          <input
            type="text"
            value={phrase}
            onChange={(e) => setPhrase(e.target.value)}
            // Phones would otherwise capitalise the first letter, which the
            // exact match then rejects.
            autoCapitalize="off"
            autoCorrect="off"
            autoComplete="off"
            spellCheck={false}
            className={c.textInput}
          />
        </label>

        {failed && (
          <div role="alert" className={c.formError}>
            Couldn&rsquo;t delete your account. Try again.
          </div>
        )}

        <div className={c.dialogActions}>
          <button type="button" onClick={onClose} className={c.buttonSecondary}>
            Cancel
          </button>
          <button type="submit" disabled={!matches || pending} className={c.buttonDanger}>
            Delete my account
          </button>
        </div>
      </form>
    </>
  );
}
