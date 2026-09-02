import adapterNode from '@sveltejs/adapter-node';
import adapterStatic from '@sveltejs/adapter-static';
import { vitePreprocess } from '@sveltejs/vite-plugin-svelte';

// VARYAONE_ADAPTER=static builds the desktop single-page bundle (served by the
// Go `varyaone stack` binary, no Node runtime). Anything else keeps the default
// adapter-node server used by the Docker / domain deployment.
const isStatic = process.env.VARYAONE_ADAPTER === 'static';

export default {
  preprocess: vitePreprocess(),
  kit: {
    adapter: isStatic
      ? adapterStatic({
          fallback: 'index.html',
          pages: 'build',
          assets: 'build',
          precompress: false
        })
      : adapterNode()
  }
};
