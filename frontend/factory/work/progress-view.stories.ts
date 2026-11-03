import type {Meta, StoryObj} from '@storybook/web-components-vite';
import {presentationLinks, renderPresentation} from '../../storybook/work-presentation-fixtures.js';
import type {PresentationState, PresentationView} from '../../storybook/work-presentation-fixtures.js';

interface Args {
  view: PresentationView;
  state: PresentationState;
  navigation: boolean;
}
const meta = {
  title: 'Factory/Work presentation',
  args: {view: 'detail', state: 'recorded', navigation: true},
  argTypes: {
    view: {
      control: 'select',
      options: ['detail', 'request', 'plan', 'overview', 'direction', 'access', 'history', 'interventions'],
    },
    state: {control: 'select', options: ['recorded', 'blocked', 'delivered', 'missing', 'unavailable']},
    navigation: {control: 'boolean'},
  },
  render: ({view, state, navigation}) => renderPresentation(view, state, navigation ? presentationLinks() : undefined),
} satisfies Meta<Args>;
export default meta;
type Story = StoryObj<Args>;
export const WorkDetail: Story = {};
export const Delivered: Story = {args: {state: 'delivered'}};
export const Blocked: Story = {args: {state: 'blocked'}};
export const WithoutNavigation: Story = {args: {navigation: false}};
export const RequestOverview: Story = {args: {view: 'request'}};
export const WorkPlan: Story = {args: {view: 'plan'}};
export const RepositoryOverview: Story = {args: {view: 'overview'}};
export const UnavailableOverview: Story = {args: {view: 'overview', state: 'unavailable'}};
export const Direction: Story = {args: {view: 'direction'}};
export const MissingDirection: Story = {args: {view: 'direction', state: 'missing'}};
export const AccessStanding: Story = {args: {view: 'access'}};
export const RevisionHistory: Story = {args: {view: 'history'}};
export const UnavailableHistory: Story = {args: {view: 'history', state: 'unavailable'}};
export const UnconnectedIntervention: Story = {args: {view: 'interventions'}};
