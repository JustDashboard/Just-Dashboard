// Package blueprint holds the reviewed, versioned workload definitions that
// ship with the dashboard.
//
// A blueprint is data, not a script. It is parsed with unknown fields rejected,
// rendered by a pure function, and validated in CI against the same rules a
// reviewer would apply by hand. Nothing here downloads or executes anything;
// rendering produces a normalized plan that the deployment engine then runs
// through exactly the same path as a hand-built container.
package blueprint

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Profile is the workload shape. It decides which normalized defaults and
// which operational surfaces a deployment gets, and nothing else.
type Profile string

const (
	ProfileWeb      Profile = "web"
	ProfileDatabase Profile = "database"
	ProfileTool     Profile = "tool"
	ProfileWorker   Profile = "worker"
	ProfileGame     Profile = "game"
	ProfileCompose  Profile = "compose"
)

// Category groups blueprints in the picker. It is presentation only.
type Category string

const (
	CategoryHTTP       Category = "http"
	CategoryDatabase   Category = "database"
	CategoryTool       Category = "tool"
	CategoryAutomation Category = "automation"
	CategoryGame       Category = "game"
)

// InputKind is the closed set of field types a blueprint may ask for. A kind
// the renderer cannot validate is a kind a blueprint cannot declare.
type InputKind string

const (
	InputText    InputKind = "text"
	InputNumber  InputKind = "number"
	InputBoolean InputKind = "boolean"
	InputChoice  InputKind = "choice"
	InputDomain  InputKind = "domain"
	InputMemory  InputKind = "memory"
	InputAccept  InputKind = "accept"
)

// OperationKind is the closed operation vocabulary. Install, release, startup
// and stop steps may only be one of these; there is no "run this shell string"
// escape, which is the whole point of shipping data rather than scripts.
type OperationKind string

const (
	OperationSetVariable   OperationKind = "set_variable"
	OperationWriteFile     OperationKind = "write_file"
	OperationConsole       OperationKind = "console_command"
	OperationStopSignal    OperationKind = "stop_signal"
	OperationWaitForLog    OperationKind = "wait_for_log"
	OperationFetchArtifact OperationKind = "fetch_artifact"
)

// CheckKind mirrors the deployment engine's own readiness vocabulary. A
// blueprint cannot invent a check the runner does not implement.
type CheckKind string

const (
	CheckHTTP      CheckKind = "http"
	CheckTCP       CheckKind = "tcp"
	CheckDocker    CheckKind = "docker_health"
	CheckCommand   CheckKind = "command"
	CheckHandshake CheckKind = "game_handshake"
)

type Provenance struct {
	Maintainer       string `json:"maintainer"`
	License          string `json:"license"`
	UpstreamURL      string `json:"upstreamUrl"`
	ReviewedAt       string `json:"reviewedAt"`
	MinimumDashboard string `json:"minimumDashboard"`
	UpdateNotes      string `json:"updateNotes,omitempty"`
}

// Image pins the runtime image. Tag is the compatible tag policy; the digest
// is resolved at deployment time and recorded on the release, so a mutable tag
// never silently becomes a different release.
type Image struct {
	Reference  string   `json:"reference"`
	TagPolicy  string   `json:"tagPolicy"`
	Platforms  []string `json:"platforms,omitempty"`
	PullPolicy string   `json:"pullPolicy,omitempty"`
	// Command replaces the image's default arguments; its entrypoint stays.
	// It is an argument vector, never a shell string, and it is not
	// templated: an image that needs an input in its arguments takes it from
	// a variable instead.
	Command []string `json:"command,omitempty"`
}

type Choice struct {
	Value       string `json:"value"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	// Image overrides the blueprint image when this choice is selected. It is
	// how one blueprint offers Vanilla, Paper and Fabric without becoming
	// three blueprints that drift apart.
	Image string `json:"image,omitempty"`
}

type Input struct {
	Name        string    `json:"name"`
	Kind        InputKind `json:"kind"`
	Label       string    `json:"label"`
	Description string    `json:"description,omitempty"`
	Default     string    `json:"default,omitempty"`
	Required    bool      `json:"required,omitempty"`
	Advanced    bool      `json:"advanced,omitempty"`
	Minimum     int       `json:"minimum,omitempty"`
	Maximum     int       `json:"maximum,omitempty"`
	Pattern     string    `json:"pattern,omitempty"`
	Choices     []Choice  `json:"choices,omitempty"`
	// Variable is the environment variable this input becomes. An input with
	// no variable only steers rendering.
	Variable string `json:"variable,omitempty"`
	// AcceptURL is the exact agreement an "accept" input refers to. Accepting
	// without a linked document is not consent.
	AcceptURL string `json:"acceptUrl,omitempty"`
}

// Secret is generated server-side. The blueprint declares its shape and where
// it is referenced; it never declares a value, and CI refuses one that does.
type Secret struct {
	Name        string `json:"name"`
	Variable    string `json:"variable"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Length      int    `json:"length"`
	Alphabet    string `json:"alphabet,omitempty"`
}

type Port struct {
	Name     string `json:"name"`
	Internal int    `json:"internal"`
	Protocol string `json:"protocol"`
	Purpose  string `json:"purpose"`
	// Exposure is "proxy" (behind the dashboard's reverse proxy), "direct"
	// (a published host port, for protocols a proxy cannot carry) or
	// "internal" (never published).
	Exposure string `json:"exposure"`
	Primary  bool   `json:"primary,omitempty"`
}

type Volume struct {
	Name     string `json:"name"`
	Target   string `json:"target"`
	Purpose  string `json:"purpose"`
	Data     bool   `json:"data"`
	Backup   bool   `json:"backup"`
	ReadOnly bool   `json:"readOnly,omitempty"`
}

type Resources struct {
	MemoryMB    int     `json:"memoryMb"`
	MinMemoryMB int     `json:"minMemoryMb"`
	CPUs        float64 `json:"cpus,omitempty"`
}

type Check struct {
	Name           string    `json:"name"`
	Kind           CheckKind `json:"kind"`
	Phase          string    `json:"phase"`
	Required       bool      `json:"required"`
	Path           string    `json:"path,omitempty"`
	Port           string    `json:"port,omitempty"`
	Command        []string  `json:"command,omitempty"`
	TimeoutSeconds int       `json:"timeoutSeconds,omitempty"`
}

type Operation struct {
	Kind           OperationKind `json:"kind"`
	Name           string        `json:"name,omitempty"`
	Value          string        `json:"value,omitempty"`
	Path           string        `json:"path,omitempty"`
	Content        string        `json:"content,omitempty"`
	Signal         string        `json:"signal,omitempty"`
	Pattern        string        `json:"pattern,omitempty"`
	URL            string        `json:"url,omitempty"`
	Checksum       string        `json:"checksum,omitempty"`
	MaxBytes       int64         `json:"maxBytes,omitempty"`
	TimeoutSeconds int           `json:"timeoutSeconds,omitempty"`
}

type Operations struct {
	Install []Operation `json:"install,omitempty"`
	Release []Operation `json:"release,omitempty"`
	Startup []Operation `json:"startup,omitempty"`
	Stop    []Operation `json:"stop,omitempty"`
}

// ConfigFile is a file the operator may safely edit after deployment. Format
// gives the settings editor a parser; "raw" means text with no structure and
// no structured editor.
type ConfigFile struct {
	Path            string     `json:"path"`
	Label           string     `json:"label"`
	Format          string     `json:"format"`
	Description     string     `json:"description,omitempty"`
	RestartRequired bool       `json:"restartRequired,omitempty"`
	Properties      []Property `json:"properties,omitempty"`
}

// Property is one known key inside a ConfigFile. Only declared keys get a
// structured control; everything else stays in the raw preview.
type Property struct {
	Key         string    `json:"key"`
	Kind        InputKind `json:"kind"`
	Label       string    `json:"label"`
	Description string    `json:"description,omitempty"`
	Default     string    `json:"default,omitempty"`
	Minimum     int       `json:"minimum,omitempty"`
	Maximum     int       `json:"maximum,omitempty"`
	Choices     []Choice  `json:"choices,omitempty"`
}

// Automation is a schedule preset offered at creation. It is a suggestion the
// operator accepts or declines; nothing is scheduled without that choice.
type Automation struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Cron        string   `json:"cron"`
	Timezone    string   `json:"timezone,omitempty"`
	Actions     []string `json:"actions"`
	Default     bool     `json:"default,omitempty"`
}

// Update describes how this blueprint learns that a newer version exists.
type Update struct {
	Detector      string `json:"detector"`
	VersionSource string `json:"versionSource,omitempty"`
	Notes         string `json:"notes,omitempty"`
	BackupFirst   bool   `json:"backupFirst,omitempty"`
}

// Security is the explicit declaration a blueprint must make before it may ask
// for anything privileged. Absence means denied.
type Security struct {
	Privileged   bool     `json:"privileged,omitempty"`
	HostNetwork  bool     `json:"hostNetwork,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
	Devices      []string `json:"devices,omitempty"`
	Reason       string   `json:"reason,omitempty"`
}

// Fixture is the expected render for a named set of inputs. Every blueprint
// ships at least one, and CI renders it and compares the digest.
type Fixture struct {
	Name   string            `json:"name"`
	Inputs map[string]string `json:"inputs"`
	Digest string            `json:"digest"`
}

type Blueprint struct {
	ID          string       `json:"id"`
	Version     string       `json:"version"`
	Name        string       `json:"name"`
	Category    Category     `json:"category"`
	Profile     Profile      `json:"profile"`
	Description string       `json:"description"`
	IconID      string       `json:"iconId"`
	DocsURL     string       `json:"docsUrl"`
	Provenance  Provenance   `json:"provenance"`
	Image       Image        `json:"image"`
	Inputs      []Input      `json:"inputs,omitempty"`
	Secrets     []Secret     `json:"secrets,omitempty"`
	Ports       []Port       `json:"ports,omitempty"`
	Volumes     []Volume     `json:"volumes,omitempty"`
	Resources   Resources    `json:"resources"`
	Checks      []Check      `json:"checks,omitempty"`
	Operations  Operations   `json:"operations"`
	Files       []ConfigFile `json:"files,omitempty"`
	Automation  []Automation `json:"automation,omitempty"`
	Update      Update       `json:"update"`
	Security    Security     `json:"security"`
	Fixtures    []Fixture    `json:"fixtures,omitempty"`
}

var ErrUnknownField = errors.New("blueprint contains an unsupported field")

// Parse decodes one blueprint with unknown fields rejected. A blueprint that
// uses a field this dashboard version does not understand is refused rather
// than silently rendered without it.
func Parse(raw []byte) (*Blueprint, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var parsed Blueprint
	if err := decoder.Decode(&parsed); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnknownField, err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return nil, fmt.Errorf("%w: trailing data after the blueprint object", ErrUnknownField)
	}
	if err := Validate(&parsed); err != nil {
		return nil, err
	}
	return &parsed, nil
}
