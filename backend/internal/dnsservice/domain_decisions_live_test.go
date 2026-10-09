package dnsservice

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"debug/elf"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"os"
	"reflect"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/errdefs"
	"github.com/docker/docker/pkg/stdcopy"
)

const decisionLabel = "io.justdashboard.dns.decision-fixture"
const decisionQueryLimit = 256
const decisionRounds = 8
const decisionNameCount = 8
const decisionFullMatrices = 5
const decisionSettlements = 6

type decisionResult struct {
	ID       uint16 `json:"id"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	Protocol string `json:"protocol"`
	Address  string `json:"address"`
}

type decisionCase struct {
	name    string
	blocked bool
}

func decisionMatrix(engine Engine, nonce, phase string) ([]decisionCase, error) {
	if engine != AdGuard && engine != PiHole || !regexp.MustCompile(`^[a-f0-9]{12}$`).MatchString(nonce) || phase != "disabled" && phase != "baseline" && phase != "added" && phase != "removed" {
		return nil, errors.New("unsupported decision fixture engine, nonce or phase")
	}
	parent, deny := "seed-"+nonce+".invalid", "deny-"+nonce+".invalid"
	baseline := phase != "disabled"
	added := phase == "added"
	return []decisionCase{{parent, baseline}, {"allow." + parent, baseline && !added},
		{"child.allow." + parent, baseline && (!added || engine == PiHole)}, {deny, added},
		{"child." + deny, added && engine == AdGuard}, {"empty-" + nonce + ".invalid", false},
		{"neutral-" + nonce + ".invalid", false}, {"seed-" + nonce + "-lookalike.invalid", false}}, nil
}

func decisionMaximumQueries() int {
	// Five full matrices cover disabled, baseline, added, restart and removed.
	// Each of six asynchronous transitions gets eight two-question rounds.
	return decisionFullMatrices*decisionNameCount*2*2 + decisionSettlements*decisionRounds*2
}

func decisionHelper(path, digest string) ([]byte, error) {
	if !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(digest) {
		return nil, errors.New("exact frozen helper digest required")
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil || !before.Mode().IsRegular() || before.Size() < 1 || before.Size() > 32<<20 || before.Mode().Perm()&0022 != 0 {
		return nil, errors.New("helper lacks bounded regular-file ownership")
	}
	st, ok := before.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != 1000 || st.Nlink != 1 {
		return nil, errors.New("helper must belong to the task account with one link")
	}
	body, err := io.ReadAll(io.LimitReader(f, (32<<20)+1))
	after, e := f.Stat()
	current, currentErr := os.Lstat(path)
	if err != nil || e != nil || currentErr != nil || len(body) != int(before.Size()) || !os.SameFile(before, after) || !os.SameFile(before, current) || before.ModTime() != after.ModTime() || before.Size() != after.Size() || after.Mode() != before.Mode() {
		return nil, errors.New("helper changed during the held-file read")
	}
	for _, info := range []os.FileInfo{after, current} {
		observed, ok := info.Sys().(*syscall.Stat_t)
		if !ok || observed.Uid != st.Uid || observed.Gid != st.Gid || observed.Nlink != st.Nlink || observed.Ctim != st.Ctim || info.Mode() != before.Mode() || info.Size() != before.Size() || info.ModTime() != before.ModTime() {
			return nil, errors.New("helper ownership/write receipt changed during its read")
		}
	}
	hash := sha256.Sum256(body)
	if hex.EncodeToString(hash[:]) != digest {
		return nil, errors.New("helper differs from the frozen digest")
	}
	file, err := elf.NewFile(bytes.NewReader(body))
	if err != nil {
		return nil, errors.New("helper is not ELF")
	}
	defer file.Close()
	if file.Class != elf.ELFCLASS64 || file.Machine != elf.EM_X86_64 || file.Type != elf.ET_EXEC {
		return nil, errors.New("helper is not the static Linux/amd64 executable")
	}
	for _, program := range file.Progs {
		if program.Type == elf.PT_INTERP || program.Type == elf.PT_DYNAMIC {
			return nil, errors.New("helper has a dynamic loader dependency")
		}
	}
	return body, nil
}

type decisionSidecar struct {
	d                        *dockerRuntime
	nonce, digest, tag       string
	imageID, containerID     string
	layerID                  string
	networkID, address       string
	mac                      string
	engineID                 string
	engineAddress, engineMAC string
	spec                     provisionSpec
	resources                ProvisionResources
}

type decisionEndpointReceipt struct {
	Present bool   `json:"present"`
	ID      string `json:"id"`
	Address string `json:"address"`
	MAC     string `json:"mac"`
}

type decisionContainerReceipt struct {
	Present      bool                    `json:"present"`
	ID           string                  `json:"id"`
	Image        string                  `json:"image"`
	Running      bool                    `json:"running"`
	Restarting   bool                    `json:"restarting"`
	PID          int                     `json:"pid"`
	ConfigSHA256 string                  `json:"configSHA256"`
	HostSHA256   string                  `json:"hostSHA256"`
	MountsSHA256 string                  `json:"mountsSHA256"`
	NetworkCount int                     `json:"networkCount"`
	Endpoint     decisionEndpointReceipt `json:"endpoint"`
}

type decisionRuntimeReceipt struct {
	Engine          decisionContainerReceipt `json:"engine"`
	Sidecar         decisionContainerReceipt `json:"sidecar"`
	BridgePresent   bool                     `json:"bridgePresent"`
	BridgeID        string                   `json:"bridgeID"`
	BridgeOwned     bool                     `json:"bridgeOwned"`
	BridgeLocal     bool                     `json:"bridgeLocal"`
	BridgeDriver    bool                     `json:"bridgeDriver"`
	BridgeInternal  bool                     `json:"bridgeInternal"`
	BridgeIngress   bool                     `json:"bridgeIngress"`
	EndpointCount   int                      `json:"endpointCount"`
	BridgeEngine    decisionEndpointReceipt  `json:"bridgeEngine"`
	BridgeSidecar   decisionEndpointReceipt  `json:"bridgeSidecar"`
	ExecPresent     bool                     `json:"execPresent"`
	ExecRunning     bool                     `json:"execRunning"`
	ExecExitCode    int                      `json:"execExitCode"`
	ExecContainerID string                   `json:"execContainerID"`
	StderrSHA256    string                   `json:"stderrSHA256"`
	HelperCode      string                   `json:"helperCode"`
}

type decisionQueryRefusal struct {
	Stage, Code, OriginalSHA256 string
	Runtime                     decisionRuntimeReceipt
	original                    error
}

func (e *decisionQueryRefusal) Error() string {
	return fmt.Sprintf("decision query refused stage=%s code=%s originalSHA256=%s", e.Stage, e.Code, e.OriginalSHA256)
}

func (e *decisionQueryRefusal) Unwrap() error { return e.original }

func decisionDigest(value any) string {
	body, err := json.Marshal(value)
	if err != nil {
		return "unavailable"
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func decisionDiagnosticID(value string) string {
	if value == "" || regexp.MustCompile(`^(sha256:)?[a-f0-9]{64}$`).MatchString(value) {
		return value
	}
	return "malformed"
}

func decisionEndpoint(present bool, id, address, mac string) decisionEndpointReceipt {
	result := decisionEndpointReceipt{Present: present, ID: decisionDiagnosticID(id)}
	if address != "" {
		parsed, err := netip.ParsePrefix(address)
		if err != nil {
			if host, e := netip.ParseAddr(address); e == nil && host.Is4() && host.IsPrivate() {
				result.Address = host.String()
			} else {
				result.Address = "malformed"
			}
		} else if parsed.Addr().Is4() && parsed.Addr().IsPrivate() {
			result.Address = parsed.String()
		} else {
			result.Address = "malformed"
		}
	}
	if mac != "" {
		if regexp.MustCompile(`^[a-fA-F0-9]{2}(:[a-fA-F0-9]{2}){5}$`).MatchString(mac) {
			result.MAC = strings.ToLower(mac)
		} else {
			result.MAC = "malformed"
		}
	}
	return result
}

func decisionContainer(info container.InspectResponse, networkID string) decisionContainerReceipt {
	result := decisionContainerReceipt{Present: info.ContainerJSONBase != nil, ConfigSHA256: decisionDigest(info.Config)}
	if info.ContainerJSONBase == nil {
		return result
	}
	result.ID, result.Image = decisionDiagnosticID(info.ID), decisionDiagnosticID(info.Image)
	result.HostSHA256, result.MountsSHA256 = decisionDigest(info.HostConfig), decisionDigest(info.Mounts)
	if info.State != nil {
		result.Running, result.Restarting, result.PID = info.State.Running, info.State.Restarting, info.State.Pid
	}
	if info.NetworkSettings != nil {
		result.NetworkCount = len(info.NetworkSettings.Networks)
		for _, endpoint := range info.NetworkSettings.Networks {
			if endpoint != nil && endpoint.NetworkID == networkID {
				result.Endpoint = decisionEndpoint(true, endpoint.EndpointID, endpoint.IPAddress, endpoint.MacAddress)
			}
		}
	}
	return result
}

func (s *decisionSidecar) bridgeReceipt(nw network.Inspect) decisionRuntimeReceipt {
	engine, enginePresent := nw.Containers[s.engineID]
	sidecar, sidecarPresent := nw.Containers[s.containerID]
	return decisionRuntimeReceipt{BridgePresent: nw.ID != "", BridgeID: decisionDiagnosticID(nw.ID),
		BridgeOwned: owned(nw.Labels, s.spec), BridgeLocal: nw.Scope == "local", BridgeDriver: nw.Driver == "bridge",
		BridgeInternal: nw.Internal, BridgeIngress: nw.Ingress, EndpointCount: len(nw.Containers),
		BridgeEngine:  decisionEndpoint(enginePresent, engine.EndpointID, engine.IPv4Address, engine.MacAddress),
		BridgeSidecar: decisionEndpoint(sidecarPresent, sidecar.EndpointID, sidecar.IPv4Address, sidecar.MacAddress)}
}

func decisionRefusal(stage string, original error, receipt decisionRuntimeReceipt) error {
	code := map[string]string{
		"question target differs from the captured engine endpoint":   "target_changed",
		"sidecar exact identity changed":                              "sidecar_identity_changed",
		"sidecar isolation or bounds changed":                         "sidecar_bounds_changed",
		"sidecar bridge or client identity changed":                   "sidecar_endpoint_changed",
		"owned DNS container identity changed":                        "engine_identity_changed",
		"owned DNS container isolation or resource contract changed":  "engine_isolation_changed",
		"owned DNS privilege or resource bound changed":               "engine_bounds_changed",
		"owned DNS loopback publication or bridge membership changed": "engine_publication_or_membership_changed",
		"owned DNS bridge identity changed":                           "engine_network_changed",
		"owned DNS persistent mount identity changed":                 "engine_mount_changed",
		"exact ordinary owned bridge and two endpoints required":      "bridge_owner_or_roster_changed",
		"owned engine bridge endpoint differs":                        "engine_endpoint_changed",
		"observed default-client bridge endpoint differs":             "client_endpoint_changed",
		"owned engine restart changed more than its endpoint":         "engine_restart_transition_changed",
	}[original.Error()]
	if code == "" {
		code = "unreported_native_error"
		if errors.Is(original, context.Canceled) {
			code = "cancelled"
		} else if errors.Is(original, context.DeadlineExceeded) {
			code = "deadline"
		} else if errdefs.IsNotFound(original) {
			code = "native_not_found"
		}
	}
	sum := sha256.Sum256([]byte(original.Error()))
	return &decisionQueryRefusal{Stage: stage, Code: code, OriginalSHA256: hex.EncodeToString(sum[:]), Runtime: receipt, original: original}
}

func decisionHelperCode(body []byte) string {
	for message, code := range map[string]string{
		"owned engine DNS connection failed\n":                                             "dns_connection_failed",
		"owned engine DNS exchange failed\n":                                               "dns_exchange_failed",
		"DNS response lacks the exact fixture question and single successful answer\n":     "dns_response_shape_changed",
		"DNS answer owner/type/class differs\n":                                            "dns_answer_identity_changed",
		"DNS answer is outside A/AAAA fixture bodies\n":                                    "dns_answer_type_changed",
		"DNS answer is neither the owned upstream value nor the explicit null-IP denial\n": "dns_answer_value_changed",
	} {
		if string(body) == message {
			return code
		}
	}
	if len(body) == 0 {
		return "empty"
	}
	return "unreported_helper_error"
}

func (s *decisionSidecar) labels() map[string]string {
	return map[string]string{decisionLabel: s.nonce, "io.justdashboard.dns.decision-helper": s.digest}
}

func decisionRootFS(helper []byte, digest string) ([]byte, string, error) {
	hash := sha256.Sum256(helper)
	if len(helper) == 0 || len(helper) > 32<<20 || hex.EncodeToString(hash[:]) != digest {
		return nil, "", errors.New("sole rootfs helper differs from its frozen size/digest bound")
	}
	var archive bytes.Buffer
	w := tar.NewWriter(&archive)
	if err := w.WriteHeader(&tar.Header{Name: "dns-fixture", Mode: 0555, Uid: 0, Gid: 0, Typeflag: tar.TypeReg, Format: tar.FormatUSTAR, Size: int64(len(helper))}); err != nil {
		return nil, "", err
	}
	if _, err := w.Write(helper); err != nil {
		return nil, "", err
	}
	if err := w.Close(); err != nil {
		return nil, "", err
	}
	rootfs := archive.Bytes()
	hash = sha256.Sum256(rootfs)
	return rootfs, "sha256:" + hex.EncodeToString(hash[:]), nil
}

func (s *decisionSidecar) importHelper(ctx context.Context, helper []byte) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if !regexp.MustCompile(`^[a-f0-9]{12}$`).MatchString(s.nonce) || s.tag != "jd-dns-decisions:"+s.nonce {
		return errors.New("rootfs import requires the closed fixture nonce/tag")
	}
	rootfs, layerID, err := decisionRootFS(helper, s.digest)
	if err != nil {
		return err
	}
	if _, err := s.d.cli.ImageInspect(ctx, s.tag); !errdefs.IsNotFound(err) {
		return errors.New("fixture image tag already exists or is unreadable")
	}
	changes := []string{"USER 65534:65534", "WORKDIR /", `ENTRYPOINT ["/dns-fixture"]`, fmt.Sprintf(`CMD ["serve",%q]`, s.nonce), fmt.Sprintf("LABEL %s=%s io.justdashboard.dns.decision-helper=%s", decisionLabel, s.nonce, s.digest)}
	response, err := s.d.cli.ImageImport(ctx, image.ImportSource{Source: bytes.NewReader(rootfs), SourceName: "-"}, s.tag, image.ImportOptions{Platform: "linux/amd64", Message: "Owned finite DNS decision fixture", Changes: changes})
	if err != nil {
		return errors.New("owned local rootfs import request failed")
	}
	defer response.Close()
	body, err := io.ReadAll(io.LimitReader(response, 8193))
	if err != nil || len(body) > 8192 {
		return errors.New("rootfs import evidence exceeds its bound")
	}
	completedID, err := decisionImportID(body)
	if err != nil {
		return err
	}
	info, err := s.d.cli.ImageInspect(ctx, s.tag)
	s.layerID = layerID
	if err != nil || s.imageIdentity(info, completedID) != nil {
		return errors.New("imported fixture image differs from its completed ID, layer or fixed configuration")
	}
	s.imageID = completedID
	return nil
}

func (s *decisionSidecar) imageIdentity(info image.InspectResponse, id string) error {
	if info.ID != id || info.Parent != "" || info.Config == nil || !reflect.DeepEqual(info.Config.Labels, s.labels()) || info.Config.User != "65534:65534" || info.Config.WorkingDir != "/" || !reflect.DeepEqual(info.Config.Entrypoint, []string{"/dns-fixture"}) || !reflect.DeepEqual(info.Config.Cmd, []string{"serve", s.nonce}) || len(info.Config.Env) != 0 || len(info.Config.ExposedPorts) != 0 || len(info.Config.Volumes) != 0 || info.Config.Healthcheck != nil || len(info.Config.OnBuild) != 0 || len(info.Config.Shell) != 0 || info.Size < 0 || info.Size > 40<<20 || info.RootFS.Type != "layers" || !reflect.DeepEqual(info.RootFS.Layers, []string{s.layerID}) || s.layerID == "" || info.Os != "linux" || info.Architecture != "amd64" || info.Variant != "" || len(info.RepoTags) != 1 || info.RepoTags[0] != s.tag {
		return errors.New("rootfs image content/configuration/identity differs")
	}
	return nil
}

func decisionImportID(body []byte) (string, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	opening, e1 := decoder.Token()
	key, e2 := decoder.Token()
	var id string
	e3 := decoder.Decode(&id)
	closing, e4 := decoder.Token()
	if e1 != nil || opening != json.Delim('{') || e2 != nil || key != "status" || e3 != nil || !regexp.MustCompile(`^sha256:[a-f0-9]{64}$`).MatchString(id) || e4 != nil || closing != json.Delim('}') || decoder.Decode(new(any)) != io.EOF {
		return "", errors.New("rootfs import lacks one closed successful full image ID")
	}
	return id, nil
}

func (s *decisionSidecar) start(ctx context.Context) error {
	name := "jd-dns-decisions-" + s.nonce
	if _, err := s.d.cli.ContainerInspect(ctx, name); !errdefs.IsNotFound(err) {
		return errors.New("sidecar name exists or is unreadable")
	}
	pids := int64(16)
	host := &container.HostConfig{NetworkMode: container.NetworkMode(s.networkID), ReadonlyRootfs: true, CapDrop: []string{"ALL"}, SecurityOpt: []string{"no-new-privileges:true"}, Resources: container.Resources{Memory: 64 << 20, MemorySwap: 64 << 20, NanoCPUs: 250000000, PidsLimit: &pids}, RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyDisabled}, LogConfig: container.LogConfig{Type: "json-file", Config: map[string]string{"max-size": "64k", "max-file": "1"}}}
	config := &container.Config{Image: s.imageID, User: "65534:65534", Entrypoint: []string{"/dns-fixture"}, Cmd: []string{"serve", s.nonce}, Labels: s.labels()}
	created, err := s.d.cli.ContainerCreate(ctx, config, host, &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{s.networkID: {NetworkID: s.networkID}}}, nil, name)
	if err != nil {
		return errors.New("owned decision sidecar creation failed")
	}
	s.containerID = created.ID
	if err = s.d.cli.ContainerStart(ctx, s.containerID, container.StartOptions{}); err != nil {
		return errors.New("owned decision sidecar start failed")
	}
	info, err := s.inspect(ctx)
	if err != nil {
		return err
	}
	for _, endpoint := range info.NetworkSettings.Networks {
		address, err := netip.ParseAddr(endpoint.IPAddress)
		if err != nil || !address.Is4() || !address.IsPrivate() || endpoint.MacAddress == "" {
			return errors.New("owned source IP/MAC is unreadable")
		}
		s.address, s.mac = address.String(), endpoint.MacAddress
	}
	return nil
}

func (s *decisionSidecar) inspect(ctx context.Context) (container.InspectResponse, error) {
	info, err := s.d.cli.ContainerInspect(ctx, s.containerID)
	if err != nil {
		return info, err
	}
	if info.ID != s.containerID || info.Name != "/jd-dns-decisions-"+s.nonce || info.Image != s.imageID || info.Config == nil || info.HostConfig == nil || info.NetworkSettings == nil || info.State == nil || !reflect.DeepEqual(info.Config.Labels, s.labels()) || info.Config.Image != s.imageID || info.Config.User != "65534:65534" || !reflect.DeepEqual([]string(info.Config.Entrypoint), []string{"/dns-fixture"}) || !reflect.DeepEqual([]string(info.Config.Cmd), []string{"serve", s.nonce}) {
		return info, errors.New("sidecar exact identity changed")
	}
	h := info.HostConfig
	if !h.ReadonlyRootfs || h.Privileged || h.NetworkMode != container.NetworkMode(s.networkID) || h.PidMode != "" || len(info.Mounts) != 0 || len(h.Binds) != 0 || len(h.Tmpfs) != 0 || len(h.Devices) != 0 || len(h.DeviceRequests) != 0 || len(h.DeviceCgroupRules) != 0 || len(h.CapAdd) != 0 || !reflect.DeepEqual([]string(h.CapDrop), []string{"ALL"}) || !reflect.DeepEqual(h.SecurityOpt, []string{"no-new-privileges:true"}) || len(h.PortBindings) != 0 || len(info.NetworkSettings.Ports) != 0 || h.Memory != 64<<20 || h.MemorySwap != 64<<20 || h.NanoCPUs != 250000000 || h.PidsLimit == nil || *h.PidsLimit != 16 || h.RestartPolicy.Name != container.RestartPolicyDisabled || len(info.NetworkSettings.Networks) != 1 {
		return info, errors.New("sidecar isolation or bounds changed")
	}
	for _, endpoint := range info.NetworkSettings.Networks {
		if endpoint.NetworkID != s.networkID && (info.State.Running || endpoint.NetworkID != "") || s.address != "" && info.State.Running && (endpoint.IPAddress != s.address || endpoint.MacAddress != s.mac) {
			return info, errors.New("sidecar bridge or client identity changed")
		}
	}
	return info, nil
}

func (s *decisionSidecar) cleanup(ctx context.Context) error {
	if s.containerID != "" {
		if _, err := s.inspect(ctx); err != nil && !errdefs.IsNotFound(err) {
			return err
		} else if err == nil {
			seconds := 3
			if err = s.d.cli.ContainerStop(ctx, s.containerID, container.StopOptions{Timeout: &seconds}); err != nil && !errdefs.IsNotFound(err) {
				return err
			}
			if _, err = s.inspect(ctx); err != nil {
				return err
			}
			if err = s.d.cli.ContainerRemove(ctx, s.containerID, container.RemoveOptions{}); err != nil {
				return err
			}
		}
		if _, err := s.d.cli.ContainerInspect(ctx, s.containerID); !errdefs.IsNotFound(err) {
			return errors.New("exact sidecar remains after removal")
		}
	}
	if s.imageID != "" {
		info, err := s.d.cli.ImageInspect(ctx, s.imageID)
		if err != nil && !errdefs.IsNotFound(err) {
			return err
		}
		if err == nil {
			if s.imageIdentity(info, s.imageID) != nil {
				return errors.New("preserve changed/foreign fixture image")
			}
			if _, err = s.d.cli.ImageRemove(ctx, s.imageID, image.RemoveOptions{PruneChildren: false}); err != nil {
				return err
			}
		}
		if _, err = s.d.cli.ImageInspect(ctx, s.imageID); !errdefs.IsNotFound(err) {
			return errors.New("exact sidecar image remains after removal")
		}
	}
	return nil
}

func (s *decisionSidecar) bridge(ctx context.Context) error {
	_, err := s.bridgeRead(ctx)
	return err
}

func (s *decisionSidecar) bridgeRead(ctx context.Context) (decisionRuntimeReceipt, error) {
	info, err := s.d.containerIdentity(ctx, s.spec, s.resources)
	engine := decisionContainer(info, s.networkID)
	if err != nil {
		receipt := decisionRuntimeReceipt{Engine: engine}
		return receipt, decisionRefusal("engine_identity", err, receipt)
	}
	nw, err := s.d.cli.NetworkInspect(ctx, s.networkID, network.InspectOptions{})
	receipt := s.bridgeReceipt(nw)
	receipt.Engine = engine
	if err != nil {
		return receipt, decisionRefusal("bridge_inspect", err, receipt)
	}
	if err = s.bridgeIdentity(nw); err != nil {
		return receipt, decisionRefusal("bridge_identity", err, receipt)
	}
	return receipt, nil
}

func (s *decisionSidecar) queryGuard(ctx context.Context) (decisionRuntimeReceipt, error) {
	info, err := s.inspect(ctx)
	sidecar := decisionContainer(info, s.networkID)
	if err != nil {
		receipt := decisionRuntimeReceipt{Sidecar: sidecar}
		return receipt, decisionRefusal("sidecar_identity", err, receipt)
	}
	receipt, err := s.bridgeRead(ctx)
	receipt.Sidecar = sidecar
	if refusal := new(decisionQueryRefusal); errors.As(err, &refusal) {
		refusal.Runtime = receipt
	}
	return receipt, err
}

// Pinned Moby restart reconnects the same container through a fresh endpoint,
// so only the engine PID, endpoint ID and MAC may differ from the pre-restart
// receipt. The questions keep targeting the unchanged engine address.
func (s *decisionSidecar) restarted(ctx context.Context, before decisionRuntimeReceipt) (decisionRuntimeReceipt, error) {
	info, err := s.inspect(ctx)
	sidecar := decisionContainer(info, s.networkID)
	if err != nil {
		receipt := decisionRuntimeReceipt{Sidecar: sidecar}
		return receipt, decisionRefusal("sidecar_identity", err, receipt)
	}
	info, err = s.d.containerIdentity(ctx, s.spec, s.resources)
	engine := decisionContainer(info, s.networkID)
	if err != nil {
		receipt := decisionRuntimeReceipt{Engine: engine, Sidecar: sidecar}
		return receipt, decisionRefusal("engine_identity", err, receipt)
	}
	nw, err := s.d.cli.NetworkInspect(ctx, s.networkID, network.InspectOptions{})
	receipt := s.bridgeReceipt(nw)
	receipt.Engine, receipt.Sidecar = engine, sidecar
	if err != nil {
		return receipt, decisionRefusal("bridge_inspect", err, receipt)
	}
	want := before
	want.Engine.PID, want.Engine.Endpoint.ID, want.Engine.Endpoint.MAC = engine.PID, engine.Endpoint.ID, engine.Endpoint.MAC
	want.BridgeEngine.ID, want.BridgeEngine.MAC = engine.Endpoint.ID, engine.Endpoint.MAC
	fresh := engine.Endpoint.ID != "" && engine.Endpoint.ID != "malformed" && engine.Endpoint.MAC != "" && engine.Endpoint.MAC != "malformed"
	if receipt != want || !fresh || engine.PID == before.Engine.PID || before.Engine.Endpoint.Address != s.engineAddress {
		return receipt, decisionRefusal("restart_transition", errors.New("owned engine restart changed more than its endpoint"), receipt)
	}
	previous := s.engineMAC
	s.engineMAC = nw.Containers[s.engineID].MacAddress
	if err = s.bridgeIdentity(nw); err != nil {
		s.engineMAC = previous
		return receipt, decisionRefusal("bridge_identity", err, receipt)
	}
	return receipt, nil
}

func (s *decisionSidecar) bridgeIdentity(nw network.Inspect) error {
	if nw.ID != s.networkID || nw.Driver != "bridge" || nw.Scope != "local" || nw.Ingress || nw.Internal || s.spec.ID == "" || s.spec.Owner == "" || !owned(nw.Labels, s.spec) || len(nw.Containers) != 2 || s.engineID == "" || s.containerID == "" {
		return errors.New("exact ordinary owned bridge and two endpoints required")
	}
	engine, ok := nw.Containers[s.engineID]
	address, err := netip.ParsePrefix(engine.IPv4Address)
	if !ok || err != nil || address.Addr().String() != s.engineAddress || engine.MacAddress != s.engineMAC || s.engineAddress == "" || s.engineMAC == "" {
		return errors.New("owned engine bridge endpoint differs")
	}
	endpoint, ok := nw.Containers[s.containerID]
	address, err = netip.ParsePrefix(endpoint.IPv4Address)
	if !ok || err != nil || address.Addr().String() != s.address || endpoint.MacAddress != s.mac {
		return errors.New("observed default-client bridge endpoint differs")
	}
	return nil
}

func (s *decisionSidecar) query(ctx context.Context, target, name, kind, protocol string, id uint16) (decisionResult, error) {
	if target != s.engineAddress {
		return decisionResult{}, decisionRefusal("target", errors.New("question target differs from the captured engine endpoint"), decisionRuntimeReceipt{})
	}
	receipt, err := s.queryGuard(ctx)
	if err != nil {
		return decisionResult{}, err
	}
	execution, err := s.d.cli.ContainerExecCreate(ctx, s.containerID, container.ExecOptions{User: "65534:65534", AttachStdout: true, AttachStderr: true, Cmd: []string{"/dns-fixture", "query", s.nonce, target + ":53", protocol, kind, name, fmt.Sprint(id)}})
	if err != nil {
		return decisionResult{}, decisionRefusal("exec_create", err, receipt)
	}
	attached, err := s.d.cli.ContainerExecAttach(ctx, execution.ID, container.ExecAttachOptions{})
	if err != nil {
		return decisionResult{}, decisionRefusal("exec_attach", err, receipt)
	}
	defer attached.Close()
	attached.Conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var stdout, stderr bytes.Buffer
	if _, err = stdcopy.StdCopy(&stdout, &stderr, io.LimitReader(attached.Reader, 8193)); err != nil || stdout.Len()+stderr.Len() > 8192 {
		if err == nil {
			err = errors.New("bounded sidecar query capture failed")
		}
		return decisionResult{}, decisionRefusal("exec_capture", err, receipt)
	}
	state, err := s.d.cli.ContainerExecInspect(ctx, execution.ID)
	stderrHash := sha256.Sum256(stderr.Bytes())
	receipt.ExecPresent, receipt.ExecRunning, receipt.ExecExitCode, receipt.ExecContainerID = err == nil, state.Running, state.ExitCode, decisionDiagnosticID(state.ContainerID)
	receipt.StderrSHA256, receipt.HelperCode = hex.EncodeToString(stderrHash[:]), decisionHelperCode(stderr.Bytes())
	if err != nil || state.ContainerID != s.containerID || state.Running || state.ExitCode != 0 || stderr.Len() != 0 {
		if err == nil {
			err = errors.New("sidecar query did not terminate successfully")
		}
		return decisionResult{}, decisionRefusal("exec_result", err, receipt)
	}
	var result decisionResult
	decoder := json.NewDecoder(&stdout)
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&result); err != nil || result.ID != id || result.Name != name || result.Type != kind || result.Protocol != protocol || decoder.Decode(new(any)) != io.EOF {
		if err == nil {
			err = errors.New("sidecar query outcome lacks exact immutable identity")
		}
		return decisionResult{}, decisionRefusal("exec_output", err, receipt)
	}
	return result, nil
}

type decisionQueries struct {
	t      *testing.T
	side   *decisionSidecar
	ctx    context.Context
	target string
	count  int
	next   time.Time
}

func (q *decisionQueries) read(name, kind, protocol string) (decisionResult, error) {
	if q.count >= decisionQueryLimit {
		return decisionResult{}, errors.New("fixed decision query budget exhausted")
	}
	if wait := time.Until(q.next); wait > 0 {
		select {
		case <-q.ctx.Done():
			return decisionResult{}, q.ctx.Err()
		case <-time.After(wait):
		}
	}
	q.next = time.Now().Add(125 * time.Millisecond)
	q.count++
	result, err := q.side.query(q.ctx, q.target, name, kind, protocol, uint16(q.count))
	q.t.Logf("decision sequence=%d source=%s name=%s type=%s protocol=%s answer=%s success=%t", q.count, q.side.address, name, kind, protocol, result.Address, err == nil)
	if refusal := new(decisionQueryRefusal); errors.As(err, &refusal) {
		body, _ := json.Marshal(refusal.Runtime)
		q.t.Logf("decision original refusal sequence=%d stage=%s code=%s originalSHA256=%s runtime=%s", q.count, refusal.Stage, refusal.Code, refusal.OriginalSHA256, body)
	}
	return result, err
}

func decisionMatches(result decisionResult, blocked bool) bool {
	want := "198.51.100.23"
	if result.Type == "AAAA" {
		want = "2001:db8::23"
	}
	if blocked {
		want = "0.0.0.0"
		if result.Type == "AAAA" {
			want = "::"
		}
	}
	return result.Address == want
}

func (q *decisionQueries) matrix(engine Engine, phase string) {
	q.t.Helper()
	cases, err := decisionMatrix(engine, q.side.nonce, phase)
	if err != nil {
		q.t.Fatal(err)
	}
	for _, c := range cases {
		for _, protocol := range []string{"udp", "tcp"} {
			for _, kind := range []string{"A", "AAAA"} {
				result, err := q.read(c.name, kind, protocol)
				if err != nil || !decisionMatches(result, c.blocked) {
					q.t.Fatalf("measured %s matrix %s %s/%s blocked=%t: %v answer=%s", phase, c.name, protocol, kind, c.blocked, err, result.Address)
				}
			}
		}
	}
}

func (q *decisionQueries) settle(first, second decisionCase) {
	q.t.Helper()
	ctx, cancel := context.WithTimeout(q.ctx, 20*time.Second)
	defer cancel()
	previous := q.ctx
	q.ctx = ctx
	defer func() { q.ctx = previous }()
	consecutive := 0
	var firstRefusal, lastRefusal error
	for round := 0; round < decisionRounds; round++ {
		a, e1 := q.read(first.name, "A", "udp")
		b, e2 := q.read(second.name, "AAAA", "tcp")
		if e1 == nil && e2 == nil && decisionMatches(a, first.blocked) && decisionMatches(b, second.blocked) {
			consecutive++
			if consecutive == 2 {
				return
			}
		} else {
			consecutive = 0
			for _, err := range []error{e1, e2} {
				if err != nil {
					if firstRefusal == nil {
						firstRefusal = err
					}
					lastRefusal = err
				}
			}
		}
		if ctx.Err() != nil {
			break
		}
	}
	q.t.Fatalf("native compilation/cache settling did not produce two consecutive measured decisions within eight rounds/20 seconds; originalFirstRefusal=%v originalLastRefusal=%v", firstRefusal, lastRefusal)
}

func TestDNSServiceNativeDomainDecisions(t *testing.T) {
	if os.Getenv("JD_DNS_DOMAIN_DECISIONS_LIVE") != "1" {
		t.Skip("explicit source-reviewed actual domain-decision fixture opt-in required")
	}
	engine := Engine(os.Getenv("JD_DNS_SERVICES_LIVE_ENGINE"))
	if engine != AdGuard && engine != PiHole {
		t.Fatal("decision fixture supports only the pinned AdGuard and Pi-hole engines")
	}
	if decisionMaximumQueries() != decisionQueryLimit {
		t.Fatal("static decision query budget differs")
	}
	helper, err := decisionHelper(os.Getenv("JD_DNS_DECISION_HELPER"), os.Getenv("JD_DNS_DECISION_HELPER_SHA256"))
	if err != nil {
		t.Fatal(err)
	}
	host := os.Getenv("JD_DNS_SERVICES_DOCKER_HOST")
	if !strings.HasPrefix(host, "unix:///") {
		t.Fatal("exact reviewed local Docker Unix endpoint required")
	}
	d := NewDockerRuntime(host).(*dockerRuntime)
	t.Cleanup(func() { d.Close() })
	ctx, cancel := context.WithTimeout(t.Context(), 180*time.Second)
	defer cancel()
	if _, err = d.Image(ctx, engine); err != nil {
		t.Fatal(err)
	}
	s := nativeFixtureService(t, t.TempDir(), &nativeAcceptanceRuntime{d, t})
	request := ProvisionRequest{Name: "Owned measured domain decisions", Engine: engine, Management: true, ManagementPort: freeNativePort(t), DNSPort: freeNativePort(t), MemoryMiB: 384, CPUs: 0.5, Upstreams: []string{"192.0.2.53:53"}, Username: "admin", Password: "explicit-owned-fixture-password"}
	for request.DNSPort == request.ManagementPort {
		request.DNSPort = freeNativePort(t)
	}
	plan, err := s.PreviewProvision(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	nonce := plan.ID[:12]
	side := &decisionSidecar{d: d, nonce: nonce, digest: os.Getenv("JD_DNS_DECISION_HELPER_SHA256"), tag: "jd-dns-decisions:" + nonce}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		defer cancel()
		// Destroy deliberately refuses any extra bridge endpoint. Never weaken
		// it or touch the provision until exact sidecar/image removal succeeds.
		if err := side.cleanup(cleanup); err != nil {
			t.Errorf("decision sidecar cleanup refused: %v", err)
			return
		}
		current, err := s.Provision(cleanup, plan.ID)
		if err == nil && current.State != "removed" {
			if err = d.Destroy(cleanup, current.spec(), current.Resources); err != nil {
				t.Errorf("decision provision cleanup: %v", err)
			}
		}
	})
	plan, err = s.ApplyProvision(ctx, plan.ID)
	if err != nil || plan.State != "verified" {
		t.Fatalf("owned decision provision failed state=%s: %v", plan.State, err)
	}
	side.networkID = plan.Resources.NetworkID
	side.engineID, side.spec = plan.Resources.ContainerID, plan.spec()
	side.resources = plan.Resources
	if err = side.importHelper(ctx, helper); err != nil {
		t.Fatal(err)
	}
	if err = side.start(ctx); err != nil {
		t.Fatal(err)
	}
	info, err := d.containerIdentity(ctx, plan.spec(), plan.Resources)
	if err != nil {
		t.Fatal(err)
	}
	target := ""
	for _, endpoint := range info.NetworkSettings.Networks {
		address, err := netip.ParseAddr(endpoint.IPAddress)
		if err != nil || !address.Is4() || !address.IsPrivate() {
			t.Fatal("exact owned engine IPv4 is unreadable")
		}
		target = address.String()
		side.engineAddress, side.engineMAC = target, endpoint.MacAddress
	}
	connection, connectionRequest, err := s.connection(ctx, plan.ConnectionID)
	if err != nil {
		t.Fatal(err)
	}
	client, err := newNativeClient(connectionRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer client.close()
	if err = client.login(ctx); err != nil {
		t.Fatal(err)
	}
	defer client.logout()
	baseline, _ := decisionMatrix(engine, nonce, "baseline")
	parent, allow, deny, empty := baseline[0].name, baseline[1].name, baseline[3].name, baseline[5].name
	if engine == AdGuard {
		err = client.request(ctx, http.MethodPost, "/control/filtering/set_rules", map[string]any{"rules": []string{"# owned unselected decision fixture comment", "", "||" + parent + "^"}}, nil)
	} else {
		err = client.request(ctx, http.MethodPost, "/api/clients", map[string]any{"client": "198.51.100.77", "groups": []int{0}, "comment": "owned unselected client comment"}, nil)
		if err == nil {
			err = client.request(ctx, http.MethodPost, "/api/domains/deny/regex", map[string]any{"domain": "(^|[.])seed-" + nonce + "[.]invalid$", "enabled": true, "groups": []int{0}, "comment": "owned unselected decision fixture comment"}, nil)
		}
	}
	if err != nil {
		t.Fatal("owned unselected baseline seed", err)
	}
	assertCurrent := func(change Change, expected *Snapshot) {
		t.Helper()
		current, err := s.CurrentChange(ctx, change.ID)
		if err != nil || current.State != "available" || current.Connection.ID != connection.ID || current.Connection.Generation != change.Generation || current.Snapshot == nil || expected == nil || current.Snapshot.PolicyFingerprint != expected.PolicyFingerprint || current.Snapshot.SelectionFingerprint != expected.SelectionFingerprint {
			t.Fatal("selected current decision evidence differs", err)
		}
		retained, err := s.Change(ctx, change.ID)
		if err != nil || retained.State != change.State || !reflect.DeepEqual(retained.Before, change.Before) || !reflect.DeepEqual(retained.After, change.After) {
			t.Fatal("current read claimed or replaced retained decision evidence", err)
		}
	}
	apply := func(request ChangeRequest) Change {
		t.Helper()
		if err := side.bridge(ctx); err != nil {
			t.Fatal(err)
		}
		change, err := s.Preview(ctx, connection.ID, request)
		if err != nil {
			t.Fatal("retained decision preview", err)
		}
		assertCurrent(change, change.Before)
		change, err = s.Apply(ctx, change.ID)
		if err != nil || change.State != "verified" || change.After == nil || request.Filter != nil && !policyPreserved(request, change.Before, change.After) {
			t.Fatalf("reviewed decision effect state=%s error=%s err=%v", change.State, change.Error, err)
		}
		assertCurrent(change, change.After)
		if _, err = s.Apply(ctx, change.ID); !errors.Is(err, ErrConflict) {
			t.Fatal("decision effect replay accepted", err)
		}
		return change
	}
	apply(ChangeRequest{Action: "upstreams", Upstreams: []string{side.address + ":5353"}})
	protection := false
	apply(ChangeRequest{Action: "protection", Protection: &protection})
	verifyDecisionClient(t, ctx, client, engine, side.address, side.mac, false)
	seed := readDecisionInventory(t, ctx, client, engine, parent)
	queries := &decisionQueries{t: t, side: side, ctx: ctx, target: target}
	queries.matrix(engine, "disabled")
	protection = true
	apply(ChangeRequest{Action: "protection", Protection: &protection})
	verifyDecisionClient(t, ctx, client, engine, side.address, side.mac, true)
	queries.settle(decisionCase{allow, true}, decisionCase{deny, false})
	queries.matrix(engine, "baseline")
	newFilter := func(domain, disposition string, groups []int) ChangeRequest {
		filter := &DomainFilterChange{Domain: domain, Disposition: disposition, Match: "suffix"}
		if engine == PiHole {
			filter.Match, filter.Groups = "exact", &groups
		}
		return ChangeRequest{Action: "filter_add", Filter: filter}
	}
	allowRequest, denyRequest := newFilter(allow, "allow", []int{0}), newFilter(deny, "deny", []int{0})
	apply(allowRequest)
	if _, err = s.Preview(ctx, connection.ID, allowRequest); err == nil {
		t.Fatal("duplicate reviewed allow was staged")
	}
	assertDecisionSeed(t, ctx, client, engine, parent, seed)
	queries.settle(decisionCase{allow, false}, decisionCase{allow, false})
	apply(denyRequest)
	if _, err = s.Preview(ctx, connection.ID, denyRequest); err == nil {
		t.Fatal("duplicate reviewed deny was staged")
	}
	assertDecisionSeed(t, ctx, client, engine, parent, seed)
	queries.settle(decisionCase{deny, true}, decisionCase{deny, true})
	requests := []ChangeRequest{allowRequest, denyRequest}
	if engine == PiHole {
		emptyRequest := newFilter(empty, "deny", []int{})
		apply(emptyRequest)
		requests = append(requests, emptyRequest)
	}
	phaseNotBefore := time.Now()
	queries.matrix(engine, "added")
	verifyDecisionHistory(t, ctx, client, engine, side.address, allow, deny, phaseNotBefore)
	configured := readDecisionInventory(t, ctx, client, engine, parent)
	seconds := 3
	if err = d.Verify(ctx, plan.spec(), plan.Resources); err != nil {
		t.Fatal(err)
	}
	restartBefore, err := side.queryGuard(ctx)
	if err != nil || !restartBefore.Engine.Running {
		t.Fatal("owned restart pre-effect runtime guard", err)
	}
	restartBody, _ := json.Marshal(restartBefore)
	t.Logf("decision runtime before owned restart runtime=%s", restartBody)
	if err = d.cli.ContainerRestart(ctx, plan.Resources.ContainerID, container.StopOptions{Timeout: &seconds}); err != nil {
		t.Fatal(decisionRefusal("owned_restart", err, restartBefore))
	}
	restartAfter, err := side.restarted(ctx, restartBefore)
	restartBody, _ = json.Marshal(restartAfter)
	t.Logf("decision runtime after owned restart runtime=%s", restartBody)
	if err != nil {
		t.Fatal(err)
	}
	queries.settle(decisionCase{allow, false}, decisionCase{deny, true})
	// Restart invalidates the FTL session. Renew authentication only; no
	// native policy or pending mutation is repeated after restart.
	client.close()
	client, err = newNativeClient(connectionRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer client.close()
	if err = client.login(ctx); err != nil {
		t.Fatal("fresh native post-restart session", err)
	}
	defer client.logout()
	verifyDecisionClient(t, ctx, client, engine, side.address, side.mac, true)
	if !reflect.DeepEqual(configured, readDecisionInventory(t, ctx, client, engine, parent)) {
		t.Fatal("native rule inventory changed on restart without reapply")
	}
	phaseNotBefore = time.Now()
	queries.matrix(engine, "added")
	verifyDecisionHistory(t, ctx, client, engine, side.address, allow, deny, phaseNotBefore)
	for _, request := range requests {
		request.Action, request.Filter.Groups = "filter_remove", nil
		apply(request)
		assertDecisionSeed(t, ctx, client, engine, parent, seed)
		if request.Filter.Domain == allow {
			queries.settle(decisionCase{allow, true}, decisionCase{allow, true})
		} else if request.Filter.Domain == deny {
			queries.settle(decisionCase{deny, false}, decisionCase{deny, false})
		}
	}
	queries.matrix(engine, "removed")
	if !reflect.DeepEqual(seed, readDecisionInventory(t, ctx, client, engine, parent)) {
		t.Fatal("literal unselected seed/client/group/source policy was not restored after reviewed removals")
	}
	if err = side.cleanup(ctx); err != nil {
		t.Fatal(err)
	}
	removed, err := s.RemoveProvision(ctx, plan.ID)
	if err != nil || removed.State != "removed" {
		t.Fatal("owned measured provision removal", err)
	}
	assertNativeResourcesAbsent(t, d, plan.Resources)
	t.Logf("engine=%s nativeClient=%s mac=%s image=%s helper=%s queries=%d budget=%d rate<=8/s scope=IPv4-client-A-AAAA-UDP-TCP suffixOrExact=measured restart=no_reapply subscriptions=none resources=cleaned", engine, side.address, side.mac, side.imageID, side.digest, queries.count, decisionQueryLimit)
}
