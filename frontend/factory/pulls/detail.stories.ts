import type {Meta, StoryObj} from '@storybook/web-components-vite';
import {SodaFactoryPull} from './detail.js';
import type {PullTab} from './detail.js';
import {pullDetailStoryPeer} from '../../storybook/pull-fixtures.js';
import type {PullDetailStoryState} from '../../storybook/pull-fixtures.js';

const meta = {
  title: 'Factory/Pull request',
  args: {tab: 'conversation', state: 'ready'},
  argTypes: {
    tab: {
      control: 'select',
      options: ['conversation', 'commits', 'reviews', 'threads', 'files', 'checks'],
    },
    state: {
      control: 'select',
      options: ['ready', 'loading', 'offline', 'denied'],
    },
  },
  render: ({tab, state}) => {
    const view = new SodaFactoryPull();
    view.context = pullDetailStoryPeer(tab as PullTab, state as PullDetailStoryState).context;
    return view;
  },
} satisfies Meta<{tab: PullTab; state: PullDetailStoryState}>;
export default meta;
type Story = StoryObj<{tab: PullTab; state: PullDetailStoryState}>;
export const Conversation: Story = {};
export const Commits: Story = {args: {tab: 'commits'}};
export const Reviews: Story = {args: {tab: 'reviews'}};
export const InlineDiscussions: Story = {args: {tab: 'threads'}};
export const Files: Story = {args: {tab: 'files'}};
export const Checks: Story = {args: {tab: 'checks'}};
export const Loading: Story = {args: {state: 'loading'}};
export const Offline: Story = {args: {state: 'offline'}};
export const Denied: Story = {args: {state: 'denied'}};
