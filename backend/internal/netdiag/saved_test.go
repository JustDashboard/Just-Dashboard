package netdiag

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/jobs"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netsec"
)

// noProbe fails any probe: saved references never run one.
func noProbe(context.Context, netsec.ProbeRequest) (*netsec.ProbeResult, error) {
	return nil, errors.New("saved references must not run a probe")
}

const trustFP = "SHA256:" + "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

func TestSSHTrustPersistsReplacesAndForgets(t *testing.T) {
	s, db := diagnosticService(t, noProbe)
	entry := netsec.SSHTrust{Target: "host.example.test:22", Keys: []netsec.SSHTrustedKey{{Type: "ssh-ed25519", Fingerprint: trustFP}}, Source: "observed", SavedBy: "operator"}
	saved, err := s.SaveSSHTrust(t.Context(), entry, false)
	if err != nil || saved.SavedAt.IsZero() {
		t.Fatalf("%+v %v", saved, err)
	}
	entry.Source = "entered"
	if _, err := s.SaveSSHTrust(t.Context(), entry, false); !errors.Is(err, ErrTrustExists) {
		t.Fatalf("existing trust was overwritten without confirmation: %v", err)
	}
	if _, err := s.SaveSSHTrust(t.Context(), entry, true); err != nil {
		t.Fatal(err)
	}
	reopened := New(NewStore(db.DB), jobs.New(nil), s.runner)
	if err := reopened.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	got, err := reopened.SSHTrustFor(t.Context(), "host.example.test:22")
	if err != nil || got == nil || got.Source != "entered" {
		t.Fatalf("reopened = %+v %v", got, err)
	}
	all, _ := reopened.SSHTrust(t.Context())
	if len(all) != 1 {
		t.Fatalf("replace duplicated the entry: %+v", all)
	}
	if missing, err := reopened.SSHTrustFor(t.Context(), "other.example.test:22"); err != nil || missing != nil {
		t.Fatalf("missing = %+v %v", missing, err)
	}
	if err := reopened.ForgetSSHTrust(t.Context(), "host.example.test:22"); err != nil {
		t.Fatal(err)
	}
	if err := reopened.ForgetSSHTrust(t.Context(), "host.example.test:22"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second forget = %v", err)
	}
	for i := 0; i < MaxSSHTrust; i++ {
		entry.Target = "h" + strings.Repeat("x", i%3) + string(rune('a'+i%26)) + string(rune('a'+i/26)) + ".test:22"
		if _, err := reopened.SaveSSHTrust(t.Context(), entry, false); err != nil {
			t.Fatalf("entry %d: %v", i, err)
		}
	}
	entry.Target = "one-too-many.test:22"
	if _, err := reopened.SaveSSHTrust(t.Context(), entry, false); !errors.Is(err, ErrTooMany) {
		t.Fatalf("over the bound = %v", err)
	}
}

func TestWakeDevicesValidateCreateUpdateAndDelete(t *testing.T) {
	s, _ := diagnosticService(t, noProbe)
	created, err := s.SaveWakeDevice(t.Context(), WakeDevice{Name: " NAS ", MAC: "02-11-22-33-44-55", Interface: "eno1", Verify: "192.168.1.50", Port: 22}, "operator")
	if err != nil || created.ID == "" || created.Name != "NAS" || created.MAC != "02:11:22:33:44:55" || created.CreatedBy != "operator" {
		t.Fatalf("%+v %v", created, err)
	}
	created.Name, created.Port, created.Verify = "Storage", 0, "192.168.1.51"
	updated, err := s.SaveWakeDevice(t.Context(), created, "someone-else")
	if err != nil || updated.CreatedBy != "operator" || updated.Verify != "192.168.1.51" {
		t.Fatalf("%+v %v", updated, err)
	}
	devices, _ := s.WakeDevices(t.Context())
	if len(devices) != 1 || devices[0].Name != "Storage" {
		t.Fatalf("devices = %+v", devices)
	}
	for _, bad := range []WakeDevice{
		{Name: "x", MAC: "ff:ff:ff:ff:ff:ff", Interface: "eno1"},
		{Name: "x", MAC: "02:11:22:33:44:55", Interface: "a/b"},
		{Name: "x", MAC: "02:11:22:33:44:55", Interface: "eno1", Port: 22},
		{Name: "x", MAC: "02:11:22:33:44:55", Interface: "eno1", Verify: "nas.local"},
		{Name: "", MAC: "02:11:22:33:44:55", Interface: "eno1"},
	} {
		if _, err := s.SaveWakeDevice(t.Context(), bad, "operator"); !errors.Is(err, ErrInvalid) {
			t.Errorf("accepted %+v: %v", bad, err)
		}
	}
	if _, err := s.SaveWakeDevice(t.Context(), WakeDevice{ID: "missing", Name: "x", MAC: "02:11:22:33:44:55", Interface: "eno1"}, "operator"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update of a missing device = %v", err)
	}
	if err := s.DeleteWakeDevice(t.Context(), created.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteWakeDevice(t.Context(), created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete = %v", err)
	}
}
