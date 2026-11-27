/*
 * Security: the firewall's service presets and app profiles, fail2ban jails
 * and blocks, the access boundary, logins and attackers, how the dashboard
 * is exposed, sshd, CrowdSec and Suricata.
 */

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

/** What a jail's numbers and actions mean together, read from the running server. */
export type JailPolicy = {
  name: string
  rule: string
  values: { key: string; running: string; dropIn?: string; drift?: boolean }[]
  watches: { kind: "files" | "journal" | "none"; files?: string[]; match?: string }
  actions: {
    name: string
    kind: "firewall" | "route" | "edge" | "report" | "unknown"
    enforces: boolean
    allPorts: boolean
    ports?: string[]
    words: string
  }[]
  coverage?: { service: string; listening: string[]; covered: string[]; uncovered: string[] }
  ignoreSelf: boolean
  ignoreIp: string[]
  findings: { level: "critical" | "warning" | "notice"; text: string }[]
  error?: string
}

export type BoundaryState = "held" | "broken" | "unknown"

export type BoundaryCheck = {
  id: "ingress" | "allowlist" | "tailnet" | "ssh" | "previews"
  state: BoundaryState
  title: string
  detail: string
  facts?: string[]
}

/** How the dashboard and its previews are reached, read as they are. */
export type AccessBoundary = {
  checks: BoundaryCheck[]
  checkedAt: string
  allowlist: string[]
  client?: string
  operator?: string
  caddyPort: number
  sshPorts: string[]
  tailnetIp?: string
  previewMin: number
  previewMax: number
}

export type BoundaryProposal = {
  kind: "ban" | "firewall.rule" | "firewall.policy" | "ssh"
  target?: string
  action?: string
  port?: string
  protocol?: string
  policy?: string
  settings?: Record<string, string>
}

/** A cut ends this session's way in and is refused; an effect needs acknowledgement. */
export type BoundaryImpact = {
  boundary: BoundaryCheck["id"]
  level: "cuts" | "affects"
  text: string
}

export type BoundaryChange = {
  id: BoundaryCheck["id"]
  title: string
  before: BoundaryState
  after: BoundaryState
  detail: string
  lost: boolean
}

/** What a reconnection verification says about the boundary after a pending apply. */
export type BoundaryAfterVerify = {
  before: boolean
  checks: BoundaryCheck[]
  changes: BoundaryChange[]
  lost: number
}

export type BlockEngine = "fail2ban" | "crowdsec" | "firewall"

export type BlockedEntry = {
  value: string
  range: boolean
  sources: {
    engine: BlockEngine
    ref: string
    detail?: string
    origin?: string
    until?: string
    community?: boolean
  }[]
  engines: BlockEngine[]
  coveredBy?: string[]
}

/** fail2ban's bans, CrowdSec's decisions and the firewall's denies, folded by address. */
export type BlocksView = {
  entries: BlockedEntry[]
  distinct: number
  duplicated: number
  covered: number
  communityOnly: number
  truncated: boolean
  engines: { engine: BlockEngine; read: boolean; count: number; note?: string }[]
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

export type SSHSetting = {
  key: string
  label: string
  value: string
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

export type CrowdSecView = {
  installed: boolean
  active: boolean
  decisions: {
    id: number
    origin: string
    scenario: string
    scope: string
    value: string
    type: string
    duration: string
    until?: string
    alertId: number
    country?: string
    as?: string
  }[]
  alerts: {
    id: number
    scenario: string
    source: { ip?: string; scope?: string; value?: string; country?: string; asName?: string }
    eventsCount: number
    createdAt: string
    decisions: number
  }[]
  bouncers: {
    name: string
    ipAddress?: string
    valid: boolean
    lastPull?: string
    type?: string
    version?: string
  }[]
  /** Whether a bouncer verifiably turns decisions into dropped traffic. */
  enforcement?: CrowdSecEnforcement
  error?: string
}

export type CrowdSecEnforcementState =
  "stopped" | "unenforced" | "stale" | "degraded" | "unverified" | "partial" | "enforcing"

export type CrowdSecEnforcement = {
  state: CrowdSecEnforcementState
  summary: string
  bouncers: {
    name: string
    kind: "firewall" | "proxy" | "other"
    valid: boolean
    lastPull?: string
    /** Seconds since the last pull; -1 for never. */
    pullAgeSeconds: number
    fresh: boolean
    unit?: string
    unitActive?: boolean
  }[]
  kernel?: {
    backend?: "nftables" | "ipset"
    sets: {
      family?: string
      table?: string
      name: string
      entries: number
      dropped: boolean
      hooks?: string[]
    }[]
    entries: number
    error?: string
  }
  checkedAt: string
  /** How recent a pull must be, in Go's duration spelling ("3m0s"). */
  freshness: string
  enforcedBy: string[]
  missing?: { bouncer?: string; reason: string }[]
}

export type SuricataView = {
  installed: boolean
  active: boolean
  version?: string
  mode: "ids" | "ips"
  modeSource?: string
  alerts: {
    time: string
    srcIp: string
    srcPort?: number
    destIp: string
    destPort?: number
    proto?: string
    appProto?: string
    signature: string
    signatureId: number
    category?: string
    severity: number
    action?: "allowed" | "blocked"
  }[]
  scanned: number
  bySeverity: { severity: number; label: string; count: number }[]
  topSignatures: {
    signature: string
    signatureId: number
    category?: string
    severity: number
    count: number
  }[]
  rulesLoaded?: number
  logPath: string
  logRefused?: string
  logError?: string
  /** The newest stats event in eve.json: whether the capture sees packets. */
  capture?: {
    at: string
    uptimeSeconds: number
    kernelPackets: number
    kernelDrops: number
    decoderPackets: number
  }
  interfaces: {
    configured: string[]
    candidates: string[]
    editable: boolean
    reason?: string
  }
  rules: { file: string; updatedAt?: string; sources: string[]; updater: boolean }
  /** Read only: the queue rules an inline deployment depends on. */
  inline: {
    queues: {
      source: string
      chain: string
      queue: string
      bypass: boolean
      rule: string
    }[]
    failOpen: boolean
    words: string
    error?: string
  }
}
