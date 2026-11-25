import {html} from 'lit';
import type {Meta, StoryObj} from '@storybook/web-components-vite';
import {feedPostStoryPeer} from '../../storybook/feed-post-fixtures.js';
import {feedPostSheets} from '../../storybook/feed-post-decision-fixtures.js';
import type {FeedPostSheet} from '../../storybook/feed-post-decision-fixtures.js';
import './feed.js';

interface Args {
  sheet: FeedPostSheet;
}

const meta = {
  title: 'Factory/Feed post types',
  args: {sheet: 'asks'},
  argTypes: {
    sheet: {
      control: 'select',
      options: Object.keys(feedPostSheets),
    },
  },
  render: ({sheet}) => {
    const peer = feedPostStoryPeer(feedPostSheets[sheet]);
    return html`<soda-factory-feed .context=${peer.context}></soda-factory-feed>`;
  },
} satisfies Meta<Args>;

export default meta;
type Story = StoryObj<Args>;
export const Asks: Story = {args: {sheet: 'asks'}};
export const TypedQuestions: Story = {args: {sheet: 'typedQuestions'}};
export const Progress: Story = {args: {sheet: 'progress'}};
export const Problems: Story = {args: {sheet: 'problems'}};
export const Results: Story = {args: {sheet: 'results'}};
export const Decisions: Story = {args: {sheet: 'decisions'}};
export const RunRecords: Story = {args: {sheet: 'runRecords'}};
export const ClosingTheLoop: Story = {args: {sheet: 'closingTheLoop'}};
// The post box opens with its "Read first by" choice; posting sends one
// request carrying read_first_by, and the stored post shows it.
// Agent requests: each card links to its setting and grants nothing.
export const Requests: Story = {args: {sheet: 'requests'}};
export const NamedRoles: Story = {
  args: {sheet: 'namedRoles'},
  render: ({sheet}) => {
    const peer = feedPostStoryPeer(feedPostSheets[sheet]);
    return html`<soda-factory-feed .context=${peer.context} .compose=${true}></soda-factory-feed>`;
  },
};
