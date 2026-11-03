import type {Meta, StoryObj} from '@storybook/web-components-vite';
import {SodaFactoryIssues} from './issues.js';
import {issuesStoryPeer} from '../../storybook/issues-fixtures.js';
import type {IssueListStoryState} from '../../storybook/issues-fixtures.js';

const meta = {
  title: 'Factory/Issues',
  args: {state: 'ready'},
  argTypes: {
    state: {
      control: 'select',
      options: ['ready', 'empty', 'loading', 'offline', 'denied', 'paged', 'missing', 'plain-text'],
    },
  },
  render: ({state}) => {
    const peer = issuesStoryPeer(state),
      view = new SodaFactoryIssues();
    view.context = peer.context;
    return view;
  },
} satisfies Meta<{state: IssueListStoryState}>;
export default meta;
type Story = StoryObj<{state: IssueListStoryState}>;
export const OpenIssues: Story = {};
export const Empty: Story = {args: {state: 'empty'}};
export const Loading: Story = {args: {state: 'loading'}};
export const Offline: Story = {args: {state: 'offline'}};
export const Denied: Story = {args: {state: 'denied'}};
export const Paged: Story = {args: {state: 'paged'}};
export const MissingMetadata: Story = {args: {state: 'missing'}};
export const LiteralWriting: Story = {args: {state: 'plain-text'}};
