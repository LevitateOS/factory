import type {Meta, StoryObj} from '@storybook/web-components-vite';
import {SodaFactoryIssueEditor} from './editor.js';
import {issueEditorStoryPeer} from '../../storybook/issue-editor-fixtures.js';
import type {IssueEditorStoryState} from '../../storybook/issue-editor-fixtures.js';
const meta = {
  title: 'Factory/Issue editor',
  args: {state: 'ready'},
  argTypes: {state: {control: 'select', options: ['ready', 'denied', 'offline', 'pending', 'stale']}},
  render: ({state}) => {
    const view = new SodaFactoryIssueEditor();
    view.context = issueEditorStoryPeer(state);
    return view;
  },
} satisfies Meta<{state: IssueEditorStoryState}>;
export default meta;
type Story = StoryObj<{state: IssueEditorStoryState}>;
export const EditIssue: Story = {};
export const Denied: Story = {args: {state: 'denied'}};
export const Offline: Story = {args: {state: 'offline'}};
export const PendingEdit: Story = {args: {state: 'pending'}};
export const ChangedOriginal: Story = {args: {state: 'stale'}};
