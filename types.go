package main

// ---------------------------------------------------------------------------
// Author: Labiyb M. Said — DevSecOps Engineer
// Contact: abdulmunimsaid82@gmail.com
// ---------------------------------------------------------------------------

import "time"

// Node mirrors a single Kubernetes node's live telemetry.
type Node struct {
	ID      string  `json:"id"`
	Name    string  `json:"name"`
	Role    string  `json:"role"`   // "control-plane" | "worker"
	Status  string  `json:"status"` // "Ready" | "SchedulingDisabled" | "NotReady"
	CPU     float64 `json:"cpu"`    // 0-100
	Mem     float64 `json:"mem"`    // 0-100
	DiskR   float64 `json:"diskR"`  // MB/s
	DiskW   float64 `json:"diskW"`  // MB/s
	NetIn   float64 `json:"netIn"`  // Mb/s
	NetOut  float64 `json:"netOut"` // Mb/s
	PodsCap int     `json:"podsCap"`
	Pods    int     `json:"pods"`

	CPUHist []float64 `json:"cpuHist"`
	MemHist []float64 `json:"memHist"`
	NetHist []float64 `json:"netHist"`
}

// Ceph mirrors the storage cluster's health and performance counters.
type Ceph struct {
	Health       string  `json:"health"`       // "HEALTH_OK" | "HEALTH_WARN" | "HEALTH_ERR"
	HealthDetail string  `json:"healthDetail"` // short human-readable detail line
	RawTiB       float64 `json:"rawTiB"`
	UsedTiB      float64 `json:"usedTiB"`
	OSDTotal     int     `json:"osdTotal"`
	OSDUp        int     `json:"osdUp"`
	OSDIn        int     `json:"osdIn"`
	OSDDown      int     `json:"osdDown"`
	ReadIOPS     float64 `json:"readIOPS"`
	WriteIOPS    float64 `json:"writeIOPS"`
	ReadMBs      float64 `json:"readMBs"`
	WriteMBs     float64 `json:"writeMBs"`
	PGs          int     `json:"pgs"`
	PGActive     int     `json:"pgActive"`
	PGDegraded   int     `json:"pgDegraded"`
	PGRecovering int     `json:"pgRecovering"`
	RecovMBs     float64 `json:"recovMBs"`

	UsedHist []float64 `json:"usedHist"`
	IopsHist []float64 `json:"iopsHist"`
}

// Event is a single alert/event-log entry (kubelet, ceph-mgr, scheduler, ...).
type Event struct {
	ID   string    `json:"id"`
	Sev  string    `json:"sev"` // "crit" | "warn" | "info" | "ok"
	Src  string    `json:"src"`
	Obj  string    `json:"obj"`
	Msg  string    `json:"msg"`
	Time time.Time `json:"t"`
}

// Cluster holds derived, cluster-wide aggregates computed from the node list.
type Cluster struct {
	Name    string  `json:"name"`
	Region  string  `json:"region"`
	Version string  `json:"version"`
	Nodes   int     `json:"nodes"`
	Ready   int     `json:"ready"`
	CPU     float64 `json:"cpu"`
	Mem     float64 `json:"mem"`

	PodsUsed int `json:"podsUsed"`
	PodsCap  int `json:"podsCap"`

	NetIn  float64 `json:"netIn"`
	NetOut float64 `json:"netOut"`
	DiskR  float64 `json:"diskR"`
	DiskW  float64 `json:"diskW"`
}

// Snapshot is the full payload served to the dashboard on every poll.
type Snapshot struct {
	Nodes   []Node  `json:"nodes"`
	Ceph    Ceph    `json:"ceph"`
	Events  []Event `json:"events"`
	Cluster Cluster `json:"cluster"`
}
