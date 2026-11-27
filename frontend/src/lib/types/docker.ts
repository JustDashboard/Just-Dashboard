/*
 * Docker: containers and their readings, ports, images, volumes, networks,
 * compose stacks, disk usage, events, and the spec a new container is
 * created from.
 */

/**
 * One container's recent shape, for a table row.
 *
 * Buckets are maxima rather than means: at sixty pixels wide a mean flattens
 * exactly the spike the thumbnail exists to surface.
 */
export type ContainerSparkline = {
  name: string
  cpu: number[]
  mem: number[]
  cpuPeak: number
  memPeak: number
}

/** One bucket of a single container's recorded history. */
export type ContainerHistoryPoint = {
  ts: string
  samples: number
  /** Null for a bucket whose samples had no CPU interval: the first after a start. */
  cpu: number | null
  cpuPeak: number | null
  mem: number
  memPeak: number
  memBytes: number
  memBytesPeak: number
  memLimit: number
  /** The bucket's mean process count; `pidsPeak` is its highest. */
  pids: number
  pidsPeak?: number
  /** Bytes per second, differenced from the cumulative counters Docker reports. */
  netRx: number | null
  netTx: number | null
  blockRead: number | null
  blockWrite: number | null
  netRxPeak?: number | null
  netTxPeak?: number | null
  blockReadPeak?: number | null
  blockWritePeak?: number | null
}

export type ContainerHistory = {
  name: string
  from: string
  to: string
  stepSeconds: number
  sampleIntervalSeconds: number
  retentionSeconds: number
  earliest: string | null
  points: ContainerHistoryPoint[]
}

export type ContainerPort = { ip?: string; privatePort: number; publicPort?: number; type: string }

/**
 * Where a published port can be reached from, as far as the binding alone can
 * say. `0.0.0.0:5432` and `127.0.0.1:5432` are one character apart and could
 * not differ more in consequence; every panel used to draw them identically.
 *
 * The vocabulary is about binding, not reachability. Docker publishes a port
 * with NAT rules the firewall never sees, so "bound to every interface" is a
 * fact and "reachable from the internet" is a conclusion that needs the
 * firewall too — see PortRoute, which is where that conclusion is drawn.
 */
export type PortScope = "loopback" | "private" | "all" | "internal"

export type PortExposure = {
  hostIp?: string
  hostPort?: number
  containerPort: number
  protocol: string
  scope: PortScope
  ipv6?: boolean
  /** The badge. */
  label: string
  /** The sentence behind it. */
  summary: string
}

/** One published port traced from the container out to the firewall. */
export type PortRoute = {
  hostIp?: string
  hostPort: number
  containerPort: number
  protocol: string
  public: boolean
  scope: PortScope
  label: string
  binding: string
  vhost?: string
  url?: string
  tls?: boolean
  firewall: {
    known: boolean
    backend?: string
    enabled?: boolean
    verdict: "allowed" | "denied" | "default" | "unknown"
    rule?: string
    defaultIncoming?: string
    /** Docker's NAT rules are consulted before ufw's filter chain. */
    dockerBypass?: boolean
  }
  reach: "server-only" | "proxied" | "external" | "blocked" | "unknown"
  reasoning: string
  /** True when the verdict was worked out rather than read. */
  inferred: boolean
}

export type Container = {
  id: string
  names: string[]
  name: string
  image: string
  imageId: string
  command: string
  state: string
  status: string
  health?: string
  createdAt: string
  startedAt?: string
  uptimeSeconds: number
  ports: ContainerPort[]
  labels: Record<string, string>
  networks: string[]
  composeStack?: string
  composeService?: string
  /** Writable-layer size. Present only when the listing was asked for sizes. */
  sizeRw?: number
  /** The port list with its meaning attached, computed on the server. */
  exposure: PortExposure[]
  /**
   * What the container was told it may use, and whether anything watches it.
   * Only running containers are inspected for these — `inspected` says whether
   * a zero here is an answer or an absence.
   */
  memoryLimit?: number
  cpuLimit?: number
  hasHealthcheck: boolean
  restartPolicy?: string
  privileged?: boolean
  inspected: boolean
}

export type ContainerDetail = Container & {
  env: string[]
  mounts: {
    type: string
    name?: string
    source: string
    destination: string
    mode: string
    rw: boolean
  }[]
  networkMode: string
  networkDetails: {
    name: string
    ipAddress: string
    gateway: string
    macAddress: string
    aliases: string[]
    networkId: string
  }[]
  restartPolicy: string
  privileged: boolean
  capAdd: string[]
  logPath: string
  exitCode: number
  error?: string
  restartCount: number
  entrypoint: string[]
  workingDir: string
  user: string
}

export type ContainerStats = {
  id: string
  name: string
  ts: string
  cpuPercent: number
  cpuReady?: boolean
  memUsage: number
  memRaw?: number
  memCache?: number
  memRss?: number | null
  memSwap?: number | null
  memLimit: number
  memPercent: number
  netRx: number
  netTx: number
  blockRead: number
  blockWrite: number
  /** True only when somebody set a limit. Docker reports host RAM otherwise. */
  memLimited: boolean
  /** Of the whole machine, and therefore always meaningful. */
  memHostPercent?: number
  /** 100% is one core. hostCpus is what the reader needs to know that. */
  hostCpus?: number
  cpuLimit?: number
  pids: number
  onlineCpus: number
  /** Cumulative nanosecond totals. Only meaningful as a difference between two samples. */
  cpuTotal: number
  systemCpu: number
  networks?: Record<string, ContainerNetworkCounters>
  networkAvailable?: boolean
  blockAvailable?: boolean
  pidsLimit?: number
  cpuPeriods?: number
  cpuThrottledPeriods?: number
  cpuThrottledTime?: number
}

export type ContainerNetworkCounters = {
  rxBytes: number
  txBytes: number
  rxPackets: number
  txPackets: number
  rxErrors: number
  txErrors: number
  rxDropped: number
  txDropped: number
}

export type DockerImage = {
  id: string
  repoTags: string[]
  repoDigests: string[]
  size: number
  created: string
  containers: number
  labels: Record<string, string>
  dangling: boolean
}

export type DockerVolume = {
  name: string
  driver: string
  mountpoint: string
  createdAt: string
  scope: string
  labels: Record<string, string>
  size: number
  refCount: number
  inUse: boolean
  /** What a local volume's driver options mount (nfs, cifs, bind); a prune skips such a volume. */
  mountType?: string
}

export type DockerNetwork = {
  id: string
  name: string
  driver: string
  scope: string
  internal: boolean
  attachable: boolean
  ipv6: boolean
  created: string
  labels: Record<string, string>
  subnets: string[]
  containers: number
  /**
   * The containers attached, joined on the server from the container listing.
   * Docker's own network listing leaves its container map empty, so `containers`
   * was structurally zero on every host until this was joined in.
   */
  usedBy: string[]
  /** The host device a bridge network is carried on: `docker0`, `br-1a2b3c4d5e6f`. */
  bridge?: string
  /** The host's own address on it, which its containers route through. */
  gateway?: string
  /**
   * Each attached container's address, from the same listing as `usedBy`. The
   * listing has no aliases; a network's own inspect (`members`) does.
   */
  endpoints?: NetworkEndpoint[]
  /**
   * False where the container listing failed: `usedBy` is then unread rather
   * than empty, and nothing may treat the network as unused. Absent from older
   * backends, which is read as known.
   */
  membersKnown?: boolean
  membersError?: string
  /** Who created the network, by its labels. */
  owner?: NetworkOwner
}

export type NetworkEndpoint = {
  container: string
  name: string
  /** With its prefix, as Docker writes it: `172.18.0.4/16`. Absent while stopped. */
  ipv4?: string
  ipv6?: string
  mac?: string
}

/**
 * Who created a Docker network and so who acts on it next: Compose recreates
 * its own, a deployment reconciles its own, nothing comes back for a manual one.
 */
export type NetworkOwner = {
  kind: "system" | "dashboard" | "database-link" | "deployment" | "compose" | "manual"
  project?: string
  composeNetwork?: string
  environmentId?: number
  /** The deployment's "project · environment", while that environment exists. */
  deployment?: string
}

/**
 * The part of `docker info` the Networks page reads, in the daemon's own field
 * names: the engine, and the pools new networks are carved from. Docker
 * reports no pools unless `default-address-pools` is set, and then allocates
 * from its built-in ones.
 */
export type DockerNetworkingInfo = {
  Name?: string
  ServerVersion?: string
  DefaultAddressPools?: { Base: string; Size: number }[] | null
}

export type ComposeService = {
  name: string
  container: string
  state: string
  status: string
  image: string
  health?: string
  ports: ContainerPort[]
  /** Declared by the compose file with no container behind it. */
  missing?: boolean
}

/**
 * A compose stack is two objects wearing one name: a *configuration* on disk,
 * which may never have been deployed, and a *deployed project*, a set of
 * containers Docker labelled. `running/total` used to count containers on both
 * sides of the slash, so a stack that existed only as a file read "0/0 up".
 */
export type StackState = "running" | "partial" | "degraded" | "stopped" | "not-deployed" | "unknown"

export type ComposeStack = {
  name: string
  workingDir: string
  configFiles: string[]
  services: ComposeService[]
  running: number
  /** Services the compose file declares — not containers that exist. */
  total: number
  managed: boolean
  declared: string[]
  /** "compose" resolves includes and profiles; "file" is a direct YAML read. */
  declaredSource?: "compose" | "file"
  containers: number
  deployed: boolean
  /** Running containers this project labels that the file no longer declares. */
  orphans: string[]
  state: StackState
  /** The sentence: "Running · 4/4 services", "Not deployed · 3 services defined". */
  summary: string
}

export type StackDetail = ComposeStack & {
  configPath?: string
  declaredError?: string
  git?: {
    path: string
    branch?: string
    dirty: boolean
    changes: number
    ahead: number
    behind: number
    commit?: string
    subject?: string
  }
}

export type ComposeValidation = {
  valid: boolean
  error?: string
  normalised?: string
  services: string[]
}

/** One line of `docker system df`. */
export type DockerDiskUsageLine = {
  total: number
  active: number
  size: number
  /**
   * What a prune would actually give back — Docker's own figure, which counts
   * an unused image's shared layers as staying put. The naive "size of the
   * things nothing is using" is always larger and is what made the old
   * reclaim button look broken.
   */
  reclaimable: number
}

/** What one figure on the disk page actually measures. */
export type DiskDefinition = {
  key: string
  label: string
  measures: string
  excludes?: string
  /** Which Docker API it came from — two figures from different ones may not be comparable. */
  source: string
}

/** One container's writable layer. */
export type ContainerDisk = {
  id: string
  name: string
  state: string
  stack?: string
  /** Written into the container's own filesystem. Volumes and bind mounts excluded. */
  sizeRw: number
  /** Writable layer plus the image beneath it — Docker's "virtual size". */
  sizeRootFs: number
}

export type DockerDiskUsage = {
  layersSize: number
  /** Every image's own size, summed. Larger than what the disk holds. */
  imagesSize: number
  containersSize: number
  volumesSize: number
  buildCacheSize: number
  /**
   * The gap between adding up every image's size and what the images occupy.
   * Both figures are true; naming the difference is what stops the page
   * reading as arithmetic that does not work.
   */
  sharedLayers: number
  writable: ContainerDisk[]
  definitions: DiskDefinition[]
  images: DockerDiskUsageLine
  containers: DockerDiskUsageLine
  volumes: DockerDiskUsageLine
  buildCache: DockerDiskUsageLine
}

/** One category of removable thing, with what removing it costs. */
export type CleanupCategory = {
  key: string
  label: string
  items: number
  reclaimable: number
  cost: string
  /** Exactly one category destroys data. */
  destroys: boolean
  examples: string[]
  recommended: boolean
}

export type CleanupPreview = {
  categories: CleanupCategory[]
  safeTotal: number
  summary: string
}

export type PruneReport = {
  kind: string
  spaceReclaimed: number
  items: string[]
  /** Set when this part of a sweep failed while the rest carried on. */
  error?: string
}

export type DockerEvent = {
  time: string
  type: string
  action: string
  name: string
  id?: string
  image?: string
  stack?: string
  exitCode?: string
  /** The compose service: what a stack's log names the container by (`db`, not `shop-db-1`). */
  service?: string
  /** The container a network's connect or disconnect moved, by id. */
  container?: string
  /**
   * The dashboard's own labels off the object this happened to, with the
   * `io.just-dashboard.` prefix stripped — `environment-id`, `release-id`,
   * `release-number` and `run-id` for a deployment's container. It is what
   * lets a project page show its own restarts without inspecting a container
   * that is by then already gone, and link a row to the run that made it.
   */
  owner?: Record<string, string>
  message: string
  level: "info" | "notice" | "error"
  /**
   * Who did this, as far as the event can say. "dashboard" is set by the
   * server after correlating against the audit log — Docker records what
   * happened and never who asked, so anything else is "external".
   */
  source: "dashboard" | "compose" | "daemon" | "docker"
  trigger?: {
    auditId: number
    action: string
    actor: string
    /** Always "likely": a time window and a name, not a causal record. */
    confidence: string
  }
}

export type DockerEventFeed = {
  events: DockerEvent[]
  listening: boolean
  since: string
  buffered: number
}

/** The part of `docker info` a page names the engine by, in Docker's own field names. */
export type DockerEngineInfo = {
  ServerVersion: string
  Driver: string
  Images: number
}

/**
 * The part of `docker info` the overview's identity line reads. The route
 * passes the daemon's own document through, so the names are its casing and
 * every field it adds in a later release is simply not read here.
 */
export type DockerInfo = {
  Name?: string
  ServerVersion?: string
  OperatingSystem?: string
  Architecture?: string
  /** The storage driver: overlay2, btrfs, zfs. */
  Driver?: string
  CgroupVersion?: string
  NCPU?: number
  MemTotal?: number
  Images?: number
}

/** Whether the tag a container runs still points where it did when pulled. */
export type ImageUpdateStatus = {
  ref: string
  /** `pinned` is a digest reference: it cannot change, so there is nothing to check. */
  state: "current" | "outdated" | "unknown" | "local" | "pinned"
  localDigest?: string
  remoteDigest?: string
  reason?: string
  checkedAt: string
}

export type ImageDetail = {
  id: string
  repoTags: string[]
  repoDigests: string[]
  size: number
  created: string
  architecture?: string
  os?: string
  author?: string
  labels: Record<string, string>
  entrypoint: string[]
  command: string[]
  workingDir?: string
  user?: string
  exposedPorts: string[]
  volumePaths: string[]
  env: string[]
  layers: {
    id: string
    created: string
    createdBy: string
    size: number
    comment?: string
    tags: string[]
  }[]
  usedBy: { id: string; name: string; state: string; stack?: string; service?: string }[]
  /** What the reference is, and therefore what can be done with it. */
  kind: "tag" | "digest" | "id" | "dangling" | "unknown"
  ref: string
  pullable: boolean
  checkable: boolean
  movingTag: boolean
  dangling: boolean
  localBuild: boolean
}

export type VolumeUser = {
  id: string
  name: string
  state: string
  destination: string
  readOnly: boolean
  stack?: string
}

export type VolumeDetail = DockerVolume & {
  usedBy: VolumeUser[]
  options?: Record<string, string>
}

export type NetworkMember = {
  id: string
  name: string
  ipv4?: string
  ipv6?: string
  mac?: string
  aliases: string[]
  state?: string
  stack?: string
  /** Its own inspect failed, so its aliases are unknown rather than none. */
  unread?: boolean
  /** Its other networks: the containers on two networks are what joins them. */
  networks?: string[]
  /** The shared public Caddy, and the dashboard's own containers: never detached. */
  ingress?: boolean
  dashboard?: boolean
}

export type NetworkDetail = DockerNetwork & {
  options?: Record<string, string>
  members: NetworkMember[]
  system: boolean
}

/**
 * Where a container's writable layer went, with how it was measured attached.
 * "Writable layer: 38.7 GB" is true and useless; which directory holds it, and
 * whether that directory survives a recreate, is the answer.
 */
export type WritableEntry = {
  path: string
  size: number
  /** The mount covering this path, empty when nothing does. */
  mounted?: string
  /** Inferred from the path — labelled as inferred wherever it is shown. */
  persistent: boolean
  kind: "data" | "logs" | "cache" | "temporary" | "other"
}

export type WritableLayerReport = {
  containerId: string
  name: string
  measuredAt: string
  /** Docker's figure. `accounted` is what the breakdown adds up to. */
  total: number
  accounted: number
  state: "measured" | "unavailable" | "failed"
  method?: string
  reason?: string
  entries: WritableEntry[]
  /** Directories holding data that no volume covers — the point of the feature. */
  unbacked: WritableEntry[]
}

export type MigrationPlan = {
  container: string
  service?: string
  stack?: string
  path: string
  size: number
  volume: string
  steps: { title: string; detail: string; reversible: boolean }[]
  commands: string[]
  composePatch?: string
  warnings: string[]
}

/** What a deploy is expected to change, before it changes it. */
export type ServiceChange = {
  name: string
  change: "recreate" | "start" | "create" | "remove" | "unchanged"
  reason: string
  fields: string[]
  imageBefore?: string
  imageAfter?: string
  inferred: boolean
}

export type DiffLine = {
  kind: "same" | "added" | "removed" | "gap"
  text: string
  section?: string
  /** The file's last line, with no newline after it (an exact diff only). */
  noNewline?: boolean
}

export type DeployPreview = {
  project: string
  action: string
  services: ServiceChange[]
  recreate: number
  start: number
  unchanged: number
  create: number
  remove: number
  /** The one number that means data is destroyed. Almost always empty. */
  volumesRemoved: string[]
  volumesKept: string[]
  diff: DiffLine[]
  diffAgainst?: string
  summary: string
  /** What this cannot know. Compose makes the final call. */
  caveats: string[]
}

/** One recorded state of a compose project, so a change is reversible. */
export type StackDeployment = {
  id: number
  project: string
  workingDir?: string
  createdAt: string
  configHash: string
  /** Only on the single-record route; the list omits it. */
  config?: string
  services: string[]
  /** What each service was actually running — the digest, not the tag. */
  imageDigests: Record<string, string>
  envHash?: string
  gitCommit?: string
  gitBranch?: string
  gitDirty?: boolean
  actor?: string
  source?: string
  action?: string
  result?: string
  detail?: string
  /** Only on the single-record route: whether the images still exist. */
  restorable?: boolean
  missing?: string[]
}

export type FileChange = { path: string; kind: "modified" | "added" | "deleted" }

/**
 * The dashboard's description of a container to run — the shape the create
 * form speaks, translated to the Engine's ninety fields on the server.
 */
export type ContainerSpec = {
  name: string
  image: string
  command?: string[]
  entrypoint?: string[]
  env?: { name: string; value: string }[]
  ports?: PortMapping[]
  mounts?: MountSpec[]
  devices?: { host: string; container?: string; permissions?: string }[]
  labels?: { name: string; value: string }[]
  health?: HealthSpec
  limits: ResourceLimits
  networks?: string[]
  networkMode?: string
  hostname?: string
  extraHosts?: string[]
  dns?: string[]
  restartPolicy?: string
  maxRetries?: number
  /**
   * The log driver and its options. Present on the spec so an edit-and-recreate
   * keeps it — a field the round trip cannot carry is a setting silently reset
   * to Docker's unbounded default on the way through.
   */
  logging?: { driver?: string; options?: Record<string, string> }
  workingDir?: string
  user?: string
  stopSignal?: string
  privileged?: boolean
  capAdd?: string[]
  capDrop?: string[]
  init?: boolean
  autoRemove?: boolean
  tty?: boolean
  openStdin?: boolean
  readOnlyRootfs?: boolean
  pull?: "missing" | "always"
  start: boolean
}

export type PortMapping = {
  hostIp?: string
  hostPort: number
  containerPort: number
  protocol?: string
}

export type MountSpec = {
  /** "volume" is Docker-managed storage, "bind" a folder on the server, "tmpfs" memory. */
  type: "volume" | "bind" | "tmpfs"
  source?: string
  target: string
  readOnly?: boolean
  sizeMb?: number
}

export type HealthSpec = {
  test: string[]
  intervalSeconds?: number
  timeoutSeconds?: number
  startPeriodSeconds?: number
  retries?: number
  disable?: boolean
}

export type ResourceLimits = {
  memoryMb?: number
  memorySwapMb?: number
  cpus?: number
  pidsLimit?: number
  shmSizeMb?: number
}

export type CreateResult = {
  ports: PortMapping[]
  id: string
  name: string
  warnings: string[]
  started: boolean
}

export type SpecPreview = { run: string; compose: string }
