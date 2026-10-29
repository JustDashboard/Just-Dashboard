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

// mongoKernelRefusal returns the log line in which MongoDB 8 says it will not
// start on this kernel, or "" when none of the lines does. Only that stated
// refusal is matched: it is what tells a server this kernel keeps down from
// one that is down for any other reason, and from one that is only between
// two of Docker's attempts.
func mongoKernelRefusal(lines []string) string {
	for _, line := range lines {
		if strings.Contains(line, "MongoDB cannot start") && strings.Contains(line, "Linux kernel") &&
			strings.Contains(line, "incompatibility with this version of MongoDB") {
			return strings.TrimSpace(line)
		}
	}
	return ""
}

// mongoKernelRefused reports whether a container's last log lines carry
// mongoKernelRefusal. The entrypoint writes little after mongod exits, and a
// server that started writes dozens of lines, so fifty hold the refusal of a
// crash loop and push out one from before a start that worked.
func (s *Server) mongoKernelRefused(ctx context.Context, id string) bool {
	lines, closer, err := s.modules.docker.Logs(ctx, id, dockerx.LogOptions{Tail: "50"})
	if err != nil {
		return false
	}
	defer closer.Close()
	tail := []string{}
	for line := range lines {
		tail = append(tail, line.Text)
	}
	return mongoKernelRefusal(tail) != ""
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
	// Fixable reports that POST /databases/{id}/kernel-mitigation, or POST
	// /databases/kernel-mitigation for a container not connected, applies it.
	Fixable bool `json:"fixable"`
}

// mongoKernelProblem is what this kernel means for a connection's server: nil
// unless it is a MongoDB 8 container here whose environment lacks the
// mitigation and whose log says, in MongoDB's words, that it cannot start on
// this kernel (mongoKernelRefused). Docker's state is not asked: a crash loop
// reads as running for the second of each attempt. And a MongoDB release that
// learns to start on these kernels never says it, so it is never told.
func (s *Server) mongoKernelProblem(ctx context.Context, driver dbx.Driver, place dbPlacement) *dbKernelProblem {
	if driver != dbx.DriverMongo || place.Container == nil || place.foreign {
		return nil
	}
	return s.mongoContainerKernelProblem(ctx, place.Container.ID)
}

// mongoContainerKernelProblem is mongoKernelProblem for one container: one
// inspect on a kernel MongoDB 8 refuses, and its log only for a MongoDB 8
// without the mitigation; nothing at all on any other kernel.
func (s *Server) mongoContainerKernelProblem(ctx context.Context, id string) *dbKernelProblem {
	if s.modules.docker == nil || id == "" {
		return nil
	}
	kernel := s.hostKernel(ctx)
	if !deploy.MongoKernelAffected(kernel) {
		return nil
	}
	detail, err := s.modules.docker.Inspect(ctx, id)
	if err != nil {
		return nil
	}
	problem := mongoKernelProblemOf(kernel, detail.Image, envMap(detail.Env), detail.ComposeStack)
	if problem == nil || !s.mongoKernelRefused(ctx, detail.ID) {
		return nil
	}
	return problem
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
	case release == "":
		return fmt.Sprintf("neither its image's tag nor its MONGO_VERSION names a MongoDB release, and only MongoDB 8 and later need %s=%s",
			deploy.MongoKernelTunables, deploy.MongoKernelTunable)
	case !deploy.MongoReleaseAffected(release):
		return fmt.Sprintf("MongoDB %s starts on this kernel as it is; only MongoDB 8 and later need %s=%s",
			release, deploy.MongoKernelTunables, deploy.MongoKernelTunable)
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
// The mitigation for a saved connection's server: its container, found where
// the connection's page finds it, recreated by mitigateKernel. While that
// runs, everyone reading the connection sees it restarting, as they would for
// a restart from the power route.
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
	if conn.Driver != dbx.DriverMongo {
		return s.refuseKernelMitigation(r, conn.Name, map[string]any{}, "kernel_mitigation_unneeded",
			"the kernel mitigation is MongoDB's; this is not a MongoDB server")
	}
	place := s.newDBHostView(ctx).place(ctx, conn, dsn)
	if place.Container == nil || place.foreign {
		return s.refuseKernelMitigation(r, conn.Name, map[string]any{}, "kernel_mitigation_unavailable",
			"no MongoDB container on this machine was found serving it, so there is no container to recreate")
	}
	return s.mitigateKernel(ctx, w, r, conn.Name, place.Container.ID, func(recreate func() error) error {
		change, carriedOut := s.dbConns.begin(id, "restart"), false
		defer func() { s.dbConns.finish(id, change, carriedOut) }()
		if err := recreate(); err != nil {
			return err
		}
		carriedOut = true
		// The pool dialled the old container; the new one has a new socket.
		s.modules.dbs.Close(id)
		s.dbConns.stale(id)
		return nil
	})
}

// POST /databases/kernel-mitigation {"container": "<id or name>"}
//
// The same for a MongoDB container found here and not connected — on an
// affected kernel, the one provisioning made before it knew, whose adopt never
// got an answer. Once it starts, discovery connects it like any other.
func (s *Server) handleDBKernelMitigationContainer(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Container string `json:"container"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	container := strings.TrimSpace(req.Container)
	if container == "" {
		return httpx.BadRequest("name the container")
	}
	if s.modules.docker == nil {
		return httpx.Err(http.StatusServiceUnavailable, "docker_unavailable", "this host has no Docker socket")
	}
	ctx, cancel := timeoutCtx(r, 5*time.Minute)
	defer cancel()
	return s.mitigateKernel(ctx, w, r, container, container, func(recreate func() error) error { return recreate() })
}

func (s *Server) refuseKernelMitigation(r *http.Request, target string, audit map[string]any, code, message string) error {
	audit["refused"] = message
	httpx.SetAudit(r, "database.server.kernel_mitigation", target, audit)
	return httpx.Err(http.StatusConflict, code, message)
}

// mitigateKernel recreates a MongoDB 8 container that cannot start on this
// kernel with the mitigation in its environment: the same configuration,
// volumes and name, through Recreate, which puts the old container back if the
// new one cannot be created. wrap runs around the recreate. Refused, and
// audited under target either way, for a container that does not need it
// (mongoKernelUnneeded) or that compose owns. Whether the container is running
// is not asked: Docker reports a restarting one as running between attempts,
// and the mitigation on a server that runs costs only the performance mongod
// warns of.
func (s *Server) mitigateKernel(ctx context.Context, w http.ResponseWriter, r *http.Request, target, container string, wrap func(recreate func() error) error) error {
	kernel := s.hostKernel(ctx)
	audit := map[string]any{"kernel": kernel}
	detail, err := s.modules.docker.Inspect(ctx, container)
	if err != nil {
		return s.dockerErr(err)
	}
	audit["container"], audit["id"] = detail.Name, detail.ID
	env := envMap(detail.Env)
	problem := mongoKernelProblemOf(kernel, detail.Image, env, detail.ComposeStack)
	switch {
	case problem == nil:
		return s.refuseKernelMitigation(r, target, audit, "kernel_mitigation_unneeded", fmt.Sprintf(
			"%s does not need the mitigation: %s", detail.Name, mongoKernelUnneeded(kernel, mongoContainerRelease(detail.Image, env), env)))
	case !problem.Fixable:
		return s.refuseKernelMitigation(r, target, audit, "compose_managed", problem.Detail)
	}
	audit["release"] = problem.Release
	spec, err := s.modules.docker.SpecOf(ctx, detail.ID)
	if err != nil {
		return s.dockerErr(err)
	}
	audit["tunables"] = applyMongoKernelMitigation(spec, kernel)
	var res *dockerx.CreateResult
	err = wrap(func() error {
		var err error
		res, err = s.modules.docker.Recreate(ctx, detail.ID, dockerx.RecreateOptions{Spec: spec}, nil)
		return err
	})
	if err != nil {
		audit["error"] = err.Error()
		httpx.SetAudit(r, "database.server.kernel_mitigation", target, audit)
		if errors.Is(err, dockerx.ErrComposeManaged) {
			return httpx.Err(http.StatusConflict, "compose_managed", err.Error())
		}
		return s.dockerErr(err)
	}
	warnings := append([]string{}, res.Warnings...)
	audit["newId"], audit["warnings"] = res.ID, warnings
	httpx.SetAudit(r, "database.server.kernel_mitigation", target, audit)
	httpx.JSON(w, http.StatusOK, map[string]any{
		"container": detail.Name, "kernel": kernel, "release": problem.Release,
		"tunables": audit["tunables"], "warnings": warnings,
	})
	return nil
}

// inventoryKernelProblems is, by instance key, mongoContainerKernelProblem for
// each MongoDB container discovery found here that no connection is made to:
// what Found on this server offers the fix on. Nil on a kernel MongoDB 8
// starts on, which costs every other machine one uname.
func (s *Server) inventoryKernelProblems(ctx context.Context, instances []dbx.Instance) map[string]*dbKernelProblem {
	if s.modules.docker == nil || !deploy.MongoKernelAffected(s.hostKernel(ctx)) {
		return nil
	}
	out := map[string]*dbKernelProblem{}
	for _, inst := range instances {
		if inst.Kind != dbx.KindServer || inst.Driver != dbx.DriverMongo || inst.Container == nil ||
			len(inst.Connections) > 0 {
			continue
		}
		if problem := s.mongoContainerKernelProblem(ctx, inst.Container.ID); problem != nil {
			out[inst.Key] = problem
		}
	}
	return out
}
