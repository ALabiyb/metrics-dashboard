// dashboard.jsx — "Mission Control" dashboard (the winning direction from the
// design canvas): 6 KPI tiles, cluster-utilization ring gauges, a 5x2 grid of
// per-node cards, and a right rail with the Ceph panel + alerts/events.
// Ported from the prototype's direction-a.jsx — the only structural change is
// that data comes from useLiveSnapshot() (polling the Go backend's
// /api/snapshot) instead of an in-browser simulation.
// Globals: React, useLiveSnapshot, fmt, thr, COL, RingGauge, Bar, Donut, Sparkline, StatusDot, HealthBadge, Panel.
//
// Author: Labiyb M. Said — DevSecOps Engineer
// Contact: abdulmunimsaid82@gmail.com

// useClock ticks once per second, driving the live clock shown in the header.
function useClock() {
  const [t, setT] = React.useState(new Date());
  React.useEffect(() => { const i = setInterval(() => setT(new Date()), 1000); return () => clearInterval(i); }, []);
  return t;
}

// --- KPI tile --------------------------------------------------------------
// Kpi renders one header metric tile: a big value + unit, an optional
// sparkline, and a small "sub" caption underneath.
function Kpi({ label, value, unit, sub, spark, sparkColor, accent }) {
  return (
    <div style={{
      background: COL.panel, border: '1px solid ' + COL.line, borderRadius: 10,
      padding: '13px 16px', display: 'flex', flexDirection: 'column', justifyContent: 'space-between', minWidth: 0,
    }}>
      <div style={{ font: '600 10.5px/1 "IBM Plex Sans",sans-serif', letterSpacing: '0.13em', textTransform: 'uppercase', color: COL.mut, display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <span>{label}</span>
      </div>
      <div style={{ display: 'flex', alignItems: 'flex-end', justifyContent: 'space-between', gap: 8, marginTop: 10 }}>
        <div style={{ display: 'flex', alignItems: 'baseline', gap: 4, minWidth: 0 }}>
          <span style={{ font: '600 30px/0.9 "IBM Plex Mono",monospace', color: accent || COL.text, letterSpacing: '-0.02em' }}>{value}</span>
          {unit && <span style={{ font: '500 13px/1 "IBM Plex Mono",monospace', color: COL.mut }}>{unit}</span>}
        </div>
        {spark && <Sparkline data={spark} w={72} h={26} color={sparkColor || COL.accent} max={Math.max(...spark) * 1.1} min={0} />}
      </div>
      {sub && <div style={{ font: '500 11px/1 "IBM Plex Mono",monospace', color: COL.mut2, marginTop: 7 }}>{sub}</div>}
    </div>
  );
}

// --- Node card -------------------------------------------------------------
// NodeCard summarizes one cluster node: status dot + health badge, role tag,
// CPU/MEM ring gauges, a pods-used bar, and a network/disk throughput line.
function NodeCard({ n }) {
  const sc = n.status === 'Ready' ? COL.ok : n.status === 'SchedulingDisabled' ? COL.warn : COL.crit;
  const hot = n.cpu >= 90 || n.mem >= 90;
  return (
    <div style={{
      background: COL.panel, border: '1px solid ' + (hot ? COL.crit + '55' : COL.line), borderRadius: 10,
      padding: 14, display: 'flex', flexDirection: 'column', gap: 11, minWidth: 0,
      boxShadow: hot ? `0 0 0 1px ${COL.crit}22, 0 0 18px ${COL.crit}14` : 'none',
    }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
        <StatusDot color={sc} size={8} pulse={sc === COL.crit} title={n.status} />
        <span style={{ font: '600 14px/1 "IBM Plex Mono",monospace', color: COL.text, flex: '1 1 auto', minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{n.name}</span>
        {n.status !== 'Ready' && <HealthBadge state={n.status} />}
        <span style={{ marginLeft: 'auto', flex: '0 0 auto', font: '600 9px/1 "IBM Plex Sans",sans-serif', letterSpacing: '0.1em', textTransform: 'uppercase', color: n.role === 'control-plane' ? COL.violet : COL.mut2, padding: '3px 6px', borderRadius: 4, background: (n.role === 'control-plane' ? COL.violet : COL.mut2) + '18' }}>
          {n.role === 'control-plane' ? 'CTRL' : 'WORK'}
        </span>
      </div>
      <div style={{ display: 'flex', gap: 8, justifyContent: 'space-around' }}>
        <RingGauge value={n.cpu} size={84} stroke={8} label="CPU" />
        <RingGauge value={n.mem} size={84} stroke={8} label="MEM" />
      </div>
      <div>
        <div style={{ display: 'flex', justifyContent: 'space-between', font: '500 10px/1 "IBM Plex Sans",sans-serif', letterSpacing: '0.08em', textTransform: 'uppercase', color: COL.mut, marginBottom: 5 }}>
          <span>Pods</span><span style={{ fontFamily: '"IBM Plex Mono",monospace', color: COL.text, letterSpacing: 0 }}>{n.pods}<span style={{ color: COL.mut2 }}>/{n.podsCap}</span></span>
        </div>
        <Bar value={(n.pods / n.podsCap) * 100} color={COL.info} height={5} />
      </div>
      <div style={{ display: 'flex', justifyContent: 'space-between', font: '500 11px/1 "IBM Plex Mono",monospace', color: COL.mut2, borderTop: '1px solid ' + COL.line2, paddingTop: 9 }}>
        <span title="network in/out">NET <span style={{ color: COL.accent }}>↓{fmt.k(n.netIn)}</span> <span style={{ color: COL.violet }}>↑{fmt.k(n.netOut)}</span></span>
        <span title="disk read/write">DSK {fmt.k(n.diskR + n.diskW)}<span style={{ color: COL.mut2 }}>MB/s</span></span>
      </div>
    </div>
  );
}

// CephRail summarizes Ceph storage health: an OSD up/down donut, a capacity
// ring gauge, per-state OSD counts, and read/write IOPS + throughput tiles.
function CephRail({ ceph }) {
  const usedPct = (ceph.usedTiB / ceph.rawTiB) * 100;
  return (
    <Panel title="Ceph Storage" right={<HealthBadge state={ceph.health} />} bodyStyle={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-around', gap: 12 }}>
        <Donut size={108} stroke={12}
          segments={[
            { value: ceph.osdUp, color: COL.ok },
            { value: ceph.osdTotal - ceph.osdUp, color: COL.crit },
          ]}
          centerTop={ceph.osdUp + '/' + ceph.osdTotal} centerSub="OSD UP" />
        <RingGauge value={usedPct} size={108} stroke={12} color={thr(usedPct)}
          label="Capacity" sub={fmt.tb(ceph.usedTiB) + ' / ' + fmt.tb(ceph.rawTiB) + ' TiB'} />
      </div>
      <div>
        <div style={{ display: 'flex', justifyContent: 'space-between', gap: 8, marginBottom: 10 }}>
          {[['UP', ceph.osdUp, COL.ok], ['IN', ceph.osdIn, COL.accent], ['DOWN', ceph.osdDown, COL.crit]].map(([k, v, c]) => (
            <div key={k} style={{ display: 'flex', alignItems: 'center', gap: 6, font: '500 12px/1 "IBM Plex Sans",sans-serif' }}>
              <StatusDot color={c} size={7} />
              <span style={{ color: COL.mut, letterSpacing: '0.08em' }}>{k}</span>
              <span style={{ font: '600 14px/1 "IBM Plex Mono",monospace', color: COL.text }}>{v}</span>
            </div>
          ))}
        </div>
        <div style={{ font: '500 11px/1 "IBM Plex Mono",monospace', color: COL.mut2 }}>{usedPct.toFixed(1)}% used · {ceph.healthDetail}</div>
      </div>
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 10 }}>
        {[['READ IOPS', fmt.iops(ceph.readIOPS), COL.accent], ['WRITE IOPS', fmt.iops(ceph.writeIOPS), COL.violet],
          ['READ', fmt.mbs(ceph.readMBs), COL.accent], ['WRITE', fmt.mbs(ceph.writeMBs), COL.violet]].map(([k, v, c]) => (
          <div key={k} style={{ background: COL.panel2, border: '1px solid ' + COL.line2, borderRadius: 8, padding: '10px 12px' }}>
            <div style={{ font: '600 9.5px/1 "IBM Plex Sans",sans-serif', letterSpacing: '0.1em', textTransform: 'uppercase', color: COL.mut, marginBottom: 7 }}>{k}</div>
            <div style={{ font: '600 18px/1 "IBM Plex Mono",monospace', color: c }}>{v}</div>
          </div>
        ))}
      </div>
    </Panel>
  );
}

// EventsPanel highlights the most urgent firing alerts (crit/warn) at the
// top, followed by a scrolling log of the most recent events overall.
function EventsPanel({ events }) {
  const sevColor = { crit: COL.crit, warn: COL.warn, info: COL.info, ok: COL.ok };
  const active = events.filter((e) => e.sev === 'crit' || e.sev === 'warn').slice(0, 3);
  return (
    <Panel title="Alerts & Events" right={<span style={{ font: '600 10px/1 "IBM Plex Mono",monospace', color: COL.crit }}>{events.filter(e => e.sev === 'crit').length} CRIT · {events.filter(e => e.sev === 'warn').length} WARN</span>}
      bodyStyle={{ display: 'flex', flexDirection: 'column', gap: 10, overflow: 'hidden' }}>
      {active.map((e) => (
        <div key={e.id} style={{ display: 'flex', gap: 10, alignItems: 'center', minWidth: 0, background: sevColor[e.sev] + '14', border: '1px solid ' + sevColor[e.sev] + '33', borderRadius: 7, padding: '8px 11px' }}>
          <StatusDot color={sevColor[e.sev]} size={7} pulse={e.sev === 'crit'} />
          <span style={{ font: '500 12px/1.3 "IBM Plex Sans",sans-serif', color: COL.text, flex: 1, minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{e.msg}</span>
          <span style={{ font: '500 10px/1 "IBM Plex Mono",monospace', color: COL.mut2, flex: '0 0 auto' }}>{e.obj}</span>
        </div>
      ))}
      <div style={{ borderTop: '1px solid ' + COL.line2, paddingTop: 9, display: 'flex', flexDirection: 'column', gap: 1, overflow: 'hidden' }}>
        {events.slice(0, 7).map((e) => (
          <div key={e.id} style={{ display: 'flex', gap: 9, alignItems: 'center', minWidth: 0, padding: '5px 0', font: '500 11.5px/1.2 "IBM Plex Mono",monospace' }}>
            <span style={{ color: COL.mut2, flex: '0 0 52px' }}>{fmt.time(e.t)}</span>
            <StatusDot color={sevColor[e.sev]} size={6} />
            <span style={{ color: COL.mut, flex: '0 0 78px', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{e.src}</span>
            <span style={{ color: COL.text, flex: 1, minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', fontFamily: '"IBM Plex Sans",sans-serif' }}>{e.msg}</span>
          </div>
        ))}
      </div>
    </Panel>
  );
}

// --- Auth UI: user chip + admin-only status chip ---------------------------
// Demonstrates authorization end-to-end: every signed-in user sees their
// name/role + a sign-out control; the small server-status readout next to it
// only renders for the "admin" role (useAdminStatus simply returns null for
// viewers — the backend never even sends them that data, see /api/admin/status).
function UserChip({ me }) {
  if (!me) return null;
  const roleColor = me.role === 'admin' ? COL.violet : COL.info;
  return (
    <span style={{ display: 'inline-flex', alignItems: 'center', gap: 9, font: '500 12px/1 "IBM Plex Sans",sans-serif', color: COL.text, padding: '5px 6px 5px 12px', borderRadius: 6, background: COL.panel, border: '1px solid ' + COL.line }}>
      <span style={{ font: '600 12px/1 "IBM Plex Mono",monospace' }}>{me.username}</span>
      <span style={{ font: '600 9px/1 "IBM Plex Sans",sans-serif', letterSpacing: '0.1em', textTransform: 'uppercase', color: roleColor, padding: '3px 6px', borderRadius: 4, background: roleColor + '18' }}>{me.role}</span>
      <a href="/logout" style={{ font: '500 11px/1 "IBM Plex Sans",sans-serif', color: COL.mut, textDecoration: 'none', padding: '5px 8px', borderRadius: 4, border: '1px solid ' + COL.line2 }}
        onMouseEnter={(e) => { e.currentTarget.style.color = COL.text; }}
        onMouseLeave={(e) => { e.currentTarget.style.color = COL.mut; }}>
        Sign out
      </a>
    </span>
  );
}

// AdminStatusChip renders the small "ADMIN · UP …" readout sourced from
// /api/admin/status — admin role only (useAdminStatus returns null for
// viewers, so this renders nothing for them).
function AdminStatusChip({ status }) {
  if (!status) return null;
  return (
    <span title={'sim started ' + fmt.time(new Date(status.simStartedAt)) + ' · ' + status.goVersion + ' · ' + status.goroutines + ' goroutines'}
      style={{ display: 'inline-flex', alignItems: 'center', gap: 8, font: '500 11px/1 "IBM Plex Mono",monospace', color: COL.violet, padding: '6px 11px', borderRadius: 6, background: COL.violet + '14', border: '1px solid ' + COL.violet + '33' }}>
      <StatusDot color={COL.violet} size={6} />
      ADMIN · UP {status.serverUptime} · {status.simTicks} TICKS
    </span>
  );
}

// LoadingScreen is shown until the first /api/snapshot response arrives.
function LoadingScreen() {
  return (
    <div style={{ width: '100%', height: '100%', background: COL.bg, color: COL.mut, display: 'flex', alignItems: 'center', justifyContent: 'center', font: '500 13px "IBM Plex Mono",monospace', letterSpacing: '0.08em' }}>
      <StatusDot color={COL.accent} size={8} pulse /> &nbsp;CONNECTING TO /api/snapshot …
    </div>
  );
}

// Dashboard is the top-level page: header (cluster identity, live clock,
// auth chips), the KPI strip, the node-grid + utilization panel on the left,
// the Ceph + alerts/events rail on the right, and the platform credit footer.
function Dashboard() {
  const snapshot = useLiveSnapshot(2000);
  const clock = useClock();
  const me = useMe();
  const adminStatus = useAdminStatus(me?.role === 'admin');
  if (!snapshot) return <LoadingScreen />;

  const { nodes, ceph, events, cluster } = snapshot;
  const alertCount = events.filter((e) => e.sev === 'crit' || e.sev === 'warn').length;

  return (
    <div style={{ width: '100%', height: '100%', background: COL.bg, color: COL.text, font: '400 14px "IBM Plex Sans",sans-serif', padding: '14px 18px', display: 'flex', flexDirection: 'column', gap: 8, boxSizing: 'border-box' }}>
      {/* header */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 16, flex: '0 0 auto' }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 11 }}>
          <div style={{ width: 30, height: 30, borderRadius: 7, background: `linear-gradient(135deg, ${COL.accent}, ${COL.accentDim})`, display: 'flex', alignItems: 'center', justifyContent: 'center', font: '700 15px "IBM Plex Mono",monospace', color: '#04222a' }}>⎈</div>
          <div>
            <div style={{ font: '600 16px/1 "IBM Plex Mono",monospace', color: COL.text }}>{cluster.name}</div>
            <div style={{ font: '500 11px/1 "IBM Plex Sans",sans-serif', color: COL.mut, marginTop: 4 }}>{cluster.region} · {cluster.version}</div>
          </div>
        </div>
        <div style={{ marginLeft: 'auto', display: 'flex', alignItems: 'center', gap: 12 }}>
          <span style={{ display: 'inline-flex', alignItems: 'center', gap: 7, font: '600 11px/1 "IBM Plex Mono",monospace', color: COL.ok, padding: '6px 11px', borderRadius: 6, background: COL.ok + '14', border: '1px solid ' + COL.ok + '33' }}>
            <StatusDot color={COL.ok} size={7} pulse />LIVE
          </span>
          <HealthBadge state={ceph.health} big />
          <AdminStatusChip status={adminStatus} />
          <span style={{ font: '600 15px/1 "IBM Plex Mono",monospace', color: COL.text, padding: '6px 12px', background: COL.panel, border: '1px solid ' + COL.line, borderRadius: 6 }}>{fmt.time(clock)}</span>
          <UserChip me={me} />
        </div>
      </div>

      {/* KPI strip */}
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(6,1fr)', gap: 12, flex: '0 0 auto' }}>
        <Kpi label="Nodes Ready" value={cluster.ready} unit={'/ ' + cluster.nodes} accent={cluster.ready < cluster.nodes ? COL.warn : COL.ok} sub={(cluster.nodes - cluster.ready) + ' not schedulable'} />
        <Kpi label="Pods Running" value={cluster.podsUsed} unit={'/ ' + cluster.podsCap} sub={((cluster.podsUsed / cluster.podsCap) * 100).toFixed(0) + '% of capacity'} />
        <Kpi label="Net Ingress" value={fmt.mbps(cluster.netIn).split(' ')[0]} unit={fmt.mbps(cluster.netIn).split(' ')[1]} spark={nodes[3].netHist} sparkColor={COL.accent} />
        <Kpi label="Net Egress" value={fmt.mbps(cluster.netOut).split(' ')[0]} unit={fmt.mbps(cluster.netOut).split(' ')[1]} spark={nodes[5].netHist} sparkColor={COL.violet} />
        <Kpi label="Disk I/O" value={fmt.mbs(cluster.diskR + cluster.diskW).split(' ')[0]} unit={fmt.mbs(cluster.diskR + cluster.diskW).split(' ')[1]} sub={'R ' + fmt.k(cluster.diskR) + ' · W ' + fmt.k(cluster.diskW)} />
        <Kpi label="Active Alerts" value={alertCount} accent={alertCount ? COL.warn : COL.ok} sub="last 15 min" />
      </div>

      {/* body */}
      <div style={{ flex: '1 1 auto', display: 'grid', gridTemplateColumns: '1fr 386px', gap: 14, minHeight: 0 }}>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 14, minHeight: 0, minWidth: 0 }}>
          <Panel title="Cluster Utilization" bodyStyle={{ display: 'flex', alignItems: 'center', justifyContent: 'space-around', gap: 16 }}>
            <RingGauge value={cluster.cpu} size={150} stroke={13} label="Cluster CPU" sub={cluster.nodes + ' nodes'} />
            <RingGauge value={cluster.mem} size={150} stroke={13} label="Cluster MEM" sub={fmt.tb(cluster.mem * 5.12 / 100 * 10) + ' TiB'} />
            <Donut size={150} stroke={15}
              segments={[{ value: cluster.podsUsed, color: COL.info }, { value: cluster.podsCap - cluster.podsUsed, color: 'rgba(255,255,255,0.06)' }]}
              centerTop={((cluster.podsUsed / cluster.podsCap) * 100).toFixed(0) + '%'} centerSub="Pods" />
            <div style={{ flex: 1, maxWidth: 280, alignSelf: 'stretch', display: 'flex', flexDirection: 'column', justifyContent: 'center', gap: 6, paddingLeft: 8, borderLeft: '1px solid ' + COL.line2 }}>
              <div style={{ font: '600 10.5px/1 "IBM Plex Sans",sans-serif', letterSpacing: '0.12em', textTransform: 'uppercase', color: COL.mut }}>Network Throughput</div>
              <Sparkline data={nodes[5].netHist} w={260} h={56} color={COL.accent} />
              <div style={{ display: 'flex', justifyContent: 'space-between', font: '500 11px/1 "IBM Plex Mono",monospace', color: COL.mut2 }}>
                <span style={{ color: COL.accent }}>↓ {fmt.mbps(cluster.netIn)}</span>
                <span style={{ color: COL.violet }}>↑ {fmt.mbps(cluster.netOut)}</span>
              </div>
            </div>
          </Panel>
          <div style={{ flex: '1 1 auto', display: 'grid', gridTemplateColumns: 'repeat(5,1fr)', gridTemplateRows: '1fr 1fr', gap: 12, minHeight: 0 }}>
            {nodes.map((n) => <NodeCard key={n.id} n={n} />)}
          </div>
        </div>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 14, minHeight: 0, minWidth: 0 }}>
          <CephRail ceph={ceph} />
          <div style={{ flex: '1 1 auto', minHeight: 0, display: 'flex' }}><EventsPanel events={events} /></div>
        </div>
      </div>

      {/* footer */}
      <div style={{ flex: '0 0 auto', textAlign: 'center', font: '500 10px/1 "IBM Plex Mono",monospace', letterSpacing: '0.04em', color: COL.mut2 }}>
        SoftNet DevSecOps Platform · © 2026 Labiyb M. Said · DevSecOps Engineer
      </div>
    </div>
  );
}

Object.assign(window, { Dashboard });
