package dockerx

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
)

func conflictLevels(conflicts []NetworkConflict) map[string]string {
	out := map[string]string{}
	for _, c := range conflicts {
		out[c.Code] = c.Level
	}
	return out
}

// lab is a user network with two running members, one of them answering to
// "db", and a stopped container that still names it.
func lab() *NetworkDependencies {
	return &NetworkDependencies{
		Network: network.Inspect{ID: "net1", Name: "lab", Driver: "bridge", Scope: "local",
			Labels: map[string]string{"com.docker.compose.project": "shop", "com.docker.compose.network": "default"},
			IPAM:   network.IPAM{Config: []network.IPAMConfig{{Subnet: "10.4.0.0/29", Gateway: "10.4.0.1"}}},
			Containers: map[string]network.EndpointResource{
				"aaaa000000000000": {Name: "postgres", IPv4Address: "10.4.0.2/29"},
				"bbbb000000000000": {Name: "api", IPv4Address: "10.4.0.3/29"},
			}},
		Networks: map[string]Network{
			"lab":    {Name: "lab", Subnets: []string{"10.4.0.0/29"}},
			"edge":   {Name: "edge", Subnets: []string{"10.4.0.0/24"}},
			"vault":  {Name: "vault", Internal: true, Subnets: []string{"10.9.0.0/24"}},
			"bridge": {Name: "bridge", Subnets: []string{"172.17.0.0/16"}},
		},
		Containers: []DependentContainer{
			{Container: Container{ID: "aaaa000000000000", Name: "postgres", State: "running", ComposeStack: "shop", Networks: []string{"lab"}},
				Inspected: true, NetworkMode: "lab", Endpoints: map[string]Endpoint{"lab": {Aliases: []string{"postgres", "db", "aaaa00000000"}}}},
			{Container: Container{ID: "bbbb000000000000", Name: "api", State: "running", ComposeStack: "shop", Networks: []string{"lab", "bridge"},
				Ports: []Port{{IP: "0.0.0.0", PrivatePort: 8080, PublicPort: 80, Type: "tcp"}}},
				Inspected: true, NetworkMode: "lab", Endpoints: map[string]Endpoint{"lab": {Aliases: []string{"api"}}, "bridge": {}}},
			{Container: Container{ID: "cccc000000000000", Name: "worker", State: "exited", ComposeStack: "shop", Networks: []string{"lab"}}},
			{Container: Container{ID: "dddd000000000000", Name: "cache", State: "running", Networks: []string{"edge"}},
				Inspected: true, NetworkMode: "edge", Endpoints: map[string]Endpoint{"edge": {}}},
			{Container: Container{ID: "eeee000000000000", Name: "probe", State: "running", Networks: []string{"host"}},
				Inspected: true, NetworkMode: "host", Endpoints: map[string]Endpoint{"host": {}}},
		},
	}
}

func TestPreviewConnectNamesSharedNamesOverlapsAndRefusals(t *testing.T) {
	d := lab()
	got := conflictLevels(PreviewConnect(d, "cache", []string{"db"}))
	if got["shared_name"] != ConflictWarn || got["subnet_overlap"] != ConflictWarn {
		t.Fatalf("a shared alias and overlapping ranges must be warned about: %+v", got)
	}
	if Blocking(PreviewConnect(d, "cache", []string{"db"})) {
		t.Fatalf("warnings alone must not refuse the attach: %+v", got)
	}
	got = conflictLevels(PreviewConnect(d, "probe", nil))
	if got["network_mode"] != ConflictBlock {
		t.Fatalf("a host-network container cannot be attached: %+v", got)
	}
	got = conflictLevels(PreviewConnect(d, "postgres", nil))
	if got["already_attached"] != ConflictBlock {
		t.Fatalf("a member cannot be attached again: %+v", got)
	}
	got = conflictLevels(PreviewConnect(d, "cache", []string{"bad name"}))
	if got["invalid_alias"] != ConflictBlock {
		t.Fatalf("an alias the resolver cannot answer is refused: %+v", got)
	}
	got = conflictLevels(PreviewConnect(d, "nobody", nil))
	if got["candidate_unread"] != ConflictBlock {
		t.Fatalf("an uninspected candidate is refused rather than guessed: %+v", got)
	}
	d.Containers[2].Inspected, d.Containers[2].Endpoints, d.Containers[2].Networks = true, map[string]Endpoint{}, nil
	got = conflictLevels(PreviewConnect(d, "worker", nil))
	if got["stopped"] != ConflictInfo || Blocking(PreviewConnect(d, "worker", nil)) {
		t.Fatalf("a stopped container joins on its next start: %+v", got)
	}
}

func TestPreviewConnectRefusesTheDashboardsNetworkAndAFullPool(t *testing.T) {
	d := lab()
	d.SelfProject = "shop"
	if got := conflictLevels(PreviewConnect(d, "cache", nil)); got["dashboard_network"] != ConflictBlock {
		t.Fatalf("the dashboard's own network must refuse other containers: %+v", got)
	}
	d = lab()
	// A /29 holds six usable addresses less the gateway: five members fill it.
	for i, id := range []string{"f1", "f2", "f3"} {
		d.Network.Containers[id] = network.EndpointResource{Name: id, IPv4Address: fmt.Sprintf("10.4.0.%d/29", 4+i)}
	}
	if got := conflictLevels(PreviewConnect(d, "cache", nil)); got["pool_exhausted"] != ConflictBlock {
		t.Fatalf("a full pool is refused before the Engine fails: %+v", got)
	}
	// Docker allocates from the next pool, so a second one with room is not exhausted.
	d.Network.IPAM.Config = append(d.Network.IPAM.Config, network.IPAMConfig{Subnet: "10.5.0.0/24"})
	if got := conflictLevels(PreviewConnect(d, "cache", nil)); got["pool_exhausted"] != "" {
		t.Fatalf("a full first pool with a second one free is not exhausted: %+v", got)
	}
	d = lab()
	d.Network.Name = "bridge"
	if got := conflictLevels(PreviewConnect(d, "cache", []string{"db"})); got["alias_on_default_bridge"] != ConflictBlock || got["shared_name"] != "" {
		t.Fatalf("the default bridge has no embedded DNS: %+v", got)
	}
	d = lab()
	d.Network.Labels = map[string]string{"io.just-dashboard.managed": "true", "io.just-dashboard.environment-id": "7", "io.just-dashboard.database-network": "k"}
	if got := conflictLevels(PreviewConnect(d, "cache", nil)); got["managed_network"] != ConflictWarn {
		t.Fatalf("attaching to a database network gives a path to databases: %+v", got)
	}
}

func TestPreviewDisconnectGuardsOwnersAndNamesWhatIsLost(t *testing.T) {
	d := lab()
	conflicts := PreviewDisconnect(d, "postgres")
	got := conflictLevels(conflicts)
	if got["last_network"] != ConflictWarn || got["peers"] != ConflictWarn || got["compose_restores"] != ConflictInfo || Blocking(conflicts) {
		t.Fatalf("detaching a sole-network member: %+v", got)
	}
	for _, c := range conflicts {
		if c.Code == "peers" && (!strings.Contains(c.Message, "api") || !strings.Contains(c.Message, "postgres, db") || strings.Contains(c.Message, "aaaa00000000")) {
			t.Fatalf("peers lose the member's names, not its short ID: %q", c.Message)
		}
	}
	got = conflictLevels(PreviewDisconnect(d, "api"))
	if got["published_ports"] != ConflictWarn || got["last_network"] != "" {
		t.Fatalf("a member with another way out may lose its publications: %+v", got)
	}
	d.SelfProject = "shop"
	if got := conflictLevels(PreviewDisconnect(d, "api")); got["dashboard_container"] != ConflictBlock {
		t.Fatalf("the dashboard's own container must never be detached: %+v", got)
	}
	d = lab()
	d.Containers[1].Labels = map[string]string{"com.just-dashboard.ingress": "true"}
	if got := conflictLevels(PreviewDisconnect(d, "api")); got["ingress"] != ConflictBlock {
		t.Fatalf("the shared ingress must never be detached: %+v", got)
	}
	d = lab()
	d.Network.Labels = map[string]string{"io.just-dashboard.managed": "true", "io.just-dashboard.environment-id": "7", "io.just-dashboard.database-network": "k"}
	if got := conflictLevels(PreviewDisconnect(d, "postgres")); got["managed_membership"] != ConflictBlock {
		t.Fatalf("database-link members belong to their deployment: %+v", got)
	}
	if got := conflictLevels(PreviewDisconnect(lab(), "cache")); got["not_attached"] != ConflictBlock {
		t.Fatalf("a non-member cannot be detached: %+v", got)
	}
}

func TestPreviewRemoveListsStoppedDependentsAndManagedOwners(t *testing.T) {
	d := lab()
	gone := func(int64) (string, bool) { return "", false }
	got := conflictLevels(PreviewRemove(d, gone))
	if got["in_use"] != ConflictBlock || got["stopped_dependents"] != ConflictWarn || got["compose_recreates"] != ConflictInfo {
		t.Fatalf("removal of a used compose network: %+v", got)
	}
	d.Network.Containers = map[string]network.EndpointResource{}
	conflicts := PreviewRemove(d, gone)
	if Blocking(conflicts) || !Warning(conflicts) {
		t.Fatalf("a network only stopped containers name is removable after confirmation: %+v", conflicts)
	}
	d.Network.Labels = map[string]string{"io.just-dashboard.managed": "true", "io.just-dashboard.environment-id": "7"}
	exists := func(id int64) (string, bool) { return "shop · production", id == 7 }
	if got := conflictLevels(PreviewRemove(d, exists)); got["managed_network"] != ConflictBlock {
		t.Fatalf("a live deployment's network is removed through its removal plan: %+v", got)
	}
	if got := conflictLevels(PreviewRemove(d, gone)); got["orphaned_managed_network"] != ConflictInfo || got["managed_network"] != "" {
		t.Fatalf("an orphaned managed network can be cleaned up: %+v", got)
	}
	d.Network.Name = "bridge"
	if got := conflictLevels(PreviewRemove(d, gone)); got["system_network"] != ConflictBlock {
		t.Fatalf("system networks are never removed: %+v", got)
	}
}

func TestPruneCandidatesKeepWhatStoppedContainersAndDeploymentsName(t *testing.T) {
	networks := []Network{
		{ID: "1", Name: "bridge"},
		{ID: "2", Name: "busy"},
		{ID: "3", Name: "dormant"},
		{ID: "4", Name: "orphan"},
		{ID: "5", Name: "managed", Labels: map[string]string{"io.just-dashboard.managed": "true", "io.just-dashboard.environment-id": "7"}},
		{ID: "6", Name: "gone", Labels: map[string]string{"io.just-dashboard.managed": "true", "io.just-dashboard.environment-id": "9"}},
	}
	containers := []Container{
		{Name: "web", State: "running", Networks: []string{"busy", "bridge"}},
		{Name: "batch", State: "exited", Networks: []string{"dormant"}},
	}
	exists := func(id int64) (string, bool) { return "shop · production", id == 7 }
	got := map[string]PruneCandidate{}
	for _, c := range PruneCandidates(networks, containers, "", exists) {
		got[c.Name] = c
	}
	if _, ok := got["bridge"]; ok {
		t.Fatal("system networks are never prune candidates")
	}
	if _, ok := got["busy"]; ok {
		t.Fatal("a network a running container uses is not a candidate")
	}
	if got["dormant"].Removable || !Warning(got["dormant"].Conflicts) {
		t.Fatalf("the Engine would prune a network a stopped container names; it must be kept: %+v", got["dormant"])
	}
	if !got["orphan"].Removable || got["managed"].Removable || !got["gone"].Removable {
		t.Fatalf("removable set: %+v", got)
	}
}

func TestOwnerOfNetworkAndIngress(t *testing.T) {
	for _, test := range []struct {
		name, self, kind string
		labels           map[string]string
	}{
		{"host", "", NetworkOwnerSystem, nil},
		{"just-dashboard_internal", "just-dashboard", NetworkOwnerDashboard, map[string]string{"com.docker.compose.project": "just-dashboard"}},
		{"jd-e6-db-x", "", NetworkOwnerDatabaseLink, map[string]string{"io.just-dashboard.managed": "true", "io.just-dashboard.environment-id": "6", "io.just-dashboard.database-network": "x"}},
		{"jd-e6", "", NetworkOwnerDeployment, map[string]string{"io.just-dashboard.managed": "true", "io.just-dashboard.environment-id": "6"}},
		{"shop_default", "just-dashboard", NetworkOwnerCompose, map[string]string{"com.docker.compose.project": "shop"}},
		{"lab", "", NetworkOwnerManual, map[string]string{"purpose": "lab"}},
		{"forged", "", NetworkOwnerManual, map[string]string{"io.just-dashboard.environment-id": "6"}},
	} {
		if got := OwnerOfNetwork(test.name, test.labels, test.self); got.Kind != test.kind {
			t.Errorf("%s: owner %+v, want %s", test.name, got, test.kind)
		}
	}
	adopted := Container{Name: "edge", Image: "caddy:2-alpine", Ports: []Port{{PublicPort: 80, Type: "tcp"}, {IP: "::", PublicPort: 443, Type: "tcp"}}}
	if !IsIngressContainer(adopted) || !IsIngressContainer(Container{Name: "just-dashboard-ingress"}) {
		t.Fatal("the shared public Caddy must be recognised")
	}
	if IsIngressContainer(Container{Name: "site", Image: "caddy:2", Ports: []Port{{IP: "127.0.0.1", PublicPort: 80, Type: "tcp"}, {IP: "127.0.0.1", PublicPort: 443, Type: "tcp"}}}) {
		t.Fatal("a loopback-only Caddy is not the public ingress")
	}
}

// The Engine-wire reading must fail closed: a preview that could not list
// the containers must not report a network nothing depends on.
func TestNetworkDependenciesReadsMembersAndFailsClosed(t *testing.T) {
	listFails := false
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		path := strings.TrimPrefix(r.URL.Path, "/v1.47")
		switch {
		case path == "/networks/lab":
			_, _ = w.Write([]byte(`{"Id":"net1","Name":"lab","Driver":"bridge","Scope":"local","Labels":{},"IPAM":{"Config":[{"Subnet":"10.4.0.0/24"}]},"Containers":{"aaaa":{"Name":"postgres","IPv4Address":"10.4.0.2/24"}}}`))
		case path == "/containers/json":
			if listFails {
				http.Error(w, `{"message":"daemon busy"}`, http.StatusInternalServerError)
				return
			}
			_, _ = w.Write([]byte(`[{"Id":"aaaa","Names":["/postgres"],"State":"running","Labels":{},"NetworkSettings":{"Networks":{"lab":{}}}},{"Id":"cccc","Names":["/worker"],"State":"exited","Labels":{},"NetworkSettings":{"Networks":{"lab":{}}}}]`))
		case path == "/networks":
			_, _ = w.Write([]byte(`[{"Id":"net1","Name":"lab","Driver":"bridge","IPAM":{"Config":[{"Subnet":"10.4.0.0/24"}]}}]`))
		case path == "/containers/aaaa/json":
			_, _ = w.Write([]byte(`{"Id":"aaaa","Name":"/postgres","State":{"Status":"running"},"HostConfig":{"NetworkMode":"lab"},"NetworkSettings":{"Networks":{"lab":{"Aliases":["db"],"IPAddress":"10.4.0.2"}}}}`))
		case path == "/containers/missing/json":
			http.Error(w, `{"message":"No such container"}`, http.StatusNotFound)
		default:
			t.Errorf("unexpected Engine request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer engine.Close()
	api, err := client.NewClientWithOpts(client.WithHost(engine.URL), client.WithVersion("1.47"))
	if err != nil {
		t.Fatal(err)
	}
	defer api.Close()
	c := &Client{cli: api}
	d, err := c.NetworkDependencies(t.Context(), "lab", "missing")
	if err != nil {
		t.Fatal(err)
	}
	member := d.Container("postgres")
	if member == nil || !member.Inspected || !slices.Contains(member.Endpoints["lab"].Aliases, "db") {
		t.Fatalf("member endpoints were not read: %+v", member)
	}
	if !slices.Contains(d.Unread, "missing") {
		t.Fatalf("a failed inspect must be named, not dropped: %+v", d.Unread)
	}
	if got := conflictLevels(PreviewRemove(d, func(int64) (string, bool) { return "", false })); got["stopped_dependents"] != ConflictWarn {
		raw, _ := json.Marshal(d.Containers)
		t.Fatalf("the stopped worker must be a dependent: %+v %s", got, raw)
	}
	listFails = true
	if _, err := c.NetworkDependencies(t.Context(), "lab"); err == nil {
		t.Fatal("a failed container listing must fail the reading")
	}
}

// A reference in any form the Engine resolves — a short ID, a name — is
// judged as the container it resolved to, so a guard cannot be stepped
// around by naming the dashboard's container another way.
func TestPreviewsJudgeTheContainerAReferenceResolvedTo(t *testing.T) {
	d := lab()
	d.SelfProject = "shop"
	d.refs = map[string]string{"bbbb": "bbbb000000000000"}
	if got := conflictLevels(PreviewDisconnect(d, "bbbb")); got["dashboard_container"] != ConflictBlock {
		t.Fatalf("a short ID of the dashboard's container is still refused: %+v", got)
	}
	if got := conflictLevels(PreviewDisconnect(lab(), "bbbb")); got["not_attached"] != ConflictBlock {
		t.Fatalf("an unresolved short reference is refused rather than guessed: %+v", got)
	}
}

// A forced disconnect exists for an endpoint whose container is gone. Only a
// reference the Engine says nothing answers to is stale; an unread one is not.
func TestPreviewDisconnectOffersAStaleEndpointOnlyWhenItsContainerIsGone(t *testing.T) {
	d := lab()
	d.Network.Containers["ffff000000000000"] = network.EndpointResource{Name: "ghost"}
	d.missing = map[string]bool{"ghost": true}
	if id, ok := d.StaleEndpoint("ghost"); !ok || id != "ffff000000000000" {
		t.Fatalf("stale endpoint: %q %v", id, ok)
	}
	conflicts := PreviewDisconnect(d, "ghost")
	if got := conflictLevels(conflicts); got["stale_endpoint"] != ConflictWarn || Blocking(conflicts) {
		t.Fatalf("a gone container's endpoint can be removed: %+v", got)
	}
	d.missing = map[string]bool{}
	if got := conflictLevels(PreviewDisconnect(d, "ghost")); got["not_attached"] != ConflictBlock {
		t.Fatalf("an endpoint whose container was merely unread is refused: %+v", got)
	}
}

func TestSwarmNetworksAreNeitherPrunedNorRemovedHereWhenTheyCarryTheMesh(t *testing.T) {
	candidates := PruneCandidates([]Network{{ID: "1", Name: "ingress", Scope: "swarm"}, {ID: "2", Name: "lab", Scope: "local"}}, nil, "", func(int64) (string, bool) { return "", false })
	if len(candidates) != 1 || candidates[0].Name != "lab" {
		t.Fatalf("swarm networks are the managers' to prune: %+v", candidates)
	}
	d := lab()
	d.Network.Containers = map[string]network.EndpointResource{}
	d.Network.Ingress = true
	if got := conflictLevels(PreviewRemove(d, func(int64) (string, bool) { return "", false })); got["swarm_ingress"] != ConflictBlock {
		t.Fatalf("the routing mesh is refused: %+v", got)
	}
}
