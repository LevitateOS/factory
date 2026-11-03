import {html} from 'lit';
import type {Meta, StoryObj} from '@storybook/web-components-vite';
import {libraryStoryPeer, libraryItemID} from '../../storybook/library-fixtures.js';
import type {LibraryStoryState} from '../../storybook/library-fixtures.js';
import type {LibrarySource} from './library.js';
import './library.js';

const meta = {
  title: 'Factory/Knowledge library',
  args: {state: 'recorded'},
  argTypes: {
    state: {
      control: 'select',
      options: ['recorded', 'empty', 'loading', 'offline', 'denied', 'paged', 'detail', 'removed'],
    },
  },
  render: ({state}) => {
    const peer = libraryStoryPeer(state);
    return html`<soda-factory-library
        .context=${peer.context}
        .itemID=${state === 'detail' || state === 'removed' ? libraryItemID : undefined}
        @library-source-open=${(event: CustomEvent<LibrarySource>) => {
          const output = (event.currentTarget as HTMLElement).nextElementSibling;
          if (output) output.textContent = 'Declared host selected source post ' + event.detail.id;
        }}
      ></soda-factory-library>
      <p role="status"></p>`;
  },
} satisfies Meta<{state: LibraryStoryState}>;
export default meta;
type Story = StoryObj<{state: LibraryStoryState}>;
export const Recorded: Story = {};
export const Empty: Story = {args: {state: 'empty'}};
export const Loading: Story = {args: {state: 'loading'}};
export const Offline: Story = {args: {state: 'offline'}};
export const Denied: Story = {args: {state: 'denied'}};
export const Paged: Story = {args: {state: 'paged'}};
export const DecisionHistory: Story = {args: {state: 'detail'}};
export const RemovedHistory: Story = {args: {state: 'removed'}};
