import type {StorybookConfig} from '@storybook/web-components-vite';

const config: StorybookConfig = {
  framework: '@storybook/web-components-vite',
  stories: ['../factory/**/*.stories.ts'],
  staticDirs: [{from: '../../assets/branding/role-portraits', to: '/role-portraits'}],
  core: {disableTelemetry: true},
  // Scheme selection belongs to the host envelope. Lowering light-dark() to
  // inherited guard variables loses root token values in native WebKit.
  viteFinal: async (config) => ({
    ...config,
    build: {...config.build, cssTarget: ['chrome123', 'firefox120', 'safari17.5']},
  }),
};

export default config;
