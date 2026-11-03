import {html} from 'lit';
import type {Meta, StoryObj} from '@storybook/web-components-vite';
import {decisionStoryPeer} from '../../storybook/decision-fixtures.js';
import type {DecisionStoryState} from '../../storybook/decision-fixtures.js';
import './acceptance.js';

const meta = {
  title: 'Factory/Requirements acceptance',
  args: {state: 'ready'},
  argTypes: {
    state: {control: 'select', options: ['ready', 'denied', 'offline', 'loading', 'unknown', 'stale', 'recovered']},
  },
  render: ({state}) =>
    html`<soda-factory-requirements-acceptance
      .context=${decisionStoryPeer(state).acceptance}
    ></soda-factory-requirements-acceptance>`,
} satisfies Meta<{state: DecisionStoryState}>;
export default meta;
type Story = StoryObj<{state: DecisionStoryState}>;
export const Ready: Story = {};
export const PermissionDenied: Story = {args: {state: 'denied'}};
export const Offline: Story = {args: {state: 'offline'}};
export const Loading: Story = {args: {state: 'loading'}};
export const LostReply: Story = {args: {state: 'unknown'}};
export const StaleEvidence: Story = {args: {state: 'stale'}};
export const RecoveredAcceptance: Story = {args: {state: 'recovered'}};
