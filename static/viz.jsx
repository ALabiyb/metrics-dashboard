// viz.jsx — reusable NOC visualization primitives.
// Exports to window: RingGauge, Bar, Donut, Sparkline, StatusDot, HealthBadge, Panel, Delta.
// Relies on globals: COL, thr, fmt.
//
// Author: Labiyb M. Said — DevSecOps Engineer
// Contact: abdulmunimsaid82@gmail.com

// ---- Panel: titled dark container -----------------------------------------
function Panel({ title, right, children, style, bodyStyle, pad = 16 }) {
  return (
    <div style={{
      background: COL.panel, border: '1px solid ' + COL.line, borderRadius: 10,
      display: 'flex', flexDirection: 'column', minHeight: 0, ...style,
    }}>
      {title != null && (
        <div style={{
          display: 'flex', alignItems: 'center', justifyContent: 'space-between',
          padding: '11px 14px', borderBottom: '1px solid ' + COL.line2, flex: '0 0 auto',
        }}>
          <span style={{
            font: '600 11px/1 "IBM Plex Sans", sans-serif', letterSpacing: '0.13em',
            textTransform: 'uppercase', color: COL.mut,
          }}>{title}</span>
          {right}
        </div>
      )}
      <div style={{ padding: pad, flex: '1 1 auto', minHeight: 0, ...bodyStyle }}>{children}</div>
    </div>
  );
}

// ---- RingGauge: 270° arc gauge with big centered value --------------------
function RingGauge({ value, size = 132, stroke = 11, color, label, sub, unit = '%' }) {
  const r = (size - stroke) / 2;
  const c = 2 * Math.PI * r;
  const sweep = 0.75;            // 270°
  const arc = c * sweep;
  const off = arc * (1 - Math.max(0, Math.min(100, value)) / 100);
  const cc = color || thr(value);
  return (
    <div style={{ position: 'relative', width: size, height: size }}>
      <svg width={size} height={size} style={{ transform: 'rotate(135deg)' }}>
        <circle cx={size / 2} cy={size / 2} r={r} fill="none" stroke="rgba(255,255,255,0.06)"
          strokeWidth={stroke} strokeDasharray={`${arc} ${c}`} strokeLinecap="round" />
        <circle cx={size / 2} cy={size / 2} r={r} fill="none" stroke={cc}
          strokeWidth={stroke} strokeDasharray={`${arc} ${c}`} strokeDashoffset={off}
          strokeLinecap="round"
          style={{ transition: 'stroke-dashoffset .9s cubic-bezier(.4,0,.2,1), stroke .4s', filter: `drop-shadow(0 0 5px ${cc}66)` }} />
      </svg>
      <div style={{
        position: 'absolute', inset: 0, display: 'flex', flexDirection: 'column',
        alignItems: 'center', justifyContent: 'center', gap: 1,
      }}>
        <div style={{ font: '600 ' + (size / 4.4) + 'px/1 "IBM Plex Mono", monospace', color: COL.text, letterSpacing: '-0.02em' }}>
          {fmt.pct(value)}<span style={{ fontSize: size / 11, color: COL.mut, marginLeft: 1 }}>{unit}</span>
        </div>
        {label && <div style={{ font: '600 ' + (size / 13.5) + 'px/1 "IBM Plex Sans",sans-serif', letterSpacing: '0.12em', textTransform: 'uppercase', color: COL.mut, marginTop: 3 }}>{label}</div>}
        {sub && <div style={{ font: '500 ' + (size / 14) + 'px/1 "IBM Plex Mono",monospace', color: COL.mut2 }}>{sub}</div>}
      </div>
    </div>
  );
}

// ---- Bar: horizontal utilization bar --------------------------------------
function Bar({ value, color, height = 6, track = 'rgba(255,255,255,0.07)', radius = 3 }) {
  const cc = color || thr(value);
  return (
    <div style={{ width: '100%', height, background: track, borderRadius: radius, overflow: 'hidden' }}>
      <div style={{
        width: Math.max(0, Math.min(100, value)) + '%', height: '100%', background: cc,
        borderRadius: radius, transition: 'width .9s cubic-bezier(.4,0,.2,1), background .4s',
        boxShadow: `0 0 6px ${cc}55`,
      }} />
    </div>
  );
}

// ---- Donut: multi-segment ring (for OSD status etc.) ----------------------
function Donut({ segments, size = 132, stroke = 14, centerTop, centerSub }) {
  const r = (size - stroke) / 2;
  const c = 2 * Math.PI * r;
  const total = segments.reduce((s, x) => s + x.value, 0) || 1;
  let acc = 0;
  return (
    <div style={{ position: 'relative', width: size, height: size }}>
      <svg width={size} height={size} style={{ transform: 'rotate(-90deg)' }}>
        <circle cx={size / 2} cy={size / 2} r={r} fill="none" stroke="rgba(255,255,255,0.05)" strokeWidth={stroke} />
        {segments.map((s, i) => {
          const len = (s.value / total) * c;
          const el = (
            <circle key={i} cx={size / 2} cy={size / 2} r={r} fill="none" stroke={s.color}
              strokeWidth={stroke} strokeDasharray={`${Math.max(0, len - 1.5)} ${c}`}
              strokeDashoffset={-acc} strokeLinecap="butt"
              style={{ transition: 'stroke-dasharray .9s,stroke-dashoffset .9s' }} />
          );
          acc += len;
          return el;
        })}
      </svg>
      <div style={{ position: 'absolute', inset: 0, display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center' }}>
        <div style={{ font: '600 ' + (size / 4.6) + 'px/1 "IBM Plex Mono",monospace', color: COL.text }}>{centerTop}</div>
        {centerSub && <div style={{ font: '600 ' + (size / 14) + 'px/1 "IBM Plex Sans",sans-serif', letterSpacing: '0.12em', textTransform: 'uppercase', color: COL.mut, marginTop: 3 }}>{centerSub}</div>}
      </div>
    </div>
  );
}

// ---- Sparkline ------------------------------------------------------------
function Sparkline({ data, w = 120, h = 34, color, fill = true, strokeW = 1.6, max = 100, min = 0 }) {
  if (!data || !data.length) return null;
  const cc = color || COL.accent;
  const range = max - min || 1;
  const step = w / (data.length - 1);
  const pts = data.map((v, i) => [i * step, h - ((Math.max(min, Math.min(max, v)) - min) / range) * h]);
  const line = pts.map((p, i) => (i ? 'L' : 'M') + p[0].toFixed(1) + ' ' + p[1].toFixed(1)).join(' ');
  const area = line + ` L${w} ${h} L0 ${h} Z`;
  const gid = 'sg' + Math.round(min * 7 + max * 13 + w);
  return (
    <svg width={w} height={h} style={{ display: 'block', overflow: 'visible' }}>
      {fill && (
        <defs>
          <linearGradient id={gid} x1="0" y1="0" x2="0" y2="1">
            <stop offset="0" stopColor={cc} stopOpacity="0.28" />
            <stop offset="1" stopColor={cc} stopOpacity="0" />
          </linearGradient>
        </defs>
      )}
      {fill && <path d={area} fill={`url(#${gid})`} />}
      <path d={line} fill="none" stroke={cc} strokeWidth={strokeW} strokeLinejoin="round" strokeLinecap="round" />
    </svg>
  );
}

// ---- StatusDot ------------------------------------------------------------
function StatusDot({ color, size = 8, pulse = false, title }) {
  return (
    <span title={title} style={{ position: 'relative', display: 'inline-flex', width: size, height: size, flex: '0 0 auto' }}>
      <span style={{ width: size, height: size, borderRadius: '50%', background: color, boxShadow: `0 0 6px ${color}` }} />
      {pulse && <span style={{
        position: 'absolute', inset: 0, borderRadius: '50%', background: color,
        animation: 'noc-pulse 1.8s ease-out infinite',
      }} />}
    </span>
  );
}

// ---- HealthBadge ----------------------------------------------------------
function HealthBadge({ state, big = false }) {
  const map = {
    HEALTH_OK: [COL.ok, 'HEALTH_OK'], Ready: [COL.ok, 'READY'],
    HEALTH_WARN: [COL.warn, 'HEALTH_WARN'], SchedulingDisabled: [COL.warn, 'CORDONED'],
    HEALTH_ERR: [COL.crit, 'HEALTH_ERR'], NotReady: [COL.crit, 'NOT READY'],
  };
  const [cc, txt] = map[state] || [COL.mut, state];
  return (
    <span style={{
      display: 'inline-flex', alignItems: 'center', gap: big ? 8 : 6,
      padding: big ? '6px 12px' : '3px 8px', borderRadius: 6,
      background: cc + '1a', border: '1px solid ' + cc + '40',
      font: `600 ${big ? 13 : 10.5}px/1 "IBM Plex Mono",monospace`, letterSpacing: '0.06em', color: cc,
    }}>
      <StatusDot color={cc} size={big ? 8 : 6} pulse={cc === COL.crit} />{txt}
    </span>
  );
}

// ---- Delta arrow ----------------------------------------------------------
function Delta({ value, unit = '%', good = 'down' }) {
  const up = value >= 0;
  const isGood = (good === 'down') ? !up : up;
  const cc = isGood ? COL.ok : COL.crit;
  return (
    <span style={{ font: '600 11px/1 "IBM Plex Mono",monospace', color: cc, display: 'inline-flex', alignItems: 'center', gap: 2 }}>
      {up ? '▲' : '▼'}{Math.abs(value).toFixed(1)}{unit}
    </span>
  );
}

Object.assign(window, { RingGauge, Bar, Donut, Sparkline, StatusDot, HealthBadge, Panel, Delta });
