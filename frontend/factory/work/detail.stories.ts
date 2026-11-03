import {html} from 'lit';
import type {Meta, StoryObj} from '@storybook/web-components-vite';
import {detailStoryPeer} from '../../storybook/detail-fixtures.js';
import type {DetailStoryState} from '../../storybook/detail-fixtures.js';
import './detail.js';

const meta = {
  title: 'Factory/Work detail',
  args: {state: 'ready'},
  argTypes: {
    state: {control: 'select', options: ['ready', 'read-only', 'loading', 'offline', 'denied', 'unknown', 'recovered']},
  },
  render: ({state}) =>
    html`<soda-factory-work-detail .context=${detailStoryPeer(state).context}></soda-factory-work-detail>`,
} satisfies Meta<{state: DetailStoryState}>;
export default meta;
type Story = StoryObj<{state: DetailStoryState}>;
export const Ready: Story = {};
export const ReadOnly: Story = {args: {state: 'read-only'}};
export const Loading: Story = {args: {state: 'loading'}};
export const Offline: Story = {args: {state: 'offline'}};
export const PermissionDenied: Story = {args: {state: 'denied'}};
export const LostRetryReply: Story = {args: {state: 'unknown'}};
export const RecoveredRetry: Story = {args: {state: 'recovered'}};
