import tailwindcss from '@tailwindcss/vite';
import { sveltekit } from '@sveltejs/kit/vite';
import { defineConfig } from 'vitest/config';

export default defineConfig({
  plugins: [tailwindcss(), sveltekit()],
  define: {
    // True only for the desktop single-page build; see svelte.config.js.
    __SPA__: JSON.stringify(process.env.VARYAONE_ADAPTER === 'static')
  },
  ssr: { noExternal: ['@lucide/svelte'] },
  test: {
    environment: 'jsdom',
    include: ['src/**/*.test.ts']
  }
});
