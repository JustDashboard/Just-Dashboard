package netx

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

// DriftReport keeps desired state, file evidence, kernel evidence and execution
// evidence separate. A journal generation describes its candidate, including
// after rollback; it is not the generation currently installed on disk.
type DriftReport struct {
	CheckedAt           time.Time          `json:"checkedAt"`
	FinishedAt          time.Time          `json:"finishedAt"`
	Status              string             `json:"status"`
	Consistent          bool               `json:"consistent"`
	SavedGeneration     string             `json:"savedGeneration,omitempty"`
	CanonicalGeneration string             `json:"canonicalGeneration,omitempty"`
	Spec                DriftObservation   `json:"spec"`
	Change              *ChangeStatus      `json:"change,omitempty"`
	Journal             DriftObservation   `json:"journal"`
	Files               []DriftObservation `json:"files"`
	Runtime             []DriftObservation `json:"runtime"`
	Boot                BootHealth         `json:"boot"`
	Admission           AdmissionState     `json:"admission"`
	Blocklists          []BlocklistView    `json:"blocklists"`
	RepairPlan          OwnedRepairPlan    `json:"repairPlan"`
}

// Status is matching, missing, drift, conflict, unreadable, unknown or
// not_required. Ownership comes from the saved spec, not a guessed host owner.
type DriftObservation struct {
	ID           string            `json:"id"`
	Domain       string            `json:"domain"`
	Resource     string            `json:"resource"`
	Status       string            `json:"status"`
	Coverage     string            `json:"coverage"`
	Reason       string            `json:"reason,omitempty"`
	Expected     map[string]string `json:"expected,omitempty"`
	Observed     map[string]string `json:"observed,omitempty"`
	Owned        bool              `json:"owned"`
	Repairable   bool              `json:"repairable"`
	Dependencies []string          `json:"dependencies,omitempty"`
}

// OwnedRepairPlan is inspectable advice, not an executable command or an
// authorization. A later apply must reread evidence and use the existing
// capability, path guards, audit and durable recovery transaction.
type OwnedRepairPlan struct {
	Generation    string            `json:"generation,omitempty"`
	CreatedAt     time.Time         `json:"createdAt"`
	Status        string            `json:"status"`
	Executable    bool              `json:"executable"`
	Blockers      []string          `json:"blockers"`
	Items         []OwnedRepair     `json:"items"`
	Excluded      []RepairExclusion `json:"excluded"`
	Preconditions []string          `json:"preconditions"`
}

type OwnedRepair struct {
	ID            string   `json:"id"`
	ObservationID string   `json:"observationId"`
	Domain        string   `json:"domain"`
	Resource      string   `json:"resource"`
	Action        string   `json:"action"`
	Reason        string   `json:"reason"`
	Preconditions []string `json:"preconditions"`
	Dependencies  []string `json:"dependencies,omitempty"`
}

type RepairExclusion struct {
	ObservationID string `json:"observationId"`
	Reason        string `json:"reason"`
}

func observation(domain, resource string) DriftObservation {
	return DriftObservation{ID: domain + ":" + resource, Domain: domain, Resource: resource,
		Status: "unknown", Coverage: "configuration", Owned: true}
}

func digestBytes(data []byte) string {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

// Do not follow a replacement symlink or read an unbounded special file. The
// opened inode must be the regular file whose ownership we inspected.
func readDriftFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file")
	}
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !os.SameFile(info, opened) || !opened.Mode().IsRegular() {
		return nil, fmt.Errorf("file changed while opening")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxRecoveryJournalBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxRecoveryJournalBytes {
		return nil, fmt.Errorf("file exceeds inspection limit")
	}
	return data, nil
}

func fileObservation(path, domain string, expected []byte, expectedErr error) DriftObservation {
	o := observation(domain, path)
	data, err := readDriftFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		o.Status, o.Reason, o.Repairable = "missing", "Saved render is absent.", expectedErr == nil
		return o
	}
	if err != nil {
		o.Status, o.Reason = "unreadable", err.Error()
		return o
	}
	o.Observed = map[string]string{"sha256": digestBytes(data)}
	if !strings.HasPrefix(string(data), generatedHeader) {
		o.Status, o.Owned, o.Reason = "conflict", false, "The file does not carry the managed render header."
		return o
	}
	if expectedErr != nil {
		o.Reason = expectedErr.Error()
		return o
	}
	o.Expected = map[string]string{"sha256": digestBytes(expected)}
	o.Status = "matching"
	if digestBytes(data) != digestBytes(expected) {
		o.Status, o.Repairable, o.Reason = "drift", true, "The owned file differs from the saved configuration."
	}
	return o
}

func readDriftSpec(s *Service) (*Spec, DriftObservation, string) {
	o := observation("spec", s.specPath())
	data, err := readDriftFile(s.specPath())
	if errors.Is(err, fs.ErrNotExist) {
		o.Status, o.Reason = "missing", "No saved managed configuration exists."
		return nil, o, ""
	}
	if err != nil {
		o.Status, o.Reason = "unreadable", err.Error()
		return nil, o, ""
	}
	o.Observed = map[string]string{"sha256": digestBytes(data)}
	sp := emptySpec()
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(sp); err != nil {
		o.Status, o.Reason = "unreadable", err.Error()
		return nil, o, digestBytes(data)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		o.Status, o.Reason = "unreadable", "Saved configuration has trailing data."
		return nil, o, digestBytes(data)
	}
	if sp.Version != specVersion {
		o.Reason = "Saved configuration version is not supported by this inspector."
		return nil, o, digestBytes(data)
	}
	if sp.Sysctls == nil {
		sp.Sysctls = map[string]string{}
	}
	if sp.NextID < 1 {
		sp.NextID = 1
	}
	if _, err := bootRecoveryDependencies(sp); err != nil {
		o.Status, o.Reason = "unreadable", "Saved managed dependencies are invalid: "+err.Error()
		return nil, o, digestBytes(data)
	}
	for _, r := range sp.Routes {
		if r.Family != "inet" && r.Family != "inet6" {
			o.Status, o.Reason = "unreadable", "Saved route family is invalid."
			return nil, o, digestBytes(data)
		}
		if _, err := routeArgs(r); err != nil {
			o.Status, o.Reason = "unreadable", err.Error()
			return nil, o, digestBytes(data)
		}
	}
	for _, r := range sp.Rules {
		if r.Family != "inet" && r.Family != "inet6" {
			o.Status, o.Reason = "unreadable", "Saved policy rule family is invalid."
			return nil, o, digestBytes(data)
		}
		if _, err := ruleArgs(r); err != nil {
			o.Status, o.Reason = "unreadable", err.Error()
			return nil, o, digestBytes(data)
		}
	}
	for _, sh := range sp.Shaping {
		if _, err := normShape(sh); err != nil {
			o.Status, o.Reason = "unreadable", err.Error()
			return nil, o, digestBytes(data)
		}
	}
	for key := range sp.Sysctls {
		if !driftSysctlKey(key) {
			o.Status, o.Reason = "unreadable", "Saved kernel setting key is invalid."
			return nil, o, digestBytes(data)
		}
	}
	o.Status = "matching"
	return sp, o, digestBytes(data)
}

// Drift is read-only: it neither creates a lock file nor installs, enables,
// fetches or reconciles anything. A concurrent independent recovery is detected
// by rereading the spec and journal; observations are a time window, not a
// claim of an atomic kernel snapshot.
func (s *Service) Drift(ctx context.Context) DriftReport {
	s.mu.Lock()
	r := DriftReport{CheckedAt: time.Now().UTC(), Consistent: true, Status: "unknown",
		Files: []DriftObservation{}, Runtime: []DriftObservation{}, Blocklists: []BlocklistView{}}
	sp, specObservation, saved := readDriftSpec(s)
	r.Spec, r.SavedGeneration = specObservation, saved
	journalBefore, journalErr := readDriftFile(filepath.Join(s.paths.Dir, recoveryFile))
	r.Journal = observation("journal", recoveryFile)
	switch {
	case errors.Is(journalErr, fs.ErrNotExist):
		r.Journal.Status = "not_required"
	case journalErr != nil:
		r.Journal.Status, r.Journal.Reason = "unreadable", journalErr.Error()
	default:
		j, err := readChange(s.paths.Dir)
		if err != nil {
			r.Journal.Status, r.Journal.Reason = "unreadable", err.Error()
		} else if j != nil {
			copy := j.ChangeStatus
			r.Change, r.Journal.Status = &copy, "matching"
			r.Journal.Observed = map[string]string{"candidateGeneration": copy.Generation, "phase": copy.Phase}
		}
	}
	s.mu.Unlock()
	if sp != nil {
		canonical, _ := json.MarshalIndent(sp, "", "  ")
		r.CanonicalGeneration = digestBytes(append(canonical, '\n'))
		gateway, gatewayErr := s.driftGatewayRender(sp)
		files := rendered{
			filepath.Join(s.paths.Dir, linksFile):   []byte(renderLinks(sp)),
			filepath.Join(s.paths.Dir, rules6File):  []byte(renderIPv6Rules(sp)),
			filepath.Join(s.paths.Dir, shapingFile): []byte(renderShaping(sp)),
			filepath.Join(s.paths.Dir, gatewayFile): []byte(gateway),
			s.paths.Sysctl:                          []byte(renderSysctl(sp)),
			s.paths.Unit:                            []byte(renderUnit(s.paths, needsAdmission(sp), s.independentRecovery)),
		}
		if s.independentRecovery {
			files[filepath.Join(filepath.Dir(s.paths.Unit), "just-dashboard-network-recovery.service")] = []byte(renderRecoveryUnit(s.paths))
		}
		paths := make([]string, 0, len(files))
		for path := range files {
			paths = append(paths, path)
		}
		sort.Strings(paths)
		for _, path := range paths {
			var err error
			if path == filepath.Join(s.paths.Dir, gatewayFile) {
				err = gatewayErr
			}
			r.Files = append(r.Files, fileObservation(path, "render", files[path], err))
		}
		if s.independentRecovery {
			r.Files = append(r.Files, s.driftRecoveryHelper())
		}
		r.Runtime = s.driftRuntime(ctx, sp)
		r.Runtime = append(r.Runtime, s.driftGateway(ctx, sp, gateway, gatewayErr)...)
		r.Admission = s.admissionState(ctx, sp)
		for _, ch := range r.Admission.Chains {
			o := observation("admission", ch.Family+"/"+ch.Chain)
			o.Coverage, o.Status, o.Reason = "owned-rule-order", "unknown", ch.Reason
			switch ch.Status {
			case "present":
				o.Status = "matching"
			case "missing", "misordered", "absent":
				o.Status, o.Repairable = "drift", true
			case "unsupported":
				if !ch.Needed {
					o.Status = "not_required"
				}
			case "unreadable":
				o.Status = "unreadable"
			}
			r.Runtime = append(r.Runtime, o)
		}
		for _, bl := range sp.Blocklists {
			v := blocklistView(filepath.Join(s.paths.Dir, "lists"), bl, netip.Addr{}, nil)
			s.blocklistHealth(ctx, bl, &v)
			r.Blocklists = append(r.Blocklists, v)
		}
	}
	r.Boot = s.driftBoot(ctx)
	_, after, afterSaved := readDriftSpec(s)
	journalAfter, afterErr := readDriftFile(filepath.Join(s.paths.Dir, recoveryFile))
	if afterSaved != saved || after.Status != r.Spec.Status || digestBytes(journalBefore) != digestBytes(journalAfter) || (journalErr == nil) != (afterErr == nil) {
		r.Consistent = false
	}
	r.FinishedAt = time.Now().UTC()
	r.Status = driftSummary(r)
	r.RepairPlan = driftRepairPlan(r)
	return r
}

func driftSummary(r DriftReport) string {
	if !r.Consistent || r.Spec.Status != "matching" {
		return "unknown"
	}
	unknown, drift := false, false
	all := append([]DriftObservation{r.Spec, r.Journal}, r.Files...)
	all = append(all, r.Runtime...)
	for _, o := range all {
		switch o.Status {
		case "missing", "drift", "conflict":
			drift = true
		case "unknown", "unreadable":
			unknown = true
		}
	}
	for _, b := range r.Blocklists {
		if !b.Enabled {
			continue
		}
		if b.Enforcement == "degraded" {
			drift = true
		} else if b.Enforcement != "verified" {
			unknown = true
		}
	}
	if r.Boot.Status == "drift" || r.Boot.Status == "conflict" || r.Boot.Status == "missing" || r.Boot.Execution.Status == "failed" {
		drift = true
	}
	if r.Boot.Status == "unknown" || r.Boot.Status == "unreadable" {
		unknown = true
	}
	if r.Change != nil && !changeTerminal(r.Change.Phase) {
		unknown = true
	}
	if r.Boot.Execution.Status == "unknown" || r.Boot.Execution.Status == "unrecorded" {
		unknown = true
	}
	if drift {
		return "drift"
	}
	if unknown {
		return "unknown"
	}
	return "matching"
}

func driftRepairPlan(r DriftReport) OwnedRepairPlan {
	p := OwnedRepairPlan{Generation: r.SavedGeneration, CreatedAt: r.FinishedAt, Status: "review_required",
		Blockers: []string{}, Items: []OwnedRepair{}, Preconditions: []string{"Saved configuration generation must still match.", "Reread selected resources and their dependencies before applying.", "Require system.admin, audit and the existing path guard and durable recovery transaction."}}
	p.Excluded = []RepairExclusion{}
	if r.Spec.Status != "matching" {
		p.Blockers = append(p.Blockers, "A readable supported saved configuration is required.")
	}
	if !r.Consistent {
		p.Blockers = append(p.Blockers, "Configuration or recovery journal changed during inspection.")
	}
	if r.Journal.Status == "unreadable" || r.Journal.Status == "unknown" {
		p.Blockers = append(p.Blockers, "Recovery journal cannot be established.")
	}
	if r.Change != nil && !changeTerminal(r.Change.Phase) {
		p.Blockers = append(p.Blockers, "An unresolved network change must be confirmed or recovered first.")
	}
	all := append(append([]DriftObservation{}, r.Files...), r.Runtime...)
	if r.Boot.Status != "matching" {
		o := observation("boot-unit", r.Boot.Unit)
		o.Status, o.Reason, o.Owned, o.Repairable = r.Boot.Status, r.Boot.Reason, r.Boot.Owned, r.Boot.Repairable
		all = append(all, o)
	}
	for _, o := range all {
		if !o.Owned || !o.Repairable || (o.Status != "missing" && o.Status != "drift") {
			if o.Status != "matching" && o.Status != "not_required" {
				p.Excluded = append(p.Excluded, RepairExclusion{ObservationID: o.ID, Reason: o.Reason})
			}
			continue
		}
		action := "restore_owned_configuration"
		if o.Domain == "render" {
			action = "regenerate_owned_file"
		}
		if o.Domain == "admission" {
			action = "reconcile_owned_admission"
		}
		p.Items = append(p.Items, OwnedRepair{ID: "repair:" + o.ID, ObservationID: o.ID, Domain: o.Domain, Resource: o.Resource, Action: action, Reason: o.Reason, Dependencies: o.Dependencies, Preconditions: []string{"Verify this resource is still owned and has the observed state.", "Refuse a foreign replacement or occupied identity."}})
	}
	if len(p.Blockers) > 0 {
		p.Status = "blocked"
	} else if len(p.Items) == 0 {
		p.Status = "no_known_repairs"
	}
	return p
}

func driftSysctlKey(key string) bool {
	parts := strings.Split(key, ".")
	if len(parts) < 2 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
		for _, c := range part {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
				return false
			}
		}
	}
	return true
}

func (s *Service) driftRecoveryHelper() DriftObservation {
	path := filepath.Join(s.paths.Dir, recoveryBinary)
	o := observation("recovery-helper", path)
	o.Coverage = "presence-and-mode"
	data, err := readDriftFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		o.Status, o.Repairable, o.Reason = "missing", true, "The independent recovery executable required by the boot unit is absent."
		return o
	}
	if err != nil {
		o.Status, o.Reason = "unreadable", err.Error()
		return o
	}
	info, err := os.Lstat(path)
	if err != nil {
		o.Status, o.Reason = "unreadable", err.Error()
		return o
	}
	o.Observed = map[string]string{"sha256": digestBytes(data), "mode": fmt.Sprintf("%04o", info.Mode().Perm())}
	if len(data) < 4 || string(data[:4]) != "\x7fELF" {
		o.Status, o.Owned, o.Reason = "conflict", false, "The helper path contains an unrecognized replacement."
		return o
	}
	if info.Mode().Perm() != 0o700 {
		o.Status, o.Reason = "drift", "The recovery helper permissions differ from its managed private executable mode."
		return o
	}
	o.Reason = "The recovery executable exists; its build identity and ability to run were not established by this read-only inspection."
	return o
}
