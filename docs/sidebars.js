// @ts-check

// Two sections, each with its own sidebar:
//   - Apps: the platform for building and sharing small internal apps.
//   - Caddy plugin: running Python apps from a Caddyfile or `caddysnake`.
// The plugin pages keep the addresses they always had (/docs/intro,
// /docs/reference, ...), so existing links keep working.

/** @type {import('@docusaurus/plugin-content-docs').SidebarsConfig} */
const sidebars = {
  appsSidebar: [
    'apps/index',
    'apps/assistant',
    'apps/how-apps-work',
    'apps/sharing',
    'apps/running',
  ],
  pluginSidebar: [
    {
      type: 'category',
      label: 'Get started',
      collapsed: false,
      items: ['intro', 'installation', 'examples'],
    },
    {
      type: 'category',
      label: 'Configure',
      collapsed: false,
      items: ['reference', 'isolation'],
    },
    {
      type: 'category',
      label: 'Understand',
      collapsed: false,
      items: ['architecture', 'benchmarks'],
    },
    {
      type: 'category',
      label: 'Advanced',
      collapsed: true,
      items: ['embed-app'],
    },
  ],
};

export default sidebars;
