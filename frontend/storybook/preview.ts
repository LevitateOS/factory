import {html} from 'lit';
import type {Preview} from '@storybook/web-components-vite';
import '../factory/styles/base.css';
import '@xterm/xterm/css/xterm.css';

const preview: Preview = {
  initialGlobals: {scheme: 'light'},
  globalTypes: {
    scheme: {
      description: 'Product color scheme',
      toolbar: {icon: 'circlehollow', items: ['light', 'dark'], dynamicTitle: true},
    },
  },
  parameters: {layout: 'fullscreen'},
  decorators: [
    (story, context) =>
      context.title === 'Factory/Application'
        ? story()
        : html`<main class="soda-factory-root" data-factory-scheme=${context.globals.scheme}>${story()}</main>`,
  ],
};

export default preview;
