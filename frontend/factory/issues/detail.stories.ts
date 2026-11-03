import type {Meta, StoryObj} from '@storybook/web-components-vite';
import {SodaFactoryIssue} from './detail.js';
import {issueDetailStoryPeer} from '../../storybook/issue-detail-fixtures.js';
import type {IssueDetailStoryState} from '../../storybook/issue-detail-fixtures.js';
const meta = {
  title: 'Factory/Issue detail',
  args: {state: 'ready'},
  argTypes: {
    state: {
      control: 'select',
      options: ['ready', 'paged', 'empty', 'loading', 'offline', 'denied', 'closed', 'missing', 'hostile', 'large'],
    },
  },
  render: ({state}) => {
    const view = new SodaFactoryIssue();
    view.context = issueDetailStoryPeer(state).context;
    return view;
  },
} satisfies Meta<{state: IssueDetailStoryState}>;
export default meta;
type Story = StoryObj<{state: IssueDetailStoryState}>;
export const DescriptionAndConversation: Story = {};
export const PagedConversation: Story = {args: {state: 'paged'}};
export const Empty: Story = {args: {state: 'empty'}};
export const Loading: Story = {args: {state: 'loading'}};
export const Offline: Story = {args: {state: 'offline'}};
export const Denied: Story = {args: {state: 'denied'}};
export const Closed: Story = {args: {state: 'closed'}};
export const MissingMetadata: Story = {args: {state: 'missing'}};
export const UntrustedMarkdown: Story = {args: {state: 'hostile'}};
export const LargeWriting: Story = {args: {state: 'large'}};
