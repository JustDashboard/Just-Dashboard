package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
)

func (s *Server) mountDockerRoutes(r chi.Router) {
	r.Route("/docker", func(r chi.Router) {
		r.Method(http.MethodGet, "/ping", s.handle(s.handleDockerPing))
		r.Method(http.MethodGet, "/info", s.handle(s.handleDockerInfo))
		r.Method(http.MethodGet, "/disk-usage", s.handle(s.handleDockerDiskUsage))
		// Starting points for the container form, rendered from the same
		// reviewed blueprint catalogue Deployments uses.
		r.Method(http.MethodGet, "/templates", s.handle(s.handleDockerTemplates))

		// What is wrong with Docker on this host, in sentences. Read-only: it
		// only reports, and every remedy it suggests is a separate route with
		// its own capability check.
		r.Method(http.MethodGet, "/health", s.handle(s.handleDockerDiagnose))

		// What the daemon did while nobody was watching.
		r.Method(http.MethodGet, "/events", s.handle(s.handleDockerEvents))
		r.Method(http.MethodGet, "/events/stream", s.handle(s.handleDockerEventStream))

		r.Route("/containers", func(r chi.Router) {
			r.Method(http.MethodGet, "/", s.handle(s.handleContainerList))
			r.Method(http.MethodGet, "/stats", s.handle(s.handleContainerStatsAll))
			r.Method(http.MethodGet, "/stream", s.handle(s.handleContainerStream))
			r.Method(http.MethodGet, "/{id}", s.handle(s.handleContainerInspect))
			r.Method(http.MethodGet, "/{id}/raw", s.handle(s.handleContainerRaw))
			r.Method(http.MethodGet, "/{id}/spec", s.handle(s.handleContainerSpec))
			r.Method(http.MethodGet, "/{id}/changes", s.handle(s.handleContainerChanges))
			// Where the writable layer went, and why it keeps failing. Both
			// are lazy by design: the first walks a filesystem and the second
			// reads the event log, and neither belongs in a listing that
			// polls.
			r.Method(http.MethodGet, "/{id}/writable-layer", s.handle(s.handleWritableLayer))
			r.Method(http.MethodGet, "/{id}/migration-plan", s.handle(s.handleMigrationPlan))
			r.Method(http.MethodGet, "/{id}/failure", s.handle(s.handleContainerFailure))
			r.Method(http.MethodGet, "/{id}/anomalies", s.handle(s.handleContainerAnomalies))
			// Where a published port is actually reachable, including
			// through the reverse proxy this dashboard also manages.
			r.Method(http.MethodGet, "/{id}/routes", s.handle(s.handleContainerRoutes))
			r.Method(http.MethodGet, "/{id}/logs", s.handle(s.handleContainerLogs))
			r.Method(http.MethodGet, "/{id}/logs/stream", s.handle(s.handleContainerLogStream))
			r.Method(http.MethodGet, "/{id}/stats/stream", s.handle(s.handleContainerStatStream))
			r.Method(http.MethodGet, "/stats/history", s.handle(s.handleContainerSparklines))
			r.Method(http.MethodGet, "/{id}/stats/history", s.handle(s.handleContainerStatsHistory))

			r.Group(func(r chi.Router) {
				r.Use(httpx.RequireCapability(auth.CapServiceControl))
				r.Method(http.MethodPost, "/{id}/start", s.handle(s.containerLifecycle(dockerx.ActionStart)))
				r.Method(http.MethodPost, "/{id}/pause", s.handle(s.containerLifecycle(dockerx.ActionPause)))
				r.Method(http.MethodPost, "/{id}/unpause", s.handle(s.containerLifecycle(dockerx.ActionUnpause)))
				// Creating a container is the strongest primitive the Docker
				// socket offers, so the handler applies a second check by
				// hand: a spec that is privileged or mounts a host path
				// additionally requires system.admin. That is the same shape
				// as POST /databases/{id}/query, where what the request is
				// allowed to do depends on what is in it.
				r.Method(http.MethodPost, "/", s.handle(s.handleContainerCreate))
				// Renders a spec as the `docker run` and compose that would
				// produce it. Reads nothing and changes nothing, but it is
				// only reachable by someone who could create the container,
				// so it sits in the same group as the form it belongs to.
				r.Method(http.MethodPost, "/preview", s.handle(s.handleContainerPreview))
				r.Method(http.MethodPost, "/{id}/rename", s.handle(s.handleContainerRename))
				r.Method(http.MethodPatch, "/{id}/resources", s.handle(s.handleContainerResources))
			})
			// Stop, restart and kill interrupt a running service, so they
			// carry the typed-confirmation requirement even though the
			// container itself survives. Recreate destroys the container and
			// builds a new one in its place, which is the same bargain with
			// higher stakes.
			s.destructive(r, func(r chi.Router) {
				r.Method(http.MethodPost, "/{id}/stop", s.handle(s.containerLifecycle(dockerx.ActionStop)))
				r.Method(http.MethodPost, "/{id}/restart", s.handle(s.containerLifecycle(dockerx.ActionRestart)))
				r.Method(http.MethodPost, "/{id}/kill", s.handle(s.containerLifecycle(dockerx.ActionKill)))
				r.Method(http.MethodPost, "/{id}/recreate", s.handle(s.handleContainerRecreate))
				r.Method(http.MethodDelete, "/{id}", s.handle(s.handleContainerRemove))
				r.Method(http.MethodPost, "/prune", s.handle(s.handleContainerPrune))
			})
			r.Group(func(r chi.Router) {
				r.Use(httpx.RequireCapability(auth.CapTerminal))
				r.Method(http.MethodGet, "/{id}/exec", s.handle(s.handleContainerExec))
			})
		})

		r.Route("/images", func(r chi.Router) {
			r.Method(http.MethodGet, "/", s.handle(s.handleImageList))
			// Registered above /{id} so chi matches the literal segment
			// first — otherwise "updates" is read as an image id.
			r.Method(http.MethodGet, "/updates", s.handle(s.handleImageUpdates))
			r.Method(http.MethodGet, "/{id}", s.handle(s.handleImageDetail))
			r.Group(func(r chi.Router) {
				r.Use(httpx.RequireCapability(auth.CapServiceControl))
				r.Method(http.MethodGet, "/pull", s.handle(s.handleImagePull))
				r.Method(http.MethodPost, "/{id}/tag", s.handle(s.handleImageTag))
				r.Method(http.MethodGet, "/build", s.handle(s.handleImageBuild))
			})
			s.destructive(r, func(r chi.Router) {
				r.Method(http.MethodDelete, "/{id}", s.handle(s.handleImageRemove))
				r.Method(http.MethodPost, "/prune", s.handle(s.handleImagePrune))
			})
		})

		r.Route("/volumes", func(r chi.Router) {
			r.Method(http.MethodGet, "/", s.handle(s.handleVolumeList))
			r.Method(http.MethodGet, "/{name}", s.handle(s.handleVolumeInspect))
			r.Group(func(r chi.Router) {
				r.Use(httpx.RequireCapability(auth.CapServiceControl))
				r.Method(http.MethodPost, "/", s.handle(s.handleVolumeCreate))
			})
			s.destructive(r, func(r chi.Router) {
				r.Method(http.MethodDelete, "/{name}", s.handle(s.handleVolumeRemove))
				r.Method(http.MethodPost, "/prune", s.handle(s.handleVolumePrune))
			})
		})

		r.Route("/networks", func(r chi.Router) {
			r.Method(http.MethodGet, "/", s.handle(s.handleNetworkList))
			r.Method(http.MethodGet, "/{id}", s.handle(s.handleNetworkInspect))
			r.Group(func(r chi.Router) {
				r.Use(httpx.RequireCapability(auth.CapServiceControl))
				r.Method(http.MethodPost, "/", s.handle(s.handleNetworkCreate))
				// Reversible in one click, and the fix for the commonest
				// Docker problem there is, so it is not behind a typed
				// phrase — the cost of getting it wrong is reconnecting.
				r.Method(http.MethodPost, "/{id}/connect", s.handle(s.handleNetworkConnect))
				r.Method(http.MethodPost, "/{id}/disconnect", s.handle(s.handleNetworkDisconnect))
			})
			s.destructive(r, func(r chi.Router) {
				r.Method(http.MethodDelete, "/{id}", s.handle(s.handleNetworkRemove))
				r.Method(http.MethodPost, "/prune", s.handle(s.handleNetworkPrune))
			})
		})

		r.Route("/stacks", func(r chi.Router) {
			r.Method(http.MethodGet, "/", s.handle(s.handleStackList))
			r.Method(http.MethodGet, "/{name}", s.handle(s.handleStackDetail))
			r.Method(http.MethodGet, "/{name}/config", s.handle(s.handleStackConfig))
			r.Method(http.MethodGet, "/{name}/logs/stream", s.handle(s.handleStackLogStream))
			// What a deploy would do, and what the last few did. Read-only:
			// the preview changes nothing and the history is a record.
			r.Method(http.MethodGet, "/{name}/preview", s.handle(s.handleStackPreview))
			r.Method(http.MethodGet, "/{name}/deployments", s.handle(s.handleStackDeployments))
			r.Method(http.MethodGet, "/{name}/deployments/{id}", s.handle(s.handleStackDeployment))
			r.Group(func(r chi.Router) {
				r.Use(httpx.RequireCapability(auth.CapServiceControl))
				r.Method(http.MethodPost, "/{name}/up", s.handle(s.stackAction(dockerx.ComposeUp)))
				r.Method(http.MethodPost, "/{name}/start", s.handle(s.stackAction(dockerx.ComposeStart)))
				r.Method(http.MethodPost, "/{name}/pull", s.handle(s.stackAction(dockerx.ComposePull)))
				r.Method(http.MethodPost, "/{name}/build", s.handle(s.stackAction(dockerx.ComposeBuild)))
				// The streaming runner. A socket rather than a POST because
				// `up` on a stack that has to pull and build takes minutes,
				// and a request that hangs for minutes is indistinguishable
				// from a broken dashboard. Which action it runs is decided
				// inside the handler, where the destructive ones can be given
				// the same confirmation and budget the POSTs above get.
				r.Method(http.MethodGet, "/{name}/run", s.handle(s.handleStackRun))
				r.Method(http.MethodPost, "/{name}/validate", s.handle(s.handleStackValidate))
			})
			r.Group(func(r chi.Router) {
				// Editing a compose file is editing a file on the server, and
				// is gated as one. Creating a stack writes a new one.
				r.Use(httpx.RequireCapability(auth.CapFileWrite))
				r.Method(http.MethodPut, "/{name}/config", s.handle(s.handleStackConfigWrite))
				r.Method(http.MethodPost, "/", s.handle(s.handleStackCreate))
			})
			s.destructive(r, func(r chi.Router) {
				r.Method(http.MethodPost, "/{name}/down", s.handle(s.stackAction(dockerx.ComposeDown)))
				r.Method(http.MethodPost, "/{name}/stop", s.handle(s.stackAction(dockerx.ComposeStop)))
				r.Method(http.MethodPost, "/{name}/restart", s.handle(s.stackAction(dockerx.ComposeRestart)))
				r.Method(http.MethodPost, "/{name}/update", s.handle(s.stackAction(dockerx.ComposeUpdate)))
				r.Method(http.MethodPost, "/{name}/recreate", s.handle(s.stackAction(dockerx.ComposeRecreate)))
			})
		})

		// What each category of cleanup holds and what removing it costs.
		// Reading it is safe for anyone who can read the disk page.
		r.Method(http.MethodGet, "/cleanup/preview", s.handle(s.handleCleanupPreview))

		s.destructive(r, func(r chi.Router) {
			r.Method(http.MethodPost, "/prune", s.handle(s.handlePruneAll))
			r.Method(http.MethodPost, "/build-cache/prune", s.handle(s.handleBuildCachePrune))
			// The category-selected sweep. Same gate as prune, and the
			// handler additionally requires the typed phrase when volumes are
			// among the categories — that is the one selection here that
			// destroys data.
			r.Method(http.MethodPost, "/cleanup", s.handle(s.handleCleanupRun))
		})
	})
}

// dockerErr turns a daemon error into something a person can act on.
//
// Docker's own messages are precise and frequently unreadable — "invalid
// reference format: repository name (library/sha256…) must be lowercase" is
// four true statements none of which is "you asked for an image by its id and
// something treated it as a name". The raw text is always kept, because an
// operator debugging this needs exactly what the daemon said; what is added is
// the reading of it.
func (s *Server) dockerErr(err error) error {
	raw := err.Error()
	lower := strings.ToLower(raw)

	switch {
	case errors.Is(err, dockerx.ErrUnavailable):
		return httpx.Err(http.StatusServiceUnavailable, "docker_unavailable",
			"The Docker daemon is not reachable from this dashboard.").
			Because("The socket is not there, or this process cannot read it. On most hosts that is the daemon being stopped, or the dashboard's container not having /var/run/docker.sock mounted.", raw).
			Retry()

	case strings.Contains(lower, "no such"):
		return httpx.Err(http.StatusNotFound, "not_found",
			"Docker does not have that object.").
			Because("It may have been removed since this page last loaded — by a compose deploy, a cleanup, or somebody in a shell.", raw).
			Retry()

	case strings.Contains(lower, "invalid reference format"):
		return httpx.Err(http.StatusBadRequest, "invalid_reference",
			"That image name is not one Docker will accept.").
			Because("A Docker reference is lower-case, and an image id (sha256:…) is not a name — asking for one as though it were produces exactly this message, with 'library/' prepended by the daemon.", raw)

	case strings.Contains(lower, "conflict") && strings.Contains(lower, "in use"):
		return httpx.Err(http.StatusConflict, "in_use",
			"Docker will not remove that while something is using it.").
			Because("Removing it would leave whatever depends on it broken, so the daemon refuses. The panel lists what is using it.", raw)

	case strings.Contains(lower, "already in use") || strings.Contains(lower, "conflict"):
		return httpx.Err(http.StatusConflict, "conflict",
			"Something with that name already exists.").
			Because("Docker names are unique per kind. Pick another name, or remove the existing object first.", raw)

	case strings.Contains(lower, "port is already allocated") || strings.Contains(lower, "address already in use"):
		return httpx.Err(http.StatusConflict, "port_taken",
			"That host port is already taken.").
			Because("Another container or a process on the host is bound to it. The Ports page lists what holds each one.", raw)

	case strings.Contains(lower, "no space left"):
		return httpx.Err(http.StatusInsufficientStorage, "no_space",
			"The server has run out of disk.").
			Because("Docker could not write what it needed. The Disk panel shows what is reclaimable, and the cleanup preview shows what removing each part would cost.", raw)

	case strings.Contains(lower, "permission denied"), strings.Contains(lower, "access denied"):
		return httpx.Err(http.StatusForbidden, "denied",
			"Docker refused the operation.").
			Because("Either the daemon's own permissions, or a registry asking for credentials this dashboard does not hold.", raw)

	case strings.Contains(lower, "context deadline exceeded"), strings.Contains(lower, "timeout"):
		return httpx.Err(http.StatusGatewayTimeout, "timeout",
			"Docker did not answer in time.").
			Because("A daemon walking a large layer or a slow registry can take longer than the request allows. Nothing was necessarily left half-done — check the object's current state before retrying.", raw).
			Retry()

	default:
		return httpx.Err(http.StatusBadGateway, "docker_error",
			"Docker refused the operation.").
			Because("The daemon's own explanation is below.", raw)
	}
}

func (s *Server) handleDockerPing(w http.ResponseWriter, r *http.Request) error {
	httpx.JSON(w, http.StatusOK, s.modules.docker.Ping(r.Context()))
	return nil
}

func (s *Server) handleDockerInfo(w http.ResponseWriter, r *http.Request) error {
	info, err := s.modules.docker.Info(r.Context())
	if err != nil {
		return s.dockerErr(err)
	}
	httpx.JSON(w, http.StatusOK, info)
	return nil
}

func (s *Server) handleDockerDiskUsage(w http.ResponseWriter, r *http.Request) error {
	du, err := s.modules.docker.DiskUsage(r.Context())
	if err != nil {
		return s.dockerErr(err)
	}
	httpx.JSON(w, http.StatusOK, du)
	return nil
}

func (s *Server) handleContainerList(w http.ResponseWriter, r *http.Request) error {
	all := r.URL.Query().Get("all") != "false"
	list, err := s.modules.docker.ListContainers(r.Context(), all)
	if err != nil {
		return s.dockerErr(err)
	}
	httpx.JSON(w, http.StatusOK, list)
	return nil
}

func (s *Server) handleContainerInspect(w http.ResponseWriter, r *http.Request) error {
	detail, err := s.modules.docker.Inspect(r.Context(), httpx.URLParam(r, "id"))
	if err != nil {
		return s.dockerErr(err)
	}
	// Every authenticated role may read container detail, and a container's
	// environment routinely holds the master key, database passwords and
	// deploy credentials. Anyone below system.admin gets them redacted here,
	// on the server, so a readonly API token cannot lift them either.
	//
	// This raises the cost of reading a secret; it does not make it
	// impossible, and it was never meant to be read as though it did. Below
	// system.admin the same values are still reachable through the compose
	// file at /docker/stacks/{name}/config, through /files/read within
	// JD_FILE_ROOTS, and through a deploy run's log. Every role sees
	// everything on this box by design — see the roles table in the README —
	// and narrowing that is a decision about the product, not about this
	// handler.
	if !httpx.MustPrincipal(r).Can(auth.CapSystemAdmin) {
		detail.Env = dockerx.RedactEnv(detail.Env)
	}
	httpx.JSON(w, http.StatusOK, detail)
	return nil
}

func (s *Server) handleContainerStatsAll(w http.ResponseWriter, r *http.Request) error {
	list, err := s.modules.docker.ListContainers(r.Context(), false)
	if err != nil {
		return s.dockerErr(err)
	}
	ids := make([]string, 0, len(list))
	for _, c := range list {
		if c.State == "running" {
			ids = append(ids, c.ID)
		}
	}
	// The shared sampler, not a fresh one: a single request has no previous
	// sample of its own to difference against, and would answer 0% for every
	// container. The recorder keeps this one warm.
	stats, err := s.modules.dockerStats.Sample(r.Context(), ids)
	if err != nil {
		return s.dockerErr(err)
	}
	httpx.JSON(w, http.StatusOK, stats)
	return nil
}

// handleContainerStatsHistory answers what a container was doing before you
// looked at it.
//
// The live stats socket, like the host one, only ever describes the time since
// the panel was opened — so a container that was OOM-killed at 03:00, or that
// pinned a core for twenty minutes overnight, left nothing behind anywhere in
// the dashboard. This reads the series the metrics recorder has been keeping.
//
// The path parameter may be a container id or a name. History is keyed by
// name, because a compose redeploy replaces the container with a new id and
// the series has to continue across that; an id is resolved to its name first.
func (s *Server) handleContainerStatsHistory(w http.ResponseWriter, r *http.Request) error {
	if !s.modules.metrics.Enabled() {
		return httpx.Err(http.StatusServiceUnavailable, "metrics_history_disabled",
			"metrics history is not being recorded on this host (JD_METRICS_RETENTION=0)")
	}
	name := s.containerName(r.Context(), httpx.URLParam(r, "id"))
	if name == "" {
		return httpx.BadRequest("a container id or name is required")
	}
	from, to, points, err := historyWindow(r)
	if err != nil {
		return err
	}
	series, err := s.modules.metrics.ContainerRange(r.Context(), name, from, to, points)
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, series)
	return nil
}

// handleContainerSparklines gives the container table a trend per row.
//
// One request for every container rather than one per row: a host running
// forty containers would otherwise answer a page load with forty queries to
// draw forty thumbnails, which is how a monitoring feature turns into the load
// it exists to watch.
//
// Registered above /{id}/stats/history so chi matches the literal segment
// first — otherwise "stats" would be read as a container id.
func (s *Server) handleContainerSparklines(w http.ResponseWriter, r *http.Request) error {
	if !s.modules.metrics.Enabled() {
		return httpx.Err(http.StatusServiceUnavailable, "metrics_history_disabled",
			"metrics history is not being recorded on this host (JD_METRICS_RETENTION=0)")
	}
	from, to, points, err := historyWindow(r)
	if err != nil {
		return err
	}
	// A hard ceiling on the width regardless of what was asked for: this
	// endpoint draws thumbnails, and a hundred points in a forty-pixel chart
	// is bandwidth spent on pixels that do not exist.
	if points > 60 {
		points = 60
	}
	lines, err := s.modules.metrics.Sparklines(r.Context(), from, to, points)
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, lines)
	return nil
}

// containerName resolves an id to the name the history is keyed by, and passes
// anything it cannot resolve through unchanged — a container that no longer
// exists cannot be inspected, and its history is exactly what someone asking
// about it wants.
func (s *Server) containerName(ctx context.Context, ref string) string {
	if ref == "" {
		return ""
	}
	if detail, err := s.modules.docker.Inspect(ctx, ref); err == nil && detail.Name != "" {
		return detail.Name
	}
	return ref
}

// handleContainerStream is the `docker stats`-equivalent feed backing the
// container table: one socket, one sampling loop, all running containers.
func (s *Server) handleContainerStream(w http.ResponseWriter, r *http.Request) error {
	conn, err := s.WS.Upgrade(w, r)
	if err != nil {
		return nil
	}
	defer conn.Close()
	ctx, cancel := contextWithCancel(r)
	defer cancel()
	go conn.Keepalive(ctx)
	go conn.DrainControl(cancel)

	// A sampler per socket, so each client's CPU deltas span its own even
	// intervals rather than whatever the last caller happened to leave behind.
	sampler := s.modules.docker.NewStatsSampler()

	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		list, err := s.modules.docker.ListContainers(ctx, true)
		if err != nil {
			conn.SendError(err.Error())
			return nil
		}
		if err := conn.Send("containers", list); err != nil {
			return nil
		}
		ids := make([]string, 0, len(list))
		for _, c := range list {
			if c.State == "running" {
				ids = append(ids, c.ID)
			}
		}
		if stats, err := sampler.Sample(ctx, ids); err == nil {
			if err := conn.Send("stats", stats); err != nil {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

func (s *Server) handleContainerLogs(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	ch, closer, err := s.modules.docker.Logs(r.Context(), httpx.URLParam(r, "id"), dockerx.LogOptions{
		Tail:       defaultStr(q.Get("tail"), "500"),
		Since:      q.Get("since"),
		Until:      q.Get("until"),
		Timestamps: q.Get("timestamps") == "true",
	})
	if err != nil {
		return s.dockerErr(err)
	}
	defer closer.Close()
	lines := []dockerx.LogLine{}
	for line := range ch {
		lines = append(lines, line)
		if len(lines) >= 20000 {
			break
		}
	}
	httpx.JSON(w, http.StatusOK, lines)
	return nil
}

func (s *Server) handleContainerLogStream(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	conn, err := s.WS.Upgrade(w, r)
	if err != nil {
		return nil
	}
	defer conn.Close()
	ctx, cancel := contextWithCancel(r)
	defer cancel()
	go conn.Keepalive(ctx)
	go conn.DrainControl(cancel)

	ch, closer, err := s.modules.docker.Logs(ctx, httpx.URLParam(r, "id"), dockerx.LogOptions{
		Tail:       defaultStr(q.Get("tail"), "500"),
		Timestamps: q.Get("timestamps") == "true",
		Follow:     true,
	})
	if err != nil {
		conn.SendError(err.Error())
		return nil
	}
	defer closer.Close()

	// Lines are batched on a short tick: a chatty container can emit
	// thousands per second, and one frame each would drown the browser.
	batch := make([]dockerx.LogLine, 0, 256)
	flush := time.NewTicker(150 * time.Millisecond)
	defer flush.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case line, ok := <-ch:
			if !ok {
				if len(batch) > 0 {
					conn.Send("logs", batch)
				}
				conn.Send("eof", nil)
				return nil
			}
			batch = append(batch, line)
			if len(batch) >= 256 {
				if err := conn.Send("logs", batch); err != nil {
					return nil
				}
				batch = batch[:0]
			}
		case <-flush.C:
			if len(batch) > 0 {
				if err := conn.Send("logs", batch); err != nil {
					return nil
				}
				batch = batch[:0]
			}
		}
	}
}

func (s *Server) handleContainerStatStream(w http.ResponseWriter, r *http.Request) error {
	conn, err := s.WS.Upgrade(w, r)
	if err != nil {
		return nil
	}
	defer conn.Close()
	ctx, cancel := contextWithCancel(r)
	defer cancel()
	go conn.Keepalive(ctx)
	go conn.DrainControl(cancel)

	out := make(chan dockerx.ContainerStats, 8)
	go func() {
		defer close(out)
		if err := s.modules.docker.StatsStream(ctx, httpx.URLParam(r, "id"), out); err != nil {
			conn.SendError(err.Error())
		}
	}()
	for st := range out {
		if err := conn.Send("stats", st); err != nil {
			return nil
		}
	}
	return nil
}

func (s *Server) containerLifecycle(action dockerx.LifecycleAction) httpx.Handler {
	return func(w http.ResponseWriter, r *http.Request) error {
		id := httpx.URLParam(r, "id")
		detail, err := s.modules.docker.Inspect(r.Context(), id)
		if err != nil {
			return s.dockerErr(err)
		}
		// No typed phrase on stop, restart or kill. They are the three most
		// pressed buttons in a Docker panel and every one of them is undone by
		// pressing start, so a typing exercise in front of them bought nothing
		// and cost the phrase its meaning everywhere else. The dialog still
		// names the container, which is what stops the mis-clicked row.
		var timeout *int
		if v := r.URL.Query().Get("timeout"); v != "" {
			t := atoiDefault(v, 10)
			timeout = &t
		}
		if err := s.modules.docker.Lifecycle(r.Context(), id, action, timeout); err != nil {
			return s.dockerErr(err)
		}
		httpx.SetAudit(r, "docker.container."+string(action), detail.Name,
			map[string]any{"id": id, "image": detail.Image})
		httpx.NoContent(w)
		return nil
	}
}

func (s *Server) handleContainerRemove(w http.ResponseWriter, r *http.Request) error {
	id := httpx.URLParam(r, "id")
	detail, err := s.modules.docker.Inspect(r.Context(), id)
	if err != nil {
		return s.dockerErr(err)
	}
	// No typed phrase: a container is a process plus a spec, and this panel can
	// render both back as a docker run line or a compose service. What would
	// not survive is data written inside the container rather than to a volume
	// — which is a finding Diagnose already raises, on the container, before
	// anybody reaches this button.
	force := r.URL.Query().Get("force") == "true"
	volumes := r.URL.Query().Get("volumes") == "true"
	if err := s.modules.docker.RemoveContainer(r.Context(), id, force, volumes); err != nil {
		return s.dockerErr(err)
	}
	httpx.SetAudit(r, "docker.container.remove", detail.Name,
		map[string]any{"id": id, "force": force, "removeVolumes": volumes})
	httpx.NoContent(w)
	return nil
}

func (s *Server) handleContainerPrune(w http.ResponseWriter, r *http.Request) error {
	space, deleted, err := s.modules.docker.PruneContainers(r.Context())
	if err != nil {
		return s.dockerErr(err)
	}
	httpx.SetAudit(r, "docker.container.prune", "", map[string]any{"deleted": len(deleted), "reclaimed": space})
	httpx.JSON(w, http.StatusOK, dockerx.PruneReport{Kind: "containers", SpaceReclaimed: space, Items: deleted})
	return nil
}

// handleContainerExec bridges a browser terminal to a shell inside the
// container. This is root-equivalent access to the host in practice, so the
// session is logged the moment it opens rather than at close, when a crashed
// process might have swallowed the record.
func (s *Server) handleContainerExec(w http.ResponseWriter, r *http.Request) error {
	id := httpx.URLParam(r, "id")
	detail, err := s.modules.docker.Inspect(r.Context(), id)
	if err != nil {
		return s.dockerErr(err)
	}
	p := httpx.MustPrincipal(r)
	s.recordAudit(r, "docker.container.exec.open", detail.Name, map[string]any{"id": id})

	conn, err := s.WS.Upgrade(w, r)
	if err != nil {
		return nil
	}
	defer conn.Close()
	ctx, cancel := contextWithCancel(r)
	defer cancel()
	go conn.Keepalive(ctx)

	q := r.URL.Query()
	var cmd []string
	if c := q.Get("cmd"); c != "" {
		cmd = strings.Fields(c)
	}
	sess, err := s.modules.docker.Exec(ctx, id, cmd, q.Get("user"),
		uint(atoiDefault(q.Get("rows"), 24)), uint(atoiDefault(q.Get("cols"), 80)))
	if err != nil {
		conn.SendError(err.Error())
		return nil
	}
	defer sess.Close()

	go func() {
		buf := make([]byte, 8192)
		for {
			n, err := sess.Conn.Read(buf)
			if n > 0 {
				if err := conn.WriteBinary(buf[:n]); err != nil {
					cancel()
					return
				}
			}
			if err != nil {
				if err != io.EOF {
					conn.SendError(err.Error())
				}
				cancel()
				return
			}
		}
	}()

	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			break
		}
		// Control frames are JSON; everything else is raw keystrokes. A
		// resize is the only control message an exec session needs.
		if len(data) > 0 && data[0] == '{' {
			var ctrl struct {
				Type string `json:"type"`
				Rows uint   `json:"rows"`
				Cols uint   `json:"cols"`
			}
			if json.Unmarshal(data, &ctrl) == nil && ctrl.Type == "resize" {
				sess.Resize(ctx, ctrl.Rows, ctrl.Cols)
				continue
			}
		}
		if _, err := sess.Conn.Write(data); err != nil {
			break
		}
	}
	s.recordAudit(r, "docker.container.exec.close", detail.Name,
		map[string]any{"id": id, "user": p.Username()})
	return nil
}

func (s *Server) handleImageList(w http.ResponseWriter, r *http.Request) error {
	list, err := s.modules.docker.ListImages(r.Context(), r.URL.Query().Get("all") == "true")
	if err != nil {
		return s.dockerErr(err)
	}
	httpx.JSON(w, http.StatusOK, list)
	return nil
}

// handleImagePull streams layer progress over a socket; a pull of a large
// image otherwise looks indistinguishable from a hung request.
func (s *Server) handleImagePull(w http.ResponseWriter, r *http.Request) error {
	ref := dockerx.ImageRef(r.URL.Query().Get("ref"))
	if ref == "" {
		return httpx.BadRequest("ref query parameter is required")
	}
	s.recordAudit(r, "docker.image.pull", ref, nil)

	conn, err := s.WS.Upgrade(w, r)
	if err != nil {
		return nil
	}
	defer conn.Close()
	ctx, cancel := contextWithCancel(r)
	defer cancel()
	go conn.Keepalive(ctx)
	go conn.DrainControl(cancel)

	out := make(chan dockerx.PullProgress, 32)
	done := make(chan error, 1)
	go func() { done <- s.modules.docker.PullImage(ctx, ref, out); close(out) }()
	for msg := range out {
		if err := conn.Send("progress", msg); err != nil {
			cancel()
			break
		}
	}
	if err := <-done; err != nil && ctx.Err() == nil {
		conn.SendError(err.Error())
		return nil
	}
	conn.Send("done", map[string]string{"ref": ref})
	return nil
}

func (s *Server) handleImageRemove(w http.ResponseWriter, r *http.Request) error {
	id := httpx.URLParam(r, "id")
	// No typed phrase: an image is reproducible — it came from a registry or a
	// Dockerfile this dashboard can rebuild. Pruning many at once still asks,
	// because that is the sweep nobody can enumerate in advance.
	//
	// The tag is resolved before the removal rather than after, because after
	// it there is nothing left to resolve it from — and a trail saying which
	// image went is worth more than one holding a bare digest.
	name := s.imageName(r.Context(), id)
	res, err := s.modules.docker.RemoveImage(r.Context(), id,
		r.URL.Query().Get("force") == "true", true)
	if err != nil {
		return s.dockerErr(err)
	}
	httpx.SetAudit(r, "docker.image.remove", name, map[string]any{"id": id, "result": res})
	httpx.JSON(w, http.StatusOK, res)
	return nil
}

func (s *Server) handleImagePrune(w http.ResponseWriter, r *http.Request) error {
	all := r.URL.Query().Get("all") == "true"
	rep, err := s.modules.docker.PruneImages(r.Context(), all)
	if err != nil {
		return s.dockerErr(err)
	}
	httpx.SetAudit(r, "docker.image.prune", "", map[string]any{"all": all, "reclaimed": rep.SpaceReclaimed})
	httpx.JSON(w, http.StatusOK, rep)
	return nil
}

// handleVolumeList joins the volume list to the containers using each one.
//
// The join is what makes the delete button honest. Docker's own RefCount
// counts running containers only, so a volume belonging to a stopped stack
// reads as unused — and that is precisely the volume an operator prunes by
// accident, along with the only copy of whatever was in it.
func (s *Server) handleVolumeList(w http.ResponseWriter, r *http.Request) error {
	list, err := s.modules.docker.ListVolumesWithUsers(r.Context())
	if err != nil {
		return s.dockerErr(err)
	}
	httpx.JSON(w, http.StatusOK, list)
	return nil
}

func (s *Server) handleVolumeInspect(w http.ResponseWriter, r *http.Request) error {
	v, err := s.modules.docker.VolumeDetail(r.Context(), httpx.URLParam(r, "name"))
	if err != nil {
		return s.dockerErr(err)
	}
	httpx.JSON(w, http.StatusOK, v)
	return nil
}

func (s *Server) handleVolumeRemove(w http.ResponseWriter, r *http.Request) error {
	name := httpx.URLParam(r, "name")
	// Typed, unlike the container, image and network beside it: a volume is the
	// one Docker object that *is* the data. Everything else on this page can be
	// rebuilt from a registry or a spec; this cannot be rebuilt from anything.
	if err := httpx.RequireTypedConfirmation(w, r, name); err != nil {
		return err
	}
	if err := s.modules.docker.RemoveVolume(r.Context(), name, r.URL.Query().Get("force") == "true"); err != nil {
		return s.dockerErr(err)
	}
	httpx.SetAudit(r, "docker.volume.remove", name, nil)
	httpx.NoContent(w)
	return nil
}

func (s *Server) handleVolumePrune(w http.ResponseWriter, r *http.Request) error {
	if err := httpx.RequireTypedConfirmation(w, r, "prune volumes"); err != nil {
		return err
	}
	rep, err := s.modules.docker.PruneVolumes(r.Context())
	if err != nil {
		return s.dockerErr(err)
	}
	httpx.SetAudit(r, "docker.volume.prune", "", map[string]any{"deleted": rep.Items})
	httpx.JSON(w, http.StatusOK, rep)
	return nil
}

// handleNetworkList joins the network list to the containers attached to each
// one, for the same reason handleVolumeList does.
//
// Docker's network listing leaves the container map empty — only an inspect
// fills it — so the count this page used to show was structurally zero for
// every network on every host, and the delete dialog read "nothing is attached
// to it" over a network carrying a running stack.
func (s *Server) handleNetworkList(w http.ResponseWriter, r *http.Request) error {
	list, err := s.modules.docker.ListNetworks(r.Context())
	if err != nil {
		return s.dockerErr(err)
	}
	httpx.JSON(w, http.StatusOK, list)
	return nil
}

// handleNetworkInspect returns the network and, more usefully, who is on it
// and what name each of them answers to. "These two containers cannot see each
// other" is the commonest Docker problem there is and its answer is nearly
// always here.
func (s *Server) handleNetworkInspect(w http.ResponseWriter, r *http.Request) error {
	n, err := s.modules.docker.NetworkDetail(r.Context(), httpx.URLParam(r, "id"))
	if err != nil {
		return s.dockerErr(err)
	}
	httpx.JSON(w, http.StatusOK, n)
	return nil
}

func (s *Server) handleNetworkRemove(w http.ResponseWriter, r *http.Request) error {
	id := httpx.URLParam(r, "id")
	n, err := s.modules.docker.InspectNetwork(r.Context(), id)
	if err != nil {
		return s.dockerErr(err)
	}
	// No typed phrase: a network holds no data and Docker refuses to remove one
	// that still has containers attached, so the mistake this would guard
	// against is one the daemon already refuses.
	if err := s.modules.docker.RemoveNetwork(r.Context(), id); err != nil {
		return s.dockerErr(err)
	}
	// The name is worth more than the id in an audit trail, and it is the only
	// reason this handler still inspects before removing.
	httpx.SetAudit(r, "docker.network.remove", n.Name, map[string]any{"id": id})
	httpx.NoContent(w)
	return nil
}

// imageName is an image's first tag, or a short id for one that was never
// tagged. It falls back to the id it was given rather than failing, so a caller
// always has something readable to name the image with.
func (s *Server) imageName(ctx context.Context, id string) string {
	images, err := s.modules.docker.ListImages(ctx, true)
	if err != nil {
		return shortID(id)
	}
	for _, img := range images {
		if img.ID != id && !strings.HasPrefix(img.ID, id) {
			continue
		}
		if len(img.RepoTags) > 0 && img.RepoTags[0] != "<none>:<none>" {
			return img.RepoTags[0]
		}
		return shortID(img.ID)
	}
	return shortID(id)
}

func shortID(id string) string { return dockerx.ShortID(id) }

func (s *Server) handleStackList(w http.ResponseWriter, r *http.Request) error {
	list, err := s.modules.docker.ListStacks(r.Context(), s.Cfg.ComposeRoots)
	if err != nil {
		return s.dockerErr(err)
	}
	httpx.JSON(w, http.StatusOK, list)
	return nil
}

func (s *Server) handleStackConfig(w http.ResponseWriter, r *http.Request) error {
	stack, err := s.findStack(r, httpx.URLParam(r, "name"))
	if err != nil {
		return err
	}
	if len(stack.ConfigFiles) == 0 {
		return httpx.ErrNotFound
	}
	content, err := dockerx.ReadComposeFile(stack.ConfigFiles[0])
	if err != nil {
		return httpx.Wrap(http.StatusInternalServerError, "read_failed", err)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"path": stack.ConfigFiles[0], "content": content})
	return nil
}

func (s *Server) findStack(r *http.Request, name string) (*dockerx.ComposeStack, error) {
	stacks, err := s.modules.docker.ListStacks(r.Context(), s.Cfg.ComposeRoots)
	if err != nil {
		return nil, s.dockerErr(err)
	}
	for i := range stacks {
		if stacks[i].Name == name {
			return &stacks[i], nil
		}
	}
	return nil, httpx.ErrNotFound
}

func (s *Server) stackAction(action dockerx.ComposeAction) httpx.Handler {
	return func(w http.ResponseWriter, r *http.Request) error {
		name := httpx.URLParam(r, "name")
		stack, err := s.findStack(r, name)
		if err != nil {
			return err
		}
		if !stack.Managed {
			return httpx.BadRequest("stack %q has no compose file on disk that this dashboard can reach", name)
		}
		// One answer to "which of these interrupts a running service", shared
		// with the streaming runner so the socket cannot become a way around
		// the confirmation the POST demands.
		if composeNeedsPhrase(action) {
			if err := httpx.RequireTypedConfirmation(w, r, name); err != nil {
				return err
			}
		}
		// Written down before it is replaced, for the same reason the
		// streaming runner does it: after the deploy the previous state is
		// gone and there is nothing left to roll back to.
		s.recordStackDeployment(r, stack, string(action))
		res, err := s.modules.docker.RunCompose(r.Context(), stack.WorkingDir, action, r.URL.Query().Get("service"))
		if err != nil {
			return httpx.Wrap(http.StatusBadGateway, "compose_failed", err)
		}
		httpx.SetAudit(r, "docker.stack."+string(action), name,
			map[string]any{"exitCode": res.ExitCode, "dir": stack.WorkingDir})
		status := http.StatusOK
		if res.ExitCode != 0 {
			status = http.StatusBadGateway
		}
		httpx.JSON(w, status, res)
		return nil
	}
}

func (s *Server) handlePruneAll(w http.ResponseWriter, r *http.Request) error {
	opts := dockerx.PruneOptions{
		Volumes:   r.URL.Query().Get("volumes") == "true",
		AllImages: r.URL.Query().Get("allImages") == "true",
		// The build cache is the largest line on any server that builds, and
		// until 0.6.4 no route in the product could touch it: the dashboard
		// reported tens of gigabytes as reclaimable and had nothing to reclaim
		// it with. It is opt-in rather than always-on so the plain sweep still
		// means what its dialog says.
		BuildCache:    r.URL.Query().Get("buildCache") == "true",
		AllBuildCache: r.URL.Query().Get("allBuildCache") == "true",
	}
	// Only the volume sweep is typed for: a container, a network, an image or
	// a cache entry comes back from a registry, a compose file or the next
	// build; a volume comes back from nothing. Without volumes this is the
	// routine housekeeping sweep and an ordinary confirmation is the right
	// weight — see invariant 3 on frequency rather than severity.
	if opts.Volumes {
		if err := httpx.RequireTypedConfirmation(w, r, "prune everything"); err != nil {
			return err
		}
	}
	reports, err := s.modules.docker.PruneAll(r.Context(), opts)
	if err != nil {
		return s.dockerErr(err)
	}
	httpx.SetAudit(r, "docker.prune.all", "", map[string]any{
		"volumes": opts.Volumes, "allImages": opts.AllImages,
		"buildCache": opts.BuildCache, "reports": reports,
	})
	httpx.JSON(w, http.StatusOK, reports)
	return nil
}

// handleBuildCachePrune is the one line of `docker system df` that had no
// button. BuildKit's cache is not in the image store, so neither the image
// prune nor the "prune everything" sweep reached it, and on a server that
// builds it is routinely the largest thing on the disk.
//
// Not typed: a cache is the definition of recoverable — the worst a wrong
// press costs is a slower next build.
func (s *Server) handleBuildCachePrune(w http.ResponseWriter, r *http.Request) error {
	// Defaults to the wide sweep, because the figure this dashboard puts on
	// screen is Docker's "reclaimable", which is what `-a` frees. A button
	// quoting one number and running the command for a smaller one is how the
	// last version of this feature came to look broken.
	all := r.URL.Query().Get("all") != "false"
	rep, err := s.modules.docker.PruneBuildCache(r.Context(), all)
	if err != nil {
		return s.dockerErr(err)
	}
	httpx.SetAudit(r, "docker.buildcache.prune", "",
		map[string]any{"all": all, "reclaimed": rep.SpaceReclaimed, "entries": len(rep.Items)})
	httpx.JSON(w, http.StatusOK, rep)
	return nil
}
