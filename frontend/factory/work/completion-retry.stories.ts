import {html} from 'lit';
import type {Meta, StoryObj} from '@storybook/web-components-vite';
import {decisionStoryPeer} from '../../storybook/decision-fixtures.js';
import type {DecisionStoryState} from '../../storybook/decision-fixtures.js';
import './completion-retry.js';

const meta = {
  title: 'Factory/Completion retry',
  args: {state: 'ready'},
  argTypes: {state: {control: 'select', options: ['ready', 'denied', 'offline', 'loading', 'unknown', 'recovered']}},
  render: ({state}) =>
    html`<soda-factory-completion-retry .context=${decisionStoryPeer(state).retry}></soda-factory-completion-retry>`,
} satisfies Meta<{state: DecisionStoryState}>;
export default meta;
type Story = StoryObj<{state: DecisionStoryState}>;
export const Ready: Story = {};
export const PermissionDenied: Story = {args: {state: 'denied'}};
export const Offline: Story = {args: {state: 'offline'}};
export const Loading: Story = {args: {state: 'loading'}};
export const LostReply: Story = {args: {state: 'unknown'}};
export const RecoveredRetry: Story = {args: {state: 'recovered'}};
