import {html} from 'lit';
import type {Meta, StoryObj} from '@storybook/web-components-vite';
import {journeyStoryContext} from '../../storybook/journey-fixtures.js';
import type {JourneyStoryState} from '../../storybook/journey-fixtures.js';
import './journey.js';

interface Args {
  state: JourneyStoryState;
}

const meta = {
  title: 'Factory/Work journey',
  args: {state: 'recorded'},
  argTypes: {state: {control: 'select', options: ['recorded', 'empty', 'loading', 'unavailable', 'denied']}},
  render: ({state}) =>
    html`<soda-factory-work-journey .context=${journeyStoryContext(state)}></soda-factory-work-journey>`,
} satisfies Meta<Args>;

export default meta;
type Story = StoryObj<Args>;
export const Recorded: Story = {};
export const Empty: Story = {args: {state: 'empty'}};
export const Loading: Story = {args: {state: 'loading'}};
export const Unavailable: Story = {args: {state: 'unavailable'}};
export const PermissionDenied: Story = {args: {state: 'denied'}};
