package procs

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"syscall"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

func capturePM2Startup(home pm2Home, namespace, name string) (json.RawMessage, []string) {
	evidence := map[string]any{}
	blockers := []string{}
	for _, filename := range []string{"dump.pm2", "dump.pm2.bak"} {
		selected, err := readPM2StartupEntries(hostexec.HostPath(filepath.Join(home.daemonDirectory(), filename)), namespace, name)
		if err != nil {
			evidence[filename] = map[string]bool{"unverified": true}
			blockers = append(blockers, "The saved PM2 startup list or backup cannot be verified safely. Review both saved lists and their original startup authority before container migration.")
			continue
		}
		evidence[filename] = selected
		if len(selected) > 0 {
			blockers = append(blockers, "A saved PM2 startup list or backup can restore this application after reboot. Retire its original startup authority under a reviewed handoff plan before container migration; importing never rewrites another application's saved list.")
		}
	}
	raw, _ := json.Marshal(evidence)
	return raw, uniqueCaptureStrings(blockers)
}

func readPM2StartupEntries(path, namespace, name string) ([]map[string]json.RawMessage, error) {
	selected := []map[string]json.RawMessage{}
	before, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return selected, nil
	}
	if err != nil || !before.Mode().IsRegular() || before.Mode().Perm()&0444 == 0 {
		return nil, fmt.Errorf("PM2 saved startup authority is unavailable")
	}
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("PM2 saved startup authority is unavailable")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
		return nil, ErrHostWorkloadChanged
	}
	content, err := io.ReadAll(io.LimitReader(file, (4<<20)+1))
	if err != nil || len(content) > 4<<20 {
		return nil, fmt.Errorf("PM2 saved startup authority is unavailable")
	}
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return nil, ErrHostWorkloadChanged
	}
	var rows []map[string]json.RawMessage
	if json.Unmarshal(content, &rows) != nil || rows == nil || len(rows) > 100000 {
		return nil, fmt.Errorf("PM2 saved startup authority is unverified")
	}
	for _, row := range rows {
		values := row
		if _, present := values["name"]; !present {
			var nested map[string]json.RawMessage
			if json.Unmarshal(row["pm2_env"], &nested) != nil || nested == nil {
				return nil, fmt.Errorf("PM2 saved startup authority is unverified")
			}
			values = nested
		}
		var savedName, savedNamespace string
		if json.Unmarshal(values["name"], &savedName) != nil || savedName == "" {
			return nil, fmt.Errorf("PM2 saved startup authority is unverified")
		}
		if raw, present := values["namespace"]; present && json.Unmarshal(raw, &savedNamespace) != nil {
			return nil, fmt.Errorf("PM2 saved startup authority is unverified")
		}
		if savedNamespace == "" {
			savedNamespace = "default"
		}
		currentNamespace := namespace
		if currentNamespace == "" {
			currentNamespace = "default"
		}
		if savedName == name && savedNamespace == currentNamespace {
			selected = append(selected, row)
		}
	}
	sort.Slice(selected, func(i, j int) bool {
		left, _ := json.Marshal(selected[i])
		right, _ := json.Marshal(selected[j])
		return string(left) < string(right)
	})
	return selected, nil
}
