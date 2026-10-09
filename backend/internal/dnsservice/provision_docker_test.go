package dnsservice

import (
	"archive/tar"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/docker/docker/client"
)

type dockerProvisionFixture struct {
	t                                                                  *testing.T
	spec                                                               provisionSpec
	resources                                                          ProvisionResources
	config, host                                                       map[string]any
	networkCreated, containerCreated, running, wildcard, foreignVolume bool
	volumes                                                            map[string]bool
	seeds                                                              map[string]string
	stops, removes, volumeRemoves, networkRemoves, execs               int
}

func (f *dockerProvisionFixture) serve(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/v1.51")
	jsonResponse := func(value any) { w.Header().Set("Content-Type", "application/json"); json.NewEncoder(w).Encode(value) }
	missing := func() {
		w.WriteHeader(404)
		jsonResponse(map[string]string{"message": "owned fixture resource not found"})
	}
	if r.Method == http.MethodGet && strings.HasPrefix(path, "/images/") {
		jsonResponse(map[string]any{"Id": f.spec.ImageID, "RepoDigests": []string{f.spec.Image}})
		return
	}
	if r.Method == http.MethodGet && strings.HasPrefix(path, "/containers/") {
		if !f.containerCreated {
			missing()
			return
		}
		host := f.host
		if f.wildcard {
			data, _ := json.Marshal(host)
			var copy map[string]any
			json.Unmarshal(data, &copy)
			host = copy
			ports := host["PortBindings"].(map[string]any)
			for _, bindings := range ports {
				bindings.([]any)[0].(map[string]any)["HostIp"] = "0.0.0.0"
			}
		}
		mounts := []any{}
		targets := volumeTargets(f.spec.Request.Engine)
		for i, name := range f.resources.Volumes {
			mounts = append(mounts, map[string]any{"Type": "volume", "Name": name, "Destination": targets[i], "RW": true})
		}
		jsonResponse(map[string]any{"Id": f.resources.ContainerID, "Name": "/" + f.resources.ContainerName, "Image": f.spec.ImageID, "Config": f.config, "HostConfig": host, "State": map[string]any{"Running": f.running}, "NetworkSettings": map[string]any{"Networks": map[string]any{f.resources.NetworkName: map[string]any{"NetworkID": f.resources.NetworkID}}}, "Mounts": mounts})
		return
	}
	if r.Method == http.MethodGet && strings.HasPrefix(path, "/networks/") {
		if !f.networkCreated {
			missing()
			return
		}
		members := map[string]any{}
		if f.containerCreated {
			members[f.resources.ContainerID] = map[string]any{}
		}
		jsonResponse(map[string]any{"Id": f.resources.NetworkID, "Name": f.resources.NetworkName, "Driver": "bridge", "Scope": "local", "Labels": ownedLabels(f.spec), "IPAM": map[string]any{"Config": []any{map[string]any{"Subnet": "172.20.0.0/16"}}}, "Containers": members})
		return
	}
	if r.Method == http.MethodGet && strings.HasPrefix(path, "/volumes/") {
		name := strings.TrimPrefix(path, "/volumes/")
		if !f.volumes[name] {
			missing()
			return
		}
		labels := ownedLabels(f.spec)
		if f.foreignVolume {
			labels[dnsOwnerLabel] = "foreign-owner"
		}
		jsonResponse(map[string]any{"Name": name, "Driver": "local", "Labels": labels})
		return
	}
	switch {
	case r.Method == http.MethodPost && path == "/networks/create":
		var body struct {
			Name, Driver string
			Labels       map[string]string
		}
		json.NewDecoder(r.Body).Decode(&body)
		if body.Name != f.resources.NetworkName || body.Driver != "bridge" || !owned(body.Labels, f.spec) {
			f.t.Error("closed bridge ownership escaped")
		}
		f.networkCreated = true
		jsonResponse(map[string]any{"Id": f.resources.NetworkID})
	case r.Method == http.MethodPost && path == "/volumes/create":
		var body struct {
			Name, Driver string
			Labels       map[string]string
		}
		json.NewDecoder(r.Body).Decode(&body)
		if body.Driver != "local" || !owned(body.Labels, f.spec) {
			f.t.Error("closed volume ownership escaped")
		}
		f.volumes[body.Name] = true
		jsonResponse(body)
	case r.Method == http.MethodPost && path == "/containers/create":
		json.NewDecoder(r.Body).Decode(&f.config)
		f.host = f.config["HostConfig"].(map[string]any)
		if f.config["Image"] != f.spec.Image || f.host["Privileged"] == true || len(f.host["Mounts"].([]any)) != 2 || !reflect.DeepEqual(f.host["CapDrop"], []any{"ALL"}) {
			f.t.Error("container privilege/image/mount contract escaped")
		}
		if strings.Contains(string(mustJSON(f.config)), f.spec.Request.Password) {
			f.t.Error("plaintext Docker create secret")
		}
		f.containerCreated = true
		w.WriteHeader(201)
		jsonResponse(map[string]any{"Id": f.resources.ContainerID, "Warnings": []string{}})
	case r.Method == http.MethodPut && strings.HasSuffix(path, "/archive"):
		reader := tar.NewReader(io.LimitReader(r.Body, 1<<20))
		for {
			header, err := reader.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				f.t.Error(err)
				break
			}
			if header.Typeflag == tar.TypeDir {
				continue
			}
			if header.Mode != 0600 || header.Size > 64<<10 {
				f.t.Error("unbounded or publicly readable seed")
			}
			data, err := io.ReadAll(reader)
			if err != nil {
				f.t.Error(err)
			}
			f.seeds[header.Name] = string(data)
		}
		w.WriteHeader(200)
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/start") && !strings.HasPrefix(path, "/exec/"):
		f.running = true
		w.WriteHeader(204)
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/exec"):
		var body struct {
			User string
			Cmd  []string
		}
		json.NewDecoder(r.Body).Decode(&body)
		if body.User != "0" || !reflect.DeepEqual(body.Cmd, []string{"rm", "-f", "--", "/run/secrets/jd_dns_password"}) {
			f.t.Error("bootstrap cleanup is not a fixed argv")
		}
		f.execs++
		jsonResponse(map[string]any{"Id": "owned-exec"})
	case r.Method == http.MethodPost && path == "/exec/owned-exec/start":
		w.WriteHeader(200)
	case r.Method == http.MethodGet && path == "/exec/owned-exec/json":
		jsonResponse(map[string]any{"Running": false, "ExitCode": 0})
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/update"):
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		f.host["RestartPolicy"] = body["RestartPolicy"]
		jsonResponse(map[string]any{"Warnings": []string{}})
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/stop"):
		f.stops++
		f.running = false
		w.WriteHeader(204)
	case r.Method == http.MethodDelete && strings.HasPrefix(path, "/containers/"):
		f.removes++
		f.containerCreated = false
		w.WriteHeader(204)
	case r.Method == http.MethodDelete && strings.HasPrefix(path, "/volumes/"):
		f.volumeRemoves++
		delete(f.volumes, strings.TrimPrefix(path, "/volumes/"))
		w.WriteHeader(204)
	case r.Method == http.MethodDelete && strings.HasPrefix(path, "/networks/"):
		f.networkRemoves++
		f.networkCreated = false
		w.WriteHeader(204)
	default:
		f.t.Errorf("unexpected Docker operation %s %s", r.Method, path)
		w.WriteHeader(500)
	}
}
func mustJSON(value any) []byte { data, _ := json.Marshal(value); return data }

func TestDNSProvisionDockerClosedSpecSecretsAndForeignCleanupRefusal(t *testing.T) {
	for _, engine := range []Engine{AdGuard, PiHole, Technitium} {
		t.Run(string(engine), func(t *testing.T) {
			id := strings.Repeat("e", 32)
			r := provisionIntent(id)
			r.NetworkID = strings.Repeat("b", 64)
			r.ContainerID = strings.Repeat("c", 64)
			spec := provisionSpec{ID: id, Owner: strings.Repeat("f", 32), Image: pinnedImages[engine], ImageID: "sha256:" + strings.Repeat("a", 64), Request: ProvisionRequest{Engine: engine, Username: "admin", Password: "explicit-native-password", ManagementPort: 43081, DNSPort: 43053, MemoryMiB: 256, CPUs: 0.5, Upstreams: []string{"192.0.2.53:5353"}}}
			fixture := &dockerProvisionFixture{t: t, spec: spec, resources: r, volumes: map[string]bool{}, seeds: map[string]string{}}
			server := httptest.NewServer(http.HandlerFunc(fixture.serve))
			defer server.Close()
			cli, err := client.NewClientWithOpts(client.WithHost(server.URL), client.WithVersion("1.51"))
			if err != nil {
				t.Fatal(err)
			}
			d := &dockerRuntime{cli: cli}
			defer d.Close()
			intent := provisionIntent(id)
			phases := []string{}
			prepared, err := d.Prepare(t.Context(), spec, intent, func(r ProvisionResources) error { phases = append(phases, r.Phase); return nil })
			if err != nil || len(phases) != 5 {
				t.Fatalf("closed SDK resource creation %+v %v phases=%v", prepared, err, phases)
			}
			if err = d.Start(t.Context(), spec, prepared); err != nil {
				t.Fatal(err)
			}
			if err = d.Verify(t.Context(), spec, prepared); err != nil {
				t.Fatal(err)
			}
			if err = d.RemoveBootstrapSecret(t.Context(), spec, prepared); err != nil {
				t.Fatal(err)
			}
			if err = d.Activate(t.Context(), spec, prepared); err != nil {
				t.Fatal(err)
			}
			if engine != AdGuard && fixture.execs != 1 {
				t.Fatal("plaintext bootstrap file was retained")
			}
			fixture.wildcard = true
			if err = d.Destroy(t.Context(), spec, prepared); err == nil || fixture.stops != 0 {
				t.Fatal("changed publication was stopped or removed")
			}
			fixture.wildcard = false
			fixture.foreignVolume = true
			if err = d.Destroy(t.Context(), spec, prepared); err == nil || fixture.stops != 0 {
				t.Fatal("foreign volume was touched before identity checks finished")
			}
			fixture.foreignVolume = false
			if err = d.Destroy(t.Context(), spec, prepared); err != nil || fixture.stops != 1 || fixture.removes != 1 || fixture.volumeRemoves != 2 || fixture.networkRemoves != 1 {
				t.Fatalf("owned SDK cleanup: %v stop=%d container=%d volumes=%d network=%d", err, fixture.stops, fixture.removes, fixture.volumeRemoves, fixture.networkRemoves)
			}
		})
	}
}
