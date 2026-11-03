import type {Meta, StoryObj} from '@storybook/web-components-vite';
import {SodaFactoryIssueComment} from './comment.js';
import {issueCommentStoryPeer} from '../../storybook/issue-comment-fixtures.js';
import type {IssueCommentStoryState} from '../../storybook/issue-comment-fixtures.js';
const meta = {
  title: 'Factory/Issue comment',
  args: {state: 'ready'},
  argTypes: {state: {control: 'select', options: ['ready', 'denied', 'offline', 'pending']}},
  render: ({state}) => {
    const view = new SodaFactoryIssueComment();
    view.context = issueCommentStoryPeer(state);
    return view;
  },
} satisfies Meta<{state: IssueCommentStoryState}>;
export default meta;
type Story = StoryObj<{state: IssueCommentStoryState}>;
export const WriteComment: Story = {};
export const Denied: Story = {args: {state: 'denied'}};
export const Offline: Story = {args: {state: 'offline'}};
export const PendingSubmission: Story = {args: {state: 'pending'}};
