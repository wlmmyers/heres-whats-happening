import { useState, type MouseEvent, type ReactNode } from 'react';
import { createPortal } from 'react-dom';
import clsx from 'clsx';
import type { CalendarEvent, ImageCredit } from '../api/calendar';
import { getArtistImage } from '../utils/artistImage';
import ExternalLink from './ExternalLink';
import * as s from './ArtistImage.css';
import * as c from '../styles/common.css';

export default function ArtistImage({
  event,
  className,
}: {
  event: CalendarEvent;
  className?: string;
}) {
  const [creditOpen, setCreditOpen] = useState(false);
  const { url, credit } = getArtistImage(event);
  if (!url) return null;
  return (
    <div data-thumbnail className={clsx(s.container, className)}>
      <img src={url} alt="" className={s.image} />
      {credit && (
        <button
          type="button"
          aria-label="Image credit"
          className={s.creditButton}
          // The image lives inside a click-to-navigate event card.
          onClick={(e) => {
            e.stopPropagation();
            setCreditOpen(true);
          }}
        >
          <img src="/infoIcon.png" alt="" aria-hidden className={s.creditIcon} />
        </button>
      )}
      {creditOpen && credit && (
        <ImageCreditDialog credit={credit} onClose={() => setCreditOpen(false)} />
      )}
    </div>
  );
}

function ImageCreditDialog({ credit, onClose }: { credit: ImageCredit; onClose: () => void }) {
  const stopCardClick = (e: MouseEvent) => e.stopPropagation();
  const license = credit.license_short_name || credit.license;
  // The event card scales on hover, and a transformed ancestor is the
  // containing block for `position: fixed` — so the backdrop only covers the
  // viewport from outside the card.
  return createPortal(
    <div
      data-testid="image-credit-backdrop"
      className={s.creditBackdrop}
      onClick={(e) => {
        stopCardClick(e);
        onClose();
      }}
    >
      <div
        role="dialog"
        aria-modal="true"
        aria-labelledby="image-credit-title"
        className={s.creditCard}
        onClick={stopCardClick}
      >
        <h2 id="image-credit-title" className={s.creditTitle}>
          Image credit
        </h2>
        <dl className={s.creditList}>
          <CreditRow label="Photographer">{credit.artist}</CreditRow>
          <CreditRow label="Credit">{credit.credit}</CreditRow>
          <CreditRow label="License">
            {license && <ExternalLink href={credit.license_url}>{license}</ExternalLink>}
          </CreditRow>
          <CreditRow label="Usage terms">
            {credit.usage_terms !== license && credit.usage_terms}
          </CreditRow>
          <CreditRow label="File">
            {credit.file && (
              <ExternalLink href={credit.description_url}>{credit.file}</ExternalLink>
            )}
          </CreditRow>
        </dl>
        <div className={s.creditActions}>
          <button type="button" onClick={onClose} className={c.buttonSecondary}>
            Close
          </button>
        </div>
      </div>
    </div>,
    document.body,
  );
}

/** Renders nothing at all when the enrichment left the field empty. */
function CreditRow({ label, children }: { label: string; children?: ReactNode }) {
  if (!children) return null;
  return (
    <>
      <dt className={s.creditLabel}>{label}</dt>
      <dd className={s.creditValue}>{children}</dd>
    </>
  );
}
