package dockerx

import (
	"context"
	"encoding/json"
	"os"
	"regexp"
	"testing"
	"time"
)

// Every list-shaped field on the wire must be a list.
//
// A nil Go slice marshals to `null`, and the client reads these as arrays, so
// one nil field is `Cannot read properties of null (reading 'length')` — a
// TypeError that blanks the entire page rather than leaving a value missing.
// It cannot be caught by a type: the Go field and the TypeScript field both
// say `[]string` and both are right.
//
// Against the real daemon rather than a fixture, because the bug only appears
// in the case a fixture would not think to build: a cleanup category with no
// examples, a network detail route that never filled a field the list route
// does. Every response the Docker section serves is walked here, so a new
// endpoint that forgets to initialise a slice fails this rather than a page.
func TestNoNullListsOnTheWire(t *testing.T) {
	host := os.Getenv("DOCKER_HOST")
	if host == "" {
		host = "unix:///var/run/docker.sock"
	}
	c := New(host)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if !c.Ping(ctx).Available {
		t.Skip("no docker daemon")
	}
	nullField := regexp.MustCompile(`"([A-Za-z0-9_]+)":null`)

	check := func(name string, v any, err error) {
		t.Helper()
		if err != nil {
			t.Logf("%s: %v", name, err)
			return
		}
		raw, marshalErr := json.Marshal(v)
		if marshalErr != nil {
			t.Fatalf("%s: %v", name, marshalErr)
		}
		for _, m := range nullField.FindAllStringSubmatch(string(raw), -1) {
			t.Errorf("%s: field %q is null on the wire", name, m[1])
		}
	}

	cleanup, err := c.PreviewCleanup(ctx)
	check("PreviewCleanup", cleanup, err)
	du, err := c.DiskUsage(ctx)
	check("DiskUsage", du, err)
	diag, err := c.Diagnose(ctx)
	check("Diagnose", diag, err)
	stacks, err := c.ListStacks(ctx, nil)
	check("ListStacks", stacks, err)
	vols, err := c.ListVolumesWithUsers(ctx)
	check("ListVolumesWithUsers", vols, err)
	nets, err := c.ListNetworks(ctx)
	check("ListNetworks", nets, err)
	imgs, err := c.ListImages(ctx, true)
	check("ListImages", imgs, err)
	for _, img := range imgs {
		detail, err := c.InspectImage(ctx, img.ID)
		check("InspectImage", detail, err)
		break
	}

	list, err := c.ListContainers(ctx, true)
	check("ListContainers", list, err)
	if len(list) > 0 {
		insp, err := c.Inspect(ctx, list[0].ID)
		check("Inspect", insp, err)
		fail, err := c.DiagnoseFailure(ctx, list[0].ID, nil)
		check("DiagnoseFailure", fail, err)
		wr, err := c.AnalyzeWritableLayer(ctx, list[0].ID, false)
		check("AnalyzeWritableLayer", wr, err)
		net, err := c.NetworkDetail(ctx, "bridge")
		check("NetworkDetail", net, err)
	}
	for _, v := range vols {
		vd, err := c.VolumeDetail(ctx, v.Name)
		check("VolumeDetail", vd, err)
		break
	}
	for i := range stacks {
		prev := c.PreviewDeploy(ctx, &stacks[i], "services:\n  a:\n    image: nginx\n", nil, "up")
		check("PreviewDeploy", prev, nil)
		break
	}
}
