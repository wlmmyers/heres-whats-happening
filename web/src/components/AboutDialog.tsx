import { Link } from 'react-router-dom';
import AboutContent from './AboutContent';
import * as s from './AboutDialog.css';
import * as c from '../styles/common.css';

export default function AboutDialog() {
  return (
    <div role="dialog" aria-label="About" className={s.aboutDialogWrapper}>
      <div className={s.aboutCard}>
        <p className={s.backLinkSection}>
          <Link to="/signup" className={c.link}>
            {`< Back`}
          </Link>
        </p>
        <AboutContent />
        <p className={s.backLinkSectionBottom}>
          <Link to="/signup" className={c.link}>
            {`Create an account >`}
          </Link>
        </p>
      </div>
    </div>
  );
}
