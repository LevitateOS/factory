import {html} from 'lit';
import type {Meta, StoryObj} from '@storybook/web-components-vite';
import {contextStoryPeer} from '../../storybook/context-fixtures.js';
import type {ContextStoryState} from '../../storybook/context-fixtures.js';
import './context-resolution.js';

interface Args {
  state: ContextStoryState;
  entry: 'issue' | 'run';
}
const meta = {
  title: 'Factory/Context choice',
  args: {state: 'complete', entry: 'issue'},
  argTypes: {
    state: {
      control: 'select',
      options: [
        'complete',
        'incomplete',
        'loading',
        'offline',
        'denied',
        'unknown',
        'stale',
        'account-unavailable',
        'recovered',
      ],
    },
    entry: {control: 'select', options: ['issue', 'run']},
  },
  render: ({state, entry}) =>
    html`<soda-factory-context-resolution
      .context=${contextStoryPeer(state).context(entry)}
    ></soda-factory-context-resolution>`,
} satisfies Meta<Args>;
export default meta;
type Story = StoryObj<Args>;
export const Complete: Story = {};
export const DirectRun: Story = {args: {entry: 'run'}};
export const Incomplete: Story = {args: {state: 'incomplete'}};
export const Loading: Story = {args: {state: 'loading'}};
export const Offline: Story = {args: {state: 'offline'}};
export const PermissionDenied: Story = {args: {state: 'denied'}};
export const LostReply: Story = {args: {state: 'unknown'}};
export const StaleChoice: Story = {args: {state: 'stale'}};

export const AccountUnavailable: Story = {args: {state: 'account-unavailable'}};

export const RecoveredChoice: Story = {args: {state: 'recovered'}};
