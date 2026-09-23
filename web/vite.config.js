import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';

// The Go binary embeds internal/webui/dist (go:embed), so the build lands there.
export default defineConfig({
  plugins: [svelte()],
  build: { outDir: '../internal/webui/dist', emptyOutDir: true, sourcemap: false, target: 'es2022' },
  server: { proxy: { '/api': 'http://127.0.0.1:8737' } },
});
