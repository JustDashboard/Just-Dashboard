/*
 * Deployments: projects and their configuration, sources, builds, releases
 * and runs, previews and pull requests, blueprints, smart stacks and game
 * servers, secret stores, rollback and data copies, image scans, sleep, the
 * request log and traffic of an app, and the notification channels its runs
 * announce on.
 */

import type { ForwardAuthProvider } from "../proxy/types-siteform"
import type { TailscaleIdentity } from "./dashboard"
import type { DbDriver } from "./database"
import type { DockerEvent } from "./docker"
import type { GitPullRequest } from "./git"

export type DeployProject = {
  id: number
  name: string
  profile: WorkloadProfile
  repoPath: string
  branch: string
  composeFile: string
  preCommand?: string
  postCommand?: string
  hookId: string
  enabled: boolean
  createdAt: string
  updatedAt: string
  archivedAt?: string
  hookUrl?: string
  currentSha?: string
  currentRef?: string
  dirty?: boolean
  lastRun?: DeployRun
  envVarCount: number
}

/** What an archived deployment's plan recorded; absent for a legacy project. */
export type DeploymentFacts = {
  sourceKind?: DeploymentSourceKind
  sourceRef?: string
  sourceRepository?: string
  sourceTemplate?: string
  buildMethod?: DeploymentBuildMethod
  recipe?: DeploymentRecipe
  framework?: string
}

/** A row of GET /deploy/?view=archived: the project record and what it deployed. */
export type ArchivedDeployment = DeployProject & DeploymentFacts

export type WorkloadProfile =
  "web" | "static" | "worker" | "image" | "compose" | "service" | "game" | "imported"

export type DeploymentSourceKind = "git" | "local" | "image" | "compose" | "blueprint" | "import"

export type DeploymentSourceMode =
  | "git_url"
  | "connected_repository"
  | "local_checkout"
  | "image_reference"
  | "compose_paste"
  | "compose_upload"
  | "compose_git"
  | "compose_local"
  | "blueprint"
  | "existing_checkout"
  | "existing_container"
  | "existing_stack"

export type DeploymentRunState =
  | "requested"
  | "validating"
  | "queued"
  | "preparing"
  | "running"
  | "verifying"
  | "activating"
  | "failed_activation"
  | "restoring_previous"
  | "cancelling"
  | "succeeded"
  | "failed"
  | "cancelled"
  | "rolled_back"
  | "superseded"

export type DeploymentStepState =
  | "pending"
  | "blocked"
  | "running"
  | "passed"
  | "warning"
  | "failed"
  | "skipped"
  | "cancelled"
  | "unavailable"

export type DeploymentEngineRun = {
  id: number
  runNumber: number
  sourceRevision?: string
  projectId: number
  environmentId: number
  state: DeploymentRunState
  operation: string
  trigger: string
  actor: string
  requestedAt: string
  queuedAt?: string
  claimedAt?: string
  heartbeatAt?: string
  endedAt?: string
  cancelRequested: boolean
  supersededBy?: number
  retryOfRunId?: number
  planRevision: number
  releaseId?: number
  candidateReleaseId?: number
  terminalCode?: string
  terminalReason?: string
  priority: number
  slotClass: "light" | "heavy"
  metadata: Record<string, unknown>
  /** Where a run in flight stands; set by the list reads, absent once it ends. */
  currentStep?: DeploymentCurrentStep
}

/** The step a run in flight is at: running, else blocking, else failed, else next. */
export type DeploymentCurrentStep = {
  key: string
  label: string
  state: DeploymentStepState
}

export type DeploymentStep = {
  id: number
  runId: number
  key: string
  ordinal: number
  state: DeploymentStepState
  attempt: number
  timeoutSeconds: number
  startedAt?: string
  endedAt?: string
  evidence: Record<string, unknown>
  errorCode?: string
  errorMessage?: string
  lastSeq: number
}

/**
 * The Build step's evidence: the image it made and how it was prepared, read
 * back from `DeploymentStep.evidence`. Everything is optional because a failed
 * build, or one recorded before a field existed, left less behind.
 */
export type DeploymentBuildEvidence = {
  result?: {
    image?: {
      reference: string
      digest: string
      sizeBytes?: number
      os?: string
      architecture?: string
      platforms?: string[]
    }
    prepared?: {
      method: DeploymentBuildMethod
      recipe?: string
      recipeVersion?: string
      /** The language release the recipe built with: "rust 1.85", "java 21 (maven)". */
      toolchain?: string
      /** The Node major a JavaScript build ran on and what chose it: "22 (.nvmrc)". */
      nodeVersion?: string
      baseImages?: { reference: string; digest: string }[]
      dockerfilePreview?: string
      dockerfileDigest?: string
      targetPlatform?: string
      cachePolicy?: string
      railpack?: RailpackEvidence
    }
  }
  /** Where the build's time went, read from BuildKit's lines; absent when it ran none. */
  timing?: DeploymentBuildTiming
}

/**
 * Where a build's time went (`BuildTiming`, `backend/internal/deploy/build_timing.go`):
 * BuildKit's own seconds for the install and the build command, the image's
 * export and unpack, and how many of the Dockerfile's steps the cache answered.
 */
export type DeploymentBuildTiming = {
  installSeconds?: number
  installCached?: boolean
  compileSeconds?: number
  compileCached?: boolean
  exportSeconds?: number
  unpackSeconds?: number
  imageSeconds?: number
  steps: number
  cachedSteps: number
  imageBytes?: number
}

/**
 * What a failed step's own output proved about why it failed, read from its
 * evidence: `cause` on a failed build or release task, `diagnostics.cause` on
 * a failed health gate. The code is also the step's error code and the run's
 * terminal code; the subjects are identifiers the output named, never a line
 * of it, and `lineSeq` is the transcript line that proves it.
 */
export type DeploymentFailureCause = {
  code: string
  phase?:
    "install" | "build" | "output_check" | "setup" | "dockerfile" | "base_image" | "pull" | "plan"
  command?: string
  exitCode?: number
  subjects?: string[]
  /** What the code is about when it covers several tools: "package-lock.json", "go". */
  detail?: string
  lineSeq?: number
  /** The Compose service whose build failed. */
  service?: string
  /** A schema_missing cause names its table here. */
  table?: string
  fix?: DeploymentCauseFix
}

/**
 * The one plan change a cause's evidence supports. `set_build` and
 * `set_runtime` replace the field with `value`; `add_variable` creates the
 * variable in `scope`; `variable_scope` adds `scope` to an existing one;
 * `review` opens a field whose right value the output cannot prove;
 * `dns_record` opens a domain's DNS record sheet on Domains settings.
 */
export type DeploymentCauseFix = {
  kind:
    | "set_build"
    | "set_runtime"
    | "add_variable"
    | "variable_scope"
    | "remove_variable_scope"
    | "review"
    | "dns_record"
  /** `configuration.build.packageManager`, `runtime.internalPort`, `variables.NAME`, `dependencies`, `domains.HOST`. */
  field: string
  value?: string
  scope?: string
}

/** What changed in the environment's settings since a run was planned. */
export type DeploymentRunSettingsDrift = {
  runId: number
  planRevision: number
  desiredRevision: number
  changed: boolean
  changes: DeploymentSettingsChange[]
}

/**
 * One changed setting. `before`/`after` are plan values that are not secret
 * — a package manager, a port, a command — or, for a variable, its scopes.
 */
export type DeploymentSettingsChange = {
  kind: "build" | "runtime" | "source" | "variable"
  field: string
  change: "changed" | "added" | "removed" | "scope"
  before?: string
  after?: string
}

/**
 * `GET`/`POST /deploy/{id}/runs/{run}/diagnosis`: the last answer the
 * assistant's provider gave about a failed run, kept with it.
 */
export type DeploymentRunDiagnosis = {
  runId: number
  provider: "anthropic" | "openai"
  model: string
  modelLabel: string
  answer: {
    cause?: string
    /** Transcript lines the answer quoted, as the transcript has them. */
    evidence: { seq: number; text: string }[]
    steps: string[]
    /** Lines it quoted that the run never printed, which are left out. */
    unmatched?: number
    /** The whole answer, for one that kept to no shape. */
    text: string
  }
  /** Exactly what went to the provider, already masked. */
  sent: string
  sentLines: number
  masked: number
  createdBy: string
  createdAt: string
}

export type DeploymentRunSnapshot = {
  run: DeploymentEngineRun
  steps: DeploymentStep[]
}

export type DeploymentRunEvent = {
  seq: number
  type: string
  runId: number
  stepId?: number
  ts: string
  data: Record<string, unknown>
}

export type DeploymentRuntimeService = {
  containerId: string
  name: string
  releaseId: number
  liveRelease: boolean
  state: string
  health: string
  imageId: string
  stack?: string
  service?: string
  startedAt?: string
  /** The image reference the container was created from, as Docker reports it. */
  image?: string
  /** How often Docker has restarted it under its restart policy; absent when none. */
  restartCount?: number
  /** The last exit status, only for a container that is not running. */
  exitCode?: number
  /** The kernel killed it for exceeding its memory. */
  oomKilled?: boolean
  /** Which of its release's replicas it is, from 1; absent for a release of one container. */
  replica?: number
  /** How many replicas its release runs; absent for a release of one container. */
  replicas?: number
}

export type DeploymentRuntimeServices = {
  status: "available" | "unavailable"
  reason?: string
  observedAt: string
  services: DeploymentRuntimeService[]
}

/**
 * How the first person to open a deployed template gets in. The catalogue
 * declares it; the picker promises it before the deploy and the project's
 * overview makes good on it afterwards.
 */
export type BlueprintAccess = {
  /**
   * `unavailable` never reaches the picker — it is legal only on a retired
   * definition — but it does reach the project overview, because a deployment
   * already running one still resolves its definition on every redeploy.
   * `managed` is a stack's preset: the dashboard has set every application up
   * before anyone reached it, and the login is on the stack's page.
   */
  kind: "setup" | "credentials" | "token" | "client" | "open" | "managed" | "unavailable"
  username?: string
  usernameVariable?: string
  secretVariable?: string
  path?: string
  note: string
}

export type BlueprintSummary = {
  deploymentSupported?: boolean
  unavailableReason?: string
  id: string
  version: string
  access: BlueprintAccess
  name: string
  category: "http" | "database" | "tool" | "automation" | "game"
  profile: "web" | "database" | "tool" | "worker" | "game" | "compose"
  description: string
  iconId: string
  docsUrl: string
  license: string
  maintainer: string
  reviewedAt: string
  image: string
  memoryMb: number
  requiresAcceptance?: boolean
  privileged?: boolean
  /**
   * The source kind it deploys as: one reviewed image, or a multi-service
   * template that runs through the Compose release path — then `image` is
   * the public service's, `memoryMb` the whole stack's limits added up, and
   * `services` what the stack runs.
   */
  sourceKind?: "blueprint" | "compose"
  services?: BlueprintServiceSummary[]
  /**
   * Shipped with the dashboard, or added on it — a template from a Compose
   * file, a deployment saved as one, or an imported file. An added
   * template's id is always `user-<name>`, so it never stands in for a
   * built-in.
   */
  origin?: "builtin" | "user"
  /**
   * The smart stack this template is a preset of (`media`). A stack's
   * templates are offered on the Stacks tab with what the stack sets up, and
   * left out of the template catalogue.
   */
  stack?: string
}

/** How a template added on this dashboard was made. */
export type DeploymentTemplateSource = "compose" | "deployment" | "import"

/** One template added on this dashboard, as `GET /deploy/templates` lists it. */
export type UserTemplate = {
  id: string
  name: string
  version: string
  source: DeploymentTemplateSource
  /** The deployment a template was saved from; it may have been removed since. */
  fromDeploymentId?: number
  summary: BlueprintSummary
  /** How many deployments' sources name it. Each runs its own copy. */
  deployments: number
  createdBy: string
  createdAt: string
  updatedBy: string
  updatedAt: string
}

export type UserTemplateDetail = UserTemplate & { definition: BlueprintDetail }

/** A dry run's answer: the definition a file or a deployment makes, not stored. */
export type TemplatePreview = {
  definition: BlueprintDetail
  summary: BlueprintSummary
  /** Whether a template already has this id, so saving would be refused. */
  exists: boolean
}

export type BlueprintServiceRole = "app" | "worker" | "database" | "cache"

/** One service of a multi-service template as the catalogue lists it. */
export type BlueprintServiceSummary = {
  name: string
  role: BlueprintServiceRole
  purpose: string
  image: string
  memoryMb: number
  /** The one service routed to the deployment's domain. */
  public?: boolean
}

/** One service as the definition declares it, on the detail endpoint. */
export type BlueprintService = {
  name: string
  role: BlueprintServiceRole
  purpose: string
  image: { reference: string; tagPolicy: string; command?: string[] }
  ports?: {
    name: string
    internal: number
    protocol: string
    purpose: string
    /** `loopback` is a stack's private web interface, published on 127.0.0.1 only. */
    exposure: "proxy" | "internal" | "loopback"
    primary?: boolean
  }[]
  volumes?: { name: string; target: string; purpose: string; data: boolean; backup: boolean }[]
  resources: { memoryMb: number; minMemoryMb: number; cpus?: number }
  dependsOn?: { service: string; condition: "healthy" | "started" | "completed" }[]
  /** A private service that may reach the internet. */
  egress?: boolean
}

export type BlueprintChoice = {
  value: string
  label: string
  description?: string
  image?: string
}

export type BlueprintInput = {
  name: string
  kind: "text" | "secret" | "number" | "boolean" | "choice" | "domain" | "memory" | "accept"
  label: string
  description?: string
  default?: string
  required?: boolean
  advanced?: boolean
  minimum?: number
  maximum?: number
  pattern?: string
  choices?: BlueprintChoice[]
  variable?: string
  acceptUrl?: string
}

export type BlueprintProperty = {
  key: string
  kind: "text" | "number" | "boolean" | "choice"
  label: string
  description?: string
  default?: string
  minimum?: number
  maximum?: number
  choices?: BlueprintChoice[]
}

export type BlueprintConfigFile = {
  path: string
  label: string
  format: "properties" | "yaml" | "json" | "toml" | "raw"
  description?: string
  restartRequired?: boolean
  properties?: BlueprintProperty[]
}

export type BlueprintAutomation = {
  name: string
  description: string
  cron: string
  timezone?: string
  actions: string[]
  default?: boolean
}

/**
 * The detail endpoint answers with the definition itself, not the listing
 * row: `image` is the pinned runtime object, and the fields the summary
 * flattens out of provenance and resources are not repeated at the top level.
 */
export type BlueprintDetail = Omit<
  BlueprintSummary,
  | "image"
  | "memoryMb"
  | "license"
  | "maintainer"
  | "reviewedAt"
  | "requiresAcceptance"
  | "privileged"
  | "sourceKind"
  | "services"
> & {
  retired?: string
  image: {
    reference: string
    tagPolicy: string
    platforms?: string[]
    pullPolicy?: string
    command?: string[]
  }
  provenance: {
    maintainer: string
    license: string
    upstreamUrl: string
    reviewedAt: string
    minimumDashboard: string
    updateNotes?: string
  }
  inputs?: BlueprintInput[]
  secrets?: {
    name: string
    variable: string
    label: string
    description?: string
    length: number
  }[]
  ports?: {
    name: string
    internal: number
    protocol: string
    purpose: string
    exposure: "proxy" | "direct" | "internal"
    primary?: boolean
  }[]
  volumes?: { name: string; target: string; purpose: string; data: boolean; backup: boolean }[]
  resources: { memoryMb: number; minMemoryMb: number; cpus?: number }
  files?: BlueprintConfigFile[]
  automation?: BlueprintAutomation[]
  update: { detector: string; versionSource?: string; notes?: string; backupFirst?: boolean }
  /** A multi-service template's services; the top-level image is then empty. */
  services?: BlueprintService[]
}

/** How a stack's service is reached: on the deployment's domain, on this server only, or not at all. */
export type SmartStackAccess = "public" | "private" | "worker"

/** One service of a stack as the catalogue describes it. */
export type SmartStackCatalogueService = {
  name: string
  title: string
  purpose: string
  port?: number
  access: SmartStackAccess
  docsUrl?: string
  /** The pinned reference the preset's template runs. */
  image: string
  /**
   * The DNS label its own address is made with beside the stack's domain
   * (`media-requests.example.com` for `requests`). Empty for the primary
   * service, which answers on the domain itself, and for a worker.
   */
  address?: string
  /** The service the stack's domain itself serves: Jellyfin's. */
  primary?: boolean
}

/** One preset of a stack: a reviewed Compose template and what it runs. */
export type SmartStackPreset = {
  id: string
  title: string
  description: string
  /** The blueprint a draft of this preset deploys. */
  templateId: string
  templateVersion: string
  /** Service names, in order. */
  services: string[]
  recommended?: boolean
  /** Said before Deploy and again on the stack's page. */
  limitations?: string[]
  /** The services' memory limits added up. */
  memoryMb: number
  /** An estimate for application data, not for media. */
  diskGb: number
}

/** One smart stack, as `GET /deploy/stacks` lists it. */
export type SmartStackCatalogueEntry = {
  id: string
  version: string
  title: string
  description: string
  presets: SmartStackPreset[]
  services: SmartStackCatalogueService[]
}

export type SmartStackStatus = "waiting" | "configuring" | "ready" | "action_required" | "failed"

export type SmartStackStage = "readiness" | "initialize" | "link" | "verify"

export type SmartStackStepStatus =
  "pending" | "running" | "done" | "failed" | "action_required" | "skipped"

export type SmartStackServiceStatus =
  "ok" | "configuring" | "action_required" | "failed" | "waiting" | "skipped"

export type SmartStackReadiness = {
  /** A release is live. */
  installed: boolean
  /** Every service's container is running and healthy. */
  healthy: boolean
  /** Every applicable link step is done. */
  connected: boolean
  /** It can fetch new titles. */
  acquisition: boolean
  /** Jellyfin is set up with its libraries. */
  playback: boolean
}

export type SmartStackService = {
  name: string
  title: string
  purpose: string
  access: SmartStackAccess
  image: string
  docsUrl?: string
  /**
   * The loopback address Docker published the service on. Kept in the state,
   * but the page no longer leads with it: every app answers on `url`.
   */
  address?: string
  /** The service's public URL, when the live release serves one. Jellyfin's is the primary domain. */
  url?: string
  /** Absent when no container exists. */
  container?: { name: string; state: string; health?: string }
  status: SmartStackServiceStatus
}

export type SmartStackStep = {
  id: string
  service: string
  title: string
  stage: SmartStackStage
  status: SmartStackStepStatus
  attempts: number
  /** What it did, or why it was skipped. */
  message?: string
  /** The last error, redacted by the server. */
  error?: string
  /** What the step said in its last pass (a tool's report), redacted as the error is. */
  log?: string[]
  startedAt?: string
  finishedAt?: string
}

/** Something only the person can do; a blocking one keeps the stack from ready. */
export type SmartStackAction = {
  id: string
  service: string
  title: string
  description: string
  blocking: boolean
}

/** An account the stack created. Its value is read through the reveal route. */
export type SmartStackCredential = {
  name: string
  service: string
  label: string
  username?: string
  /** False until the step that creates it has run. */
  stored: boolean
}

/** The one login every app of a stack signs in with. Its password is read through the reveal route. */
export type SmartStackLogin = {
  username: string
  /** False until the stack's first pass has made it. */
  stored: boolean
}

/** A service of the stack that has no address of its own yet, and the name it would get. */
export type SmartStackMissingAddress = {
  service: string
  title: string
  /** Empty while the deployment has no primary domain to make it from. */
  hostname: string
}

/** The stack's addresses: its primary domain, and the services that have none beside it. */
export type SmartStackAddresses = {
  /** Empty when the deployment has no primary domain. */
  primary: string
  missing: SmartStackMissingAddress[]
  /**
   * Saved among the deployment's domains but not served by the live release,
   * because the release that was to publish them failed. The same press
   * deploys again. Absent from a server before it said so.
   */
  unpublished?: SmartStackMissingAddress[]
}

/** What `POST /deploy/{id}/stack/addresses` started: the release that serves the new names. */
export type SmartStackAddressesStarted = {
  runId: number
  domains: string[]
}

/** An app the stack's template gained that the deployment does not run yet. */
export type SmartStackUpgradeService = {
  service: string
  title: string
  purpose: string
}

/**
 * What the stack's template gained since the deployment was made: the apps the
 * dashboard now ships with it and the live release does not run.
 */
export type SmartStackUpgrade = {
  template: string
  /** The template version this dashboard ships. */
  version: string
  /** The version the deployment runs. */
  from: string
  added: SmartStackUpgradeService[]
  /**
   * The source already names `version`, but the live release is older: the
   * release that was to bring the apps failed, has not run or is running.
   */
  saved: boolean
}

/** What `POST /deploy/{id}/stack/upgrade` started: the release that brings the new apps. */
export type SmartStackUpgradeStarted = {
  runId: number
  version: string
  added: string[]
}

/** How open a media source is, in Prowlarr's words. */
export type SmartStackSourcePrivacy = "public" | "semiPrivate" | "private"

/** A media source Prowlarr holds, as `GET /deploy/{id}/stack/sources` lists it. */
export type SmartStackSource = {
  id: number
  name: string
  /** The definition it was made from. */
  definition: string
  privacy: SmartStackSourcePrivacy
  protocol: string
  enabled: boolean
}

/** One of the sources Prowlarr knows how to add, as the catalogue lists it. */
export type SmartStackSourceDefinition = {
  definition: string
  name: string
  description: string
  privacy: SmartStackSourcePrivacy
  language: string
  /** Some field has no usable default, so the person has to fill it in. */
  needsInput: boolean
}

export type SmartStackSourceCatalogue = {
  definitions: SmartStackSourceDefinition[]
  fetchedAt: string
}

export type SmartStackSourceFieldType = "text" | "password" | "number" | "checkbox" | "select"

export type SmartStackSourceField = {
  name: string
  label: string
  help?: string
  type: SmartStackSourceFieldType
  value?: string | number | boolean | null
  options?: { value: string | number; label: string }[]
  advanced: boolean
}

/** One definition with its fields, as `GET …/sources/catalogue/{definition}` answers. */
export type SmartStackSourceDetail = {
  definition: string
  name: string
  description: string
  privacy: SmartStackSourcePrivacy
  urls: string[]
  fields: SmartStackSourceField[]
}

/** What `POST …/sources` answers when Prowlarr tested and kept the source. */
export type SmartStackSourceAdded = {
  source: SmartStackSource
  /** Radarr and Sonarr have it too. */
  synced: boolean
}

/** A stack project's state, as `GET /deploy/{id}/stack` answers. */
export type SmartStackState = {
  stackId: string
  title: string
  version: string
  preset: string
  presetTitle: string
  status: SmartStackStatus
  /** The one sentence the page leads with. */
  summary: string
  readiness: SmartStackReadiness
  services: SmartStackService[]
  steps: SmartStackStep[]
  actions: SmartStackAction[]
  credentials: SmartStackCredential[]
  /** The one login every app signs in with. Absent from a server older than it. */
  login?: SmartStackLogin
  /** The stack's addresses. Absent from a server older than them. */
  addresses?: SmartStackAddresses
  /** The apps the stack's template gained; absent when there are none. */
  upgrade?: SmartStackUpgrade
  limitations: string[]
  liveReleaseId?: number
  configuredReleaseId?: number
  startedAt?: string
  finishedAt?: string
  updatedAt: string
}

export type GameImportPreview = {
  root: string
  edition: string
  software: string
  version?: string
  evidence: { path: string; reason: string }[]
  worldPaths: string[]
  modPaths: string[]
  configPaths: string[]
  logPaths: string[]
  ignoredPaths: string[]
  properties?: Record<string, string>
  port?: number
  totalBytes: number
  eulaAccepted: boolean
  warnings: string[]
}

export type GameVersionList = {
  status: "available" | "stale" | "unavailable"
  reason?: string
  source: string
  checkedAt: string
  versions: {
    id: string
    kind: string
    releasedAt?: string
    recommended?: boolean
    latest?: boolean
  }[]
  recommended?: string
}

export type GamePlayers = {
  supported: boolean
  status: "available" | "unavailable" | "unsupported"
  reason?: string
  online: number
  maximum: number
  names: string[]
  observedAt: string
}

/**
 * Where a game server's players connect, read from the live release: the
 * port and protocol, the bind address, and what the host firewall does with
 * it. `firewall` is "admits" (an allow rule matches), "drops" (on, refusing
 * by default, nothing matches), "off", "open" (accepts by default) or
 * "unknown" with a reason.
 */
export type GameReach = {
  port: number
  /** One number on both for a server that signals over TCP and plays over UDP. */
  protocol: "tcp" | "udp" | "tcp+udp"
  bind: string
  public: boolean
  firewall: "admits" | "drops" | "off" | "open" | "unknown"
  reason?: string
  editable?: boolean
}

export type GameOverview = {
  status: "available" | "unavailable"
  reason?: string
  blueprintId?: string
  edition?: string
  containerId?: string
  address?: string
  players?: GamePlayers
  console: boolean
  /** Why the game has no console of its own (Bedrock has no RCON). */
  consoleReason?: string
  reach?: GameReach
  files: BlueprintConfigFile[]
}

export type GameConsoleResult = {
  command: string
  output: string
  exitCode: number
  executedAt: string
}

export type GameProperties = {
  status: "available" | "unavailable"
  reason?: string
  path?: string
  raw?: string
  values?: Record<string, string>
  known: BlueprintProperty[]
  restartRequired?: boolean
}

export type DeploymentDomainRoute = {
  hostname: string
  https: boolean
  ownership: DeploymentOwnership
  route: "served" | "missing" | "foreign" | "conflict" | "unavailable"
  servedBy?: string
  certificate: "valid" | "expiring" | "expired" | "missing" | "not requested" | "unavailable"
  certificateName?: string
  certificateDaysLeft?: number
  /** Who issued the covering certificate, read from the certificate itself. */
  certificateIssuer?: string
  /**
   * The copy a Docker Caddy release kept covers the name. Caddy renews what it
   * serves and never the copy, so there are no days left to show.
   */
  certificateRenewedBy?: "caddy"
  deepLink?: string
  certificateLink?: string
  protected?: boolean
  /**
   * Set on a name under the site's app-domain zone: `detached` when this server no longer holds
   * its handle, so the name does not resolve. Read only through `appDomainStateOf`
   * (`lib/app-domain.ts`), the one place that decides it.
   */
  appDomain?: { status: "active" | "detached" }
}

export type DeploymentStorageMount = {
  source: string
  target: string
  kind: "volume" | "bind"
  readOnly?: boolean
  ownership: DeploymentOwnership
  status: "present" | "missing" | "unavailable"
  detail?: string
  deepLink?: string
}

export type DeploymentBackupJob = {
  resourceId: string
  required: boolean
  status: "present" | "missing" | "unavailable"
  lastStatus?: string
  fresh: boolean
  detail?: string
  deepLink?: string
}

export type DeploymentDependencyItem = {
  kind: string
  resourceKind: string
  resourceId: string
  available: boolean
  fresh?: boolean
  status?: string
  detail?: string
  deepLink?: string
}

export type DeploymentOperations = {
  observedAt: string
  releaseId?: number
  evidence: "release" | "none"
  reason?: string
  runtime: DeploymentRuntimeServices
  domains: {
    status: "available" | "unavailable"
    reason?: string
    siteName?: string
    domains: DeploymentDomainRoute[]
  }
  storage: {
    status: "available" | "unavailable"
    reason?: string
    mounts: DeploymentStorageMount[]
  }
  backups: {
    status: "available" | "unavailable"
    reason?: string
    jobs: DeploymentBackupJob[]
  }
  dependencies: {
    status: "available" | "unavailable"
    reason?: string
    items: DeploymentDependencyItem[]
  }
  /** The tailnet address and who may open it, for a release that is tailnet-only. */
  tailnet?: DeploymentTailnetAddress
}

/** A tailnet-only environment as its live release and its address record have it. */
export type DeploymentTailnetAddress = {
  url?: string
  port?: number
  /** The mapping is in place for the live release. */
  published: boolean
  /** Who may open it; empty is everyone on the tailnet. */
  allow: string[]
  /** The proxy that asks who every visitor is, for a release that names them. */
  gate?: "nginx" | "caddy"
  gateRunning?: boolean
}

export type ReleaseFieldChange = {
  field: string
  from: string
  to: string
  changed: boolean
}

export type ReleaseListChange = {
  name: string
  change: "added" | "removed" | "changed" | "unchanged"
  from?: string
  to?: string
  detail?: string
  secret?: boolean
}

export type ReleaseArtifactStatus = {
  kind: string
  reference: string
  digest: string
  sizeBytes: number
  state: string
  retained: boolean
  reason: string
  /** When release retention removed it from the server (`state` is "pruned"). */
  prunedAt?: string
}

export type ReleaseComparison = {
  fromReleaseId: number
  toReleaseId: number
  changes: Record<string, boolean>
  detail: {
    status: "available" | "unavailable"
    reason?: string
    fields: ReleaseFieldChange[]
    variables: ReleaseListChange[]
    dependencies: ReleaseListChange[]
    checks: ReleaseListChange[]
    domains: ReleaseListChange[]
  }
  artifacts: ReleaseArtifactStatus[]
  /** Each release's recorded data point, per linked database. */
  dataPoints?: { from: ReleaseDataPoint[]; to: ReleaseDataPoint[] }
}

export type ReleaseUpdateStatus = {
  status: "available" | "unavailable"
  reason?: string
  reference?: string
  state?: "current" | "outdated" | "unknown" | "local" | "pinned"
  localDigest?: string
  remoteDigest?: string
  checkedAt?: string
}

export type ReleaseComparisonResponse = {
  comparison: ReleaseComparison | null
  update: ReleaseUpdateStatus
  artifacts: ReleaseArtifactStatus[]
  reason?: string
}

export type DeploymentSummary = {
  id: number
  name: string
  profile: WorkloadProfile
  environmentId: number
  environmentName: string
  environmentKind: "production" | "staging" | "preview"
  desiredRevision: number
  liveReleaseId?: number
  livePlanRevision?: number
  strategy: DeploymentStrategy
  expectedDowntime: boolean
  sourceKind: DeploymentSourceKind
  buildMethod: DeploymentBuildMethod
  sourceRef?: string
  sourceRevision?: string
  endpoint?: string
  internalPort?: number
  hostPort?: number
  health: string
  pendingChanges: boolean
  /** The live release's runtime was stopped on purpose and waits for Start. */
  stopped?: boolean
  /** When the live release was put to sleep, while it is asleep by design and the next request wakes it. */
  asleepSince?: string
  /** Services in the live release's runtime: one, or a Compose stack's count. */
  serviceCount?: number
  lastRun?: DeploymentEngineRun
  activeRun?: DeploymentEngineRun
  updatedAt: string
  /** owner/name for Git; the reference itself for an image or template, whose sourceRef is empty. */
  sourceRepository?: string
  /** A Git source's remote without userinfo: `https://host/path` or `git@host:path`. */
  sourceRemote?: string
  /**
   * The reviewed template (`id@version`) the deployment was made from: a
   * blueprint source's definition, or the multi-service template a Compose
   * source was rendered from.
   */
  sourceTemplate?: string
  recipe?: DeploymentRecipe
  /** What detection recognised the source as (`nextjs`, `vite`, `django`…), recorded at creation. */
  framework?: string
  /** Image references the live release runs, one per distinct Compose service image. */
  images?: string[]
  /** The newest runs, newest first, at most 14; the first is `lastRun`. */
  recentRuns?: DeploymentRecentRun[]
}

/** One square of a deployment's run-history strip. */
export type DeploymentRecentRun = {
  id: number
  runNumber: number
  state: DeploymentRunState
  operation: string
  requestedAt: string
  /** When a slot took it: a run's own time is measured from here. */
  claimedAt?: string
  endedAt?: string
}

/** The commit a Git run built, recorded in the run's metadata under `commit`. */
export type DeploymentCommit = {
  sha: string
  subject?: string
  author?: string
  authoredAt?: string
}

export type DeploymentGitPolicy = {
  automatic: boolean
  watchInclude: string[]
  watchExclude: string[]
  commitStatuses?: boolean
  /** Production refuses every build and takes only releases promoted from staging. */
  promotionOnly?: boolean
  revision: number
  inherited?: boolean
  conflict?: boolean
}

export type DeploymentGitWatch = {
  automatic: boolean
  branch?: string
  status: string
  checkedAt?: string
  intervalSeconds: number
  reason?: string
  policy?: DeploymentGitPolicy
  commitStatus?: DeploymentCommitStatus
}

/** The last thing written back to the Git host: a commit status or a preview comment. */
export type DeploymentForgeReport = {
  provider: string
  repository?: string
  revision?: string
  /** pending/success/failure/error for a status; building/ready/failed/removed/cancelled for a comment. */
  state: string
  runId?: number
  ok: boolean
  /** Why the host did not take it, in words the operator can act on. */
  message?: string
  at: string
}

/** Where an environment's releases are reported, as whom, and how the last one went. */
export type DeploymentCommitStatus = {
  /** github, gitlab or gitea (Gitea and Forgejo share one API); absent when nothing is reported. */
  provider?: "github" | "gitlab" | "gitea" | "git"
  /** What to call the host: github, gitlab, gitea, forgejo or codeberg. */
  product?: string
  host?: string
  repository?: string
  /** The status's name on the host, `just-dashboard/<environment>`. */
  context?: string
  /** The saved token a GitLab or Gitea status is written with. */
  credential?: string
  ready: boolean
  reason?: string
  lastStatus?: DeploymentForgeReport
  lastComment?: DeploymentForgeReport
}

/** Where deployments' output and requests are copied to: an OTLP collector, a Loki, or a JSON-lines endpoint. */
export type LogDrainKind = "otlp" | "loki" | "http"
/** Traces are the requests again, as OpenTelemetry spans; only an `otlp` drain takes them. */
export type LogDrainStream = "output" | "requests" | "traces"

export type LogDrain = {
  id: number
  name: string
  kind: LogDrainKind
  endpoint: string
  enabled: boolean
  streams: LogDrainStream[]
  /** Empty sends every project, including ones created later. */
  projects: number[]
  /** Where the drain's service shows a trace, with `{traceId}` for the id; the request console links to it. */
  traceUrl?: string
  /** The headers the drain sends, by name; their values are sealed and never read back. */
  headerNames: string[]
  createdAt: string
  updatedAt: string
  status: {
    shipped: number
    dropped: number
    batches: number
    lastSuccessAt?: string
    lastError?: string
    lastErrorAt?: string
    /** Waiting in memory for the next delivery. */
    queued: number
  }
}

/**
 * A connection to an external secret manager that deployment variables read
 * as `${{store.NAME/path#KEY}}`. Vault and OpenBao share the KV version 2
 * API; Infisical signs in with a Universal Auth machine identity; Doppler
 * with a service token; AWS Secrets Manager with an access key pair (SigV4);
 * Azure Key Vault with an application's client credentials. The last two
 * keep whole secrets, read as `${{store.NAME/secret-name}}` or, for a field
 * of a JSON value, `${{store.NAME/secret-name#KEY}}`.
 */
export type SecretStoreKind = "vault" | "openbao" | "infisical" | "doppler" | "aws" | "azure"

export type SecretStoreSettings = {
  /** Vault and OpenBao: where the KV version 2 engine is mounted ("secret"). */
  mount?: string
  namespace?: string
  auth?: "token" | "approle"
  authMount?: string
  /** Infisical: the project id and environment slug a reference reads in. */
  project?: string
  environment?: string
  /** AWS: the region the Secrets Manager endpoint is derived from. */
  region?: string
  /** Azure: the vault's name (its host), the tenant signed in to, the cloud. */
  vaultName?: string
  tenantId?: string
  cloud?: "public" | "usgov" | "china"
  /** Azure: a sign-in authority other than the cloud's own. */
  authority?: string
  /** A PEM certificate authority for a self-hosted store's TLS. */
  caCert?: string
}

/** When something was tried against a store and what it answered. */
export type SecretStoreOutcome = { at: string; ok: boolean; message: string }

/** An environment variable that references a store. */
export type SecretStoreUse = {
  projectId: number
  projectName: string
  environmentId: number
  environmentName: string
  environmentKind: string
  variable: string
  /** The reference's target, `name/path#KEY`. */
  reference: string
}

export type SecretStore = {
  id: number
  name: string
  kind: SecretStoreKind
  address: string
  /** Where the store is reached: the address, or what the region or vault name derives. */
  endpoint: string
  settings: SecretStoreSettings
  /** The credentials that are set, by name; their values are sealed and never read back. */
  credentialNames: string[]
  /** A pull request preview's variables may read this store. */
  allowPreviews: boolean
  createdBy: string
  updatedBy: string
  createdAt: string
  updatedAt: string
  test?: SecretStoreOutcome
  lastRead?: SecretStoreOutcome
  usedBy: SecretStoreUse[]
}

/** What Test found: the store's own words, and a probe's version. */
export type SecretStoreTest = { ok: boolean; message: string; version?: string }

/** One page of a project's run history, newest first. */
export type DeploymentRunsPage = {
  runs: DeploymentEngineRun[]
  running: boolean
  /** The cursor for the next page, present only when older runs exist. */
  nextBefore?: number
}

/** A provider or generic hook delivery the trigger received. */
export type DeploymentTriggerDelivery = {
  deliveryId: string
  event: string
  ref?: string
  decision: string
  reason?: string
  runId?: number
  receivedAt: string
}

/** An unfinished project setup the caller can resume from `/deploy/new?draft=`. */
export type DeploymentDraftSummary = {
  id: string
  name?: string
  source?: string
  currentStep: DeploymentDraft["currentStep"]
  updatedAt: string
  expiresAt: string
}

export type DeploymentInsights = {
  projectId: number
  windowDays: number
  generatedAt: string
  runs: number
  succeeded: number
  failed: number
  rolledBack: number
  cancelled: number
  successRate: number
  failureStreak: number
  medianDurationSeconds: number
  p95DurationSeconds: number
  deploysPerWeek: number
  meanRecoverySeconds: number
  recoveredFailures: number
  lastSuccessAt?: string
  lastFailureAt?: string
  daily: {
    date: string
    succeeded: number
    failed: number
    cancelled: number
    medianDurationSeconds: number
  }[]
  topFailures: { code: string; count: number }[]
}

/**
 * Where a promoted release's image came from: the release it was promoted
 * from — staging's, or for staging a pull request preview's — and the local
 * image id the container is created from.
 */
export type DeploymentPromotion = {
  environmentId: number
  environmentName: string
  releaseId: number
  releaseNumber: number
  runId: number
  imageId: string
  imageDigest: string
  sourceRevision?: string
  /** Absent on a record from before previews could be promoted, which is staging's. */
  sourceKind?: "staging" | "preview"
  /** The pull request a preview's image was built from, and who approved its commit. */
  preview?: DeploymentPromotedPreview
}

export type DeploymentPromotedPreview = {
  number: number
  url?: string
  repository?: string
  headRepository?: string
  approvedBy: string
}

/** The newest promotion of a preview's image into staging, as the preview's row reads it. */
export type DeploymentPreviewPromotion = {
  runId: number
  runNumber: number
  state: DeploymentRunState
  environmentId: number
  releaseNumber: number
  sourceRevision?: string
  requestedAt: string
  endedAt?: string
}

/** One of a deployment's long-lived environments, for the environment switcher. */
export type DeploymentEnvironment = {
  id: number
  name: string
  slug: string
  kind: "production" | "staging"
  desiredRevision: number
  liveReleaseId?: number
  /** Production refuses builds and takes only releases promoted from staging. */
  promotionOnly?: boolean
  createdAt: string
}

/** What adding staging copied from production, what it left behind, and why. */
export type DeploymentStagingCopy = {
  environment: DeploymentEnvironment
  copied: string[]
  skipped: { name: string; kind: string; reason: string }[]
  adjusted: string[]
}

/** The Promote confirmation's read: the exact image and how the target differs. */
export type DeploymentPromotionPreview = {
  source: {
    /** "staging" into production, or "preview" into staging. */
    kind: "staging" | "preview"
    environmentId: number
    environmentName: string
    release: DeploymentRelease
    imageId: string
    imageDigest: string
    imageSizeBytes: number
    imagePresent: boolean
    /** The pull request behind a preview source. */
    preview?: {
      number: number
      title?: string
      url?: string
      author?: string
      headRepository?: string
      fork: boolean
      approvedBy?: string
      approvalState?: string
    }
  }
  target: {
    environmentId: number
    environmentName: string
    liveRelease?: DeploymentRelease
    desiredRevision: number
    activeRuns: number
    promotionOnly: boolean
    pendingChanges: { kind: string; name: string; change: string }[]
    releaseTasks: string[]
    /** Production's terms rehearse those tasks on a restored copy first. */
    rehearsal: boolean
  }
  detail: ReleaseComparison["detail"]
  blockers: { code: string; message: string }[]
}

export type DeploymentRelease = {
  id: number
  projectId: number
  environmentId: number
  number: number
  runId: number
  predecessorReleaseId?: number
  state: "candidate" | "live" | "retained" | "failed" | "retired"
  planRevision: number
  sourceRevision?: string
  imageDigest?: string
  configDigest: string
  variablesDigest: string
  strategy: DeploymentStrategy
  expectedDowntime: boolean
  createdAt: string
  activatedAt?: string
  retiredAt?: string
  pinned: boolean
  /** Set on a release an instant rollback took traffic away from. */
  rolledBackAt?: string
  rollbackReason?: string
  /** Set when the image was promoted — from staging, or into staging from a preview — rather than built here. */
  promotion?: DeploymentPromotion
  /**
   * Where each linked database stood when this release went live
   * (`deploy/data_rollback.go`): the moment its point-in-time archive recovers
   * to, or the newest dump that covered it. Absent on a release that went
   * live before this was recorded, or with no linked database.
   */
  dataPoints?: ReleaseDataPoint[]
  /** Whether "Restore data as of this release" is offered, and why not. */
  dataCopy?: { eligible: boolean; reason?: string }
}

/** One linked database's state at the moment a release went live. */
export type ReleaseDataPoint = {
  connectionId: number
  /** The connection's name when the point was recorded. */
  name: string
  engine: string
  kind: "pitr" | "dump" | "none"
  /** pitr: the moment the archive recovers to; dump: when its backup run started. */
  at?: string
  backupRunId?: number
  backupJobId?: number
  archiveId?: number
  /** kind none: why there is nothing to restore a copy from. */
  reason?: string
  recordedAt: string
}

export type DataCopyState = "waiting" | "restoring" | "ready" | "failed" | "skipped" | "removed"

/**
 * A linked database restored from a release's data point into a throwaway
 * container beside production, on the deployment's network. Its credentials
 * are read by a session alone (`…/data-copies/{copy}/url`) and never ride here.
 */
export type DataCopy = {
  id: number
  projectId: number
  environmentId: number
  releaseId: number
  releaseNumber: number
  connectionId: number
  name: string
  engine: string
  database: string
  state: DataCopyState
  source: {
    kind: "pitr" | "dump"
    at?: string
    backupRunId?: number
    backupJobId?: number
    archiveId?: number
  }
  /** pitr: the moment the copy actually reached. */
  recoveredTo?: string
  /** The rollback run it waits for before restoring. */
  afterRunId?: number
  /** The copy's name on the deployment's network, e.g. db-5-copy-3.jd.internal. */
  host?: string
  port?: number
  network?: string
  expiresAt?: string
  createdBy: string
  createdAt: string
  readyAt?: string
  endedAt?: string
  reason?: string
  restoreMs?: number
}

export type DataCopyConnection = {
  url: string
  host: string
  port: number
  user: string
  database: string
}

/** The copy's tables beside the live database's, with their row counts. */
export type DataCopyComparison = {
  copyId: number
  database: string
  tables: {
    schema: string
    name: string
    copyRows: number | null
    liveRows: number | null
    estimated?: boolean
    only?: "copy" | "live"
  }[]
  /** More tables than the server compares. */
  truncated: boolean
  copyError?: string
  liveError?: string
  readAt: string
}

/**
 * An environment's terms for keeping the previous release warm after a
 * blue/green cutover (`deploy/rollback_watch.go`).
 */
export type RollbackPolicy = {
  keepWarm: boolean
  windowSeconds: number
  automatic: boolean
  /** Percentage points over the previous release's 5xx share. */
  errorRateMargin: number
  /** Multiple of the previous release's p95. */
  latencyFactor: number
  minRequests: number
  updatedBy?: string
  updatedAt?: string
}

export type RollbackReading = {
  requests: number
  failures: number
  /** A share, 0–1. */
  errorRate: number
  p95?: number
  from: string
  until: string
}

export type RollbackVerdictCode =
  "no_route" | "no_record" | "not_enough_requests" | "within_baseline" | "error_rate" | "latency"

export type RollbackVerdict = {
  code: RollbackVerdictCode
  judged: boolean
  tripped: boolean
  reason: string
  latencyJudged: boolean
}

export type RollbackWatchState = "watching" | "rolling_back" | "rolled_back" | "closing" | "closed"

/** How one release replaces the next: candidate first, stopping first, or tried as a canary between. */
export type DeploymentStrategy = "blue_green" | "stop_first" | "canary"

/** A canary plan's trial (`deploy.CanaryTerms`). */
export type DeploymentCanaryTerms = {
  /** The candidate's share of the requests, 1–50. */
  percent: number
  /** How long the trial lasts, 60–3600. */
  durationSeconds: number
  /** Cut over at the end of a trial that compared well, rather than waiting for an operator. */
  autoPromote?: boolean
}

/** One canary trial, as GET …/canary reads it (`deploy.CanaryWindow`). */
export type DeploymentCanary = {
  id: number
  projectId: number
  environmentId: number
  runId: number
  releaseId: number
  liveReleaseId: number
  percent: number
  durationSeconds: number
  autoPromote: boolean
  policy: RollbackPolicy
  state: "running" | "holding" | "promoted" | "aborted"
  /** An operator's promote or abort, asked for and not yet carried out. */
  decision?: "promote" | "abort"
  decidedBy?: string
  startedAt: string
  endsAt: string
  live?: RollbackReading
  candidate?: RollbackReading
  verdict?: RollbackVerdict
  checkedAt?: string
  endReason?: string
  endMessage?: string
  endedAt?: string
  releaseNumber: number
  liveReleaseNumber: number
}

export type DeploymentCanaryResponse = { canary: DeploymentCanary | null }

export type RollbackWatch = {
  id: number
  projectId: number
  environmentId: number
  runId: number
  releaseId: number
  warmReleaseId: number
  warmRuntimeId: string
  state: RollbackWatchState
  policy: RollbackPolicy
  cutoverAt: string
  expiresAt: string
  baseline?: RollbackReading
  observed?: RollbackReading
  verdict?: RollbackVerdict
  checkedAt?: string
  rollbackRunId?: number
  attempts: number
  endReason?: string
  endMessage?: string
  endedAt?: string
  createdAt: string
  releaseNumber: number
  warmReleaseNumber: number
  /**
   * The warm release is kept running, not paused, because skew protection
   * still sends it the visitors it answered first.
   */
  warmRunning?: boolean
  /** What the running warm release answered since the cutover. */
  warmServed?: RollbackReading
}

export type RollbackWatchResponse = {
  policy: RollbackPolicy
  watch: RollbackWatch | null
  preview: boolean
}

export type DeploymentTrigger = {
  id: number
  environmentId: number
  projectId: number
  name: string
  kind: "generic_hook" | "api" | "github" | "gitlab" | "bitbucket" | "gitea" | "legacy_hook"
  provider?: string
  config: {
    repository?: string
    ref?: string
    events?: string[]
    watchInclude?: string[]
    watchExclude?: string[]
    preview?: boolean
    previewQuota?: number
    previewDomain?: string
    /** "app" when the dashboard's GitHub App delivers this trigger's events. */
    delivery?: "app"
  }
  hookId?: string
  enabled: boolean
  lastDeliveryAt?: string
  lastStatus?: string
  /**
   * The delivery log as the list row draws it: the newest delivery in full, and
   * the last 14 decisions oldest first. Present only for an administrator in a
   * session — the log's own route is theirs alone — so absent is not "none".
   */
  lastDelivery?: DeploymentTriggerDelivery
  recent?: DeploymentTriggerOutcome[]
}

/** One square of a trigger's recent-delivery strip. */
export type DeploymentTriggerOutcome = {
  decision: string
  reason?: string
  receivedAt: string
}

/** One account that installed the dashboard's GitHub App. */
export type GitHubAppInstallation = {
  id: number
  account: string
  accountType: string
  htmlUrl: string
  repositorySelection: string
  /** The deploy credential that mints this installation's tokens for clones. */
  credentialId?: number
}

export type GitHubAppStatus = {
  configured: boolean
  app?: {
    id: number
    slug: string
    name: string
    owner: string
    htmlUrl: string
    createdAt: string
  }
  installations: GitHubAppInstallation[]
  installUrl?: string
  webhookUrl?: string
  /** A GitHub-side problem beside a configured App: a deleted App, a revoked key. */
  error?: string
}

/** A repository one of the App's installations grants. */
export type GitHubAppRepository = {
  installationId: number
  account: string
  nameWithOwner: string
  name: string
  description?: string
  language?: string
  private: boolean
  defaultBranch: string
  cloneUrl: string
  htmlUrl: string
  /** What the picker sorts by, and the two marks it reads. */
  pushedAt?: string
  fork?: boolean
  archived?: boolean
  credentialId?: number
}

/** What the page posts to GitHub to create the App, and the state GitHub sends back. */
export type GitHubAppManifestStart = {
  action: string
  state: string
  manifest: Record<string, unknown>
}

export type DeploymentSchedule = {
  id: number
  environmentId: number
  name: string
  expression: string
  timezone: string
  enabled: boolean
  nextRunAt?: string
  /**
   * The next five firings, led by `nextRunAt` and walked in `timezone`, so a
   * daylight-saving change lands where the dispatcher puts it. Absent while paused.
   */
  nextRuns?: string[]
  steps: { action: string; config: Record<string, unknown>; required: boolean }[]
}

/** POST …/schedules/test: when an expression would fire, in its zone. */
export type DeploymentScheduleTest = {
  nextRunAt: string
  nextRuns: string[]
}

export type DeploymentPreviewAddress = {
  /** tailnet: a port on this host's Tailscale node, reachable only from the tailnet; domain: a public hostname. */
  kind: "tailnet" | "domain"
  url: string
  port?: number
  /** Whether the address currently answers: the mapping or route is in place for a live release. */
  published: boolean
}

export type DeploymentPreview = {
  id: number
  triggerId: number
  providerRef: string
  environmentId: number
  environmentSlug: string
  state: "open" | "closed"
  updatedAt: string
  isolationStatus?: "pending" | "quarantined" | "cleared"
  isolationReason?: string
  /** The pull request number, as providerRef reads as an integer. */
  number: number
  /**
   * The deploy project the preview belongs to. A checkout on the Git page can
   * be deployed by more than one project, and its verbs address this one.
   */
  projectId?: number
  /** "dashboard" when Test this pull request created it; absent for a webhook delivery. */
  origin?: "dashboard"
  title?: string
  /** The approved head the preview is configured at. */
  revision?: string
  headRef?: string
  headRepository?: string
  author?: string
  /** The newest head seen on the pull request; differs from revision once new commits arrive. */
  headRevision?: string
  approvalState?: string
  /** The head the production variables were copied for; absent when the preview carries none. */
  variablesCopiedRevision?: string
  liveReleaseId?: number
  address?: DeploymentPreviewAddress
  lastRun?: DeploymentRecentRun
  /** A live Compose preview's services as Docker reports them; absent for a single container. */
  services?: DeploymentRuntimeServices
  /** The newest promotion of this preview's image into staging, however it ended. */
  promotion?: DeploymentPreviewPromotion
  /** The preview's own copies of databases production gives previews a branch of. */
  branches?: PreviewDatabaseBranch[]
}

/** What a linked database gives an environment's previews (`deploy/preview_branches.go`). */
export type PreviewBranchMode = "none" | "branch" | "masked"

export type PreviewBranchPolicy = {
  connectionId: number
  mode: PreviewBranchMode
  /** table.column or schema.table.column, overwritten in their shape on the copy. */
  maskedColumns: string[]
  updatedBy?: string
  updatedAt?: string
}

/** A preview's own copy of a linked database, restored from its newest backup. */
export type PreviewDatabaseBranch = {
  id: number
  environmentId: number
  connectionId: number
  name: string
  engine?: string
  database?: string
  mode: PreviewBranchMode
  maskedColumns: string[]
  state: "waiting" | "restoring" | "ready" | "failed" | "removed"
  /** "dump" for a backup job's native dump, "pitr" for a point-in-time recovery. */
  source: { kind: string; backupRunId?: number; takenAt?: string; bytes?: number; method?: string }
  recoveredTo?: string
  host?: string
  port?: number
  maskedRows?: number
  reason?: string
  restoreMs?: number
  requestedBy?: string
  createdAt: string
  readyAt?: string
  endedAt?: string
}

/** One service of a Compose stack, and what its preview gets in place of production's. */
export type ComposePreviewService = {
  name: string
  /** The service the preview's route and tailnet address reach; the only one that answers. */
  public: boolean
  /** Ports production publishes on the host; the preview publishes none of them. */
  ports: string[]
  /** Mount targets that start empty on storage of the preview's own. */
  volumes: string[]
  /** Read-only files of the commit itself the preview mounts. */
  checkout: string[]
  /** Read-only paths of this server the preview does not get. */
  leftOut: string[]
}

/** What a preview of a Compose stack gets, service by service, and what refuses one. */
export type ComposePreviewFacts = {
  public: string
  services: ComposePreviewService[]
  /** What no preview may have; any one refuses the preview. */
  refusals: string[]
}

/** A deploy project's pull requests on GitHub, joined to the previews built from them. */
export type ProjectPullRequests = {
  repository: string
  host: string
  available: boolean
  /** not_github, sign_in_required, app_not_installed or not_installed when pull requests cannot be read. */
  reason?: string
  identity?: "cli" | "app"
  /** A checkout of the same repository under the git roots, for the link to the Git page. */
  checkoutPath?: string
  pulls: GitPullRequest[]
  previews: DeploymentPreview[]
  production: {
    environmentId: number
    automatic: boolean
    intervalSeconds: number
    awaitingFirstDeployment: boolean
  }
  tailnet: TailscaleIdentity
  /** The production build is a Compose stack; `composePreview` says what its preview gets. */
  compose: boolean
  /** Present for a Compose stack: each service's isolation, and what refuses a preview. */
  composePreview?: ComposePreviewFacts
  /** The databases production gives its previews a branch of; a fork's preview gets none. */
  databaseBranches?: (PreviewBranchPolicy & { name: string; driver?: string })[]
  /** The production source is a local checkout, which has no remote to fetch a pull request from. */
  localCheckout: boolean
  /** The runtime plan's internal port; 0 means nothing to publish. */
  internalPort: number
  /** Production release tasks, which a preview never runs. */
  releaseTasks: number
}

/** Open pull request and preview counts per deploy project, for the fleet. */
export type FleetPullRequests = {
  projects: Record<string, { open: number; previews: number }>
}

export type DeploymentPreviewApproval = {
  configured: boolean
  id: number
  triggerId: number
  providerRef: string
  revision: string
  repository: string
  headRepository: string
  /** The pull request's branch; absent on an approval recorded before deliveries kept it. */
  headRef?: string
  author: string
  state: "pending" | "approved" | "rejected" | "superseded" | "closed"
  approvedBy?: string
  updatedAt: string
}

export type DeploymentActiveWork = {
  run: DeploymentEngineRun
  projectName: string
  environment: string
  currentStep?: string
  currentStatus?: DeploymentStepState
  queuePosition?: number
}

export type DeploymentFleet = {
  deployments: DeploymentSummary[]
  activeWork: DeploymentActiveWork[]
  slots: {
    heavyUsed: number
    heavyCapacity: number
    lightUsed: number
    lightCapacity: number
  }
}

export type DeploymentComposeDocument = { path: string; content: string; order: number }

/**
 * Where a new deployment's public address comes from when the operator has not
 * bought a domain: a name that already resolves to this server, plus whether a
 * certificate can cover it today.
 */
export type DeploymentHostnameSuggestion = {
  hostname: string
  base?: string
  /** Whether a certificate on this host already covers it — the only thing activation accepts. */
  covered: boolean
  certificateName?: string
  /**
   * The HTTP-01 challenge this host could issue one with now, if any — or, for a
   * wildcard, `dns-01` through the saved credentials of `certificateDnsProvider`.
   */
  certificateMethod?: "nginx" | "webroot" | "standalone" | "caddy" | "dns-01" | "app-domain"
  certificateDnsProvider?: string
  certificateIssue?: string
  /** `tunnel` answers a name asked about with `tunnel=1`: whether a tunnel can serve it. */
  /**
   * `app-domain` is a name under this server's just-dashboard.com handle: covered when the
   * handle's wildcard is valid, and otherwise `certificateMethod: "app-domain"`, the release
   * issuing that wildcard. Asked for with `method=app-domain`, it is answered whatever the default.
   */
  method: "wildcard" | "sslip" | "custom" | "none" | "tunnel" | "app-domain"
  detail: string
  /** This server's public IPv4 — for a typed hostname, what its A record should name. */
  address?: string
  /**
   * For a typed hostname, whether its DNS already points at this server. Only an
   * administrator is answered (resolving a chosen name is theirs, as on the proxy
   * page), and never when this host has no public address — absent is not a no.
   */
  resolves?: boolean
  /**
   * Whether a live project already answers to the `name` this was asked with.
   * Absent when the question was about a hostname, which carries no claim
   * about project names at all.
   */
  nameTaken?: boolean
}

/** One record set: a name, a type, and every value the name holds of it. */
export type DeploymentDNSRecordSet = {
  name: string
  type: "A" | "AAAA" | "CNAME"
  values: string[]
  /** Seconds; 1 is Cloudflare's automatic, which a proxied record has. */
  ttl: number
  /** Cloudflare's edge answers for the name: a tunnel's CNAME always is. */
  proxied?: boolean
  comment?: string
}

/** A set's line of a DNS record plan: what the zone holds and what a write does. */
export type DeploymentDNSRecordChange = {
  type: DeploymentDNSRecordSet["type"]
  name: string
  /** `conflict` blocks the write; `stale` is said and left alone, since nothing is deleted. */
  action: "create" | "update" | "keep" | "conflict" | "stale"
  current: string[]
  want: string[]
  currentTtl?: number
  ttl?: number
  proxied?: boolean
  detail: string
  /** The set as the zone holds it now is the one this dashboard wrote. */
  ours: boolean
}

/** A set this dashboard wrote into the zone, with who and when. */
export type DeploymentDNSRecordManaged = {
  type: DeploymentDNSRecordSet["type"]
  values: string[]
  ttl: number
  provider: string
  zone: string
  createdBy: string
  createdAt: string
  updatedBy: string
  updatedAt: string
  /** The zone still holds what was written. */
  current: boolean
}

/**
 * `GET /deploy/{id}/domains/{domain}/dns-record`, the dry run, and the POST
 * that writes it: what the name should hold (A and AAAA to this server, or a
 * proxied CNAME to the tunnel), what its zone holds, and through which saved
 * DNS credential and zone it is written.
 */
export type DeploymentDNSRecordPlan = {
  hostname: string
  environmentId: number
  environment: string
  tunnel: boolean
  want: DeploymentDNSRecordSet[]
  provider?: string
  providerKey?: string
  zone?: string
  account?: string
  candidates: { key: string; provider: string; zone?: string; error?: string }[]
  problems: string[]
  changes: DeploymentDNSRecordChange[]
  writable: boolean
  ready: boolean
  /** The write would change a record this dashboard did not write; it is sent with `replace`. */
  foreign: boolean
  refusal?: string
  refusalCode?:
    | "no_public_address"
    | "tunnel_not_connected"
    | "no_credentials"
    | "no_zone"
    | "ambiguous"
    | "tunnel_zone"
    | "provider_error"
    | "conflict"
  summary: string
  managed: DeploymentDNSRecordManaged[]
  written?: DeploymentDNSRecordChange[]
}

export type DeploymentDraftSource = {
  kind: DeploymentSourceKind
  mode: DeploymentSourceMode
  url?: string
  provider?: string
  providerBaseUrl?: string
  repository?: string
  ref?: string
  credentialId?: number
  localPath?: string
  subdirectory?: string
  managedInPlace?: boolean
  includeSubmodules?: boolean
  includeLfs?: boolean
  image?: string
  platform?: string
  composeFiles?: DeploymentComposeDocument[]
  resourceId?: string
  blueprintId?: string
  blueprintVersion?: string
  blueprintInputs?: Record<string, string>
}

export type NodePackageManager = "bun" | "npm" | "pnpm" | "yarn"

/**
 * The recipe stage a build variable is mounted in: the dependency install, the
 * build command, or both — a root package's own postinstall runs inside the
 * install. `validBuildSecretStep` is the server's closed set.
 */
export type BuildSecretStep = "install" | "build" | "install_and_build"

/** The automatic recipes the backend can build; `validRecipe` is its closed set. */
export type DeploymentRecipe =
  | "node"
  | "go"
  | "python"
  | "rust"
  | "java"
  | "dotnet"
  | "deno"
  | "php"
  | "site"
  | "ruby"
  | "elixir"
  | "scala"
  | "clojure"
  | "dart"
  | "gleam"

/** An environment variable detection found the source reading. */
export type DeploymentDetectedVariable = {
  name: string
  /** The example file's own value, when it had one and it was not credential-shaped. */
  example?: string
  sources: string[]
  /**
   * How the dashboard supplies the value when nothing is typed: a self-issued
   * secret minted at commit, the planned domain, a harmless documented default,
   * or a secret only the operator holds and has to paste.
   */
  setup?: DeploymentVariableSetup
  /** The evidence behind `setup`, e.g. "Auth.js signs and encrypts sessions with it". */
  setupReason?: string
  generateLength?: number
  generateFormat?: DeploymentGeneratedSecretFormat
  domainTemplate?: string
  defaultValue?: string
  /** "build" when the value is read while the build runs. */
  phase?: "build"
  /** Compiled into the JavaScript every visitor downloads. */
  browserInlined?: boolean
  /** Read with no default where the application starts or builds. */
  required?: boolean
  /** Read with no default on a path that may not always run. */
  requiredRead?: boolean
  /** The files that read it while the build runs, which its build phase comes from. */
  buildSources?: string[]
  /** A committed file whose value for this name points at loopback. */
  localhostIn?: string
  /**
   * "install" for a registry credential a package manager's configuration
   * (.npmrc, .yarnrc.yml, bunfig.toml) names: only the dependency install
   * reads it. `installRequired` says the install fails without it.
   */
  step?: "install"
  installRequired?: boolean
}

/** A committed JavaScript lockfile, compared as data with package.json. */
export type DeploymentDetectedLockfile = {
  path: string
  manager: NodePackageManager
  /** `stale` is what the manager's frozen install would refuse. */
  state: "in_sync" | "stale" | "unknown"
  missing?: string[]
  extra?: string[]
  changed?: string[]
  /** The sentence an operator reads: "package-lock.json is missing 15 dependencies (…)". */
  note?: string
}

/**
 * What the recipe installs when `manager` is chosen, computed by the same
 * planner the build runs. An empty `install` with a blocked finding is a
 * choice the build would refuse.
 */
export type DeploymentDetectedNodeInstall = {
  manager: NodePackageManager
  lockfile?: string
  install?: string
  toolchain?: string
  /** Detection's commands for this manager's runner. */
  buildCommand?: string
  startCommand?: string
  findings?: DeploymentPreflightFinding[]
}

export type DeploymentVariableSetup = "generate" | "domain" | "default" | "paste"

/**
 * A database address in the shape its consumer parses: JDBC (Spring,
 * Quarkus), JDBC over MariaDB Connector/J, which accepts only
 * `jdbc:mariadb://`, ADO.NET (.NET), or Rails' `mysql2://`.
 */
export type DeploymentDatabaseConnectionFormat = "jdbc" | "jdbc-mariadb" | "adonet" | "mysql2"

export type DeploymentGeneratedSecretFormat = "" | "hex" | "base64" | "laravel" | "keylist"

/** A database engine detection found the source connecting to. */
export type DeploymentDetectedDatabase = {
  engine: string
  variable: string
  evidence: string
  /** The connection shape the consumer parses when it is not a URL. */
  format?: DeploymentDatabaseConnectionFormat
  /** Postgres extensions the schema needs; the official image ships neither. */
  extensions?: ("vector" | "postgis")[]
  /** A driver that only speaks its hosted provider's protocol. */
  hosted?:
    | "neon-http"
    | "neon-ws"
    | "vercel-postgres"
    | "planetscale-http"
    | "prisma-accelerate"
    | "upstash-rest"
  /** Further databases on the same server the framework reads by name. */
  alsoVariables?: string[]
}

/** A fact about the source's configuration that preflight answers. */
export type DeploymentEnvironmentNote = {
  code: string
  detail?: string
  path?: string
}

/**
 * How detection proposes a release proves it serves (`DetectedReadiness`):
 * the check `defaultChecks` builds, where it came from, and what the source
 * says about hosts and HTTPS that preflight turns into findings.
 */
export type DeploymentDetectedReadiness = {
  kind: "http" | "docker_health"
  path?: string
  /** Any answer below 500 (but 400 and 421) counts as ready. */
  acceptAnyAnswer?: boolean
  attempts?: number
  intervalSeconds?: number
  source: "healthcheck" | "platform" | "framework" | "code" | "convention"
  evidence: string
  slowStart?: string
  modelDownload?: string
  modelCache?: string
  rootRoute?: "routed" | "unrouted"
  httpsRedirect?: string
  httpsRedirectIgnoresProxy?: boolean
  allowedHosts?: string[]
  allowedHostsSource?: string
}

/** The library that makes a candidate a process that never listens. */
export type DeploymentDetectedBackgroundWorker = {
  library: string
  kind: string
  evidence: string
}

/** A start command that backgrounds the application, which detection could not rewrite. */
export type DeploymentDetectedStartDetach = {
  command: string
  script?: string
  source: string
  effect: "exits" | "backgrounds"
  reason: string
  action: string
}

/**
 * What the source says about where its server listens: a port it fixes
 * whatever PORT says, whether it reads PORT, and a loopback bind nothing
 * outside the container reaches. Preflight re-checks these against the plan.
 */
export type DeploymentDetectedListen = {
  port?: number
  portFrom?: string
  readsPort?: boolean
  readsPortFrom?: string
  loopback?: string
  loopbackFrom?: string
  loopbackCertain?: boolean
  loopbackRecipeFix?: string
  loopbackVariable?: string
  unbridged?: string
}

/**
 * A plain runtime variable the deployment's place behind the proxy decides:
 * AUTH_TRUST_HOST, NEXTAUTH_URL (following the primary domain), HOST.
 */
export type DeploymentNetworkVariable = {
  name: string
  value?: string
  domainTemplate?: string
  reason: string
}

/**
 * State the application writes to its own filesystem, which a new release
 * would start without. `target` is where a managed volume can stand without
 * hiding code (absent when none can); `variable` and `value` move the state
 * under it; a server database linked through `databaseVariable` replaces the
 * file altogether, and `connectionVariable` set to any driver but sqlite
 * takes the file out of use (Laravel's DB_CONNECTION).
 */
export type DeploymentDetectedPersistentPath = {
  kind: "sqlite" | "uploads" | "storage" | "volume" | "keys"
  path: string
  target?: string
  variable?: string
  value?: string
  databaseVariable?: string
  connectionVariable?: string
  source: string
  reason: string
}

/** `DetectedStaticSite` (backend `detect_static_site.go`). */
export type DeploymentStaticSite = {
  generator?: string
  version?: string
  declared?: string
  unpinned?: boolean
  versionIssue?: string
  basePath?: string
  basePathSource?: string
  basePathExpression?: boolean
  themeSubmodule?: string
  hostingRules?: number
  hostingRulesLeftOut?: number
}

export type DeploymentDetectionCandidate = {
  dockerfile?: string
  /**
   * The Railpack provider that would build this directory: beside a recipe, the choice
   * offered next to it; on a Railpack candidate, the one it builds with.
   */
  railpackProvider?: RailpackProvider
  goVersion?: string
  packageManager?: NodePackageManager
  packageManagers?: NodePackageManager[]
  recipeIssue?: string
  id: string
  name: string
  root: string
  profile: WorkloadProfile
  buildMethod: DeploymentBuildMethod
  confidence: "high" | "medium" | "low"
  framework?: string
  recipe?: DeploymentRecipe
  buildCommand?: string
  startCommand?: string
  outputDirectory?: string
  port?: number
  schemaTool?: string
  schemaCommand?: string
  schemaInStart?: boolean
  /** The repository's release command applies the schema, so the start command does not. */
  schemaInRelease?: boolean
  spaFallback?: boolean
  pythonVersion?: string
  unpinnedDependencies?: boolean
  listen?: DeploymentDetectedListen
  networkVariables?: DeploymentNetworkVariable[]
  variables?: DeploymentDetectedVariable[]
  databases?: DeploymentDetectedDatabase[]
  persistentPaths?: DeploymentDetectedPersistentPath[]
  /** Loads the project's seed data; `seedResets` says it clears tables first. */
  seedCommand?: string
  seedResets?: boolean
  /** The schema step pushes the declared model instead of applying migrations. */
  schemaPush?: boolean
  /** Variable prefixes this root's framework compiles into browser code. */
  browserPrefixes?: string[]
  environmentNotes?: DeploymentEnvironmentNote[]
  evidence: { path: string; reason: string }[]
  needsDecision: string[]
  readiness?: DeploymentDetectedReadiness
  backgroundWorker?: DeploymentDetectedBackgroundWorker
  startDetaches?: DeploymentDetectedStartDetach
  /** What the Dockerfile's name, place or command says it was written for. */
  dockerfileRole?: "production" | "development"
  /** The Dockerfile stage to build when its last stage is a development one. */
  dockerfileTarget?: string
  /** The Dockerfile's build arguments, by name only. */
  dockerfileArgs?: DeploymentDockerfileArg[]
  /** Literal `FROM --platform=` values the Dockerfile pins. */
  dockerfilePlatforms?: string[]
  /** The Dockerfile's named stages, which a configured stage must be one of. */
  dockerfileStages?: string[]
  /** What detection proved about how this candidate's image would build. */
  imageBuildIssues?: DeploymentImageBuildIssue[]
  /** The command the repository declares runs once before each release. */
  releaseCommand?: string
  /** Why this candidate is offered but never chosen over the application: an example, a docs site, the frontend of an API. */
  demotion?: string
  /** A shape nothing on this server can serve. */
  notDeployable?: DeploymentNotDeployableKind
  /** The desktop shell (tauri, wails) whose frontend this is. */
  desktopShell?: string
  /** The other roots of a repository split into a frontend and an API. */
  companions?: string[]
  /** What the source runs besides this candidate's own process. */
  processes?: DeploymentDetectedProcess[]
  /** What other platforms' deployment files declare for this candidate. */
  platformManifests?: DeploymentPlatformManifest[]
  /** Serverless or edge code a container build does not run. */
  serverlessCode?: { platform: string; paths: string[]; entry?: string; blocking?: boolean }[]
  /**
   * What a static site's own files say about its build and serving: the site
   * generator and its release, the sub-path it was built for, the theme's Git
   * submodule and the hosting rules the static server applies.
   */
  staticSite?: DeploymentStaticSite
  /** Imports that resolve only on a case-insensitive disk. */
  importCaseMismatches?: {
    file: string
    line: number
    specifier: string
    actual: string
    language: "javascript" | "php"
  }[]
  lockfiles?: DeploymentDetectedLockfile[]
  nodeInstalls?: DeploymentDetectedNodeInstall[]
  /** The Node major the recipe builds on and where it came from, e.g. "22 (.nvmrc)". */
  nodeVersion?: string
  /**
   * What the build reads that preflight judges against the configuration and
   * the host: the env-validation schema it imports and the memory it is
   * estimated to peak at.
   */
  nodeBuild?: {
    envSchema?: string
    envServer?: string[]
    envClient?: string[]
    envSkippable?: boolean
    memoryMiB?: number
    /** Names `prisma.config` reads that the recipe gives a placeholder while `prisma generate` runs. */
    prismaEnv?: string[]
    /** What the framework's configuration made the recipe do, said before Deploy. */
    findings?: DeploymentPreflightFinding[]
    /** Package scripts that start a development server or a watcher, with what they start. */
    devScripts?: Record<string, string>
    /** Package scripts that migrate or push, so a build that runs one needs the real database. */
    prismaConnectScripts?: string[]
  }
  /** go.mod's toolchain line and the .go-version pin, judged against the plan's Go version. */
  goToolchain?: string
  goVersionFile?: string
  /** The module's buildable main packages ("." is the root) and the one detection chose. */
  goMainPackages?: string[]
  /** How many main packages the bounded list above leaves out. */
  goMainPackagesOmitted?: number
  goPackage?: string
  /** A Go module with no main package: nothing for the recipe to run. */
  goLibrary?: boolean
  /** How the Go module builds beyond its toolchain (detect_go.go). */
  go?: {
    context?: string
    workspace?: boolean
    localReplaces?: string[]
    replacesOutside?: string[]
    cgoModules?: string[]
    cgoLocal?: string[]
    cgoPackages?: string[]
    cgoRuntime?: string[]
    cgoUnknown?: string[]
    vendored?: boolean
    sumMissing?: boolean
    sumStale?: string[]
    ownerModules?: string[]
    embeds?: {
      pattern: string
      path: string
      present?: boolean
      frontend?: string
      framework?: string
    }[]
    codegen?: string
    codegenMissing?: string[]
    subcommand?: string
    /** The go and toolchain lines of the go.work that uses the module. */
    workGo?: string
    workToolchain?: string
  }
  /** How the Rust crate builds: its workspace, binaries and native crates (detect_rust.go). */
  rust?: {
    workspace?: string
    package?: string
    /** The crate's binary targets, and the one the recipe serves unless the plan names another. */
    binaries?: string[]
    binary?: string
    binaryReason?: string
    nativePackages?: string[]
    nativeCrates?: string[]
    /** What the runtime image installs when the binary links dynamically (bindgen). */
    nativeRuntime?: string[]
    nativeUnmapped?: string[]
    sqlxMacros?: boolean
    sqlxOffline?: boolean
    /** Where the offline query data is: a .sqlx directory or sqlx-data.json. */
    sqlxOfflineData?: string
    sqlxMigrate?: boolean
    lockVersion?: number
    lockStale?: string[]
    toolchain?: string
    heavyRelease?: string
    fullstack?: "leptos" | "trunk" | "dioxus" | "shuttle"
  }
  /** The interpreter range pyproject declares, and the manifest the recipe installs from. */
  pythonRequires?: string
  pythonInstall?:
    | "uv.lock"
    | "poetry.lock"
    | "pdm.lock"
    | "Pipfile.lock"
    | "Pipfile"
    | "requirements.txt"
    | "pyproject.toml"
    | "setup.py"
    | "environment.yml"
  /**
   * Debian packages the source needs in its image. `automatic` ones the Python
   * recipe installs by itself; the others (another platform's Aptfile) seed
   * `build.systemPackages`.
   */
  systemPackages?: DeploymentDetectedSystemPackage[]
  /** What a Maven or Gradle build says about building it. */
  javaBuild?: DeploymentDetectedJavaBuild
  /** What a .NET project says about publishing it. */
  dotnetBuild?: DeploymentDetectedDotnetBuild
}

export type DeploymentDetectedSystemPackage = {
  name: string
  reason?: string
  source?: string
  automatic?: boolean
}

/**
 * A JVM candidate's build: the reactor or settings root it builds from and
 * the module it selects there, how it packages, and the Java release its
 * build files declare (`toolchain` for an exact Gradle toolchain) and its
 * version files pin.
 */
export type DeploymentDetectedJavaBuild = {
  tool: "maven" | "gradle"
  context?: string
  module?: string
  packaging?: string
  runnable?: boolean
  library?: boolean
  aggregator?: boolean
  release?: number
  releaseFrom?: string
  toolchain?: boolean
  pinned?: number
  pinnedFrom?: string
  wrapper?: string
  wrapperUsable?: boolean
  wrapperJarMissing?: boolean
  foojay?: boolean
  profiles?: string[]
  vaadinDevMode?: boolean
}

/**
 * A .NET candidate's project: the directory it publishes from, the targets
 * it declares, the SDK global.json pins, and what the recipe changes about
 * publishing it.
 */
export type DeploymentDetectedDotnetBuild = {
  project: string
  kind: string
  context?: string
  targets?: string[]
  targetText?: string
  multiTarget?: boolean
  sdkPin?: string
  sdkPinFrom?: string
  rollForward?: string
  native?: string[]
  spaRoot?: string
  appHost?: string
  aspire?: string[]
  /** The projects it references, with the frameworks each declares. */
  references?: { project: string; targets?: string }[]
}

export type DeploymentDockerfileArg = {
  name: string
  hasDefault?: boolean
  usedInFrom?: boolean
  consumed?: boolean
}

export type DeploymentImageBuildIssue = {
  code: string
  severity: "blocked" | "warning"
  line?: number
  subject?: string
  detail: string
}

export type DeploymentNotDeployableKind =
  | "library"
  | "cli"
  | "editor-extension"
  | "browser-extension"
  | "github-action"
  | "desktop-app"
  | "mobile-app"
  | "notebook"
  | "windows-only"

/** A process the source runs besides its main one: a queue worker, a scheduler, a release command. */
export type DeploymentDetectedProcess = {
  name: string
  kind: "worker" | "scheduler" | "release" | "web"
  command?: string
  source: string
  reason: string
}

/** Facts another platform's file (fly.toml, render.yaml, app.json, …) declares, and which were taken. */
export type DeploymentPlatformManifest = {
  file: string
  platform: string
  startCommand?: string
  buildCommand?: string
  outputDirectory?: string
  port?: number
  healthPath?: string
  spaFallback?: boolean
  dockerfile?: string
  releaseCommand?: string
  generatedVariables?: string[]
  requiredVariables?: string[]
  volumes?: string[]
  systemPackages?: string[]
  redirects?: number
  toolchains?: string[]
  applied?: string[]
}

export type DeploymentDetection = {
  source: {
    kind: DeploymentSourceKind
    remote?: string
    repository?: string
    ref?: string
    revision?: string
    digest?: string
    os?: string
    architecture?: string
    platforms?: string[]
    localPath?: string
    dirty?: boolean
    composeFiles?: string[]
    services?: string[]
    /** The multi-service template (`id@version`) a Compose source was rendered from. */
    template?: string
  }
  candidates: DeploymentDetectionCandidate[]
  compose?: {
    files: string[]
    services: {
      name: string
      image?: string
      buildContext?: string
      buildDockerfile?: string
      ports: string[]
      mounts: string[]
      advanced: string[]
      buildTarget?: string
      buildArgs?: { name: string; value?: string; fromEnvironment?: boolean }[]
      platform?: string
      imagePlatforms?: string[]
      envFiles?: { path: string; required: boolean; missing?: boolean }[]
      buildContextMissing?: boolean
      dockerfileIssues?: DeploymentImageBuildIssue[]
    }[]
    variables: string[]
    warnings: string[]
    unsupported: string[]
    preview: string
    digest: string
    /** The service readiness and the release's container follow. */
    primaryService?: string
    /** Variables the file interpolates with a default; never required. */
    optionalVariables?: { name: string; default?: string }[]
  }
  selectedId?: string
  /** Why `selectedId` won, or why nothing did. */
  selectionReason?: string
  scannedFiles: number
  scannedBytes: number
  truncated: boolean
  truncatedReason?: string
  unavailable?: string
  gitRequirements: {
    submodules: boolean
    lfs: boolean
    /** Each declared submodule, and whether the source's own access fetches it. */
    submoduleList?: { path: string; sameSource: boolean }[]
    submodulesChecked?: boolean
    lfsChecked?: boolean
    /** How many files LFS tracks, and (bounded) which. */
    lfsFiles?: number
    lfsPaths?: string[]
  }
  /** What detection recognised and deliberately did not offer, and why. */
  setAside?: { path: string; reason: string; kind: string }[]
  /** A reviewed template or the project's own published image of the same application. */
  alternatives?: { kind: "template" | "image"; ref: string; label: string; evidence: string }[]
}

export type DeploymentBuildMethod =
  "recipe" | "dockerfile" | "static" | "image" | "compose" | "none" | "legacy_compose" | "railpack"

/** The providers Railpack has: the values its provider setting, like railpack.json's, takes. */
export type RailpackProvider =
  | "cpp"
  | "deno"
  | "dotnet"
  | "elixir"
  | "gleam"
  | "golang"
  | "java"
  | "node"
  | "php"
  | "python"
  | "ruby"
  | "rust"
  | "shell"
  | "staticfile"
  | "zig"

/**
 * What Railpack decided for a run, from its info and its plan: the release (pinned by
 * digest), the providers it detected, the packages it resolved, the steps of its plan
 * and the images that plan builds from, as Railpack names them.
 */
export type RailpackEvidence = {
  version: string
  image: string
  provider?: RailpackProvider
  providers: string[]
  configFile?: string
  packages: { name: string; requested?: string; resolved?: string; source?: string }[]
  steps: { name: string; commands: string[] }[]
  startCommand?: string
  images: string[]
  secrets?: string[]
  facts?: Record<string, string>
  logs?: { level: "error" | "warn" | "info" | "suggestion"; message: string }[]
  success: boolean
}

export type DeploymentOwnership = "managed" | "linked" | "observed"

/** A container port published on the host next to the routed one: Gitea's SSH, Syncthing's sync protocol. */
/** A route's request rate per client address, with the burst let through at once. */
export type DeploymentRateLimit = { requests: number; per: "second" | "minute"; burst?: number }

export type DeploymentEdgeMethod = "DELETE" | "GET" | "HEAD" | "OPTIONS" | "PATCH" | "POST" | "PUT"

/** What a request has to meet, every condition at once. */
export type DeploymentEdgeMatch = {
  pathPrefix?: string
  pathRegex?: string
  methods?: DeploymentEdgeMethod[]
  /** Matched ignoring case. */
  userAgent?: string
  clientFrom?: string[]
}

/**
 * What a matching request gets: refused 403, answered with a status and
 * nothing else, let through without asking the rules after it, or redirected.
 */
export type DeploymentEdgeAction =
  | { kind: "block"; status?: 403 }
  | { kind: "status"; status: number }
  | { kind: "allow" }
  | { kind: "redirect"; location: string; code: 301 | 302 | 307 | 308 }

export type DeploymentEdgeRule = {
  name: string
  match: DeploymentEdgeMatch
  action: DeploymentEdgeAction
}

export type DeploymentPublishedPort = {
  hostPort: number
  containerPort: number
  protocol?: "tcp" | "udp"
  bindAddress?: string
}

export type DeploymentRestartPolicy = "unless-stopped" | "always" | "on-failure" | "no"

/** The limits Docker sets with --ulimit (`deploy.UlimitNames`). */
export type DeploymentUlimitName =
  | "core"
  | "cpu"
  | "data"
  | "fsize"
  | "locks"
  | "memlock"
  | "msgqueue"
  | "nice"
  | "nofile"
  | "nproc"
  | "rss"
  | "rtprio"
  | "rttime"
  | "sigpending"
  | "stack"

export interface DeploymentUlimit {
  name: DeploymentUlimitName
  soft: number
  hard: number
}

export interface DeploymentTmpfs {
  path: string
  sizeMb?: number
}

export interface DeploymentSysctl {
  name: string
  value: string
}

/** Every GPU, a number of them, or the ones named by index or UUID. */
export type DeploymentGPUMode = "all" | "count" | "devices"

export interface DeploymentGPUs {
  mode: DeploymentGPUMode
  count?: number
  deviceIds?: string[]
  /** A pull request preview of the environment gets them too. */
  previews?: boolean
}

export type NotificationChannelKind =
  | "webhook"
  | "discord"
  | "slack"
  | "telegram"
  | "email"
  | "ntfy"
  | "gotify"
  | "pushover"
  | "mattermost"
  | "teams"

export type NotificationEvent = "run.started" | "run.succeeded" | "run.failed" | "run.cancelled"

export type NotificationChannel = {
  id: number
  name: string
  kind: NotificationChannelKind
  url: string
  target: string
  events: string[]
  enabled: boolean
  createdAt: string
  updatedAt: string
  /**
   * The list's reading of a channel, only on GET /deploy/notifications — a
   * mutation's answer is the bare channel, so reload the list after one. `via`
   * is an e-mail channel's SMTP host; `recent` is the last 14 attempts, oldest first.
   */
  via?: string
  lastDelivery?: NotificationAttempt
  recent?: NotificationOutcome[]
}

/** A channel's newest delivery attempt, as its row reports it. */
export type NotificationAttempt = {
  status: string
  event: string
  responseClass: string
  createdAt: string
  nextAttemptAt?: string
}

/** One square of a channel's recent-delivery strip; `test` marks a message sent from the page. */
export type NotificationOutcome = {
  status: string
  createdAt: string
  test: boolean
}

export type NotificationChannelConfig = {
  webhookUrl?: string
  botToken?: string
  chatId?: string
  smtpHost?: string
  smtpPort?: number
  smtpUsername?: string
  smtpPassword?: string
  smtpSecurity?: "starttls" | "tls" | "none"
  from?: string
  to?: string[]
  /** An ntfy or Gotify server's base URL; ntfy's defaults to https://ntfy.sh. */
  serverUrl?: string
  /** The ntfy topic. */
  topic?: string
  /** An ntfy access token, a Gotify application token or a Pushover application token. */
  token?: string
  /** The Pushover user or group key. */
  userKey?: string
}

export type NotificationDelivery = {
  id: number
  channelId: number
  runId?: number
  /** The run it announced; all three absent for a test message or a purged run. */
  projectId?: number
  projectName?: string
  runNumber?: number
  event: string
  attempt: number
  status: string
  responseClass: string
  nextAttemptAt?: string
  createdAt: string
  completedAt?: string
}

export type DeploymentConfiguration = {
  build: {
    method: DeploymentBuildMethod
    recipe?: DeploymentRecipe
    /**
     * What detection recognised the source as, recorded at creation. Read-only:
     * the server ignores a sent value and drops it once the method, recipe,
     * root directory or source location changes.
     */
    framework?: string
    goVersion?: string
    /** The main package a Go recipe builds, relative to the root directory; empty lets it choose. */
    goPackage?: string
    /** The binary target a Rust recipe serves; empty lets it choose. */
    cargoBin?: string
    pythonVersion?: string
    /**
     * The Node major a JavaScript recipe builds and runs on, or the one the PHP, Python, Ruby
     * or Elixir recipe installs front-end assets with; empty follows the repository.
     */
    nodeVersion?: string
    /** The PHP release a PHP recipe builds on; empty lets composer.json and composer.lock decide. */
    phpVersion?: string
    /** Debian packages the Python recipe installs beside the ones its dependencies need. */
    systemPackages?: string[]
    /** The JDK release a Java recipe builds and runs on; empty lets the build and version files decide. */
    javaVersion?: string
    /** The .NET release a .NET recipe publishes for; empty lets the project and global.json decide. */
    dotnetVersion?: string
    packageManager?: NodePackageManager
    rootDirectory?: string
    dockerfile?: string
    buildCommand?: string
    startCommand?: string
    outputDirectory?: string
    spaFallback?: boolean
    targetPlatform?: string
    /** The Dockerfile stage to build (custom Dockerfiles only). */
    target?: string
    /** The Compose service readiness and the release's container follow; empty keeps detection's. */
    primaryService?: string
    /** A Railpack build's release from the reviewed catalogue; empty is the dashboard's current one. */
    railpackVersion?: string
    /** The provider Railpack is told to use instead of detecting one. */
    railpackProvider?: RailpackProvider
    /** The configuration file Railpack reads, relative to the root; empty is railpack.json. */
    railpackConfig?: string
    noCache?: boolean
    secrets?: { variable: string; step: BuildSecretStep }[]
    releaseTasks?: {
      name: string
      command: string
      workingDirectory?: string
      timeoutSeconds: number
      env: string[]
      /** "image" runs it in the release's own image; absent is the host shell. */
      runner?: "image"
    }[]
  }
  runtime: {
    previewIsolation?: boolean
    image?: string
    command?: string[]
    internalPort?: number
    hostPort?: number
    ports?: DeploymentPublishedPort[]
    bindAddress?: string
    strategy: DeploymentStrategy
    /** A canary plan's trial: its share of the requests, its window, and whether it promotes itself. */
    canary?: DeploymentCanaryTerms
    privileged?: boolean
    hostNetwork?: boolean
    capabilities?: string[]
    devices?: string[]
    memoryMb?: number
    cpus?: number
    pidsLimit?: number
    restartPolicy?: DeploymentRestartPolicy
    /** The user the process runs as, `uid[:gid]` or a name the image has; empty is the image's own. */
    user?: string
    /** The root filesystem is mounted read-only; the tmpfs paths (default /tmp) stay writable. */
    readOnlyRootfs?: boolean
    /** Paths kept writable in memory, each at its size in MiB (empty is 64). */
    tmpfs?: DeploymentTmpfs[]
    /** Kernel limits each process starts with; -1 is unlimited where the limit allows it. */
    ulimits?: DeploymentUlimit[]
    /** Kernel parameters of the container's own namespaces: net.*, fs.mqueue.*, the IPC limits. */
    sysctls?: DeploymentSysctl[]
    /** The server's NVIDIA GPUs; a preview gets them only when `previews` says so. */
    gpus?: DeploymentGPUs
    /** Containers of each release side by side behind the route, 2 to 8; empty is one. */
    replicas?: number
    /**
     * Keeps each visitor on the release that first answered them while it is
     * kept warm, by the jd_release cookie. The rollback terms' to edit.
     */
    skewProtection?: boolean
    /** The signal that asks the container to stop; empty is the image's own, SIGTERM. */
    stopSignal?: string
    /** Seconds the container has to exit after that signal before it is killed; empty is 10. */
    gracePeriodSeconds?: number
    /** Seconds the previous release keeps running beside the new one before it is stopped. */
    drainSeconds?: number
    /** The largest request body the route lets through, in MB; empty is 64. */
    maxRequestBodyMb?: number
    /** The only addresses and ranges the public route answers; empty answers everyone. */
    allowFrom?: string[]
    /** Set on every answer the public route gives, replacing the application's own. */
    responseHeaders?: { name: string; value: string }[]
    /** An auth server asked about every request before the public route answers it. */
    forwardAuth?: DeploymentForwardAuth
    /** What the route's maintenance does while it is on; whether it is on is the switch's. */
    maintenance?: DeploymentRouteMaintenance
    /** The codes the public route answers with a page of its own. */
    errorPages?: DeploymentErrorPage[]
    /** Gives the application's own answers with those codes the page too. */
    interceptErrors?: boolean
    /** Strict-Transport-Security on every HTTPS answer; absent sends none. */
    hsts?: DeploymentRouteHSTS
    /** Requests one client address may make; past it the route answers 429. */
    rateLimit?: DeploymentRateLimit
    /** Decided in order at the proxy; the first rule a request meets decides it. */
    edgeRules?: DeploymentEdgeRule[]
    /** Refuses the addresses CrowdSec's local engine has banned. */
    crowdSec?: boolean
    /**
     * Published on this host's tailnet and nowhere else, as a preview is: no
     * public domain and no public certificate. Absent is an ordinary
     * environment.
     */
    tailnet?: DeploymentRouteTailnet
    /** Recorded requests replayed against the candidate before the cutover; absent is off. */
    replay?: ReplayPlan
    /** The container is stopped while nobody visits and the next request starts it; absent is off. */
    sleep?: SleepPlan
    mounts?: {
      source: string
      target: string
      readOnly?: boolean
      ownership: DeploymentOwnership
    }[]
  }
  variables: {
    name: string
    sensitivity: "plain" | "secret"
    scopes: string[]
    required?: boolean
    reference?: string
    // A plain literal a blueprint input became; secrets never travel here.
    value?: string
    /** Recomputes this blueprint default when its primary domain changes. */
    domainTemplate?: string
    // Length of a secret the server generates when the deployment is saved.
    generate?: number
    /** The shape of that generated secret; empty is alphanumeric. */
    generateFormat?: DeploymentGeneratedSecretFormat
  }[]
  dependencies: {
    kind: string
    ownership: DeploymentOwnership
    resourceKind: string
    resourceId?: string
    config?: Record<string, unknown>
  }[]
  checks: {
    name: string
    kind: string
    phase: "readiness" | "smoke"
    required: boolean
    config?: Record<string, unknown>
  }[]
  domains: DeploymentPlannedDomain[]
}

/** A password in front of the route: the hash is what the plan keeps, the password is write-only. */
export type DeploymentDomainProtection = {
  username: string
  password?: string
  hash?: string
}

export type DeploymentPlannedDomain = {
  hostname: string
  https: boolean
  ownership: "managed" | "linked"
  protection?: DeploymentDomainProtection
  /** Another of the plan's hostnames this one sends visitors to, instead of serving the app. */
  redirectTo?: string
  /** Empty is 308, permanent and keeping the method. */
  redirectCode?: DeploymentRedirectCode
  /**
   * Served through the connected Cloudflare Tunnel rather than at this
   * server's address: Cloudflare terminates HTTPS, nothing is issued here,
   * and the name's route and CNAME are set as the release goes live.
   */
  tunnel?: boolean
  /**
   * The Compose service this name serves, where it is not the plan's primary
   * one: a stack gives each of its apps a name of its own. Kept as it is by
   * every form that edits the list.
   */
  service?: string
  /** That service's container port; empty is its only one published on loopback. */
  port?: number
}

export type DeploymentRedirectCode = 301 | 302 | 307 | 308

/**
 * A tailnet-only publication. `allow`, when it names anybody, is who may open
 * it — Tailscale logins (alice@example.com, compared without case) and ACL
 * tags (tag:ci, compared exactly) — and the proxy then asks the dashboard
 * about every request; empty lets everyone on the tailnet in. Previews
 * inherit it with the rest of the plan.
 */
export type DeploymentRouteTailnet = {
  allow?: string[]
}

/**
 * Sign-in in front of a deployment's public route: a Proxy site's single
 * sign-on on every path, which previews inherit with the rest of the plan.
 */
export type DeploymentForwardAuth = {
  provider: ForwardAuthProvider
  /** The auth server's check endpoint, asked with nginx's auth_request convention. */
  verify: string
  /** Where a visitor without a session is sent, with rd= set to the address they asked for. */
  signIn: string
  /** Leaves this environment's route open and gates only the previews made from it. */
  previewsOnly?: boolean
}

/**
 * A deployment route's HSTS policy, sent on its HTTPS domains alone and held
 * to the bounds a Proxy site's control is: max-age in seconds (absent is six
 * months; five minutes to two years), the subdomains too, and preload, which
 * needs a year and the subdomains.
 */
export type DeploymentRouteHSTS = {
  maxAge?: number
  includeSubDomains?: boolean
  preload?: boolean
}

/** What a deployment's maintenance does while it is on. */
export type DeploymentRouteMaintenance = {
  /** Seconds the 503 tells clients and crawlers to wait; empty sends no Retry-After. */
  retryAfter?: number
  /** The addresses and ranges that reach the application as usual while it is on. */
  bypassFrom?: string[]
}

export type DeploymentErrorPage = 404 | 502 | 503 | 504

/** A page a deployment's route answers with: its maintenance page or an error page. */
export type DeploymentPageName = "maintenance" | "404" | "502" | "503" | "504"

/**
 * GET/PUT /deploy/{id}/environments/{env}/maintenance: the switch beside the
 * live route, which the route reads on every request.
 */
export type DeploymentMaintenanceSwitch = {
  driver: "nginx" | "caddy"
  /** The environment has a route on the proxy at all. */
  route: boolean
  /** The route carries the check, so the switch acts at once. */
  ready: boolean
  on: boolean
  /** When it was turned on (RFC 3339). */
  since?: string
}

export type DeploymentVariable = {
  name: string
  revision: number
  sensitivity: "plain" | "secret"
  scopes: ("build" | "runtime" | "release_task")[]
  masked: string
  valueDigest: string
  reference?: { kind: string; target: string }
  pending: boolean
  createdBy: string
  createdAt: string
  environmentId: number
  desiredRevision: number
}

/**
 * A value defined once and read by any environment whose own variable
 * references it: the server's as `${{server.NAME}}` by every project, a
 * project's as `${{project.NAME}}` by that project's environments. The list
 * never carries the value; `usedBy` names every variable that reads it.
 */
export type DeploymentSharedVariable = {
  scope: "server" | "project"
  projectId?: number
  name: string
  sensitivity: "plain" | "secret"
  masked: string
  valueDigest: string
  /** The literal a variable holds to read this one. */
  reference: string
  updatedBy: string
  createdAt: string
  updatedAt: string
  usedBy: DeploymentSharedVariableUse[]
}

export type DeploymentSharedVariableUse = {
  projectId: number
  projectName: string
  environmentId: number
  environmentName: string
  environmentKind: string
  /** The environment's own variable that holds the reference. */
  variable: string
  /** Its live release started before the value last changed, so it runs the earlier one until it deploys again. */
  waiting: boolean
}

/**
 * POST …/variables/import?dryRun=1: what importing the same body would do to
 * each name, in the order written, compared by digest on the server. One
 * refused name refuses the whole import; `reason` says which line to fix.
 */
export type DeploymentDotenvImportPreview = {
  variables: {
    name: string
    line: number
    /** "skipped" is a name the request asked the import to leave out. */
    change: "added" | "changed" | "unchanged" | "skipped" | "refused"
    reason?: "invalid_name" | "duplicate" | "invalid_value"
  }[]
}

export type DeploymentPendingChange = {
  kind: string
  name: string
  change: "added" | "changed" | "removed"
  beforeDigest?: string
  afterDigest?: string
}

export type DeploymentPendingState = {
  pending: boolean
  desiredRevision: number
  liveReleaseId?: number
  livePlanRevision?: number
  changes: DeploymentPendingChange[]
}

export type DeploymentEnvironmentConfiguration = Omit<DeploymentConfiguration, "variables"> & {
  revision: number
  variables: DeploymentVariable[]
  pending: DeploymentPendingState
  /** Present once the backend fills it in; until then the Source card falls back to the summary. */
  source?: DeploymentDraftSource
  identity?: SourceIdentity
  /** The detected candidate this build still describes, from the evidence saved with the plan. */
  detected?: DeploymentDetectionCandidate
}

/**
 * One database bound to an environment's managed network, as reconciliation
 * last observed it. `detail` is why the last pass could not repair a binding;
 * it is empty while the database is connected.
 */
export type DeploymentDatabaseLink = {
  connectionId: number
  name: string
  driver: DbDriver
  database: string
  network: string
  hostname: string
  /** The connection has a pooler, so `${{database.N.pooled}}` resolves. */
  pooler?: boolean
  /** What the environment's previews get of it: nothing, a branch, or a masked branch. */
  previews?: PreviewBranchPolicy
  status: "pending" | "connected" | "stale" | "unavailable"
  detail?: string
  checkedAt?: string
}

/**
 * An environment's migration rehearsal terms (`deploy/rehearsal.go`): before
 * its release tasks run against production, they run against a copy of the
 * database restored from its newest backup.
 */
export type RehearsalPolicy = {
  enabled: boolean
  /** The database to rehearse against when the tasks reach several; 0 is the only one. */
  connectionId: number
  /** What a rehearsal that cannot be carried out does to the release. */
  whenUnavailable: "block" | "warn"
  restoreTimeoutSeconds: number
  maxBackupMb: number
  updatedBy?: string
  updatedAt?: string
}

/** A severity as the scanner names it, worst first. */
export type VulnerabilitySeverity = "CRITICAL" | "HIGH" | "MEDIUM" | "LOW" | "UNKNOWN"

/**
 * An environment's vulnerability scanning terms (`deploy/image_scan.go`):
 * whether its releases' images are scanned, and the lowest severity the
 * panel lists, the releases list counts and the alert is raised at.
 */
export type ImageScanPolicy = {
  enabled: boolean
  severityFloor: Exclude<VulnerabilitySeverity, "UNKNOWN">
  updatedBy?: string
  updatedAt?: string
}

export type VulnerabilityCounts = {
  critical: number
  high: number
  medium: number
  low: number
  unknown: number
}

/** One vulnerability in one package of a release's image. */
export type VulnerabilityFinding = {
  id: string
  package: string
  installed: string
  /** The version that fixes it; absent while no fix is published. */
  fixed?: string
  severity: VulnerabilitySeverity
  title?: string
  url?: string
  /** The OS packages ("alpine 3.12.12") or a lock file, with a Compose service in front. */
  target: string
  class?: string
  /** A base the release was built on carries it too, so a newer base may fix it. */
  fromBase?: boolean
  /** The pinned digests of the bases that carry it: only a newer image of one of them fixes it. */
  baseDigests?: string[]
  /** Where the package is in the image: its package.json, or the binary it is compiled into. */
  path?: string
  /**
   * For a package under node_modules: whether the application runs it (named in
   * `dependencies`, or needed by one that is) or only a development tool brought it.
   */
  dependency?: "direct" | "indirect" | "development"
  /** The dependency the application names that brought it in. */
  via?: string
  /** Whose it is: the application's, a development tool's shipped with it, or a base's. */
  origin?: VulnerabilityOrigin
  /** What the reader can do: upgrade the package, or rebuild to pick up a fix. */
  action?: "upgrade" | "rebuild"
}

/** Whose a finding is (`imagescan.FindingOrigin`). */
export type VulnerabilityOrigin = "app" | "development" | "base"

/** One image a scan read: the release's own, or a base it was pinned on. */
export type ImageScanTarget = {
  kind: "release" | "base"
  service?: string
  reference: string
  digest?: string
  state: "scanned" | "failed"
  reason?: string
  os?: string
  endOfLife?: boolean
  counts: VulnerabilityCounts
  scannedAt?: string
}

/** A base the release was pinned on, against what its tag names in the registry now. */
export type ReleaseBaseUpdate = {
  reference: string
  pinned?: string
  current?: string
  state: "current" | "outdated" | "unknown" | "local"
  reason?: string
  checkedAt?: string
}

/** A release's latest scan, read at its environment's floor. */
export type ImageScan = {
  releaseId: number
  releaseNumber: number
  environmentId: number
  state: "queued" | "scanning" | "scanned" | "failed"
  reason?: string
  trigger?: "build" | "schedule" | "manual"
  scanner?: string
  databaseUpdatedAt?: string
  databaseAgeHours?: number
  floor: ImageScanPolicy["severityFloor"]
  summary: {
    counts: VulnerabilityCounts
    fixableCounts: VulnerabilityCounts
    /** The fixable findings at or above the floor, worst first. */
    fixable: VulnerabilityFinding[]
    /** The findings at or above the floor nobody has published a fix for. */
    unfixed: VulnerabilityFinding[]
    truncated?: boolean
  }
  targets: ImageScanTarget[]
  baseUpdates: ReleaseBaseUpdate[]
  /** A base's tag names a newer image than the release was pinned on. */
  newerBase: boolean
  /** A build re-pins the bases (a recipe, static, Dockerfile or Compose build). */
  rebuildable: boolean
  /** Every finding counted by whose it is. */
  origins?: Record<VulnerabilityOrigin, VulnerabilityCounts>
  /** The fixable findings the reader can act on: an upgrade or a rebuild. */
  actionable?: VulnerabilityCounts
  requestedAt?: string
  startedAt?: string
  finishedAt?: string
}

/** A release's scan as its row in the releases list says it. */
export type ImageScanBadge = {
  releaseId: number
  state: ImageScan["state"]
  floor: ImageScanPolicy["severityFloor"]
  counts: VulnerabilityCounts
  /** Findings at or above the floor. */
  atFloor: number
  /** Of those, the ones with a published fix. */
  fixable: number
  /** Of those, the ones an upgrade or a rebuild fixes: all the row marks. */
  actionable?: number
  newerBase: boolean
  scannedAt?: string
}

/** GET /deploy/{id}/environments/{env}/vulnerabilities. */
export type ImageScanReading = {
  policy: ImageScanPolicy
  /** Whether this host has a scanner (Docker) at all; `reason` says why not. */
  available: boolean
  reason?: string
  scanner: string
  database: {
    installed: boolean
    updatedAt?: string
    nextUpdate?: string
    ageHours?: number
    lastError?: string
    source?: string
  }
  /** The release the scan is of: the live one unless another was asked for. */
  releaseId?: number
  scan: ImageScan | null
  /** The run the Rebuild button asks for, through the runs route. */
  rebuild?: { operation: "force_build"; sourceRevision?: string }
  rebuildReason?: string
  releases: ImageScanBadge[]
}

/** The rehearse_migration step's evidence. */
export type RehearsalReport = {
  rehearsed: boolean
  /** Why it was not rehearsed. */
  reason?: string
  database?: {
    connectionId: number
    name: string
    engine: "postgres" | "mysql" | "mariadb" | string
    database: string
    image: string
  }
  source?: {
    kind: "pitr" | "dump"
    backupRunId?: number
    takenAt?: string
    bytes?: number
    method?: string
  }
  restoreMs?: number
  tasks: { name: string; durationMs: number; exitCode: number; error?: string }[]
  locks: {
    read: boolean
    samples: number
    maxWaiting: number
    waitedMs: number
    held: { relation: string; mode: string; heldMs: number }[]
  }
  isolation?: { network: string; internal: boolean; published: boolean; volumesMounted: boolean }
  cleanedUp: boolean
  totalMs: number
}

/**
 * A plan's traffic replay terms (`deploy/replay.go`): before the cutover, the
 * last distinct GET and HEAD requests the access log recorded for the
 * environment's domains are sent to the candidate and its answers compared
 * with the live release's. An absent number is the server's default.
 */
/**
 * A plan's scale-to-zero terms (`deploy/sleep.go`): the live release's
 * container is stopped once its route has served no request for the idle
 * minutes, and the next request is held while it starts again. An absent
 * number is the server's default.
 */
export type SleepPlan = {
  enabled: boolean
  /** Minutes without a request before it sleeps; absent is 15, 5 to 1440. */
  idleMinutes?: number
  /** How long a wake waits for the readiness checks; absent is 60 seconds, 5 to 600. */
  wakeTimeoutSeconds?: number
}

/** One stretch an environment spent asleep. */
export type SleepSpan = {
  id: number
  environmentId: number
  releaseId: number
  containerId?: string
  idleMinutes: number
  reason: string
  lastRequestAt?: string
  sleptAt: string
  /** Absent while it is asleep now. */
  wokeAt?: string
  /** request, manual (Wake now), run, or repair (found started outside a wake). */
  wakeTrigger?: string
  /** The host a request asked for, the run, or who pressed Wake now. */
  wakeDetail?: string
  wakeSeconds?: number
  wakeError?: string
}

/** GET …/environments/{env}/sleep. */
export type SleepStatus = {
  /** The live release's terms, the ones in force. */
  enabled: boolean
  idleMinutes: number
  wakeTimeoutSeconds: number
  holdSeconds: number
  state: "off" | "awake" | "asleep" | "waking"
  since?: string
  reason?: string
  lastRequestAt?: string
  sleepsAt?: string
  switch?: { driver: string; ready: boolean; on: boolean; since?: string }
  /** The last seven days, newest first. */
  recent: SleepSpan[]
  asleepSeconds: number
}

/** What a wake came to. */
export type WakeOutcome = {
  status: number
  upstream?: string
  reason: string
  seconds?: number
}

export type ReplayPlan = {
  enabled: boolean
  /** Distinct requests replayed, newest first; absent is 50, at most 500. */
  requests?: number
  /** Only recorded paths under one of these prefixes; absent replays every path. */
  paths?: string[]
  /** The percent of replayed requests whose status class may differ; absent is 2. */
  maxStatusDrift?: number
  /** What the candidate's p95 may be of the recorded p95; absent is 2. */
  latencyFactor?: number
  /** The whole replay's time limit; absent is 60 seconds. */
  timeoutSeconds?: number
}

/** One replayed request the candidate answered in another status class, or not at all. */
export type ReplayDrift = {
  method: string
  path: string
  query?: string
  recordedStatus: number
  /** 0 is no answer, with `error` saying why. */
  candidateStatus: number
  error?: string
  recordedMs?: number
  candidateMs?: number
}

/** The replay_traffic step's evidence. */
export type ReplayReport = {
  replayed: boolean
  /** Why it was not replayed. */
  reason?: string
  domains?: string[]
  /** The candidate's own address the requests were sent to. */
  target?: string
  read: number
  eligible: number
  requests: number
  answered: number
  drifted: number
  driftPercent: number
  maxStatusDrift: number
  latencyFactor: number
  latencyRecorded: boolean
  latencyJudged: boolean
  recordedP95?: number
  candidateP95?: number
  verdict: {
    code: "within_drift" | "status_drift" | "latency" | "unanswered" | ""
    tripped: boolean
    reason: string
  }
  drifts: ReplayDrift[]
  concurrency: number
  timeoutSeconds: number
  timedOut: boolean
  durationMs: number
  recovery?: {
    candidateStopped: boolean
    runtimeRestored: boolean
    routeRestored: boolean
  }
}

/** A saved database a variable names by its address rather than a link. */
export type DeploymentDatabaseAddress = {
  connectionId: number
  name: string
  driver: DbDriver
  database: string
  /** The variables whose values hold its address. */
  variables: string[]
}

export type DeploymentRemovalTarget = {
  id: string
  kind: string
  resourceId: string
  displayName: string
  owner: string
  ownership: DeploymentOwnership
  data: boolean
  requiresAdmin: boolean
  confirmationType: "ordinary" | "typed"
  confirmationPhrase?: string
  deepLink?: string
  workingDirectory?: string
}

export type DeploymentRemovalPlan = {
  deploymentId: number
  archived: boolean
  targets: DeploymentRemovalTarget[]
  digest: string
  generatedAt: string
}

export type DeploymentBackupGateEvidence = {
  jobId: number
  runId?: number
  status: string
  startedAt?: string
  endedAt?: string
  fresh: boolean
  restoreTested: boolean
  databaseDumps?: number[]
  restoreVerificationId?: number
  restoreApplicationImage?: string
  restoreSchemaVersion?: string
  detail?: string
}

export type DeploymentPreflightFinding = {
  code: string
  severity: "pass" | "decision" | "warning" | "blocked" | "unavailable"
  title: string
  measured?: string
  means?: string
  action?: string
  owner?: string
  fieldId?: string
  deepLink?: string
  /** The one plan change the finding offers, when the check could compute it. */
  fix?: DeploymentCauseFix
}

/** One plan field whose saved value differs from what detection proposes now. */
export type DeploymentDetectionChange = {
  /** The plan field: `build.packageManager`, `runtime.internalPort`. */
  field: string
  label: string
  saved: string
  detected: string
  /** What detection proposed when the plan was saved. */
  previous?: string
  /** Detection's answer moved since the plan was saved; false is an edit made on purpose. */
  changed: boolean
}

/** Fresh detection of a project's source, compared with its saved plan (`.../detect`, a source change). */
export type DeploymentDetectionProposal = {
  /** The desired revision it was computed against — the guard an Apply saves with. */
  revision: number
  sourceRevision?: string
  candidate?: DeploymentDetectionCandidate
  /**
   * Detection found nothing at the plan's root that builds the plan's way:
   * `candidate` is what it selected instead, for information, and no field
   * is compared with it.
   */
  elsewhere?: boolean
  changes: DeploymentDetectionChange[]
  variables: DeploymentDetectedVariable[]
  newVariables: string[]
  databases: DeploymentDetectedDatabase[]
}

export type DeploymentDraft = {
  id: string
  ownerUsername: string
  currentStep: "intent" | "source" | "detection" | "configuration" | "preflight"
  revision: number
  /** Names saved encrypted on the server; values are never returned. */
  environmentKeys?: string[]
  data: {
    intent?: { name: string; profile: WorkloadProfile }
    source?: DeploymentDraftSource
    detection?: DeploymentDetection
    configuration?: DeploymentConfiguration
  }
  findings: DeploymentPreflightFinding[]
  planPreview: string
  committedProjectId?: number
  updatedAt: string
  expiresAt: string
}

export type DeploymentPreflight = {
  revision: number
  findings: DeploymentPreflightFinding[]
  expectedDowntime: boolean
  preview: string
  digest: string
  plan: {
    actions: {
      ordinal: number
      phase: string
      owner: string
      action: string
      arguments?: string[]
      changesState: boolean
    }[]
  }
}

export type DeployRun = {
  id: number
  runNumber: number
  projectId: number
  startedAt: string
  endedAt?: string
  status: "running" | "success" | "failed"
  trigger: string
  actor: string
  fromCommit: string
  toCommit: string
  log: string
  duration?: string
  rollbackable: boolean
}

export type DeployCommit = {
  sha: string
  short: string
  author: string
  date: string
  subject: string
}

/** A token or key the server holds so it can reach a private repository or registry. */
export type DeploymentCredentialKind =
  "git_bearer" | "git_ssh" | "registry" | "provider_token" | "github_app"

export type DeploymentCredential = {
  id: number
  name: string
  kind: DeploymentCredentialKind
  /** A host, e.g. "github.com" or "ghcr.io" — empty when the kind has none. */
  target: string
  username?: string
  createdAt: string
  updatedAt: string
  lastUsedAt?: string
  /** How many non-archived projects' current source uses it — 0 means safe to remove. */
  usedBy: number
  /** The projects behind `usedBy`, ascending; `usedBy` counts environments, so it can be larger. */
  usedByProjectIds?: number[]
  /**
   * The projects whose commit statuses and preview comments a provider token
   * writes. Nothing deploys through it, so this does not count as `usedBy`.
   */
  reportsForProjectIds?: number[]
}

/**
 * What a source actually resolved to the last time it was validated — the
 * adapter's own facts, not what was typed. Same shape as `DeploymentDetection`'s
 * `source`, which the same resolution path already produces.
 */
export type SourceIdentity = {
  kind: DeploymentSourceKind
  remote?: string
  repository?: string
  ref?: string
  revision?: string
  digest?: string
  os?: string
  architecture?: string
  platforms?: string[]
  localPath?: string
  dirty?: boolean
  composeFiles?: string[]
  services?: string[]
}

/**
 * What a deployment served, as the ingress recorded it.
 *
 * The container's own output answers "what did the application print", which
 * for a modern framework is a startup banner and then nothing at all. This is
 * the other half of the Logs page: every request that reached the deployment,
 * whether or not the application chose to say anything about it.
 */
export type RequestEntry = {
  /**
   * Where this request sits in the server's record of its route: a number
   * that only rises. A live tail continues from it exactly, where "newer than
   * the last timestamp" drops the second of two requests in one second — which
   * is every request, in nginx's format.
   */
  seq?: number
  time: string
  method: string
  path: string
  query?: string
  host?: string
  proto?: string
  status: number
  size: number
  remoteIp?: string
  userAgent?: string
  referer?: string
  /** Absent where the format carries no duration — nginx's stock `combined` does not. */
  durationMs?: number
  tls?: boolean
  /**
   * The W3C trace context the ingress sent the application with this request
   * (`traceparent`), and the client's span it continued. Absent on a route
   * rendered before the ingress sent one, and on every Proxy site.
   */
  traceId?: string
  spanId?: string
  parentSpanId?: string
  /** The Tailscale login (or a tagged machine's tags) a tailnet gate admitted. */
  user?: string
  /** The release that answered, on a route that records which (a canary's split). */
  release?: string
  /** The edge rule that decided this request instead of the application. */
  edgeRule?: string
}

export type RequestFacet = {
  value: string
  count: number
  /** How many of those were 5xx, so the busiest and the failing are one table. */
  errors: number
  bytes?: number
  p95?: number
  /** How many were answered 4xx, and how many of those looked like a scanner's probe. */
  refused?: number
  probes?: number
}

/** One column of the request chart, counted by status family. */
export type RequestBucket = {
  start: string
  total: number
  counts: Record<string, number>
  p95?: number
  /** What the column's answers sent, for the Served reading's line. */
  bytes?: number
  /** The distinct people who opened a page in the column. */
  visitors?: number
}

/**
 * One person in the window: an address and the browser it sent, once it has
 * opened a page. A script, a crawler or a scanner's probe is never one.
 */
export type RequestVisitor = {
  client: string
  /** The whole user agent, which the page reads as a browser on a system. */
  agent?: string
  requests: number
  pages: number
  /** 5xx answers they met, and 4xx. */
  errors: number
  refused: number
  bytes: number
  first: string
  last: string
  /** The first page they opened, and the outside site they came from. */
  landing?: string
  referer?: string
  /** The pages they opened, in order of first opening, at most six. */
  journey: string[]
}

/** Who the traffic was: the people, told apart from the programs. */
export type RequestAudience = {
  visitors: number
  /** Distinct addresses whose agent is a crawler, a scanner or a script. */
  bots: number
  humanRequests: number
  botRequests: number
  /** Counted per visitor, not per request. */
  devices: RequestFacet[] | null
  browsers: RequestFacet[] | null
  systems: RequestFacet[] | null
  top: RequestVisitor[] | null
}

/** The distribution, not an average: a p50 of 30ms hides a p99 of nine seconds. */
export type RequestLatency = {
  p50: number
  p75: number
  p90: number
  p95: number
  p99: number
  max: number
  mean: number
}

export type RequestSummary = {
  total: number
  scanned: number
  classes: Record<string, number>
  /** Shares of the window, 0–1. Two readings because they are two people's problem. */
  errorRate: number
  clientErrorRate: number
  bytes: number
  latency?: RequestLatency
  /** Arrival rate, so the figure means the same thing whichever range is chosen. */
  perMinute: number
  /** Page views: documents a person opened, not what a page load dragged in or a scanner's probes. */
  pages: number
  methods: RequestFacet[]
  statuses: RequestFacet[]
  paths: RequestFacet[]
  hosts: RequestFacet[]
  clients: RequestFacet[]
  agents: RequestFacet[]
  /** The sites traffic arrived from, by host; the site's own links are not a source. */
  referers: RequestFacet[]
  /** Refused paths that look like scanning, and the clients that asked for several. */
  probes: RequestFacet[]
  scanners: RequestFacet[]
  /** How many requests each edge rule decided in the window; absent from older servers. */
  edgeRules?: RequestFacet[]
  audience?: RequestAudience
  buckets: RequestBucket[]
  bucketSeconds: number
  first?: string
  last?: string
  /** The scan hit its own bound, so every figure above is a floor. */
  truncated: boolean
}

/**
 * What the server holds of a route's record, so an empty window can explain
 * itself: whether a record exists at all, how far back what is held reaches,
 * whether that is the whole retained record or a tail of it, and how fresh.
 */
export type RequestCoverage = {
  exists: boolean
  from?: string
  to?: string
  held: number
  complete: boolean
  /** The newest request's sequence — what a live tail continues from. */
  cursor: number
  refreshedAt: string
  /** The last read failed; these figures are from the previous successful one. */
  stale?: boolean
  /** What the dashboard has kept of this route's record, past what the proxy still holds. */
  history?: RequestHistory
}

/**
 * The request history the dashboard keeps for a route: recorded as the
 * proxy writes it, whether or not anybody has the page open, and kept for
 * `days` — so a window reaching before `from` is answered as far as that,
 * and said to be.
 */
export type RequestHistory = {
  /** The first request kept; absent while nothing has been recorded. */
  from?: string
  days: number
  /** Whether the recorder follows this route now. */
  recording: boolean
}

export type DeploymentRequests = {
  status: "available" | "unavailable"
  reason?: string
  /** Which server wrote the record: the two drivers do not record the same things. */
  driver?: string
  format?: string
  /** Whether this format carries a request duration at all. */
  latency: boolean
  /**
   * Where the proxy says why it failed a request: the Caddy ingress
   * container, whose output carries its error lines, or nginx's error file
   * for the site. At most one is set.
   */
  ingress?: string
  errorLog?: string
  /** What is held reaches the start of the retained record; false means every figure is a floor. */
  complete: boolean
  observedAt: string
  entries: RequestEntry[]
  slowest?: RequestEntry[]
  summary: RequestSummary
  coverage: RequestCoverage
  /** The kept history, where the server reports it beside the coverage rather than in it. */
  history?: RequestHistory
}

/** A project's last hour on a card: alive, failing, and a line to draw. */
export type TrafficPulse = {
  status: "available" | "unavailable"
  perMinute: number
  errorRate: number
  pages: number
  points: number[]
}

export type TrafficAlertKind = "error_rate" | "latency" | "silence"

/**
 * A rule over a deployment's request record, told to the notification
 * channels once when it crosses its line and once when it comes back.
 */
export type TrafficAlert = {
  id: number
  projectId: number
  environmentId: number
  kind: TrafficAlertKind
  /** A percentage for error_rate, milliseconds for latency, unused for silence. */
  threshold: number
  windowMinutes: number
  /** Channel ids; empty means every enabled channel. */
  channels: number[]
  enabled: boolean
  state: "ok" | "firing"
  stateSince?: string
  observed: number
  checkedAt?: string
  firedAt?: string
  createdAt: string
  updatedAt: string
}

export type TrafficAlertList = {
  alerts: TrafficAlert[]
  kinds: TrafficAlertKind[]
}

/** What Docker did to this deployment's containers, as opposed to what they printed. */
export type DeploymentLifecycle = {
  status: "available" | "unavailable"
  reason?: string
  /** Whether the event stream is connected, so an empty feed can explain itself. */
  watching: boolean
  /** When this process connected to the stream; the record itself reaches back to `recordSince`. */
  since?: string
  /**
   * The oldest event the dashboard keeps for any deployment: how far back the
   * feed can see. The record survives a restart of the dashboard; absent until
   * the first event has been kept.
   */
  recordSince?: string
  /** How long an event is kept. */
  retentionDays: number
  events: DockerEvent[]
}

/** The figure over one span of time ending now (`deploy/availability.go`). */
export type AvailabilityWindow = {
  /** "24h", "7d" or "30d". */
  window: string
  seconds: number
  /** Live-release time inside the window the record covers: what `ratio` is over. 0 means no figure. */
  observedSeconds: number
  downSeconds: number
  incidents: number
  /** 1 - down/observed, 0..1; absent while nothing was observed. */
  ratio?: number
  /** How much of the window the event record reaches back over. */
  coverageSeconds: number
}

/** How much of the last day, week and month a deployment's live release was up. */
export type DeploymentAvailability = {
  status: "available" | "unavailable"
  reason?: string
  observedAt: string
  /** When the event record began, for any deployment. */
  recordSince?: string
  /** The number of the release live now. */
  liveRelease?: number
  /** Whether the record says the live release is down at this moment. */
  down: boolean
  windows: AvailabilityWindow[]
  /** The last thirty stretches of 24 hours ending now, oldest first. */
  days: AvailabilityDay[]
}

/** One 24-hour stretch of the record. 0 observed is unknown, not up. */
export type AvailabilityDay = {
  from: string
  observedSeconds: number
  downSeconds: number
  incidents: number
}
