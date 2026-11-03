import type {Meta, StoryObj} from '@storybook/web-components-vite';
import {SodaFactoryWatch} from './watch.js';
import {watchRenderer, watchStoryPeer} from '../../storybook/watch-fixtures.js';
import type {WatchStoryState} from '../../storybook/watch-fixtures.js';

const meta = {
  title: 'Factory/Watch',
  args: {state: 'live'},
  argTypes: {state: {control: 'select', options: ['live', 'idle', 'loading', 'ended', 'denied', 'offline', 'invalid']}},
  render: ({state}) => {
    const view = new SodaFactoryWatch();
    view.configure(watchStoryPeer(state).context, async () => watchRenderer);
    if (state !== 'idle') queueMicrotask(() => void view.watch());
    return view;
  },
} satisfies Meta<{state: WatchStoryState}>;
export default meta;
type Story = StoryObj<{state: WatchStoryState}>;
export const Live: Story = {};
export const Idle: Story = {args: {state: 'idle'}};
export const Loading: Story = {args: {state: 'loading'}};
export const Ended: Story = {args: {state: 'ended'}};
export const PermissionDenied: Story = {args: {state: 'denied'}};
export const Offline: Story = {args: {state: 'offline'}};
export const InvalidStream: Story = {args: {state: 'invalid'}};
