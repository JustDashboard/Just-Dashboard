export type Capability =
  "read" | "service.control" | "file.write" | "terminal" | "destructive" | "system.admin"

export type Role = "admin" | "limited" | "readonly"

export type DashboardUser = {
  id: number
  /** The sign-in key: lower case, one word, what the audit log names. */
  username: string
  /** The name shown — the username as typed at creation until it is changed. */
  displayName: string
  /** When the picture was last set, or 0 for none; the avatar URL's cache key. */
  avatarVersion: number
  role: Role
  totpEnabled: boolean
  disabled: boolean
  mustChangePassword: boolean
  lastLoginAt: string
  createdAt: string
}

export type AuthStatus = {
  authenticated: boolean
  user?: DashboardUser
  capabilities?: Capability[]
  needsTotp: boolean
  needsEnrollment: boolean
  needsPasswordChange?: boolean
  require2fa: boolean
}

export type HostInfo = {
  hostname: string
  os: string
  platform: string
  platformVersion: string
  kernelVersion: string
  kernelArch: string
  virtualization: string
  bootTime: string
  uptimeSeconds: number
  processes: number
  cpuModel: string
  cpuCores: number
  cpuMhz: number
}

export type Snapshot = {
  ts: string
  cpu: {
    totalPercent: number
    perCore: number[]
    loadAvg1: number
    loadAvg5: number
    loadAvg15: number
    cores: number
    /** Where the time went. `steal` is the one a total can never express. */
    modes: CPUModes
  }
  memory: {
    total: number
    used: number
    free: number
    available: number
    cached: number
    buffers: number
    usedPercent: number
  }
  swap: { total: number; used: number; free: number; usedPercent: number }
  mounts: MountStats[]
  net: NetStats[]
  uptimeSeconds: number
  pressure: Pressure
  sockets: Sockets
  procs: ProcCounts
  /** Live-only readings the recorder does not keep; absent on an older backend. */
  files?: FileHandles
  sensors?: SensorReading[] | null
}

/**
 * Open file descriptions against the kernel's ceiling.
 *
 * `max` is 0 when the kernel reports no real limit — some container runtimes
 * hand out the 64-bit maximum — so a page must not divide by it.
 */
export type FileHandles = {
  open: number
  max: number
}

/**
 * One temperature the board reports. `high` and `critical` are the sensor's
 * own thresholds, 0 where the driver has none: a CPU package idles where an
 * NVMe drive throttles, and only the driver knows which this is.
 */
export type SensorReading = {
  name: string
  tempC: number
  high: number
  critical: number
}

/**
 * The CPU breakdown, as percentages of the interval that sum to 100.
 *
 * `iowait` and `steal` are why this exists. One "68% busy" figure cannot tell
 * apart a server doing work, a server waiting for a disk, and a hypervisor
 * running somebody else on the core you are paying for — and the fix for each
 * is completely different.
 */
export type CPUModes = {
  user: number
  system: number
  nice: number
  iowait: number
  irq: number
  softirq: number
  steal: number
  idle: number
}

/**
 * Kernel pressure stall information: the share of the last ten seconds during
 * which work was waiting rather than running.
 *
 * `supported` is false on a kernel built without PSI, which the UI must show
 * as "cannot tell" rather than as three reassuring zeroes.
 */
export type Pressure = {
  supported: boolean
  cpuSome: number
  memSome: number
  memFull: number
  ioSome: number
  ioFull: number
}

export type Sockets = {
  tcpInUse: number
  tcpTimeWait: number
  tcpOrphan: number
  udpInUse: number
  used: number
}

/** The run queue. `blocked` counts tasks stuck in uninterruptible sleep — the
 *  half of the story that turns high iowait from a curiosity into a cause. */
export type ProcCounts = {
  running: number
  blocked: number
  total: number
}

/**
 * One bucket of recorded history.
 *
 * Every series carries its peak next to its mean because the mean is what
 * hides the interesting moment: a 100% second inside a ten-minute bucket
 * averages away to nothing, and a chart drawn only from means reports a quiet
 * night that was not quiet.
 */
export type MetricsHistoryPoint = {
  ts: string
  samples: number
  cpu: number
  cpuPeak: number
  mem: number
  memPeak: number
  swap: number
  swapPeak: number
  rx: number
  rxPeak: number
  tx: number
  txPeak: number
  diskRead: number
  diskReadPeak: number
  diskWrite: number
  diskWritePeak: number
  load1: number
  load1Peak: number
  diskPercent: number
  memUsed: number

  /** The CPU breakdown. Means only: a stack of peaks would sum past 100. */
  cpuUser: number
  cpuSystem: number
  cpuIowait: number
  cpuSteal: number

  psiCpu: number
  psiCpuPeak: number
  psiMem: number
  psiMemPeak: number
  psiIo: number
  psiIoPeak: number

  /** Operations per second, and the service time each one took. */
  diskReads: number
  diskReadsPeak: number
  diskWrites: number
  diskWritesPeak: number
  diskAwait: number
  diskAwaitPeak: number
  diskBusy: number
  diskBusyPeak: number

  tcpConns: number
  tcpConnsPeak: number
  tcpTimeWait: number

  load5: number
  load15: number
  memAvailable: number
  procs: number
  procsPeak: number
}

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
  cpu: number
  cpuPeak: number
  mem: number
  memPeak: number
  memBytes: number
  memBytesPeak: number
  memLimit: number
  pids: number
  /** Bytes per second, differenced from the cumulative counters Docker reports. */
  netRx: number
  netTx: number
  blockRead: number
  blockWrite: number
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

/** One bucket of one filesystem's recorded capacity. */
export type MountHistoryPoint = {
  ts: string
  samples: number
  usedPercent: number
  usedPercentPeak: number
  used: number
  total: number
  /** Inodes fill independently of bytes, on a disk the capacity chart calls empty. */
  inodesPercent: number
}

export type MountHistory = {
  mountpoint: string
  points: MountHistoryPoint[]
}

export type StorageHistory = {
  from: string
  to: string
  stepSeconds: number
  sampleIntervalSeconds: number
  retentionSeconds: number
  earliest: string | null
  mounts: MountHistory[]
}

/**
 * Something that happened to the server, positioned in time so a chart can
 * mark it.
 *
 * This is what turns an observation into a cause: "memory climbed at 14:20"
 * versus "memory climbed at 14:20, and api-server was deployed at 14:19".
 */
export type MetricEvent = {
  ts: string
  kind: "deploy" | "backup" | "reboot" | "action"
  title: string
  detail?: string
  severity: "info" | "warning" | "error"
  /** Non-zero for events that occupied a span, so a long deploy can be a band. */
  durationSeconds?: number
}

/** One thing worth telling the operator, with the reasoning attached. */
export type HealthFinding = {
  id: string
  level: "critical" | "warning" | "notice"
  title: string
  /** What was measured. */
  detail: string
  /** What to do about it — an opinion, kept separate from the fact. */
  advice?: string
  metric?: string
  value: number
  threshold: number
  since?: string
}

export type Health = {
  status: "ok" | "critical" | "warning" | "notice"
  findings: HealthFinding[]
  checkedAt: string
  /** False when nothing is recording, which makes the verdict a shallower one. */
  recorded: boolean
}

/** One ban or unban, read from fail2ban's own log rather than remembered here. */
export type BanEvent = {
  action: "ban" | "unban"
  jail: string
  ip: string
  at: string
}

export type MetricsHistory = {
  from: string
  to: string
  /** Width of one bucket. A gap wider than this is missing data, not a flat line. */
  stepSeconds: number
  sampleIntervalSeconds: number
  retentionSeconds: number
  /** Oldest sample still retained, or null when nothing has been recorded yet. */
  earliest: string | null
  points: MetricsHistoryPoint[]
}

export type MountStats = {
  device: string
  mountpoint: string
  fstype: string
  total: number
  used: number
  free: number
  usedPercent: number
  inodesTotal: number
  inodesUsed: number
  readBytes: number
  writeBytes: number
  readRate: number
  writeRate: number
  readOps: number
  writeOps: number
  readLatencyMs: number
  writeLatencyMs: number
  busyPercent: number
}

export type NetStats = {
  interface: string
  bytesSent: number
  bytesRecv: number
  packetsSent: number
  packetsRecv: number
  errIn: number
  errOut: number
  dropIn: number
  dropOut: number
  sendRate: number
  recvRate: number
  addrs: string[]
  isUp: boolean
}

export type DirEntry = {
  name: string
  path: string
  size: number
  isDir: boolean
  entries: number
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
  memUsage: number
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

/**
 * The dashboard's opinion of what Docker on this host is doing wrong.
 * `action` names a remedy the UI turns into a button; the empty ones are
 * findings whose fix is outside this panel.
 */
export type Severity = "critical" | "warning" | "recommendation" | "info"

/**
 * What kind of problem this is, which decides whether it belongs to runtime
 * health or to attention. Mixing the two is what let the overview say "all
 * good" above a containers page full of security warnings.
 */
export type FindingClass =
  "runtime" | "security" | "storage" | "configuration" | "exposure" | "lifecycle"

export type DockerFinding = {
  id: string
  /** Derived from `severity`; kept for older bundles. Prefer `severity`. */
  level: "critical" | "warning" | "notice"
  severity: Severity
  class: FindingClass
  title: string
  detail: string
  advice?: string
  scope: "container" | "stack" | "daemon" | string
  target?: string
  targetId?: string
  action?: string
  actionLabel?: string
}

/** What Docker itself reports about what is running. Clears itself. */
export type RuntimeHealth = {
  total: number
  running: number
  exited: number
  created: number
  restarting: number
  paused: number
  dead: number
  removing: number
  /** Of the running containers. `noHealthcheck` is what makes "all healthy" honest. */
  healthy: number
  unhealthy: number
  starting: number
  noHealthcheck: number
  status: "ok" | "notice" | "warning" | "critical"
  summary: string
}

/** Everything else: posture, storage, configuration, exposure. Never "health". */
export type AttentionSummary = {
  critical: number
  warning: number
  recommendations: number
  info: number
  /** critical + warning — what is wrong now, as opposed to what could be better. */
  issues: number
  total: number
}

export type DockerDiagnosis = {
  /** The worst of everything. Never render this as "health" — see `runtime`. */
  status: "ok" | "notice" | "warning" | "critical"
  findings: DockerFinding[]
  checkedAt: string
  checked: number
  runtime: RuntimeHealth
  attention: AttentionSummary
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
}

export type NetworkDetail = DockerNetwork & {
  gateway?: string
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

/** Why a container is not working, with the reasoning shown. */
export type FailureDiagnosis = {
  containerId: string
  name: string
  checkedAt: string
  state: "running" | "stopped" | "looping" | "unhealthy" | "flapping" | "unknown"
  headline: string
  /** The inferred cause. Always worded as "likely" when it is one. */
  likely?: string
  /** "observed" when the evidence states it outright, "inferred" when worked out. */
  confidence: "observed" | "inferred"
  evidence: {
    label: string
    value: string
    source: string
    weight: "decisive" | "supporting" | "context"
  }[]
  restarts: {
    count: number
    window?: string
    recent: number
    looping: boolean
    since?: string
    summary?: string
  }
  suggestions: string[]
  /** The range worth reading logs over — the failure, not the tail. */
  logWindow?: { since: string; until: string; reason: string }
}

/** A change in behaviour read out of recorded history. Always inferred. */
export type Anomaly = {
  id: string
  severity: Severity
  class: FindingClass
  title: string
  detail: string
  advice?: string
  metric: string
  window: string
  inferred: boolean
}

export type AnomalyReport = {
  container: string
  window: string
  samples: number
  anomalies: Anomaly[]
  note?: string
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

export type PM2Process = {
  id: number
  daemonId: string
  logsAvailable?: boolean
  logsUnavailableReason?: string
  name: string
  namespace: string
  status: string
  pid: number
  cpu: number
  memory: number
  restarts: number
  unstableRestarts: number
  uptimeMs: number
  execMode: string
  instances: number
  scriptPath: string
  cwd: string
  outLogPath: string
  errLogPath: string
  nodeVersion: string
  user: string
  watching: boolean
  interpreter?: string
  version?: string
  autorestart: boolean
  maxMemoryRestart?: number
  createdAtMs?: number
}

/** One account's PM2 daemon: whether what it runs would survive a reboot. */
export type PM2Daemon = {
  account: string
  home: string
  /** When `pm2 save` last wrote the resurrection list; absent when it never has. */
  dumpSavedAt?: string
  /** The systemd unit `pm2 startup` installed for this account, if any. */
  startupUnit?: string
}

export type PM2Inventory = {
  available: boolean
  processes: PM2Process[] | null
  daemons?: PM2Daemon[]
}

export type PM2StartRequest = {
  account: string
  script: string
  name?: string
  cwd?: string
  interpreter?: string
  /** 0 or 1 is one fork; 2+ is a cluster of that size; -1 is one per CPU. */
  instances?: number
  watch?: boolean
  maxMemoryRestart?: string
  args?: string[]
}

export type SystemdTimer = {
  unit: string
  activates: string
  activeState: string
  subState: string
  unitFileState: string
  enabled: boolean
  next?: string
  last?: string
}

export type SystemdUnit = {
  name: string
  description: string
  loadState: string
  activeState: string
  subState: string
  unitFileState: string
  enabled: boolean
  mainPid?: number
  memoryBytes?: number
  tasks?: number
  activeSince?: number
  fragmentPath?: string
  result?: string
  restarts?: number
}

export type ProcessRow = {
  pid: number
  ppid: number
  name: string
  cmdline: string
  username: string
  status: string
  cpuPercent: number
  memPercent: number
  rss: number
  vms: number
  threads: number
  nice: number
  createTime: string
  cwd?: string
  exe?: string
  ioReadBytes?: number
  ioWriteBytes?: number
  ioReadRate?: number
  ioWriteRate?: number
  fileDescriptors?: number
  children?: number
  state: "running" | "sleeping" | "blocked" | "stopped" | "zombie" | "other"
  manager: "pm2" | "systemd" | "container" | "session" | "kernel" | "unmanaged"
  managerName?: string
  /** Detail only: what it listens on and how many connections it holds. */
  listening?: ListeningPort[]
  connections?: number
  openFilesLimit?: number
}

export type ListeningPort = {
  proto: string
  address: string
  port: number
}

/** A process as seen from another's detail: enough to recognise and open it. */
export type ProcessLink = {
  pid: number
  name: string
  cmdline: string
  username: string
  state: ProcessRow["state"]
  cpuPercent: number
  rss: number
  createTime: string
}

export type ProcessTree = {
  /** Outermost first, ending with the direct parent. */
  ancestors: ProcessLink[]
  children: ProcessLink[]
}

export type ProcessFacet = {
  value: string
  label: string
  count: number
}

export type ProcessList = {
  processes: ProcessRow[]
  /** Rows matching the current filters before the response cap. */
  total: number
  /** Every process observed before filters. */
  available: number
  truncated: boolean
  /** False on the first snapshot, before cumulative disk counters have a delta. */
  ratesReady: boolean
  users: ProcessFacet[]
  states: ProcessFacet[]
  managers: ProcessFacet[]
}

export type SystemdUnitDetail = {
  unit: SystemdUnit
  properties: Record<string, string>
}

export type CronJob = {
  line: number
  schedule: string
  /** Set only for /etc/crontab and /etc/cron.d entries, which name an account. */
  user?: string
  command: string
  comment?: string
  raw: string
  disabled: boolean
}

export type Crontab = {
  user: string
  source: string
  raw: string
  jobs: CronJob[]
  env: string[]
  comments: string[]
}

export type LogSource = {
  id: string
  label: string
  kind: "system" | "nginx" | "app" | "pm2" | "docker" | "journal"
  path?: string
  size?: number
  modified?: string
  rotated: boolean
  /** Rotated generations sitting next to a live file, and their total size. */
  archives?: number
  archiveBytes?: number
  /** One line saying what this source actually holds, for the reader who has never met auth.log. */
  detail?: string
  /** A live source's state — a stopped container still has logs worth reading. */
  status?: string
}

export type LogJournalUnit = {
  name: string
  description: string
  active: string
}

/**
 * Everything the viewer needs to offer a choice, in one request. The journal's
 * units ship with the sources rather than from a second fetch: putting every
 * unit in the source list would bury syslog under systemd's inventory, and
 * loading them separately leaves the unit picker empty for a second after the
 * journal is chosen, which reads as "this host has no units".
 */
export type LogSourceIndex = {
  sources: LogSource[]
  units: LogJournalUnit[]
  roots: string[]
  /** Why a source kind is absent — "no Docker here" is not the same as "no containers". */
  missing: Record<string, string>
}

export type LogLine = {
  text: string
  level?: string
  timestamp?: string
  source?: string
  /**
   * "stdout" or "stderr", sent wherever the producer distinguishes them — a
   * container and a PM2 process do, a plain file does not.
   */
  stream?: string
  /** 1-based line number within its file, set by the history search. */
  no?: number
  /** True for a line included because it sits next to a match, not because it matched. */
  context?: boolean
  /** Which file of a rotated set this came from. */
  file?: string
  /** Byte ranges of the search term, computed server-side where the browser cannot re-run a Go regexp. */
  match?: [number, number][]
  /**
   * The human sentence out of a structured line, and the context around it.
   * Both are absent for the plain text that is most of a host's logs; where
   * they are set, the line was JSON and the viewer shows the sentence with the
   * request id and the rest a keystroke away rather than making the reader
   * parse JSON by eye.
   */
  message?: string
  fields?: Record<string, string>
}

/** One column of the search histogram, counted by level. */
export type LogBucket = {
  start: string
  total: number
  counts: Record<string, number>
}

export type LogSearchedFile = {
  path: string
  name: string
  archive: boolean
  scanned: number
  matched: number
  modified?: string
  error?: string
}

export type LogSearchResult = {
  lines: LogLine[]
  scanned: number
  matched: number
  truncated: boolean
  /** False when the scan ran out of time rather than out of file. */
  complete: boolean
  files: LogSearchedFile[]
  histogram: LogBucket[]
  bucketSeconds?: number
  first?: string
  last?: string
  tookMillis: number
}

/** The frame the live socket opens with, before any lines. */
export type LogStreamMeta = {
  kind: LogSource["kind"]
  label: string
  path?: string
  filtered: boolean
  prefill?: { lines: number; complete: boolean }
  archives?: number
  note?: string
}

export type LogRotateRule = {
  configFile: string
  patterns: string[]
  frequency?: string
  rotate?: string
  compress: boolean
  size?: string
  maxSize?: string
  missingOk: boolean
  options: string[]
}

export type LogRotateStatus = {
  available: boolean
  rules: LogRotateRule[]
  stateFile?: string
  lastRun?: string
}

/** The verdict for one file: is anything actually trimming this. */
export type LogRetention = {
  managed: boolean
  rule?: LogRotateRule
  pattern?: string
  summary: string
  level: "ok" | "warn" | "unknown"
  lastRun?: string
  available: boolean
}

export type JournalEntry = {
  timestamp: string
  message: string
  priority: number
  unit?: string
  pid?: string
  hostname?: string
  syslogIdentifier?: string
}

export type FileEntry = {
  name: string
  path: string
  size: number
  mode: string
  modeOctal: string
  isDir: boolean
  isSymlink: boolean
  linkTarget?: string
  linkBroken?: boolean
  modified: string
  owner: string
  group: string
  uid: number
  gid: number
  mimeHint?: string
}

export type FileListing = {
  path: string
  parent: string
  entries: FileEntry[]
  roots: string[]
}

export type FileContent = {
  path: string
  content: string
  size: number
  language: string
  binary: boolean
  modeOctal: string
}

/** One click on a row: what the thing is, without loading it. */
export type FilePreview = {
  path: string
  name: string
  kind: "dir" | "text" | "image" | "video" | "audio" | "pdf" | "archive" | "binary"
  mime?: string
  size: number
  modified: string
  modeOctal: string
  owner?: string
  group?: string
  language?: string
  text?: string
  lines?: number
  truncated?: boolean
  editable: boolean
  width?: number
  height?: number
  entries?: { name: string; size: number; isDir: boolean }[]
  entryCount?: number
  moreEntries?: boolean
  archiveError?: string
  childCount?: number
  dirCount?: number
  fileCount?: number
  isSymlink?: boolean
  symlinkTarget?: string
  linkBroken?: boolean
}

export type FilePlace = {
  name: string
  path: string
  kind: "home" | "root" | "user" | "notable"
  hint?: string
}

export type FileBookmark = { path: string; name?: string }

export type FilePlaces = {
  home: string
  roots: string[]
  places: FilePlace[]
  bookmarks: FileBookmark[]
  /** The colour each labelled folder is drawn in, by resolved path. Absent before 0.7.0. */
  colours?: Record<string, string>
}

export type FileFindHit = {
  path: string
  name: string
  rel: string
  dir: string
  isDir: boolean
  size: number
  modified: string
  score: number
  /** UTF-16 offsets into `name`, for the highlight. */
  matches?: number[]
}

export type FileFindResult = {
  root: string
  hits: FileFindHit[]
  truncated: boolean
  visited: number
  elapsedMs: number
}

export type FileUsage = {
  path: string
  bytes: number
  files: number
  dirs: number
  truncated: boolean
  elapsedMs: number
  largest?: { name: string; path: string; bytes: number; isDir: boolean }[]
}

export type FileChecksum = { path: string; algo: string; sum: string; size: number }

export type VHost = {
  name: string
  kind: "nginx" | "caddy"
  path: string
  enabledPath?: string
  enabled: boolean
  serverNames: string[]
  listen: string[]
  upstreams: string[]
  tls: boolean
  certPath?: string
  modified: string
  size: number
}

export type Certificate = {
  name: string
  path: string
  domains: string[]
  issuer: string
  notBefore: string
  notAfter: string
  daysLeft: number
  expired: boolean
  expiring: boolean
  selfSigned: boolean
  source: string
  error?: string
  /** The nginx sites whose ssl_certificate points at this file. */
  usedBy: string[]
}

export type Listener = {
  protocol: string
  address: string
  port: number
  pid: number
  process: string
  cmdline?: string
  user?: string
  exposed: boolean
}

export type DbDriver =
  "postgres" | "mysql" | "sqlite" | "sqlserver" | "clickhouse" | "oracle" | "mongodb" | "redis"

/**
 * What one engine can do, as the server reports it.
 *
 * The frontend deliberately keeps no table of its own: a tab that would 400 on
 * every request should not be offered, and the only thing that actually knows
 * which those are is the dialect registry on the server.
 */
export type DbDriverInfo = {
  id: DbDriver
  label: string
  kind: "sql" | "document" | "keyvalue"
  placeholder: string
  sql: boolean
  ddl: boolean
  columnTypes?: string[]
  filterOps?: string[]
}

export type DbConnection = {
  id: number
  name: string
  driver: DbDriver
  host: string
  port: string
  user: string
  database: string
  createdAt: string
}

/** One table, view or Mongo collection, as the schema browser lists it. */
export type DbTable = {
  schema: string
  name: string
  type: string
  estimatedRows: number
  size?: number
  comment?: string
}

export type DbColumn = {
  name: string
  type: string
  nullable: boolean
  default?: string
  key?: string
  position: number
}

export type QueryResult = {
  columns: string[]
  types: string[]
  rows: unknown[][]
  rowCount: number
  rowsAffected: number
  duration: string
  truncated: boolean
  statement: string
}

export type QueryRisk = {
  destructive: boolean
  level: "read" | "medium" | "high" | "critical"
  reasons: string[]
}

/** One index on a table, with the columns it covers in order. */
export type DbIndex = {
  name: string
  columns: string[]
  unique: boolean
  primary: boolean
}

/** One outgoing foreign key. Composite keys keep columns paired in order. */
export type DbForeignKey = {
  name: string
  columns: string[]
  refSchema?: string
  refTable: string
  refColumns: string[]
  onUpdate?: string
  onDelete?: string
}

/** Everything the Structure tab shows and everything row editing needs. */
export type DbTableDetail = {
  schema: string
  name: string
  columns: DbColumn[]
  primaryKey: string[]
  indexes: DbIndex[]
  foreignKeys: DbForeignKey[]
  createSql?: string
}

/** A named SQL snippet kept against a connection. */
export type DbSavedQuery = {
  id: number
  name: string
  sql: string
  createdAt: string
}

/** One entry in a connection's recent-statement history. */
export type DbHistoryEntry = {
  id: number
  sql: string
  risk: string
  success: boolean
  durationMs: number
  rowCount: number
  ranAt: string
}

export type OrmTarget = "prisma" | "drizzle" | "typescript" | "zod"

/** A generator the server offers, with the filename its download will use. */
export type OrmTargetInfo = {
  id: OrmTarget
  label: string
  filename: string
  description: string
}

/** One session the database server is currently running. */
export type DbActivity = {
  pid: string
  user?: string
  database?: string
  state?: string
  seconds: number
  query?: string
  client?: string
  wait?: string
  blockedBy?: string
  /** True for the connection that answered this request — never offer to kill it. */
  self?: boolean
}

export type DbActivityResponse = {
  sessions: DbActivity[]
  /** False on an engine with no server-side session list, e.g. SQLite. */
  supported: boolean
  reason?: string
}

/** One row found by the schema-wide value search. */
export type DbSearchMatch = {
  schema: string
  table: string
  column: string
  value: string
  row: Record<string, unknown>
}

export type DbSearchResult = {
  matches: DbSearchMatch[]
  tablesScanned: number
  tablesSkipped?: string[]
  truncated: boolean
}

/** What one table costs on disk. Row counts are the engine's estimate. */
export type DbTableSize = {
  schema: string
  table: string
  rows: number
  bytes: number
  dataBytes: number
  indexBytes: number
}

export type DbPoolStats = {
  open: number
  inUse: number
  idle: number
  waitCount: number
  waitDuration: string
  maxOpen: number
  maxIdleClosed: number
  maxLifetimeClosed: number
}

export type DbOverview = {
  schema: string
  tables: DbTableSize[]
  totalBytes: number
  totalRows: number
  tableCount: number
  /** False where the engine cannot report bytes — show rows and say so. */
  sizesKnown: boolean
  pool: DbPoolStats
}

/** One condition in the data grid's filter row. */
export type DbFilter = {
  column: string
  op: string
  value: string
}

/** A column being created or added, as the DDL form describes it. */
export type DbNewColumn = {
  name: string
  type: string
  notNull?: boolean
  primaryKey?: boolean
  default?: string
}

export type DbImportResult = {
  inserted: number
  failed: number
  errors: string[]
  errorsTruncated: boolean
  statement: string
}

/** Table name to column names, for editor completion. */
export type DbOutline = {
  schema: string
  tables: Record<string, string[]>
}

/** table -> its outgoing foreign keys, for the entity diagram. */
export type DbRelations = Record<string, DbForeignKey[]>

// --- Redis ---------------------------------------------------------------

export type RedisKeyInfo = {
  key: string
  type: string
  /** Seconds; -1 means no expiry, -2 means the key is gone. */
  ttl: number
  size: number
}

export type RedisPage = {
  keys: RedisKeyInfo[]
  cursor: string
  done: boolean
}

export type RedisZMember = { member: string; score: number }

export type RedisValue = {
  key: string
  type: string
  ttl: number
  string?: string
  list?: string[]
  set?: string[]
  hash?: Record<string, string>
  zset?: RedisZMember[]
  stream?: { id: string; values: Record<string, unknown> }[]
  truncated: boolean
}

// --- MongoDB -------------------------------------------------------------

export type MongoCollectionInfo = {
  indexes: DbIndex[]
  stats?: Record<string, unknown>
}

export type SystemUser = {
  username: string
  uid: number
  gid: number
  comment: string
  home: string
  shell: string
  groups: string[]
  system: boolean
  locked: boolean
  noPassword: boolean
  lastLogin?: string
  lastLoginFrom?: string
  sshKeyCount: number
  canLogin: boolean
}

export type SSHKey = {
  line: number
  type: string
  comment: string
  fingerprint: string
  bits?: number
  options?: string
  raw: string
}

export type FirewallRule = {
  number?: number
  action: string
  protocol?: string
  from: string
  to: string
  port?: string
  direction?: string
  comment?: string
  /** ufw's duplicate of the rule for the v6 table, not a second rule. */
  ipv6?: boolean
  service?: string
  /** Why this rule is dangerous, when it opens a sensitive port to everyone. */
  danger?: string
  raw: string
}

export type DefaultPolicy = {
  incoming?: string
  outgoing?: string
  routed?: string
}

/**
 * What this host's firewall can actually be told to do.
 *
 * ufw, firewalld and raw iptables answer the same questions differently — one
 * has an on/off switch, one has a service, one has no persistence at all — so
 * the status says what is possible and the UI hides the rest, with a reason.
 */
export type FirewallCapabilities = {
  editable: boolean
  toggle: boolean
  defaultPolicy: boolean
  logging: boolean
  reset: boolean
  profiles: boolean
  readOnlyReason?: string
}

export type FirewallStatus = {
  backend: "ufw" | "firewalld" | "iptables"
  available: boolean
  enabled: boolean
  defaultPolicy?: string
  policy: DefaultPolicy
  logging?: string
  /** firewalld's active zone. Absent for backends with no such idea. */
  zone?: string
  capabilities: FirewallCapabilities
  rules: FirewallRule[]
  raw?: string
  error?: string
}

/** A named port from the server's catalogue, with its warning attached. */
export type ServicePreset = {
  key: string
  name: string
  port: string
  protocol: string
  detail: string
  danger?: string
}

/** A ufw application profile, as the host's own packages define it. */
export type AppProfile = {
  name: string
  title?: string
  description?: string
  ports: string[]
}

export type Fail2banJail = {
  name: string
  currentlyFailed: number
  totalFailed: number
  currentlyBanned: number
  totalBanned: number
  bannedIps: string[]
  fileList: string[]
}

/** A jail's working policy: this many failures in this window earns this ban. */
/** What happened to a jail parameter change, in both halves. */
export type JailParamResult = {
  applied: boolean
  persisted: boolean
  file?: string
  output?: string
  warning?: string
}

export type JailConfig = {
  name: string
  banTime: number
  findTime: number
  maxRetry: number
  ignoreIp: string[]
  actions: string[]
  error?: string
}

export type Offender = {
  ip: string
  bans: number
  jails: string[]
  first: string
  last: string
}

export type BanSummary = {
  total: number
  bans: number
  unbans: number
  offenders: Offender[]
  byJail: Record<string, number>
  perDay: { day: string; count: number }[]
  since?: string
}

export type LoginSession = {
  user: string
  tty: string
  from: string
  loginTime?: string
  idle?: string
  pid?: number
  command?: string
  isSsh: boolean
}

export type BackupJob = {
  id: number
  name: string
  sources: string[]
  excludes: string[]
  targetKind: "local" | "s3" | "b2"
  target: {
    bucket?: string
    region?: string
    endpoint?: string
    prefix?: string
    path?: string
  }
  schedule: string
  retention: number
  /** Prunes artifacts older than this many days; 0 keeps by count alone. */
  retentionDays: number
  enabled: boolean
  createdAt: string
  hasCredentials: boolean
  lastRun?: BackupRun
  nextRun?: string
  /** When the newest successful artifact was taken. */
  lastSuccessAt?: string
  /** A scheduled job that has gone two intervals without a successful run. */
  overdue: boolean
  /** The retained artifacts and what they add up to. */
  stored: { runs: number; bytes: number }
  sqlitePaths?: string[]
  // Saved database connections whose native dump every run captures.
  databaseDumps?: number[]
  /** Containers frozen for the archive step, so their volumes are quiet. */
  pauseContainers?: string[]
  recovery?: BackupRecoveryPlan
}

export type BackupResourceKind =
  "dashboard" | "proxy" | "volume" | "stack" | "deployment" | "repository" | "database"

/**
 * One thing the dashboard already knows about that a backup could protect,
 * with the job it would write for it and the jobs that already cover it.
 */
export type BackupResource = {
  kind: BackupResourceKind
  id: string
  name: string
  detail?: string
  paths?: string[]
  connectionId?: number
  suggest: {
    name: string
    sources: string[]
    excludes?: string[]
    sqlitePaths?: string[]
    databaseDumps?: number[]
    pauseContainers?: string[]
  }
  coveredBy: { jobId: number; jobName: string; enabled: boolean }[]
  protected: boolean
  lastBackupAt?: string
}

export type BackupResourceReport = {
  resources: BackupResource[]
  unavailable: Record<string, string>
}

export type BackupArchiveEntry = {
  name: string
  size: number
  mode: string
  isDir: boolean
}

export type BackupRestoreResult = {
  runId: number
  destination: string
  entries: number
  bytes: number
  skipped?: string[]
  targets?: string[]
}

export type BackupDatabaseDump = {
  connectionId: number
  name: string
  driver: string
  database: string
  method: string
  file: string
  archivePath: string
  digest: string
  bytes: number
}

export type BackupManifest = {
  version: number
  artifactDigest: string
  complete: boolean
  sources?: { path: string; archivePath: string }[]
  databaseDumps?: BackupDatabaseDump[]
  pausedContainers?: string[]
}

export type BackupRecoveryPlan = {
  image: string
  command: string[]
  schemaVersion: string
  expectedOutputDigest: string
  timeoutSeconds: number
  maxBytes: number
  automatic: boolean
}

export type BackupRestoreVerification = {
  id: number
  runId: number
  state: "running" | "passed" | "failed" | "cleanup_failed"
  startedAt: string
  endedAt?: string
  artifactDigest: string
  manifestDigest: string
  planDigest: string
  applicationImage?: string
  schemaVersion: string
  outputDigest?: string
  entries: number
  bytes: number
  cleanupComplete: boolean
  detail?: string
}

export type BackupRun = {
  id: number
  jobId: number
  startedAt: string
  endedAt?: string
  status: "running" | "success" | "failed"
  artifact: string
  sizeBytes: number
  log: string
  trigger: string
  duration?: string
  manifest?: BackupManifest
  restoreVerification?: BackupRestoreVerification
}

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
    }
  }
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
  phase?: "install" | "build" | "output_check" | "setup" | "dockerfile" | "base_image" | "pull"
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
 * `review` opens a field whose right value the output cannot prove.
 */
export type DeploymentCauseFix = {
  kind: "set_build" | "set_runtime" | "add_variable" | "variable_scope" | "review"
  /** `configuration.build.packageManager`, `runtime.internalPort`, `variables.NAME`, `dependencies`. */
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
}

export type DeploymentRuntimeServices = {
  status: "available" | "unavailable"
  reason?: string
  observedAt: string
  services: DeploymentRuntimeService[]
}

export type DockerTemplate = {
  id: string
  name: string
  blurb: string
  category: "http" | "database" | "tool" | "automation" | "game"
  requires?: string
  docsUrl: string
  license: string
  spec: ContainerSpec
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
   */
  kind: "setup" | "credentials" | "token" | "client" | "open" | "unavailable"
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

export type GameOverview = {
  status: "available" | "unavailable"
  reason?: string
  blueprintId?: string
  edition?: string
  containerId?: string
  address?: string
  players?: GamePlayers
  console: boolean
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

export type BlueprintRenderedPlan = {
  detection: { source: { kind: string; repository?: string; ref?: string; digest?: string } }
  configuration: DeploymentConfiguration
  rendered: {
    blueprintId: string
    blueprintVersion: string
    profile: string
    image: string
    memoryMb: number
    digest: string
    variables: {
      name: string
      value?: string
      sensitivity: string
      generated?: boolean
      length?: number
      label?: string
    }[]
    ports: {
      name: string
      internal: number
      protocol: string
      exposure: string
      purpose: string
      primary?: boolean
    }[]
    volumes: { name: string; target: string; purpose: string; data: boolean; backup: boolean }[]
    checks: { name: string; kind: string; phase: string; required: boolean }[]
    domains?: string[]
    acceptances?: { input: string; label: string; url: string }[]
    stopCommands?: string[]
  }
  summary: BlueprintSummary
  inputs: BlueprintInput[]
  automation: BlueprintAutomation[]
  files: BlueprintConfigFile[]
}

export type DeploymentDiagnosisFinding = {
  code: string
  severity: "critical" | "warning" | "notice"
  title: string
  measured: string
  means: string
  action: string
  owner: string
  deepLink?: string
  external?: boolean
}

export type DeploymentDiagnosis = {
  status: "assessed" | "partial"
  findings: DeploymentDiagnosisFinding[]
  silences: { subject: string; reason: string }[]
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
  deepLink?: string
  certificateLink?: string
  protected?: boolean
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
  diagnosis: DeploymentDiagnosis
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
  strategy: "blue_green" | "stop_first"
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
  /** Services in the live release's runtime: one, or a Compose stack's count. */
  serviceCount?: number
  lastRun?: DeploymentEngineRun
  activeRun?: DeploymentEngineRun
  updatedAt: string
  /** owner/name for Git; the reference itself for an image or template, whose sourceRef is empty. */
  sourceRepository?: string
  /** A Git source's remote without userinfo: `https://host/path` or `git@host:path`. */
  sourceRemote?: string
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
}

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

export type DeploymentRelease = {
  id: number
  projectId: number
  environmentId: number
  number: number
  runId: number
  predecessorReleaseId?: number
  state: "candidate" | "live" | "retained"
  planRevision: number
  sourceRevision?: string
  imageDigest?: string
  configDigest: string
  variablesDigest: string
  strategy: "blue_green" | "stop_first"
  expectedDowntime: boolean
  createdAt: string
  activatedAt?: string
  retiredAt?: string
  pinned: boolean
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
  /** The HTTP-01 challenge this host could issue one with now, if any. */
  certificateMethod?: "nginx" | "webroot" | "standalone" | "caddy"
  certificateIssue?: string
  method: "wildcard" | "sslip" | "custom" | "none"
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
  "node" | "go" | "python" | "rust" | "java" | "dotnet" | "deno" | "php" | "site"

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
  /** The interpreter range pyproject declares, and the manifest the recipe installs from. */
  pythonRequires?: string
  pythonInstall?: "uv.lock" | "poetry.lock" | "requirements.txt" | "pyproject.toml"
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
  "recipe" | "dockerfile" | "static" | "image" | "compose" | "none" | "legacy_compose"

export type DeploymentOwnership = "managed" | "linked" | "observed"

/** A container port published on the host next to the routed one: Gitea's SSH, Syncthing's sync protocol. */
export type DeploymentPublishedPort = {
  hostPort: number
  containerPort: number
  protocol?: "tcp" | "udp"
  bindAddress?: string
}

export type DeploymentRestartPolicy = "unless-stopped" | "always" | "on-failure" | "no"

export type NotificationChannelKind = "webhook" | "discord" | "slack" | "telegram" | "email"

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
    pythonVersion?: string
    /**
     * The Node major a JavaScript recipe (or a PHP recipe's asset stage) builds and runs on;
     * empty follows the repository.
     */
    nodeVersion?: string
    /** The PHP release a PHP recipe builds on; empty lets composer.json and composer.lock decide. */
    phpVersion?: string
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
    strategy: "blue_green" | "stop_first"
    privileged?: boolean
    hostNetwork?: boolean
    capabilities?: string[]
    devices?: string[]
    memoryMb?: number
    cpus?: number
    pidsLimit?: number
    restartPolicy?: DeploymentRestartPolicy
    /** The largest request body the route lets through, in MB; empty is 64. */
    maxRequestBodyMb?: number
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
  status: "pending" | "connected" | "stale" | "unavailable"
  detail?: string
  checkedAt?: string
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

export type DeploymentRemovalExecution = {
  deploymentId: number
  removed: DeploymentRemovalTarget[]
  remaining: DeploymentRemovalTarget[]
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
}

/**
 * POST /deploy/{id}/environments/{env}/check: preflight for the saved plan
 * against the commit a deployment would build now — what analyze_plan runs
 * before every build, asked before Deploy is pressed.
 */
export type DeploymentCheckResult = {
  findings: DeploymentPreflightFinding[]
  planRevision: number
  sourceRevision?: string
  checkedAt: string
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

export type EnvVar = {
  key: string
  value?: string
  updatedAt: string
  masked: string
}

export type AuditEntry = {
  id: number
  ts: string
  userId: number
  username: string
  role: string
  ip: string
  actor: string
  action: string
  target: string
  method: string
  path: string
  status: number
  success: boolean
  detail: string
}

export type ApiToken = {
  id: number
  userId: number
  name: string
  prefix: string
  role: Role
  createdAt: string
  expiresAt?: string
  lastUsedAt?: string
  revoked: boolean
}

export type SessionInfo = {
  id: string
  userId: number
  twoFactorPassed: boolean
  ip: string
  userAgent: string
  createdAt: string
  lastSeenAt: string
  expiresAt: string
  current: boolean
}

// --- Git ---

export type GitRepo = {
  path: string
  name: string
  branch: string
  remote?: string
  /** The branch this one tracks, e.g. origin/main. */
  upstream?: string
  head?: string
  subject?: string
  author?: string
  commitAt?: string
  dirty: boolean
  changes: number
  staged: number
  untracked: number
  conflicts: number
  ahead: number
  behind: number
  detached: boolean
  /** The upstream is configured but no longer exists on the remote. */
  gone?: boolean
  /** No commits yet. */
  empty?: boolean
}

/**
 * One entry of `git status`. A file staged and then edited again is one
 * entry with both `staged` and `unstaged` set, and is listed under both
 * headings; `index` and `worktree` carry each side's status letter.
 */
export type GitFileChange = {
  path: string
  index: string
  worktree: string
  label: string
  staged: boolean
  unstaged: boolean
  /** The previous name of a renamed or copied path. */
  from?: string
}

export type GitIdentity = {
  name?: string
  email?: string
}

export type GitStatus = {
  repo: GitRepo
  files: GitFileChange[]
  clean: boolean
  stashes: number
  identity: GitIdentity
  /** A merge, rebase, revert, cherry-pick or bisect a shell left half-finished. */
  operation?: string
}

export type GitChangedFile = {
  path: string
  from?: string
  status: string
  insertions: number
  deletions: number
  binary?: boolean
}

/** One commit with its message and what it changed — the diff is fetched per file. */
export type GitCommitDetail = GitCommit & {
  body?: string
  committer?: string
  committedAt?: string
  changes: GitChangedFile[]
}

export type GitTag = {
  name: string
  commit?: string
  at?: string
  message?: string
  annotated: boolean
  tagger?: string
}

export type GitStash = {
  index: number
  sha: string
  at?: string
  branch?: string
  message: string
}

export type GitRemote = {
  name: string
  fetchUrl: string
  pushUrl?: string
}

/** What merging `head` into `base` would bring. */
export type GitComparison = {
  base: string
  head: string
  ahead: number
  behind: number
  commits: GitCommit[]
  files: number
  insertions: number
  deletions: number
  baseSha?: string
  headSha?: string
  changes?: GitChangedFile[]
}

export type GitReflogEntry = {
  sha: string
  selector: string
  message: string
  author: string
  at: string
}

export type GitBlame = {
  ref: string
  file: string
  hasMore: boolean
  lines: {
    sha: string
    line: number
    originalLine: number
    author: string
    at: string
    subject: string
    content: string
  }[]
}

export type GitSignature = {
  status: string
  signer?: string
  key?: string
  fingerprint?: string
}

export type GitWorktree = {
  path: string
  head: string
  branch: string
  current: boolean
  main: boolean
  locked: boolean
  prunable: boolean
  accessible: boolean
}

export type GitConflictSide = {
  present: boolean
  content: string
  binary: boolean
  mode?: string
  object?: string
}

export type GitConflict = {
  file: string
  version: string
  base: GitConflictSide
  ours: GitConflictSide
  theirs: GitConflictSide
  result: string
  editable: boolean
  operation: string
}

export type GitPartialDiff = {
  file: string
  staged: boolean
  body: string
  version: string
  lines: number[]
  reason?: string
}

export type GitCommit = {
  sha: string
  short: string
  subject: string
  author: string
  email: string
  at: string
  refs?: string
  insertions: number
  deletions: number
  files: number
  isMerge: boolean
  parents?: string[]
}

/** One node in the branch graph: a commit plus the lane its dot sits in. */
export type GitGraphCommit = GitCommit & {
  col: number
  /** The lane each edge to a parent travels down, index for index with `parents`. */
  parentLanes?: number[]
}

export type GitGraph = {
  commits: GitGraphCommit[]
  /** How many lanes wide the busiest row gets — the canvas is sized from this. */
  lanes: number
  hasMore?: boolean
  skip?: number
  /** How many commits the query reaches in all — sent with the first page only. */
  total?: number
}

export type GitBranch = {
  name: string
  current: boolean
  remote: boolean
  upstream?: string
  head?: string
  subject?: string
  at?: string
  ahead: number
  behind: number
  /** Another worktree that has this branch checked out; git refuses to delete
   *  or switch it even with -D. */
  worktree?: string
  /** A local branch whose upstream no longer exists on the remote. */
  gone?: boolean
  /** Every commit is already reachable from HEAD: safe to delete. */
  merged?: boolean
  /** A remote-tracking branch's two halves: the remote, and the local name. */
  remoteName?: string
  local?: string
}

export type GitResult = {
  command: string
  output: string
  ok: boolean
}

/**
 * Who this server is, to GitHub, in one repository.
 *
 * The answer is per repository and not global, and `owner` is why: gh keeps
 * the token under the home of the host account that owns the checkout, which
 * is the same account git runs as when it pushes. Signing in for /srv/app does
 * not sign in for a repository owned by somebody else.
 */
export type GitHubAccount = {
  loggedIn: boolean
  host?: string
  login?: string
  name?: string
  avatarUrl?: string
  profileUrl?: string
  scopes?: string[]
  protocol?: string
  owner?: string
  /** Whether a push and a commit would actually use the account. */
  gitConfigured: boolean
  committerName?: string
  committerEmail?: string
  /** How origin is reached: an ssh remote never asks the token for anything. */
  remoteProtocol?: string
  /** gh's own words for why nobody is signed in. */
  reason?: string
}

export type GitHubStatus = {
  available: boolean
  account?: GitHubAccount
}

export type GitHubRepo = {
  nameWithOwner: string
  defaultBranch: string
  url: string
  private: boolean
  permission?: string
}

/**
 * One repository the signed-in account can deploy, as the chooser lists them.
 * Distinct from GitHubRepo, which describes the checkout a page is already in.
 */
export type GitHubRepoSummary = {
  nameWithOwner: string
  name: string
  owner: string
  description?: string
  url: string
  cloneUrl: string
  defaultBranch?: string
  language?: string
  private: boolean
  fork: boolean
  archived: boolean
  pushedAt?: string
}

export type GitHubBranch = {
  name: string
  protected?: boolean
  default?: boolean
}

/** The code to type into github.com, and where to type it. */
export type GitHubDeviceStart = {
  id: string
  userCode: string
  verificationUri: string
  expiresIn: number
  interval: number
}

export type GitHubDeviceState = {
  status: "pending" | "complete" | "denied" | "expired"
  account?: GitHubAccount
  message?: string
}

export type GitPullRequest = {
  headSha?: string
  baseSha?: string
  number: number
  title: string
  url: string
  state: string
  draft: boolean
  head: string
  base: string
  author?: string
  createdAt?: string
  comments: number
  /** approved, changes_requested, review_required, or empty. */
  review?: string
  /** success, failure, pending, or empty where there are no checks. */
  checks?: string
  /** mergeable, conflicting, or unknown — only on the detail read. */
  mergeable?: string
  additions?: number
  deletions?: number
  files?: number
  body?: string
}

export type GitHubWorkflowRun = {
  id: number
  name: string
  workflow?: string
  status: string
  conclusion?: string
  url: string
  branch?: string
  sha?: string
  event?: string
  createdAt?: string
}

/**
 * The answer to "is this shell sitting inside a checkout" for a terminal's
 * working directory. `inRoots` is false for a real repository that falls
 * outside JD_GIT_ROOTS: it can be named but not operated on, since every other
 * git route is gated on those roots.
 */
export type GitDetect = {
  available: boolean
  inRoots?: boolean
  root?: string
  repo?: GitRepo
}

// --- System updates ---

export type UpdatePackage = {
  name: string
  current: string
  candidate: string
  origin?: string
  security: boolean
  arch?: string
}

export type UpdateReport = {
  available: boolean
  manager?: string
  packages: UpdatePackage[]
  securityCount: number
  /**
   * Whether this manager can tell a security update from any other. Alpine
   * and Arch publish no advisory data, so a zero count there means "cannot
   * tell", not "none outstanding".
   */
  securityFiltering: boolean
  rebootRequired: boolean
  rebootPackages?: string[]
  lastChecked: string
  error?: string
}

// --- The host's software ---
//
// Mirrors internal/updates' catalogue half. The upgrade report above answers
// "how far behind is this machine"; these answer "what is on it", "what else
// is there" and — the one nothing in this class offers — "now that it is
// installed, what do I type".

export type InstalledPackage = {
  name: string
  version: string
  arch?: string
  summary?: string
  /** Installed size in bytes, or absent where the manager reports none. */
  size?: number
  section?: string
  /** Somebody asked for this, as opposed to it arriving as a dependency. */
  explicit: boolean
  /** The package database says the system does not work without it. */
  essential?: boolean
  /** The pending version, joined on from the upgrade report by the server. */
  upgradable?: string
  security?: boolean
}

export type PackageInventory = {
  available: boolean
  manager?: string
  packages: InstalledPackage[]
  explicitCount: number
  totalSize?: number
  upgradeCount: number
  securityCount: number
  canInstall: boolean
  /**
   * Whether "and delete its configuration" means anything here. RPM keeps a
   * modified config file whatever it is asked, so the switch is hidden there
   * rather than shown doing nothing.
   */
  canPurge: boolean
  /**
   * When the repository index was last refreshed. Everything on the page is
   * read from that index, so a three-month-old one is a catalogue missing
   * every package added since — absent where the manager cannot say.
   */
  indexAge?: string
  canRefresh: boolean
  readAt: string
  error?: string
}

export type PackageSearchResult = {
  name: string
  version?: string
  summary?: string
  repository?: string
  installed: boolean
  installedVersion?: string
}

export type PackageDetail = {
  name: string
  version?: string
  installedVersion?: string
  installed: boolean
  summary?: string
  description?: string
  homepage?: string
  license?: string
  section?: string
  repository?: string
  arch?: string
  maintainer?: string
  size?: number
  dependencies?: string[]
  essential?: boolean
  /** Why the dashboard will not remove it; absent when it will. */
  protected?: string
  upgradable?: string
}

export type ManPage = {
  name: string
  /** The manual volume: 1 is a command, 5 a file format, 8 a root-only tool. */
  section: string
  path: string
}

/** What a package gives you and how to reach it, read from its own file list. */
export type PackageUsage = {
  package: string
  commands?: string[]
  manPages?: ManPage[]
  services?: string[]
  configFiles?: string[]
  docs?: string[]
  manual?: string
  manualFor?: string
  /** A page exists but `man` is not installed here, so it could not be read. */
  manUnavailable?: string
  truncated?: boolean
  /** No commands, pages or units — a library other packages use. */
  empty: boolean
}

/** How the dashboard itself can be reached, graded weakest-entry-first. */
/**
 * One entry from the host's own login accounting (wtmp, or btmp for failures).
 * Nothing here is recorded by the dashboard — it is the host's record, which
 * the Security page previously never showed.
 */
export type LoginRecord = {
  kind: "login" | "boot" | "shutdown"
  user: string
  tty: string
  from: string
  loginTime?: string
  endTime?: string
  /** How it ended when there is no end time: "down" or "crash". */
  ended?: string
  duration?: string
  active: boolean
}

export type Exposure = {
  grade: "tailscale" | "tunnel" | "private" | "public" | "open"
  summary: string
  allowlist: string[]
  interfaces: string[]
  tailscaleIp?: string
  recommendation?: string
  /** The address this browser is reaching the dashboard from — the one every lockout guard protects. */
  client?: string
}

/** One address folded across the failed-login record: who is trying, and how hard. */
export type Attacker = {
  address: string
  attempts: number
  /** The account names tried from this address, most tried first. */
  users: string[]
  first: string
  last: string
}

export type AttackSummary = {
  attempts: number
  addresses: number
  windowHours: number
  /** The sample ran out inside the window, so every figure is a floor. */
  capped: boolean
  attackers: Attacker[]
  since?: string
}

/** One named, filed in-memory workspace containing one or more direct PTYs. */
export type TerminalWorkspace = {
  id: string
  title: string
  /** Whether the operator chose the title. A default one follows the shell. */
  named?: boolean
  folder?: string
  favourite: boolean
  live: boolean
  cwd?: string
  windows: number
  createdAt: string
  attached: number
  user?: string
  shell?: string
  owner?: string
  /** Whether any window has a program in the foreground. */
  busy?: boolean
  /** Whether any window is working, and when the last one finished (ms since the epoch). */
  working?: boolean
  finishedAt?: number
  /** The window that stands for the session: the one last shown, else the newest. */
  current?: TerminalWindowSummary
}

/**
 * What a direct PTY is doing, as the backend reads it off the terminal: the
 * title the foreground program set, whether anything but the prompt holds the
 * terminal, and what that is. Polled with the window lists and pushed over
 * the attach socket as a `state` frame whenever it changes.
 */
export type TerminalActivity = {
  /** The OSC title, when it was set by whatever is in the foreground now. */
  title?: string
  /** A program has held the terminal for longer than a blink. */
  busy: boolean
  /** The foreground command, while busy. */
  process?: string
  /**
   * Something is actually happening: output has been arriving for a second
   * and still is, or the job is burning CPU. An editor or an agent waiting at
   * its prompt is busy but not working.
   */
  working?: boolean
  /** When the last stretch of work ended, in ms since the epoch, until work starts again. */
  finishedAt?: number
}

export type TerminalWindowSummary = {
  id: string
  name: string
  /** Whether the operator named the window rather than it carrying a default. */
  named?: boolean
} & Partial<TerminalActivity>

/** A server-backed folder reconciled with live workspace membership. */
export type TerminalFolder = {
  name: string
  collapsed?: boolean
}

/** An independent direct PTY shown as a window tab inside one session. */
export type TerminalWindow = TerminalWindowSummary & {
  index: number
  cwd?: string
}

// --- detected and provisioned database servers ----------------------------

/**
 * A database server found running on this host. It deliberately carries no
 * password: what reaches the browser is the description of a connection that
 * could be made, not the means to make it.
 */
export type DbDetectedServer = {
  driver: DbConnection["driver"]
  container: string
  image: string
  host: string
  port: number
  user?: string
  database?: string
  /** Why this one cannot be connected to, when it cannot. */
  reason?: string
  /** The name of the connection already pointing at it, if there is one. */
  adopted?: string
  health?: string
  status?: string
}

export type DbDetected = { servers: DbDetectedServer[] }

/**
 * Where a connection's server can be reached from, as GET /databases/{id}/access
 * reads it off the container's port binding and the host's firewall.
 *
 * `detected` is the fact the Remove row keys off: a server the sync would
 * connect again on the next page load is not worth a button that forgets it.
 * `managed` is whether the binding can be changed from here — a container on
 * this host that is not compose-owned and publishes the engine's port.
 */
export type DbAccess = {
  detected: boolean
  container?: string
  composeProject?: string
  managed: boolean
  exposure: "local" | "public" | "private" | "remote"
  port?: number
  publicAddresses: string[]
  firewall: {
    backend?: string
    active: boolean
    open: boolean
    editable: boolean
  }
}

/** What PUT /databases/{id}/access did, and the reading afterwards. */
export type DbAccessChange = {
  access: DbAccess
  /** opened, closed, already, none, inactive, read-only or failed. */
  firewall: string
  firewallError?: string
}

/**
 * What POST /databases/sync did.
 *
 * `unreachable` is the half that has to be shown: a database this host is
 * running that was recognised and could not be connected to — almost always a
 * container on a compose network with no published port. Dropping it silently
 * is what made the reconcile look like it did nothing.
 */
export type DbUnreachableServer = {
  container: string
  driver: string
  reason: string
}

export type DbSyncResult = {
  added: string[]
  already: string[]
  unreachable?: DbUnreachableServer[]
  /** Servers running on the host itself, which nothing here can sign in to. */
  needsCredentials?: DbCredentialServer[]
}

/**
 * A database installed on the machine rather than in a container.
 *
 * Everything about it is known except the password: a container states its
 * credentials in its environment, and an apt-installed server keeps them in
 * its own catalogue, where no amount of reading the machine finds them. So the
 * page asks for that one thing, and the server dials before it saves anything.
 */
export type DbCredentialServer = {
  driver: string
  host: string
  port: number
  process?: string
  name: string
  /** The engine's own conventional account and database, to open the form with. */
  user?: string
  database?: string
}

export type DbProvisionOption = {
  engine: string
  label: string
  image: string
  driver: string
}

// --- schema graph (the diagram) -------------------------------------------

export type DbGraphColumn = {
  name: string
  type: string
  nullable: boolean
  primaryKey: boolean
  foreignKey?: string
  unique?: boolean
}

export type DbGraphTable = {
  schema: string
  name: string
  type: string
  rows: number
  columns: DbGraphColumn[]
}

export type DbGraphEdge = {
  name: string
  fromTable: string
  fromColumn: string
  toTable: string
  toColumn: string
  onDelete?: string
  cardinality: "one-to-one" | "many-to-one"
}

export type DbSchemaGraph = {
  schema: string
  tables: DbGraphTable[]
  edges: DbGraphEdge[]
  truncated: boolean
}

/**
 * How the operator arranged one schema's diagram, as the server hands it back.
 * The document itself is decoded by `components/database/diagram/memory.ts`;
 * the server stores it whole and says only when it was last saved.
 */
export type DbDiagramLayoutResponse = {
  layout: Record<string, unknown> | null
  updatedAt?: string
}

// ---------------------------------------------------------------------------
// The dashboard's own version, its changelog, and updating it in place.
//
// These mirror internal/selfupdate. The changelog is structured rather than
// markdown for the reason that package gives: the UI has to select the
// releases between the installed version and the newest, and paint a tag
// against each change — neither of which is a thing you can do to a paragraph.
// ---------------------------------------------------------------------------

export type ChangeKind = "added" | "changed" | "fixed" | "removed" | "security" | "deprecated"

export type ReleaseChange = {
  kind: ChangeKind
  text: string
  /** The sentence under the line, where the consequence is not obvious. */
  detail?: string
}

export type Release = {
  version: string
  /** A calendar day, YYYY-MM-DD. */
  date: string
  title: string
  summary?: string
  changes: ReleaseChange[]
  /** Needs the operator to do something by hand; never hidden behind a click. */
  breaking?: boolean
  breakingNote?: string
}

export type UpdateRunStatus = "pending" | "running" | "success" | "failed"
export type UpdatePhase = "queued" | "fetching" | "building" | "restarting" | "finished"

/** One upgrade attempt, as recorded on disk by the container that ran it. */
export type UpdateRun = {
  id: string
  status: UpdateRunStatus
  phase: UpdatePhase
  fromVersion: string
  toVersion: string
  ref: string
  dir: string
  compose: string
  image: string
  container: string
  health?: string
  fromCommit?: string
  toCommit?: string
  actor: string
  startedAt: string
  updatedAt: string
  finishedAt?: string
  error?: string
}

export type SelfUpdateReport = {
  version: string
  latest?: string
  available: boolean
  /** Releases newer than the installed version, newest first. */
  releases: Release[]
  /** Everything this build knows about its own past; needs no network. */
  history: Release[]
  breaking: boolean
  check: {
    enabled: boolean
    checkedAt?: string
    error?: string
    source?: string
    repo: string
    ref: string
  }
  install: {
    supported: boolean
    reason?: string
    dir?: string
    compose?: string
    /** Uncommitted changes in the checkout, in `git status --porcelain` form. */
    dirty?: string[]
  }
  run?: UpdateRun
  log?: string
}

/**
 * The dashboard's own settings, as internal/selfcfg speaks them.
 *
 * Every field here is a line in the `.env` beside the compose file, and
 * changing one restarts the stack into it. That is why the shape is flat and
 * small: these are the settings somebody has a reason to change from a
 * browser, not every variable the backend reads.
 */
export type DashboardSettings = {
  site: string
  /** The interface to listen on, when that is not the same as `site`. */
  bind: string
  tls: "tailscale" | "internal" | "off"
  port: number
  frontendPort: number
  backendPort: number
  allowedCidrs: string
  terminalEnabled: boolean
  require2fa: boolean
  sessionTtl: string
  idleTtl: string
  updateCheck: boolean
}

/** One setting moving, for the confirmation dialog and the run record. */
export type DashboardConfigChange = {
  key: string
  label: string
  from: string
  to: string
}

export type ConfigRunStatus = "pending" | "running" | "success" | "failed" | "rolled_back"
export type ConfigPhase = "queued" | "applying" | "waiting" | "rollback" | "finished"

/**
 * One restart, as recorded on disk by the container that carried it out.
 *
 * It outlives the dashboard it restarts, which is the whole point: the browser
 * follows this record across the moment the API stops answering, and picks the
 * story up from whatever backend comes back.
 */
export type DashboardConfigRun = {
  id: string
  status: ConfigRunStatus
  phase: ConfigPhase
  action: "apply" | "restart" | "rebuild"
  changes?: DashboardConfigChange[]
  dir: string
  compose: string
  image: string
  envPath?: string
  backup?: string
  health?: string
  rollbackHealth?: string
  /** Where to go once this finishes — the address may have moved. */
  endpoint?: string
  container: string
  /** A caveat about a run that otherwise worked, e.g. a certificate that could not be issued. */
  note?: string
  actor: string
  startedAt: string
  updatedAt: string
  finishedAt?: string
  error?: string
}

/**
 * What this machine is on its tailnet, as the dashboard discovered it.
 *
 * The settings form fills the address, the listening interface and the
 * allowlist from this the moment somebody picks a Tailscale certificate. Those
 * three facts are one `tailscale status` away, and asking an operator to find
 * them by hand — then rejecting the form when they guessed — is the dashboard
 * refusing to do its own job.
 */
export type TailscaleIdentity = {
  available: boolean
  running: boolean
  state?: string
  /** MagicDNS name, without the trailing dot. */
  hostname?: string
  ip4?: string
  /** Whether the tailnet will issue certificates at all. */
  httpsEnabled: boolean
  /** Why it cannot be used, in a sentence meant for a person. */
  detail?: string
}

export type DashboardCertificate = {
  issued: boolean
  expires?: string
}

export type DashboardConfigReport = {
  /** False on an install with no compose stack to recreate. */
  supported: boolean
  reason?: string
  dir?: string
  compose?: string
  envPath?: string
  settings: DashboardSettings
  /** The URL this configuration implies. */
  endpoint: string
  tailscale: TailscaleIdentity
  /**
   * What is actually on disk for the configured address — not the same
   * question as which mode is set. A Tailscale install with no certificate yet
   * answers perfectly well on Caddy's internal CA, and saying "trusted"
   * because the setting says so would contradict the padlock.
   */
  certificate: DashboardCertificate
  /** Settings the file asks for that the running process is not doing. */
  drift?: DashboardConfigChange[]
  run?: DashboardConfigRun
  log?: string
}

/**
 * The security verdict. Mirrors netsec.Posture: the same three-field shape as
 * a health finding, because what was measured, what it means and what to do
 * are three different things and the UI renders them differently.
 */
export type SecurityFinding = {
  id: string
  level: "critical" | "warning" | "notice"
  title: string
  detail: string
  advice?: string
  area: "exposure" | "firewall" | "ssh" | "intrusion" | "ports" | "tls" | "updates"
  /** A remedy the dashboard can carry out itself, rendered as a button. */
  fix?: string
  fixLabel?: string
}

export type Posture = {
  status: "ok" | "notice" | "warning" | "critical"
  findings: SecurityFinding[]
  checkedAt: string
  checks: number
  /** Checks that could not run, because a check that did not run is not a pass. */
  skipped: string[]
}

export type SSHSetting = {
  key: string
  label: string
  value: string
  recommended: string
  secure: boolean
  detail: string
  risk?: string
  options?: string[]
  /** "list" is a space-separated set of account names; empty means unrestricted. */
  kind: "choice" | "number" | "list"
}

export type KeyedAccount = { user: string; keys: number }

/**
 * A systemd socket unit standing in front of sshd.
 *
 * Where one is active it owns the listener and sshd_config's Port is read and
 * ignored — the default on Ubuntu since 22.10. The page has to say so, because
 * otherwise the port control is the one setting that reports success and
 * changes nothing.
 */
export type SSHSocket = {
  unit?: string
  ports?: string[]
  dropIn?: string
}

export type SSHDConfig = {
  available: boolean
  source: string
  settings: SSHSetting[]
  ports: string[]
  managedFile?: string
  keyedAccounts: KeyedAccount[]
  hasMatchBlocks: boolean
  socket?: SSHSocket
  error?: string
}

export type SSHApplyResult = {
  written: boolean
  file: string
  valid: boolean
  output?: string
  reloaded: boolean
  reloadError?: string
  applied: string[]
  /** Whether the socket unit was moved onto the new port as well. */
  socketMoved?: boolean
  socketUnit?: string
  socketError?: string
}

export type Peer = {
  address: string
  count: number
  established: number
  ports: number[]
  processes: string[]
  private: boolean
  service?: string
}

export type Connections = {
  peers: Peer[]
  total: number
  listening: number
  loopback: number
}

export type NetInterface = {
  name: string
  addresses: string[]
  mac?: string
  mtu: number
  up: boolean
  loopback: boolean
  kind: "physical" | "tunnel" | "bridge" | "virtual" | "loopback"
  bytesSent: number
  bytesRecv: number
  public: boolean
}

export type Route = {
  destination: string
  gateway?: string
  interface?: string
  source?: string
  metric?: string
  family: "ipv4" | "ipv6"
  raw: string
}

export type NetworkInfo = {
  interfaces: NetInterface[]
  routes: Route[]
  resolvers: string[]
  search: string[]
}

export type ProbeResult = {
  tool: string
  target: string
  ok: boolean
  output: string
  records?: string[]
  duration: string
  error?: string
}

/** The live TLS report: what a visitor actually gets, not what is on disk. */
export type ProtocolResult = {
  name: string
  status: "offered" | "refused" | "unknown"
  detail?: string
}

export type ChainLink = {
  subject: string
  issuer: string
  notAfter: string
  isCa: boolean
  keyType?: string
  keyBits?: number
  selfIssued: boolean
}

export type HSTS = {
  maxAge: number
  includeSubDomains: boolean
  preload: boolean
  raw: string
}

export type HeaderCheck = {
  name: string
  value?: string
  present: boolean
  level: "important" | "optional"
  detail: string
}

export type HTTPScan = {
  statusCode: number
  server?: string
  plainRedirects: boolean
  plainStatus?: number
  plainLocation?: string
  plainError?: string
  hsts?: HSTS
  headers: HeaderCheck[]
}

export type ScanFinding = {
  id: string
  level: "critical" | "warning" | "notice"
  title: string
  detail: string
  advice?: string
}

export type TLSScan = {
  domain: string
  port: number
  checkedAt: string
  reachable: boolean
  error?: string
  grade: string
  summary: string
  negotiated?: string
  cipherSuite?: string
  protocols: ProtocolResult[]
  certificate?: Certificate
  chain: ChainLink[]
  chainComplete: boolean
  trusted: boolean
  trustError?: string
  nameMatches: boolean
  keyType?: string
  keyBits?: number
  signatureAlgorithm?: string
  fingerprint?: string
  serial?: string
  ocspStapled: boolean
  http?: HTTPScan
  findings: ScanFinding[]
}

export type DomainCheck = {
  domain: string
  addresses: string[]
  hostAddresses: string[]
  /**
   * False on every VPS behind provider NAT — AWS, Google Cloud, Azure and
   * Oracle all give the instance a private address and map a public one in
   * front of it — where the comparison cannot be made at all. Rendered as
   * "cannot tell", never as a mismatch.
   */
  hostAddressesKnown: boolean
  pointsHere: boolean
  behindProxy: boolean
  summary: string
  error?: string
}

export type CertbotCert = {
  name: string
  domains: string[]
  expiry: string
  daysLeft: number
  valid: boolean
  certPath?: string
  keyPath?: string
  serial?: string
}

export type CertbotState = {
  available: boolean
  version?: string
  certs: CertbotCert[]
  /** Whether anything is scheduled to renew these, and what. */
  autoRenew: boolean
  renewSource?: string
  /** A certbot timer systemd knows but is not running: the thing to turn on. */
  renewUnit?: string
  raw?: string
  error?: string
}

/** A site as the dashboard describes it, not as nginx does. */
export type SiteLocation = {
  path: string
  upstream?: string
  root?: string
  webSockets: boolean
}

export type SiteSpec = {
  managedAcme?: boolean
  name: string
  domains: string[]
  kind: "proxy" | "static" | "redirect"
  upstream?: string
  root?: string
  redirectTo?: string
  permanent?: boolean
  tls: boolean
  certPath?: string
  keyPath?: string
  forceHttps: boolean
  hsts: boolean
  http2: boolean
  webSockets: boolean
  gzip: boolean
  blockExploits: boolean
  securityHeaders: boolean
  clientMaxBody?: string
  proxyTimeout?: number
  allowFrom: string[]
  denyFrom: string[]
  basicAuthFile?: string
  basicAuthRealm?: string
  accessLog: boolean
  locations: SiteLocation[]
  custom?: string
}

/**
 * The server's own config test. `note` qualifies a verdict nginx could not
 * actually give — a file outside its include tree passes `nginx -t` without
 * being read.
 */
export type ProxyValidation = { valid: boolean; output: string; command: string; note?: string }

export type SiteResult = {
  name: string
  path: string
  content: string
  warnings: string[]
  validation?: ProxyValidation
  enabled: boolean
  reloaded: boolean
  output?: string
}

/** A certbot DNS plugin — the only way to a wildcard, or past a CDN. */
export type DNSProvider = {
  key: string
  name: string
  plugin: string
  installed: boolean
  credentials: string
  defaultWait: number
  /** A token is saved for this provider. The token itself is never read back. */
  hasCredentials: boolean
}

export type ImportResult = {
  name: string
  certPath: string
  keyPath: string
  certificate: Certificate
  chainComplete: boolean
  warnings: string[]
}

/** One forwarded port for something that does not speak HTTP. */
export type StreamSpec = {
  name: string
  listen: number
  protocol: "tcp" | "udp"
  upstream: string
  proxyProtocol: boolean
  timeout?: number
  allowFrom: string[]
}

export type StreamStatus = {
  /** Whether nginx.conf actually pulls these in. Without it they are ignored. */
  included: boolean
  snippet: string
  dir: string
  streams: StreamSpec[]
}

/** An htpasswd file and who is in it. */
export type AuthFile = {
  name: string
  path: string
  users: string[]
}

/**
 * One long-running operation — a certificate issuance, a package upgrade, an
 * sshd apply.
 *
 * Unlike the compose runner's socket, a job is not owned by the console
 * watching it: closing the tab does not stop it, and reopening picks it up
 * where it is rather than starting it again.
 */
export type JobStatus = "running" | "succeeded" | "failed" | "cancelled"

export type Job = {
  id: string
  kind: string
  title: string
  target?: string
  status: JobStatus
  exitCode: number
  error?: string
  startedAt: string
  endedAt?: string
  startedBy?: string
  /** Every line produced, including any the buffer has dropped. */
  lines: number
}

export type JobLine = {
  /** Assigned by the job, and what a reconnecting console resumes from. */
  seq: number
  /** stdout, stderr, or status for the runner's own headings. */
  stream: string
  text: string
  at: string
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
}

export type DeploymentRequests = {
  status: "available" | "unavailable"
  reason?: string
  /** Which server wrote the record: the two drivers do not record the same things. */
  driver?: string
  format?: string
  /** Whether this format carries a request duration at all. */
  latency: boolean
  /** What is held reaches the start of the retained record; false means every figure is a floor. */
  complete: boolean
  observedAt: string
  entries: RequestEntry[]
  slowest?: RequestEntry[]
  summary: RequestSummary
  coverage: RequestCoverage
}

/** One window's worth of traffic, small enough to sit beside a release or on a card. */
export type TrafficReading = {
  requests: number
  pages: number
  perMinute: number
  errorRate: number
  p95?: number
  from: string
  until: string
}

/** What a release did to the traffic: the same reading either side of its activation. */
export type RunTraffic = {
  status: "available" | "unavailable"
  reason?: string
  activationCompletedAt?: string
  windowMinutes: number
  before?: TrafficReading
  after?: TrafficReading
  latency: boolean
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
  since?: string
  events: DockerEvent[]
}
