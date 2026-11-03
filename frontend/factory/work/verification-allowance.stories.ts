import {html} from 'lit';
import type {Meta, StoryObj} from '@storybook/web-components-vite';
import {allowanceStoryPeer} from '../../storybook/allowance-fixtures.js';
import type {AllowanceStoryState} from '../../storybook/allowance-fixtures.js';
import './verification-allowance.js';
const meta = {
  title: 'Factory/Verification limits',
  args: {state: 'ready'},
  argTypes: {
    state: {
      control: 'select',
      options: ['ready', 'read-only', 'loading', 'offline', 'denied', 'unknown', 'recovered', 'stale'],
    },
  },
  render: ({state}) =>
    html`<soda-factory-verification-allowance
      .context=${allowanceStoryPeer(state).context}
    ></soda-factory-verification-allowance>`,
} satisfies Meta<{state: AllowanceStoryState}>;
export default meta;
type Story = StoryObj<{state: AllowanceStoryState}>;
export const Ready: Story = {};
export const ReadOnly: Story = {args: {state: 'read-only'}};
export const Loading: Story = {args: {state: 'loading'}};
export const Offline: Story = {args: {state: 'offline'}};
export const Denied: Story = {args: {state: 'denied'}};
export const LostReply: Story = {args: {state: 'unknown'}};
export const RecoveredChange: Story = {args: {state: 'recovered'}};
export const StaleRevision: Story = {args: {state: 'stale'}};
