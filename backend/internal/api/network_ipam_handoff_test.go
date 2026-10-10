package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netipam"
)

func TestIPAMDockerHandoffRetainsBothFamiliesAndNativeIdentity(t *testing.T) {
	s, router := gatewayRouter(t, auth.RoleAdmin, false)
	snapshot := netipam.Snapshot{Coverage: []netipam.Coverage{{Source: "provider", State: "unknown"}}}
	s.modules.ipam = netipam.New(s.Store, func(context.Context) (netipam.Snapshot, error) { return snapshot, nil })
	makeReservation := func(prefix string, bits int) netipam.Reservation {
		p, e := s.modules.ipam.CreatePool(t.Context(), netipam.PoolRequest{Name: prefix, Prefix: prefix, AllocationBits: bits})
		if e != nil {
			t.Fatal(e)
		}
		r, e := s.modules.ipam.Reserve(t.Context(), netipam.ReserveRequest{PoolID: p.ID, Owner: "docker_network", Resource: "app-private", AcknowledgeUnknown: true}, "operator")
		if e != nil {
			t.Fatal(e)
		}
		return r
	}
	r4, r6 := makeReservation("10.244.0.0/16", 24), makeReservation("fd48:abcd::/48", 64)
	calls := 0
	var native map[string]json.RawMessage
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/_ping" {
			w.Header().Set("API-Version", "1.47")
			return
		}
		calls++
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/networks/create") {
			if e := json.NewDecoder(r.Body).Decode(&native); e != nil {
				t.Error(e)
			}
			w.WriteHeader(201)
			w.Write([]byte(`{"Id":"native-created"}`))
			return
		}
		if strings.HasSuffix(r.URL.Path, "/networks/native-created") {
			w.Write([]byte(`{"Id":"native-created","Name":"app-private","Driver":"bridge"}`))
			return
		}
		t.Errorf("unexpected native request %s", r.URL.Path)
		w.WriteHeader(404)
	}))
	defer engine.Close()
	original := s.modules.docker
	s.modules.docker = dockerx.New(engine.URL)
	t.Cleanup(func() { s.modules.docker.Close(); s.modules.docker = original })
	s.mountDockerRoutes(router)
	body := string(netipamTestJSON(map[string]any{"name": "app-private", "ipv6": true, "ipam": []dockerx.NetworkIPAM{{Subnet: r4.Prefix}, {Subnet: r6.Prefix}}, "ipamReservationIds": []string{r4.ID, r6.ID}}))
	response := ipamDo(router, "POST", "/docker/networks/", body)
	if response.Code != 201 {
		t.Fatal(response.Code, response.Body.String())
	}
	if calls != 2 || !strings.Contains(string(native["IPAM"]), r4.Prefix) || !strings.Contains(string(native["IPAM"]), r6.Prefix) {
		t.Fatal("native exact family tuple changed", calls, string(native["IPAM"]))
	}
	for _, r := range []netipam.Reservation{r4, r6} {
		var state, id string
		if e := s.Store.DB.QueryRow(`SELECT state,native_id FROM network_ipam_reservations WHERE id=?`, r.ID).Scan(&state, &id); e != nil || state != "observed" || id != "native-created" {
			t.Fatal("native identity not recorded with held plan", state, id, e)
		}
	}
	if response = ipamDo(router, "POST", "/docker/networks/", body); response.Code != 409 || calls != 2 {
		t.Fatal("used handoff replay reached native owner", response.Code, calls)
	}
}

func TestIPAMDockerRefusesWrongOrMissingReservationBeforeNativeMutation(t *testing.T) {
	for _, scenario := range []string{"omitted", "unknown", "wrong-name", "wrong-prefix", "new-coverage-gap", "new-native-overlap", "limited"} {
		t.Run(scenario, func(t *testing.T) {
			role := auth.RoleAdmin
			if scenario == "limited" {
				role = auth.RoleLimited
			}
			s, router := gatewayRouter(t, role, false)
			snapshot := netipam.Snapshot{Coverage: []netipam.Coverage{{Source: "provider", State: "unknown"}}}
			s.modules.ipam = netipam.New(s.Store, func(context.Context) (netipam.Snapshot, error) { return snapshot, nil })
			p, e := s.modules.ipam.CreatePool(t.Context(), netipam.PoolRequest{Name: "shared", Prefix: "10.244.0.0/16", AllocationBits: 24})
			if e != nil {
				t.Fatal(e)
			}
			reservation, e := s.modules.ipam.Reserve(t.Context(), netipam.ReserveRequest{PoolID: p.ID, Owner: "docker_network", Resource: "app-private", AcknowledgeUnknown: true}, "operator")
			if e != nil {
				t.Fatal(e)
			}
			calls := 0
			engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(500) }))
			defer engine.Close()
			original := s.modules.docker
			s.modules.docker = dockerx.New(engine.URL)
			t.Cleanup(func() { s.modules.docker.Close(); s.modules.docker = original })
			s.mountDockerRoutes(router)
			name, prefix, ids := "app-private", reservation.Prefix, []string{reservation.ID}
			switch scenario {
			case "omitted":
				ids = nil
			case "unknown":
				ids = []string{strings.Repeat("f", 32)}
			case "wrong-name":
				name = "other"
			case "wrong-prefix":
				prefix = "10.244.1.0/24"
			case "new-coverage-gap":
				snapshot.Coverage = append(snapshot.Coverage, netipam.Coverage{Source: "docker", State: "unreadable"})
			case "new-native-overlap":
				snapshot.Observations = []netipam.Observation{{Prefix: prefix, Owner: "foreign", Resource: "new", Basis: "observed_native"}}
			}
			body := string(netipamTestJSON(map[string]any{"name": name, "subnet": prefix, "ipamReservationIds": ids}))
			response := ipamDo(router, "POST", "/docker/networks/", body)
			want := 409
			if scenario == "limited" {
				want = 403
			}
			if response.Code != want || calls != 0 {
				t.Fatal("refused handoff reached native owner", response.Code, response.Body.String(), calls)
			}
			var state string
			s.Store.DB.QueryRow(`SELECT state FROM network_ipam_reservations WHERE id=?`, reservation.ID).Scan(&state)
			if state != "reserved" {
				t.Fatal("refused selection changed held plan", state)
			}
		})
	}
}

func TestIPAMDockerOutcomeLossHoldsPlanAndAnnotationFailureDoesNotReplayCreation(t *testing.T) {
	for _, lost := range []bool{false, true} {
		t.Run(map[bool]string{false: "native-error", true: "annotation-race"}[lost], func(t *testing.T) {
			s, router := gatewayRouter(t, auth.RoleAdmin, false)
			s.modules.ipam = netipam.New(s.Store, func(context.Context) (netipam.Snapshot, error) {
				return netipam.Snapshot{Coverage: []netipam.Coverage{{Source: "fixture", State: "observed"}}}, nil
			})
			p, e := s.modules.ipam.CreatePool(t.Context(), netipam.PoolRequest{Name: "shared", Prefix: "10.244.0.0/16", AllocationBits: 24})
			if e != nil {
				t.Fatal(e)
			}
			reservation, e := s.modules.ipam.Reserve(t.Context(), netipam.ReserveRequest{PoolID: p.ID, Owner: "docker_network", Resource: "app-private"}, "operator")
			if e != nil {
				t.Fatal(e)
			}
			creates := 0
			engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/_ping" {
					w.Header().Set("API-Version", "1.47")
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if strings.HasSuffix(r.URL.Path, "/networks/create") {
					creates++
					if !lost {
						w.WriteHeader(500)
						w.Write([]byte(`{"message":"native response unavailable"}`))
						return
					}
					if e := s.modules.ipam.ReviewInterruptedHandoffs(context.Background()); e != nil {
						t.Error(e)
					}
					w.WriteHeader(201)
					w.Write([]byte(`{"Id":"native-created"}`))
					return
				}
				w.Write([]byte(`{"Id":"native-created","Name":"app-private","Driver":"bridge"}`))
			}))
			defer engine.Close()
			original := s.modules.docker
			s.modules.docker = dockerx.New(engine.URL)
			t.Cleanup(func() { s.modules.docker.Close(); s.modules.docker = original })
			s.mountDockerRoutes(router)
			body := string(netipamTestJSON(map[string]any{"name": "app-private", "subnet": reservation.Prefix, "ipamReservationIds": []string{reservation.ID}}))
			response := ipamDo(router, "POST", "/docker/networks/", body)
			if lost && (response.Code != 201 || !strings.Contains(response.Body.String(), "ipamWarning")) {
				t.Fatal("successful native create falsely failed and invited replay", response.Code, response.Body.String())
			}
			if !lost && response.Code < 400 {
				t.Fatal("native error became success", response.Code)
			}
			var state string
			s.Store.DB.QueryRow(`SELECT state FROM network_ipam_reservations WHERE id=?`, reservation.ID).Scan(&state)
			if state != "review_required" || creates != 1 {
				t.Fatal("lost outcome released or repeated native work", state, creates)
			}
			if e := s.modules.ipam.CheckUnselectedReservations(context.Background(), []string{reservation.Prefix}); e == nil {
				t.Fatal("unknown native outcome allocation was released")
			}
			deadlineCtx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if _, e := s.modules.ipam.BeginHandoff(deadlineCtx, []string{reservation.ID}, "docker_network", "app-private", []string{reservation.Prefix}); e == nil {
				t.Fatal("unknown native outcome silently retried")
			}
		})
	}
}
