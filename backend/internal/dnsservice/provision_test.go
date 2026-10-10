package dnsservice

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/store"
	"golang.org/x/crypto/bcrypt"
	"gopkg.in/yaml.v3"
)

type provisionFixtureRuntime struct {
	prepares, starts, cleans, secrets, activates, closes atomic.Int32
	failPrepare, failClean                               bool
	spec                                                 provisionSpec
}

func (f *provisionFixtureRuntime) Image(context.Context, Engine) (string, error) {
	return "sha256:" + strings.Repeat("a", 64), nil
}
func (f *provisionFixtureRuntime) Prepare(ctx context.Context, s provisionSpec, r ProvisionResources, journal func(ProvisionResources) error) (ProvisionResources, error) {
	f.prepares.Add(1)
	f.spec = s
	r.NetworkID = strings.Repeat("b", 64)
	r.Phase = "network_created"
	if err := journal(r); err != nil {
		return r, err
	}
	if f.failPrepare {
		return r, errors.New("controlled resource-stage failure")
	}
	r.ContainerID = strings.Repeat("c", 64)
	r.Phase = "prepared"
	return r, journal(r)
}
func (f *provisionFixtureRuntime) Start(context.Context, provisionSpec, ProvisionResources) error {
	f.starts.Add(1)
	return nil
}
func (f *provisionFixtureRuntime) Verify(context.Context, provisionSpec, ProvisionResources) error {
	return nil
}
func (f *provisionFixtureRuntime) RemoveBootstrapSecret(context.Context, provisionSpec, ProvisionResources) error {
	f.secrets.Add(1)
	return nil
}
func (f *provisionFixtureRuntime) Activate(context.Context, provisionSpec, ProvisionResources) error {
	f.activates.Add(1)
	return nil
}
func (f *provisionFixtureRuntime) Destroy(context.Context, provisionSpec, ProvisionResources) error {
	if f.failClean {
		return errors.New("controlled foreign-identity refusal")
	}
	f.cleans.Add(1)
	return nil
}
func (f *provisionFixtureRuntime) Close() error { f.closes.Add(1); return nil }

func newProvisionFixture(t *testing.T, engine Engine) (*Service, *provisionFixtureRuntime, ProvisionRequest) {
	t.Helper()
	password := "explicit-fixture-native-password"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var value any
		switch engine {
		case AdGuard:
			user, pass, ok := r.BasicAuth()
			if !ok || user != "admin" || pass != password {
				http.Error(w, "invalid auth", 401)
				return
			}
			switch r.URL.Path {
			case "/control/status":
				value = map[string]any{"version": "v0.107.71", "dns_addresses": []string{"0.0.0.0"}, "dns_port": 53, "protection_enabled": true, "running": true}
			case "/control/dns_info":
				value = map[string]any{"upstream_dns": []string{"192.0.2.53:53"}}
			case "/control/access/list":
				value = map[string]any{"allowed_clients": []string{"172.18.0.0/16"}, "disallowed_clients": []string{}, "blocked_hosts": []string{}}
			case "/control/clients":
				value = map[string]any{"clients": []any{}}
			case "/control/rewrite/list":
				value = []any{}
			case "/control/querylog":
				value = map[string]any{"data": []any{}}
			default:
				http.Error(w, "unsupported", 404)
				return
			}
		case PiHole:
			if r.URL.Path == "/api/auth" && r.Method == http.MethodPost {
				var body map[string]string
				json.NewDecoder(r.Body).Decode(&body)
				if body["password"] != password {
					http.Error(w, "invalid auth", 401)
					return
				}
				value = map[string]any{"session": map[string]any{"valid": true, "sid": "native-ftl-session"}}
			} else {
				if r.Header.Get("X-FTL-SID") != "native-ftl-session" {
					http.Error(w, "missing SID", 401)
					return
				}
				switch r.URL.Path {
				case "/api/auth":
					w.WriteHeader(204)
					return
				case "/api/info/version":
					value = map[string]any{"version": map[string]any{"ftl": map[string]any{"local": map[string]any{"version": "v6.7.1"}}}}
				case "/api/info/ftl":
					value = map[string]any{"ftl": map[string]any{"pid": 123, "uptime": 10000}}
				case "/api/config":
					value = map[string]any{"config": map[string]any{"dns": map[string]any{"upstreams": []string{"192.0.2.53#53"}, "port": 53, "listeningMode": "ALL", "hosts": []string{}, "cnameRecords": []string{}}}}
				case "/api/dns/blocking":
					value = map[string]any{"blocking": "enabled", "timer": nil}
				case "/api/clients":
					value = map[string]any{"clients": []any{}}
				case "/api/groups":
					value = map[string]any{"groups": []any{map[string]any{"id": 0, "name": "Default", "enabled": true}}}
				case "/api/queries":
					value = map[string]any{"queries": []any{}}
				default:
					http.Error(w, "unsupported", 404)
					return
				}
			}
		case Technitium:
			if r.URL.Path == "/" {
				if r.Method != http.MethodHead || r.Header.Get("Authorization") != "" {
					t.Error("Technitium readiness downloaded its console or sent a bootstrap bearer credential")
				}
				w.Header().Set("Content-Length", "646662")
				w.WriteHeader(http.StatusOK)
				return
			}
			if r.URL.Path == "/api/user/createToken" {
				r.ParseForm()
				if r.Form.Get("pass") != password || r.Form.Get("user") != "admin" || r.URL.RawQuery != "" {
					http.Error(w, "bad private bootstrap", 401)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"status": "ok", "token": "created-native-api-token"})
				return
			}
			if r.Header.Get("Authorization") != "Bearer created-native-api-token" {
				http.Error(w, "bad native token", 401)
				return
			}
			switch r.URL.Path {
			case "/api/settings/get":
				value = map[string]any{"version": "15.6", "dnsServerLocalEndPoints": []string{"0.0.0.0:53"}, "recursion": "UseSpecifiedNetworkACL", "recursionNetworkACL": []string{"172.18.0.0/16"}, "enableBlocking": true, "forwarders": []string{"192.0.2.53:53"}, "forwarderProtocol": "Udp"}
			case "/api/zones/list":
				value = map[string]any{"zones": []any{}, "totalZones": 0, "totalPages": 1}
			case "/api/apps/list":
				value = map[string]any{"apps": []any{}}
			default:
				http.Error(w, "unsupported", 404)
				return
			}
			value = map[string]any{"status": "ok", "response": value}
		}
		json.NewEncoder(w).Encode(value)
	}))
	t.Cleanup(server.Close)
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	sealer, err := auth.NewSealer(strings.Repeat("b", 64))
	if err != nil {
		t.Fatal(err)
	}
	runtime := &provisionFixtureRuntime{}
	s := New(Options{DB: db.DB, Seal: sealer.Seal, Open: sealer.Open, Runtime: runtime})
	port := server.Listener.Addr().(*net.TCPAddr).Port
	req := ProvisionRequest{Name: "Owned native engine", Engine: engine, ManagementPort: port, DNSPort: 53531, MemoryMiB: 256, CPUs: 0.5, Upstreams: []string{"192.0.2.53:53"}, Username: "admin", Password: password}
	if port == req.DNSPort {
		req.DNSPort++
	}
	return s, runtime, req
}

func TestDNSProvisionAllEngineSealingSingleUseAndOwnedLifecycle(t *testing.T) {
	for _, engine := range []Engine{AdGuard, PiHole, Technitium} {
		t.Run(string(engine), func(t *testing.T) {
			s, runtime, req := newProvisionFixture(t, engine)
			plan, err := s.PreviewProvision(t.Context(), req)
			if err != nil {
				t.Fatal(err)
			}
			encoded, _ := json.Marshal(plan)
			if strings.Contains(string(encoded), req.Password) || runtime.prepares.Load() != 0 || plan.Request.Management {
				t.Fatalf("preview leaked a secret or created resources: %s", encoded)
			}
			var sealed, metadata string
			s.db.QueryRow(`SELECT secret_enc,request_json FROM network_dns_service_provisions WHERE id=?`, plan.ID).Scan(&sealed, &metadata)
			if strings.Contains(sealed, req.Password) || strings.Contains(metadata, req.Password) {
				t.Fatal("bootstrap password persisted outside sealing")
			}
			applied, err := s.ApplyProvision(t.Context(), plan.ID)
			if err != nil || applied.State != "verified" || applied.ConnectionID == "" || runtime.prepares.Load() != 1 || runtime.starts.Load() != 1 || runtime.secrets.Load() != 1 || runtime.activates.Load() != 1 {
				t.Fatalf("owned native lifecycle: %+v %v", applied, err)
			}
			if _, err = s.ApplyProvision(t.Context(), plan.ID); !errors.Is(err, ErrConflict) || runtime.prepares.Load() != 1 {
				t.Fatalf("setup replayed: %v", err)
			}
			view, err := s.Inspect(t.Context(), applied.ConnectionID)
			if err != nil || view.Connection.Management || view.Connection.Ownership != "provisioned" || view.Snapshot == nil {
				t.Fatalf("native managed identity: %+v %v", view, err)
			}
			if err = s.Delete(t.Context(), applied.ConnectionID); err == nil {
				t.Fatal("ordinary disconnect bypassed owned volume review")
			}
			if err = s.Reconcile(t.Context()); err != nil || runtime.cleans.Load() != 0 {
				t.Fatalf("startup removed a verified engine: %v", err)
			}
			if err = s.Close(); err != nil || runtime.cleans.Load() != 0 || runtime.closes.Load() != 1 {
				t.Fatalf("shutdown removed a verified engine: %v", err)
			}
			removed, err := s.RemoveProvision(t.Context(), plan.ID)
			if err != nil || removed.State != "removed" || runtime.cleans.Load() != 1 {
				t.Fatalf("reviewed owned removal: %+v %v", removed, err)
			}
			if _, err = s.Inspect(t.Context(), applied.ConnectionID); !errors.Is(err, ErrNotFound) {
				t.Fatalf("removed engine metadata remained: %v", err)
			}
		})
	}
}

func TestDNSProvisionFailedStageCleanupAndInterruptedSetupWithoutReplay(t *testing.T) {
	for _, foreign := range []bool{false, true} {
		t.Run(strconv.FormatBool(foreign), func(t *testing.T) {
			s, runtime, req := newProvisionFixture(t, AdGuard)
			runtime.failPrepare = true
			runtime.failClean = foreign
			plan, err := s.PreviewProvision(t.Context(), req)
			if err != nil {
				t.Fatal(err)
			}
			failed, err := s.ApplyProvision(t.Context(), plan.ID)
			expected := "failed"
			if foreign {
				expected = "needs_review"
			}
			if err != nil || failed.State != expected || runtime.starts.Load() != 0 || runtime.prepares.Load() != 1 {
				t.Fatalf("failed stage %+v %v", failed, err)
			}
			if _, err = s.ApplyProvision(t.Context(), plan.ID); !errors.Is(err, ErrConflict) {
				t.Fatalf("failed setup replayed: %v", err)
			}
			runtime.failClean = false
			plan, err = s.PreviewProvision(t.Context(), req)
			if err != nil {
				t.Fatal(err)
			}
			resources := plan.Resources
			resources.NetworkID = strings.Repeat("b", 64)
			resources.Phase = "network_created"
			data, _ := json.Marshal(resources)
			if _, err = s.db.Exec(`UPDATE network_dns_service_provisions SET state='applying',resources_json=? WHERE id=?`, string(data), plan.ID); err != nil {
				t.Fatal(err)
			}
			prepares := runtime.prepares.Load()
			if err = s.Reconcile(t.Context()); err != nil {
				t.Fatal(err)
			}
			reconciled, err := s.Provision(t.Context(), plan.ID)
			if err != nil || reconciled.State != "failed" || runtime.prepares.Load() != prepares || runtime.starts.Load() != 0 {
				t.Fatalf("interrupted native work replayed: %+v %v", reconciled, err)
			}
		})
	}
}

func TestDNSProvisionExpiresAndFencesRemovalAgainstNativeChanges(t *testing.T) {
	s, runtime, req := newProvisionFixture(t, AdGuard)
	req.Management = true
	p, err := s.PreviewProvision(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	s.db.Exec(`UPDATE network_dns_service_provisions SET expires_at=? WHERE id=?`, time.Now().Add(-time.Second).UnixMilli(), p.ID)
	if _, err = s.ApplyProvision(t.Context(), p.ID); !errors.Is(err, ErrConflict) || runtime.prepares.Load() != 0 {
		t.Fatalf("expired provision changed resources: %v", err)
	}
	p, err = s.PreviewProvision(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	p, err = s.ApplyProvision(t.Context(), p.ID)
	if err != nil || p.State != "verified" {
		t.Fatalf("native provision %+v %v", p, err)
	}
	enabled := false
	change, err := s.Preview(t.Context(), p.ConnectionID, ChangeRequest{Action: "protection", Protection: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	s.db.Exec(`UPDATE network_dns_service_changes SET state='applying' WHERE id=?`, change.ID)
	if _, err = s.RemoveProvision(t.Context(), p.ID); !errors.Is(err, ErrConflict) || runtime.cleans.Load() != 0 {
		t.Fatalf("removal raced an applying native plan: %v", err)
	}
}

func TestDNSProvisionNativeSeedAndClosedDockerEnvironment(t *testing.T) {
	for _, engine := range []Engine{AdGuard, PiHole, Technitium} {
		t.Run(string(engine), func(t *testing.T) {
			req := ProvisionRequest{Engine: engine, Username: "admin", Password: "explicit-native-password", Upstreams: []string{"192.0.2.53:5353", "[2001:db8::53]:53"}}
			spec := provisionSpec{ID: strings.Repeat("d", 32), Request: req}
			files, err := nativeSeedFiles(spec, "172.18.0.0/16")
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range nativeEnvironment(spec, "172.18.0.0/16") {
				if strings.Contains(entry, req.Password) {
					t.Fatal("plaintext Docker environment credential")
				}
			}
			if engine == AdGuard {
				var config struct {
					Version int `yaml:"schema_version"`
					Users   []struct {
						Name     string `yaml:"name"`
						Password string `yaml:"password"`
					} `yaml:"users"`
				}
				data := files["opt/adguardhome/conf/AdGuardHome.yaml"]
				if yaml.Unmarshal(data, &config) != nil || config.Version != 32 || len(config.Users) != 1 || bcrypt.CompareHashAndPassword([]byte(config.Users[0].Password), []byte(req.Password)) != nil || strings.Contains(string(data), req.Password) {
					t.Fatal("native AdGuard schema/password bootstrap is invalid")
				}
			}
			if engine == PiHole {
				config := string(files["etc/pihole/pihole.toml"])
				if !strings.Contains(config, "192.0.2.53#5353") || !strings.Contains(config, "2001:db8::53#53") || len(files["etc/pihole/adlists.list"]) != 0 {
					t.Fatal("native FTL bootstrap altered upstream ports or installed a default list")
				}
			}
		})
	}
}
