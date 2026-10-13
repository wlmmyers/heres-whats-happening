import AboutContent from '../components/AboutContent';
import Layout from '../components/Layout';
import RotatingLogo from '../components/RotatingLogo';
import * as c from '../styles/common.css';
import * as s from './AboutPage.css';

export default function AboutPage() {
  return (
    <Layout>
      <div className={s.logoWrap}>
        <RotatingLogo />
      </div>
      <div className={c.sectionHighPad}>
        <AboutContent />
      </div>
    </Layout>
  );
}
