import RotatingLogo from './RotatingLogo';
import SectionTitle from './SectionTitle';
import * as s from './AboutContent.css';

type Feature = { name: string; description: string };
type Upcoming = { name: string; description: string };

// Prose written for the About page, not transcribed from a bullet list.
const FEATURES: Feature[] = [
  {
    name: 'Rich ingestion of events',
    description:
      'We pull events from the major ticketing platforms and from local promoter newsletters and concert flyers.',
  },
  {
    name: 'Interests from your listening history and more',
    description:
      "Link your Spotify account and we'll read the artists and genres you already listen to, then turn them into interests that get matched against local events.",
  },
  {
    name: 'Intelligent matching',
    description:
      'Every event is scored against your taste using a blend of keyword and AI semantic matching.',
  },
  {
    name: 'Integrate with calendar apps',
    description:
      'Generate a personal feed and subscribe to it from Apple Calendar, Google Calendar, or Fantastical.',
  },
  {
    name: 'Quick to use, and then out of your way',
    description:
      'Sign up, connect Spotify, adjust your interests, and browse your event calendar from this web app or any calendar app.',
  },
];

const COMING_SOON: Upcoming[] = [
  {
    name: 'Build your own day view',
    description:
      'Design an at-a-glance layout and render it to an always-on screen on your desk or widget on your desktop.',
  },
  {
    name: 'More cities',
    description: 'Planned support for New York, San Francisco, and more.',
  },
  {
    name: 'Better live sports coverage',
    description: 'Event coverage for more sports and leagues.',
  },
];

export default function AboutContent() {
  return (
    <div>
      <div className={s.logoWrap}>
        <RotatingLogo />
      </div>
      <p className={s.lede}>
        Live events for all types. Connect Spotify or tell us your interests and we'll surface shows
        you'll love - in Seattle for now; expanding soon!
      </p>
      <div className={s.birds}>
        <img src={'/bird1.png'} className={s.bird} />
        <img src={'/bird2.png'} className={s.bird} />
        <img src={'/bird3.png'} className={s.bird} />
        <img src={'/bird4.png'} className={s.bird} />
        <img src={'/bird5.png'} className={s.bird} />
      </div>

      <ul className={s.list}>
        {FEATURES.map((f) => (
          <li key={f.name} className={s.featureRow}>
            <strong className={s.rowName}>{f.name}</strong>
            <span className={s.rowText}>{f.description}</span>
          </li>
        ))}
      </ul>
      <SectionTitle>Coming soon</SectionTitle>
      <ul className={s.list}>
        {COMING_SOON.map((item) => (
          <li key={item.name} className={s.comingRow}>
            <strong className={s.rowName}>{item.name}</strong>
            <span className={s.rowText}>{item.description}</span>
          </li>
        ))}
      </ul>
    </div>
  );
}
