import type {Meta, StoryObj} from '@storybook/web-components-vite';
import {SodaFactoryApp} from './factory-app.js';
import {appStoryPeer} from '../../storybook/app-fixtures.js';
import type {AppStoryState} from '../../storybook/app-fixtures.js';
import {disconnectedSignInStory} from '../../storybook/sign-in-fixtures.js';
import type {SignInStoryState} from '../../storybook/sign-in-fixtures.js';

const meta = {
  title: 'Factory/Application',
  args: {state: 'ready'},
  argTypes: {
    state: {
      control: 'select',
      options: ['ready', 'loading', 'unavailable', 'disconnected', 'issues', 'issue', 'issue-new', 'settings', 'accounts', 'projects', 'feed-projects', 'jump'],
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
export const Settings: Story = {args: {state: 'settings'}};
export const Accounts: Story = {args: {state: 'accounts'}};
/** Projects across accounts, one row in each state. */
export const Projects: Story = {args: {state: 'projects'}};
/** A feed route whose sidebar lists the host's other projects. */
export const FeedWithProjectList: Story = {args: {state: 'feed-projects'}};
/** The quick switcher open over a feed that has the project list. */
export const JumpToOpen: Story = {
  args: {state: 'jump'},
  play: async ({canvasElement}) => {
    const button = canvasElement.querySelector<HTMLButtonElement>('.soda-factory-jump-open');
    for (let i = 0; i < 50 && (!button || button.disabled); i++) await new Promise((r) => setTimeout(r, 50));
    await new Promise((r) => setTimeout(r, 300));
    button?.click();
  },
};
/** The desktop's guided first run, shown while the frame has no context. */
const firstRun = (state: SignInStoryState): Story => ({
  args: {state: 'disconnected'},
  render: (_, context) => disconnectedSignInStory(context.globals.scheme === 'dark' ? 'dark' : 'light', state),
});
export const SignInWithoutConnection = firstRun('github');
export const FirstRunStarting = firstRun('starting');
export const FirstRunMuse = firstRun('muse');
export const FirstRunProject = firstRun('project');
