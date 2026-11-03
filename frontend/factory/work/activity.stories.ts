import {html} from 'lit';
import type {Meta, StoryObj} from '@storybook/web-components-vite';
import {activityStoryPeer} from '../../storybook/activity-fixtures.js';
import type {ActivityStoryState} from '../../storybook/activity-fixtures.js';
import './activity.js';
const meta = {
  title: 'Factory/Activity',
  args: {state: 'recorded', navigation: true},
  argTypes: {
    state: {
      control: 'select',
      options: ['recorded', 'empty', 'loading', 'offline', 'denied', 'read-only', 'unknown', 'recovered', 'truncated'],
    },
  },
  render: ({state, navigation}) => {
    const peer = activityStoryPeer(state),
      {presentItem: _links, ...plain} = peer.context;
    return html`<soda-factory-activity .context=${navigation ? peer.context : plain}></soda-factory-activity>`;
  },
} satisfies Meta<{state: ActivityStoryState; navigation: boolean}>;
export default meta;
type Story = StoryObj<{state: ActivityStoryState; navigation: boolean}>;
export const Recorded: Story = {};
export const PlainText: Story = {args: {navigation: false}};
export const Empty: Story = {args: {state: 'empty'}};
export const Loading: Story = {args: {state: 'loading'}};
export const Offline: Story = {args: {state: 'offline'}};
export const Denied: Story = {args: {state: 'denied'}};
export const ReadOnly: Story = {args: {state: 'read-only'}};
export const LostReply: Story = {args: {state: 'unknown'}};
export const RecoveredPreferences: Story = {args: {state: 'recovered'}};
export const PartialHistory: Story = {args: {state: 'truncated'}};
