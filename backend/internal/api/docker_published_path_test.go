package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netpath"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netsec"
)

const publishedFixtureID = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

func publishedPathRouter(t *testing.T, role auth.Role) http.Handler {
	t.Helper()
	s, router := gatewayRouter(t, role, false)
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/_ping" {
			w.Header().Set("API-Version", "1.47")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		path := r.URL.Path[strings.Index(r.URL.Path[1:], "/")+1:]
		switch {
		case path == "/containers/shop-db/json" || path == "/containers/"+publishedFixtureID+"/json":
			_, _ = w.Write([]byte(`{"Id":"` + publishedFixtureID + `","Name":"/shop-db","State":{"Status":"running","Running":true},"Config":{"Image":"postgres:16","Labels":{}},"HostConfig":{"NetworkMode":"bridge"},
				"NetworkSettings":{"Networks":{"bridge":{"IPAddress":"10.0.0.2","NetworkID":"n1"}},"Ports":{"5432/tcp":[{"HostIp":"0.0.0.0","HostPort":"5432"}]}}}`))
		case path == "/containers/json":
			_, _ = w.Write([]byte(`[]`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"No such object"}`))
		}
	}))
	t.Cleanup(engine.Close)
	original := s.modules.docker
	s.modules.docker = dockerx.New(engine.URL)
	t.Cleanup(func() { _ = s.modules.docker.Close(); s.modules.docker = original })
	chains := readDockerChains
	readDockerChains = func(context.Context, string) netsec.DockerChains {
		return netsec.DockerChains{NAT: netsec.ParseDockerNAT("-A DOCKER ! -i docker0 -p tcp -m tcp --dport 5432 -j DNAT --to-destination 10.0.0.2:5432"), User: []string{}}
	}
	t.Cleanup(func() { readDockerChains = chains })
	s.mountDockerRoutes(router)
	return router
}

func TestPublishedPathJoinsTheInboundLayersForAdmins(t *testing.T) {
	router := publishedPathRouter(t, auth.RoleAdmin)
	rec := gwDo(router, http.MethodGet, "/docker/containers/shop-db/published/5432?protocol=tcp&family=inet", "")
	var result netpath.Result
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &result) != nil {
		t.Fatalf("path: %d %s", rec.Code, rec.Body.String())
	}
	if result.Scope.Vantage != "published_port" || result.Scope.Target != "shop-db" || result.Scope.Address != "10.0.0.2" || result.Request.ContainerID != publishedFixtureID {
		t.Fatalf("scope: %+v %+v", result.Scope, result.Request)
	}
	states := map[string]string{}
	for _, e := range result.Evidence {
		states[e.ID] = string(e.Basis) + "/" + e.State
	}
	if states["dnat"] != "observed/observed" || states["docker-user"] != "observed/empty" || states["provider"] != "unknown/unknown" || states["external"] != "unknown/unknown" {
		t.Fatalf("layers: %v", states)
	}
	if rec := gwDo(router, http.MethodGet, "/docker/containers/shop-db/published/8080", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("a port the container does not publish: %d %s", rec.Code, rec.Body.String())
	}
	if rec := gwDo(router, http.MethodGet, "/docker/containers/shop-db/published/5432?family=inet6", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("a family the container does not publish in: %d %s", rec.Code, rec.Body.String())
	}
}

func TestPublishedPathIsTheInvestigatorsCapability(t *testing.T) {
	router := publishedPathRouter(t, auth.RoleLimited)
	if rec := gwDo(router, http.MethodGet, "/docker/containers/shop-db/published/5432", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("a limited operator without system.admin: %d %s", rec.Code, rec.Body.String())
	}
}
