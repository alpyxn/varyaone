declare global {
  namespace App {}

  // Injected by Vite (see vite.config.ts): true for the desktop SPA build.
  const __SPA__: boolean;
}

export {};
