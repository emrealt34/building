// Data access for the site.
//
// Two sources, one shape:
//   • live      — a running Go backend (API_URL), used in local dev and in CI builds
//                 when the network cooperates;
//   • snapshot  — frontend/src/data/snapshot.json, produced by `go run ./cmd/snapshot`,
//                 which calls the same handlers. This is what GitHub Pages renders.
//
// The browser never calls the API: every page is rendered at build time, so there is
// no token, no CORS surface and no key anywhere near the client.
import type {
  Plan, Venue, Decision, NetworkStatus, PolicyDoc, AlphaReport, Meta, HistoryPoint, CarryReport, CherryReport,
} from './types';
import snapshotJson from '../data/snapshot.json';

const LIVE = (import.meta.env.API_URL ?? process.env.API_URL ?? '').replace(/\/+$/, '');

interface Snapshot {
  generated_at: string;
  note: string;
  deposits: number[];
  responses: Record<string, unknown>;
}

const snap = snapshotJson as unknown as Snapshot;

async function get<T>(path: string): Promise<T | null> {
  if (LIVE) {
    try {
      const res = await fetch(`${LIVE}/api/v1${path}`, { headers: { accept: 'application/json' } });
      if (res.ok) return (await res.json()) as T;
    } catch {
      // fall through to the snapshot: a build must not fail because a provider blipped
    }
  }
  // Snapshot keys are the real request paths, e.g. "/api/v1/plan?deposit=1000".
  const raw = snap.responses[`/api/v1${path}`];
  return raw === undefined || raw === null ? null : (raw as T);
}

export const api = {
  meta: () => get<Meta>('/meta'),
  health: () => get<{ status: string; venues_cached: number; venues_updated: string; version: string }>('/health'),
  network: () => get<NetworkStatus>('/network'),
  policy: () => get<PolicyDoc>('/policy'),
  venues: (deposit: number) => get<{ as_of: string; count: number; venues: Venue[] }>(`/venues?deposit=${deposit}`),
  venue: (id: string, deposit: number) => get<{ as_of: string; venue: Venue }>(`/venues/${id}?deposit=${deposit}`),
  history: (id: string, days = 90) => get<{ venue_id: string; days: number; points: HistoryPoint[] }>(`/venues/${id}/history?days=${days}`),
  plan: (deposit: number) => get<Plan>(`/plan?deposit=${deposit}`),
  decisions: () => get<{ note: string; decisions: Decision[] }>('/decisions'),
  alpha: (days = 90) => get<AlphaReport>(`/alpha?days=${days}`),
  carry: () => get<CarryReport>('/carry'),
  cherry: () => get<CherryReport>('/cherry'),
};

/** Deposit presets the static site builds one page per entry. */
export const DEPOSITS: number[] = snap.deposits?.length ? snap.deposits : [100, 1000, 10000, 50000];

/** Where the numbers on the page came from, so the UI can say it out loud. */
export const dataSource = () =>
  LIVE
    ? { mode: 'live' as const, label: 'live Go API', at: null as string | null }
    : { mode: 'snapshot' as const, label: 'snapshot', at: snap.generated_at };
