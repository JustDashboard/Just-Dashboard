package api

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/sysinfo"
)

// MongoDB 8 on a kernel it refuses to start on (deploy.MongoKernelAffected).
//
// A new server is started with the mitigation in its environment, without
// asking: it is the difference between a database and a container that
// restarts for ever, and nothing about it is the operator's to decide. A
// server that already exists, made before this or by hand, is recreated with
// it from the Databases page, as any other change to a container's
// configuration is: on the same volumes, through Recreate's park-and-restore,
// confirmed and audited.
//
// The container carries a label naming the kernel it was applied for, which is
// how a connection's page says that the mitigation is on and why. The
// environment itself is what decides whether a container has it.

// mongoKernelLabel is set on a container started with the mitigation; its
// value is the kernel release it was applied for.
const mongoKernelLabel = "io.just-dashboard.mongo-kernel-mitigation"

// mongoKernelMessage is what the Databases page says of a server that needs
// it, and mongoKernelComposeMessage of one whose compose file has to say it.
const (
	mongoKernelMessage        = "MongoDB cannot start on this kernel; apply the mitigation"
	mongoKernelComposeMessage = "MongoDB cannot start on this kernel; add the mitigation to its compose file"
)

// hostKernel is the running kernel's release, empty when it cannot be read.
func (s *Server) hostKernel(ctx context.Context) string {
	read := s.modules.kernelRelease
	if read == nil {
		read = sysinfo.KernelRelease
	}
	release, err := read(ctx)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(release)
}

// mongoKernelNeedsMitigation reports whether a MongoDB release starts here
// only with the mitigation.
func mongoKernelNeedsMitigation(kernel, release string) bool {
	return deploy.MongoKernelAffected(kernel) && deploy.MongoReleaseAffected(release)
}

// applyMongoKernelMitigation puts the tunable into a container's environment,
// keeping any other glibc tunable it sets, and labels it with the kernel.
func applyMongoKernelMitigation(spec *dockerx.ContainerSpec, kernel string) string {
	value := deploy.MongoKernelTunable
	found := false
	for i, e := range spec.Env {
		if e.Name == deploy.MongoKernelTunables {
			value = deploy.WithMongoKernelTunable(e.Value)
			spec.Env[i].Value, found = value, true
		}
	}
	if !found {
		spec.Env = append(spec.Env, dockerx.EnvVar{Name: deploy.MongoKernelTunables, Value: value})
	}
	labelled := false
	for i, l := range spec.Labels {
		if l.Name == mongoKernelLabel {
			spec.Labels[i].Value, labelled = kernel, true
		}
	}
	if !labelled {
		spec.Labels = append(spec.Labels, dockerx.LabelSpec{Name: mongoKernelLabel, Value: kernel})
	}
	return value
}

// mongoKernelRecord is the sentence a connection's page says of a container
// started with the mitigation, from its label; empty without one.
func mongoKernelRecord(labels map[string]string) string {
	kernel, ok := labels[mongoKernelLabel]
	if !ok {
		return ""
	}
	return fmt.Sprintf("Started with %s=%s, without which MongoDB 8 does not start on Linux %s (MongoDB %s). mongod warns that it costs performance; Linux 7.0.14 and later do not need it.",
		deploy.MongoKernelTunables, deploy.MongoKernelTunable, cmp.Or(kernel, "6.19 to 7.0.13"), deploy.MongoKernelTicket)
}

// mongoContainerRelease is the MongoDB release a container runs: the official
// image's own MONGO_VERSION or MONGO_MAJOR, else its image's tag. Empty when
// neither says.
func mongoContainerRelease(image string, env map[string]string) string {
	if v := cmp.Or(env["MONGO_VERSION"], env["MONGO_MAJOR"]); v != "" {
		return v
	}
	return mongoImageRelease(image)
}

// mongoImageRelease is the release a MongoDB image's tag names ("mongo:8.0",
// "mongodb/mongodb-community-server:8.0.4-ubuntu2204"), or empty for any other
// image and for a tag that names none ("latest").
func mongoImageRelease(image string) string {
	ref := dockerx.NormalizeImageRef(image)
	slash := strings.LastIndex(ref, "/")
	repo, tag, ok := strings.Cut(ref[slash+1:], ":")
	if !ok || (repo != "mongo" && repo != "mongodb-community-server" && repo != "mongodb-enterprise-server") {
		return ""
	}
	tag, _, _ = strings.Cut(tag, "@")
	tag, _, _ = strings.Cut(tag, "-")
	if tag == "" || tag[0] < '0' || tag[0] > '9' {
		return ""
	}
	return tag
}

// dbKernelProblem is a MongoDB server that does not start on this kernel and
// would with the mitigation, as the Databases page draws it.
type dbKernelProblem struct {
	// Message is the alert; Detail the sentence behind it, which says what
	// applying it does or, where the dashboard cannot, what to do instead.
	Message string `json:"message"`
	Detail  string `json:"detail"`
	Kernel  string `json:"kernel"`
	Release string `json:"release"`
	// Fixable reports that POST /databases/{id}/kernel-mitigation applies it.
	Fixable bool `json:"fixable"`
}

// mongoKernelProblem is what this kernel means for a connection's server:
// nil unless it is a MongoDB 8 container here that is not running and whose
// environment lacks the mitigation. A running one is left alone whatever it
// carries, so a MongoDB release that learns to start on these kernels is
// never told it cannot.
func (s *Server) mongoKernelProblem(ctx context.Context, driver dbx.Driver, place dbPlacement) *dbKernelProblem {
	if driver != dbx.DriverMongo || s.modules.docker == nil || place.Container == nil || place.foreign ||
		place.Container.State == "running" {
		return nil
	}
	kernel := s.hostKernel(ctx)
	if !deploy.MongoKernelAffected(kernel) {
		return nil
	}
	detail, err := s.modules.docker.Inspect(ctx, place.Container.ID)
	if err != nil {
		return nil
	}
	return mongoKernelProblemOf(kernel, detail.Image, envMap(detail.Env), place.Container.ComposeProject)
}

func mongoKernelProblemOf(kernel, image string, env map[string]string, composeProject string) *dbKernelProblem {
	release := mongoContainerRelease(image, env)
	if mongoKernelUnneeded(kernel, release, env) != "" {
		return nil
	}
	problem := &dbKernelProblem{Message: mongoKernelMessage, Kernel: kernel, Release: release}
	if composeProject != "" {
		problem.Message = mongoKernelComposeMessage
		problem.Detail = fmt.Sprintf("MongoDB %s refuses to start on Linux %s (MongoDB %s). It belongs to the compose project %s, which puts back what its file says: add %s=%s to the service's environment there and redeploy the stack.",
			release, kernel, deploy.MongoKernelTicket, composeProject, deploy.MongoKernelTunables, deploy.MongoKernelTunable)
		return problem
	}
	problem.Fixable = true
	problem.Detail = fmt.Sprintf("MongoDB %s refuses to start on Linux %s (MongoDB %s). Applying the mitigation recreates its container with %s=%s, on the same volumes.",
		release, kernel, deploy.MongoKernelTicket, deploy.MongoKernelTunables, deploy.MongoKernelTunable)
	return problem
}

// mongoKernelUnneeded is why a container does not need the mitigation, or
// empty when it does.
func mongoKernelUnneeded(kernel, release string, env map[string]string) string {
	switch {
	case !deploy.MongoKernelAffected(kernel):
		return fmt.Sprintf("Linux %s is not a kernel MongoDB 8 refuses to start on; only 6.19 up to 7.0.13 are", cmp.Or(kernel, "(unreadable)"))
	case !deploy.MongoReleaseAffected(release):
		return fmt.Sprintf("MongoDB %s starts on this kernel as it is; only MongoDB 8 and later need %s=%s",
			cmp.Or(release, "of an unknown release"), deploy.MongoKernelTunables, deploy.MongoKernelTunable)
	case deploy.MongoKernelMitigated(env[deploy.MongoKernelTunables]):
		return fmt.Sprintf("its environment already sets %s=%s", deploy.MongoKernelTunables, env[deploy.MongoKernelTunables])
	}
	return ""
}

// mongoKernelAlertLine is what a host alert says of a container that is down
// because it is MongoDB 8 on such a kernel, read from the listing alone (its
// image) so a pass inspects nothing more for it; empty for any other.
func mongoKernelAlertLine(kernel string, ct dockerx.Container) string {
	if _, applied := ct.Labels[mongoKernelLabel]; applied || !mongoKernelNeedsMitigation(kernel, mongoImageRelease(ct.Image)) {
		return ""
	}
	return fmt.Sprintf("It is MongoDB %s, which refuses to start on this server's kernel (Linux %s, MongoDB %s) without %s=%s; the Databases page applies that.",
		mongoImageRelease(ct.Image), kernel, deploy.MongoKernelTicket, deploy.MongoKernelTunables, deploy.MongoKernelTunable)
}

// POST /databases/{id}/kernel-mitigation
//
// Recreates a MongoDB 8 container that cannot start on this kernel with the
// mitigation in its environment: the same configuration, volumes and name,
// through Recreate, which puts the old container back if the new one cannot
// be created. Refused, and audited either way, for a container that does not
// need it (mongoKernelUnneeded) or that compose owns. Whether the container is
// running is not asked: Docker reports a restarting one as running between
// attempts, and the mitigation on a server that runs costs only the
// performance mongod warns of.
func (s *Server) handleDBKernelMitigation(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	conn, dsn, err := s.dbConnRow(r.Context(), id)
	if err != nil {
		return err
	}
	if s.modules.docker == nil {
		return httpx.Err(http.StatusServiceUnavailable, "docker_unavailable", "this host has no Docker socket")
	}
	ctx, cancel := timeoutCtx(r, 5*time.Minute)
	defer cancel()
	refuse := func(code, message string, audit map[string]any) error {
		audit["refused"] = message
		httpx.SetAudit(r, "database.server.kernel_mitigation", conn.Name, audit)
		return httpx.Err(http.StatusConflict, code, message)
	}
	kernel := s.hostKernel(ctx)
	audit := map[string]any{"kernel": kernel}
	if conn.Driver != dbx.DriverMongo {
		return refuse("kernel_mitigation_unneeded", "the kernel mitigation is MongoDB's; this is not a MongoDB server", audit)
	}
	place := s.newDBHostView(ctx).place(ctx, conn, dsn)
	if place.Container == nil || place.foreign {
		return refuse("kernel_mitigation_unavailable", "no MongoDB container on this machine was found serving it, so there is no container to recreate", audit)
	}
	detail, err := s.modules.docker.Inspect(ctx, place.Container.ID)
	if err != nil {
		return s.dockerErr(err)
	}
	audit["container"], audit["id"] = detail.Name, detail.ID
	env := envMap(detail.Env)
	problem := mongoKernelProblemOf(kernel, detail.Image, env, place.Container.ComposeProject)
	switch {
	case problem == nil:
		return refuse("kernel_mitigation_unneeded", fmt.Sprintf("%s does not need the mitigation: %s",
			detail.Name, mongoKernelUnneeded(kernel, mongoContainerRelease(detail.Image, env), env)), audit)
	case !problem.Fixable:
		return refuse("compose_managed", problem.Detail, audit)
	}
	audit["release"] = problem.Release
	spec, err := s.modules.docker.SpecOf(ctx, detail.ID)
	if err != nil {
		return s.dockerErr(err)
	}
	audit["tunables"] = applyMongoKernelMitigation(spec, kernel)
	// Everyone reading the connection sees it restarting until it answers,
	// as they would for a restart from the power route.
	change, carriedOut := s.dbConns.begin(id, "restart"), false
	defer func() { s.dbConns.finish(id, change, carriedOut) }()
	res, err := s.modules.docker.Recreate(ctx, detail.ID, dockerx.RecreateOptions{Spec: spec}, nil)
	if err != nil {
		audit["error"] = err.Error()
		httpx.SetAudit(r, "database.server.kernel_mitigation", conn.Name, audit)
		if errors.Is(err, dockerx.ErrComposeManaged) {
			return httpx.Err(http.StatusConflict, "compose_managed", err.Error())
		}
		return s.dockerErr(err)
	}
	carriedOut = true
	warnings := append([]string{}, res.Warnings...)
	audit["newId"], audit["warnings"] = res.ID, warnings
	// The pool dialled the old container; the new one has a new socket.
	s.modules.dbs.Close(id)
	s.dbConns.stale(id)
	httpx.SetAudit(r, "database.server.kernel_mitigation", conn.Name, audit)
	httpx.JSON(w, http.StatusOK, map[string]any{
		"container": detail.Name, "kernel": kernel, "release": problem.Release,
		"tunables": audit["tunables"], "warnings": warnings,
	})
	return nil
}
