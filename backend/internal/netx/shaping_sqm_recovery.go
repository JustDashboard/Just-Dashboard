package netx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

const maxSQMRecoveryBytes = 4096

type sqmRecoveryShape struct {
	Kbit int     `json:"kbit"`
	SQM  SQMSpec `json:"sqm"`
}

type sqmRecoveryPayload struct {
	Version int               `json:"version"`
	Device  string            `json:"device"`
	Before  *sqmRecoveryShape `json:"before,omitempty"`
	After   *sqmRecoveryShape `json:"after,omitempty"`
}

func sqmRecoveryEntry(sh *ShapeSpec) *sqmRecoveryShape {
	if sh == nil || sh.SQM == nil {
		return nil
	}
	return &sqmRecoveryShape{Kbit: sh.IngressKbit, SQM: *sh.SQM}
}

func (p sqmRecoveryPayload) shape(entry *sqmRecoveryShape) ShapeSpec {
	return ShapeSpec{Device: p.Device, IngressKbit: entry.Kbit, SQM: &entry.SQM}
}

// The payload is a closed description of a single owned resource, not argv.
// Validate all entries before a recovery journal can restore any file.
func validateSQMRecoveryArgs(args []string) (*sqmRecoveryPayload, error) {
	if len(args) != 1 || len(args[0]) == 0 || len(args[0]) > maxSQMRecoveryBytes {
		return nil, errors.New("invalid bounded SQM recovery payload")
	}
	var p sqmRecoveryPayload
	d := json.NewDecoder(bytes.NewBufferString(args[0]))
	d.DisallowUnknownFields()
	if err := d.Decode(&p); err != nil {
		return nil, fmt.Errorf("invalid SQM recovery payload: %w", err)
	}
	if d.Decode(new(any)) != io.EOF {
		return nil, errors.New("trailing SQM recovery data")
	}
	if p.Version != 1 || ValidIfName(p.Device) != nil || p.Before == nil && p.After == nil {
		return nil, errors.New("invalid SQM recovery version or source")
	}
	for _, entry := range []*sqmRecoveryShape{p.Before, p.After} {
		if entry == nil {
			continue
		}
		if entry.Kbit < 1 || entry.Kbit > maxShapeKbit || entry.SQM.IFB == p.Device {
			return nil, errors.New("invalid SQM recovery rate or interface relationship")
		}
		if err := validSQMIdentity(&entry.SQM); err != nil {
			return nil, err
		}
	}
	if p.Before != nil && p.After != nil {
		a, b := p.Before.SQM, p.After.SQM
		a.SQMProfile, b.SQMProfile = SQMProfile{}, SQMProfile{}
		if a != b {
			return nil, errors.New("SQM update cannot change saved resource identity")
		}
	}
	return &p, nil
}

func sqmRecoveryPlan(old, next *Spec) ([]recoveryCommand, error) {
	before := map[string]ShapeSpec{}
	for _, sh := range old.Shaping {
		before[sh.Device] = sh
	}
	after := map[string]ShapeSpec{}
	for _, sh := range next.Shaping {
		after[sh.Device] = sh
	}
	var commands []recoveryCommand
	keys := map[string]bool{}
	for device := range before {
		keys[device] = true
	}
	for device := range after {
		keys[device] = true
	}
	devices := make([]string, 0, len(keys))
	for device := range keys {
		devices = append(devices, device)
	}
	sort.Strings(devices)
	for _, device := range devices {
		b, a := before[device], after[device]
		if b.SQM == nil && a.SQM == nil || reflect.DeepEqual(b, a) {
			continue
		}
		payload := sqmRecoveryPayload{Version: 1, Device: device, Before: sqmRecoveryEntry(&b), After: sqmRecoveryEntry(&a)}
		data, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		args := []string{string(data)}
		if _, err := validateSQMRecoveryArgs(args); err != nil {
			return nil, err
		}
		commands = append(commands, recoveryCommand{Tool: "sqm", Args: args})
	}
	return commands, nil
}

func recoverSQM(ctx context.Context, args []string) error {
	p, err := validateSQMRecoveryArgs(args)
	if err != nil {
		return err
	}
	entry := p.After
	if entry == nil {
		entry = p.Before
	}
	sh := p.shape(entry)
	var alternatives []ShapeSpec
	if p.Before != nil {
		alternatives = append(alternatives, p.shape(p.Before))
	}
	if err := removeSQM(ctx, sh, true, alternatives...); err != nil {
		return err
	}
	if p.Before != nil {
		return applySQM(ctx, p.shape(p.Before), nil)
	}
	return nil
}

// prepareSQMHelper is admission for this explicit operation. Legacy shaping
// does not acquire a new helper dependency. A reconnect confirmation uses the
// existing owner/session protocol rather than an SQM-specific transaction.
func (s *Service) prepareSQMHelper(ctx context.Context) error {
	if !s.independentRecovery || !has("systemctl") || !has("systemd-run") {
		return &ConfirmationError{"Download SQM requires independent host recovery; nothing was applied."}
	}
	if pendingOwner(ctx) == 0 {
		return &ConfirmationError{"Download SQM requires pending apply and a successful reconnect confirmation; nothing was applied."}
	}
	if err := s.installRecoveryBinary(ctx); err != nil {
		return fmt.Errorf("provisioning SQM recovery before network effects: %w", err)
	}
	out, err := run(ctx, filepath.Join(s.paths.Dir, recoveryBinary), "--network-sqm-check")
	if err != nil || out != "sqm-v1\n" {
		return errors.New("the packaged host helper cannot verify SQM support; nothing was applied")
	}
	return nil
}

// RestoreSQMBoot restores only explicit saved SQM profiles. Existing resources
// require the saved nonce, MAC, exact queue and redirect; a name/alias pattern
// cannot authorize adoption. Errors fail the SQM boot step visibly.
func RestoreSQMBoot(ctx context.Context, dir string) error {
	if !filepath.IsAbs(dir) || filepath.Clean(dir) != dir {
		return errors.New("SQM boot restoration needs an absolute clean directory")
	}
	lock, err := lockChange(dir)
	if err != nil {
		return err
	}
	defer unlockChange(lock)
	f, err := os.Open(filepath.Join(dir, "spec.json"))
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxRecoveryJournalBytes {
		return errors.New("invalid bounded SQM boot spec")
	}
	var sp Spec
	if err := json.NewDecoder(io.LimitReader(f, maxRecoveryJournalBytes+1)).Decode(&sp); err != nil {
		return err
	}
	if sp.Version != specVersion || len(sp.Shaping) > 1024 {
		return errors.New("invalid SQM boot spec version or entry count")
	}
	for _, sh := range sp.Shaping {
		if sh.SQM == nil {
			continue
		}
		if _, err := normShape(sh); err != nil {
			return err
		}
		if err := validSQMIdentity(sh.SQM); err != nil {
			return err
		}
	}
	for _, sh := range sp.Shaping {
		if sh.SQM == nil {
			continue
		}
		link, err := readSQMLink(ctx, sh.SQM.IFB)
		if err != nil {
			return err
		}
		if link != nil {
			if err := verifySQM(ctx, sh); err != nil {
				return err
			}
			continue
		}
		filters, err := sqmReadFilters(ctx, sh.Device, "ingress")
		if err != nil {
			return err
		}
		if len(filters) != 0 {
			return fmt.Errorf("%s has foreign ingress filters at boot; SQM was not restored", sh.Device)
		}
		if err := applySQM(ctx, sh, nil); err != nil {
			return errors.Join(err, removeSQM(ctx, sh, true))
		}
	}
	return nil
}

func RestoreSQMBootStandalone(ctx context.Context, dir string) error {
	execute := recoveryExecutor(func(ctx context.Context, input []byte, name string, args ...string) (string, error) {
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, name, args...)
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		if input != nil {
			cmd.Stdin = bytes.NewReader(input)
		}
		_, err := hostexec.RunGroup(ctx, cmd, 200*time.Millisecond)
		if err != nil {
			return out.String(), fmt.Errorf("%s: %s: %w", name, firstLines(out.String(), 6), err)
		}
		return out.String(), nil
	})
	return RestoreSQMBoot(context.WithValue(ctx, recoveryExecutorKey{}, execute), dir)
}
