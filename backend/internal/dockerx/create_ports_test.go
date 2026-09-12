package dockerx

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
)

func TestCreateRetriesOnlyBindFailuresAndReturnsActualPorts(t *testing.T) {
	for _, failure := range []string{"port is already allocated", "permission denied"} {
		t.Run(failure, func(t *testing.T) {
			listener, err := net.Listen("tcp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			occupied := listener.Addr().(*net.TCPAddr).Port
			creates, starts, removes := 0, 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				path := r.URL.Path
				switch {
				case strings.Contains(path, "/images/"):
					fmt.Fprint(w, `{"Id":"sha256:test"}`)
				case strings.HasSuffix(path, "/containers/create"):
					var body struct{ HostConfig container.HostConfig }
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					bindings := body.HostConfig.PortBindings[nat.Port("80/tcp")]
					if len(bindings) != 1 || bindings[0].HostIP != "127.0.0.1" {
						t.Errorf("binding scope changed: %+v", bindings)
					}
					if creates == 0 && bindings[0].HostPort != fmt.Sprint(occupied) {
						t.Error("preference not tried")
					}
					if creates > 0 && (bindings[0].HostPort == "" || bindings[0].HostPort == fmt.Sprint(occupied)) {
						t.Error("a new concrete port was not selected")
					}
					creates++
					fmt.Fprintf(w, `{"Id":"candidate%d","Warnings":[]}`, creates)
				case strings.HasSuffix(path, "/start"):
					starts++
					if starts == 1 {
						w.WriteHeader(500)
						_ = json.NewEncoder(w).Encode(map[string]string{"message": failure})
					} else {
						w.WriteHeader(204)
					}
				case r.Method == http.MethodDelete:
					removes++
					if r.URL.Query().Get("force") == "1" || r.URL.Query().Get("v") == "1" {
						t.Error("unsafe retry removal")
					}
					w.WriteHeader(204)
				case strings.Contains(path, "/containers/") && strings.HasSuffix(path, "/json"):
					if strings.Contains(path, "candidate") {
						fmt.Fprint(w, `{"Id":"candidate2","Name":"/port-test","NetworkSettings":{"Ports":{"80/tcp":[{"HostIp":"127.0.0.1","HostPort":"32781"}]}}}`)
					} else {
						w.WriteHeader(404)
						fmt.Fprint(w, `{"message":"not found"}`)
					}
				default:
					t.Errorf("unexpected %s %s", r.Method, path)
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			api, err := client.NewClientWithOpts(client.WithHost(server.URL), client.WithVersion("1.47"))
			if err != nil {
				t.Fatal(err)
			}
			defer api.Close()
			c := &Client{cli: api}
			result, err := c.Create(context.Background(), ContainerSpec{Name: "port-test", Image: "nginx:alpine", Start: true, Ports: []PortMapping{{HostIP: "127.0.0.1", HostPort: occupied, ContainerPort: 80, Protocol: "tcp"}}}, nil)
			if failure == "permission denied" {
				if err == nil || creates != 1 || removes != 0 {
					t.Fatalf("unrelated failure retried: %v, %d/%d", err, creates, removes)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if creates != 2 || removes != 1 || !result.Started || len(result.Ports) != 1 || result.Ports[0].HostPort != 32781 {
				t.Fatalf("result %+v, creates=%d removes=%d", result, creates, removes)
			}
		})
	}
}

func TestZeroHostPortRemainsUnpublished(t *testing.T) {
	_, host, _, _, err := (ContainerSpec{Image: "nginx", Ports: []PortMapping{{ContainerPort: 80, HostPort: 0}}}).toEngine()
	if err != nil {
		t.Fatal(err)
	}
	if len(host.PortBindings) != 0 {
		t.Fatal("unpublished service gained a host binding")
	}
}

func TestLiveCreateSurvivesOccupiedPort(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 for Docker mutation test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	c := New("unix:///var/run/docker.sock")
	result, err := c.Create(ctx, ContainerSpec{Name: fmt.Sprintf("jd-port-test-%d", time.Now().UnixNano()), Image: "nginx:alpine", Start: true, Ports: []PortMapping{{HostIP: "127.0.0.1", HostPort: port, ContainerPort: 80, Protocol: "tcp"}}}, nil)
	if result != nil && result.ID != "" {
		api, _ := c.api()
		defer api.ContainerRemove(context.Background(), result.ID, container.RemoveOptions{Force: true, RemoveVolumes: true})
	}
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Ports) != 1 || result.Ports[0].HostPort == port || result.Ports[0].HostIP != "127.0.0.1" {
		t.Fatalf("actual binding: %+v", result.Ports)
	}
	conn, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal("original owner lost its listener", err)
	}
	conn.Close()
	endpoint := fmt.Sprintf("http://127.0.0.1:%d", result.Ports[0].HostPort)
	for i := 0; i < 40; i++ {
		response, err := http.Get(endpoint)
		if err == nil {
			response.Body.Close()
			if response.StatusCode == 200 {
				api, _ := c.api()
				if err := api.ContainerStop(ctx, result.ID, container.StopOptions{}); err != nil {
					t.Fatal(err)
				}
				if err := api.ContainerStart(ctx, result.ID, container.StartOptions{}); err != nil {
					t.Fatal(err)
				}
				restarted, err := api.ContainerInspect(ctx, result.ID)
				if err != nil {
					t.Fatal(err)
				}
				ports := publishedMappings(restarted.NetworkSettings.Ports)
				if len(ports) != 1 || ports[0].HostPort != result.Ports[0].HostPort {
					t.Fatalf("restart changed runtime port: before=%+v after=%+v", result.Ports, ports)
				}
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("replacement port never served the application")
}
