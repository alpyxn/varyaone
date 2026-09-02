// The desktop bundle (VARYAONE_ADAPTER=static) is a pure SPA: no SvelteKit
// server, so rendering and routing happen entirely in the browser and every
// `/api/v1/*` call hits the Go server on the same origin. The Docker build keeps
// SSR on (__SPA__ is false there).
export const ssr = !__SPA__;
export const prerender = false;
