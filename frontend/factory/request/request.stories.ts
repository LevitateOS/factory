import {html} from 'lit';
import type {Meta, StoryObj} from '@storybook/web-components-vite';
import {requestStoryContext} from '../../storybook/request-fixtures.js';
import type {RequestStoryState} from '../../storybook/request-fixtures.js';
import './request.js';

interface Args {
  state: RequestStoryState;
  active: boolean;
}

const meta = {
  title: 'Factory/Request',
  args: {state: 'entry', active: true},
  argTypes: {
    state: {
      control: 'select',
      options: [
        'entry',
        'review',
        'loading',
        'denied',
        'offline',
        'stale',
        'draft-restored',
        'draft-stale',
        'save-uncertain',
        'generation-uncertain',
        'planning-uncertain',
        'exploration-uncertain',
      ],
    },
  },
  render: ({state, active}) => {
    const context = requestStoryContext(state);
    return html`<soda-intent
      .api=${context.api}
      .actor=${context.actor}
      .draftScope=${context.draftScope}
      .request=${context.request}
      .active=${active}
      .blocked=${context.blocked}
    ></soda-intent>`;
  },
} satisfies Meta<Args>;

export default meta;
type Story = StoryObj<Args>;
export const NewRequest: Story = {};
export const SavedDirection: Story = {args: {state: 'review'}};
export const Loading: Story = {args: {state: 'loading'}};
export const PermissionDenied: Story = {args: {state: 'denied'}};
export const Offline: Story = {args: {state: 'offline'}};
export const StaleSave: Story = {args: {state: 'stale'}};

export const Inactive: Story = {args: {active: false}};

export const RestoredLocalDraft: Story = {args: {state: 'draft-restored'}};
export const OlderLocalDraft: Story = {args: {state: 'draft-stale'}};
export const UnconfirmedSave: Story = {args: {state: 'save-uncertain'}};

export const UnconfirmedWorkCreation: Story = {args: {state: 'generation-uncertain'}};

export const UnconfirmedPlanning: Story = {args: {state: 'planning-uncertain'}};

export const UnconfirmedExploration: Story = {args: {state: 'exploration-uncertain'}};
