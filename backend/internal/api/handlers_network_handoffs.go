package api

import (
	"fmt"
	"net/http"
	"path/filepath"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netsec"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netx"
	"github.com/go-chi/chi/v5"
)

// What follows a package install on the Network and Security pages.
//
// An install job succeeding means the package manager put files on disk. It
// does not mean the software is configured, running or doing its job, and a
// page that refreshes into "installed" lets the reader believe all three. So
// each package the hand-offs offer is read again as four phases — installed,
// configured, active, verified — each from the module that already reads
// that software, each with the sentence that says what it found. A phase
// that does not apply to a tool says so rather than passing.

// HandoffPhase is one step from a package on disk to a working service.
type HandoffPhase struct {
	// Key is installed, configured, active or verified.
	Key string `json:"key"`
	// Status is done, pending (not yet: something to do), failed, unknown or
	// not_applicable.
	Status string `json:"status"`
	Detail string `json:"detail"`
}

// PackageHandoff is a package's phases at one read.
type PackageHandoff struct {
	Package   string         `json:"package"`
	CheckedAt time.Time      `json:"checkedAt"`
	Phases    []HandoffPhase `json:"phases"`
	// Working is true only when every applicable phase is done.
	Working bool `json:"working"`
}

func handoff(pkg string, phases ...HandoffPhase) PackageHandoff {
	h := PackageHandoff{Package: pkg, CheckedAt: time.Now().UTC(), Phases: phases, Working: true}
	for _, p := range phases {
		if p.Status != "done" && p.Status != "not_applicable" {
			h.Working = false
		}
	}
	return h
}

func phase(key, status, detail string) HandoffPhase {
	return HandoffPhase{Key: key, Status: status, Detail: detail}
}

// wireguardHandoff reads the WireGuard view: the tools, the kernel's support,
// a tunnel configured, one up.
func wireguardHandoff(v *netx.WireGuardView) PackageHandoff {
	if !v.Installed {
		missing := "wg and wg-quick are not on the host"
		if v.Tools.Wg != v.Tools.WgQuick {
			missing = "only one of wg and wg-quick is on the host"
		}
		return handoff("wireguard-tools", phase("installed", "pending", missing),
			phase("configured", "pending", "no tunnel can be configured until the tools are in"),
			phase("active", "pending", "nothing is running"),
			phase("verified", "pending", "nothing to verify yet"))
	}
	configured, up := 0, 0
	for _, ifc := range v.Interfaces {
		if ifc.Configured {
			configured++
		}
		if ifc.Up {
			up++
		}
	}
	cfg := phase("configured", "pending", "no tunnel is configured yet; create one below")
	if configured > 0 {
		cfg = phase("configured", "done", fmt.Sprintf("%d tunnel file(s) in the WireGuard directory", configured))
	}
	active := phase("active", "pending", "no WireGuard interface is up")
	if up > 0 {
		active = phase("active", "done", fmt.Sprintf("%d WireGuard interface(s) up in the kernel", up))
	}
	verified := phase("verified", "done", "wg reads the kernel's WireGuard state, and the kernel has the WireGuard module")
	switch {
	case !v.Kernel:
		verified = phase("verified", "failed", "the tools are installed but this kernel has no WireGuard module to load")
	case v.Error != "":
		verified = phase("verified", "failed", "wg could not read the interfaces: "+v.Error)
	}
	return handoff("wireguard-tools", phase("installed", "done", "wg and wg-quick are on the host"), cfg, active, verified)
}

// bpftoolHandoff reads the eBPF inventory: bpftool present and allowed to
// list what the kernel has loaded.
func bpftoolHandoff(v *netx.EBPFView) PackageHandoff {
	if !v.Installed {
		return handoff("bpftool", phase("installed", "pending", "bpftool is not on the host"),
			phase("configured", "not_applicable", "bpftool has no configuration"),
			phase("active", "not_applicable", "bpftool is a command, not a service"),
			phase("verified", "pending", "nothing to verify yet"))
	}
	verified := phase("verified", "done", fmt.Sprintf("bpftool listed %d loaded program(s)", v.Total))
	if v.Error != "" {
		verified = phase("verified", "failed", v.Error)
	}
	return handoff("bpftool", phase("installed", "done", "bpftool is on the host"),
		phase("configured", "not_applicable", "bpftool has no configuration"),
		phase("active", "not_applicable", "bpftool is a command, not a service"), verified)
}

// crowdsecHandoff reads CrowdSec: the engine answering, running, and at least
// one bouncer pulling its decisions — without one a decision blocks nothing.
func crowdsecHandoff(v *netsec.CrowdSecView) PackageHandoff {
	if !v.Installed {
		return handoff("crowdsec", phase("installed", "pending", "cscli is not on the host"),
			phase("configured", "pending", "nothing to configure yet"),
			phase("active", "pending", "nothing is running"),
			phase("verified", "pending", "nothing to verify yet"))
	}
	cfg := phase("configured", "done", "cscli reads the engine's decisions and bouncers")
	if v.Error != "" {
		cfg = phase("configured", "failed", "cscli could not answer: "+v.Error)
	}
	active := phase("active", "done", "the crowdsec service is running")
	if !v.Active {
		active = phase("active", "pending", "the crowdsec service is not running")
	}
	valid := 0
	for _, b := range v.Bouncers {
		if b.Valid {
			valid++
		}
	}
	verified := phase("verified", "done", fmt.Sprintf("%d bouncer(s) pull decisions and enforce them", valid))
	switch {
	case !v.Active || v.Error != "":
		verified = phase("verified", "pending", "a running engine is verified by its bouncers")
	case valid == 0:
		verified = phase("verified", "pending", "no bouncer pulls the decisions, so they block nothing; install a firewall bouncer")
	}
	return handoff("crowdsec", phase("installed", "done", "cscli is on the host"), cfg, active, verified)
}

// suricataHandoff reads Suricata: rules loaded, the service running, and its
// event log readable, which is where its alerts arrive.
func suricataHandoff(v *netsec.SuricataView) PackageHandoff {
	if !v.Installed {
		return handoff("suricata", phase("installed", "pending", "suricata is not on the host"),
			phase("configured", "pending", "nothing to configure yet"),
			phase("active", "pending", "nothing is running"),
			phase("verified", "pending", "nothing to verify yet"))
	}
	cfg := phase("configured", "unknown", "the rule file could not be read, so how many rules are loaded is unknown")
	if v.RulesLoaded != nil {
		if *v.RulesLoaded > 0 {
			cfg = phase("configured", "done", fmt.Sprintf("%d enabled rule(s) in the rule file", *v.RulesLoaded))
		} else {
			cfg = phase("configured", "pending", "the rule file enables no rules; update them with suricata-update")
		}
	}
	active := phase("active", "done", "the suricata service is running in "+v.Mode+" mode")
	if !v.Active {
		active = phase("active", "pending", "the suricata service is not running")
	}
	verified := phase("verified", "done", "its event log "+v.LogPath+" is readable")
	switch {
	case !v.Active:
		verified = phase("verified", "pending", "a running engine is verified by its event log")
	case v.LogRefused != "":
		verified = phase("verified", "unknown", v.LogRefused)
	case v.LogError != "":
		verified = phase("verified", "failed", v.LogError)
	}
	return handoff("suricata", phase("installed", "done", "suricata is on the host"), cfg, active, verified)
}

// handoffPackages are the packages the hand-offs offer.
var handoffPackages = map[string]bool{"wireguard-tools": true, "bpftool": true, "crowdsec": true, "suricata": true}

func (s *Server) handleNetworkHandoff(w http.ResponseWriter, r *http.Request) error {
	pkg := chi.URLParam(r, "package")
	if !handoffPackages[pkg] {
		return httpx.Err(http.StatusNotFound, "not_found", "no hand-off reads "+pkg)
	}
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	var out PackageHandoff
	switch pkg {
	case "wireguard-tools":
		v, err := s.modules.network.WireGuard(ctx)
		if err != nil {
			return mapNetworkError(err)
		}
		out = wireguardHandoff(v)
	case "bpftool":
		v, err := s.modules.network.EBPF(ctx)
		if err != nil {
			return mapNetworkError(err)
		}
		out = bpftoolHandoff(v)
	case "crowdsec":
		v, err := s.modules.netsec.CrowdSec(ctx)
		if err != nil {
			return httpx.Internal(err)
		}
		out = crowdsecHandoff(v)
	case "suricata":
		v, err := s.modules.netsec.Suricata(ctx, func(path string) (string, error) {
			if err := s.modules.logs.Allow(path); err != nil {
				return "", err
			}
			if resolved, err := filepath.EvalSymlinks(path); err == nil {
				return resolved, nil
			}
			return path, nil
		})
		if err != nil {
			return httpx.Internal(err)
		}
		// The phases say whether its log is readable, never what is in it.
		out = suricataHandoff(v)
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}
