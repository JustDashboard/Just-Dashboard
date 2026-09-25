package deploy

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/distribution/reference"
)

type DraftStep string

const (
	DraftIntent        DraftStep = "intent"
	DraftSource        DraftStep = "source"
	DraftDetection     DraftStep = "detection"
	DraftConfiguration DraftStep = "configuration"
	DraftPreflight     DraftStep = "preflight"
)

var draftSteps = map[DraftStep]struct{}{
	DraftIntent: {}, DraftSource: {}, DraftDetection: {}, DraftConfiguration: {}, DraftPreflight: {},
}

type SourceMode string

const (
	SourceModeGitURL              SourceMode = "git_url"
	SourceModeConnectedRepository SourceMode = "connected_repository"
	SourceModeLocalCheckout       SourceMode = "local_checkout"
	SourceModeImageReference      SourceMode = "image_reference"
	SourceModeComposePaste        SourceMode = "compose_paste"
	SourceModeComposeUpload       SourceMode = "compose_upload"
	SourceModeComposeGit          SourceMode = "compose_git"
	SourceModeComposeLocal        SourceMode = "compose_local"
	SourceModeBlueprint           SourceMode = "blueprint"
	SourceModeExistingCheckout    SourceMode = "existing_checkout"
	SourceModeExistingContainer   SourceMode = "existing_container"
	SourceModeExistingStack       SourceMode = "existing_stack"
)

type Draft struct {
	ID                 string             `json:"id"`
	OwnerUserID        int64              `json:"ownerUserId"`
	OwnerUsername      string             `json:"ownerUsername"`
	CurrentStep        DraftStep          `json:"currentStep"`
	Revision           int                `json:"revision"`
	Data               DraftData          `json:"data"`
	Findings           []PreflightFinding `json:"findings"`
	PlanPreview        string             `json:"planPreview"`
	CommittedProjectID int64              `json:"committedProjectId,omitempty"`
	CreatedAt          time.Time          `json:"createdAt"`
	UpdatedAt          time.Time          `json:"updatedAt"`
	ExpiresAt          time.Time          `json:"expiresAt"`
	EnvironmentKeys    []string           `json:"environmentKeys,omitempty"`
	// Values live only in the separately sealed draft column, never data_json,
	// plan previews, or the draft returned to a browser.
	environment    map[string]string
	environmentEnc string
}

type DraftData struct {
	Intent        *DraftIntentConfig `json:"intent,omitempty"`
	Source        *DraftSourceConfig `json:"source,omitempty"`
	Detection     *DetectionResult   `json:"detection,omitempty"`
	Configuration *PlanConfiguration `json:"configuration,omitempty"`
}

// DraftSummary is what the new-project page shows to offer resuming an
// uncommitted draft: enough to recognise it, not the whole wizard state.
type DraftSummary struct {
	ID          string    `json:"id"`
	Name        string    `json:"name,omitempty"`
	Source      string    `json:"source,omitempty"`
	CurrentStep DraftStep `json:"currentStep"`
	UpdatedAt   time.Time `json:"updatedAt"`
	ExpiresAt   time.Time `json:"expiresAt"`
}

// draftSourceSummary is a one-line description of a draft's source step, for
// a resume list that has no room for the full DraftSourceConfig.
func draftSourceSummary(source *DraftSourceConfig) string {
	if source == nil {
		return ""
	}
	switch {
	case source.Repository != "":
		return string(source.Kind) + " · " + source.Repository
	case source.URL != "":
		return string(source.Kind) + " · " + source.URL
	case source.Image != "":
		return string(source.Kind) + " · " + source.Image
	case source.LocalPath != "":
		return string(source.Kind) + " · " + source.LocalPath
	default:
		return string(source.Kind)
	}
}

type DraftIntentConfig struct {
	Name    string          `json:"name"`
	Profile WorkloadProfile `json:"profile"`
}

type ComposeDocument struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Order   int    `json:"order"`
}

type DraftSourceConfig struct {
	Kind              SourceKind        `json:"kind"`
	Mode              SourceMode        `json:"mode"`
	URL               string            `json:"url,omitempty"`
	Provider          string            `json:"provider,omitempty"`
	ProviderBaseURL   string            `json:"providerBaseUrl,omitempty"`
	Repository        string            `json:"repository,omitempty"`
	Ref               string            `json:"ref,omitempty"`
	CredentialID      int64             `json:"credentialId,omitempty"`
	LocalPath         string            `json:"localPath,omitempty"`
	Subdirectory      string            `json:"subdirectory,omitempty"`
	ManagedInPlace    bool              `json:"managedInPlace,omitempty"`
	IncludeSubmodules bool              `json:"includeSubmodules,omitempty"`
	IncludeLFS        bool              `json:"includeLfs,omitempty"`
	Image             string            `json:"image,omitempty"`
	Platform          string            `json:"platform,omitempty"`
	ComposeFiles      []ComposeDocument `json:"composeFiles,omitempty"`
	ResourceID        string            `json:"resourceId,omitempty"`
	BlueprintID       string            `json:"blueprintId,omitempty"`
	BlueprintVersion  string            `json:"blueprintVersion,omitempty"`
	// BlueprintInputs are the operator's answers to the blueprint's declared
	// fields. They are the only thing that varies between two deployments of
	// the same reviewed blueprint version.
	BlueprintInputs map[string]string `json:"blueprintInputs,omitempty"`
}

type SourceIdentity struct {
	Kind              SourceKind      `json:"kind"`
	Remote            string          `json:"remote,omitempty"`
	Repository        string          `json:"repository,omitempty"`
	Ref               string          `json:"ref,omitempty"`
	Revision          string          `json:"revision,omitempty"`
	Digest            string          `json:"digest,omitempty"`
	OS                string          `json:"os,omitempty"`
	Architecture      string          `json:"architecture,omitempty"`
	Platforms         []string        `json:"platforms,omitempty"`
	LocalPath         string          `json:"localPath,omitempty"`
	Dirty             bool            `json:"dirty,omitempty"`
	IncludeSubmodules bool            `json:"includeSubmodules,omitempty"`
	IncludeLFS        bool            `json:"includeLfs,omitempty"`
	ComposeFiles      []string        `json:"composeFiles,omitempty"`
	Services          []string        `json:"services,omitempty"`
	CredentialID      int64           `json:"credentialId,omitempty"`
	Observed          json.RawMessage `json:"observed,omitempty"`
}

type DetectionConfidence string

const (
	ConfidenceHigh   DetectionConfidence = "high"
	ConfidenceMedium DetectionConfidence = "medium"
	ConfidenceLow    DetectionConfidence = "low"
)

type DetectionEvidence struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

type DetectedCandidate struct {
	ID               string              `json:"id"`
	Name             string              `json:"name"`
	Root             string              `json:"root"`
	Profile          WorkloadProfile     `json:"profile"`
	BuildMethod      BuildMethod         `json:"buildMethod"`
	Confidence       DetectionConfidence `json:"confidence"`
	Framework        string              `json:"framework,omitempty"`
	Recipe           string              `json:"recipe,omitempty"`
	BuildCommand     string              `json:"buildCommand,omitempty"`
	StartCommand     string              `json:"startCommand,omitempty"`
	OutputDirectory  string              `json:"outputDirectory,omitempty"`
	Dockerfile       string              `json:"dockerfile,omitempty"`
	GoVersion        string              `json:"goVersion,omitempty"`
	GoMinimumVersion string              `json:"goMinimumVersion,omitempty"`
	PackageManager   string              `json:"packageManager,omitempty"`
	PackageManagers  []string            `json:"packageManagers,omitempty"`
	RecipeIssue      string              `json:"recipeIssue,omitempty"`
	Port             int                 `json:"port,omitempty"`
	// SchemaTool names the migration tool the source declares. SchemaCommand
	// is how the detected start command applies its schema; it is empty when
	// the tool needs a decision, and SchemaInStart says the package's own
	// start script already runs it.
	SchemaTool    string `json:"schemaTool,omitempty"`
	SchemaCommand string `json:"schemaCommand,omitempty"`
	SchemaInStart bool   `json:"schemaInStart,omitempty"`
	// SPAFallback says the site's client owns its routes, so nginx answers
	// any path it has no file for with index.html.
	SPAFallback   bool   `json:"spaFallback,omitempty"`
	PythonVersion string `json:"pythonVersion,omitempty"`
	// UnpinnedDependencies records that the manifest names dependencies
	// without exact versions, so a rebuild may install different ones.
	UnpinnedDependencies bool                `json:"unpinnedDependencies,omitempty"`
	Variables            []DetectedVariable  `json:"variables,omitempty"`
	Databases            []DetectedDatabase  `json:"databases,omitempty"`
	Evidence             []DetectionEvidence `json:"evidence"`
	NeedsDecision        []string            `json:"needsDecision"`

	// Readiness is how the candidate proves it is serving, read from what the
	// source declares (detect_readiness.go); nil keeps the plan's GET / check.
	Readiness *DetectedReadiness `json:"readiness,omitempty"`
	// BackgroundWorker names the library that makes the candidate a process
	// that never listens, such as a chat bot or a queue consumer.
	BackgroundWorker *DetectedBackgroundWorker `json:"backgroundWorker,omitempty"`
	// StartDetaches is a start command that puts the application in the
	// background, which detection could not rewrite into a foreground one.
	StartDetaches *DetectedStartDetach `json:"startDetaches,omitempty"`
	// Listen is what the source says about where its server listens — a
	// port it fixes, whether it reads PORT, a loopback bind — for preflight
	// to re-check against the plan without the source tree.
	Listen *DetectedListen `json:"listen,omitempty"`
	// NetworkVariables are plain runtime variables the deployment's place
	// behind the managed proxy decides (AUTH_TRUST_HOST, NEXTAUTH_URL, HOST).
	NetworkVariables []DetectedNetworkVariable `json:"networkVariables,omitempty"`
	// PersistentPaths is state the application writes to its own filesystem
	// (detect_state.go); SeedCommand loads its seed data, SeedResets says the
	// seed clears tables first, and SchemaPush says the schema step pushes the
	// declared model instead of applying committed migrations.
	PersistentPaths []DetectedPersistentPath `json:"persistentPaths,omitempty"`
	SeedCommand     string                   `json:"seedCommand,omitempty"`
	SeedResets      bool                     `json:"seedResets,omitempty"`
	SchemaPush      bool                     `json:"schemaPush,omitempty"`
	// BrowserPrefixes are the variable prefixes this root's framework
	// compiles into the JavaScript every visitor downloads (NEXT_PUBLIC_,
	// VITE_, …), so a value under one is build input and public.
	BrowserPrefixes []string `json:"browserPrefixes,omitempty"`
	// EnvironmentNotes are facts about the source's configuration that
	// preflight turns into findings; see EnvironmentNote.
	EnvironmentNotes []EnvironmentNote `json:"environmentNotes,omitempty"`
	// The repository's own Dockerfile, read as data (detector_dockerfile.go):
	// what its name, place or command says it was written for, the stage to
	// build, its build arguments by name, and the platforms it pins.
	DockerfileRole      string          `json:"dockerfileRole,omitempty"`
	DockerfileTarget    string          `json:"dockerfileTarget,omitempty"`
	DockerfileArgs      []DockerfileArg `json:"dockerfileArgs,omitempty"`
	DockerfilePlatforms []string        `json:"dockerfilePlatforms,omitempty"`
	// DockerfileStages are the named stages a configured target must be one of.
	DockerfileStages []string `json:"dockerfileStages,omitempty"`
	// ImageBuildIssues are what detection proved about how this candidate's
	// image would build — a refused line, a missing COPY source, a script
	// without its executable bit — so preflight says so before Deploy.
	ImageBuildIssues []ImageBuildIssue `json:"imageBuildIssues,omitempty"`
	// ReleaseCommand is the one-off command the repository declares must run
	// before each release starts (Procfile release:, fly.toml, render.yaml).
	ReleaseCommand string `json:"releaseCommand,omitempty"`
	// Repository shape (detect_repo_shape.go). Demotion says why a candidate
	// is offered but never chosen over the application (an example, a docs
	// site, the frontend of an API); NotDeployable names a shape nothing can
	// serve (a library, an extension, a mobile app). Companions are the other
	// roots of a split frontend and API; Processes are what the source runs
	// besides this candidate's own process.
	Demotion             string                     `json:"demotion,omitempty"`
	NotDeployable        string                     `json:"notDeployable,omitempty"`
	DesktopShell         string                     `json:"desktopShell,omitempty"`
	Companions           []string                   `json:"companions,omitempty"`
	Processes            []DetectedProcess          `json:"processes,omitempty"`
	PlatformManifests    []DetectedPlatformManifest `json:"platformManifests,omitempty"`
	ServerlessCode       []DetectedServerlessCode   `json:"serverlessCode,omitempty"`
	ImportCaseMismatches []ImportCaseMismatch       `json:"importCaseMismatches,omitempty"`
	// StaticSite is what a static site's own files say about its build and
	// serving: the generator release, the sub-path, the hosting rules
	// (detect_static_site.go).
	StaticSite *DetectedStaticSite `json:"staticSite,omitempty"`
	// Lockfiles are the JavaScript lockfiles committed for this package (or
	// its workspace), each compared with package.json. NodeInstalls is what
	// the recipe installs under each package manager an operator can choose,
	// computed by the same planner the build runs, so preflight can judge a
	// choice without reading the source again. NodeVersion is the Node major
	// the recipe builds on and where it came from.
	Lockfiles    []DetectedLockfile    `json:"lockfiles,omitempty"`
	NodeInstalls []DetectedNodeInstall `json:"nodeInstalls,omitempty"`
	NodeVersion  string                `json:"nodeVersion,omitempty"`
	// NodeBuild is what the build reads that preflight judges against the
	// configuration and the host.
	NodeBuild *DetectedNodeBuild `json:"nodeBuild,omitempty"`

	// GoToolchain and GoVersionFile are the go.mod toolchain line and the
	// .go-version pin, kept as facts rather than folded into RecipeIssue: which
	// toolchain builds the module is an operator setting, so preflight decides
	// it against the plan's Go version instead of against detection's default.
	GoToolchain   string `json:"goToolchain,omitempty"`
	GoVersionFile string `json:"goVersionFile,omitempty"`
	// GoMainPackages are the module's buildable main packages, relative to
	// the candidate root ("." for the root itself); GoPackage is the one the
	// ranking chose, empty when the ranking tied or there is none.
	// GoMainPackagesOmitted counts the mains past the list's bound, so a
	// package missing from a list that is not whole is not called absent.
	GoMainPackages        []string `json:"goMainPackages,omitempty"`
	GoMainPackagesOmitted int      `json:"goMainPackagesOmitted,omitempty"`
	GoPackage             string   `json:"goPackage,omitempty"`
	// GoLibrary says the module has no buildable main package at all, which
	// no Go setting can fix: the recipe builds a command, not a library.
	GoLibrary bool `json:"goLibrary,omitempty"`
	// PythonRequires is the interpreter range the source declares
	// (requires-python, or Poetry's python constraint), and PythonInstall the
	// manifest the recipe installs from; together they say whether a chosen
	// family can run the project and how loudly the install would refuse it.
	PythonRequires string `json:"pythonRequires,omitempty"`
	PythonInstall  string `json:"pythonInstall,omitempty"`

	// readingConfidence is what the source's own evidence supports when an
	// unsettled package manager caps Confidence (packageCandidate). Ranking
	// compares roots by it: which manager installs is a question of building
	// the application, not of what the repository is for. Detection only.
	readingConfidence DetectionConfidence
}

// EnvironmentNote is a fact detection read from the source's configuration
// that preflight answers before the first build: a committed Django secret
// key, a .env file the process refuses to start without, an identity
// provider whose callback allowlist must name the planned domain. Detail and
// Path are evidence; a note never carries a variable's value.
type EnvironmentNote struct {
	Code   string `json:"code"`
	Detail string `json:"detail,omitempty"`
	Path   string `json:"path,omitempty"`
}

// DetectedNodeBuild is what a JavaScript build reads beyond its install:
// the env-validation schema it imports (EnvSchema, read as text) with the
// server and client variables it requires and whether it honours
// SKIP_ENV_VALIDATION, the memory the build is estimated to peak at, and
// the names prisma.config reads that the recipe gives a placeholder while
// the build runs `prisma generate` (PrismaEnv, empty when the build
// command connects to the database).
type DetectedNodeBuild struct {
	EnvSchema    string   `json:"envSchema,omitempty"`
	EnvServer    []string `json:"envServer,omitempty"`
	EnvClient    []string `json:"envClient,omitempty"`
	EnvSkippable bool     `json:"envSkippable,omitempty"`
	MemoryMiB    int      `json:"memoryMiB,omitempty"`
	PrismaEnv    []string `json:"prismaEnv,omitempty"`
	// Findings are what the framework's configuration made the recipe do
	// that the repository could say itself (adapter-node for adapter-auto,
	// a Nitro preset replaced); DevScripts names each package script that
	// starts a development server or a watcher, with what it starts, so a
	// start command written later is judged without the source.
	Findings   []PreflightFinding `json:"findings,omitempty"`
	DevScripts map[string]string  `json:"devScripts,omitempty"`
}

// DetectedVariable is an environment variable the source reads, found in an
// example env file or in the code itself. Example is the template's own
// value when it has one and it is not credential-shaped; a committed real
// .env contributes names only.
type DetectedVariable struct {
	Name    string   `json:"name"`
	Example string   `json:"example,omitempty"`
	Sources []string `json:"sources"`
	// Setup is how the dashboard supplies the value when the operator types
	// none: "generate" mints a self-issued secret at commit in
	// GenerateFormat, "domain" binds it to the planned domain through
	// DomainTemplate, "default" applies DefaultValue, a harmless documented
	// setting, and "paste" marks a secret only the operator holds (Rails'
	// master key) that must never be generated. SetupReason is the evidence.
	Setup          string `json:"setup,omitempty"`
	SetupReason    string `json:"setupReason,omitempty"`
	GenerateLength int    `json:"generateLength,omitempty"`
	GenerateFormat string `json:"generateFormat,omitempty"`
	DomainTemplate string `json:"domainTemplate,omitempty"`
	DefaultValue   string `json:"defaultValue,omitempty"`
	// Phase "build" says the value is read while the build runs — a
	// framework config file, a static env import, a browser prefix — so it
	// needs build scope; empty means it is read at runtime.
	Phase string `json:"phase,omitempty"`
	// BrowserInlined says the framework compiles the value into client
	// JavaScript, which makes it public whatever its sensitivity.
	BrowserInlined bool `json:"browserInlined,omitempty"`
	// Required is a read with no default at a position that runs as the
	// application starts or builds; RequiredRead is the same form somewhere
	// that may only run on one path, which is a warning rather than a gate.
	Required     bool `json:"required,omitempty"`
	RequiredRead bool `json:"requiredRead,omitempty"`
	// LocalhostIn names a committed file whose value for this variable
	// points at loopback, which inside the container is the app itself.
	LocalhostIn string `json:"localhostIn,omitempty"`
	// Step "install" marks a registry credential a package manager's
	// configuration reads, which only the dependency install needs;
	// InstallRequired says the install fails without it.
	Step            string `json:"step,omitempty"`
	InstallRequired bool   `json:"installRequired,omitempty"`
}

// DetectedLockfile is one committed JavaScript lockfile compared, as data,
// with the package.json of every workspace it records. State is in_sync,
// stale (the manager's frozen install would refuse it) or unknown; the name
// lists are capped and Note is the sentence an operator reads.
type DetectedLockfile struct {
	Path    string   `json:"path"`
	Manager string   `json:"manager"`
	State   string   `json:"state"`
	Missing []string `json:"missing,omitempty"`
	Extra   []string `json:"extra,omitempty"`
	Changed []string `json:"changed,omitempty"`
	Note    string   `json:"note,omitempty"`
}

// DetectedNodeInstall is the dependency install the recipe runs when Manager
// is chosen: the lockfile it installs from, the exact command, the manager
// release, the build and start commands detection proposes for that runner,
// and the preflight findings that choice carries.
type DetectedNodeInstall struct {
	Manager      string             `json:"manager"`
	Lockfile     string             `json:"lockfile,omitempty"`
	Install      string             `json:"install,omitempty"`
	Toolchain    string             `json:"toolchain,omitempty"`
	BuildCommand string             `json:"buildCommand,omitempty"`
	StartCommand string             `json:"startCommand,omitempty"`
	Findings     []PreflightFinding `json:"findings,omitempty"`
}

// DetectedDatabase is a database engine the source's dependencies or example
// variables say it connects to, with the variable its connection URL is
// conventionally read from.
type DetectedDatabase struct {
	Engine   string `json:"engine"`
	Variable string `json:"variable"`
	Evidence string `json:"evidence"`
	// Format is the connection string the consumer parses when it is not a
	// URL: "jdbc" (Spring, Quarkus), "jdbc-mariadb" (the same over MariaDB
	// Connector/J, which refuses jdbc:mysql://), "adonet" (.NET) or "mysql2"
	// (Rails before 7.2, which has no mysql:// adapter alias).
	Format string `json:"format,omitempty"`
	// Extensions are the Postgres extensions the schema needs (vector,
	// postgis); the official image ships neither.
	Extensions []string `json:"extensions,omitempty"`
	// Hosted names a driver that only speaks a provider's own protocol
	// (neon-http, vercel-postgres, planetscale-http, prisma-accelerate,
	// upstash-rest), which no database created here can answer.
	Hosted string `json:"hosted,omitempty"`
	// AlsoVariables are further databases on the same server the framework
	// reads by name — Rails 8's CACHE_, QUEUE_ and CABLE_DATABASE_URL.
	AlsoVariables []string `json:"alsoVariables,omitempty"`
}

type DetectionResult struct {
	Source          SourceIdentity      `json:"source"`
	Candidates      []DetectedCandidate `json:"candidates"`
	Compose         *ComposeAnalysis    `json:"compose,omitempty"`
	SelectedID      string              `json:"selectedId,omitempty"`
	ScannedFiles    int                 `json:"scannedFiles"`
	ScannedBytes    int64               `json:"scannedBytes"`
	Truncated       bool                `json:"truncated"`
	TruncatedReason string              `json:"truncatedReason,omitempty"`
	Unavailable     string              `json:"unavailable,omitempty"`
	GitRequirements GitRequirements     `json:"gitRequirements"`
	// SetAside lists what detection recognised and deliberately did not
	// offer; Alternatives a template or published image of the same
	// application (detect_repo_shape.go).
	SetAside     []DetectionSetAside    `json:"setAside,omitempty"`
	Alternatives []DetectionAlternative `json:"alternatives,omitempty"`

	// SelectionReason says why SelectedID won, or why nothing did.
	SelectionReason string `json:"selectionReason,omitempty"`
}

type GitRequirements struct {
	Submodules bool `json:"submodules"`
	LFS        bool `json:"lfs"`
	// SubmoduleList and the LFS file list say which build roots need them;
	// the Checked flags distinguish "none under the root" from evidence
	// recorded before detection read them (detect_git_extras.go).
	SubmoduleList     []GitSubmodule `json:"submoduleList,omitempty"`
	SubmodulesChecked bool           `json:"submodulesChecked,omitempty"`
	LFSChecked        bool           `json:"lfsChecked,omitempty"`
	LFSFiles          int            `json:"lfsFiles,omitempty"`
	LFSPaths          []string       `json:"lfsPaths,omitempty"`
}

type BuildPlanConfig struct {
	Method          BuildMethod `json:"method"`
	Recipe          string      `json:"recipe,omitempty"`
	GoVersion       string      `json:"goVersion,omitempty"`
	PythonVersion   string      `json:"pythonVersion,omitempty"`
	NodeVersion     string      `json:"nodeVersion,omitempty"`
	PackageManager  string      `json:"packageManager,omitempty"`
	RootDirectory   string      `json:"rootDirectory,omitempty"`
	Dockerfile      string      `json:"dockerfile,omitempty"`
	BuildCommand    string      `json:"buildCommand,omitempty"`
	StartCommand    string      `json:"startCommand,omitempty"`
	OutputDirectory string      `json:"outputDirectory,omitempty"`
	// SPAFallback makes the static server answer unknown paths with
	// index.html, for a site whose client owns its routes.
	SPAFallback    bool                `json:"spaFallback,omitempty"`
	TargetPlatform string              `json:"targetPlatform,omitempty"`
	NoCache        bool                `json:"noCache,omitempty"`
	Secrets        []BuildSecretConfig `json:"secrets"`
	ReleaseTasks   []ReleaseTaskConfig `json:"releaseTasks"`
	// Framework is what detection recognised the chosen candidate as, recorded
	// when the project is created so later reads can name it without detecting
	// again. The server owns it: a client's value is never kept, it is carried
	// forward only while the build still describes that candidate, and it is
	// left out of the plan's digest because it is a name, not a build input.
	Framework string `json:"framework,omitempty"`
	// Target is the Dockerfile stage to build, for a file whose last stage
	// is a development one.
	Target string `json:"target,omitempty"`
	// PrimaryService is the Compose service the operator chose for readiness
	// and the release's container identity; empty keeps the analysis's own
	// choice (composePrimaryService).
	PrimaryService string `json:"primaryService,omitempty"`
	// GoPackage is the main package a Go recipe builds, relative to the root
	// directory; empty lets the recipe choose when the module has only one.
	GoPackage string `json:"goPackage,omitempty"`
}

// BuildSecretConfig names a variable and the reviewed recipe stages in which
// BuildKit may expose it: "install", "build", or "install_and_build" for a
// value both the dependency install and the build command read — a root
// package's own postinstall runs inside the install. Values never enter this
// plan or process argv.
type BuildSecretConfig struct {
	Variable string `json:"variable"`
	Step     string `json:"step"`
}

// ReleaseTaskConfig is the one deliberate shell boundary in a normalized
// deployment. The command is immutable, admin-reviewed plan content; Env lists
// the exact release_task-scoped variables available to that named timed gate.
type ReleaseTaskConfig struct {
	Name             string   `json:"name"`
	Command          string   `json:"command"`
	WorkingDirectory string   `json:"workingDirectory,omitempty"`
	TimeoutSeconds   int      `json:"timeoutSeconds"`
	Env              []string `json:"env"`
	// Runner is where the command runs: "image" is one throwaway container
	// of the release's own image, with the application's toolchain and
	// variables; empty is the historical shell over the unbuilt checkout.
	Runner string `json:"runner,omitempty"`
}

type RuntimePlanConfig struct {
	PreviewIsolation   bool            `json:"previewIsolation,omitempty"`
	Protocol           string          `json:"protocol,omitempty"`
	Image              string          `json:"image,omitempty"`
	Command            []string        `json:"command,omitempty"`
	InternalPort       int             `json:"internalPort,omitempty"`
	HostPort           int             `json:"hostPort,omitempty"`
	Ports              []PublishedPort `json:"ports,omitempty"`
	BindAddress        string          `json:"bindAddress,omitempty"`
	Strategy           ReleaseStrategy `json:"strategy"`
	StopSignal         string          `json:"stopSignal,omitempty"`
	GracePeriodSeconds int             `json:"gracePeriodSeconds,omitempty"`
	DrainSeconds       int             `json:"drainSeconds,omitempty"`
	Privileged         bool            `json:"privileged,omitempty"`
	HostNetwork        bool            `json:"hostNetwork,omitempty"`
	Capabilities       []string        `json:"capabilities,omitempty"`
	Devices            []string        `json:"devices,omitempty"`
	Mounts             []RuntimeMount  `json:"mounts,omitempty"`
	// Resource limits are optional caps handed to the container runtime. Zero
	// means unlimited, which is Docker's own default; the plan carries them so a
	// release snapshot pins exactly what the candidate was allowed to use.
	MemoryMB      int64   `json:"memoryMb,omitempty"`
	CPUs          float64 `json:"cpus,omitempty"`
	PidsLimit     int64   `json:"pidsLimit,omitempty"`
	RestartPolicy string  `json:"restartPolicy,omitempty"`
	// MaxRequestBodyMB is the largest request body the managed route lets
	// through, in megabytes. Zero is DefaultMaxRequestBodyMB.
	MaxRequestBodyMB int `json:"maxRequestBodyMb,omitempty"`
}

// PublishedPort is a container port published on the host next to the routed
// one: Gitea's SSH, Syncthing's sync protocol, anything a reverse proxy cannot
// carry. It pins a host binding the way a fixed host port does, so a plan that
// has one activates stop-first and never becomes a preview.
type PublishedPort struct {
	HostPort      int    `json:"hostPort"`
	ContainerPort int    `json:"containerPort"`
	Protocol      string `json:"protocol,omitempty"`
	// BindAddress is the host interface. Empty publishes on every interface,
	// which is what a port a proxy cannot front exists for.
	BindAddress string `json:"bindAddress,omitempty"`
}

func (p PublishedPort) effectiveProtocol() string {
	if p.Protocol == "udp" {
		return "udp"
	}
	return "tcp"
}

func validBindAddress(address string) bool {
	return address == "" || address == "127.0.0.1" || address == "::1" || address == "0.0.0.0" || address == "::"
}

// Resource limit bounds. The floor keeps a typo such as "5" (MiB) from producing
// a container the kernel kills before its runtime starts; the ceiling keeps a
// stray unit conversion from asking Docker for a petabyte.
const (
	MinRuntimeMemoryMB = 16
	MaxRuntimeMemoryMB = 4 << 20
	MaxRuntimeCPUs     = 1024
	MinRuntimePids     = 16
	MaxRuntimePids     = 1 << 20
)

// RestartPolicies is the closed set the runtime accepts. Empty means the
// historical default, unless-stopped, so existing plans keep their behavior.
var RestartPolicies = []string{"unless-stopped", "always", "on-failure", "no"}

func validRestartPolicy(policy string) bool {
	return policy == "" || slices.Contains(RestartPolicies, policy)
}

// EffectiveRestartPolicy resolves the policy the runtime owner applies.
func (c RuntimePlanConfig) EffectiveRestartPolicy() string {
	if c.RestartPolicy == "" {
		return "unless-stopped"
	}
	return c.RestartPolicy
}

type RuntimeMount struct {
	Source    string        `json:"source"`
	Target    string        `json:"target"`
	ReadOnly  bool          `json:"readOnly,omitempty"`
	Ownership OwnershipMode `json:"ownership"`
}

type PlannedVariable struct {
	Name        string   `json:"name"`
	Sensitivity string   `json:"sensitivity"`
	Scopes      []string `json:"scopes"`
	Required    bool     `json:"required,omitempty"`
	Reference   string   `json:"reference,omitempty"`
	// Value is a plain initial value the plan may carry literally: a blueprint
	// input such as a database name. Secrets never travel this way; they are
	// typed references or are generated.
	Value string `json:"value,omitempty"`
	// DomainTemplate lets the setup form keep reviewed public URL defaults in
	// step with the primary route while leaving explicit overrides alone.
	DomainTemplate string `json:"domainTemplate,omitempty"`
	// Generate asks commit to produce a random secret of this many characters
	// instead of accepting a value. It is how a blueprint's declared secrets
	// exist on this host without ever appearing in a plan or a request.
	Generate int `json:"generate,omitempty"`
	// GenerateFormat shapes the generated secret the way its framework
	// reads it; see GeneratedSecretFormats. Empty is alphanumeric.
	GenerateFormat string `json:"generateFormat,omitempty"`
}

// Bounds for generated secrets: long enough to be a real credential, short
// enough for every engine's password field.
const (
	MinGeneratedSecretLength = 16
	MaxGeneratedSecretLength = 128
)

type PlannedDependency struct {
	Kind         string          `json:"kind"`
	Ownership    OwnershipMode   `json:"ownership"`
	ResourceKind string          `json:"resourceKind"`
	ResourceID   string          `json:"resourceId,omitempty"`
	Config       json.RawMessage `json:"config,omitempty"`
}

type PlannedCheck struct {
	Name     string          `json:"name"`
	Kind     string          `json:"kind"`
	Phase    string          `json:"phase"`
	Required bool            `json:"required"`
	Config   json.RawMessage `json:"config,omitempty"`
}

type PlannedDomain struct {
	Hostname  string        `json:"hostname"`
	HTTPS     bool          `json:"https"`
	Ownership OwnershipMode `json:"ownership"`
	// Protection asks the proxy for a password before it serves this
	// deployment. It applies to the deployment's route as a whole.
	Protection *DomainProtection `json:"protection,omitempty"`
}

type PlanConfiguration struct {
	Build        BuildPlanConfig     `json:"build"`
	Runtime      RuntimePlanConfig   `json:"runtime"`
	Variables    []PlannedVariable   `json:"variables"`
	Dependencies []PlannedDependency `json:"dependencies"`
	Checks       []PlannedCheck      `json:"checks"`
	Domains      []PlannedDomain     `json:"domains"`
	// Accepted for older clients; remote Git branches are always monitored.
	AutoDeploy bool `json:"autoDeploy,omitempty"`
}

type PlanAction struct {
	Ordinal          int      `json:"ordinal"`
	Phase            string   `json:"phase"`
	Owner            string   `json:"owner"`
	Action           string   `json:"action"`
	Arguments        []string `json:"arguments,omitempty"`
	WorkingDirectory string   `json:"workingDirectory,omitempty"`
	ChangesState     bool     `json:"changesState"`
}

type ExactPlan struct {
	DraftRevision int                 `json:"draftRevision"`
	Intent        DraftIntentConfig   `json:"intent"`
	Source        SourceIdentity      `json:"source"`
	Build         BuildPlanConfig     `json:"build"`
	Runtime       RuntimePlanConfig   `json:"runtime"`
	Variables     []PlannedVariable   `json:"variables"`
	Dependencies  []PlannedDependency `json:"dependencies"`
	Checks        []PlannedCheck      `json:"checks"`
	Domains       []PlannedDomain     `json:"domains"`
	Compose       *ComposeAnalysis    `json:"compose,omitempty"`
	Actions       []PlanAction        `json:"actions"`
}

type PreflightResult struct {
	Revision         int                `json:"revision"`
	Findings         []PreflightFinding `json:"findings"`
	Plan             ExactPlan          `json:"plan"`
	ExpectedDowntime bool               `json:"expectedDowntime"`
	Preview          string             `json:"preview"`
	Digest           string             `json:"digest"`
}

type ImportPreview struct {
	Kind          string            `json:"kind"`
	ResourceID    string            `json:"resourceId"`
	Name          string            `json:"name"`
	Source        DraftSourceConfig `json:"source"`
	Configuration PlanConfiguration `json:"configuration"`
	Observed      json.RawMessage   `json:"observed"`
	Unsupported   []string          `json:"unsupported"`
	Warnings      []string          `json:"warnings"`
	WouldChange   []string          `json:"wouldChange"`
}

var (
	ErrDraftNotFound     = errors.New("deployment draft not found")
	ErrDraftExpired      = errors.New("deployment draft expired")
	ErrDraftRevision     = errors.New("deployment draft revision conflict")
	ErrDraftForbidden    = errors.New("deployment draft belongs to another user")
	ErrDraftIncomplete   = errors.New("deployment draft is incomplete")
	ErrDraftCommitted    = errors.New("deployment draft is already committed")
	ErrPreflightBlocked  = errors.New("deployment preflight is blocked")
	ErrInvalidPlan       = errors.New("invalid deployment plan")
	ErrSourceUnavailable = errors.New("deployment source is unavailable")
	ErrGitUnavailable    = errors.New("Git is unavailable")
	ErrDockerUnavailable = errors.New("Docker is unavailable")
	ErrUnsupportedSource = errors.New("unsupported deployment source")
	ErrInvalidSource     = errors.New("invalid deployment source")
	ErrInvalidRef        = errors.New("invalid source ref")
	ErrInvalidImage      = errors.New("invalid image reference")
	ErrInvalidCompose    = errors.New("invalid compose source")
	ErrImportNotFound    = errors.New("import resource not found")
	ErrInvalidVariable   = errors.New("invalid deployment variable")
	ErrVariableNotFound  = errors.New("deployment variable not found")
	ErrVariableCycle     = errors.New("deployment variable reference cycle")
	ErrRevisionConflict  = errors.New("deployment desired revision conflict")
)

// ValidationError is a PlanConfiguration.Validate failure a UI control can
// attach to: Field is a JSON-path-shaped pointer such as
// "runtime.internalPort" or "checks[2]", empty when the failure does not
// belong to one field. Every Validate call site that returns one wraps it
// behind ErrInvalidPlan (fmt.Errorf("%w: %w", ErrInvalidPlan, validationErr)),
// so an existing errors.Is(err, ErrInvalidPlan) check keeps working exactly
// as it did before this type existed; callers that also want the field use
// errors.As to reach it.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string { return e.Message }

// invalidField builds a ValidationError. Every Validate call site that names
// a field reads the same way: the pointer a control can attach to, then the
// message it shows.
func invalidField(field, format string, a ...any) error {
	return &ValidationError{Field: field, Message: fmt.Sprintf(format, a...)}
}

func (c DraftIntentConfig) Validate() error {
	if !projectNameRe.MatchString(c.Name) {
		return fmt.Errorf("name must start with a letter or digit and contain only letters, digits, dots, dashes and underscores")
	}
	if !validProfile(c.Profile) {
		return fmt.Errorf("invalid workload profile %q", c.Profile)
	}
	return nil
}

func validProfile(profile WorkloadProfile) bool {
	switch profile {
	case ProfileWeb, ProfileStatic, ProfileWorker, ProfileImage, ProfileCompose, ProfileService, ProfileGame, ProfileImported:
		return true
	default:
		return false
	}
}

func (c DraftSourceConfig) Validate() error {
	if !validSourceKind(c.Kind) {
		return fmt.Errorf("%w: kind %q", ErrInvalidSource, c.Kind)
	}
	if !validModeForKind(c.Kind, c.Mode) {
		return fmt.Errorf("%w: mode %q is not valid for %s", ErrInvalidSource, c.Mode, c.Kind)
	}
	if c.CredentialID < 0 {
		return fmt.Errorf("%w: credential id must not be negative", ErrInvalidSource)
	}
	if c.Ref == "" && (c.Mode == SourceModeGitURL || c.Mode == SourceModeConnectedRepository || c.Mode == SourceModeComposeGit) {
		c.Ref = "main"
	}
	if c.Ref != "" && !validSourceRef(c.Ref) {
		return fmt.Errorf("%w: ref is malformed", ErrInvalidRef)
	}
	if c.Subdirectory != "" && !safeRelativePath(c.Subdirectory) {
		return fmt.Errorf("%w: subdirectory must remain inside the source root", ErrInvalidSource)
	}
	if c.Platform != "" {
		if c.Mode != SourceModeImageReference || !validPlatform(strings.ToLower(c.Platform)) {
			return fmt.Errorf("%w: image platform is malformed", ErrInvalidImage)
		}
	}
	var allowed map[string]bool
	switch c.Mode {
	case SourceModeGitURL:
		allowed = sourceFieldSet("url", "ref", "credentialId", "subdirectory", "includeSubmodules", "includeLfs")
		if _, _, err := normalizeGitRemote(c.URL); err != nil {
			return err
		}
	case SourceModeComposeGit:
		allowed = sourceFieldSet("url", "ref", "credentialId", "subdirectory", "includeSubmodules", "includeLfs", "composeFiles")
		if _, _, err := normalizeGitRemote(c.URL); err != nil {
			return err
		}
		if err := validateComposeSelectors(c.ComposeFiles); err != nil {
			return err
		}
	case SourceModeConnectedRepository:
		allowed = sourceFieldSet("provider", "providerBaseUrl", "repository", "ref", "credentialId", "subdirectory", "includeSubmodules", "includeLfs")
		if !validProvider(c.Provider) || !validRepository(c.Repository) {
			return fmt.Errorf("%w: provider and owner/repository are required", ErrInvalidSource)
		}
		if c.Provider == "gitea" {
			if _, err := urlWithoutCredentials(c.ProviderBaseURL); err != nil {
				return err
			}
		} else if c.ProviderBaseURL != "" {
			return fmt.Errorf("%w: provider base URL is supported only for Gitea", ErrInvalidSource)
		}
	case SourceModeLocalCheckout:
		allowed = sourceFieldSet("localPath", "subdirectory", "includeSubmodules", "includeLfs")
		if !filepath.IsAbs(c.LocalPath) || len(c.LocalPath) > 4096 {
			return fmt.Errorf("%w: local path must be absolute", ErrInvalidSource)
		}
	case SourceModeComposeLocal:
		allowed = sourceFieldSet("localPath", "subdirectory", "composeFiles")
		if !filepath.IsAbs(c.LocalPath) || len(c.LocalPath) > 4096 {
			return fmt.Errorf("%w: local path must be absolute", ErrInvalidSource)
		}
		if err := validateComposeSelectors(c.ComposeFiles); err != nil {
			return err
		}
	case SourceModeExistingCheckout:
		allowed = sourceFieldSet("localPath", "subdirectory", "managedInPlace", "includeSubmodules", "includeLfs")
		if !filepath.IsAbs(c.LocalPath) || len(c.LocalPath) > 4096 {
			return fmt.Errorf("%w: local path must be absolute", ErrInvalidSource)
		}
	case SourceModeImageReference:
		allowed = sourceFieldSet("image", "platform", "credentialId")
		if _, err := normalizeImageReference(c.Image); err != nil {
			return err
		}
	case SourceModeComposePaste, SourceModeComposeUpload:
		allowed = sourceFieldSet("composeFiles")
		if _, err := analyzeComposeDocuments(c.ComposeFiles); err != nil {
			return err
		}
	case SourceModeExistingContainer, SourceModeExistingStack:
		allowed = sourceFieldSet("resourceId")
		if c.ResourceID == "" || len(c.ResourceID) > 256 || strings.ContainsAny(c.ResourceID, "\x00\r\n") {
			return fmt.Errorf("%w: import resource id is required", ErrInvalidSource)
		}
	case SourceModeBlueprint:
		allowed = sourceFieldSet("blueprintId", "blueprintVersion", "blueprintInputs")
		if c.BlueprintID == "" || c.BlueprintVersion == "" || len(c.BlueprintID) > 128 ||
			len(c.BlueprintVersion) > 128 || strings.ContainsAny(c.BlueprintID+c.BlueprintVersion, "\x00\r\n") {
			return fmt.Errorf("%w: blueprint id and version are required", ErrInvalidSource)
		}
		if len(c.BlueprintInputs) > 64 {
			return fmt.Errorf("%w: a blueprint accepts at most 64 inputs", ErrInvalidSource)
		}
		// Values are checked against the blueprint's own declarations when it
		// is rendered. Only the envelope is bounded here.
		for name, value := range c.BlueprintInputs {
			if name == "" || len(name) > 64 || len(value) > 4096 ||
				strings.ContainsAny(name, "\x00\r\n") || strings.ContainsRune(value, '\x00') {
				return fmt.Errorf("%w: blueprint input %q is invalid", ErrInvalidSource, name)
			}
		}
	}
	if field := c.firstUnexpectedField(allowed); field != "" {
		return fmt.Errorf("%w: field %s is not valid for mode %s", ErrInvalidSource, field, c.Mode)
	}
	return nil
}

func sourceFieldSet(fields ...string) map[string]bool {
	result := make(map[string]bool, len(fields))
	for _, field := range fields {
		result[field] = true
	}
	return result
}

func (c DraftSourceConfig) firstUnexpectedField(allowed map[string]bool) string {
	present := []struct {
		name string
		set  bool
	}{
		{"url", c.URL != ""}, {"provider", c.Provider != ""}, {"providerBaseUrl", c.ProviderBaseURL != ""},
		{"repository", c.Repository != ""}, {"ref", c.Ref != ""}, {"credentialId", c.CredentialID != 0},
		{"localPath", c.LocalPath != ""}, {"subdirectory", c.Subdirectory != ""}, {"managedInPlace", c.ManagedInPlace},
		{"includeSubmodules", c.IncludeSubmodules}, {"includeLfs", c.IncludeLFS}, {"image", c.Image != ""},
		{"platform", c.Platform != ""}, {"composeFiles", len(c.ComposeFiles) != 0}, {"resourceId", c.ResourceID != ""},
		{"blueprintId", c.BlueprintID != ""}, {"blueprintVersion", c.BlueprintVersion != ""},
		{"blueprintInputs", len(c.BlueprintInputs) != 0},
	}
	for _, field := range present {
		if field.set && !allowed[field.name] {
			return field.name
		}
	}
	return ""
}

func validateComposeSelectors(documents []ComposeDocument) error {
	if len(documents) > 16 {
		return fmt.Errorf("%w: at most 16 Compose files are allowed", ErrInvalidCompose)
	}
	seen := make(map[string]bool, len(documents))
	for _, document := range documents {
		if document.Content != "" || !safeRelativePath(document.Path) || cleanComposePath(document.Path) != document.Path ||
			!(strings.HasSuffix(document.Path, ".yml") || strings.HasSuffix(document.Path, ".yaml")) ||
			seen[document.Path] || document.Order < 0 {
			return fmt.Errorf("%w: local/Git Compose selectors must be unique relative .yml/.yaml paths without inline content", ErrInvalidCompose)
		}
		seen[document.Path] = true
	}
	return nil
}

func validSourceKind(kind SourceKind) bool {
	switch kind {
	case SourceGit, SourceLocal, SourceImage, SourceCompose, SourceBlueprint, SourceImport:
		return true
	default:
		return false
	}
}

func validModeForKind(kind SourceKind, mode SourceMode) bool {
	switch kind {
	case SourceGit:
		return mode == SourceModeGitURL || mode == SourceModeConnectedRepository || mode == SourceModeLocalCheckout
	case SourceLocal:
		return mode == SourceModeLocalCheckout
	case SourceImage:
		return mode == SourceModeImageReference
	case SourceCompose:
		return mode == SourceModeComposePaste || mode == SourceModeComposeUpload ||
			mode == SourceModeComposeGit || mode == SourceModeComposeLocal
	case SourceBlueprint:
		return mode == SourceModeBlueprint
	case SourceImport:
		return mode == SourceModeExistingCheckout || mode == SourceModeExistingContainer || mode == SourceModeExistingStack
	default:
		return false
	}
}

var sourceRefRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/@+-]{0,254}$`)

func validSourceRef(ref string) bool {
	if !sourceRefRE.MatchString(ref) || strings.Contains(ref, "..") || strings.Contains(ref, "@{") ||
		strings.Contains(ref, "//") || strings.HasSuffix(ref, "/") || strings.HasSuffix(ref, ".") ||
		ref == "HEAD" || ref == "FETCH_HEAD" {
		return false
	}
	name := ref
	if strings.HasPrefix(ref, "refs/") {
		if strings.HasPrefix(ref, "refs/heads/") {
			name = strings.TrimPrefix(ref, "refs/heads/")
		} else if strings.HasPrefix(ref, "refs/tags/") {
			name = strings.TrimPrefix(ref, "refs/tags/")
		} else {
			return false
		}
	}
	for _, component := range strings.Split(name, "/") {
		if component == "" || strings.HasPrefix(component, ".") || strings.HasSuffix(component, ".lock") {
			return false
		}
	}
	return true
}

func normalizeGitRemote(raw string) (remote, repository string, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 2048 || strings.ContainsAny(raw, "\x00\r\n") {
		return "", "", fmt.Errorf("%w: Git URL is missing or too long", ErrInvalidSource)
	}
	// Strict SCP-style SSH is supported without passing the value through a
	// shell. The user is deliberately limited to git; credentials belong in a
	// referenced credential record, not the URL.
	if strings.HasPrefix(raw, "git@") && !strings.Contains(raw, "://") {
		hostPath := strings.TrimPrefix(raw, "git@")
		host, path, ok := strings.Cut(hostPath, ":")
		if !ok || !validRemoteHost(host) || !validRepositoryPath(path) {
			return "", "", fmt.Errorf("%w: malformed SSH Git URL", ErrInvalidSource)
		}
		return "git@" + strings.ToLower(host) + ":" + path, repositoryName(path), nil
	}
	u, parseErr := url.Parse(raw)
	if parseErr != nil || u.Hostname() == "" || u.RawQuery != "" || u.Fragment != "" {
		return "", "", fmt.Errorf("%w: malformed Git URL", ErrInvalidSource)
	}
	if u.Scheme != "https" && u.Scheme != "ssh" {
		return "", "", fmt.Errorf("%w: Git URL must use https or ssh", ErrInvalidSource)
	}
	if u.User != nil {
		if _, hasPassword := u.User.Password(); hasPassword || (u.Scheme == "https" && u.User.Username() != "") ||
			(u.Scheme == "ssh" && u.User.Username() != "git") {
			return "", "", fmt.Errorf("%w: credentials must be referenced, not embedded in a Git URL", ErrInvalidSource)
		}
	}
	if !validRemoteHost(u.Hostname()) || !validRepositoryPath(strings.TrimPrefix(u.EscapedPath(), "/")) {
		return "", "", fmt.Errorf("%w: malformed Git repository path", ErrInvalidSource)
	}
	u.Host = strings.ToLower(u.Host)
	return u.String(), repositoryName(u.Path), nil
}

func validRemoteHost(host string) bool {
	return host != "" && len(host) <= 253 && !strings.ContainsAny(host, " /\\@")
}

func validRepositoryPath(path string) bool {
	decoded, err := url.PathUnescape(path)
	if err != nil || decoded == "" || strings.HasPrefix(decoded, "/") ||
		strings.ContainsAny(decoded, "\x00\r\n\t\\") {
		return false
	}
	parts := strings.Split(strings.TrimSuffix(decoded, ".git"), "/")
	if len(parts) < 2 {
		return false
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || strings.ContainsAny(part, "\x00\\") {
			return false
		}
	}
	return true
}

func repositoryName(path string) string {
	path = strings.TrimSuffix(strings.Trim(path, "/"), ".git")
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		return path
	}
	return parts[len(parts)-2] + "/" + parts[len(parts)-1]
}

func validProvider(provider string) bool {
	switch provider {
	case "github", "gitlab", "bitbucket", "gitea":
		return true
	default:
		return false
	}
}

func validRepository(repository string) bool {
	return validRepositoryPath(repository) && !strings.Contains(repository, ":")
}

func normalizeImageReference(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 512 || strings.ContainsAny(raw, "\x00\r\n\t ") {
		return "", fmt.Errorf("%w: malformed image reference", ErrInvalidImage)
	}
	named, err := reference.ParseNormalizedNamed(raw)
	if err != nil {
		return "", fmt.Errorf("%w: malformed image reference", ErrInvalidImage)
	}
	if _, tagged := named.(reference.NamedTagged); !tagged {
		if _, digested := named.(reference.Canonical); !digested {
			named = reference.TagNameOnly(named)
		}
	}
	return named.String(), nil
}

func validateComposeDocuments(documents []ComposeDocument) error {
	if len(documents) == 0 || len(documents) > 16 {
		return fmt.Errorf("%w: between 1 and 16 Compose files are required", ErrInvalidCompose)
	}
	seen := make(map[string]bool, len(documents))
	total := 0
	for i := range documents {
		doc := &documents[i]
		if !safeRelativePath(doc.Path) || cleanComposePath(doc.Path) != doc.Path || doc.Order < 0 ||
			!(strings.HasSuffix(doc.Path, ".yml") || strings.HasSuffix(doc.Path, ".yaml")) {
			return fmt.Errorf("%w: file %q must be a relative .yml/.yaml path", ErrInvalidCompose, doc.Path)
		}
		if seen[doc.Path] {
			return fmt.Errorf("%w: duplicate file %q", ErrInvalidCompose, doc.Path)
		}
		seen[doc.Path] = true
		if doc.Content == "" {
			return fmt.Errorf("%w: file %q is empty", ErrInvalidCompose, doc.Path)
		}
		total += len(doc.Content)
	}
	if total > 4<<20 {
		return fmt.Errorf("%w: Compose input exceeds 4 MiB", ErrInvalidCompose)
	}
	return nil
}

func safeRelativePath(path string) bool {
	// The string checked here is the string callers join onto a root. Trimming
	// first would validate one path and use another: "\r0" trims to "0", passes
	// the control-character check, and is then written as a file whose name
	// carries a carriage return.
	if path == "" || path != strings.TrimSpace(path) || strings.ContainsAny(path, "\x00\r\n") {
		return false
	}
	clean := filepath.Clean(path)
	return clean != "" && clean != "." && !filepath.IsAbs(clean) && clean != ".." &&
		!strings.HasPrefix(clean, ".."+string(filepath.Separator))
}

func (c PlanConfiguration) Validate() error {
	if len(c.Variables) > 256 || len(c.Dependencies) > 128 || len(c.Checks) > 128 ||
		len(c.Domains) > 32 || len(c.Runtime.Command) > 256 || len(c.Runtime.Mounts) > 128 ||
		len(c.Runtime.Capabilities) > 128 || len(c.Runtime.Devices) > 128 ||
		len(c.Build.Secrets) > 64 || len(c.Build.ReleaseTasks) > 16 {
		return fmt.Errorf("plan configuration exceeds its item bounds")
	}
	if !validBuildMethod(c.Build.Method) {
		return fmt.Errorf("invalid build method %q", c.Build.Method)
	}
	if c.Build.Recipe != "" && !validRecipe(c.Build.Recipe) {
		return fmt.Errorf("unsupported automatic build recipe %q", c.Build.Recipe)
	}
	if c.Build.Method != BuildRecipe && c.Build.Recipe != "" {
		return fmt.Errorf("a recipe is valid only for the automatic builder")
	}
	if c.Build.GoVersion != "" && (c.Build.Method != BuildRecipe || c.Build.Recipe != "go" || !goRecipeVersionRE.MatchString(c.Build.GoVersion)) {
		return fmt.Errorf("Go version must select stable Go 1.25 or 1.26 in a Go recipe; use a Dockerfile for other toolchains")
	}
	if c.Build.GoPackage != "" && (c.Build.Method != BuildRecipe || c.Build.Recipe != "go" || !validGoPackagePath(c.Build.GoPackage)) {
		return fmt.Errorf("Go main package must be a directory inside the root, such as cmd/api, in a Go recipe")
	}
	// The PHP recipe installs its front-end assets through the same Node
	// install, so the same choice applies to it.
	if c.Build.PackageManager != "" && (c.Build.Method != BuildRecipe || (c.Build.Recipe != "node" && c.Build.Recipe != "php") || !validNodePackageManager(c.Build.PackageManager)) {
		return fmt.Errorf("package manager must be bun, npm, pnpm or yarn in a JavaScript or PHP recipe")
	}
	if c.Build.PythonVersion != "" && (c.Build.Method != BuildRecipe || c.Build.Recipe != "python" || !pythonRecipeVersionRE.MatchString(c.Build.PythonVersion)) {
		return fmt.Errorf("Python version must select 3.10, 3.11, 3.12 or 3.13 in a Python recipe; use a Dockerfile for other interpreters")
	}
	// A Node major chosen in Build settings outranks what the repository
	// declares; empty follows the repository.
	if c.Build.NodeVersion != "" && (c.Build.Method != BuildRecipe || c.Build.Recipe != "node" || !nodeRecipeVersionRE.MatchString(c.Build.NodeVersion)) {
		return fmt.Errorf("Node version must select 20, 22 or 24 in a JavaScript recipe; use a Dockerfile for other releases")
	}
	if c.Build.SPAFallback && c.Build.Method != BuildRecipe && c.Build.Method != BuildStatic {
		return fmt.Errorf("the single-page fallback applies only to a static site or a recipe with static output")
	}
	if c.Build.Recipe == "go" && cgoEnabledCommandRE.MatchString(c.Build.BuildCommand) {
		return fmt.Errorf("the Go recipe builds without CGO; use a Dockerfile with the required C toolchain")
	}
	if c.Build.TargetPlatform != "" && !validPlatform(strings.ToLower(c.Build.TargetPlatform)) {
		return fmt.Errorf("build target platform is malformed")
	}
	if c.Build.Target != "" && (c.Build.Method != BuildDockerfile || !dockerfileStageNameRE.MatchString(c.Build.Target)) {
		return fmt.Errorf("a build target names one stage of a custom Dockerfile")
	}
	if c.Build.PrimaryService != "" && (c.Build.Method != BuildCompose || !validComposeServiceName(c.Build.PrimaryService)) {
		return invalidField("build.primaryService", "a primary service names one service of a Compose stack")
	}
	for _, path := range []string{c.Build.RootDirectory, c.Build.Dockerfile} {
		if path != "" && !safeRelativePath(path) {
			return fmt.Errorf("build paths must remain inside the source root")
		}
	}
	if c.Build.OutputDirectory != "" && !validOutputDirectory(c.Build.OutputDirectory) {
		return fmt.Errorf("build paths must remain inside the source root")
	}
	if len(c.Build.BuildCommand) > 4096 || len(c.Build.StartCommand) > 4096 {
		return fmt.Errorf("build or start command exceeds 4096 bytes")
	}
	for label, command := range map[string]string{
		"build command": c.Build.BuildCommand,
		"start command": c.Build.StartCommand,
	} {
		if err := rejectPlanSecretLiteral(label, command); err != nil {
			return err
		}
		if secretCommandFlagRE.MatchString(command) {
			return fmt.Errorf("%s passes credential material through argv; use a scoped variable", label)
		}
	}
	if !validStrategy(c.Runtime.Strategy) {
		return fmt.Errorf("invalid release strategy %q", c.Runtime.Strategy)
	}
	if c.Runtime.Image != "" && !validPlannedReference(c.Runtime.Image) {
		if _, err := normalizeImageReference(c.Runtime.Image); err != nil {
			return fmt.Errorf("invalid runtime image: %w", err)
		}
	}
	for index, argument := range c.Runtime.Command {
		if len(argument) > 4096 {
			return fmt.Errorf("runtime command argument exceeds 4096 bytes")
		}
		if err := rejectPlanSecretLiteral("runtime command", argument); err != nil {
			return err
		}
		if commandArgumentContainsSecret(c.Runtime.Command, index) {
			return fmt.Errorf("runtime command passes credential material through argv; use a scoped variable")
		}
	}
	if c.Runtime.Protocol != "" && c.Runtime.Protocol != "tcp" {
		return fmt.Errorf("runtime protocol %q is not supported by this release", c.Runtime.Protocol)
	}
	if c.Runtime.InternalPort < 0 || c.Runtime.InternalPort > 65535 {
		return invalidField("runtime.internalPort", "runtime internal port must be 0 (unset) or between 1 and 65535")
	}
	if c.Runtime.HostPort < 0 || c.Runtime.HostPort > 65535 {
		return invalidField("runtime.hostPort", "runtime host port must be 0 (unset) or between 1 and 65535")
	}
	publishedHostPorts := map[string]bool{}
	for index, port := range c.Runtime.Ports {
		field := fmt.Sprintf("runtime.ports[%d]", index)
		if port.HostPort < 1 || port.HostPort > 65535 || port.ContainerPort < 1 || port.ContainerPort > 65535 {
			return invalidField(field, "a published port needs a host port and a container port between 1 and 65535")
		}
		if port.Protocol != "" && port.Protocol != "tcp" && port.Protocol != "udp" {
			return invalidField(field, "a published port's protocol must be tcp or udp")
		}
		if !validBindAddress(port.BindAddress) {
			return invalidField(field, "a published port's bind address is not supported")
		}
		if c.Runtime.HostNetwork {
			return invalidField(field, "host networking already exposes every port; remove the published ports or the host network")
		}
		key := port.effectiveProtocol() + ":" + strconv.Itoa(port.HostPort)
		if publishedHostPorts[key] || (port.effectiveProtocol() == "tcp" && port.HostPort == c.Runtime.HostPort) {
			return invalidField(field, "host port %d/%s is published twice", port.HostPort, port.effectiveProtocol())
		}
		publishedHostPorts[key] = true
	}
	if c.Runtime.GracePeriodSeconds < 0 || c.Runtime.GracePeriodSeconds > 300 ||
		c.Runtime.DrainSeconds < 0 || c.Runtime.DrainSeconds > 300 {
		return fmt.Errorf("runtime grace and drain periods must be between 0 and 300 seconds")
	}
	if c.Runtime.StopSignal != "" && c.Runtime.StopSignal != "SIGTERM" && c.Runtime.StopSignal != "SIGINT" &&
		c.Runtime.StopSignal != "SIGQUIT" && c.Runtime.StopSignal != "SIGHUP" {
		return fmt.Errorf("runtime stop signal is not supported")
	}
	if !validBindAddress(c.Runtime.BindAddress) {
		return fmt.Errorf("runtime bind address is not supported")
	}
	if c.Runtime.MemoryMB != 0 && (c.Runtime.MemoryMB < MinRuntimeMemoryMB || c.Runtime.MemoryMB > MaxRuntimeMemoryMB) {
		return fmt.Errorf("runtime memory limit must be between %d MiB and %d MiB, or zero for no limit", MinRuntimeMemoryMB, MaxRuntimeMemoryMB)
	}
	if c.Runtime.CPUs != 0 && (c.Runtime.CPUs < 0.01 || c.Runtime.CPUs > MaxRuntimeCPUs || math.IsNaN(c.Runtime.CPUs) || math.IsInf(c.Runtime.CPUs, 0)) {
		return fmt.Errorf("runtime CPU limit must be between 0.01 and %d CPUs, or zero for no limit", MaxRuntimeCPUs)
	}
	if c.Runtime.PidsLimit != 0 && (c.Runtime.PidsLimit < MinRuntimePids || c.Runtime.PidsLimit > MaxRuntimePids) {
		return fmt.Errorf("runtime PID limit must be between %d and %d, or zero for no limit", MinRuntimePids, MaxRuntimePids)
	}
	if c.Runtime.MaxRequestBodyMB < 0 || c.Runtime.MaxRequestBodyMB > MaxRequestBodyMB {
		return invalidField("runtime.maxRequestBodyMb", "request body limit must be between 1 and %d MB, or zero for the %d MB default", MaxRequestBodyMB, DefaultMaxRequestBodyMB)
	}
	if !validRestartPolicy(c.Runtime.RestartPolicy) {
		return fmt.Errorf("runtime restart policy must be one of %s", strings.Join(RestartPolicies, ", "))
	}
	seenMountTargets := map[string]bool{}
	for index, mount := range c.Runtime.Mounts {
		source := strings.TrimSpace(mount.Source)
		target := filepath.Clean(mount.Target)
		pathShapedSource := filepath.IsAbs(source) || strings.HasPrefix(source, ".") || strings.Contains(source, "/")
		if source == "" || strings.ContainsAny(source, "\x00\r\n") ||
			(pathShapedSource && !filepath.IsAbs(source)) || mount.Target == "" || !filepath.IsAbs(target) ||
			target == "/" || strings.ContainsAny(mount.Target, "\x00\r\n") || seenMountTargets[target] ||
			!validOwnership(mount.Ownership) {
			return invalidField(fmt.Sprintf("runtime.mounts[%d]", index), "runtime mount target and ownership are invalid")
		}
		seenMountTargets[target] = true
	}
	for _, capability := range c.Runtime.Capabilities {
		if !capabilityRE.MatchString(capability) {
			return fmt.Errorf("runtime capability is invalid")
		}
	}
	for _, device := range c.Runtime.Devices {
		if !filepath.IsAbs(device) || strings.ContainsAny(device, "\x00\r\n") {
			return fmt.Errorf("runtime device is invalid")
		}
	}
	seenVariables := map[string]bool{}
	variableScopes := map[string]map[string]bool{}
	for _, variable := range c.Variables {
		if ValidateEnvKey(variable.Name) != nil || seenVariables[variable.Name] {
			return invalidField(variable.Name, "invalid or duplicate planned variable %q", variable.Name)
		}
		seenVariables[variable.Name] = true
		if variable.Sensitivity != "plain" && variable.Sensitivity != "secret" {
			return invalidField(variable.Name, "invalid sensitivity for %s", variable.Name)
		}
		if len(variable.Scopes) == 0 || len(variable.Scopes) > 3 {
			return invalidField(variable.Name, "variable %s must have at least one scope", variable.Name)
		}
		seenScopes := map[string]bool{}
		for _, scope := range variable.Scopes {
			if scope != "build" && scope != "runtime" && scope != "release_task" {
				return invalidField(variable.Name, "invalid scope %q for %s", scope, variable.Name)
			}
			if seenScopes[scope] {
				return invalidField(variable.Name, "duplicate scope %q for %s", scope, variable.Name)
			}
			seenScopes[scope] = true
		}
		variableScopes[variable.Name] = seenScopes
		if variable.DomainTemplate != "" {
			template := variable.DomainTemplate
			literal := strings.NewReplacer("{{hostname}}", "example.com", "{{scheme}}", "https").Replace(template)
			if variable.Sensitivity != "plain" || len(template) > 4096 ||
				!strings.Contains(template, "{{hostname}}") || strings.ContainsAny(literal, "{}\x00\r\n") {
				return invalidField(variable.Name, "domain template for %s is malformed or not plain", variable.Name)
			}
			if err := rejectPlanSecretLiteral("domain template for "+variable.Name, literal); err != nil {
				return invalidField(variable.Name, "%v", err)
			}
		}
		if variable.Reference != "" {
			reference, err := ParseVariableReference(variable.Reference)
			if err != nil {
				return invalidField(variable.Name, "invalid typed reference for %s", variable.Name)
			}
			if (reference.Kind == "credential" || reference.Kind == "database") && variable.Sensitivity != "secret" {
				return invalidField(variable.Name, "%s reference for %s must be secret", reference.Kind, variable.Name)
			}
		}
		if variable.Value != "" {
			if variable.Reference != "" || variable.Generate != 0 {
				return invalidField(variable.Name, "%s may carry a value, a reference or a generation request, not several", variable.Name)
			}
			if variable.Sensitivity == "secret" {
				return invalidField(variable.Name, "%s is secret and cannot carry a literal value; use a typed reference or generate it", variable.Name)
			}
			if len(variable.Value) > 4096 || strings.ContainsAny(variable.Value, "\x00") || strings.HasPrefix(strings.TrimSpace(variable.Value), "${{") {
				return invalidField(variable.Name, "literal value for %s is malformed", variable.Name)
			}
			if err := rejectPlanSecretLiteral("value for "+variable.Name, variable.Value); err != nil {
				return invalidField(variable.Name, "%v", err)
			}
		}
		if variable.Generate != 0 {
			if variable.Reference != "" {
				return invalidField(variable.Name, "%s cannot be both generated and referenced", variable.Name)
			}
			if variable.Sensitivity != "secret" {
				return invalidField(variable.Name, "generated variable %s must be secret", variable.Name)
			}
			if variable.Generate < MinGeneratedSecretLength || variable.Generate > MaxGeneratedSecretLength {
				return invalidField(variable.Name, "generated length for %s must be between %d and %d", variable.Name, MinGeneratedSecretLength, MaxGeneratedSecretLength)
			}
		}
		if variable.GenerateFormat != "" && (variable.Generate == 0 || !validGeneratedSecretFormat(variable.GenerateFormat)) {
			return invalidField(variable.Name, "generated format for %s is invalid", variable.Name)
		}
	}
	variableReferences := make(map[string]string, len(c.Variables))
	for _, variable := range c.Variables {
		variableReferences[variable.Name] = variable.Reference
	}
	if _, _, err := ResolveVariableGraph(variableReferences, nil); err != nil {
		return err
	}
	seenBuildSecrets := map[string]bool{}
	for _, secret := range c.Build.Secrets {
		if ValidateEnvKey(secret.Variable) != nil || !validBuildSecretStep(secret.Step) ||
			seenBuildSecrets[secret.Variable] || !variableScopes[secret.Variable]["build"] {
			return fmt.Errorf("build secret %q must name one build-scoped variable and the install, build or install_and_build step", secret.Variable)
		}
		seenBuildSecrets[secret.Variable] = true
	}
	if len(c.Build.Secrets) != 0 && c.Build.Method != BuildRecipe {
		return fmt.Errorf("build secrets are supported only by reviewed automatic recipes")
	}
	seenTasks := map[string]bool{}
	for index, task := range c.Build.ReleaseTasks {
		field := fmt.Sprintf("build.releaseTasks[%d]", index)
		if !releaseTaskNameRE.MatchString(task.Name) || seenTasks[task.Name] || strings.TrimSpace(task.Command) == "" ||
			len(task.Command) > 16<<10 || task.TimeoutSeconds < 1 || task.TimeoutSeconds > 3600 ||
			(task.WorkingDirectory != "" && !safeRelativePath(task.WorkingDirectory)) || len(task.Env) > 64 {
			return invalidField(field, "release task %q is invalid", task.Name)
		}
		if err := rejectPlanSecretLiteral("release task command", task.Command); err != nil {
			return invalidField(field, "%v", err)
		}
		if task.Runner != "" && task.Runner != ReleaseTaskRunnerImage {
			return invalidField(field, "release task %q runner must be image or the host shell", task.Name)
		}
		if task.Runner == ReleaseTaskRunnerImage && (c.Build.Method == BuildNone || c.Build.Method == BuildLegacyCompose) {
			return invalidField(field, "release task %q runs in the release image, and this build produces none", task.Name)
		}
		if secretCommandFlagRE.MatchString(task.Command) {
			return invalidField(field, "release task %q passes credential material through argv; use its scoped environment", task.Name)
		}
		seenTasks[task.Name] = true
		seenEnv := map[string]bool{}
		for _, name := range task.Env {
			if ValidateEnvKey(name) != nil || seenEnv[name] || !variableScopes[name]["release_task"] {
				return invalidField(field, "release task %q environment %q is not a declared release_task-scoped variable", task.Name, name)
			}
			seenEnv[name] = true
		}
	}
	for index, dependency := range c.Dependencies {
		field := fmt.Sprintf("dependencies[%d]", index)
		if dependency.Kind == "" || dependency.ResourceKind == "" || !validOwnership(dependency.Ownership) ||
			len(dependency.Kind) > 64 || len(dependency.ResourceKind) > 64 || len(dependency.ResourceID) > 512 ||
			len(dependency.Config) > 256<<10 || dependency.Kind == "domain" ||
			strings.ContainsAny(dependency.ResourceID, "\x00\r\n") ||
			(len(dependency.Config) != 0 && !json.Valid(dependency.Config)) {
			return invalidField(field, "invalid planned dependency")
		}
		if err := rejectPlanConfigSecrets("dependency config", dependency.Config); err != nil {
			return invalidField(field, "%v", err)
		}
		switch dependency.Kind {
		case "backup":
			if dependency.ResourceKind != "backup_job" {
				return invalidField(field, "backup dependency must name a backup_job")
			}
			if _, err := parsePositiveReferenceID(dependency.ResourceID); err != nil {
				return invalidField(field, "%v", err)
			}
			if _, err := decodeBackupDependencyConfig(dependency.Config); err != nil {
				return invalidField(field, "%v", err)
			}
		case "database":
			if dependency.ResourceKind != "database_connection" {
				return invalidField(field, "database dependency must name a database_connection")
			}
			if _, err := parsePositiveReferenceID(dependency.ResourceID); err != nil {
				return invalidField(field, "%v", err)
			}
		case "storage":
			if dependency.ResourceKind != "docker_volume" && dependency.ResourceKind != "bind_path" {
				return invalidField(field, "storage dependency must name a docker_volume or bind_path")
			}
			if dependency.ResourceID == "" {
				return invalidField(field, "storage dependency resource id is required")
			}
		}
	}
	for index, check := range c.Checks {
		field := fmt.Sprintf("checks[%d]", index)
		if check.Name == "" || !validCheckKind(check.Kind) || (check.Phase != "readiness" && check.Phase != "smoke") ||
			len(check.Name) > 128 || len(check.Config) > 256<<10 || strings.ContainsAny(check.Name, "\x00\r\n") ||
			(len(check.Config) != 0 && !json.Valid(check.Config)) {
			return invalidField(field, "invalid planned check %q", check.Name)
		}
		if err := rejectPlanConfigSecrets("check config", check.Config); err != nil {
			return invalidField(field, "%v", err)
		}
		if err := validateCheckConfiguration(check.Kind, check.Config); err != nil {
			return invalidField(field, "invalid planned check %q: %v", check.Name, err)
		}
	}
	seenDomains := map[string]bool{}
	for index, domain := range c.Domains {
		hostname := strings.ToLower(strings.TrimSpace(domain.Hostname))
		if !plannedDomainRE.MatchString(hostname) || seenDomains[hostname] ||
			(domain.Ownership != OwnershipManaged && domain.Ownership != OwnershipLinked) {
			return invalidField(fmt.Sprintf("domains[%d]", index), "invalid or duplicate planned domain")
		}
		seenDomains[hostname] = true
		if err := domain.Protection.validate(fmt.Sprintf("domains[%d].protection", index)); err != nil {
			return err
		}
	}
	return nil
}

var plannedReferenceRE = regexp.MustCompile(`^\$\{\{[A-Za-z][A-Za-z0-9_-]{0,31}\.[A-Za-z0-9][A-Za-z0-9._:-]{0,222}\}\}$`)
var releaseTaskNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._ -]{0,63}$`)
var secretAssignmentRE = regexp.MustCompile(`(?i)(?:^|[[:space:];])(?:[A-Za-z0-9_]*(?:password|passwd|secret|token|api_key|access_key)[A-Za-z0-9_]*)[[:space:]]*=`)
var secretCommandFlagRE = regexp.MustCompile(`(?i)(?:^|[[:space:]])--?[A-Za-z0-9_-]*(?:password|passwd|secret|token|api[_-]?key|access[_-]?key)(?:[=[:space:]]|$)`)
var capabilityRE = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)
var plannedDomainRE = regexp.MustCompile(`^(?:\*\.)?[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)+$`)

func validPlannedReference(reference string) bool {
	_, err := ParseVariableReference(reference)
	return err == nil
}

// VariableReference is the closed, parsed form of the reference syntax shown
// in the UI. Keeping the parser in the backend makes previews and execution
// agree about whether a value is a reference; callers never infer semantics by
// splitting arbitrary strings themselves.
type VariableReference struct {
	Kind   string `json:"kind"`
	Target string `json:"target"`
}

func ParseVariableReference(value string) (VariableReference, error) {
	if !plannedReferenceRE.MatchString(value) {
		return VariableReference{}, fmt.Errorf("%w: malformed typed reference", ErrInvalidVariable)
	}
	body := strings.TrimSuffix(strings.TrimPrefix(value, "${{"), "}}")
	kind, target, ok := strings.Cut(body, ".")
	if !ok || target == "" {
		return VariableReference{}, fmt.Errorf("%w: malformed typed reference", ErrInvalidVariable)
	}
	switch kind {
	case "variable":
		if ValidateEnvKey(target) != nil {
			return VariableReference{}, fmt.Errorf("%w: variable reference target is invalid", ErrInvalidVariable)
		}
	case "credential", "domain", "service", "database":
		// A reference target is a name or an id. It is never a path, and a
		// parent segment in one is only ever an attempt to make it into one.
		if len(target) > 223 || strings.ContainsAny(target, "\x00\r\n/\\") ||
			strings.Contains(target, "..") {
			return VariableReference{}, fmt.Errorf("%w: reference target is invalid", ErrInvalidVariable)
		}
	default:
		return VariableReference{}, fmt.Errorf("%w: unsupported reference kind %q", ErrInvalidVariable, kind)
	}
	return VariableReference{Kind: kind, Target: target}, nil
}

func rejectPlanSecretLiteral(label, value string) error {
	if value == "" || validPlannedReference(value) {
		return nil
	}
	lower := strings.ToLower(value)
	if strings.Contains(lower, "-----begin private key-----") || URLHasCredentials(value) || secretAssignmentRE.MatchString(value) {
		return fmt.Errorf("%s contains credential material; use a scoped typed reference", label)
	}
	return nil
}

func commandArgumentContainsSecret(arguments []string, index int) bool {
	argument := strings.TrimSpace(arguments[index])
	key, value, hasValue := strings.Cut(argument, "=")
	if strings.HasPrefix(key, "-") && importSecretFlag(key) {
		if hasValue {
			return !validPlannedReference(value)
		}
		return index+1 >= len(arguments) || !validPlannedReference(strings.TrimSpace(arguments[index+1]))
	}
	if index > 0 && strings.HasPrefix(strings.TrimSpace(arguments[index-1]), "-") && importSecretFlag(arguments[index-1]) {
		return !validPlannedReference(argument)
	}
	return false
}

func rejectPlanConfigSecrets(label string, raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return fmt.Errorf("invalid %s", label)
	}
	if configContainsSecretLiteral(value, "") {
		return fmt.Errorf("%s contains credential material; use a scoped typed reference", label)
	}
	return nil
}

func configContainsSecretLiteral(value any, key string) bool {
	switch typed := value.(type) {
	case map[string]any:
		for childKey, child := range typed {
			if configContainsSecretLiteral(child, childKey) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if configContainsSecretLiteral(child, key) {
				return true
			}
		}
	case string:
		if typed == "" || validPlannedReference(typed) {
			return false
		}
		return secretShapedKey(key) || strings.Contains(strings.ToLower(typed), "-----begin private key-----") || URLHasCredentials(typed)
	}
	return false
}

// validRecipe is the closed set of automatic recipes; a name outside it is
// refused at planning so a plan never names a builder that does not exist.
func validRecipe(name string) bool {
	switch name {
	case "node", "go", "python", "rust", "java", "dotnet", "deno", "php", "site":
		return true
	}
	return false
}

// validBuildSecretStep is the closed set of recipe stages a build value can
// be mounted in.
func validBuildSecretStep(step string) bool {
	switch step {
	case "install", "build", "install_and_build":
		return true
	}
	return false
}

// buildSecretReaches says whether a value mapped to mapped is mounted in the
// recipe stage step.
func buildSecretReaches(mapped, step string) bool {
	return mapped == step || mapped == "install_and_build"
}

// validDetectedDatabaseEngine is the closed set of engines quick setup can
// provision, which is what makes a suggestion actionable.
func validDetectedDatabaseEngine(engine string) bool {
	switch engine {
	case "postgres", "mysql", "mariadb", "redis", "mongodb":
		return true
	}
	return false
}

func validBuildMethod(method BuildMethod) bool {
	switch method {
	case BuildRecipe, BuildDockerfile, BuildStatic, BuildImage, BuildCompose, BuildNone, BuildLegacyCompose:
		return true
	default:
		return false
	}
}

func validStrategy(strategy ReleaseStrategy) bool {
	return strategy == StrategyBlueGreen || strategy == StrategyStopFirst
}

func validOwnership(ownership OwnershipMode) bool {
	return ownership == OwnershipManaged || ownership == OwnershipLinked || ownership == OwnershipObserved
}

func validCheckKind(kind string) bool {
	switch kind {
	case "http", "tcp", "docker_health", "command", "public_route", "dns", "tls", "game_handshake", "backup_freshness":
		return true
	default:
		return false
	}
}

func validateDetectionResult(source *DraftSourceConfig, detection DetectionResult) error {
	if source == nil || detection.Source.Kind != source.Kind ||
		detection.Source.CredentialID != source.CredentialID {
		return fmt.Errorf("%w: detection source does not match the saved source", ErrInvalidPlan)
	}
	if detection.ScannedFiles < 0 || detection.ScannedFiles > 100_000 ||
		detection.ScannedBytes < 0 || detection.ScannedBytes > 64<<20 ||
		len(detection.Candidates) > 256 || len(detection.Unavailable) > 1024 ||
		len(detection.TruncatedReason) > 256 || detection.Truncated != (detection.TruncatedReason != "") {
		return fmt.Errorf("%w: detection evidence exceeds its bounds", ErrInvalidPlan)
	}
	if rejectPlanSecretLiteral("source availability evidence", detection.Unavailable) != nil {
		return fmt.Errorf("%w: source availability evidence is malformed", ErrInvalidPlan)
	}
	identity := detection.Source
	for _, value := range []string{
		identity.Remote, identity.Repository, identity.Ref, identity.Revision,
		identity.Digest, identity.OS, identity.Architecture, identity.LocalPath,
	} {
		if len(value) > 4096 || strings.ContainsAny(value, "\x00\r\n") {
			return fmt.Errorf("%w: source identity is malformed", ErrInvalidPlan)
		}
	}
	if identity.Remote != "" {
		if normalized, _, err := normalizeGitRemote(identity.Remote); err != nil || normalized != identity.Remote {
			return fmt.Errorf("%w: source identity remote is malformed", ErrInvalidPlan)
		}
	}
	if identity.Revision != "" &&
		(source.Mode == SourceModeGitURL || source.Mode == SourceModeConnectedRepository ||
			source.Mode == SourceModeLocalCheckout || source.Mode == SourceModeComposeGit ||
			source.Mode == SourceModeExistingCheckout) && !validGitObjectID(identity.Revision) {
		return fmt.Errorf("%w: source revision is not an immutable Git object id", ErrInvalidPlan)
	}
	if identity.Digest != "" && !contentDigestRE.MatchString(identity.Digest) {
		return fmt.Errorf("%w: source digest is malformed", ErrInvalidPlan)
	}
	if len(identity.Observed) > 1<<20 || (len(identity.Observed) != 0 && !json.Valid(identity.Observed)) {
		return fmt.Errorf("%w: observed source evidence is malformed", ErrInvalidPlan)
	}
	if len(identity.Platforms) > 128 {
		return fmt.Errorf("%w: source platform evidence exceeds its bounds", ErrInvalidPlan)
	}
	for _, platform := range identity.Platforms {
		if !validPlatform(platform) {
			return fmt.Errorf("%w: source platform evidence is malformed", ErrInvalidPlan)
		}
	}
	if source.Mode == SourceModeGitURL || source.Mode == SourceModeConnectedRepository || source.Mode == SourceModeComposeGit {
		remote, repository, err := remoteForSource(*source)
		if err != nil || identity.Remote != remote || identity.Repository != repository ||
			identity.Ref != canonicalSourceConfig(*source).Ref || !validGitObjectID(identity.Revision) {
			return fmt.Errorf("%w: remote Git identity does not match the saved source", ErrInvalidPlan)
		}
	}
	if source.Mode == SourceModeImageReference {
		repository, err := normalizeImageReference(source.Image)
		if err != nil || identity.Repository != repository ||
			(detection.Unavailable == "" && !contentDigestRE.MatchString(identity.Digest)) {
			return fmt.Errorf("%w: image identity does not match the saved source", ErrInvalidPlan)
		}
	}
	if source.Kind == SourceCompose {
		if detection.Compose == nil || identity.Digest == "" || detection.Compose.Digest != identity.Digest ||
			!validateComposeAnalysis(*detection.Compose, identity) {
			return fmt.Errorf("%w: Compose detection evidence is malformed", ErrInvalidPlan)
		}
	} else if detection.Compose != nil {
		return fmt.Errorf("%w: Compose evidence is not valid for this source", ErrInvalidPlan)
	}
	if identity.IncludeSubmodules != source.IncludeSubmodules || identity.IncludeLFS != source.IncludeLFS {
		return fmt.Errorf("%w: Git materialization choices do not match the saved source", ErrInvalidPlan)
	}
	if err := rejectPlanConfigSecrets("observed source evidence", identity.Observed); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidPlan, err)
	}
	ids := map[string]bool{}
	selected := detection.SelectedID == ""
	for _, candidate := range detection.Candidates {
		if candidate.ID == "" || len(candidate.ID) > 128 || ids[candidate.ID] ||
			!validBuildMethod(candidate.BuildMethod) || !validProfile(candidate.Profile) ||
			!validDetectionConfidence(candidate.Confidence) || candidate.Name == "" || len(candidate.Name) > 256 ||
			(candidate.Root != "" && !safeRelativePath(candidate.Root)) || len(candidate.Root) > 4096 ||
			len(candidate.Framework) > 128 || len(candidate.Recipe) > 32 ||
			(candidate.Recipe != "" && !validRecipe(candidate.Recipe)) ||
			len(candidate.PythonVersion) > 16 || (candidate.PythonVersion != "" && !pythonRecipeVersionRE.MatchString(candidate.PythonVersion)) ||
			len(candidate.Variables) > 64 || len(candidate.Databases) > 8 ||
			len(candidate.BuildCommand) > 4096 ||
			len(candidate.StartCommand) > 4096 || len(candidate.OutputDirectory) > 4096 ||
			len(candidate.Dockerfile) > 4096 || (candidate.Dockerfile != "" && !safeRelativePath(candidate.Dockerfile)) ||
			len(candidate.GoVersion) > 32 || (candidate.GoVersion != "" && !goRecipeVersionRE.MatchString(candidate.GoVersion)) || len(candidate.RecipeIssue) > 512 ||
			len(candidate.GoMinimumVersion) > 32 || (candidate.GoMinimumVersion != "" && !stableGoVersionRE.MatchString(candidate.GoMinimumVersion)) ||
			len(candidate.GoToolchain) > 32 || strings.ContainsAny(candidate.GoToolchain, "\x00\r\n ") ||
			len(candidate.GoVersionFile) > 32 || strings.ContainsAny(candidate.GoVersionFile, "\x00\r\n") ||
			len(candidate.GoMainPackages) > goMainPackagesKept || slices.ContainsFunc(candidate.GoMainPackages, func(pkg string) bool { return !validGoPackagePath(pkg) }) ||
			candidate.GoMainPackagesOmitted < 0 ||
			(candidate.GoPackage != "" && !validGoPackagePath(candidate.GoPackage)) ||
			len(candidate.PythonRequires) > 128 || strings.ContainsAny(candidate.PythonRequires, "\x00\r\n") ||
			(candidate.PythonInstall != "" && !validPythonInstallKind(candidate.PythonInstall)) ||
			(candidate.PackageManager != "" && !validNodePackageManager(candidate.PackageManager)) || len(candidate.PackageManagers) > 4 ||
			slices.ContainsFunc(candidate.PackageManagers, func(manager string) bool { return !validNodePackageManager(manager) }) ||
			(candidate.OutputDirectory != "" && !validOutputDirectory(candidate.OutputDirectory)) ||
			candidate.Port < 0 || candidate.Port > 65535 ||
			len(candidate.Evidence) > 128 || len(candidate.NeedsDecision) > 128 {
			return fmt.Errorf("%w: detected candidate is malformed", ErrInvalidPlan)
		}
		if !validPersistentPaths(candidate.PersistentPaths) || len(candidate.SeedCommand) > 512 ||
			strings.ContainsAny(candidate.SeedCommand, "\x00\r\n") {
			return fmt.Errorf("%w: detected candidate state is malformed", ErrInvalidPlan)
		}
		for _, command := range []string{candidate.BuildCommand, candidate.StartCommand, candidate.RecipeIssue, candidate.SeedCommand} {
			if rejectPlanSecretLiteral("detected command", command) != nil {
				return fmt.Errorf("%w: detected candidate contains credential material", ErrInvalidPlan)
			}
		}
		if err := validateDetectedNodeInstall(candidate); err != nil {
			return err
		}
		if err := validateDetectedStaticSite(candidate); err != nil {
			return err
		}
		for _, label := range []string{candidate.Name, candidate.Framework, candidate.Recipe} {
			if rejectPlanSecretLiteral("detected label", label) != nil {
				return fmt.Errorf("%w: detected candidate contains credential material", ErrInvalidPlan)
			}
		}
		ids[candidate.ID] = true
		if candidate.ID == detection.SelectedID {
			selected = true
		}
		for _, evidence := range candidate.Evidence {
			if len(evidence.Path) > 4096 || len(evidence.Reason) > 512 ||
				strings.ContainsAny(evidence.Path, "\x00\r\n") ||
				rejectPlanSecretLiteral("detection evidence", evidence.Reason) != nil {
				return fmt.Errorf("%w: detection evidence is malformed", ErrInvalidPlan)
			}
		}
		for _, variable := range candidate.Variables {
			if ValidateEnvKey(variable.Name) != nil || len(variable.Example) > 256 ||
				(variable.Step != "" && variable.Step != "install") ||
				strings.ContainsAny(variable.Example, "\x00\r\n") ||
				rejectPlanSecretLiteral("detected variable example", variable.Example) != nil ||
				len(variable.Sources) > 8 {
				return fmt.Errorf("%w: detected variable is malformed", ErrInvalidPlan)
			}
			for _, source := range variable.Sources {
				if source == "" || len(source) > 4096 || strings.ContainsAny(source, "\x00\r\n") {
					return fmt.Errorf("%w: detected variable is malformed", ErrInvalidPlan)
				}
			}
		}
		for _, database := range candidate.Databases {
			if !validDetectedDatabaseEngine(database.Engine) || ValidateEnvKey(database.Variable) != nil ||
				len(database.Evidence) > 512 || strings.ContainsAny(database.Evidence, "\x00\r\n") ||
				rejectPlanSecretLiteral("detected database evidence", database.Evidence) != nil ||
				validateDetectedDatabaseDetails(database) != nil {
				return fmt.Errorf("%w: detected database is malformed", ErrInvalidPlan)
			}
		}
		if err := validateDetectedEnvironment(candidate); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidPlan, err)
		}
		for _, decision := range candidate.NeedsDecision {
			if decision == "" || len(decision) > 512 || strings.ContainsAny(decision, "\x00\r\n") ||
				rejectPlanSecretLiteral("detection decision", decision) != nil {
				return fmt.Errorf("%w: detection decision is malformed", ErrInvalidPlan)
			}
		}
		if err := validateDetectedServing(candidate); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidPlan, err)
		}
		if err := validateNetworkFacts(candidate); err != nil {
			return err
		}
		if err := validateCandidateImageFacts(candidate); err != nil {
			return err
		}
	}
	if len(detection.SelectionReason) > 512 || strings.ContainsAny(detection.SelectionReason, "\x00\r\n") ||
		rejectPlanSecretLiteral("selection reason", detection.SelectionReason) != nil {
		return fmt.Errorf("%w: detection selection reason is malformed", ErrInvalidPlan)
	}
	if !selected {
		return fmt.Errorf("%w: selected detection candidate does not exist", ErrInvalidPlan)
	}
	return validateRepoShapeEvidence(detection)
}

// validateDetectedNodeInstall bounds the lockfile readings and install
// plans a saved draft carries, the way every other detected field is: they
// are stored, revalidated when the draft returns, and shown to operators.
func validateDetectedNodeInstall(candidate DetectedCandidate) error {
	malformed := fmt.Errorf("%w: detected Node install is malformed", ErrInvalidPlan)
	text := func(value string, limit int) bool {
		return len(value) <= limit && !strings.ContainsAny(value, "\x00\r\n") &&
			rejectPlanSecretLiteral("detected install", value) == nil
	}
	names := func(list []string) bool {
		if len(list) > nodeListedNames {
			return false
		}
		for _, name := range list {
			if name == "" || !text(name, 512) {
				return false
			}
		}
		return true
	}
	if len(candidate.Lockfiles) > len(nodeLockfileNames) || len(candidate.NodeInstalls) > len(nodeManagerOrder) || !text(candidate.NodeVersion, 64) {
		return malformed
	}
	if build := candidate.NodeBuild; build != nil {
		if !text(build.EnvSchema, 256) || len(build.EnvServer) > 64 || len(build.EnvClient) > 64 || len(build.PrismaEnv) > 16 ||
			build.MemoryMiB < 0 || build.MemoryMiB > 65536 {
			return malformed
		}
		for _, name := range slices.Concat(build.EnvServer, build.EnvClient, build.PrismaEnv) {
			if ValidateEnvKey(name) != nil {
				return malformed
			}
		}
		if len(build.Findings) > 8 || len(build.DevScripts) > 32 || !validDetectedFindings(build.Findings, text) {
			return malformed
		}
		for name, label := range build.DevScripts {
			if !nodeScriptNameRE.MatchString(name) || len(name) > 64 || !text(label, 64) {
				return malformed
			}
		}
	}
	for _, lockfile := range candidate.Lockfiles {
		if nodeLockfileManager(lockfile.Path) == "" || nodeLockfileManager(lockfile.Path) != lockfile.Manager ||
			(lockfile.State != LockfileInSync && lockfile.State != LockfileStale && lockfile.State != LockfileUnknown) ||
			!names(lockfile.Missing) || !names(lockfile.Extra) || !names(lockfile.Changed) || !text(lockfile.Note, 512) {
			return malformed
		}
	}
	for _, install := range candidate.NodeInstalls {
		if !validNodePackageManager(install.Manager) || (install.Lockfile != "" && nodeLockfileManager(install.Lockfile) == "") ||
			!text(install.Install, 1024) || !text(install.Toolchain, 256) ||
			!text(install.BuildCommand, 4096) || !text(install.StartCommand, 4096) || len(install.Findings) > 32 ||
			!validDetectedFindings(install.Findings, text) {
			return malformed
		}
	}
	return nil
}

// validDetectedFindings bounds findings detection recorded on a candidate
// the way a saved draft revalidates them.
func validDetectedFindings(findings []PreflightFinding, text func(string, int) bool) bool {
	for _, finding := range findings {
		switch finding.Severity {
		case PreflightPass, PreflightWarning, PreflightDecision, PreflightBlocked, PreflightUnavailable:
		default:
			return false
		}
		if finding.Code == "" || !text(finding.Code, 64) || !text(finding.Title, 512) || !text(finding.Measured, 512) ||
			!text(finding.Means, 512) || !text(finding.Action, 512) || !text(finding.Owner, 64) ||
			!text(finding.FieldID, 256) || finding.DeepLink != "" {
			return false
		}
	}
	return true
}

var contentDigestRE = regexp.MustCompile(`^[a-z0-9][a-z0-9+._-]{0,31}:[0-9a-f]{32,128}$`)

// validGoPackagePath accepts a main package as the recipe names it: "." for
// the root, or a relative directory that stays inside it.
func validGoPackagePath(pkg string) bool {
	return pkg == "." || (len(pkg) <= 512 && safeRelativePath(pkg) && !strings.Contains(pkg, "\\"))
}

func validPythonInstallKind(kind string) bool {
	switch kind {
	case "uv.lock", "poetry.lock", "requirements.txt", "pyproject.toml":
		return true
	}
	return false
}

func validDetectionConfidence(confidence DetectionConfidence) bool {
	return confidence == ConfidenceHigh || confidence == ConfidenceMedium || confidence == ConfidenceLow
}

func validateComposeAnalysis(analysis ComposeAnalysis, identity SourceIdentity) bool {
	if !contentDigestRE.MatchString(analysis.Digest) || len(analysis.Files) == 0 || len(analysis.Files) > 16 ||
		len(analysis.Services) == 0 || len(analysis.Services) > 256 || len(analysis.Variables) > 256 ||
		len(analysis.Warnings) > 256 || len(analysis.Unsupported) > 256 || len(analysis.Preview) > 4<<20 {
		return false
	}
	if !equalStrings(analysis.Files, identity.ComposeFiles) {
		return false
	}
	services := make([]string, 0, len(analysis.Services))
	seenServices := map[string]bool{}
	for _, service := range analysis.Services {
		if !validComposeServiceName(service.Name) || seenServices[service.Name] ||
			len(service.Image) > 512 || len(service.BuildContext) > 4096 || len(service.BuildDockerfile) > 4096 ||
			len(service.Ports) > 256 || len(service.Mounts) > 256 || len(service.Advanced) > 64 {
			return false
		}
		if service.BuildContext != "" && !strings.Contains(service.BuildContext, "$") &&
			service.BuildContext != "." && !safeRelativePath(service.BuildContext) {
			return false
		}
		if service.BuildDockerfile != "" && !strings.Contains(service.BuildDockerfile, "$") &&
			!safeRelativePath(service.BuildDockerfile) {
			return false
		}
		seenServices[service.Name] = true
		services = append(services, service.Name)
		for _, values := range [][]string{service.Ports, service.Mounts, service.Advanced} {
			for _, value := range values {
				if value == "" || len(value) > 4096 || strings.ContainsAny(value, "\x00\r\n") {
					return false
				}
			}
		}
	}
	sort.Strings(services)
	if !equalStrings(services, identity.Services) || !validComposeBuildEvidence(analysis) {
		return false
	}
	for _, variable := range analysis.Variables {
		if ValidateEnvKey(variable) != nil {
			return false
		}
	}
	for _, values := range [][]string{analysis.Warnings, analysis.Unsupported} {
		for _, value := range values {
			if value == "" || len(value) > 512 || strings.ContainsAny(value, "\x00\r\n") ||
				rejectPlanSecretLiteral("Compose evidence", value) != nil {
				return false
			}
		}
	}
	return true
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

var platformRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}/[a-z0-9][a-z0-9._-]{0,63}(?:/[a-z0-9][a-z0-9._-]{0,63})?$`)

func validPlatform(platform string) bool { return platformRE.MatchString(platform) }

// buildPlanDigest identifies a build plan for pending-change and release
// comparison. Framework is left out, so recording or dropping the name
// detection gave the source never makes a plan differ from the live one.
func buildPlanDigest(build BuildPlanConfig) string {
	build.Framework = ""
	encoded, _ := json.Marshal(build)
	return digestBytes(encoded)
}

// detectedFramework is the framework a committed plan records: the chosen
// candidate's, while the build still reads that candidate's directory.
// Without a chosen candidate the plan keeps what it carries, which only a
// server-side copy such as a duplicate can have put there.
func detectedFramework(detection *DetectionResult, build BuildPlanConfig) string {
	candidate := selectedDetectionCandidate(detection)
	if candidate == nil {
		return build.Framework
	}
	if !sameBuildRoot(candidate.Root, build.RootDirectory) {
		return ""
	}
	return candidate.Framework
}

// carriedFramework keeps a stored framework on a saved build plan only while
// the plan builds the same way from the same directory. Once the method,
// recipe or root changes, detection no longer describes what is built.
func carriedFramework(previous, next BuildPlanConfig) string {
	if previous.Method != next.Method || previous.Recipe != next.Recipe ||
		!sameBuildRoot(previous.RootDirectory, next.RootDirectory) {
		return ""
	}
	return previous.Framework
}

func sameBuildRoot(left, right string) bool {
	return path.Clean("/"+left) == path.Clean("/"+right)
}

// sameSourceLocation says two source configurations point at the same code.
// A new branch or credential still builds the app detection recognised; a
// new repository, directory or image may not.
func sameSourceLocation(left, right DraftSourceConfig) bool {
	return left.URL == right.URL && left.Provider == right.Provider &&
		left.ProviderBaseURL == right.ProviderBaseURL && left.Repository == right.Repository &&
		left.LocalPath == right.LocalPath && left.Subdirectory == right.Subdirectory &&
		left.Image == right.Image
}

func canonicalConfiguration(c PlanConfiguration) PlanConfiguration {
	// A stage belongs to the Dockerfile it was detected in; switching the
	// build method away must not leave a plan that validation refuses.
	if c.Build.Method != BuildDockerfile {
		c.Build.Target = ""
	}
	if c.Build.Method != BuildCompose {
		c.Build.PrimaryService = ""
	}
	if c.Build.Secrets == nil {
		c.Build.Secrets = []BuildSecretConfig{}
	}
	if c.Build.ReleaseTasks == nil {
		c.Build.ReleaseTasks = []ReleaseTaskConfig{}
	}
	for index := range c.Build.ReleaseTasks {
		if c.Build.ReleaseTasks[index].Env == nil {
			c.Build.ReleaseTasks[index].Env = []string{}
		}
	}
	if c.Variables == nil {
		c.Variables = []PlannedVariable{}
	}
	if c.Dependencies == nil {
		c.Dependencies = []PlannedDependency{}
	}
	if c.Checks == nil {
		c.Checks = []PlannedCheck{}
	}
	if c.Domains == nil {
		c.Domains = []PlannedDomain{}
	}
	if c.Runtime.Command == nil {
		c.Runtime.Command = []string{}
	}
	if c.Runtime.Capabilities == nil {
		c.Runtime.Capabilities = []string{}
	}
	if c.Runtime.Devices == nil {
		c.Runtime.Devices = []string{}
	}
	if c.Runtime.Mounts == nil {
		c.Runtime.Mounts = []RuntimeMount{}
	}
	if c.Runtime.Ports == nil {
		c.Runtime.Ports = []PublishedPort{}
	}
	sort.SliceStable(c.Runtime.Ports, func(i, j int) bool {
		left, right := c.Runtime.Ports[i], c.Runtime.Ports[j]
		if left.effectiveProtocol() != right.effectiveProtocol() {
			return left.effectiveProtocol() < right.effectiveProtocol()
		}
		return left.HostPort < right.HostPort
	})
	sort.Slice(c.Variables, func(i, j int) bool { return c.Variables[i].Name < c.Variables[j].Name })
	sort.Slice(c.Dependencies, func(i, j int) bool {
		left := c.Dependencies[i].Kind + "\x00" + c.Dependencies[i].ResourceKind + "\x00" + c.Dependencies[i].ResourceID
		right := c.Dependencies[j].Kind + "\x00" + c.Dependencies[j].ResourceKind + "\x00" + c.Dependencies[j].ResourceID
		return left < right
	})
	sort.Slice(c.Checks, func(i, j int) bool { return c.Checks[i].Name < c.Checks[j].Name })
	for index := range c.Domains {
		c.Domains[index].Hostname = strings.ToLower(strings.TrimSpace(c.Domains[index].Hostname))
		c.Domains[index].Protection = canonicalDomainProtection(c.Domains[index].Protection)
	}
	sort.Slice(c.Domains, func(i, j int) bool { return c.Domains[i].Hostname < c.Domains[j].Hostname })
	return c
}

func canonicalSourceConfig(source DraftSourceConfig) DraftSourceConfig {
	if source.Ref == "" && (source.Mode == SourceModeGitURL || source.Mode == SourceModeConnectedRepository || source.Mode == SourceModeComposeGit) {
		source.Ref = "main"
	}
	if source.Mode == SourceModeGitURL || source.Mode == SourceModeComposeGit {
		if normalized, _, err := normalizeGitRemote(source.URL); err == nil {
			source.URL = normalized
		}
	}
	if source.Mode == SourceModeImageReference {
		if normalized, err := normalizeImageReference(source.Image); err == nil {
			source.Image = normalized
		}
	}
	source.Platform = strings.ToLower(strings.TrimSpace(source.Platform))
	if source.LocalPath != "" {
		source.LocalPath = filepath.Clean(source.LocalPath)
	}
	if source.Subdirectory != "" {
		source.Subdirectory = filepath.ToSlash(filepath.Clean(source.Subdirectory))
	}
	if source.ComposeFiles == nil {
		source.ComposeFiles = []ComposeDocument{}
	} else {
		source.ComposeFiles = append([]ComposeDocument(nil), source.ComposeFiles...)
		sort.SliceStable(source.ComposeFiles, func(i, j int) bool {
			if source.ComposeFiles[i].Order != source.ComposeFiles[j].Order {
				return source.ComposeFiles[i].Order < source.ComposeFiles[j].Order
			}
			return source.ComposeFiles[i].Path < source.ComposeFiles[j].Path
		})
	}
	return source
}
