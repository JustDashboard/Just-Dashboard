package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

func TestWorkloadIngressCaptureFailurePersistsAnUnverifiedLink(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "nginx"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	s := &Server{}
	s.modules.proxy = proxysvc.New(root, filepath.Join(root, "Caddyfile"))
	recovered := &deploy.RecoveredWorkload{Adoption: &deploy.WorkloadAdoption{Key: "systemd:fixture", BaselineDigest: "sha256:original", Snapshot: json.RawMessage(`{"version":1}`)}}
	result, err := s.attachWorkloadIngress(t.Context(), &deploy.WorkloadCandidate{Kind: "systemd"}, recovered, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Adoption.IngressBindings) != 1 || result.Adoption.IngressBindings[0].Status != "unverified" {
		t.Fatal("failed manager inspection became only an advisory warning")
	}
	if len(result.Configuration.Dependencies) != 1 || result.Configuration.Dependencies[0].Kind != "ingress" || result.Configuration.Dependencies[0].Ownership != deploy.OwnershipLinked || result.Configuration.Dependencies[0].ResourceKind != "existing_proxy_route" {
		t.Fatal("unverified manager has no deployment continuity fence")
	}
}
