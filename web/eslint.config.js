import svelte from 'eslint-plugin-svelte';
import globals from 'globals';

// Catches what svelte-check does not in plain-JS components: identifiers that are used but never defined
// (a deleted $derived once crashed the Setup page at runtime).
export default [
  ...svelte.configs['flat/base'],
  {
    files: ['src/**/*.js', 'src/**/*.svelte'],
    languageOptions: { globals: { ...globals.browser } },
    rules: { 'no-undef': 'error', 'no-unused-vars': ['error', { args: 'none', caughtErrors: 'none' }] },
  },
];
