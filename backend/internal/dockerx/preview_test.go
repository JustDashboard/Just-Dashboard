package dockerx

import (
	"context"
	"strings"
	"testing"
	"time"
)

const composeBefore = `services:
  web:
    image: nginx:1.25
    ports:
      - "8080:80"
  db:
    image: postgres:16
    volumes:
      - pgdata:/var/lib/postgresql/data

volumes:
  pgdata:
`

const composeAfter = `services:
  web:
    image: nginx:1.27
    ports:
      - "8080:80"
  db:
    image: postgres:16
    volumes:
      - pgdata:/var/lib/postgresql/data

volumes:
  pgdata:
`

func previewFor(t *testing.T, before, after string, running ...ComposeService) *DeployPreview {
	t.Helper()
	st := &ComposeStack{Name: "app", Services: running}
	var prev *StackDeployment
	if before != "" {
		prev = &StackDeployment{Config: before, CreatedAt: time.Now().Add(-time.Hour)}
	}
	return (&Client{}).PreviewDeploy(context.Background(), st, after, prev, "up")
}

func TestPreviewNamesOnlyWhatChanged(t *testing.T) {
	p := previewFor(t, composeBefore, composeAfter,
		ComposeService{Name: "web", State: "running"},
		ComposeService{Name: "db", State: "running"})

	if p.Recreate != 1 || p.Unchanged != 1 {
		t.Fatalf("one image changed: %+v", p)
	}
	var web *ServiceChange
	for i := range p.Services {
		if p.Services[i].Name == "web" {
			web = &p.Services[i]
		}
	}
	if web == nil || web.Change != "recreate" {
		t.Fatalf("web should be recreated: %+v", p.Services)
	}
	if web.ImageBefore != "nginx:1.25" || web.ImageAfter != "nginx:1.27" {
		t.Errorf("the image change belongs on the row: %+v", web)
	}
	if !web.Inferred {
		t.Error("compose makes the final call, so this is inferred")
	}
}

// The commonest fear about a deploy, answered explicitly.
func TestPreviewSaysNoVolumeIsRemoved(t *testing.T) {
	p := previewFor(t, composeBefore, composeAfter,
		ComposeService{Name: "web", State: "running"},
		ComposeService{Name: "db", State: "running"})
	if len(p.VolumesRemoved) != 0 {
		t.Errorf("an up never removes a volume: %v", p.VolumesRemoved)
	}
	if len(p.VolumesKept) != 1 || p.VolumesKept[0] != "pgdata" {
		t.Errorf("named volumes should be listed as kept: %v", p.VolumesKept)
	}
	if !strings.Contains(p.Summary, "No volume is removed") {
		t.Errorf("summary = %q", p.Summary)
	}
}

func TestPreviewWithoutHistorySaysSo(t *testing.T) {
	p := previewFor(t, "", composeAfter, ComposeService{Name: "web", State: "running"})
	if len(p.Diff) != 0 {
		t.Error("nothing to diff against")
	}
	found := false
	for _, c := range p.Caveats {
		if strings.Contains(c, "No previous deployment") {
			found = true
		}
	}
	if !found {
		t.Errorf("say what cannot be known: %v", p.Caveats)
	}
}

func TestPreviewCountsCreateStartAndOrphans(t *testing.T) {
	p := previewFor(t, composeBefore, composeBefore,
		ComposeService{Name: "web", State: "exited"},
		ComposeService{Name: "gone", State: "running"})
	if p.Create != 1 {
		t.Errorf("db has no container, so it is created: %+v", p)
	}
	if p.Start != 1 {
		t.Errorf("web exists and is stopped, so it is started: %+v", p)
	}
	if p.Remove != 1 {
		t.Errorf("gone is not in the file, so it is an orphan: %+v", p)
	}
}

func TestChangedFieldsIgnoresReordering(t *testing.T) {
	before := parseComposeServices(`services:
  web:
    image: nginx
    environment:
      - B=2
      - A=1
`)
	after := parseComposeServices(`services:
  web:
    image: nginx
    environment:
      - A=1
      - B=2
`)
	if got := changedFields(before["web"], after["web"]); len(got) != 0 {
		t.Errorf("a reordered environment block is not a change: %v", got)
	}
}

func TestChangedFieldsCatchesTheOnesThatMatter(t *testing.T) {
	before := parseComposeServices(`services:
  web:
    image: nginx:1.25
    ports: ["80:80"]
`)
	after := parseComposeServices(`services:
  web:
    image: nginx:1.27
    ports: ["8080:80"]
    mem_limit: 512m
`)
	got := changedFields(before["web"], after["web"])
	want := map[string]bool{"image": true, "ports": true, "memory limit": true}
	if len(got) != len(want) {
		t.Fatalf("changedFields = %v", got)
	}
	for _, field := range got {
		if !want[field] {
			t.Errorf("unexpected field %q", field)
		}
	}
}

func TestUnifiedDiffMarksBothSides(t *testing.T) {
	diff := unifiedDiff(composeBefore, composeAfter)
	added, removed := 0, 0
	for _, line := range diff {
		switch line.Kind {
		case "added":
			added++
			if !strings.Contains(line.Text, "1.27") {
				t.Errorf("unexpected added line %q", line.Text)
			}
		case "removed":
			removed++
			if !strings.Contains(line.Text, "1.25") {
				t.Errorf("unexpected removed line %q", line.Text)
			}
		}
	}
	if added != 1 || removed != 1 {
		t.Fatalf("one line each way, got +%d -%d", added, removed)
	}
}

func TestUnifiedDiffOfIdenticalFilesIsEmpty(t *testing.T) {
	if got := unifiedDiff(composeBefore, composeBefore); len(got) != 0 {
		t.Errorf("nothing changed, so nothing is shown: %d lines", len(got))
	}
}

func TestServiceVolumeNamesSkipsBindMounts(t *testing.T) {
	svc := parseComposeServices(`services:
  app:
    volumes:
      - data:/var/lib/app
      - /etc/localtime:/etc/localtime:ro
      - ./config:/config
`)["app"]
	got := serviceVolumeNames(svc)
	if len(got) != 1 || got[0] != "data" {
		t.Fatalf("only named volumes are Docker's to remove: %v", got)
	}
}

func TestHashConfigIgnoresTrailingWhitespace(t *testing.T) {
	if HashConfig("services:\n  a: {}\n") != HashConfig("services:\n  a: {}\n\n  ") {
		t.Error("a re-save with no change is not a change")
	}
	if HashConfig("a") == HashConfig("b") {
		t.Error("different content, different hash")
	}
}
