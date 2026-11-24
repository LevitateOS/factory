import type {Meta, StoryObj} from '@storybook/web-components-vite';
import {SodaFactoryPulls} from './list.js';
import {pullListStoryPeer} from '../../storybook/pull-fixtures.js';
import type {PullListStoryState} from '../../storybook/pull-fixtures.js';

const meta = {
  title: 'Factory/Pull requests',
  args: {state: 'ready'},
  argTypes: {
    state: {
      control: 'select',
      options: ['ready', 'empty', 'loading', 'offline', 'denied', 'paged'],
    },
  },
  render: ({state}) => {
    const view = new SodaFactoryPulls();
    view.context = pullListStoryPeer(state).context;
    return view;
  },
} satisfies Meta<{state: PullListStoryState}>;
export default meta;
type Story = StoryObj<{state: PullListStoryState}>;
export const OpenPulls: Story = {};
export const Empty: Story = {args: {state: 'empty'}};
export const Loading: Story = {args: {state: 'loading'}};
export const Offline: Story = {args: {state: 'offline'}};
export const Denied: Story = {args: {state: 'denied'}};
export const Paged: Story = {args: {state: 'paged'}};
