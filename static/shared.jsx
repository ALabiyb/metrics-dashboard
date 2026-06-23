// shared.jsx — palette, formatting helpers, and the live-snapshot hook.
// Exports to window: useLiveSnapshot, fmt, thr, COL.
// The values/structure here mirror the original design prototype's
// cluster-data.jsx — only the data *source* changed: instead of simulating
// the cluster in the browser, we poll the Go backend's /api/snapshot, which
// runs the same random-walk simulation server-side (see simulator.go).
//
// Author: Labiyb M. Said — DevSecOps Engineer
// Contact: abdulmunimsaid82@gmail.com

// API_BASE: '/api' when the dashboard is loaded under the normal authenticated
// path, '/tv' when loaded under /tv/ for the kiosk display (public read-only,
// no login). The TV path serves the same SPA and the JS detects the URL it was
// loaded under, so the same bundle works for both modes.
const API_BASE = window.location.pathname.startsWith('/tv') ? '/tv' : '/api';
const TV_MODE  = API_BASE === '/tv';

const COL = {
  bg: '#0a0e14',
  panel: '#10151e',
  panel2: '#0d121a',
  line: 'rgba(255,255,255,0.07)',
  line2: 'rgba(255,255,255,0.04)',
  text: '#e6edf3',
  mut: '#7d8a9c',
  mut2: '#576373',
  accent: '#22d3ee',   // cyan
  accentDim: '#0e7490',
  ok: '#34d399',
  warn: '#fbbf24',
  crit: '#fb7185',
  info: '#60a5fa',
  violet: '#a78bfa',
};

// threshold color for a 0-100 utilization metric
function thr(v) {
  if (v >= 90) return COL.crit;
  if (v >= 75) return COL.warn;
  return COL.accent;
}

// number formatting helpers
const fmt = {
  pct: (v) => v.toFixed(0),
  pct1: (v) => v.toFixed(1),
  k: (v) => (v >= 1000 ? (v / 1000).toFixed(1) + 'k' : Math.round(v).toString()),
  mbps: (v) => (v >= 1000 ? (v / 1000).toFixed(2) + ' Gb/s' : v.toFixed(0) + ' Mb/s'),
  mbs: (v) => (v >= 1000 ? (v / 1000).toFixed(2) + ' GB/s' : v.toFixed(0) + ' MB/s'),
  tb: (v) => v.toFixed(1),
  iops: (v) => (v >= 1000 ? (v / 1000).toFixed(1) + 'k' : v.toFixed(0)),
  time: (d) => d.toTimeString().slice(0, 8),
  ago: (d) => {
    const s = Math.floor((Date.now() - d) / 1000);
    if (s < 60) return s + 's';
    if (s < 3600) return Math.floor(s / 60) + 'm';
    return Math.floor(s / 3600) + 'h';
  },
};

// ---- fetchJSON --------------------------------------------------------------
// Thin wrapper used by every authenticated poll below. A 401 means the
// session cookie is missing/expired — bounce to /login immediately rather
// than spinning on retries. A 403 means "logged in, wrong role", which
// callers handle themselves (e.g. useAdminStatus just stops polling).
async function fetchJSON(url) {
  const res = await fetch(url, { credentials: 'same-origin' });
  if (res.status === 401) {
    // In TV kiosk mode there's no login to bounce to — just throw and the
    // caller will keep retrying. Normal mode redirects to /login.
    if (!TV_MODE) window.location.href = '/login';
    throw new Error('unauthenticated');
  }
  if (!res.ok) throw new Error(url + ' → ' + res.status);
  return res.json();
}

// ---- useLiveSnapshot --------------------------------------------------------
// Polls the Go backend every 2s (matching the server's simulation tick) and
// hands back the same shape the prototype's useClusterData produced:
// { nodes, ceph, events, cluster }. Returns null until the first response
// lands, and parses event timestamps (JSON strings) back into Date objects.
function useLiveSnapshot(pollMs = 2000) {
  const [snapshot, setSnapshot] = React.useState(null);

  React.useEffect(() => {
    let alive = true;

    async function poll() {
      try {
        const data = await fetchJSON(API_BASE + '/snapshot');
        if (!alive) return;
        setSnapshot({
          ...data,
          events: data.events.map((e) => ({ ...e, t: new Date(e.t) })),
        });
      } catch (err) {
        // keep showing the last good snapshot; the next tick will retry
        console.error(err);
      }
    }

    poll();
    const iv = setInterval(poll, pollMs);
    return () => { alive = false; clearInterval(iv); };
  }, [pollMs]);

  return snapshot;
}

// ---- useMe ------------------------------------------------------------------
// Who's logged in, and what role they hold. The session cookie is HttpOnly
// (so JS can't read it directly) — this is how the frontend learns enough to
// decide what to render, e.g. whether to show admin-only panels.
function useMe() {
  const [me, setMe] = React.useState(null);
  React.useEffect(() => {
    let alive = true;
    fetchJSON(API_BASE + '/me').then((d) => { if (alive) setMe(d); }).catch((err) => console.error(err));
    return () => { alive = false; };
  }, []);
  return me;
}

// ---- useAdminStatus ---------------------------------------------------------
// Polls the admin-only /api/admin/status endpoint. Authorization in action:
// viewers never call this (enabled=false), and if a non-admin session somehow
// did, the 403 just stops the polling — no retry storm, no error surfaced.
function useAdminStatus(enabled, pollMs = 5000) {
  const [status, setStatus] = React.useState(null);
  React.useEffect(() => {
    if (!enabled) { setStatus(null); return; }
    let alive = true;
    let iv;
    async function poll() {
      try {
        const data = await fetchJSON('/api/admin/status');
        if (alive) setStatus(data);
      } catch (err) {
        clearInterval(iv);
      }
    }
    poll();
    iv = setInterval(poll, pollMs);
    return () => { alive = false; clearInterval(iv); };
  }, [enabled, pollMs]);
  return status;
}

Object.assign(window, { useLiveSnapshot, useMe, useAdminStatus, fmt, thr, COL });
