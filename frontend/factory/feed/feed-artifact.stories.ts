import {html} from 'lit';
import type {Meta, StoryObj} from '@storybook/web-components-vite';
import {artifactStoryPeer} from '../../storybook/artifact-fixtures.js';
import type {ArtifactStoryState} from '../../storybook/artifact-fixtures.js';
import './feed-artifact.js';

const meta = {
  title: 'Factory/Artifact preview',
  args: {state: 'text'},
  argTypes: {
    state: {
      control: 'select',
      options: ['text', 'image', 'binary', 'loading', 'offline', 'denied', 'too-large', 'truncated'],
    },
  },
  render: ({state}) => {
    const peer = artifactStoryPeer(state);
    return html`<soda-feed-artifact .artifact=${peer.artifact} .context=${peer.context}></soda-feed-artifact>`;
  },
} satisfies Meta<{state: ArtifactStoryState}>;
export default meta;
type Story = StoryObj<{state: ArtifactStoryState}>;
export const Text: Story = {};
export const Image: Story = {args: {state: 'image'}};
export const BinaryDownload: Story = {args: {state: 'binary'}};
export const Loading: Story = {args: {state: 'loading'}};
export const Offline: Story = {args: {state: 'offline'}};
export const Denied: Story = {args: {state: 'denied'}};
export const SizeLimit: Story = {args: {state: 'too-large'}};
export const TruncatedText: Story = {args: {state: 'truncated'}};
