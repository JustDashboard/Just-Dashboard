package netx

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type SQMView struct {
	SQMProfile
	IFB    string             `json:"ifb"`
	Queue  *QdiscStat         `json:"queue"`
	Helper *ShapeVerification `json:"helper"`
	Boot   *ShapeVerification `json:"boot"`
}

func hasSQM(sp *Spec) bool {
	for _, sh := range sp.Shaping {
		if sh.SQM != nil {
			return true
		}
	}
	return false
}

func (s *Service) verifySQMBoot(ctx context.Context) error {
	out, err := run(ctx, "systemctl", "show", UnitName, "--property=FragmentPath", "--property=DropInPaths", "--property=ExecStart")
	if err != nil {
		return fmt.Errorf("reading loaded SQM boot dependency: %w", err)
	}
	properties := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		key, value, found := strings.Cut(line, "=")
		if found {
			properties[key] = value
		}
	}
	if properties["FragmentPath"] != s.paths.Unit || properties["DropInPaths"] != "" {
		return shapingDrift("the loaded SQM boot unit is missing, replaced or has foreign drop-ins")
	}
	argv := "argv[]=" + filepath.Join(s.paths.Dir, recoveryBinary) + " --network-sqm-restore " + s.paths.Dir + " ;"
	if strings.Count(properties["ExecStart"], argv) != 1 || strings.Contains(properties["ExecStart"], argv+" ignore_errors=yes") {
		return shapingDrift("the loaded boot unit does not contain the required SQM restoration command")
	}
	enabled, err := run(ctx, "systemctl", "is-enabled", UnitName)
	if err != nil || strings.TrimSpace(enabled) != "enabled" {
		return shapingDrift("the SQM restoration boot unit is not verified enabled")
	}
	return nil
}

func sqmHealth(read func() error) *ShapeVerification {
	v := &ShapeVerification{Status: "verified", CheckedAt: time.Now().UTC()}
	if err := read(); err != nil {
		v.Status, v.Reason = "unknown", err.Error()
		if errors.Is(err, errShapingDrift) {
			v.Status = "drift"
		}
	}
	return v
}

func (s *Service) sqmView(ctx context.Context, sh ShapeSpec, queues []tcQdisc) *SQMView {
	v := &SQMView{SQMProfile: sh.SQM.SQMProfile, IFB: sh.SQM.IFB}
	for _, q := range queues {
		if q.Root {
			v.Queue = qdiscStat(q)
		}
	}
	v.Helper = sqmHealth(func() error {
		path := filepath.Join(s.paths.Dir, recoveryBinary)
		info, err := os.Stat(path)
		if errors.Is(err, fs.ErrNotExist) {
			return shapingDrift("the packaged SQM host helper is missing")
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Mode().Perm() != 0o700 {
			return shapingDrift("the packaged SQM helper permissions differ")
		}
		out, err := run(ctx, path, "--network-sqm-check")
		if err != nil {
			return err
		}
		if out != "sqm-v1\n" {
			return shapingDrift("the packaged host helper does not support SQM")
		}
		return nil
	})
	v.Boot = sqmHealth(func() error { return s.verifySQMBoot(ctx) })
	return v
}
