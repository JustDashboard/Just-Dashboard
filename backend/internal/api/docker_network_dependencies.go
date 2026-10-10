package api

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

// networkDependencyTimeout bounds a dependency reading: a network inspect,
// the container and network listings and one inspect per member.
const networkDependencyTimeout = 15 * time.Second

// networkChangePreview is what a connect, a disconnect or a removal would run
// into, read afresh. Blocked conflicts are refused by the mutation itself, so
// the preview and the refusal cannot disagree.
type networkChangePreview struct {
	Network   string                    `json:"network"`
	NetworkID string                    `json:"networkId"`
	Container string                    `json:"container,omitempty"`
	Owner     dockerx.NetworkOwner      `json:"owner"`
	Conflicts []dockerx.NetworkConflict `json:"conflicts"`
	Blocked   bool                      `json:"blocked"`
	CheckedAt time.Time                 `json:"checkedAt"`
}

// networkPrunePreview is the reviewed prune: every network the Engine's own
// prune would take, and which of them this one removes.
type networkPrunePreview struct {
	Candidates []dockerx.PruneCandidate `json:"candidates"`
	CheckedAt  time.Time                `json:"checkedAt"`
}

// selfProject is the dashboard's own Compose project among the running
// containers, by the data directory its backend mounts.
func (s *Server) selfProject(containers []dockerx.Container) string {
	running := make([]dockerx.Container, 0, len(containers))
	for _, c := range containers {
		if c.State == "running" {
			running = append(running, c)
		}
	}
	return proxysvc.SelfProject(runningContainers(running), s.Cfg.DataDir)
}

// deploymentEnvironments names the deployment environments that still exist
// among ids, as "project · environment".
func (s *Server) deploymentEnvironments(ctx context.Context, ids []int64) (map[int64]string, error) {
	out := map[int64]string{}
	args := []any{}
	for _, id := range ids {
		if id > 0 {
			args = append(args, id)
		}
	}
	if len(args) == 0 {
		return out, nil
	}
	if s.Store == nil {
		return out, errors.New("the store is unavailable")
	}
	rows, err := s.Store.DB.QueryContext(ctx, `SELECT e.id, e.name, p.name
		FROM deploy_environments e JOIN deploy_projects p ON p.id = e.project_id
		WHERE e.id IN (?`+strings.Repeat(",?", len(args)-1)+`)`, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var environment, project string
		if err := rows.Scan(&id, &environment, &project); err != nil {
			return out, err
		}
		out[id] = project + " · " + environment
	}
	return out, rows.Err()
}

// environmentLookup is the existence check the removal rules take. An
// environment that could not be read counts as existing: refusing to
// remove a network is recoverable, removing a live deployment's is not.
func (s *Server) environmentLookup(ctx context.Context, ids ...int64) func(int64) (string, bool) {
	names, err := s.deploymentEnvironments(ctx, ids)
	return func(id int64) (string, bool) {
		if name, ok := names[id]; ok {
			return name, true
		}
		if err != nil {
			return fmt.Sprintf("environment %d (unreadable)", id), true
		}
		return "", false
	}
}

// annotateNetworkOwners stamps each listed network with its owner, the
// deployment by name included.
func (s *Server) annotateNetworkOwners(ctx context.Context, networks []dockerx.Network, self string) {
	ids := []int64{}
	owners := make([]dockerx.NetworkOwner, len(networks))
	for i, n := range networks {
		owners[i] = dockerx.OwnerOfNetwork(n.Name, n.Labels, self)
		ids = append(ids, owners[i].EnvironmentID)
	}
	names, _ := s.deploymentEnvironments(ctx, ids)
	for i := range networks {
		owner := owners[i]
		owner.Deployment = names[owner.EnvironmentID]
		networks[i].Owner = &owner
	}
}

// ipamHolderConflicts says which shared planning reservations still name
// the network as their owner, since removing it leaves them held.
func (s *Server) ipamHolderConflicts(ctx context.Context, name, id string) []dockerx.NetworkConflict {
	if s.modules.ipam == nil {
		return nil
	}
	held, err := s.modules.ipam.HeldFor(ctx, "docker_network", name, id)
	if err != nil {
		// Unread is not "none": a prune keeps a network it cannot check.
		return []dockerx.NetworkConflict{{Code: "ipam_unread", Level: dockerx.ConflictWarn,
			Message: "Shared IPAM reservations could not be checked for this network, so one may still record it as owner."}}
	}
	out := []dockerx.NetworkConflict{}
	for _, r := range held {
		out = append(out, dockerx.NetworkConflict{Code: "ipam_reservation", Level: dockerx.ConflictWarn, Subjects: []string{r.Prefix},
			Message: fmt.Sprintf("The shared IPAM reservation %s still records this network as its owner and stays held after removal; release it on Network → IPAM once the network is gone.", r.Prefix)})
	}
	return out
}

// networkDependencies reads a network's dependents with the dashboard's own
// project resolved. A failed reading is a refusal to preview, never an empty
// list of dependents.
func (s *Server) networkDependencies(ctx context.Context, id string, extra ...string) (*dockerx.NetworkDependencies, error) {
	deps, err := s.modules.docker.NetworkDependencies(ctx, id, extra...)
	if err != nil {
		var api *httpx.APIError
		if errors.As(s.dockerErr(err), &api) && api.Status == http.StatusNotFound {
			return nil, s.dockerErr(err)
		}
		return nil, httpx.Err(http.StatusServiceUnavailable, "dependencies_unread",
			"The network's dependents could not be read, so nothing was changed.").Because("A change is previewed and checked against every container that names the network; without that reading the dashboard will not guess.", err.Error()).Retry()
	}
	containers := make([]dockerx.Container, 0, len(deps.Containers))
	for _, c := range deps.Containers {
		containers = append(containers, c.Container)
	}
	deps.SelfProject = s.selfProject(containers)
	return deps, nil
}

func (s *Server) previewOf(r *http.Request, deps *dockerx.NetworkDependencies, container string, conflicts []dockerx.NetworkConflict) networkChangePreview {
	owner := deps.Owner()
	names, _ := s.deploymentEnvironments(r.Context(), []int64{owner.EnvironmentID})
	owner.Deployment = names[owner.EnvironmentID]
	return networkChangePreview{
		Network: deps.Network.Name, NetworkID: deps.Network.ID, Container: container,
		Owner: owner, Conflicts: conflicts, Blocked: dockerx.Blocking(conflicts), CheckedAt: time.Now().UTC(),
	}
}

// conflictRefusal turns a blocked preview into the 409 the mutation answers.
func conflictRefusal(conflicts []dockerx.NetworkConflict) error {
	blocks := []string{}
	for _, c := range conflicts {
		if c.Level == dockerx.ConflictBlock {
			blocks = append(blocks, c.Message)
		}
	}
	return httpx.Err(http.StatusConflict, "network_conflict", blocks[0]).
		Because(strings.Join(blocks[1:], " "), "")
}

func conflictCodes(conflicts []dockerx.NetworkConflict) []string {
	out := make([]string, 0, len(conflicts))
	for _, c := range conflicts {
		out = append(out, c.Code)
	}
	return out
}

// handleNetworkConnectPreview is GET /docker/networks/{id}/connect: what
// attaching ?container= with each ?alias= would run into.
func (s *Server) handleNetworkConnectPreview(w http.ResponseWriter, r *http.Request) error {
	container := r.URL.Query().Get("container")
	if container == "" {
		return httpx.BadRequest("a container is required")
	}
	aliases := cleanAliases(r.URL.Query()["alias"])
	ctx, cancel := timeoutCtx(r, networkDependencyTimeout)
	defer cancel()
	deps, err := s.networkDependencies(ctx, httpx.URLParam(r, "id"), container)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, s.previewOf(r, deps, container, dockerx.PreviewConnect(deps, container, aliases)))
	return nil
}

// handleNetworkDisconnectPreview is GET /docker/networks/{id}/disconnect:
// who loses what when ?container= leaves.
func (s *Server) handleNetworkDisconnectPreview(w http.ResponseWriter, r *http.Request) error {
	container := r.URL.Query().Get("container")
	if container == "" {
		return httpx.BadRequest("a container is required")
	}
	ctx, cancel := timeoutCtx(r, networkDependencyTimeout)
	defer cancel()
	deps, err := s.networkDependencies(ctx, httpx.URLParam(r, "id"), container)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, s.previewOf(r, deps, container, dockerx.PreviewDisconnect(deps, container)))
	return nil
}

// removalConflicts is the removal preview with the parts the dashboard
// itself knows: whether a managed network's deployment still exists, and
// which IPAM reservations still name it.
func (s *Server) removalConflicts(ctx context.Context, deps *dockerx.NetworkDependencies) []dockerx.NetworkConflict {
	owner := deps.Owner()
	conflicts := dockerx.PreviewRemove(deps, s.environmentLookup(ctx, owner.EnvironmentID))
	return append(conflicts, s.ipamHolderConflicts(ctx, deps.Network.Name, deps.Network.ID)...)
}

// handleNetworkRemovalPreview is GET /docker/networks/{id}/removal.
func (s *Server) handleNetworkRemovalPreview(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := timeoutCtx(r, networkDependencyTimeout)
	defer cancel()
	deps, err := s.networkDependencies(ctx, httpx.URLParam(r, "id"))
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, s.previewOf(r, deps, "", s.removalConflicts(ctx, deps)))
	return nil
}

// pruneCandidates is the reviewed prune's set, read afresh.
func (s *Server) pruneCandidates(ctx context.Context) ([]dockerx.PruneCandidate, error) {
	networks, err := s.modules.docker.ListNetworks(ctx)
	if err != nil {
		return nil, s.dockerErr(err)
	}
	containers, err := s.modules.docker.ListContainers(ctx, true)
	if err != nil {
		return nil, httpx.Err(http.StatusServiceUnavailable, "dependencies_unread",
			"The containers could not be listed, so which networks are unused is not known.").Because("A network a stopped container still names is not unused; without the container listing the dashboard will not guess.", err.Error()).Retry()
	}
	ids := []int64{}
	for _, n := range networks {
		ids = append(ids, dockerx.OwnerOfNetwork(n.Name, n.Labels, "").EnvironmentID)
	}
	lookup := s.environmentLookup(ctx, ids...)
	candidates := dockerx.PruneCandidates(networks, containers, s.selfProject(containers), lookup)
	for i := range candidates {
		c := &candidates[i]
		if name, ok := lookup(c.Owner.EnvironmentID); ok && c.Owner.EnvironmentID > 0 {
			c.Owner.Deployment = name
		}
		c.Conflicts = append(c.Conflicts, s.ipamHolderConflicts(ctx, c.Name, c.ID)...)
		c.Removable = !dockerx.Blocking(c.Conflicts) && !dockerx.Warning(c.Conflicts)
	}
	return candidates, nil
}

// handleNetworkPrunePreview is GET /docker/networks/prune.
func (s *Server) handleNetworkPrunePreview(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := timeoutCtx(r, networkDependencyTimeout)
	defer cancel()
	candidates, err := s.pruneCandidates(ctx)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, networkPrunePreview{Candidates: candidates, CheckedAt: time.Now().UTC()})
	return nil
}

// handleNetworkDrivers is GET /docker/networks/drivers: what this Engine can
// create a network with, read now.
func (s *Server) handleNetworkDrivers(w http.ResponseWriter, r *http.Request) error {
	catalogue, err := s.modules.docker.NetworkDrivers(r.Context())
	if err != nil {
		return s.dockerErr(err)
	}
	httpx.JSON(w, http.StatusOK, catalogue)
	return nil
}

// checkNetworkDriver refuses a driver this Engine cannot create a network
// with before the Engine is asked. Every Engine ships the bridge driver, so
// a bridge network is not checked; any other driver is refused when the
// catalogue cannot be read, since what was asked for could not be checked.
func (s *Server) checkNetworkDriver(ctx context.Context, spec dockerx.NetworkSpec) error {
	if spec.Driver == "" || spec.Driver == "bridge" {
		return nil
	}
	catalogue, err := s.modules.docker.NetworkDrivers(ctx)
	if err != nil {
		return httpx.Err(http.StatusServiceUnavailable, "drivers_unread",
			"The Engine's network drivers could not be read, so the "+spec.Driver+" driver was not checked.").Because("", err.Error()).Retry()
	}
	if err := catalogue.Check(spec, hostInterfaces()); err != nil {
		return httpx.Err(http.StatusBadRequest, "driver_unavailable", err.Error())
	}
	return nil
}

// hostInterfaces names this host's network devices, for the parent a
// macvlan or ipvlan network is carried on. The backend shares the host's
// network namespace, so these are the host's own. A variable so tests name
// the devices a fixture host has.
var hostInterfaces = func() []string {
	list, err := net.Interfaces()
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(list))
	for _, i := range list {
		out = append(out, i.Name)
	}
	return out
}

func cleanAliases(values []string) []string {
	out := []string{}
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}
