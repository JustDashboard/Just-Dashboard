package procs

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

// A startup plan is private restart authority. Deploy stores it encrypted, never
// in discovery, public draft JSON, audit payloads or a manager-wide dump backup.
type NativeStartupPlan struct {
	Version         int                   `json:"version"`
	Manager         string                `json:"manager"`
	ResourceID      string                `json:"resourceId"`
	Account         string                `json:"account"`
	UID             uint32                `json:"uid"`
	GID             uint32                `json:"gid"`
	Digest          string                `json:"digest"`
	StartupEvidence json.RawMessage       `json:"startupEvidence"`
	HandledBlockers []string              `json:"handledBlockers"`
	Actions         []NativeStartupAction `json:"actions"`
}

type NativeStartupAction struct {
	Kind              string               `json:"kind"`
	Path              string               `json:"path"`
	Fingerprint       string               `json:"fingerprint"`
	UID               uint32               `json:"uid"`
	GID               uint32               `json:"gid"`
	Mode              uint32               `json:"mode"`
	ParentFingerprint string               `json:"parentFingerprint"`
	Name              string               `json:"name,omitempty"`
	Namespace         string               `json:"namespace,omitempty"`
	Selected          []NativeStartupEntry `json:"selected,omitempty"`
	Target            string               `json:"target,omitempty"`
	Authority         string               `json:"authority,omitempty"`
	Relation          string               `json:"relation,omitempty"`
}

type NativeStartupEntry struct {
	Index int             `json:"index"`
	Row   json.RawMessage `json:"row"`
}

// The journal must be durably saved before a side effect and after its observed
// outcome. PendingPath lives beside the original authority, under its ownership.
// It makes an interrupted atomic swap recoverable without persisting shared
// application configuration in the deployment database.
type NativeStartupJournal struct {
	Version    int                          `json:"version"`
	PlanDigest string                       `json:"planDigest"`
	Phase      string                       `json:"phase"`
	Actions    []NativeStartupActionJournal `json:"actions"`
}

type NativeStartupActionJournal struct {
	Phase               string `json:"phase"`
	BeforeFingerprint   string `json:"beforeFingerprint"`
	RetiredFingerprint  string `json:"retiredFingerprint,omitempty"`
	RestoredFingerprint string `json:"restoredFingerprint,omitempty"`
	PendingPath         string `json:"pendingPath,omitempty"`
}

type NativeStartupSummary struct {
	Version     int    `json:"version"`
	Manager     string `json:"manager"`
	Digest      string `json:"digest"`
	ActionCount int    `json:"actionCount"`
	Status      string `json:"status"`
	Description string `json:"description"`
}

func (p *NativeStartupPlan) Summary() NativeStartupSummary {
	count := 0
	for _, action := range p.Actions {
		if action.Kind == "systemd_link" || len(action.Selected) > 0 {
			count++
		}
	}
	description := "The original startup authority is already retired."
	if count > 0 {
		description = "Deploy changes retires only this application's verified startup entries; rollback restores them."
	}
	return NativeStartupSummary{Version: p.Version, Manager: p.Manager, Digest: p.Digest, ActionCount: count, Status: "prepared", Description: description}
}

func (p *NativeStartupPlan) sealDigest() {
	copy := *p
	copy.Digest = ""
	data, _ := json.Marshal(copy)
	p.Digest = captureDigest(data)
}
func (p *NativeStartupPlan) validate() error {
	if p == nil || p.Version != 1 || (p.Manager != "pm2" && p.Manager != "systemd") || len(p.Actions) > 256 {
		return fmt.Errorf("startup handoff plan is unavailable")
	}
	copy := *p
	copy.sealDigest()
	if p.Digest == "" || copy.Digest != p.Digest {
		return ErrHostWorkloadChanged
	}
	paths := map[string]bool{}
	for _, action := range p.Actions {
		if !filepath.IsAbs(action.Path) || filepath.Clean(action.Path) != action.Path || strings.ContainsAny(action.Path, "\x00\r\n") || paths[action.Path] || action.Fingerprint == "" || action.ParentFingerprint == "" {
			return ErrHostWorkloadChanged
		}
		paths[action.Path] = true
		if (p.Manager == "pm2" && action.Kind != "pm2_file" && action.Kind != "absent") || (p.Manager == "systemd" && action.Kind != "systemd_link") {
			return ErrHostWorkloadChanged
		}
	}
	return nil
}

func initializeStartupJournal(plan *NativeStartupPlan, journal *NativeStartupJournal) error {
	if err := plan.validate(); err != nil {
		return err
	}
	if journal == nil {
		return fmt.Errorf("durable startup journal is required")
	}
	if journal.PlanDigest == "" {
		*journal = NativeStartupJournal{Version: 1, PlanDigest: plan.Digest, Phase: "prepared", Actions: make([]NativeStartupActionJournal, len(plan.Actions))}
		for i, action := range plan.Actions {
			journal.Actions[i] = NativeStartupActionJournal{Phase: "prepared", BeforeFingerprint: action.Fingerprint}
		}
	}
	if journal.Version != 1 || journal.PlanDigest != plan.Digest || len(journal.Actions) != len(plan.Actions) {
		return ErrHostWorkloadChanged
	}
	for i, action := range plan.Actions {
		if journal.Actions[i].BeforeFingerprint != action.Fingerprint {
			return ErrHostWorkloadChanged
		}
	}
	return nil
}

func RetireStartupHandoff(ctx context.Context, plan *NativeStartupPlan, journal *NativeStartupJournal, persist func(NativeStartupJournal) error) error {
	return executeStartupHandoff(ctx, plan, journal, persist, false)
}
func RestoreStartupHandoff(ctx context.Context, plan *NativeStartupPlan, journal *NativeStartupJournal, persist func(NativeStartupJournal) error) error {
	return executeStartupHandoff(ctx, plan, journal, persist, true)
}

func VerifyStartupHandoff(ctx context.Context, plan *NativeStartupPlan, journal NativeStartupJournal) error {
	if err := initializeStartupJournal(plan, &journal); err != nil {
		return err
	}
	for i, action := range plan.Actions {
		if err := ctx.Err(); err != nil {
			return err
		}
		current, err := readStartupAuthority(action.Path, action.Kind)
		if err != nil || current.ParentFingerprint != action.ParentFingerprint {
			return ErrHostWorkloadChanged
		}
		state := journal.Actions[i]
		expected := state.BeforeFingerprint
		switch state.Phase {
		case "retired":
			expected = state.RetiredFingerprint
		case "restored":
			expected = state.RestoredFingerprint
		case "retiring", "restoring":
			return fmt.Errorf("startup handoff is interrupted; resume its durable journal first")
		case "prepared":
		default:
			return ErrHostWorkloadChanged
		}
		if current.Fingerprint != expected {
			return ErrHostWorkloadChanged
		}
	}
	return nil
}

// Runtime normalization alone cannot authorize a changed launcher. The exact
// startup evidence and file/link journal must independently match the prepared
// authority or the small, positively attributable retirement transition.
func VerifyCapturedStartup(capture *HostWorkloadCapture, plan *NativeStartupPlan, journal NativeStartupJournal) error {
	if plan == nil || capture == nil || capture.Manager != plan.Manager || capture.ResourceID != plan.ResourceID {
		return ErrHostWorkloadChanged
	}
	if err := VerifyStartupHandoff(context.Background(), plan, journal); err != nil {
		return err
	}
	if equalStartupJSON(capture.StartupEvidence, plan.StartupEvidence) {
		return nil
	}
	retired := journal.Phase == "retired"
	if !retired {
		return ErrHostWorkloadChanged
	}
	if plan.Manager == "pm2" {
		var evidence map[string]json.RawMessage
		if json.Unmarshal(capture.StartupEvidence, &evidence) != nil || len(evidence) != 2 {
			return ErrHostWorkloadChanged
		}
		for _, name := range []string{"dump.pm2", "dump.pm2.bak"} {
			var rows []json.RawMessage
			if json.Unmarshal(evidence[name], &rows) != nil || len(rows) != 0 {
				return ErrHostWorkloadChanged
			}
		}
		return nil
	}
	return verifyRetiredSystemdEvidence(capture.StartupEvidence, plan)
}

func equalStartupJSON(a, b json.RawMessage) bool {
	var left, right any
	if json.Unmarshal(a, &left) != nil || json.Unmarshal(b, &right) != nil {
		return false
	}
	l, _ := json.Marshal(left)
	r, _ := json.Marshal(right)
	return string(l) == string(r)
}
