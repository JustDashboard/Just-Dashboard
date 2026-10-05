package dockerx

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/go-connections/nat"
)

func TestAdoptionRawConfigurationKeepsKnownEmbeddedAndDynamicSettingsCompatible(t *testing.T) {
	init := false
	raw := adoptionJSON(map[string]any{
		"Config":     &container.Config{Env: []string{"PRIVATE_VALUE=artificial-private-value"}, Labels: map[string]string{"future-looking.label": "artificial-private-value"}, Healthcheck: &container.HealthConfig{Test: []string{"CMD", "true"}}},
		"HostConfig": &container.HostConfig{Init: &init, Resources: container.Resources{Memory: 128 << 20, NanoCPUs: 500000000}, PortBindings: nat.PortMap{"3000/tcp": {{HostIP: "127.0.0.1", HostPort: "3000"}}}, LogConfig: container.LogConfig{Type: "local", Config: map[string]string{"driver-option": "artificial-private-value"}}, Mounts: []mount.Mount{{Type: mount.TypeBind, Source: "/original", Target: "/app", BindOptions: &mount.BindOptions{CreateMountpoint: true}}}},
	})
	fields, err := adoptionUnrepresentedConfiguration(raw)
	if err != nil || len(fields) != 0 {
		t.Fatal("known embedded fields, JSON tags or dynamic maps were rejected", err, fields)
	}
}

func TestAdoptionRawCaptureRetainsFieldsTypedSDKWouldDrop(t *testing.T) {
	imageID := "sha256:" + strings.Repeat("2", 64)
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/_ping"):
			w.Header().Set("API-Version", "1.47")
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/containers/original/json"):
			json.NewEncoder(w).Encode(map[string]any{"Id": "original", "Image": imageID, "State": map[string]any{"Running": true, "Pid": 42}, "Config": map[string]any{"FutureFeature": false, "UnknownAbsent": nil}, "HostConfig": map[string]any{"Memory": 0, "FutureLimit": 0}})
		case r.Method == http.MethodGet && (strings.HasSuffix(r.URL.Path, "/containers/json") || strings.HasSuffix(r.URL.Path, "/history") || strings.HasSuffix(r.URL.Path, "/changes")):
			json.NewEncoder(w).Encode([]any{})
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/images/"):
			json.NewEncoder(w).Encode(map[string]any{"Id": imageID, "Os": "linux", "Architecture": "amd64", "Config": map[string]any{}})
		default:
			t.Errorf("read-only raw capture attempted an unexpected action: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer engine.Close()
	client := New(engine.URL)
	defer client.Close()
	capture, err := client.CaptureAdoptionContainer(t.Context(), "original")
	if err != nil || capture == nil || strings.Join(capture.UnrepresentedOptions, ",") != "Config.FutureFeature,HostConfig.FutureLimit" {
		t.Fatal("raw capture lost present false/zero fields during typed SDK decoding", err)
	}
	public, err := json.Marshal(capture)
	if err != nil || string(public) != "{}" {
		t.Fatal("private raw capture became public JSON")
	}
}

func TestAdoptionRawConfigurationBlocksUnknownEffectiveNestedOptionsWithoutTheirValues(t *testing.T) {
	raw := []byte(`{"Config":{"FutureEnvironment":"artificial-private-value"},"HostConfig":{"FutureMode":true,"Mounts":[{"Type":"bind","BindOptions":{"FutureOwnership":"artificial-private-value"}}],"PortBindings":{"3000/tcp":[{"HostIp":"127.0.0.1","HostPort":"3000","FutureRouting":1}]}}}`)
	fields, err := adoptionUnrepresentedConfiguration(raw)
	want := "Config.FutureEnvironment,HostConfig.FutureMode,HostConfig.Mounts[].BindOptions.FutureOwnership,HostConfig.PortBindings[][].FutureRouting"
	if err != nil || strings.Join(fields, ",") != want {
		t.Fatal("unknown effective options escaped the raw capture guard", err, fields)
	}
	encoded, _ := json.Marshal(fields)
	if strings.Contains(string(encoded), "artificial-private-value") || strings.Contains(string(encoded), "3000/tcp") {
		t.Fatal("unknown-option evidence exposed values or dynamic map keys")
	}
}

func TestAdoptionRawConfigurationOnlyAcceptsAbsentUnknownValues(t *testing.T) {
	for _, test := range []struct {
		name  string
		value string
		inert bool
	}{
		{"null", `null`, true}, {"zero", `0`, false}, {"negative_zero", `-0`, false}, {"false", `false`, false},
		{"empty_string", `""`, false}, {"empty_array", `[]`, false}, {"empty_object", `{}`, false},
		{"enabled", `true`, false}, {"nonzero", `1`, false}, {"string_zero", `"0"`, false},
		{"nonempty_zero_array", `[0]`, false}, {"structured_zero", `{"mode":0}`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			fields, err := adoptionUnrepresentedConfiguration([]byte(`{"Config":{},"HostConfig":{"FutureOption":` + test.value + `}}`))
			if err != nil || (len(fields) == 0) != test.inert {
				t.Fatal("unknown-value compatibility inferred an unsafe default", err)
			}
		})
	}
	if _, err := adoptionUnrepresentedConfiguration([]byte(`{"Config":`)); err == nil {
		t.Fatal("unreadable raw configuration was accepted")
	}
}
