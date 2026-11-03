import {LitElement, html} from 'lit';
import type {Meta, StoryObj} from '@storybook/web-components-vite';
import {diffFiles, reviewThreads, type DiffFixture} from '../../storybook/diff-fixtures.js';
import type {ChangedFile} from '../api/collaboration.js';
import type {ReviewDraft} from './diff.js';
import './diff.js';

class DiffStory extends LitElement {
  static properties = {file: {attribute: false}, draft: {state: true}, scheme: {type: String}};
  declare file: ChangedFile;
  declare private draft: ReviewDraft | null;
  declare scheme: 'light' | 'dark';
  constructor() {
    super();
    this.file = diffFiles.renamed;
    this.draft = null;
    this.scheme = 'light';
  }
  protected createRenderRoot() {
    return this;
  }
  protected render() {
    return html`<p>Storybook renderer fixture · No provider writes</p>
      <soda-factory-diff
        .file=${this.file}
        .threads=${reviewThreads}
        .draft=${this.draft}
        .scheme=${this.scheme}
        @factory-review-draft-change=${(event: CustomEvent<ReviewDraft | null>) => {
          this.draft = event.detail;
        }}
      ></soda-factory-diff>`;
  }
}
customElements.define('soda-factory-diff-story', DiffStory);

if (new URLSearchParams(location.search).has('nativeDiffCheck')) {
  void import('../../storybook/diff-native-check.js').then(({runNativeDiffCheck}) => runNativeDiffCheck());
}

const meta = {
  title: 'Factory/Review',
  args: {variant: 'renamed'},
  argTypes: {variant: {control: 'select', options: Object.keys(diffFiles)}},
  render: ({variant}, context) =>
    html`<soda-factory-diff-story
      .file=${diffFiles[variant]}
      .scheme=${context.globals.scheme}
    ></soda-factory-diff-story>`,
} satisfies Meta<{variant: DiffFixture}>;
export default meta;
type Story = StoryObj<{variant: DiffFixture}>;
export const Renamed: Story = {};
export const Long: Story = {args: {variant: 'long'}};
export const Added: Story = {args: {variant: 'added'}};
export const Deleted: Story = {args: {variant: 'deleted'}};
export const Binary: Story = {args: {variant: 'binary'}};
export const Truncated: Story = {args: {variant: 'truncated'}};
export const Unavailable: Story = {args: {variant: 'unavailable'}};
