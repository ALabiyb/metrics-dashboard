package main

// ---------------------------------------------------------------------------
// Author: Labiyb M. Said — DevSecOps Engineer
// Contact: saidlabiybm@gmail.com
// ---------------------------------------------------------------------------

import (
	"fmt"
	"math/rand"
	"os"
	"sync"
	"time"
)

// histLen is how many samples we keep for sparkline history (mirrors HIST in
// the original cluster-data.jsx prototype).
const histLen = 48

// rw performs a bounded random walk: nudge v by up to ±step, clamping (with a
// small inward bounce) to [min, max]. Same shape as the prototype's rw().
func rw(v, step, min, max float64) float64 {
	n := v + (rand.Float64()-0.5)*2*step
	if n < min {
		n = min + rand.Float64()*step
	}
	if n > max {
		n = max - rand.Float64()*step
	}
	return n
}

func rnd(a, b float64) float64 { return a + rand.Float64()*(b-a) }

func seedHist(base, spread float64) []float64 {
	a := make([]float64, histLen)
	v := base
	for i := range a {
		v = rw(v, spread, 2, 99)
		a[i] = v
	}
	return a
}

func pushHist(h []float64, v float64) []float64 {
	out := make([]float64, len(h))
	copy(out, h[1:])
	out[len(out)-1] = v
	return out
}

// nodeSeed is the static starting point for each simulated node — name, role,
// starting CPU/Mem and status. Mirrors the `defs` table in makeNodes().
type nodeSeed struct {
	name, role, status string
	cpu, mem           float64
}

var nodeSeeds = []nodeSeed{
	{"cp-01", "control-plane", "Ready", 22, 41},
	{"cp-02", "control-plane", "Ready", 19, 38},
	{"cp-03", "control-plane", "Ready", 25, 44},
	{"wrk-01", "worker", "Ready", 58, 63},
	{"wrk-02", "worker", "Ready", 71, 55},
	{"wrk-03", "worker", "Ready", 88, 79},                // hot box
	{"wrk-04", "worker", "Ready", 44, 61},
	{"wrk-05", "worker", "Ready", 67, 91},                // memory pressure
	{"wrk-06", "worker", "Ready", 34, 49},
	{"wrk-07", "worker", "SchedulingDisabled", 12, 18},   // cordoned
}

// eventTemplate is a (severity, source, object, message) tuple drawn from
// when emitting a fresh simulated event. Mirrors EVT_POOL.
type eventTemplate struct{ sev, src, obj, msg string }

var eventPool = []eventTemplate{
	{"warn", "kubelet", "wrk-03", "CPU throttling on pod ingest-worker-7d4"},
	{"info", "scheduler", "wrk-04", "Scheduled pod payments-api-5f8 → wrk-04"},
	{"crit", "ceph-mgr", "osd.17", "OSD reported slow ops (> 30s)"},
	{"warn", "ceph-mgr", "osd.09", "OSD nearfull (87% used)"},
	{"info", "kube-apiserver", "cp-01", "Leader election renewed"},
	{"ok", "kubelet", "wrk-05", "Memory pressure cleared"},
	{"warn", "kubelet", "wrk-05", "Node memory pressure detected"},
	{"info", "autoscaler", "—", "Scaled deployment web from 6 → 8 replicas"},
	{"ok", "ceph-mgr", "cluster", "PG recovery complete (4 → 0 degraded)"},
	{"crit", "kubelet", "wrk-07", "Node cordoned — SchedulingDisabled"},
	{"info", "etcd", "cp-02", "Compaction completed, db size 412 MiB"},
	{"warn", "network", "wrk-02", "TX retransmits elevated (0.8%)"},
}

// seedEvent is a pre-populated event shown at startup, with how many seconds
// "ago" it should appear to have happened. Mirrors seedEvents().
type seedEvent struct {
	sev, src, obj, msg string
	agoSeconds         int
}

var seedEvents = []seedEvent{
	{"crit", "kubelet", "wrk-07", "Node cordoned — SchedulingDisabled", 95},
	{"warn", "ceph-mgr", "osd.09", "OSD nearfull (87% used)", 220},
	{"warn", "kubelet", "wrk-03", "CPU throttling on pod ingest-worker-7d4", 360},
	{"info", "autoscaler", "—", "Scaled deployment web from 6 → 8 replicas", 540},
	{"ok", "ceph-mgr", "cluster", "PG recovery complete (4 → 0 degraded)", 720},
	{"info", "kube-apiserver", "cp-01", "Leader election renewed", 900},
}

// Simulator owns the in-memory cluster state and advances it on a timer,
// exactly like the `useClusterData` random-walk effect in the prototype.
// Swap this out for RealSource (see real_data.go) to drive the dashboard
// from an actual cluster instead of synthetic data.
type Simulator struct {
	mu        sync.RWMutex
	nodes     []Node
	ceph      Ceph
	events    []Event
	evtSeq    int
	ticks     int
	startedAt time.Time
}

func NewSimulator() *Simulator {
	s := &Simulator{
		nodes:     makeNodes(),
		ceph:      makeCeph(),
		startedAt: time.Now(),
	}
	s.events = s.makeSeedEvents()
	return s
}

// Stats reports simulator-internal counters — surfaced on the admin-only
// status endpoint as a stand-in for whatever ops/debug info you'd want to
// gate behind the "admin" role on a real deployment.
func (s *Simulator) Stats() (ticks int, startedAt time.Time) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ticks, s.startedAt
}

// SourceLabel implements dataSourceStatus — shown on the login page's DATA
// SOURCE row so it's obvious the dashboard is running on synthetic data.
func (s *Simulator) SourceLabel() string { return "Simulated data (demo mode)" }

// Healthy implements dataSourceStatus. The simulator runs entirely in-process
// and never fails, so it's always "Operational".
func (s *Simulator) Healthy() bool { return true }

func makeNodes() []Node {
	nodes := make([]Node, len(nodeSeeds))
	for i, d := range nodeSeeds {
		podsCap := 250
		podsLo, podsHi := 60.0, 210.0
		if d.role == "control-plane" {
			podsCap = 110
			podsLo, podsHi = 18, 34
		}
		nodes[i] = Node{
			ID: d.name, Name: d.name, Role: d.role, Status: d.status,
			CPU: d.cpu, Mem: d.mem,
			DiskR: rnd(20, 180), DiskW: rnd(10, 140),
			NetIn: rnd(60, 900), NetOut: rnd(40, 700),
			PodsCap: podsCap, Pods: int(rnd(podsLo, podsHi)),
			CPUHist: seedHist(d.cpu, 6),
			MemHist: seedHist(d.mem, 4),
			NetHist: seedHist(rnd(30, 70), 12),
		}
	}
	return nodes
}

func makeCeph() Ceph {
	return Ceph{
		Health: "HEALTH_WARN", HealthDetail: "1 nearfull osd(s)",
		RawTiB: 192.0, UsedTiB: 121.4,
		OSDTotal: 24, OSDUp: 24, OSDIn: 24, OSDDown: 0,
		ReadIOPS: rnd(8000, 14000), WriteIOPS: rnd(3000, 7000),
		ReadMBs: rnd(700, 1400), WriteMBs: rnd(300, 800),
		PGs: 2048, PGActive: 2041, PGDegraded: 5, PGRecovering: 2,
		RecovMBs: rnd(40, 180),
		UsedHist: seedHist(63, 0.4),
		IopsHist: seedHist(60, 10),
	}
}

func (s *Simulator) makeSeedEvents() []Event {
	now := time.Now()
	out := make([]Event, len(seedEvents))
	for i, e := range seedEvents {
		out[i] = Event{
			ID: fmt.Sprintf("seed-%d", i), Sev: e.sev, Src: e.src, Obj: e.obj, Msg: e.msg,
			Time: now.Add(-time.Duration(e.agoSeconds) * time.Second),
		}
	}
	return out
}

// Run advances the simulation every 2s, matching the prototype's setInterval.
// Call it in its own goroutine; it never returns.
func (s *Simulator) Run() {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		s.step()
	}
}

func (s *Simulator) step() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.ticks++

	for i := range s.nodes {
		n := &s.nodes[i]
		cordoned := n.Status == "SchedulingDisabled"
		if cordoned {
			n.CPU = rw(n.CPU, 2, 4, 22)
			n.Mem = rw(n.Mem, 1.5, 8, 28)
		} else {
			n.CPU = rw(n.CPU, 5, 6, 98)
			n.Mem = rw(n.Mem, 3, 20, 96)
		}
		n.DiskR = rw(n.DiskR, 30, 5, 320)
		n.DiskW = rw(n.DiskW, 24, 2, 260)
		n.NetIn = rw(n.NetIn, 140, 20, 1200)
		n.NetOut = rw(n.NetOut, 110, 10, 980)
		if !cordoned {
			delta := int(rnd(-2, 2))
			n.Pods = clampInt(n.Pods+delta, 0, n.PodsCap)
		}
		n.CPUHist = pushHist(n.CPUHist, n.CPU)
		n.MemHist = pushHist(n.MemHist, n.Mem)
		n.NetHist = pushHist(n.NetHist, (n.NetIn+n.NetOut)/24)
	}

	c := &s.ceph
	c.UsedTiB = rw(c.UsedTiB, 0.05, 118, 130)
	usedPct := (c.UsedTiB / c.RawTiB) * 100
	c.ReadIOPS = rw(c.ReadIOPS, 1200, 5000, 18000)
	c.WriteIOPS = rw(c.WriteIOPS, 700, 1500, 9000)
	c.ReadMBs = rw(c.ReadMBs, 160, 400, 1800)
	c.WriteMBs = rw(c.WriteMBs, 90, 150, 1100)
	c.RecovMBs = rw(c.RecovMBs, 30, 0, 260)
	c.UsedHist = pushHist(c.UsedHist, usedPct)
	c.IopsHist = pushHist(c.IopsHist, rw(c.ReadIOPS, 1200, 5000, 18000)/200)

	if rand.Float64() < 0.45 {
		t := eventPool[rand.Intn(len(eventPool))]
		s.evtSeq++
		ev := Event{ID: fmt.Sprintf("e%d", s.evtSeq), Sev: t.sev, Src: t.src, Obj: t.obj, Msg: t.msg, Time: time.Now()}
		s.events = append([]Event{ev}, s.events...)
		if len(s.events) > 40 {
			s.events = s.events[:40]
		}
	}
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// Snapshot returns a point-in-time copy of the cluster state plus derived
// cluster-wide aggregates, ready to be served as JSON. Mirrors the `cluster`
// useMemo in the prototype's useClusterData hook.
func (s *Simulator) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	nodes := make([]Node, len(s.nodes))
	copy(nodes, s.nodes)

	events := make([]Event, len(s.events))
	copy(events, s.events)

	var ready, podsUsed, podsCap int
	var activeCPU, activeMem, netIn, netOut, diskR, diskW float64
	activeCount := 0
	for _, n := range nodes {
		if n.Status == "Ready" {
			ready++
		}
		podsUsed += n.Pods
		podsCap += n.PodsCap
		netIn += n.NetIn
		netOut += n.NetOut
		diskR += n.DiskR
		diskW += n.DiskW
		if n.Status != "SchedulingDisabled" {
			activeCPU += n.CPU
			activeMem += n.Mem
			activeCount++
		}
	}
	if activeCount == 0 {
		activeCount = 1
	}

	clusterName := os.Getenv("CLUSTER_NAME")
	if clusterName == "" {
		clusterName = "dev-cluster"
	}
	cluster := Cluster{
		Name: clusterName, Region: "eu-central · rack A/B", Version: "v1.30.2",
		Nodes: len(nodes), Ready: ready,
		CPU: activeCPU / float64(activeCount), Mem: activeMem / float64(activeCount),
		PodsUsed: podsUsed, PodsCap: podsCap,
		NetIn: netIn, NetOut: netOut, DiskR: diskR, DiskW: diskW,
	}

	return Snapshot{Nodes: nodes, Ceph: s.ceph, Events: events, Cluster: cluster}
}
