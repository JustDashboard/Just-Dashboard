/*
 * Processes and services: the process table and its inspection, systemd
 * units, timers and their security, PM2, and cron.
 */

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
  /** PM2 stamps each line it writes (`--time`, `log_date_format`), so the log can be read by time. */
  logTimes?: boolean
}

/** One account's PM2 daemon: whether what it runs would survive a reboot. */
export type PM2Daemon = {
  account: string
  home: string
  /** When `pm2 save` last wrote the resurrection list; absent when it never has. */
  dumpSavedAt?: string
  /** The application names that list holds; null when there is none or it could not be read. */
  savedApps?: string[] | null
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
  /** What the timer's unit says it is for. */
  description?: string
  /** Each `OnCalendar=` it fires on, as systemd normalises it. */
  calendar?: string[]
  /** Each interval it fires on, as the unit file spells it: `OnBootSec=15min`. */
  monotonic?: string[]
  /** `RandomizedDelaySec`, as systemd writes the span; absent for none. */
  randomDelay?: string
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
  /** simple, notify, forking, oneshot… */
  type?: string
  /** When it last started, stopped, finished or failed (unix seconds). */
  changedAt?: number
  /** A share of one core over the last measured window; 200 is two cores. */
  cpuPercent?: number
  /** False until the unit has been read twice a window apart. */
  cpuReady?: boolean
  /** How its main process last ended: exited, killed or dumped. */
  exitCode?: string
  /** The exit status, or the signal's number when killed or dumped. */
  exitStatus?: number
  /** Its recent measured windows, oldest first; only the detail route returns them. */
  history?: SystemdPoint[]
}

export type SystemdPoint = { t: number; cpu: number; memory: number }

export type SystemdManager = {
  version: string
  /** running, degraded, starting, stopping, maintenance… */
  state: string
  bootedAt?: number
}

export type SystemdList = {
  available: boolean
  units: SystemdUnit[]
  manager?: SystemdManager
  ratesReady?: boolean
}

export type ProcessRow = {
  pid: number
  ppid: number
  name: string
  cmdline: string
  username: string
  status: string
  cpuPercent: number
  cpuReady?: boolean
  cpuWindowSeconds?: number
  memPercent: number
  rss: number
  vms: number
  swap?: number
  memoryReady?: boolean
  /** Resident memory backed by files and shared mappings. */
  shared?: number
  threads: number
  nice: number
  createTime: string
  cwd?: string
  exe?: string
  ioReadBytes?: number
  ioWriteBytes?: number
  ioReadRate?: number
  ioWriteRate?: number
  ioReady?: boolean
  fileDescriptors?: number
  fdReady?: boolean
  children?: number
  state: "running" | "sleeping" | "blocked" | "stopped" | "zombie" | "other"
  manager: "pm2" | "systemd" | "container" | "session" | "kernel" | "unmanaged"
  managerName?: string
  /** The supervisor as its owner names it: a container's name beside its id. */
  managerLabel?: string
  /** Detail only: what it listens on and how many connections it holds. */
  listening?: ListeningPort[]
  connections?: number
  openFilesLimit?: number
  /** Detail only: its measured windows, oldest first. */
  history?: ProcessSample[]
}

/** One measured interval of a process. */
export type ProcessSample = {
  at: string
  cpu: number
  rss: number
  read: number
  write: number
}

/**
 * One workload: a supervisor's processes, or the copies of one program
 * started by hand, with shared pages counted once.
 */
export type ProcessGroup = {
  key: string
  manager: ProcessRow["manager"]
  name: string
  label?: string
  count: number
  cpuPercent: number
  memory: number
  ioRate: number
  /** The group's heaviest process. */
  pid: number
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
  /** False until the sampler has measured it; its CPU is then unknown. */
  cpuReady?: boolean
  rss: number
  createTime: string
}

export type ProcessTree = {
  /** Outermost first, ending with the direct parent. */
  ancestors: ProcessLink[]
  children: ProcessLink[]
}

export type InspectID = { id: number; name?: string }

/**
 * Everything else the kernel keeps about one process, from
 * `GET /processes/{pid}/inspect` — the process page's slower read. Each block
 * carries `ready: false` where this account could not read it.
 */
export type ProcessInspect = {
  pid: number
  createTime: string
  argv: string[]
  exe?: string
  /** The file it was started from has been replaced since: a restart picks up the new one. */
  exeDeleted?: boolean
  root?: string
  waitChannel?: string
  namespacePid?: number
  identity: {
    ready: boolean
    user: InspectID
    effectiveUser: InspectID
    savedUser: InspectID
    group: InspectID
    effectiveGroup: InspectID
    groups: InspectID[]
    terminal?: string
    session: number
    processGroup: number
    foreground?: boolean
  }
  scheduling: {
    ready: boolean
    policy: string
    realtimePriority?: number
    nice: number
    cpus?: string
    cpuCount: number
    hostCpus: number
    lastCpu: number
    userSeconds: number
    systemSeconds: number
    childrenUserSeconds: number
    childrenSystemSeconds: number
    ioWaitSeconds: number
    voluntarySwitches: number
    involuntarySwitches: number
    minorFaults: number
    majorFaults: number
  }
  memory: {
    ready: boolean
    resident: number
    anonymous: number
    fileBacked: number
    sharedMemory: number
    peak: number
    virtual: number
    virtualPeak: number
    data: number
    stack: number
    text: number
    libraries: number
    locked: number
    pageTables: number
    swap: number
    proportional: number
    unique: number
    rollupReady: boolean
    oomScore: number
    oomScoreAdj: number
    oomReady: boolean
  }
  io: {
    ready: boolean
    charsRead: number
    charsWritten: number
    readCalls: number
    writeCalls: number
    readBytes: number
    writeBytes: number
    cancelledWriteBytes: number
  }
  security: {
    ready: boolean
    capabilities: string[]
    allCapabilities: boolean
    boundingAll: boolean
    noNewPrivs: boolean
    seccomp: "off" | "strict" | "filter"
    seccompFilters?: number
    label?: string
  }
  signals: {
    ready: boolean
    caught: string[]
    ignored: string[]
    blocked: string[]
    pending: string[]
  }
  namespaces: { kind: string; own: boolean }[]
  cgroup: {
    path?: string
    memoryLimit?: number
    memoryCurrent?: number
    cpuLimit?: number
    pidsLimit?: number
    pidsCurrent?: number
  }
  limits: { name: string; soft: string; hard: string; unit?: string }[]
  files: {
    ready: boolean
    total: number
    files: number
    sockets: number
    pipes: number
    devices: number
    other: number
    deleted: number
    entries: {
      fd: number
      kind: "file" | "socket" | "pipe" | "device" | "other"
      target: string
      deleted?: boolean
    }[]
    truncated?: boolean
  }
  libraries: { path: string; deleted?: boolean }[]
  connections: { proto: string; local: string; remote?: string; state?: string }[]
  connectionsTotal: number
  environment: string[]
  environmentReadable: boolean
  threads: { tid: number; name: string; state: string; cpuSeconds: number; lastCpu: number }[]
  threadsTotal: number
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
  /** The heaviest workloads by CPU and by memory, over the whole host. */
  groups?: ProcessGroup[]
}

export type SystemdUnitDetail = {
  unit: SystemdUnit
  properties: Record<string, string>
}

/**
 * `GET /systemd/{name}/files` — the unit file and its drop-ins as systemd
 * loaded them (`FragmentPath`, then `DropInPaths` in the order they apply).
 * Credential-shaped `Environment=` values are masked for anyone below
 * system.admin.
 */
export type SystemdUnitFiles = {
  files: SystemdUnitFile[]
  /** The files changed on disk since systemd last read them. */
  needDaemonReload: boolean
}

export type SystemdUnitFile = {
  path: string
  dropIn: boolean
  content: string
  size: number
  /** Cut at 256 KiB. */
  truncated: boolean
  /** Why it could not be read: missing, not a regular file, masked to /dev/null. */
  error?: string
}

/**
 * `GET /systemd/{name}/security` — `systemd-analyze security`: how much of
 * the host a service can reach, 0 (locked down) to 10 (nothing applied),
 * and each setting that counts toward it, most exposed first.
 */
export type SystemdSecurity = {
  available: boolean
  score?: number
  /** SAFE, OK, MEDIUM, EXPOSED or UNSAFE. */
  verdict?: string
  checks: SystemdSecurityCheck[]
  reason?: string
}

export type SystemdSecurityCheck = {
  /** The setting, as systemd names it: `ProtectSystem=`, `CapabilityBoundingSet=~CAP_SYS_ADMIN`. */
  name: string
  field: string
  /** What the setting's current value means, in systemd's words. */
  description: string
  /** Whether it is applied; null where it does not apply to this unit. */
  set: boolean | null
  /** What it adds to the score while unapplied; null where it counts for nothing. */
  exposure: number | null
}

/** `GET /systemd/{name}/related` — every unit it names in a dependency, with its state. */
export type SystemdRelated = {
  units: SystemdRelatedUnit[]
}

export type SystemdRelatedUnit = {
  name: string
  description: string
  loadState: string
  activeState: string
  subState: string
  unitFileState: string
  /** The dependency properties that named it: `Requires`, `After`… */
  relations?: string[]
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
  timeZone?: string
}

export type Crontab = {
  user: string
  source: string
  raw: string
  revision?: string
  timeZone?: string
  jobs: CronJob[]
  env: string[]
  comments: string[]
}
