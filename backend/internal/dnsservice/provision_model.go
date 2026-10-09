package dnsservice

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/netip"
	"strings"
	"time"
	"unicode"
)

var pinnedImages = map[Engine]string{
	AdGuard:    "adguard/adguardhome@sha256:92929135ced2554aaf94706f766a98ad348f211df61b0704e2db7e8498cc00b7",
	PiHole:     "pihole/pihole@sha256:5b9c8cf51de7d6d3f2240dbe72baf5f06e1fd39cb4d77a99fab4fa13e23bd1be",
	Technitium: "technitium/dns-server@sha256:1045db38fd2f2e4d9578f9168b7eb4862575469ed0ffc79e03f36bdc772a93b5",
}

type ProvisionRequest struct {
	Name           string   `json:"name"`
	Engine         Engine   `json:"engine"`
	ManagementPort int      `json:"managementPort"`
	DNSPort        int      `json:"dnsPort"`
	MemoryMiB      int64    `json:"memoryMiB"`
	CPUs           float64  `json:"cpus"`
	Upstreams      []string `json:"upstreams"`
	Management     bool     `json:"management"`
	Username       string   `json:"username"`
	Password       string   `json:"password,omitempty"`
}

type ProvisionResources struct {
	NetworkName   string   `json:"networkName"`
	NetworkID     string   `json:"networkId,omitempty"`
	Volumes       []string `json:"volumes"`
	ContainerName string   `json:"containerName"`
	ContainerID   string   `json:"containerId,omitempty"`
	Phase         string   `json:"phase"`
}

type Provision struct {
	ID           string             `json:"id"`
	Request      ProvisionRequest   `json:"request"`
	Image        string             `json:"image"`
	ImageID      string             `json:"imageId"`
	Owner        string             `json:"owner"`
	Resources    ProvisionResources `json:"resources"`
	ConnectionID string             `json:"connectionId,omitempty"`
	State        string             `json:"state"`
	CreatedAt    time.Time          `json:"createdAt"`
	ExpiresAt    time.Time          `json:"expiresAt"`
	EndedAt      *time.Time         `json:"endedAt,omitempty"`
	Error        string             `json:"error,omitempty"`
	Limitations  []string           `json:"limitations"`
}

type provisionSpec struct {
	ID, Owner, Image, ImageID string
	Request                   ProvisionRequest
}

type ProvisionRuntime interface {
	Image(context.Context, Engine) (string, error)
	Prepare(context.Context, provisionSpec, ProvisionResources, func(ProvisionResources) error) (ProvisionResources, error)
	Start(context.Context, provisionSpec, ProvisionResources) error
	RemoveBootstrapSecret(context.Context, provisionSpec, ProvisionResources) error
	Verify(context.Context, provisionSpec, ProvisionResources) error
	Activate(context.Context, provisionSpec, ProvisionResources) error
	Destroy(context.Context, provisionSpec, ProvisionResources) error
	Close() error
}

func validateProvision(req ProvisionRequest) (ProvisionRequest, error) {
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len(req.Name) > 80 || strings.IndexFunc(req.Name, unicode.IsControl) >= 0 {
		return req, errors.New("give the DNS provision a bounded name without controls")
	}
	if pinnedImages[req.Engine] == "" {
		return req, errors.New("select an engine with a reviewed immutable image")
	}
	if req.ManagementPort < 1024 || req.ManagementPort > 65535 || req.DNSPort < 1024 || req.DNSPort > 65535 || req.ManagementPort == req.DNSPort {
		return req, errors.New("select different explicit loopback management and DNS ports from 1024 to 65535")
	}
	if req.MemoryMiB < 128 || req.MemoryMiB > 1024 || math.IsNaN(req.CPUs) || math.IsInf(req.CPUs, 0) || req.CPUs < 0.25 || req.CPUs > 2 {
		return req, errors.New("native DNS limits require 128–1024 MiB and 0.25–2 CPUs")
	}
	if len(req.Password) < 16 || len(req.Password) > 4096 || strings.IndexFunc(req.Password, unicode.IsControl) >= 0 {
		return req, errors.New("provide an explicit native password of 16–4096 characters without controls")
	}
	if req.Engine == AdGuard && len(req.Password) > 72 {
		return req, errors.New("AdGuard's native bcrypt password must fit 72 bytes")
	}
	if req.Engine == AdGuard {
		if req.Username == "" || len(req.Username) > 80 || strings.IndexFunc(req.Username, unicode.IsControl) >= 0 {
			return req, errors.New("provide the native AdGuard administrator username")
		}
	} else if req.Username != "admin" {
		return req, errors.New("the native bootstrap administrator username is admin")
	}
	if err := validateChange(ChangeRequest{Action: "upstreams", Upstreams: req.Upstreams}, req.Engine); err != nil {
		return req, err
	}
	for _, upstream := range req.Upstreams {
		ap, _ := netip.ParseAddrPort(upstream)
		if ap.Addr().IsLoopback() {
			return req, errors.New("an owned container's loopback is not the host; declare a reachable non-loopback upstream")
		}
	}
	return req, nil
}

func provisionIntent(id string) ProvisionResources {
	stem := "jd-dns-" + id
	return ProvisionResources{NetworkName: stem, Volumes: []string{stem + "-config", stem + "-data"}, ContainerName: stem, Phase: "planned"}
}

func (p Provision) spec() provisionSpec {
	return provisionSpec{p.ID, p.Owner, p.Image, p.ImageID, p.Request}
}
func (p Provision) endpoint() string {
	return fmt.Sprintf("http://127.0.0.1:%d", p.Request.ManagementPort)
}
