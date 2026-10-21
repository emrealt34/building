// GitHub Pages serves project sites under /<repo>/, so no link may be written as a
// bare "/path". Every href goes through this helper, which prefixes BASE_URL.
const base = import.meta.env.BASE_URL.replace(/\/+$/, '');

/** Absolute, base-aware path for an internal link. */
export const href = (path: string): string => `${base}${path.startsWith('/') ? path : `/${path}`}`;

/** Base-aware link to a dashboard page for a given deposit amount. */
export const depositHref = (deposit: number): string => href(`/d/${deposit}/`);

/**
 * Venue ids look like "blend-pools-v2:ecf788e3". A colon is a legal URL character but an
 * illegal character in the file path GitHub Pages and its artifact uploader create, so
 * the public route uses a slug and the id is carried along as page props.
 */
export const venueSlug = (id: string): string => id.replace(/[^a-zA-Z0-9._-]+/g, '-');
export const venueHref = (id: string): string => href(`/venues/${venueSlug(id)}/`);
