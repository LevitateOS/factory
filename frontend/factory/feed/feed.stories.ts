import {html} from 'lit';
import type {Meta, StoryObj} from '@storybook/web-components-vite';
import {feedStoryPeer} from '../../storybook/fixtures.js';
import type {FeedStoryState} from '../../storybook/fixtures.js';
import './feed.js';

interface Args {
  state: FeedStoryState;
}

const meta = {
  title: 'Factory/Feed',
  args: {state: 'activity'},
  argTypes: {
    state: {
      control: 'select',
      options: [
        'activity',
        'empty',
        'loading',
        'unavailable',
        'denied',
        'stale',
        'expired',
        'withdrawn',
        'paging',
        'replies',
        'busy',
        'retry',
        'unknown',
        'revision-unknown',
        'review-unknown',
      ],
    },
  },
  render: ({state}) => {
    const peer = feedStoryPeer(state);
    return html`<soda-factory-feed .context=${peer.context}></soda-factory-feed>
      ${state === 'busy' ? html`<button type="button" @click=${() => peer.release()}>Release declared response</button>` : ''}`;
  },
} satisfies Meta<Args>;

export default meta;
type Story = StoryObj<Args>;
export const Activity: Story = {};
export const Empty: Story = {args: {state: 'empty'}};
export const Loading: Story = {args: {state: 'loading'}};
export const Unavailable: Story = {args: {state: 'unavailable'}};
export const PermissionDenied: Story = {args: {state: 'denied'}};
export const StaleSubmission: Story = {
  args: {state: 'stale'},
  parameters: {
    docs: {
      description: {
        story:
          'Edit the recorded post and save. The first save encounters a newer source revision; review its current text before explicitly saving again.',
      },
    },
  },
};
export const ExpiredSession: Story = {args: {state: 'expired'}};
export const AccessWithdrawn: Story = {args: {state: 'withdrawn'}};
export const Paging: Story = {args: {state: 'paging'}};
export const PopulatedReplies: Story = {args: {state: 'replies'}};
export const BusySubmission: Story = {args: {state: 'busy'}};
export const RetryableSubmission: Story = {args: {state: 'retry'}};
export const UnknownSubmission: Story = {
  args: {state: 'unknown'},
  parameters: {
    docs: {
      description: {
        story:
          'Submit once to lose the declared reply. Explicit retry keeps the exact submitted command and reads back one declared post.',
      },
    },
  },
};
export const UnknownRevision: Story = {
  args: {state: 'revision-unknown'},
  parameters: {
    docs: {
      description: {
        story:
          'Edit the recorded post and save to lose its declared receipt. Check saved edit reads the exact result without submitting again; newer writing is kept.',
      },
    },
  },
};
export const UnknownChangeReview: Story = {
  args: {state: 'review-unknown'},
  parameters: {
    docs: {
      description: {
        story:
          'Open Review proposed change to lose the declared draft reply. Check saved change review recovers the exact source-bound draft without another write or automatic acceptance.',
      },
    },
  },
};
