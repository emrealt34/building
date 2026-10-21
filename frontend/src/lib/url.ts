// GitHub Pages serves project sites under /<repo>/, so no link may be written as a
// bare "/path". Every href goes through this helper, which prefixes BASE_URL.
const base = import.meta.env.BASE_URL.replace(/\/+$/, '');

/** Absolute, base-aware path for an internal link. */
export const href = (path: string): string => `${base}${path.startsWith('/') ? path : `/${path}`}`;

/** Base-aware link to a dashboard page for a given deposit amount. */
export const depositHref = (deposit: number): string => href(`/d/${deposit}/`);
