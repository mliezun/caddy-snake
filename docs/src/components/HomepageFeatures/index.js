import clsx from 'clsx';
import Heading from '@theme/Heading';
import styles from './styles.module.css';

const FeatureList = [
  {
    title: 'Ask for it',
    description: (
      <>
        Your assistant writes the app and publishes it. Each app gets its own
        address and its own database. No AI is included: bring Claude,
        ChatGPT, Cursor or any other that uses MCP tools.
      </>
    ),
  },
  {
    title: 'Safe to change',
    description: (
      <>
        Every save is a version. A version that does not start is refused and
        the running one stays up. Try bigger changes in a draft, with a copy
        of the data.
      </>
    ),
  },
  {
    title: 'Yours to run',
    description: (
      <>
        One program on your own machine: Linux, macOS or Windows. Open on your
        computer, sign-in for the team, automatic HTTPS with a domain.
      </>
    ),
  },
];

function Feature({title, description}) {
  return (
    <div className={clsx('col col--4')}>
      <div className={styles.featureItem}>
        <Heading as="h3" className={styles.featureTitle}>
          {title}
        </Heading>
        <p className={styles.featureText}>{description}</p>
      </div>
    </div>
  );
}

export default function HomepageFeatures() {
  return (
    <section className={styles.features}>
      <div className="container">
        <div className="row">
          {FeatureList.map((props, idx) => (
            <Feature key={idx} {...props} />
          ))}
        </div>
      </div>
    </section>
  );
}
