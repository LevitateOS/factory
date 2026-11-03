import {html} from 'lit';
import type {Meta, StoryObj} from '@storybook/web-components-vite';
import {verificationStoryPeer} from '../../storybook/verification-fixtures.js';
import type {VerificationStoryState} from '../../storybook/verification-fixtures.js';
import './verification-retry.js';

const meta = {
  title: 'Factory/Application verification',
  args: {state: 'retry'},
  argTypes: {
    state: {
      control: 'select',
      options: [
        'retry',
        'repair',
        'investigating',
        'attention',
        'adopted',
        'empty',
        'loading',
        'offline',
        'denied',
        'unknown-retry',
        'unknown-repair',
        'recovered-retry',
        'recovered-repair',
      ],
    },
  },
  render: ({state}) =>
    html`<soda-factory-verification-retry
      .context=${verificationStoryPeer(state).context}
    ></soda-factory-verification-retry>`,
} satisfies Meta<{state: VerificationStoryState}>;
export default meta;
type Story = StoryObj<{state: VerificationStoryState}>;
export const RetryVerification: Story = {};
export const RequestRepair: Story = {args: {state: 'repair'}};
export const Investigating: Story = {args: {state: 'investigating'}};
export const Attention: Story = {args: {state: 'attention'}};
export const Adopted: Story = {args: {state: 'adopted'}};
export const Empty: Story = {args: {state: 'empty'}};
export const Loading: Story = {args: {state: 'loading'}};
export const Offline: Story = {args: {state: 'offline'}};
export const PermissionDenied: Story = {args: {state: 'denied'}};
export const LostRetryReply: Story = {args: {state: 'unknown-retry'}};
export const LostRepairReply: Story = {args: {state: 'unknown-repair'}};
export const RecoveredRetry: Story = {args: {state: 'recovered-retry'}};
export const RecoveredRepair: Story = {args: {state: 'recovered-repair'}};
