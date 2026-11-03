import type {Meta, StoryObj} from '@storybook/web-components-vite';
import {SodaFactoryApp} from './factory-app.js';
import {appStoryPeer} from '../../storybook/app-fixtures.js';
import type {AppStoryState} from '../../storybook/app-fixtures.js';

const meta = {
  title: 'Factory/Application',
  args: {state: 'ready'},
  argTypes: {
    state: {
      control: 'select',
      options: ['ready', 'loading', 'unavailable', 'disconnected', 'issues', 'issue', 'issue-new'],
    },
  },
  render: ({state}, context) => {
    const view = new SodaFactoryApp();
    view.scheme = context.globals.scheme === 'dark' ? 'dark' : 'light';
    appStoryPeer(state).attach(view);
    return view;
  },
} satisfies Meta<{state: AppStoryState}>;
export default meta;
type Story = StoryObj<{state: AppStoryState}>;
export const ConnectedFrame: Story = {};
export const LoadingAdmission: Story = {args: {state: 'loading'}};
export const UnavailableAdmission: Story = {args: {state: 'unavailable'}};
export const Disconnected: Story = {args: {state: 'disconnected'}};
export const IssueList: Story = {args: {state: 'issues'}};
export const NewIssue: Story = {args: {state: 'issue-new'}};
export const IssueAndFactoryFacts: Story = {args: {state: 'issue'}};
