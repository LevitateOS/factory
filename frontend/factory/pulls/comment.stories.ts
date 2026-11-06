import type {Meta, StoryObj} from '@storybook/web-components-vite';
import {SodaFactoryIssueComment} from '../issues/comment.js';
import {pullCommentStoryPeer} from '../../storybook/issue-comment-fixtures.js';

const meta = {
  title: 'Factory/Pull comment',
  render: () => {
    const view = new SodaFactoryIssueComment();
    view.context = pullCommentStoryPeer();
    return view;
  },
} satisfies Meta;
export default meta;
type Story = StoryObj;
export const WriteComment: Story = {};
