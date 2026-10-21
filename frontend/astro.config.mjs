// Astro config for a fully static build that works on GitHub Pages.
//
// Why static instead of SSR: the site must be deployable to GitHub Pages, which serves
// files only. Data therefore comes from either a live Go backend at build time
// (API_URL=...) or the frozen snapshot produced by `go run ./cmd/snapshot`.
// Everything is prerendered; there is no server to pay for and nothing to keep up.
import { defineConfig } from 'astro/config';

// BASE_PATH is set to "/<repo>" for project pages (e.g. /building/) and "/" for a
// user site or local preview. Trailing slash keeps links working on Pages.
const base = process.env.BASE_PATH ?? '/';

export default defineConfig({
  site: process.env.SITE_URL ?? 'https://emrealt34.github.io',
  base,
  output: 'static',
  trailingSlash: 'always',
  compressHTML: true,
  devToolbar: { enabled: false },
  server: { host: true, port: Number(process.env.PORT ?? 4321) },
});
