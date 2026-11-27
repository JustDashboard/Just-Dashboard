/*
 * The server itself: its name and host facts, live metrics and their
 * history, pressure, sensors, mounts and interfaces, directory sizes, the
 * events a chart marks, and the map of what runs on it.
 */

/** `GET /system/hostname`: what the rename form opens on. */
export type ServerName = {
  hostname: string
  /** What the server is called on its tailnet, when it is on one. A rename leaves it. */
  tailscaleName?: string
}

/** `PUT /system/hostname`: a finished rename. */
export type ServerRename = ServerName & {
  previous: string
  /** What could not be finished around a name that is set. */
  warnings?: string[]
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
  /** How connections fare: TCP's own rates and established connections' RTT. */
  tcp?: TCPStats
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

export type TCPStats = {
  supported: boolean
  outSegsRate: number
  retransRate: number
  attemptFailsRate: number
  estabResetsRate: number
  listenDropsRate: number
  retransPercent: number
  /** The kernel's smoothed RTT of established connections to peers elsewhere. */
  latency: { supported: boolean; sockets: number; medianMs: number; p90Ms: number }
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

  /** Null where the bucket holds no measurement — before these were sampled. */
  retransPct: number | null
  retransPctPeak: number | null
  attemptFails: number | null
  attemptFailsPeak: number | null
  resets: number | null
  listenDrops: number | null
  listenDropsPeak: number | null
  rtt: number | null
  rttPeak: number | null

  load5: number
  load15: number
  memAvailable: number
  procs: number
  procsPeak: number
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
  /** The project a deploy event released. */
  projectId?: number
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
  kind: NetInterface["kind"]
}

export type DirEntry = {
  name: string
  path: string
  size: number
  isDir: boolean
  entries: number
  /** The scan ran out of time or work before it had counted all of it: `size` is a floor. */
  partial?: boolean
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

// ---------------------------------------------------------------------------
// The server map: one summary of everything the dashboard manages.
//
// These mirror api/handlers_map.go. Each kind is read from the source its own
// page reads, summarised to what a box on the map draws — a name, a state, one
// fact and the page that manages it — so one request draws the whole server.
// ---------------------------------------------------------------------------

/** A thing's state as the map colours it: measured, never guessed. */
export type MapTone = "ok" | "warning" | "danger" | "idle" | "unknown"

export type MapItem = {
  /** Stable across readings: `container:<id>`, `project:12`, `domain:shop.example.com`. */
  id: string
  name: string
  /** The product its logo is drawn from, where one is known. */
  product?: string
  /** A deployment's project, whose website's icon draws it. */
  projectId?: number
  /** An icon the dashboard serves for it — a project's website's favicon. */
  icon?: string
  tone: MapTone
  /** The state in a word or two: "running", "exited (137)", "answers", "failed". */
  state: string
  /** One more fact: an image, a size, when it last ran. */
  detail?: string
  /** The page that manages it. */
  href: string
  /** What the inspector lists under it: a few label and value pairs. */
  facts?: MapFact[]
}

/** Two things wired together, and how the server knows: a label, a route, a mount. */
export type MapLink = {
  from: string
  to: string
  /** How the server knows the two are joined, the surest way where it knows several. */
  via: string
  /** The other ways it knows the same pair is joined; one line is drawn for all of them. */
  also?: string[]
}

export type MapFact = { label: string; value: string; tone?: MapTone }

export type MapKindReading = {
  key: string
  /** How many exist; `items` may hold fewer where the list was capped. */
  total: number
  items: MapItem[]
  /**
   * read: the list arrived. unread: its source failed, and `reason` says why —
   * never drawn as empty. limited: this role is shown part of it. absent: this
   * server does not run the module (no PM2, no Docker), so there is no shelf.
   */
  state: "read" | "unread" | "limited" | "absent"
  reason?: string
  /** A kind summarised rather than listed — packages, services — says it in facts. */
  facts?: MapFact[]
}

export type ServerMap = {
  host: {
    hostname: string
    platform?: string
    platformVersion?: string
    kernel?: string
    uptimeSeconds?: number
  }
  kinds: MapKindReading[]
  links: MapLink[]
  checkedAt: string
}

export type MapLayoutResponse = { layout: unknown; updatedAt?: string }
