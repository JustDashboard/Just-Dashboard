package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

// The kernel a GitHub runner image moved to, on which mongo:8.0 refused to
// start until GLIBC_TUNABLES handed rseq back to glibc.
const refusingKernel = "7.0.0-1017-azure"

func TestMongoImageReleaseReadsTheTag(t *testing.T) {
	for image, want := range map[string]string{
		"mongo:8.0":                 "8.0",
		"mongo:8.0.34":              "8.0.34",
		"docker.io/library/mongo:8": "8",
		"mongo:7.0-jammy":           "7.0",
		"mongodb/mongodb-community-server:8.0.4-ubuntu2204": "8.0.4",
		"mongo":                         "",
		"mongo:latest":                  "",
		"bitnami/mongodb:8.0":           "",
		"postgres:18-alpine":            "",
		"registry.local:5000/mongo:8.0": "8.0",
	} {
		if got := mongoImageRelease(image); got != want {
			t.Errorf("mongoImageRelease(%q) = %q, want %q", image, got, want)
		}
	}
	// The official image's own variables outrank a tag that names nothing.
	if got := mongoContainerRelease("mongo:latest", map[string]string{"MONGO_VERSION": "8.0.34"}); got != "8.0.34" {
		t.Errorf("release from the image's environment = %q", got)
	}
}

func TestMongoKernelProblemIsOnlyMongoDB8WithoutTheTunable(t *testing.T) {
	shipped := map[string]string{"MONGO_VERSION": "8.0.34", deploy.MongoKernelTunables: "glibc.pthread.rseq=0"}
	problem := mongoKernelProblemOf(refusingKernel, "mongo:8.0", shipped, "")
	if problem == nil || !problem.Fixable || problem.Message != mongoKernelMessage || problem.Release != "8.0.34" ||
		!strings.Contains(problem.Detail, refusingKernel) || !strings.Contains(problem.Detail, deploy.MongoKernelTicket) ||
		!strings.Contains(problem.Detail, "glibc.pthread.rseq=1") {
		t.Fatalf("MongoDB 8.0 as shipped on Linux %s: %+v", refusingKernel, problem)
	}
	// Compose puts back what its file says, so the dashboard says what to
	// change there rather than offering to recreate it.
	composed := mongoKernelProblemOf(refusingKernel, "mongo:8.0", shipped, "shop")
	if composed == nil || composed.Fixable || composed.Message != mongoKernelComposeMessage ||
		!strings.Contains(composed.Detail, "compose project shop") {
		t.Fatalf("a compose service: %+v", composed)
	}
	for why, c := range map[string]struct {
		kernel, image string
		env           map[string]string
	}{
		"already mitigated": {refusingKernel, "mongo:8.0", map[string]string{deploy.MongoKernelTunables: "glibc.pthread.rseq=1"}},
		"MongoDB 7.0":       {refusingKernel, "mongo:7.0", map[string]string{"MONGO_VERSION": "7.0.45"}},
		"a fixed kernel":    {"7.0.14-generic", "mongo:8.0", shipped},
		"an older kernel":   {"6.8.0-139-generic", "mongo:8.0", shipped},
		"no kernel read":    {"", "mongo:8.0", shipped},
		"an unknown image":  {refusingKernel, "mongo:latest", map[string]string{}},
	} {
		if got := mongoKernelProblemOf(c.kernel, c.image, c.env, ""); got != nil {
			t.Errorf("%s: offered %+v", why, got)
		}
		if mongoKernelUnneeded(c.kernel, mongoContainerRelease(c.image, c.env), c.env) == "" {
			t.Errorf("%s: no reason given for not needing it", why)
		}
	}
}

func TestApplyingTheMitigationKeepsTheRestOfTheEnvironment(t *testing.T) {
	spec := &dockerx.ContainerSpec{
		Env: []dockerx.EnvVar{
			{Name: "MONGO_INITDB_ROOT_USERNAME", Value: "jd"},
			{Name: deploy.MongoKernelTunables, Value: "glibc.malloc.arena_max=2:glibc.pthread.rseq=0"},
		},
		Labels: []dockerx.LabelSpec{{Name: "team", Value: "shop"}},
	}
	if got := applyMongoKernelMitigation(spec, refusingKernel); got != "glibc.malloc.arena_max=2:glibc.pthread.rseq=1" {
		t.Fatalf("tunables = %q", got)
	}
	if len(spec.Env) != 2 || spec.Env[0].Value != "jd" || spec.Env[1].Value != "glibc.malloc.arena_max=2:glibc.pthread.rseq=1" {
		t.Errorf("env = %+v", spec.Env)
	}
	if len(spec.Labels) != 2 || spec.Labels[1] != (dockerx.LabelSpec{Name: mongoKernelLabel, Value: refusingKernel}) {
		t.Errorf("labels = %+v", spec.Labels)
	}
	// Applied twice, it is the same container.
	applyMongoKernelMitigation(spec, refusingKernel)
	if len(spec.Env) != 2 || len(spec.Labels) != 2 {
		t.Errorf("applied twice: env %+v, labels %+v", spec.Env, spec.Labels)
	}
	record := mongoKernelRecord(map[string]string{mongoKernelLabel: refusingKernel})
	if !strings.Contains(record, refusingKernel) || !strings.Contains(record, deploy.MongoKernelTicket) ||
		mongoKernelRecord(map[string]string{"team": "shop"}) != "" {
		t.Errorf("record = %q", record)
	}
}

func TestProvisionStartsMongoDB8WithWhatThisKernelNeeds(t *testing.T) {
	for _, c := range []struct {
		kernel, version string
		mitigated       bool
	}{
		{refusingKernel, "", true},
		{refusingKernel, "8.0", true},
		{refusingKernel, "7.0", false},
		{"6.8.0-139-generic", "8.0", false},
	} {
		s, router := dbTestRouter(t)
		created := provisionNameEngine(t, s, map[string]bool{}, map[string]bool{})
		kernel := c.kernel
		s.modules.kernelRelease = func(context.Context) (string, error) { return kernel, nil }
		body, _ := json.Marshal(map[string]string{"engine": "mongodb", "name": "jd-mongo", "version": c.version})
		rec := do(t, router, http.MethodPost, "/databases/provision", string(body))
		if rec.Code != http.StatusAccepted {
			t.Fatalf("Linux %s, MongoDB %q: provision = %d %s", c.kernel, c.version, rec.Code, rec.Body.String())
		}
		var reply struct {
			KernelMitigation string `json:"kernelMitigation"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &reply)
		spec := (*created)[0]
		env := strings.Join(spec.Env, "\n")
		has := strings.Contains(env, deploy.MongoKernelTunables+"="+deploy.MongoKernelTunable)
		if has != c.mitigated || (spec.Labels[mongoKernelLabel] == c.kernel) != c.mitigated ||
			(reply.KernelMitigation != "") != c.mitigated {
			t.Errorf("Linux %s, MongoDB %q: env %q, labels %v, reply %q; want mitigated %t",
				c.kernel, c.version, env, spec.Labels, reply.KernelMitigation, c.mitigated)
		}
		if !strings.Contains(env, "MONGO_INITDB_ROOT_USERNAME=jd") {
			t.Errorf("the account went missing: %q", env)
		}
	}
}

func TestContainerDownSaysWhenItIsMongoDBOnThisKernel(t *testing.T) {
	line := mongoKernelAlertLine(refusingKernel, dockerx.Container{Image: "mongo:8.0"})
	if !strings.Contains(line, refusingKernel) || !strings.Contains(line, deploy.MongoKernelTicket) {
		t.Fatalf("line = %q", line)
	}
	for _, quiet := range []dockerx.Container{
		{Image: "mongo:7.0"},
		{Image: "postgres:18-alpine"},
		{Image: "mongo:8.0", Labels: map[string]string{mongoKernelLabel: refusingKernel}},
	} {
		if got := mongoKernelAlertLine(refusingKernel, quiet); got != "" {
			t.Errorf("%+v: %q", quiet, got)
		}
	}
	if got := mongoKernelAlertLine("6.8.0-139-generic", dockerx.Container{Image: "mongo:8.0"}); got != "" {
		t.Errorf("an older kernel: %q", got)
	}
	reading := containerAlertReading(hostAlertRule{Kind: hostAlertContainerDown}, []hostContainer{
		{Name: "orders-mongo", State: "restarting", Kernel: line},
		{Name: "queue", State: "restarting"},
	}, true)
	said := map[string]string{}
	for _, f := range reading.Firing {
		said[f.Subject] = f.Detail
	}
	if !strings.HasSuffix(said["orders-mongo"], line) || strings.Contains(said["queue"], deploy.MongoKernelTicket) {
		t.Errorf("firing = %v", said)
	}
}

func TestKernelMitigationRefusesWhatIsNotMongoDB(t *testing.T) {
	_, router := dbTestRouter(t)
	rec := do(t, router, http.MethodPost, "/databases/1/kernel-mitigation", "")
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "not a MongoDB server") {
		t.Fatalf("a SQLite connection = %d %s", rec.Code, rec.Body.String())
	}
}

func TestKernelMitigationOfAContainerNamesOne(t *testing.T) {
	_, router := dbTestRouter(t)
	if rec := do(t, router, http.MethodPost, "/databases/kernel-mitigation", `{}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("no container = %d %s", rec.Code, rec.Body.String())
	}
}
