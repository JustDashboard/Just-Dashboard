package accountlink

import (
	"encoding/json"
	"regexp"
)

// Snapshot is the heartbeat body, protocol section 6. The field set is fixed
// and pinned by a test: nothing is added here without it being listed in that
// document and in the scope table the Settings page shows.
//
// Every section except Server may be absent when the dashboard cannot read it,
// which is different from empty: `"alerts": []` says nothing is firing, and no
// `alerts` key says the dashboard does not know. A slice is therefore omitted
// only when it is nil.
type Snapshot struct {
	Protocol    int              `json:"protocol"`
	SentAt      int64            `json:"sentAt"`
	Server      ServerInfo       `json:"server"`
	Access      *AccessInfo      `json:"access,omitempty"`
	Metrics     *MetricsInfo     `json:"metrics,omitempty"`
	Containers  *ContainersInfo  `json:"containers,omitempty"`
	Stacks      []StackInfo      `json:"stacks,omitzero"`
	Alerts      []AlertInfo      `json:"alerts,omitzero"`
	Backups     *BackupsInfo     `json:"backups,omitempty"`
	Deployments *DeploymentsInfo `json:"deployments,omitempty"`
}

type ServerInfo struct {
	Name            string         `json:"name"`
	Version         string         `json:"version"`
	Latest          string         `json:"latest,omitempty"`
	UpdateAvailable bool           `json:"updateAvailable"`
	UpdateBreaking  bool           `json:"updateBreaking"`
	UpdateRun       *UpdateRunInfo `json:"updateRun,omitempty"`
	Scope           Scope          `json:"scope"`
	UptimeSeconds   uint64         `json:"uptimeSeconds"`
	OS              string         `json:"os,omitempty"`
	Kernel          string         `json:"kernel,omitempty"`
	Arch            string         `json:"arch,omitempty"`
	UpdatePolicy    *UpdatePolicy  `json:"updatePolicy,omitempty"`
}

type UpdateRunInfo struct {
	Status    string `json:"status"`
	Phase     string `json:"phase,omitempty"`
	ToVersion string `json:"toVersion,omitempty"`
}

type AccessInfo struct {
	Kind      string         `json:"kind"`
	Endpoint  string         `json:"endpoint,omitempty"`
	Port      int            `json:"port,omitempty"`
	TLS       string         `json:"tls,omitempty"`
	Tailscale *TailscaleInfo `json:"tailscale,omitempty"`
	SSHHost   string         `json:"sshHost,omitempty"`
}

type TailscaleInfo struct {
	Hostname string `json:"hostname,omitempty"`
	IP4      string `json:"ip4,omitempty"`
	Running  bool   `json:"running"`
}

type MetricsInfo struct {
	CPUPercent  float64 `json:"cpuPercent"`
	Load1       float64 `json:"load1"`
	MemPercent  float64 `json:"memPercent"`
	MemUsed     uint64  `json:"memUsed"`
	MemTotal    uint64  `json:"memTotal"`
	SwapPercent float64 `json:"swapPercent"`
	DiskPercent float64 `json:"diskPercent"`
	DiskMount   string  `json:"diskMount,omitempty"`
	NetRx       float64 `json:"netRx"`
	NetTx       float64 `json:"netTx"`
	CPUSteal    float64 `json:"cpuSteal"`
	PSICPU      float64 `json:"psiCpu"`
}

type ContainersInfo struct {
	Running   int             `json:"running"`
	Stopped   int             `json:"stopped"`
	Unhealthy int             `json:"unhealthy"`
	List      []ContainerInfo `json:"list,omitzero"`
}

type ContainerInfo struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Image  string `json:"image"`
	State  string `json:"state"`
	Health string `json:"health,omitempty"`
}

type StackInfo struct {
	Name     string `json:"name"`
	Services int    `json:"services"`
	Running  int    `json:"running"`
	Managed  bool   `json:"managed"`
}

type AlertInfo struct {
	Kind    string `json:"kind"`
	Subject string `json:"subject,omitempty"`
	Reading string `json:"reading,omitempty"`
	Since   int64  `json:"since"`
}

type BackupsInfo struct {
	Jobs    int          `json:"jobs"`
	LastRun *RunInfo     `json:"lastRun,omitempty"`
	Failed  int          `json:"failed"`
	List    []BackupInfo `json:"list,omitzero"`
}

type RunInfo struct {
	Status string `json:"status"`
	At     int64  `json:"at"`
}

type BackupInfo struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	LastStatus string `json:"lastStatus,omitempty"`
	LastAt     int64  `json:"lastAt,omitempty"`
}

type DeploymentsInfo struct {
	Projects int            `json:"projects"`
	LastRun  *DeployRunInfo `json:"lastRun,omitempty"`
}

type DeployRunInfo struct {
	Project string `json:"project,omitempty"`
	Status  string `json:"status"`
	At      int64  `json:"at"`
}

// The kinds of alert whose subject and reading name nothing but a measurement:
// the machine's CPU, its memory, or a mount point. The others name containers
// and backup jobs.
var measurementAlerts = map[string]bool{
	"cpu_high": true, "memory_high": true, "disk_filling": true,
}

// readingPattern is the whole vocabulary of an alert's reading: a number, as a
// percentage ("95%", "91.2%") or an exit code ("exit 137"). An alert's own
// message is free text that can carry a container's name, a file's path or a
// line of a log, and none of that is ever sent; the host builds the reading
// from the number alone and this refuses anything else, at every scope.
var readingPattern = regexp.MustCompile(`^(exit )?[0-9]{1,3}(\.[0-9]{1,2})?%?$`)

// Redact removes what a scope does not share. Below operate nothing that names
// a thing on the machine leaves it: no container, stack, backup job or project
// name, and no alert subject that is one. The counts stay. At every scope an
// alert's reading is kept only if it is a number.
func (s Snapshot) Redact(scope Scope) Snapshot {
	if s.Alerts != nil {
		alerts := make([]AlertInfo, len(s.Alerts))
		for i, a := range s.Alerts {
			if !readingPattern.MatchString(a.Reading) {
				a.Reading = ""
			}
			alerts[i] = a
		}
		s.Alerts = alerts
	}
	if scope.Includes(ScopeOperate) {
		return s
	}
	if s.Containers != nil {
		c := *s.Containers
		c.List = nil
		s.Containers = &c
	}
	s.Stacks = nil
	if s.Backups != nil {
		b := *s.Backups
		b.List = nil
		s.Backups = &b
	}
	if s.Deployments != nil {
		d := *s.Deployments
		if d.LastRun != nil {
			run := *d.LastRun
			run.Project = ""
			d.LastRun = &run
		}
		s.Deployments = &d
	}
	for i, a := range s.Alerts {
		if !measurementAlerts[a.Kind] {
			s.Alerts[i].Subject, s.Alerts[i].Reading = "", ""
		}
	}
	return s
}

// The site stores at most this many of each list; sending more only costs the
// request body, which the protocol caps at 64 KiB.
const (
	maxContainers = 200
	maxStacks     = 100
	maxBackups    = 100
	maxAlerts     = 50
)

// encode renders the snapshot, keeping the lists inside the site's limits and
// the body inside the protocol's. If it is still too large the lists are cut
// back until it fits: the counts carry the information, the lists are detail.
func (s Snapshot) encode() ([]byte, error) {
	if s.Containers != nil && len(s.Containers.List) > maxContainers {
		c := *s.Containers
		c.List = c.List[:maxContainers]
		s.Containers = &c
	}
	if len(s.Stacks) > maxStacks {
		s.Stacks = s.Stacks[:maxStacks]
	}
	if s.Backups != nil && len(s.Backups.List) > maxBackups {
		b := *s.Backups
		b.List = b.List[:maxBackups]
		s.Backups = &b
	}
	if len(s.Alerts) > maxAlerts {
		s.Alerts = s.Alerts[:maxAlerts]
	}
	const budget = maxRequestBody - 1024
	for {
		body, err := json.Marshal(s)
		if err != nil || len(body) <= budget {
			return body, err
		}
		switch {
		case s.Containers != nil && len(s.Containers.List) > 0:
			c := *s.Containers
			c.List = c.List[:len(c.List)/2]
			s.Containers = &c
		case s.Backups != nil && len(s.Backups.List) > 0:
			b := *s.Backups
			b.List = b.List[:len(b.List)/2]
			s.Backups = &b
		case len(s.Stacks) > 0:
			s.Stacks = s.Stacks[:len(s.Stacks)/2]
		case len(s.Alerts) > 0:
			s.Alerts = s.Alerts[:len(s.Alerts)/2]
		default:
			return body, nil
		}
	}
}
