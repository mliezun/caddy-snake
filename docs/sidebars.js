// @ts-check

/** @type {import('@docusaurus/plugin-content-docs').SidebarsConfig} */
const sidebars = {
  tutorialSidebar: [
    {
      type: 'category',
      label: 'Get started',
      collapsed: false,
      items: ['intro', 'installation', 'examples', 'apps'],
    },
    {
      type: 'category',
      label: 'Configure',
      collapsed: false,
      items: ['reference', 'esgi', 'isolation'],
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
