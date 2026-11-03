import {html} from 'lit';
import type {Meta, StoryObj} from '@storybook/web-components-vite';
import {progressStoryPeer} from '../../storybook/progress-fixtures.js';
import type {ProgressStoryState} from '../../storybook/progress-fixtures.js';
import './progress.js';

interface Args {
  view: 'overview' | 'repository' | 'plan' | 'direction';
  state: ProgressStoryState;
}
const meta = {
  title: 'Factory/Work progress',
  args: {view: 'overview', state: 'ready'},
  argTypes: {
    view: {control: 'select', options: ['overview', 'repository', 'plan', 'direction']},
    state: {control: 'select', options: ['ready', 'loading', 'missing', 'denied', 'offline', 'partial']},
  },
  render: ({view, state}) => {
    const peer = progressStoryPeer(state);
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
export const Plan: Story = {args: {view: 'plan'}};
export const PartialPlan: Story = {args: {view: 'plan', state: 'partial'}};
export const DirectionHistory: Story = {args: {view: 'direction'}};
export const MissingDirection: Story = {args: {view: 'direction', state: 'missing'}};
export const Loading: Story = {args: {state: 'loading'}};
export const PermissionDenied: Story = {args: {state: 'denied'}};
export const Offline: Story = {args: {state: 'offline'}};
