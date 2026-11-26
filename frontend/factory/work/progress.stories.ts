import {html} from 'lit';
import type {Meta, StoryObj} from '@storybook/web-components-vite';
import {progressStoryPeer} from '../../storybook/progress-fixtures.js';
import type {ProgressStoryState} from '../../storybook/progress-fixtures.js';
import {workPageStoryPeer} from '../../storybook/work-page-fixtures.js';
import './progress.js';

interface Args {
  view: 'overview' | 'repository' | 'runs' | 'no-runs' | 'plan' | 'direction' | 'roles' | 'setup';
  state: ProgressStoryState;
}
const meta = {
  title: 'Factory/Work progress',
  args: {view: 'overview', state: 'ready'},
  argTypes: {
    view: {control: 'select', options: ['overview', 'repository', 'runs', 'no-runs', 'plan', 'direction', 'roles', 'setup']},
    state: {control: 'select', options: ['ready', 'loading', 'missing', 'denied', 'offline', 'partial']},
  },
  render: ({view, state}) => {
    if (view === 'runs' || view === 'no-runs') {
      const page = workPageStoryPeer(view === 'runs' ? 'some' : 'none');
      return html`<soda-factory-work-overview .context=${page.context}></soda-factory-work-overview>`;
    }
    const peer = progressStoryPeer(state, undefined, view === 'setup' ? 'setup' : view === 'roles');
    if (view === 'plan') return html`<soda-factory-work-plan .context=${peer.plan}></soda-factory-work-plan>`;
    if (view === 'direction')
      return html`<soda-factory-direction-revision .context=${peer.direction}></soda-factory-direction-revision>`;
    if (view === 'repository') {
      const {intentID: _intent, ...context} = peer.overview;
      return html`<soda-factory-work-overview .context=${context}></soda-factory-work-overview>`;
    }
    return html`<soda-factory-work-overview .context=${peer.overview}></soda-factory-work-overview>`;
  },
} satisfies Meta<Args>;
export default meta;
type Story = StoryObj<Args>;
export const RequestOverview: Story = {};
export const RepositoryOverview: Story = {args: {view: 'repository'}};
export const WithRuns: Story = {args: {view: 'runs'}};
export const NoRuns: Story = {args: {view: 'no-runs'}};
export const Plan: Story = {args: {view: 'plan'}};
export const PartialPlan: Story = {args: {view: 'plan', state: 'partial'}};
export const DirectionHistory: Story = {args: {view: 'direction'}};
export const MissingDirection: Story = {args: {view: 'direction', state: 'missing'}};
export const RoleSettings: Story = {args: {view: 'roles'}};
export const InitialPolicy: Story = {args: {view: 'setup'}};
export const Loading: Story = {args: {state: 'loading'}};
export const PermissionDenied: Story = {args: {state: 'denied'}};
export const Offline: Story = {args: {state: 'offline'}};
