import {html} from 'lit';
import type {Meta, StoryObj} from '@storybook/web-components-vite';
import {repairStoryPeer} from '../../storybook/repair-fixtures.js';
import type {RepairStoryState} from '../../storybook/repair-fixtures.js';
import './repair-funding.js';
const meta = {
  title: 'Factory/Repair funding',
  args: {state: 'unfunded'},
  argTypes: {
    state: {
      control: 'select',
      options: ['unfunded', 'active', 'withdrawn', 'loading', 'offline', 'denied', 'unknown', 'recovered', 'stale'],
    },
  },
  render: ({state}) => html`<soda-factory-repair .context=${repairStoryPeer(state).context}></soda-factory-repair>`,
} satisfies Meta<{state: RepairStoryState}>;
export default meta;
type Story = StoryObj<{state: RepairStoryState}>;
export const Unfunded: Story = {};
export const ActiveGrant: Story = {args: {state: 'active'}};
export const Withdrawn: Story = {args: {state: 'withdrawn'}};
export const Loading: Story = {args: {state: 'loading'}};
export const Offline: Story = {args: {state: 'offline'}};
export const Denied: Story = {args: {state: 'denied'}};
export const LostReply: Story = {args: {state: 'unknown'}};
export const RecoveredChange: Story = {args: {state: 'recovered'}};
export const StaleRevision: Story = {args: {state: 'stale'}};
