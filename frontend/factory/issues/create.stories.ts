import type {Meta, StoryObj} from '@storybook/web-components-vite';
import {SodaFactoryIssueCreate} from './create.js';
import {issueCreateStoryPeer} from '../../storybook/issue-create-fixtures.js';
import type {IssueCreateStoryState} from '../../storybook/issue-create-fixtures.js';
const meta = {
  title: 'Factory/New issue',
  args: {state: 'ready'},
  argTypes: {state: {control: 'select', options: ['ready', 'denied', 'offline', 'pending', 'created']}},
  render: ({state}) => {
    const view = new SodaFactoryIssueCreate();
    view.context = issueCreateStoryPeer(state);
    return view;
  },
} satisfies Meta<{state: IssueCreateStoryState}>;
export default meta;
type Story = StoryObj<{state: IssueCreateStoryState}>;
export const WriteIssue: Story = {};
export const Denied: Story = {args: {state: 'denied'}};
export const Offline: Story = {args: {state: 'offline'}};
export const PendingSubmission: Story = {args: {state: 'pending'}};
export const SavedDestination: Story = {args: {state: 'created'}};
