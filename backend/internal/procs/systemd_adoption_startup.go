package procs

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

type installedStartupUnit struct {
	Name  string `json:"unit_file"`
	State string `json:"state"`
}

type systemdStartupRunner func(context.Context, time.Duration, string, ...string) (*CommandResult, error)

func captureInstalledSystemdStartup(ctx context.Context, execute systemdStartupRunner, name string, names []string) (json.RawMessage, []string, error) {
	list := func() ([]installedStartupUnit, error) {
		result, err := execute(ctx, 20*time.Second, "systemctl", "list-unit-files", "--type=timer,path,socket,target,service", "--no-pager", "--no-legend", "--output=json")
		if err != nil || result == nil || len(result.Stdout) > 4<<20 {
			return nil, fmt.Errorf("unverified startup inventory")
		}
		var rows []installedStartupUnit
		if json.Unmarshal([]byte(result.Stdout), &rows) != nil || rows == nil || len(rows) > 10000 {
			return nil, fmt.Errorf("unverified startup inventory")
		}
		units := []installedStartupUnit{}
		for _, row := range rows {
			if ValidateName(row.Name) != nil || row.State == "" {
				return nil, fmt.Errorf("unverified startup inventory")
			}
			if row.State == "masked" || row.State == "masked-runtime" {
				continue
			}
			units = append(units, row)
		}
		if len(units) > 512 {
			return nil, fmt.Errorf("startup inventory exceeds limit")
		}
		sort.Slice(units, func(i, j int) bool { return units[i].Name < units[j].Name })
		return units, nil
	}
	units, err := list()
	unverified := func() (json.RawMessage, []string, error) {
		return json.RawMessage(`{"unverified":true}`), []string{"The installed systemd startup inventory cannot be verified within its safety limits. Review installed timers, paths, sockets, targets and aliases under a reversible startup handoff before container migration."}, nil
	}
	if err != nil {
		return unverified()
	}
	aliases := map[string]bool{name: true}
	for _, alias := range names {
		aliases[alias] = true
	}
	properties := map[string]map[string]string{}
	if len(units) > 0 {
		args := []string{"show", "--no-pager", "--property=Id,Names,LoadState,UnitFileState,FragmentPath,DropInPaths,Triggers,Unit,Service,Wants,Requires,BindsTo,Upholds,OnFailure,OnSuccess,NeedDaemonReload"}
		for _, unit := range units {
			if !strings.Contains(unit.Name, "@.") {
				args = append(args, unit.Name)
			}
		}
		if len(args) == 3 {
			args = append(args, name)
		}
		result, showErr := execute(ctx, 20*time.Second, "systemctl", args...)
		if showErr != nil || result == nil || len(result.Stdout) > 4<<20 {
			return unverified()
		}
		for _, block := range strings.Split(strings.TrimSpace(result.Stdout), "\n\n") {
			props := map[string]string{}
			for _, line := range strings.Split(block, "\n") {
				key, value, ok := strings.Cut(line, "=")
				if ok {
					props[key] = value
				}
			}
			if props["Id"] == "" || props["LoadState"] != "loaded" {
				return unverified()
			}
			for _, alias := range append(strings.Fields(props["Names"]), props["Id"]) {
				properties[alias] = props
			}
		}
	}
	for _, unit := range units {
		if !strings.Contains(unit.Name, "@.") {
			continue
		}
		result, catErr := execute(ctx, 20*time.Second, "systemctl", "cat", "--no-pager", unit.Name)
		if catErr != nil || result == nil || len(result.Stdout) > 4<<20 {
			return unverified()
		}
		props, parseErr := systemdTemplateStartupProperties(unit.Name, result.Stdout)
		if parseErr != nil {
			return unverified()
		}
		properties[unit.Name] = props
		if strings.HasSuffix(unit.Name, ".service") && strings.Contains(name, "@") {
			canonical := filepath.Base(props["FragmentPath"])
			prefix, _, _ := strings.Cut(canonical, "@.")
			if strings.HasPrefix(name, prefix+"@") {
				_, instance, _ := strings.Cut(name, "@")
				aliasPrefix, _, _ := strings.Cut(unit.Name, "@.")
				aliases[aliasPrefix+"@"+instance] = true
			}
		}
	}
	for _, unit := range units {
		props := properties[unit.Name]
		if props == nil {
			return unverified()
		}
		if strings.HasSuffix(unit.Name, ".service") && props["Id"] == name {
			aliases[unit.Name] = true
			for _, alias := range strings.Fields(props["Names"]) {
				aliases[alias] = true
			}
		}
	}
	proof := map[string]any{}
	blockers := []string{}
	total := 0
	for _, unit := range units {
		props := properties[unit.Name]
		if props["NeedDaemonReload"] == "yes" {
			return unverified()
		}
		files := map[string]string{}
		for _, filename := range append([]string{props["FragmentPath"]}, strings.Fields(props["DropInPaths"])...) {
			if !filepath.IsAbs(filename) || strings.ContainsAny(filename, "\\\x00\n\r") {
				return unverified()
			}
			content, fileErr := readSystemdStartupFile(hostexec.HostPath(filename))
			if fileErr != nil {
				return unverified()
			}
			total += len(content)
			if total > 8<<20 {
				return unverified()
			}
			files[filename] = captureDigest(content)
		}
		matched := false
		if strings.Contains(unit.Name, "@.") && (unit.State == "enabled" || unit.State == "enabled-runtime") {
			for _, field := range []string{"Unit", "Service", "Wants", "Requires", "BindsTo", "Upholds", "OnFailure", "OnSuccess"} {
				for _, reference := range strings.Fields(props[field]) {
					if strings.Contains(reference, "%") && (strings.HasSuffix(reference, ".service") || strings.HasSuffix(reference, ".target")) {
						return unverified()
					}
				}
			}
		}
		for _, field := range []string{"Triggers", "Unit", "Service", "Wants", "Requires", "BindsTo", "Upholds", "OnFailure", "OnSuccess"} {
			for _, reference := range strings.Fields(props[field]) {
				matched = matched || systemdStartupReferenceMatches(reference, aliases)
			}
		}
		if strings.HasSuffix(unit.Name, ".timer") || strings.HasSuffix(unit.Name, ".path") || strings.HasSuffix(unit.Name, ".socket") {
			// Default triggers use the same basename when no explicit Unit or Service is present.
			base := strings.TrimSuffix(unit.Name, filepath.Ext(unit.Name)) + ".service"
			if props["Unit"] == "" && props["Service"] == "" && props["Triggers"] == "" {
				matched = matched || systemdStartupReferenceMatches(base, aliases)
			}
		}
		if matched {
			proof[unit.Name] = struct {
				State      string
				Properties map[string]string
				Files      map[string]string
			}{unit.State, props, files}
			blockers = append(blockers, "An installed systemd service, timer, path, socket or target can activate this application, including startup units not previously loaded. Review and retire that external startup authority under a reversible handoff before container migration.")
		}
	}
	after, err := list()
	beforeRaw, _ := json.Marshal(units)
	afterRaw, _ := json.Marshal(after)
	if err != nil || !bytes.Equal(beforeRaw, afterRaw) {
		return nil, nil, ErrHostWorkloadChanged
	}
	raw, _ := json.Marshal(proof)
	return raw, uniqueCaptureStrings(blockers), nil
}

func readSystemdStartupFile(path string) ([]byte, error) {
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || before.Mode().Perm()&0444 == 0 || before.Size() > 4<<20 {
		return nil, fmt.Errorf("startup file is unverified")
	}
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
		return nil, ErrHostWorkloadChanged
	}
	content, err := io.ReadAll(io.LimitReader(file, (4<<20)+1))
	if err != nil || len(content) > 4<<20 {
		return nil, fmt.Errorf("startup file is unverified")
	}
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return nil, ErrHostWorkloadChanged
	}
	return content, nil
}

// ControlCaptured rechecks installed startup authority immediately before the
// manager action. It never changes global enablement or daemon configuration.
func (s *Systemd) ControlCaptured(ctx context.Context, captured *HostWorkloadCapture, action UnitAction) (*CommandResult, error) {
	if action != UnitStart && action != UnitStop {
		return nil, fmt.Errorf("unsupported original systemd action")
	}
	if err := s.VerifyCaptured(ctx, captured); err != nil {
		return nil, err
	}
	return s.Control(ctx, captured.ResourceID, action)
}

// VerifyCaptured fences exact manager configuration, installed startup evidence
// and PID identity for an authoritative controller using the captured unit.
func (s *Systemd) VerifyCaptured(ctx context.Context, captured *HostWorkloadCapture) error {
	if captured == nil || captured.Manager != "systemd" {
		return fmt.Errorf("unsupported original systemd identity")
	}
	fresh, err := s.CaptureExisting(ctx, captured.ResourceID)
	if err != nil || fresh.ConfigurationDigest != captured.ConfigurationDigest {
		return ErrHostWorkloadChanged
	}
	if len(fresh.Processes) != len(captured.Processes) {
		return ErrHostWorkloadChanged
	}
	for index, process := range fresh.Processes {
		if process.PID != captured.Processes[index].PID || process.CreateTime != captured.Processes[index].CreateTime {
			return ErrHostWorkloadChanged
		}
	}
	return nil
}

func systemdTemplateStartupProperties(name, content string) (map[string]string, error) {
	props := map[string]string{"Id": name, "Names": name, "LoadState": "loaded"}
	paths := []string{}
	section, pending := "", ""
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(line, "# /") {
			paths = append(paths, strings.TrimPrefix(line, "# "))
			continue
		}
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if pending != "" {
			line = pending + " " + line
			pending = ""
		}
		if strings.HasSuffix(line, "\\") {
			pending = strings.TrimSuffix(line, "\\")
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = line[1 : len(line)-1]
			continue
		}
		field, value, ok := strings.Cut(line, "=")
		field, value = strings.TrimSpace(field), strings.TrimSpace(value)
		if !ok {
			return nil, fmt.Errorf("ambiguous template startup authority")
		}
		if (section == "Unit" && (field == "Wants" || field == "Requires" || field == "BindsTo" || field == "Upholds" || field == "OnFailure" || field == "OnSuccess")) || ((section == "Timer" || section == "Path") && field == "Unit") || (section == "Socket" && field == "Service") {
			if strings.ContainsAny(value, "\\\"'") {
				return nil, fmt.Errorf("ambiguous template startup authority")
			}
			if value == "" {
				props[field] = ""
			} else {
				props[field] = strings.TrimSpace(props[field] + " " + value)
			}
		}
	}
	if len(paths) == 0 || pending != "" {
		return nil, fmt.Errorf("ambiguous template startup authority")
	}
	props["FragmentPath"] = paths[0]
	props["DropInPaths"] = strings.Join(paths[1:], " ")
	return props, nil
}

func systemdStartupReferenceMatches(reference string, aliases map[string]bool) bool {
	if aliases[reference] {
		return true
	}
	if strings.Contains(reference, "@.") {
		prefix, suffix, _ := strings.Cut(reference, "@.")
		for alias := range aliases {
			if strings.HasPrefix(alias, prefix+"@") && strings.HasSuffix(alias, "."+suffix) {
				return true
			}
		}
	}
	return false
}
