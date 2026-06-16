package main

// ---------------------------------------------------------------------------
// Author: Labiyb M. Said — DevSecOps Engineer
// Contact: abdulmunimsaid82@gmail.com
// ---------------------------------------------------------------------------

import (
	"context"
	"fmt"
	"log"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	promapi "github.com/prometheus/client_golang/api"
	promv1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
)

// RealSource queries your Prometheus instance (which must already be scraping
// node-exporter and kube-state-metrics — standard kube-prometheus-stack does
// this out of the box) and builds a Snapshot from the results.
//
// No kubeconfig is needed: every piece of data (node names, roles, status,
// CPU/mem/disk/net, pod counts, Ceph metrics, firing alerts) is fetched
// through Prometheus alone, so the dashboard works the same whether you're
// running it locally against the NodePort or as a Pod inside the cluster.
//
// Enable it by setting PROMETHEUS_URL, e.g.:
//   - inside the cluster:  http://monitoring-kube-prometheus-prometheus.monitoring.svc:9090
//   - NodePort (dev/local): http://192.168.200.15:30909
type RealSource struct {
	prom promv1.API

	mu           sync.RWMutex
	snap         Snapshot
	cpuHist      map[string][]float64
	memHist      map[string][]float64
	netHist      map[string][]float64
	cephUsedHist []float64
	cephIopsHist []float64
	events       []Event
	evtSeq       int
	lastOK       bool // true if the most recent refresh() saw at least one node from Prometheus
}

func NewRealSource(prometheusURL string) (*RealSource, error) {
	client, err := promapi.NewClient(promapi.Config{Address: prometheusURL})
	if err != nil {
		return nil, fmt.Errorf("prometheus client: %w", err)
	}
	// quick connectivity check — fail fast at startup rather than silently
	// serving empty data
	prom := promv1.NewAPI(client)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if _, _, err := prom.Query(ctx, "1", time.Now()); err != nil {
		return nil, fmt.Errorf("cannot reach Prometheus at %s: %w", prometheusURL, err)
	}
	log.Printf("real source: connected to Prometheus at %s", prometheusURL)
	return &RealSource{
		prom:         prom,
		cpuHist:      make(map[string][]float64),
		memHist:      make(map[string][]float64),
		netHist:      make(map[string][]float64),
		cephUsedHist: make([]float64, histLen),
		cephIopsHist: make([]float64, histLen),
	}, nil
}

// Run refreshes the snapshot every 2s. Call it in a goroutine; it never
// returns. The first refresh runs immediately (synchronously) so the first
// call to Snapshot() always returns real data, not an empty struct.
func (r *RealSource) Run() {
	r.refresh()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		r.refresh()
	}
}

func (r *RealSource) Snapshot() Snapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.snap
}

// SourceLabel implements dataSourceStatus — shown on the login page's DATA
// SOURCE row to make it clear the dashboard is backed by a live cluster.
func (r *RealSource) SourceLabel() string { return "Prometheus (live cluster)" }

// Healthy implements dataSourceStatus. It reflects whether the most recent
// refresh() tick actually got node data back from Prometheus.
func (r *RealSource) Healthy() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.lastOK
}

// ---- queries ----------------------------------------------------------------

// promQueries is the full set of PromQL expressions fetched on every tick.
// All of them run in parallel inside refresh(). Add or change expressions here
// if your cluster uses different label names or job names.
var promQueries = map[string]string{
	// node inventory (from kube-state-metrics)
	"nodes":    `kube_node_info{}`,
	"cpRole":   `kube_node_role{role=~"control-plane|master"}`,
	"ready":    `kube_node_status_condition{condition="Ready",status="true"}`,
	"cordoned": `kube_node_spec_unschedulable`,
	"podCap":   `kube_node_status_capacity{resource="pods"}`,
	"pods":     `count by (node) (kube_pod_info{node!=""})`,

	// Per-node utilisation from node-exporter.
	//
	// kube-prometheus-stack normally relabels node-exporter metrics to carry a
	// "node" label matching the Kubernetes node name — but some cluster configs
	// skip that and leave only "instance" (IP:port). We handle both cases:
	//   • If a "node" label exists, avg/sum by (node) works directly.
	//   • If only "instance" exists, we strip the port with label_replace and
	//     join against kube_node_info's internal_ip to get the node name.
	//
	// The queries below use the join path, which works in both cases. The
	// right-hand side is wrapped in topk(1, ...) by (ip) to dedupe — clusters
	// that have a stale/decommissioned node object sharing an internal_ip with
	// a newer node (e.g. during a node replacement) would otherwise produce two
	// kube_node_info series for the same ip, and Prometheus rejects that as
	// "many-to-many matching not allowed", which fails the *entire* query and
	// zeroes out CPU/mem/disk/net for *every* node, not just the duplicated one.
	"cpu": `
		label_replace(
		  avg by (instance) (rate(node_cpu_seconds_total{mode="idle"}[2m])),
		  "ip","$1","instance","([^:]+):.*"
		) * 100
		* on(ip) group_left(node)
		topk(1, label_replace(kube_node_info, "ip","$1","internal_ip","(.*)")) by (ip)
		* -1 + 100`,
	"mem": `
		label_replace(
		  1 - node_memory_MemAvailable_bytes / node_memory_MemTotal_bytes,
		  "ip","$1","instance","([^:]+):.*"
		) * 100
		* on(ip) group_left(node)
		topk(1, label_replace(kube_node_info, "ip","$1","internal_ip","(.*)")) by (ip)`,
	"dskR": `
		label_replace(
		  sum by (instance) (rate(node_disk_read_bytes_total[2m])) / 1048576,
		  "ip","$1","instance","([^:]+):.*"
		)
		* on(ip) group_left(node)
		topk(1, label_replace(kube_node_info, "ip","$1","internal_ip","(.*)")) by (ip)`,
	"dskW": `
		label_replace(
		  sum by (instance) (rate(node_disk_written_bytes_total[2m])) / 1048576,
		  "ip","$1","instance","([^:]+):.*"
		)
		* on(ip) group_left(node)
		topk(1, label_replace(kube_node_info, "ip","$1","internal_ip","(.*)")) by (ip)`,
	"netIn": `
		label_replace(
		  sum by (instance) (rate(node_network_receive_bytes_total{device!~"lo|veth.+|docker.+|flannel.+|cali.+|cilium.+|tunl.+"}[2m])) * 8 / 1048576,
		  "ip","$1","instance","([^:]+):.*"
		)
		* on(ip) group_left(node)
		topk(1, label_replace(kube_node_info, "ip","$1","internal_ip","(.*)")) by (ip)`,
	"netOut": `
		label_replace(
		  sum by (instance) (rate(node_network_transmit_bytes_total{device!~"lo|veth.+|docker.+|flannel.+|cali.+|cilium.+|tunl.+"}[2m])) * 8 / 1048576,
		  "ip","$1","instance","([^:]+):.*"
		)
		* on(ip) group_left(node)
		topk(1, label_replace(kube_node_info, "ip","$1","internal_ip","(.*)")) by (ip)`,

	// Ceph — works for both Rook-Ceph's built-in exporter and a standalone
	// ceph-exporter. Uses `or` to handle different metric name variants across
	// versions. If your cluster has none of these, the Ceph panel will show
	// "HEALTH_UNKNOWN" with zero values rather than crashing.
	"cephHealth":  `ceph_health_status`,
	"cephRaw":     `sum(ceph_cluster_total_bytes) or sum(ceph_osd_stat_bytes)`,
	"cephUsed":    `sum(ceph_cluster_total_used_bytes) or sum(ceph_osd_stat_bytes_used)`,
	"osdTotal":    `count(ceph_osd_metadata) or count(ceph_osd_up)`,
	"osdUp":       `sum(ceph_osd_up)`,
	"osdIn":       `sum(ceph_osd_in)`,
	"readIOPS":    `sum(rate(ceph_pool_rd[2m]))`,
	"writeIOPS":   `sum(rate(ceph_pool_wr[2m]))`,
	"readMBs":     `sum(rate(ceph_pool_rd_bytes[2m])) / 1048576`,
	"writeMBs":    `sum(rate(ceph_pool_wr_bytes[2m])) / 1048576`,
	"pgActive":    `sum(ceph_pg_active)`,
	"pgDegraded":  `sum(ceph_pg_degraded)`,
	"pgTotal":     `sum(ceph_pg_total)`,

	// firing alert rules → event feed (built into Prometheus, no alertmanager needed)
	"alerts": `ALERTS{alertstate="firing"}`,
}

func (r *RealSource) queryAll(ctx context.Context) map[string]model.Vector {
	results := make(map[string]model.Vector, len(promQueries))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for name, q := range promQueries {
		wg.Add(1)
		go func(name, q string) {
			defer wg.Done()
			v := r.query(ctx, q)
			mu.Lock()
			results[name] = v
			mu.Unlock()
		}(name, q)
	}
	wg.Wait()
	return results
}

// query runs a single instant Prometheus query. Errors are logged and an empty
// vector is returned so the rest of the snapshot still builds correctly.
func (r *RealSource) query(ctx context.Context, q string) model.Vector {
	result, warnings, err := r.prom.Query(ctx, q, time.Now())
	if err != nil {
		log.Printf("prom query %q: %v", q, err)
		return nil
	}
	for _, w := range warnings {
		log.Printf("prom query %q warning: %s", q, w)
	}
	v, _ := result.(model.Vector)
	return v
}

// ---- helpers ----------------------------------------------------------------

// nodeVal extracts the value for a given node from a Vector whose samples
// carry a "node" label. Returns 0 if the node is not present or the value
// is NaN/Inf.
func nodeVal(v model.Vector, node string) float64 {
	for _, s := range v {
		if string(s.Metric["node"]) == node {
			f := float64(s.Value)
			if math.IsNaN(f) || math.IsInf(f, 0) {
				return 0
			}
			return f
		}
	}
	return 0
}

// scalar returns the value of the first sample in v, or def if v is empty /
// the value is not a real number.
func scalar(v model.Vector, def float64) float64 {
	if len(v) == 0 {
		return def
	}
	f := float64(v[0].Value)
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return def
	}
	return f
}

func seedHistFlat(v float64) []float64 {
	h := make([]float64, histLen)
	for i := range h {
		h[i] = v
	}
	return h
}

// isControlPlane checks both the dedicated kube_node_role metric and common
// naming conventions as a fallback for clusters with older kube-state-metrics.
func isControlPlane(node string, cpSet map[string]bool) bool {
	if cpSet[node] {
		return true
	}
	n := strings.ToLower(node)
	return strings.Contains(n, "master") || strings.Contains(n, "control-plane") ||
		strings.Contains(n, "cp-") || strings.HasPrefix(n, "cp")
}

// ---- refresh ----------------------------------------------------------------

func (r *RealSource) refresh() {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()

	res := r.queryAll(ctx)

	// ---- node inventory ------------------------------------------------

	// collect unique node names from kube_node_info
	nodeSet := make(map[string]struct{})
	for _, s := range res["nodes"] {
		if n := string(s.Metric["node"]); n != "" {
			nodeSet[n] = struct{}{}
		}
	}
	nodeNames := make([]string, 0, len(nodeSet))
	for n := range nodeSet {
		nodeNames = append(nodeNames, n)
	}
	sort.Strings(nodeNames)

	cpNodes := make(map[string]bool)
	for _, s := range res["cpRole"] {
		cpNodes[string(s.Metric["node"])] = true
	}

	readyNodes := make(map[string]bool)
	for _, s := range res["ready"] {
		if float64(s.Value) == 1 {
			readyNodes[string(s.Metric["node"])] = true
		}
	}
	cordonedNodes := make(map[string]bool)
	for _, s := range res["cordoned"] {
		if float64(s.Value) == 1 {
			cordonedNodes[string(s.Metric["node"])] = true
		}
	}
	podCap := make(map[string]int)
	for _, s := range res["podCap"] {
		if n := string(s.Metric["node"]); n != "" {
			podCap[n] = int(float64(s.Value))
		}
	}

	// ---- build Node list -----------------------------------------------

	nodes := make([]Node, 0, len(nodeNames))
	for _, name := range nodeNames {
		role := "worker"
		if isControlPlane(name, cpNodes) {
			role = "control-plane"
		}

		status := "NotReady"
		if readyNodes[name] {
			status = "Ready"
		}
		if cordonedNodes[name] {
			status = "SchedulingDisabled"
		}

		cap := podCap[name]
		if cap == 0 {
			if role == "control-plane" {
				cap = 110
			} else {
				cap = 250
			}
		}

		cpu := nodeVal(res["cpu"], name)
		mem := nodeVal(res["mem"], name)
		netIn := nodeVal(res["netIn"], name)
		netOut := nodeVal(res["netOut"], name)

		// initialise ring buffers on first time we see this node
		if _, ok := r.cpuHist[name]; !ok {
			r.cpuHist[name] = seedHistFlat(cpu)
			r.memHist[name] = seedHistFlat(mem)
			r.netHist[name] = seedHistFlat((netIn + netOut) / 24)
		}
		r.cpuHist[name] = pushHist(r.cpuHist[name], cpu)
		r.memHist[name] = pushHist(r.memHist[name], mem)
		r.netHist[name] = pushHist(r.netHist[name], (netIn+netOut)/24)

		nodes = append(nodes, Node{
			ID: name, Name: name, Role: role, Status: status,
			CPU: cpu, Mem: mem,
			DiskR: nodeVal(res["dskR"], name), DiskW: nodeVal(res["dskW"], name),
			NetIn: netIn, NetOut: netOut,
			PodsCap: cap, Pods: int(nodeVal(res["pods"], name)),
			CPUHist: append([]float64{}, r.cpuHist[name]...),
			MemHist: append([]float64{}, r.memHist[name]...),
			NetHist: append([]float64{}, r.netHist[name]...),
		})
	}

	// ---- Ceph ----------------------------------------------------------

	healthCode := int(scalar(res["cephHealth"], -1))
	healthStr, healthDetail := "HEALTH_UNKNOWN", "Ceph metrics not available in Prometheus"
	switch healthCode {
	case 0:
		healthStr, healthDetail = "HEALTH_OK", ""
	case 1:
		healthStr, healthDetail = "HEALTH_WARN", "check Ceph dashboard for details"
	case 2:
		healthStr, healthDetail = "HEALTH_ERR", "cluster error — check Ceph dashboard"
	}

	rawBytes := scalar(res["cephRaw"], 0)
	usedBytes := scalar(res["cephUsed"], 0)
	rawTiB := rawBytes / (1024 * 1024 * 1024 * 1024)
	usedTiB := usedBytes / (1024 * 1024 * 1024 * 1024)
	usedPct := 0.0
	if rawTiB > 0 {
		usedPct = (usedTiB / rawTiB) * 100
	}
	readIOPS := scalar(res["readIOPS"], 0)
	r.cephUsedHist = pushHist(r.cephUsedHist, usedPct)
	r.cephIopsHist = pushHist(r.cephIopsHist, readIOPS/200)

	osdTotal := int(scalar(res["osdTotal"], 0))
	osdUp := int(scalar(res["osdUp"], 0))
	osdDown := osdTotal - osdUp
	if osdDown < 0 {
		osdDown = 0
	}

	ceph := Ceph{
		Health: healthStr, HealthDetail: healthDetail,
		RawTiB: rawTiB, UsedTiB: usedTiB,
		OSDTotal: osdTotal, OSDUp: osdUp, OSDIn: int(scalar(res["osdIn"], 0)), OSDDown: osdDown,
		ReadIOPS: readIOPS, WriteIOPS: scalar(res["writeIOPS"], 0),
		ReadMBs: scalar(res["readMBs"], 0), WriteMBs: scalar(res["writeMBs"], 0),
		PGs:         int(scalar(res["pgTotal"], 0)),
		PGActive:    int(scalar(res["pgActive"], 0)),
		PGDegraded:  int(scalar(res["pgDegraded"], 0)),
		UsedHist:    append([]float64{}, r.cephUsedHist...),
		IopsHist:    append([]float64{}, r.cephIopsHist...),
	}

	// ---- Events (firing Prometheus alert rules) -------------------------

	r.mu.Lock()
	existing := make(map[string]bool, len(r.events))
	for _, e := range r.events {
		existing[e.ID] = true
	}
	r.mu.Unlock()

	newEvents := make([]Event, 0)
	for _, s := range res["alerts"] {
		alertName := string(s.Metric["alertname"])
		sev := "warn"
		if sv := string(s.Metric["severity"]); sv == "critical" || sv == "error" {
			sev = "crit"
		}
		ns := string(s.Metric["namespace"])
		if ns == "" {
			ns = "—"
		}
		id := "alert-" + alertName + "-" + ns
		if !existing[id] {
			r.evtSeq++
			newEvents = append(newEvents, Event{
				ID:   fmt.Sprintf("real-%d", r.evtSeq),
				Sev:  sev,
				Src:  "prometheus",
				Obj:  ns,
				Msg:  alertName,
				Time: time.Now(),
			})
		}
	}

	r.mu.Lock()
	if len(newEvents) > 0 {
		r.events = append(newEvents, r.events...)
		if len(r.events) > 40 {
			r.events = r.events[:40]
		}
	}
	events := make([]Event, len(r.events))
	copy(events, r.events)
	r.mu.Unlock()

	// ---- Cluster aggregates --------------------------------------------

	var activeCPU, activeMem, netInSum, netOutSum, diskRSum, diskWSum float64
	var podsUsed, podsCap, ready, activeCount int
	for _, n := range nodes {
		if n.Status == "Ready" {
			ready++
		}
		podsUsed += n.Pods
		podsCap += n.PodsCap
		netInSum += n.NetIn
		netOutSum += n.NetOut
		diskRSum += n.DiskR
		diskWSum += n.DiskW
		if n.Status != "SchedulingDisabled" {
			activeCPU += n.CPU
			activeMem += n.Mem
			activeCount++
		}
	}
	if activeCount == 0 {
		activeCount = 1
	}

	cluster := Cluster{
		Name: "prod-cluster-01", Region: "connected · live", Version: "live",
		Nodes: len(nodes), Ready: ready,
		CPU: activeCPU / float64(activeCount), Mem: activeMem / float64(activeCount),
		PodsUsed: podsUsed, PodsCap: podsCap,
		NetIn: netInSum, NetOut: netOutSum, DiskR: diskRSum, DiskW: diskWSum,
	}

	r.mu.Lock()
	r.snap = Snapshot{Nodes: nodes, Ceph: ceph, Events: events, Cluster: cluster}
	r.lastOK = len(nodeNames) > 0
	r.mu.Unlock()
}
