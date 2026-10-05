package procs

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

const pm2SavedStartupBlocker = "A saved PM2 startup list or backup can restore this application after reboot. Retire its original startup authority under a reviewed handoff plan before container migration; importing never rewrites another application's saved list."
const systemdEnabledStartupBlocker = "The original systemd startup authority is not verifiably disabled. Enabled, static, alias, indirect, generated or unknown units can restart beside the Docker deployment after reboot; review a reversible startup handoff before migration."
const systemdReverseStartupBlocker = "Other systemd units can activate or control this application. Review its external startup authority and a reversible handoff before container migration."
const systemdInstalledStartupBlocker = "An installed systemd service, timer, path, socket or target can activate this application, including startup units not previously loaded. Review and retire that external startup authority under a reversible handoff before container migration."

func PreparePM2StartupHandoff(capture *HostWorkloadCapture, directory, namespace string) (*NativeStartupPlan, error) {
	if capture == nil || capture.Manager != "pm2" || !filepath.IsAbs(directory) || ValidateName(capture.Name) != nil {
		return nil, fmt.Errorf("PM2 startup authority is unverified")
	}
	if namespace == "" {
		namespace = "default"
	}
	plan := &NativeStartupPlan{Version: 1, Manager: capture.Manager, ResourceID: capture.ResourceID, Account: capture.Account, UID: capture.UID, GID: capture.GID, StartupEvidence: append(json.RawMessage(nil), capture.StartupEvidence...)}
	evidence := map[string]any{}
	for _, filename := range []string{"dump.pm2", "dump.pm2.bak"} {
		path := filepath.Join(directory, filename)
		current, err := readStartupAuthority(path, "pm2_file")
		if err != nil {
			return nil, err
		}
		action := NativeStartupAction{Kind: "absent", Path: path, Fingerprint: current.Fingerprint, UID: current.UID, GID: current.GID, Mode: current.Mode, ParentFingerprint: current.ParentFingerprint, Name: capture.Name, Namespace: namespace}
		selected := []map[string]json.RawMessage{}
		if !current.Absent {
			if current.UID != capture.UID || (current.ParentUID != capture.UID && current.ParentUID != 0) {
				return nil, fmt.Errorf("PM2 saved list belongs to a different account")
			}
			rows, err := parseStartupRows(current.Data)
			if err != nil {
				return nil, err
			}
			action.Kind = "pm2_file"
			for index, row := range rows {
				match, err := startupRowMatches(row, namespace, capture.Name)
				if err != nil {
					return nil, err
				}
				if !match {
					continue
				}
				var values map[string]json.RawMessage
				_ = json.Unmarshal(row, &values)
				if _, ok := values["name"]; !ok {
					_ = json.Unmarshal(values["pm2_env"], &values)
				}
				if captureString(values["pm_exec_path"]) != capture.SourcePath || captureString(values["pm_cwd"]) != capture.SourceDirectory {
					return nil, fmt.Errorf("PM2 saved startup entry differs from the captured source identity")
				}
				action.Selected = append(action.Selected, NativeStartupEntry{Index: index, Row: append(json.RawMessage(nil), row...)})
				var publicRow map[string]json.RawMessage
				_ = json.Unmarshal(row, &publicRow)
				selected = append(selected, publicRow)
			}
		}
		sort.Slice(selected, func(i, j int) bool {
			a, _ := json.Marshal(selected[i])
			b, _ := json.Marshal(selected[j])
			return string(a) < string(b)
		})
		evidence[filename] = selected
		plan.Actions = append(plan.Actions, action)
	}
	raw, _ := json.Marshal(evidence)
	if !equalStartupJSON(raw, capture.StartupEvidence) {
		return nil, ErrHostWorkloadChanged
	}
	for _, action := range plan.Actions {
		if len(action.Selected) > 0 {
			plan.HandledBlockers = []string{pm2SavedStartupBlocker}
			break
		}
	}
	plan.sealDigest()
	return plan, nil
}

type startupUnitProof struct {
	State      string
	Properties map[string]string
	Files      map[string]string
}

func PrepareSystemdStartupHandoff(capture *HostWorkloadCapture) (*NativeStartupPlan, error) {
	return prepareSystemdStartupHandoff(capture, []string{"/etc/systemd/system", "/run/systemd/system"})
}

func prepareSystemdStartupHandoff(capture *HostWorkloadCapture, roots []string) (*NativeStartupPlan, error) {
	return prepareSystemdStartupHandoffOwned(capture, roots, 0)
}

func prepareSystemdStartupHandoffOwned(capture *HostWorkloadCapture, roots []string, owner uint32) (*NativeStartupPlan, error) {
	if capture == nil || capture.Manager != "systemd" || ValidateName(capture.ResourceID) != nil || !strings.HasSuffix(capture.ResourceID, ".service") {
		return nil, fmt.Errorf("systemd startup authority is unverified")
	}
	var original struct {
		Properties map[string]string
		Files      map[string]string
	}
	if json.Unmarshal(capture.OriginalConfig, &original) != nil || original.Properties == nil {
		return nil, fmt.Errorf("systemd startup authority is unverified")
	}
	props := original.Properties
	state := props["UnitFileState"]
	if state != "enabled" && state != "enabled-runtime" && state != "disabled" {
		return nil, fmt.Errorf("systemd startup state cannot be retired by a direct-link handoff")
	}
	for _, field := range []string{"TriggeredBy", "BoundBy", "UpheldBy", "ConsistsOf", "OnFailureOf", "OnSuccessOf"} {
		if value := strings.TrimSpace(props[field]); value != "" && value != "[]" {
			return nil, fmt.Errorf("shared or indirect systemd startup authority requires review")
		}
	}
	names := capture.UnitNames
	if names == nil {
		names = strings.Fields(props["Names"])
	}
	if len(names) > 1 || (len(names) == 1 && names[0] != capture.ResourceID) {
		return nil, fmt.Errorf("systemd alias startup authority requires review")
	}
	plan := &NativeStartupPlan{Version: 1, Manager: capture.Manager, ResourceID: capture.ResourceID, Account: capture.Account, UID: capture.UID, GID: capture.GID, StartupEvidence: append(json.RawMessage(nil), capture.StartupEvidence...)}
	targets := map[string]map[string]bool{}
	for _, root := range roots {
		entries, err := os.ReadDir(hostexec.HostPath(root))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("systemd enablement directory is unavailable")
		}
		if len(entries) > 4096 {
			return nil, fmt.Errorf("systemd enablement inventory exceeds limit")
		}
		for _, entry := range entries {
			directory := entry.Name()
			relation := ""
			if strings.HasSuffix(directory, ".wants") {
				relation = "Wants"
			} else if strings.HasSuffix(directory, ".requires") {
				relation = "Requires"
			} else {
				continue
			}
			authority := strings.TrimSuffix(strings.TrimSuffix(directory, ".wants"), ".requires")
			if !strings.HasSuffix(authority, ".target") || ValidateName(authority) != nil {
				continue
			}
			path := filepath.Join(root, directory, capture.ResourceID)
			current, err := readStartupAuthority(path, "systemd_link")
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return nil, err
			}
			if current.Absent {
				continue
			}
			target := current.Target
			if !filepath.IsAbs(target) {
				target = filepath.Join(filepath.Dir(path), target)
			}
			if filepath.Clean(target) != filepath.Clean(props["FragmentPath"]) || current.UID != owner {
				return nil, fmt.Errorf("systemd direct enablement link has unverified authority")
			}
			plan.Actions = append(plan.Actions, NativeStartupAction{Kind: "systemd_link", Path: path, Fingerprint: current.Fingerprint, UID: current.UID, GID: current.GID, Mode: current.Mode, ParentFingerprint: current.ParentFingerprint, Target: current.Target, Authority: authority, Relation: relation})
			if targets[authority] == nil {
				targets[authority] = map[string]bool{}
			}
			targets[authority][relation] = true
		}
	}
	if len(plan.Actions) > 256 {
		return nil, fmt.Errorf("systemd enablement inventory exceeds limit")
	}
	for _, field := range []string{"WantedBy", "RequiredBy"} {
		relation := "Wants"
		if field == "RequiredBy" {
			relation = "Requires"
		}
		for _, target := range strings.Fields(props[field]) {
			if !targets[target][relation] {
				return nil, fmt.Errorf("systemd reverse startup relationship is not a verified direct link")
			}
		}
	}
	if state != "disabled" && len(plan.Actions) == 0 {
		return nil, fmt.Errorf("systemd enablement is not explained by verified direct links")
	}
	var evidence map[string]startupUnitProof
	if json.Unmarshal(capture.StartupEvidence, &evidence) != nil || evidence == nil {
		return nil, fmt.Errorf("systemd startup inventory is unverified")
	}
	for authority, proof := range evidence {
		relations := targets[authority]
		if len(relations) == 0 || !strings.HasSuffix(authority, ".target") {
			return nil, fmt.Errorf("shared systemd startup authority requires review")
		}
		attributed := false
		for _, field := range []string{"Triggers", "Unit", "Service", "Wants", "Requires", "BindsTo", "Upholds", "OnFailure", "OnSuccess"} {
			for _, reference := range strings.Fields(proof.Properties[field]) {
				if reference == capture.ResourceID {
					if !relations[field] {
						return nil, fmt.Errorf("systemd startup relationship is not attributable to a direct link")
					}
					attributed = true
				}
			}
		}
		if !attributed {
			return nil, fmt.Errorf("systemd startup relationship is unverified")
		}
		// An explicit dependency in the target's source would survive unlinking.
		for file, digest := range proof.Files {
			content, err := readSystemdStartupFile(hostexec.HostPath(file))
			if err != nil || captureDigest(content) != digest {
				return nil, ErrHostWorkloadChanged
			}
			if strings.Contains(string(content), capture.ResourceID) {
				return nil, fmt.Errorf("explicit target startup dependency requires a shared-authority review")
			}
		}
	}
	sort.Slice(plan.Actions, func(i, j int) bool { return plan.Actions[i].Path < plan.Actions[j].Path })
	if len(plan.Actions) > 0 {
		plan.HandledBlockers = []string{systemdEnabledStartupBlocker, systemdReverseStartupBlocker}
		if len(evidence) > 0 {
			plan.HandledBlockers = append(plan.HandledBlockers, systemdInstalledStartupBlocker)
		}
	}
	plan.sealDigest()
	return plan, nil
}

func verifyRetiredSystemdEvidence(current json.RawMessage, plan *NativeStartupPlan) error {
	var before, after map[string]startupUnitProof
	if json.Unmarshal(plan.StartupEvidence, &before) != nil || json.Unmarshal(current, &after) != nil || after == nil {
		return ErrHostWorkloadChanged
	}
	authorized := map[string]map[string]bool{}
	for _, action := range plan.Actions {
		if authorized[action.Authority] == nil {
			authorized[action.Authority] = map[string]bool{}
		}
		authorized[action.Authority][action.Relation] = true
	}
	for authority, proof := range after {
		expected, ok := before[authority]
		if !ok {
			return ErrHostWorkloadChanged
		}
		if authorized[authority] == nil {
			return ErrHostWorkloadChanged
		}
		expectedRaw, _ := json.Marshal(expected)
		currentRaw, _ := json.Marshal(proof)
		if equalStartupJSON(expectedRaw, currentRaw) {
			continue
		}
		normalized := expected
		normalized.Properties = map[string]string{}
		for key, value := range expected.Properties {
			normalized.Properties[key] = value
		}
		for relation := range authorized[authority] {
			references := []string{}
			for _, reference := range strings.Fields(normalized.Properties[relation]) {
				if reference != plan.ResourceID {
					references = append(references, reference)
				}
			}
			normalized.Properties[relation] = strings.Join(references, " ")
		}
		normalizedRaw, _ := json.Marshal(normalized)
		if !equalStartupJSON(normalizedRaw, currentRaw) {
			return ErrHostWorkloadChanged
		}
	}
	// Only positively attributed target rows may disappear after manager reload.
	for authority := range before {
		if _, exists := after[authority]; !exists && authorized[authority] == nil {
			return ErrHostWorkloadChanged
		}
	}
	return nil
}

func parseStartupRows(data []byte) ([]json.RawMessage, error) {
	var rows []json.RawMessage
	if json.Unmarshal(data, &rows) != nil || rows == nil || len(rows) > 100000 {
		return nil, fmt.Errorf("PM2 saved startup authority is unverified")
	}
	for _, row := range rows {
		if _, err := startupRowMatches(row, "", ""); err != nil {
			return nil, err
		}
	}
	return rows, nil
}
func startupRowMatches(row json.RawMessage, namespace, name string) (bool, error) {
	var values map[string]json.RawMessage
	if json.Unmarshal(row, &values) != nil || values == nil {
		return false, fmt.Errorf("PM2 startup row is unverified")
	}
	if _, present := values["name"]; !present {
		if json.Unmarshal(values["pm2_env"], &values) != nil || values == nil {
			return false, fmt.Errorf("PM2 startup row is unverified")
		}
	}
	var savedName, savedNamespace string
	if json.Unmarshal(values["name"], &savedName) != nil || savedName == "" {
		return false, fmt.Errorf("PM2 startup identity is unverified")
	}
	if raw, present := values["namespace"]; present && json.Unmarshal(raw, &savedNamespace) != nil {
		return false, fmt.Errorf("PM2 startup namespace is unverified")
	}
	if savedNamespace == "" {
		savedNamespace = "default"
	}
	if namespace == "" {
		namespace = "default"
	}
	return savedName == name && savedNamespace == namespace, nil
}
