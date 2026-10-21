export const usd = (n: number, digits = 0): string =>
  new Intl.NumberFormat('en-US', {
    style: 'currency',
    currency: 'USD',
    maximumFractionDigits: digits,
    minimumFractionDigits: digits,
  }).format(Number.isFinite(n) ? n : 0);

export const compactUSD = (n: number): string => {
  if (!Number.isFinite(n)) return '—';
  if (n >= 1_000_000) return `$${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1_000) return `$${(n / 1_000).toFixed(0)}K`;
  return `$${n.toFixed(0)}`;
};

export const pct = (n: number, digits = 2): string => `${(Number.isFinite(n) ? n : 0).toFixed(digits)}%`;

export const bps = (n: number): string => `${n >= 0 ? '+' : ''}${Math.round(n)} bps`;

export const timeAgo = (iso: string): string => {
  if (!iso) return '—';
  const then = new Date(iso).getTime();
  if (Number.isNaN(then)) return '—';
  const secs = Math.max(0, Math.round((Date.now() - then) / 1000));
  if (secs < 60) return `${secs}s ago`;
  if (secs < 3600) return `${Math.round(secs / 60)}m ago`;
  if (secs < 86400) return `${Math.round(secs / 3600)}h ago`;
  return `${Math.round(secs / 86400)}d ago`;
};

export const bandLabel: Record<string, string> = {
  A: 'Solid',
  B: 'Fair',
  C: 'Weak',
  D: 'Poor',
};

export const bandColor: Record<string, string> = {
  A: '#3E6B4F',
  B: '#C98A1B',
  C: '#C7481D',
  D: '#8F3112',
};

export const subLabel: Record<string, string> = {
  contract: 'Has it been audited?',
  liquidity: 'Can you get your money out?',
  oracle: 'Could it break?',
  durability: 'Will the rate last?',
  contagion: 'Is it tied to something risky?',
  governance: 'Who can change the rules?',
};

// Inline SVG sparkline: no chart library, no external requests, renders inside the
// sandboxed preview iframe.
export function sparkline(points: { date: string; apy: number }[], width = 640, height = 150): string {
  if (!points.length) return '';
  const values = points.map((p) => p.apy);
  const min = Math.min(...values);
  const max = Math.max(...values);
  const span = Math.max(0.1, max - min);
  const pad = 8;
  const step = (width - pad * 2) / Math.max(1, points.length - 1);
  const xy = points.map((p, i) => {
    const x = pad + i * step;
    const y = height - pad - ((p.apy - min) / span) * (height - pad * 2);
    return `${x.toFixed(1)},${y.toFixed(1)}`;
  });
  const area = `M${pad},${height - pad} L${xy.join(' L')} L${width - pad},${height - pad} Z`;
  return `<svg viewBox="0 0 ${width} ${height}" width="100%" height="${height}" preserveAspectRatio="none" role="img">
    <path d="${area}" fill="rgba(199,72,29,0.10)" />
    <polyline points="${xy.join(' ')}" fill="none" stroke="#C7481D" stroke-width="2" stroke-linejoin="round" />
  </svg>`;
}
