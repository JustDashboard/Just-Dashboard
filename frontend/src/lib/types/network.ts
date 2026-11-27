/*
 * Networking: the firewall, links and addresses, routing and its rules,
 * BGP and OSPF, VPNs (WireGuard, Tailscale, Headscale), DNS and host records,
 * connections and per-process traffic, the gateway, protection and shaping.
 */

import type {
  AppliedQueue,
  CakeTin,
  ConnectionQuality,
  EBPFPlatform,
  Offload,
  Percentiles,
  QdiscNode,
  RecordedHistory,
  ShapeOwnership,
  TCPLatency,
  TCPPoint,
  UploadProfile,
} from "@/lib/network/traffic"

export type FirewallRule = {
  number?: number
  /** Stable identity: survives renumbering, and an edit naming a stale one is refused. */
  id?: string
  /** The device a rule is scoped to ("on eth0"). */
  interface?: string
  /** The firewalld zone holding the rule. */
  zone?: string
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

export type FirewallBackend = "ufw" | "firewalld" | "iptables" | "nftables"

/** One firewall the host could be run by, and whether it is the one in charge. */
export type FirewallDetection = {
  backend: FirewallBackend
  installed: boolean
  active: "active" | "inactive" | "unknown" | "not_checked"
  selected: boolean
  reason: string
}

export type FirewallZone = {
  name: string
  default: boolean
  target: string
  interfaces: string[]
  sources: string[]
  rules: FirewallRule[]
}

export type FirewallFamilyPolicy = {
  family: "ipv4" | "ipv6"
  filtered: boolean
  incoming?: string
  outgoing?: string
  routed?: string
  reason?: string
}

export type FirewallInterfacePolicy = {
  interface: string
  zone?: string
  incoming: string
  rules: number
  reason?: string
}

export type FirewallRuleFinding = {
  ruleId: string
  number: number
  kind: "shadowed" | "redundant"
  byId: string
  byNumber: number
  reason: string
}

/** One way in and whether the firewall admits it. */
export type FirewallAccessCheck = {
  name: string
  port: number
  protocol: string
  family: "ipv4" | "ipv6"
  source: string
  interface?: string
  required: boolean
  verdict: "admitted" | "limited" | "refused" | "unfiltered" | "unknown"
  rule?: number
  ruleId?: string
  reason: string
}

export type FirewallStatus = {
  backend: FirewallBackend
  available: boolean
  enabled: boolean
  defaultPolicy?: string
  policy: DefaultPolicy
  logging?: string
  /** firewalld's active zone. Absent for backends with no such idea. */
  zone?: string
  capabilities: FirewallCapabilities
  rules: FirewallRule[]
  /** "configured" when an inactive firewall's rules were read from its configuration. */
  rulesFrom?: "configured"
  raw?: string
  error?: string
  detection?: FirewallDetection[]
  zones?: FirewallZone[]
  effective?: { families: FirewallFamilyPolicy[]; interfaces: FirewallInterfacePolicy[] }
  findings?: FirewallRuleFinding[]
  analysis?: string
  /** Other nftables tables beside the dashboard's own, which keep enforcing. */
  foreign?: string[]
}

/** The requester's own ways in and Caddy's public ingress under the current rules. */
export type FirewallAccess = { backend: FirewallBackend; checks: FirewallAccessCheck[] }

export type FirewallAccessComparison = FirewallAccessCheck & {
  before: FirewallAccessCheck["verdict"]
}

/** A proposed firewall change judged before it is made. */
export type FirewallPreflight = {
  backend: FirewallBackend
  checks: FirewallAccessComparison[]
  findings: FirewallRuleFinding[]
  refusal?: string
}

export type FirewallPlanOperation = {
  op: "add" | "replace" | "delete"
  ruleId?: string
  rule?: Record<string, unknown>
}

export type FirewallPlanReview = FirewallPreflight & {
  steps: {
    op: "add" | "replace" | "delete"
    ruleId?: string
    number?: number
    description: string
    outcome: "pending" | "applied" | "failed" | "skipped" | "compensated"
    detail?: string
  }[]
}

export type FirewallRuleEvent = {
  id: number
  at: string
  actor: string
  backend: FirewallBackend
  operation: string
  ruleId?: string
  previousRuleId?: string
  rule?: Record<string, unknown>
  previous?: Record<string, unknown>
  outcome: "applied" | "refused" | "failed" | "pending" | "compensated" | "skipped"
  detail?: string
  changeId?: string
}

export type FirewallRuleHistory = { events: FirewallRuleEvent[]; limits: string[] }

export type Peer = {
  address: string
  count: number
  established: number
  ports: number[]
  processes: string[]
  private: boolean
  service?: string
  /** What folding by address keeps of each socket: transports, far-end ports, states. */
  protocols?: string[]
  remotePorts?: number[]
  morePorts?: number
  states?: Record<string, number>
}

export type Connections = {
  peers: Peer[]
  total: number
  listening: number
  loopback: number
  quality?: ConnectionQuality
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

/*
 * The Network section (`backend/internal/netx`). Every shape below is the
 * module's JSON as it is written; the comments say what a field means where
 * the name alone does not.
 */

/** One network device, joined with what the rest of the host says about it. */
export type NetworkLink = {
  name: string
  index: number
  /** The kernel's driver kind (bridge, veth, vlan, vxlan, wireguard, tun, …), "physical" or "loopback". */
  kind: string
  role: "uplink" | "loopback" | "tunnel" | "bridge" | "container" | "vlan" | "virtual" | "physical"
  owner:
    | "kernel"
    | "system"
    | "docker"
    | "tailscale"
    | "wireguard"
    | "libvirt"
    | "lxd"
    | "just-dashboard"
  /** Operational state, lower-cased. A working tunnel reads "unknown". */
  state: string
  adminUp: boolean
  carrier: boolean
  mtu: number
  mac?: string
  qdisc?: string
  speedMbps?: number
  master?: string
  members?: string[]
  parent?: string
  vlanId?: number
  vni?: number
  local?: string
  remote?: string
  port?: number
  addresses: NetworkAddress[]
  uplink: boolean
  /** The reply to this browser leaves through this device. */
  clientPath?: boolean
  counters: {
    rxBytes: number
    txBytes: number
    rxPackets: number
    txPackets: number
    rxErrors: number
    txErrors: number
    rxDropped: number
    txDropped: number
  }
  /** Bytes a second over the sampler's last two seconds. */
  rxRate: number
  txRate: number
  container?: string
  containerImage?: string
  dockerNetwork?: string
  xdp?: string
  managed: boolean
  /** Why it may not be set down, deleted or re-parented. */
  guard?: string
  /**
   * Set where a Docker device's join to its container or network could not be
   * made, with the reason: "unresolved" veth, "unknown" bridge network.
   */
  dockerJoin?: "unresolved" | "unknown"
  dockerJoinReason?: string
  /** A dashboard-made veth's other end and the managed namespace it lives in. */
  peer?: string
  peerNamespace?: string
  /** A dashboard-made unicast VXLAN's further flood ends. */
  remotes?: string[]
}

export type NetworkAddress = {
  cidr: string
  family: "inet" | "inet6"
  scope: string
  dynamic?: boolean
  public?: boolean
  managed?: boolean
  guard?: string
  /** How the kernel says the address came to be. */
  origin?: "static" | "dhcp" | "slaac" | "temporary" | "dynamic" | "link-local"
  /** Remaining lifetimes of a dynamic address; absent is forever. */
  validSeconds?: number
  preferredSeconds?: number
}

/** Whether one part of a detailed reading arrived, and why not. */
export type NetworkReading = {
  state: "ok" | "unavailable" | "not_applicable" | "failed"
  reason?: string
  package?: string
}

/** A device's driver, offloads and every error counter, read when its sheet opens. */
export type NetworkLinkDetail = {
  name: string
  checkedAt: string
  driver?: { name: string; version?: string; firmware?: string; bus?: string }
  driverRead: NetworkReading
  offloads: { name: string; enabled: boolean; fixed: boolean }[]
  offloadsRead: NetworkReading
  errors?: Record<
    | "rxErrors"
    | "rxDropped"
    | "rxOverErrors"
    | "rxLengthErrors"
    | "rxCrcErrors"
    | "rxFrameErrors"
    | "rxFifoErrors"
    | "rxMissedErrors"
    | "txErrors"
    | "txDropped"
    | "txCarrierErrors"
    | "txCollisions"
    | "txAbortedErrors"
    | "txFifoErrors"
    | "txWindowErrors"
    | "txHeartbeatErrors"
    | "carrierChanges",
    number
  >
  errorsRead: NetworkReading
}

/** One VLAN a bridge port carries. */
export type NetworkPortVLAN = { vid: number; pvid?: boolean; untagged?: boolean }

/** A bridge read as a switch. */
export type NetworkBridgeView = {
  name: string
  managed: boolean
  checkedAt: string
  stp: boolean
  vlanFiltering: boolean
  multicastSnooping: boolean
  defaultPvid: number
  ageingSeconds: number
  vlanProtocol?: string
  settingsRead: NetworkReading
  ports: {
    name: string
    self: boolean
    managed: boolean
    vlans: NetworkPortVLAN[]
    desired?: NetworkPortVLAN[]
  }[]
  vlansRead: NetworkReading
  fdb: { mac: string; port: string; vlan?: number; state?: string; dst?: string; static: boolean }[]
  fdbTotal: number
  fdbRead: NetworkReading
}

/** What joining a device to a bridge, or leaving one, would do. */
export type NetworkMasterPreview = {
  device: string
  bridge?: string
  allowed: boolean
  refusal?: string
  persisted: boolean
  steps: string[]
  effects: { kind: string; detail: string }[]
}

/** What this host can establish about whether a tunnel or virtual device can carry traffic. */
export type NetworkLinkReadiness = {
  name: string
  kind: string
  checkedAt: string
  checks: {
    id: string
    label: string
    state: "ok" | "warning" | "failed" | "unknown" | "info"
    detail: string
  }[]
  limits: string[]
}

/** One namespace read as its own network. */
export type NetworkNamespaceDetail = {
  name: string
  kind: "named" | "container"
  managed: boolean
  image?: string
  pid?: number
  checkedAt: string
  devices: NetworkNamespace["devices"]
  devicesRead: NetworkReading
  routes: {
    family: "inet" | "inet6"
    destination: string
    type?: string
    gateway?: string
    device?: string
    source?: string
    protocol?: string
    metric?: number
    table?: string
  }[]
  routesRead: NetworkReading
  dns?: { nameservers: string[]; search: string[]; options: string[] }
  dnsRead: NetworkReading
  listeners: { protocol: string; address: string; port: number }[]
  listenersRead: NetworkReading
}

/** One two-second reading of a device: bytes and packets a second, faults the step added. */
export type NetworkLivePoint = {
  t: number
  rx: number
  tx: number
  rxp?: number
  txp?: number
  err?: number
  drop?: number
}

export type NetworkLive = {
  now: number
  series: Record<string, NetworkLivePoint[]>
  /** When the newest reading was taken, unix seconds; 0 before the first. */
  sampledAt?: number
  stepSeconds?: number
  tcp?: TCPPoint[]
  tcpError?: string
  latency?: TCPLatency
}

export type NetworkHistoryPoint = {
  t: number
  rx: number
  tx: number
  rxPeak: number
  txPeak: number
  errors: number
  dropped: number
}

export type NetworkHistory = {
  from: number
  to: number
  stepSeconds: number
  interfaces: Record<string, NetworkHistoryPoint[]>
  recording: boolean
  percentiles?: Record<string, Percentiles>
  retainedFrom?: number
}

/** How the kernel answers one address: this browser's, on every Network page. */
export type NetworkPath = {
  address: string
  device?: string
  gateway?: string
  source?: string
  /** The table the kernel says answered, when it names one; absent for main. */
  table?: string
  local?: boolean
}

export type NetworkChangeStatus = {
  id: string
  phase:
    | "prepared"
    | "runtime_applied"
    | "persisted"
    | "saved"
    | "awaiting_confirmation"
    | "confirmed"
    | "recovering"
    | "recovered"
    | "degraded"
    | "boot_degraded"
    | "unreadable"
  generation: string
  updatedAt: string
  watchdog: "unsupported" | "armed" | "completed" | "failed_to_arm" | "recovered"
  runtime: "not_applied" | "applied" | "undo_attempted" | "restored" | "unknown"
  persistence: "not_written" | "written" | "restored" | "unknown" | "not_applicable"
  boot: "not_verified" | "enabled" | "unsupported" | "failed" | "unknown" | "not_applicable"
  recoveryErrors?: string[]
  cleanup?: "pending" | "failed" | "complete"
  /** A journal enrolled by another owner than the network spec: sshd's apply. */
  subsystem?: "sshd"
  ownerUserId?: number
  expiresAt?: string
  appliedAt?: string
  verifiedAt?: string
  /** What a routing change was checked against after it applied. */
  validation?: NetworkChangeValidation
}

export type NetworkChangeValidation = {
  checkedAt: string
  client: boolean
  sourceSelected: boolean
  anchors: string[]
  flows: number
  moved: { address: string; before: string; after: string }[]
}

export type NetworkPersistence = {
  unit: "enabled" | "disabled" | "missing" | "unsupported"
  made: number
  dir: string
  change?: NetworkChangeStatus
}

export type NetworkDefaultRoute = {
  family: "inet" | "inet6"
  device: string
  gateway?: string
  metric?: number
}

/** The Network Overview's one read. */
export type NetworkOverview = {
  hostname: string
  client: NetworkPath
  defaults: NetworkDefaultRoute[]
  /** The globally routable addresses on the uplinks: what the internet reaches. */
  publicAddresses: string[]
  links: NetworkLink[]
  dockerNetworks: {
    id: string
    name: string
    bridge?: string
    subnets: string[]
    containers: { name: string; image: string }[]
  }[]
  firewall: {
    backend?: string
    available: boolean
    enabled: boolean
    incoming?: string
    rules: number
  }
  connections: { total: number; peers: number; fromInternet: number; listening: number }
  forwarding: { ipv4: boolean; ipv6: boolean }
  persistence: NetworkPersistence
  vpn: VPNSummary
  dns?: DNSSummary
  gateway: {
    loaded: boolean
    writable: boolean
    reason?: string
    forwards: number
    nat: number
    limits: number
    blocklists: number
    /** Packets the blocklists and limits refused since the table was loaded. */
    dropped: number
    conntrack: Conntrack
  }
  made: {
    links: number
    routes: number
    rules: number
    forwards: number
    nat: number
    limits: number
    blocklists: number
    shaping: number
    namespaces: number
  }
  /** Each family's way out, and what it establishes about the address the internet sees. */
  identity: NetworkEgressIdentity[]
  /** The topology's edges as connection tracking holds them. */
  flows: NetworkTopologyFlows
  /** Which readings arrived; a failed one is not an empty answer. */
  observations: NetworkObservation[]
  readAt: string
}

export type NetworkEgressIdentity = {
  family: "inet" | "inet6"
  forwarding: boolean
  forwardingError?: string
  device?: string
  gateway?: string
  /** The address the kernel sends from: the NIC source. */
  source?: string
  sourceScope: "public" | "private" | "shared" | "unique-local" | "link-local" | "loopback" | "none"
  /** What is known about the provider-facing identity. */
  public: "nic" | "translated" | "unobserved" | "no_route" | "unknown"
  detail: string
  error?: string
}

export type NetworkObservation = {
  source: string
  label: string
  state: "ok" | "failed" | "unavailable"
  error?: string
  href: string
}

export type NetworkFlowEdge = {
  /** Topology node ids: "internet", "host", "link:<device>" or "docker:<network id>". */
  from: string
  to: string
  flows: number
  bytes?: number
  protocols: string[]
  translation: "none" | "masquerade" | "forward"
  via?: string
  path: string[]
}

export type NetworkTopologyFlows = {
  state: "ok" | "unavailable" | "failed"
  error?: string
  readAt: string
  total: number
  classified: number
  truncated: boolean
  accounting: boolean
  edges: NetworkFlowEdge[]
}

export type NetworkNamespace = {
  name: string
  kind: "named" | "container"
  id?: number
  managed: boolean
  image?: string
  pid?: number
  devices: { name: string; state: string; mtu: number; mac?: string; addresses: string[] }[]
  /** Why its devices could not be read; they are then unknown, not empty. */
  readError?: string
}

export type NetworkRouteOwner =
  "kernel" | "dhcp" | "tailscale" | "docker" | "wireguard" | "just-dashboard" | "system"

export type NetworkRoute = {
  /** Zero unless the dashboard made it. */
  id: number
  family: "inet" | "inet6"
  /** "default" for either family's default route. */
  destination: string
  type: string
  gateway?: string
  device?: string
  protocol: string
  scope?: string
  metric: number
  source?: string
  flags: string[]
  nexthops: { gateway?: string; device?: string; weight?: number }[]
  owner: NetworkRouteOwner
  managed: boolean
  guard?: string
  /** The note a managed route was saved with. */
  comment?: string
}

export type NetworkNexthop = { gateway?: string; device?: string; weight?: number }

/** A route as the dashboard saved it, which an edit plan compares. */
export type NetworkRouteSpec = {
  id: number
  family: "inet" | "inet6"
  destination: string
  type: string
  gateway?: string
  device?: string
  table: number
  metric?: number
  source?: string
  nexthops?: NetworkNexthop[]
  comment?: string
}

export type NetworkRule = {
  id: number
  family: "inet" | "inet6"
  priority: number
  from?: string
  to?: string
  iif?: string
  oif?: string
  fwmark?: string
  uidRange?: string
  tos?: string
  ipProto?: string
  sport?: string
  dport?: string
  /** Inverts every selector. */
  not?: boolean
  /** Looks the packet up in its VRF device's table. */
  l3mdev?: boolean
  suppressPrefixLength?: number
  action: "lookup" | "blackhole" | "unreachable" | "prohibit" | "goto" | "nop"
  table?: number
  tableName?: string
  goto?: number
  /** A goto whose target priority holds no rule. */
  unresolved?: boolean
  owner: "system" | "tailscale" | "just-dashboard"
  managed: boolean
  guard?: string
}

/** What one rule did with a modeled packet. */
export type NetworkDecisionStep = {
  priority: number
  result:
    | "no_match"
    | "unknown"
    | "undecided"
    | "no_route"
    | "suppressed"
    | "throw"
    | "jumped"
    | "passed"
    | "decided"
  detail?: string
}

/** The table and rule behind the browser's reply path. */
export type NetworkClientDecision = {
  basis: "kernel_and_model" | "kernel" | "disagree"
  table: number
  tableName?: string
  device?: string
  gateway?: string
  rulePriority?: number
  candidates: number[]
  reason?: string
  steps: NetworkDecisionStep[]
}

export type NetworkRouteDecision = {
  status: "route" | "local" | "discard" | "unreachable" | "unknown"
  rulePriority?: number
  table?: number
  tableName?: string
  route?: NetworkRoute
  reason?: string
  steps: NetworkDecisionStep[]
  alternatives?: { assumption: string; decision: NetworkRouteDecision }[]
}

export type NetworkImpactItem = {
  kind: string
  name: string
  address: string
  before: string
  after: string
  reason?: string
}

/** What a route or rule would do to the traffic this host is known to send. */
export type NetworkRouteImpact = {
  family: "inet" | "inet6"
  table: number
  checkedAt: string
  affected: NetworkImpactItem[]
  unknown: NetworkImpactItem[]
  unaffected: number
  clientAffected: boolean
  limits: string[]
}

export type NetworkRoutePlan = {
  before: NetworkRouteSpec
  after: NetworkRouteSpec
  replace: boolean
  commands: string[]
  changes: string[]
  impact: NetworkRouteImpact
}

export type NetworkRuleRelation = { priority: number; owner: string; reason: string }

export type NetworkRulePreview = {
  rule: { priority: number; family: "inet" | "inet6" }
  checkedAt: string
  after?: NetworkRule
  before?: NetworkRule
  shadowedBy: NetworkRuleRelation[]
  shadows: NetworkRuleRelation[]
  impact: NetworkRouteImpact
  probe?: {
    tuple: Record<string, unknown>
    kernel?: NetworkPath & { family: string; table?: number }
    kernelError?: string
    now: NetworkRouteDecision
    with: NetworkRouteDecision
    agreement: "agrees" | "disagrees" | "model_unknown" | "kernel_unavailable"
    changes: boolean
  }
  refusal?: string
}

export type NetworkRouteEvent = {
  id: number
  observedAt: string
  previousAt: string
  acrossRestart?: boolean
  object: "route" | "rule"
  change: "added" | "removed" | "changed"
  family: "inet" | "inet6"
  table?: number
  tableName?: string
  destination?: string
  owner: string
  managed: boolean
  before?: string
  after?: string
}

export type NetworkRouteHistory = {
  intervalSeconds: number
  running: boolean
  since?: string
  lastReading?: string
  lastError?: string
  events: NetworkRouteEvent[]
  limits: string[]
}

export type ForwardingFamily = {
  available: boolean
  enabled: boolean
  /** The dashboard's boot file sets it. */
  persisted: boolean
  /** What stops working if it is turned off. */
  neededBy: string[]
  guard?: string
  /** How the Docker dependency was counted for this family. */
  dockerBasis?: string
  health?: {
    status: "off" | "measuring" | "forwarding" | "idle" | "partial" | "unknown"
    forwarded?: number
    ratePerSecond?: number
    windowSeconds?: number
    disabled: string[]
    checkedAt: string
    reason?: string
  }
}

export type NetworkForwarding = { ipv4: ForwardingFamily; ipv6: ForwardingFamily }

export type NetworkRouting = {
  /** Main first, then by id. */
  tables: { id: number; name: string; routes: NetworkRoute[] }[]
  /** By priority: the order the kernel asks them in. */
  rules: NetworkRule[]
  clientPath: NetworkPath
  /** The local table's entries, which are the kernel's bookkeeping and not drawn. */
  hiddenLocal: number
  rulePriorities: { min: number; max: number }
  forwarding: NetworkForwarding
  clientDecision?: NetworkClientDecision
  vrfs?: { name: string; table: number }[]
}

export type BGPPolicy = {
  routeMapIn?: string
  routeMapOut?: string
  prefixListIn?: string
  prefixListOut?: string
  filterListIn?: string
  filterListOut?: string
}

export type OSPFNeighbor = {
  version: 2 | 3
  routerId: string
  address?: string
  interface?: string
  state: string
  role?: string
  priority: number
  deadSeconds?: number
  uptimeSeconds?: number
}

export type BGPRoutesView = {
  family: string
  prefix?: string
  routerId?: string
  localAs?: number
  total: number
  truncated: boolean
  routes: {
    prefix: string
    best: boolean
    valid: boolean
    multipath: boolean
    nexthops: string[]
    peer?: string
    path: string
    origin?: string
    localPref?: number
    med?: number
    weight: number
    from?: string
  }[]
  error?: string
}

export type BGPView = {
  installed: boolean
  running: boolean
  error?: string
  ospf?: OSPFNeighbor[]
  readOnly?: string
  families: {
    name: string
    routerId: string
    localAs: number
    peers: {
      address: string
      hostname?: string
      remoteAs: number
      state: string
      uptime?: string
      uptimeSeconds: number
      prefixesReceived: number
      prefixesSent: number
      messagesReceived: number
      messagesSent: number
      connectionsDropped: number
      description?: string
      policy?: BGPPolicy
    }[]
  }[]
}

export type VPNSummary = {
  wireguard: {
    installed: boolean
    interfaces: number
    peers: number
    online: number
    rx: number
    tx: number
  }
  tailscale: {
    installed: boolean
    running: boolean
    peers: number
    online: number
    exitNode: boolean
    subnetRoutes: number
    selfIps: string[]
  }
}

export type WGPeer = {
  /** Zero for a peer the dashboard did not make, which is read-only. */
  id: number
  name: string
  kind: "device" | "site" | "peer"
  publicKey: string
  address: string
  address6?: string
  allowedIps: string[]
  endpoint: string
  /** Unix seconds; zero is never. */
  latestHandshake: number
  /** A handshake within the last three minutes. */
  online: boolean
  rxBytes: number
  txBytes: number
  keepalive: number
  /**
   * online; stale when a peer that keeps its session alive has gone quiet;
   * idle when one that does not is quiet (a phone with its tunnel off); never.
   */
  handshakeState?: "online" | "stale" | "idle" | "never"
  /** The client's configuration is kept, sealed, and can be shown again. */
  hasConfig: boolean
  /** A device's own AllowedIPs as last generated here; absent when unknown. */
  clientRoutes?: string[]
  createdAt?: string
  transport?: WGTransport
  quota?: WGQuota
}

/** Where a peer's encrypted packets are routed, as last checked. */
export type WGTransport = {
  endpoint: string
  state: "native" | "captured" | "unroutable" | "unknown"
  device?: string
  reason?: string
  /** Unix seconds. */
  checkedAt: number
}

/** A peer's usage budget: an alert threshold, never enforced. */
export type WGQuota = {
  period: "day" | "week" | "month"
  limitBytes: number
  usedBytes: number
  /** Unix seconds. */
  periodStart: number
  state: "ok" | "warning" | "exceeded" | "unmeasured"
  enforced: boolean
  createdBy?: string
}

export type WGAlert = {
  kind: "stale_handshake" | "transport_captured" | "quota_warning" | "quota_exceeded"
  peer?: string
  peerName?: string
  /** Unix seconds. */
  since?: number
  message: string
}

export type WGEvent = {
  id: number
  iface: string
  /** Unix seconds. */
  at: number
  kind: string
  outcome: "ok" | "failed" | "degraded" | "recovered"
  peer?: string
  peerName?: string
  actor?: string
  detail?: string
}

export type WGHistory = {
  iface: string
  peer?: string
  recording: boolean
  window: number
  bucket: number
  points: { t: number; rx: number; tx: number }[]
  endpoints: { endpoint: string; firstSeen: number; lastSeen: number; observations: number }[]
  events: WGEvent[]
  usage: { day: number; rx: number; tx: number }[]
}

export type WGEndpointEvidence = {
  endpoint: string
  host: string
  port: number
  kind: "address" | "hostname"
  resolution: "literal" | "resolved" | "failed"
  resolutionError?: string
  addresses: { address: string; public: boolean; onHost: boolean; device?: string }[]
  uplink?: string
  uplinkPublic: boolean
  listening: boolean
  verdict: "on_host_public" | "provider_mapped" | "elsewhere" | "private" | "unresolved"
  explanation: string
  reachability: "not_tested"
  checkedAt: number
}

export type WGArchived = {
  file: string
  name: string
  /** Unix seconds. */
  archivedAt: number
  listenPort: number
  addresses: string[]
  endpoint: string
  peers: number
  restorable: boolean
  refusal?: string
}

export type WGSiteVerification = {
  peer: string
  peerName: string
  at: number
  outcome: "verified" | "partial" | "failed"
  checks: { name: string; status: "pass" | "fail" | "warn" | "skipped"; detail: string }[]
  remoteSteps: string[]
}

export type WGPeerEdited = {
  peer: WGPeer
  clientChanged: boolean
  config?: string
  qr?: string
  reloaded: boolean
  warnings: string[]
}

export type WGInterface = {
  name: string
  /** The dashboard wrote this tunnel's file; a hand-written one is read-only. */
  managed: boolean
  configured: boolean
  up: boolean
  enabled: boolean
  active: boolean
  publicKey: string
  listenPort: number
  addresses: string[]
  mtu: number
  dns: string[]
  endpoint: string
  exitNode: boolean
  subnet: string
  ipv6Enabled?: boolean
  families?: {
    ipv4: WGFamilyState
    ipv6: WGFamilyState
  }
  endpointReachability?: "not_tested"
  peers: WGPeer[]
  alerts?: WGAlert[]
}

export type WGFamilyState = {
  configured: boolean
  subnet?: string
  runtime: "not_configured" | "present" | "down" | "missing" | "unreadable"
  reason?: string
  exit: {
    configured: boolean
    interface?: string
    runtime: "disabled" | "verified" | "degraded" | "unknown"
    reason?: string
    capability: GatewayCapability
    /** Client connections the owned masquerade rule has translated since it loaded. */
    translated?: number
  }
}

export type WireGuardView = {
  installed: boolean
  tools: { wg: boolean; wgQuick: boolean }
  package: string
  kernel: boolean
  systemd: boolean
  interfaces: WGInterface[]
  error?: string
}

export type TailscalePeer = {
  id: string
  hostName: string
  dnsName: string
  os: string
  tailscaleIps: string[]
  online: boolean
  active: boolean
  exitNode: boolean
  exitNodeOption: boolean
  relay: string
  direct: boolean
  curAddr: string
  rxBytes: number
  txBytes: number
  lastSeen?: string
  lastHandshake?: string
  primaryRoutes?: string[]
  tags?: string[]
  userLoginName?: string
  expired?: boolean
}

export type TailscaleView = {
  prefsReadable?: boolean
  installed: boolean
  running: boolean
  backendState: string
  version: string
  authUrl?: string
  self: {
    hostName: string
    dnsName: string
    tailscaleIps: string[]
    os: string
    online: boolean
    exitNodeOption: boolean
    primaryRoutes?: string[]
    relay: string
    /** Unix seconds; absent when the node key does not expire. */
    keyExpiry?: number
  } | null
  magicDnsSuffix: string
  tailnet: string
  health: string[]
  peers: TailscalePeer[]
  prefs: {
    advertiseRoutes: string[]
    advertisingExitNode: boolean
    usingExitNode: boolean
    exitNodeId?: string
    routeAll: boolean
    corpDns: boolean
    shieldsUp: boolean
  }
  controlServer: "tailscale" | "self-hosted"
  controlUrl: string
  /** What the tailnet grants of what this server advertises, from its own status. */
  approval?: {
    exitNode?: "serving" | "not_serving"
    routes: { route: string; state: "serving" | "not_serving" }[]
  }
  clientOnTailnet: boolean
  forwarding: { ipv4: boolean; ipv6: boolean }
  warnings: string[]
  error?: string
}

export type HeadscaleView = {
  installed: boolean
  container?: string
  nodes: {
    id: string
    name: string
    givenName: string
    ipAddresses: string[]
    online: boolean
    /** Unix seconds; zero for never. */
    lastSeen?: number
    user: string
    forcedTags?: string[]
    validTags?: string[]
    /** Unix seconds; zero when the key does not expire. */
    expiry?: number
    availableRoutes?: string[]
    approvedRoutes?: string[]
    /** False when this Headscale reported no routes, so empty lists say nothing. */
    routesKnown?: boolean
  }[]
  users: { id: string; name: string; nodes: number }[]
  error?: string
}

export type VPNView = {
  wireguard: WireGuardView
  tailscale: TailscaleView
  headscale: HeadscaleView
}

export type WGPeerCreated = {
  peer: WGPeer
  config: string
  /** A PNG data URL. */
  qr: string
  reloaded: boolean
  warnings: string[]
}

export type DNSScope = {
  protocols: string[]
  dnsOverTLS: "yes" | "opportunistic" | "no"
  dnssec: string
  dnssecSupported: boolean
  currentServer?: string
  servers: string[]
  fallbackServers: string[]
  domains: string[]
}

export type DNSPreset = {
  id: string
  name: string
  product: string
  servers: string[]
  tlsName: string
  tlsServers: string[]
  blocksAds: boolean
  blocksMalware: boolean
}

/** Who decides /etc/resolv.conf, and where a DNS change is made on this host. */
export type ResolverOwner = {
  id:
    | "systemd-resolved"
    | "networkmanager"
    | "resolvconf"
    | "openresolv"
    | "netconfig"
    | "dhcpcd"
    | "dhclient"
    | "tailscale"
    | "wsl"
    | "local-cache"
    | "static"
    | "missing"
    | "unknown"
  name: string
  chain: "stub" | "uplink" | "local-cache" | "direct" | "missing"
  resolver: string
  confidence: "confirmed" | "declared" | "inferred" | "unknown"
  dashboardWrites: boolean
  handoff: string
  handoffHref?: string
  evidence: { source: string; detail: string }[]
  conflicts: string[]
}

export type DNSView = {
  owner: ResolverOwner
  resolvConf: {
    path: string
    mode: "stub" | "uplink" | "static" | "link" | "missing"
    target?: string
    managedBy?: string
    nameservers: string[]
    search: string[]
    options: string[]
  }
  resolved: {
    installed: boolean
    active: boolean
    global: DNSScope & { resolvConfMode?: string }
    links: (DNSScope & { name: string; index: number; scopes: string[]; defaultRoute: boolean })[]
    linksTotal: number
    statistics?: {
      cacheSize: number
      cacheHits: number
      cacheMisses: number
      hitPercent: number
      transactions: number
      currentTransactions: number
      timeouts: number
      failures: number
      dnssecSecure: number
      dnssecInsecure: number
      dnssecBogus: number
      dnssecIndeterminate: number
    }
    statisticsError?: string
    error?: string
  }
  listeners: {
    address: string
    protocol: string
    process?: string
    pid?: number
    container?: string
    loopback: boolean
    kind:
      | "resolved-stub"
      | "dnsmasq"
      | "unbound"
      | "named"
      | "adguardhome"
      | "pihole"
      | "docker-proxy"
      | "other"
  }[]
  adblock: {
    kind: "adguardhome" | "pihole" | "technitium"
    name: string
    runsAs: "container" | "process"
    container?: string
    containerId?: string
    image?: string
    answering: boolean
    webPort?: number
    webAddress?: string
  }[]
  managed: {
    path: string
    exists: boolean
    servers: string[]
    fallback: string[]
    domains: string[]
    dnssec: string
    dnsOverTLS: string
    cache: string
    /** Lists the drop-in empties on purpose rather than leaving to the host. */
    cleared?: DNSClearableList[]
  }
  presets: DNSPreset[]
}

export type DNSClearableList = "servers" | "fallback" | "domains"

export type DNSVerificationCheck = {
  kind: "readback" | "resolution" | "transport" | "dnssec"
  scope: string
  name?: string
  required: boolean
  state: "planned" | "passed" | "failed" | "warning" | "unknown" | "skipped"
  answer?: string
  type?: string
  millis?: number
  detail?: string
}

/** The checks a resolver change is held to, and the scopes none of them reaches. */
export type DNSVerification = {
  checks: DNSVerificationCheck[]
  unverified: string[]
}

export type DNSTLSCheck = {
  server: string
  scope: string
  address: string
  tlsName: string
  state: "trusted" | "untrusted" | "no-answer" | "unreachable"
  version?: string
  subject?: string
  issuer?: string
  names?: string[]
  notAfter?: string
  fingerprint?: string
  chainLength?: number
  latencyMs: number
  error?: string
}

export type DNSTLSReport = {
  checkedAt: string
  checks: DNSTLSCheck[]
  omitted: { server: string; label: string; reason?: string }[]
  limitations: string[]
}

export type DNSSECSet = {
  state:
    | "authenticated"
    | "unauthenticated"
    | "absent"
    | "not_found"
    | "validation_failed"
    | "error"
    | "not_asked"
  records: number
  detail?: string
}

export type DNSSECLevel = {
  zone: string
  role: "apex" | "not_apex" | "not_queried" | "unavailable"
  scope?: string
  dnskey: DNSSECSet
  ds: DNSSECSet
  keys: { keyTag: number; algorithm: number; flags: number; sep: boolean }[]
  links: {
    keyTag: number
    algorithm: number
    digestType: number
    digest: string
    matched: boolean
    matchedKey?: number
    supported: boolean
  }[]
  link:
    | "digest_match"
    | "digest_mismatch"
    | "no_ds"
    | "root_anchor"
    | "root_anchor_mismatch"
    | "not_checked"
  detail: string
}

export type DNSSECChain = {
  name: string
  startedAt: string
  endedAt: string
  owner: string
  ownerVersion?: string
  policyMatch?: string
  policyStable: boolean
  levels: DNSSECLevel[]
  verdict: "secure" | "anchored" | "insecure" | "broken" | "unknown"
  summary: string
  questions: number
  error?: string
  limitations: string[]
}

export type HostRecordIssue = {
  kind: "duplicate" | "conflict" | "shadowed" | "overrides" | "repeated"
  name: string
  address: string
  record: number
  other?: string
  line?: number
  detail: string
}

export type HostRecordsPreview = {
  records: { address: string; names: string[] }[]
  block: string[]
  added: { address: string; names: string[] }[]
  removed: { address: string; names: string[] }[]
  issues: HostRecordIssue[]
}

export type HostNameResolution = {
  name: string
  configured: string[]
  ipv4: string[]
  ipv6: string[]
  state: "matches" | "includes" | "differs" | "unresolved" | "unknown"
  detail: string
}

export type HostResolutionEvidence = {
  checkedAt: string
  hosts: string
  names: HostNameResolution[]
  omitted: number
  limitations: string[]
}

export type DNSSummary = {
  resolver: string
  mode: string
  upstreams: string[]
  dnsOverTLS: string
  dnssec: string
  adblock: boolean
  custom: boolean
}

export type DNSApplied = {
  verified: boolean
  via?: string
  millis?: number
  warning?: string
  managed: DNSView["managed"] | null
  verification?: DNSVerification
}

export type HostRecords = {
  path: string
  managed: { address: string; names: string[] }[]
  other: { address: string; names: string[]; line: number }[]
  problem?: string
}

export type DNSLookup = {
  name: string
  type: string
  mode: "effective" | "compare"
  route: string
  note: string
  comparisonTargets: { server: string; label: string; tlsName?: string; reason?: string }[]
  omittedTargets: { server: string; label: string; tlsName?: string; reason?: string }[]
  results: {
    server: string
    label: string
    answers: string[]
    latencyMs: number
    error?: string
    transport?: "udp" | "tcp" | "tls"
    tlsName?: string
    tlsVersion?: string
    authenticatedData?: boolean
    signatures?: number
  }[]
}

export type ProcessTraffic = {
  at: string
  intervalSeconds: number
  warming: boolean
  truncated: boolean
  note: string
  /** TCP connections opened since the last read that no read saw; absent is unknown. */
  missedOpens?: number
  closed?: number
  udpError?: string
  history?: RecordedHistory
  programs: {
    name: string
    pids: number[]
    connections: number
    rxRate: number
    txRate: number
    rxTotal: number
    txTotal: number
    udpConnected?: number
    udpUnconnected?: number
    medianRttMs?: number
    retransmitted?: number
    segmentsOut?: number
    peers: {
      address: string
      port: number
      protocol?: "tcp" | "udp"
      bytesKnown?: boolean
      connections: number
      rxBytes: number
      txBytes: number
    }[]
  }[]
}

export type ContainerTraffic = {
  windowSeconds: number
  recording: boolean
  containers: {
    name: string
    rxBytes: number
    txBytes: number
    rxRate: number
    txRate: number
    lastSeen: string
    series: { t: number; rx: number; tx: number }[]
  }[]
}

export type EBPFView = {
  installed: boolean
  package?: string
  bpfStatsEnabled: boolean
  total: number
  programs: {
    id: number
    type: string
    name?: string
    tag?: string
    loadedAt?: string
    uid: number
    bytesXlated: number
    bytesJited: number
    memlock: number
    mapIds: number[]
    runTimeNs?: number
    runCount?: number
    pinned?: string[]
    owners?: string[]
  }[]
  byType: { type: string; count: number }[]
  attachments: {
    device: string
    ifindex: number
    kind: "xdp" | "tc ingress" | "tc egress"
    programId: number
    name?: string
    mode?: string
  }[]
  error?: string
  platform?: EBPFPlatform
  cgroupAttachments?: number
  /** The programs this dashboard's own kernel observer holds while attached. */
  observerProgramIds?: number[]
}

type NetworkMade = { createdAt: string; createdBy?: string }

/** An entry's count across gateway table replacements. */
export type CounterTotal = {
  packets: number
  bytes: number
  /** When the recorder first saw the rule; earlier traffic is not in it. */
  since: string | null
  resets: number
}

/** Labels the live figures and the totals a gateway or protection read shows. */
export type CounterEvidence = {
  /** The loaded table's kernel handle; 0 when not loaded. */
  generation: number
  observedAt: string | null
  persistent: boolean
  gap: string
}

/** What is installed for an entry, apart from whether a visitor reaches it. */
export type EntryReadiness = {
  policy: "installed" | "partial" | "missing" | "drift" | "not_loaded" | "disabled"
  rules: number
  expected: number
  forwarding: boolean
  admission: "present" | "absent" | "unreadable" | "unsupported" | "not_required"
  ready: boolean
  /** Only an external measurement after the last change moves it off unverified. */
  reachability: "verified" | "failed" | "unverified"
  reason: string
}

/** Auto source translation re-checked against the host now. */
export type NATDecision = {
  stored: boolean
  current: boolean | null
  drift: boolean
  reason: string
  error?: string
}

/** The last check of a forward's target from this server. */
export type ForwardCheck = {
  status: "answering" | "refused" | "timeout" | "unreachable" | "error" | "not_measurable"
  detail: string
  target: string
  protocol: string
  checkedAt: string
  basis: string
  current: boolean
}

/** The external measurement that speaks for a forward's public port. */
export type ExternalEvidence = {
  status: "connected" | "failed"
  checkId: string
  vantage: string
  location: string
  placement: string
  address: string
  port: number
  family: string
  checkedAt: string
  detail: string
  current: boolean
  basis: string
}

/** One foreign chain's modeled verdict for one entry's flow. */
export type FlowLayer = {
  family: string
  table: string
  chain: string
  hook: string
  verdict: "clear" | "restricted" | "blocked" | "unknown"
  rule: number
  path?: string
  reason: string
}

export type EntryFlow = {
  entry: string
  name: string
  direction: "inbound" | "outbound"
  verdict: "clear" | "blocked" | "unknown"
  layers: FlowLayer[]
}

export type GatewayCapability = {
  writable: boolean
  reason?: string
  firewall: "ufw" | "firewalld" | "iptables" | "nftables" | "none"
  docker: boolean
  /** Compatibility is separate from a measured source-to-destination path. */
  reachability?: "unknown"
  layers?: {
    family: string
    table: string
    chain: string
    hook: string
    policy: string
    status: "owned" | "admitted" | "checked" | "blocked" | "unknown"
    reason?: string
    type?: string
    rules?: number
    /** The rule forms the generic check could not read, by position. */
    uncertain?: string[]
  }[]
  unknownLayers?: string[]
  /** The drop-forward chain that makes the gateway read-only, and the accept to add there. */
  blocker?: { family: string; table: string; chain: string; rule: string }
}

export type GatewayForward = NetworkMade & {
  id: number
  name: string
  protocol: "tcp" | "udp" | "both"
  /** "" is any device. */
  interface: string
  ports: string
  target: string
  /** "" keeps the port it arrived on. */
  targetPort: string
  /** Empty is anyone. */
  sources: string[]
  sourceNat: "auto" | "always" | "never"
  /** What `auto` resolved to: whether the visitor's address is replaced. */
  masquerade: boolean
  enabled: boolean
  changedAt?: string | null
  packets: number
  bytes: number
  total?: CounterTotal
  readiness?: EntryReadiness
  decision?: NATDecision
  check?: ForwardCheck
  external?: ExternalEvidence
}

export type GatewayNAT = NetworkMade & {
  id: number
  name: string
  source: string
  interface: string
  /** "" masquerades behind the interface's own address. */
  toAddress: string
  mode?: "masquerade" | "snat" | "one-to-one" | "nptv6"
  /** A mapping's public side. */
  translated?: string
  /** Empty translates traffic to every destination. */
  destinations?: string[]
  /** What made it when not a person: "wireguard:wg0". */
  owner: string
  enabled: boolean
  packets: number
  bytes: number
  inPackets?: number
  inBytes?: number
  total?: CounterTotal
  inTotal?: CounterTotal
  readiness?: EntryReadiness
}

export type GatewayView = {
  capability: GatewayCapability
  loaded: boolean
  forwarding: { ipv4: boolean; ipv6: boolean }
  admission: {
    needed: boolean
    present: boolean
    checkedAt?: string
    chains?: {
      family: "inet" | "inet6"
      tool: string
      chain: string
      needed: boolean
      status: "present" | "absent" | "unsupported" | "unreadable"
      reason?: string
    }[]
  }
  forwards: GatewayForward[]
  nat: GatewayNAT[]
  counters?: CounterEvidence
  flows?: EntryFlow[]
}

/** One consequence of a proposed change. */
export type GatewayImpact = {
  severity: "refused" | "warning" | "info"
  kind: string
  message: string
  entry?: string
}

/** Tracked connections a change concerns. */
export type ConnectionImpact = {
  tracked: number
  sources: number
  sample: string[]
  truncated: boolean
  error?: string
}

export type GatewayPreview = {
  valid: boolean
  error?: string
  forward?: GatewayForward
  nat?: GatewayNAT
  impacts: GatewayImpact[]
  connections?: ConnectionImpact
  flows: EntryFlow[]
}

/** How many sources a limit's per-source sets track now. */
export type LimitMeters = {
  rateSources: number | null
  connSources: number | null
  capacity: number
  checkedAt: string
  error?: string
}

export type ProtectionLimit = NetworkMade & {
  id: number
  name: string
  protocol: "tcp" | "udp" | "both"
  ports: string
  rate: number
  per: "second" | "minute" | "hour"
  burst: number
  perSource: boolean
  maxConnections: number
  /** The ceiling for every source together; 0 is none. */
  globalConnections?: number
  profile?: string
  action: "drop" | "reject"
  enabled: boolean
  /** What it refused since the table was loaded. */
  packets: number
  bytes: number
  globalPackets?: number
  total?: CounterTotal
  meters?: LimitMeters
}

/** A starting point for a limit on a common service. */
export type LimitProfile = {
  id: string
  name: string
  why: string
  protocol: "tcp" | "udp" | "both"
  ports: string
  rate: number
  per: "second" | "minute" | "hour"
  burst: number
  perSource: boolean
  maxConnections: number
  globalConnections: number
  action: "drop" | "reject"
}

/** Where one URL of a fetched list came from. */
export type BlocklistSource = {
  url: string
  country?: string
  family?: string
  status: "ok" | "absent" | "unchanged"
  fetchedAt: string
  bytes: number
  sha256?: string
  networks: number
  skipped: number
  etag?: string
  lastModified?: string
  signed?: boolean
}

export type BlocklistDiff = {
  at: string
  added: number
  removed: number
  addedSample?: string[]
  removedSample?: string[]
  baseline: boolean
}

export type BlocklistCoverage = {
  ipv4Addresses: number
  ipv4Share: number
  ipv6Slash48s: number
  ipv4Networks: number
  ipv6Networks: number
}

export type ProtectionBlocklist = NetworkMade & {
  id: number
  name: string
  kind: "manual" | "country" | "feed"
  countries: string[]
  url: string
  entries: string[]
  enabled: boolean
  refreshed: string | null
  count: number
  savedCount?: number
  cache?: {
    status: "ready" | "missing" | "unreadable" | "invalid"
    count: number
    generation: string
    error?: string
  }
  renderedGeneration?: string
  runtime?: {
    status: "present" | "absent" | "unreadable" | "not_required"
    count: number | null
    generation?: string
    error?: string
  }
  enforcement?: "disabled" | "verified" | "degraded" | "unknown"
  error: string
  /** The list holds the reader's own address; the trusted set still lets them in. */
  containsYou: boolean
  packets: number
  bytes: number
  total?: CounterTotal
  /** A fetched list's schedule: 6h, 12h, 24h, 72h, 168h or manual. */
  refresh?: string
  nextRefresh?: string | null
  stale?: boolean
  lastAttempt?: string | null
  failures?: number
  sources?: BlocklistSource[]
  lastDiff?: BlocklistDiff | null
  integrity?: "signed" | "https" | "local"
  signatureUrl?: string
  coverage?: BlocklistCoverage
}

/** A network let past the drops for a scope, a reason and maybe until a time. */
export type ProtectionException = NetworkMade & {
  id: number
  address: string
  /** "all" or "blocklist:<id>". */
  scope: string
  scopeName: string
  reason: string
  expiresAt: string | null
  expired: boolean
  packets: number
  bytes: number
}

export type ProtectionSetting = {
  key: string
  label: string
  why: string
  kind: "choice" | "number"
  allowed: string[]
  min: number
  max: number
  current: string
  available: boolean
  /** The dashboard keeps a value for boot. */
  setHere: boolean
  value: string
  atRecommended: boolean
}

/** A set of kernel values for a kind of host, staged like hand-edited ones. */
export type KernelProfile = {
  id: string
  name: string
  why: string
  values: Record<string, string>
}

/** The per-interface protections, as each device actually runs them. */
export type InterfaceReading = {
  interfaces: {
    name: string
    forwarding: boolean
    values: { key: string; own: string; all: string; effective: string; differs: boolean }[]
  }[]
  rules: { key: string; combine: string; explain: string }[]
  omitted: number
}

export type Conntrack = {
  available: boolean
  count: number
  max: number
  percent: number
  level: "ok" | "warning" | "critical"
}

export type ProtectionTrusted = {
  address: string
  origin: "loopback" | "allowlist" | "you" | "kept"
  removable: boolean
  reason?: string
  addedBy?: string
  addedAt?: string | null
  expiresAt?: string | null
  expired?: boolean
  confirmedAt?: string | null
  confirmedBy?: string
  lastSeen?: string | null
  stale?: boolean
}

export type ProtectionView = {
  loaded: boolean
  limits: ProtectionLimit[]
  blocklists: ProtectionBlocklist[]
  presets: { id: string; name: string; url: string; description: string }[]
  trusted: ProtectionTrusted[]
  client: string
  clientTrusted: boolean
  settings: ProtectionSetting[]
  resetNote: string
  conntrack: Conntrack
  exceptions?: ProtectionException[]
  profiles?: LimitProfile[]
  kernelProfiles?: KernelProfile[]
  interfaces?: InterfaceReading
  counters?: CounterEvidence
}

export type BlocklistPreview = {
  valid: boolean
  error?: string
  refused?: string
  networks: number
  coverage: BlocklistCoverage
  diff?: BlocklistDiff
  sources: BlocklistSource[]
  trustedOverlap: string[]
  localOverlap: { network: string; what: string }[]
  connections?: ConnectionImpact
  impacts: GatewayImpact[]
  geography?: { source: string; basis: string; limits: string[]; countries: string[] }
}

export type SessionPreview = {
  network: string
  blocklist: string
  connections: ConnectionImpact
  refused?: string
  basis: string
}

export type SessionResult = {
  network: string
  matched: number
  ended: number
  failed: number
  truncated: boolean
  error?: string
  basis: string
}

export type SeriesPoint = { t: number; value: number; bytes?: number }

export type PressureCount = { key: string; count: number; share: number }

export type ProtectionPressure = {
  conntrack: Conntrack
  stats: {
    cpus: number
    found: number
    invalid: number
    insert: number
    insertFailed: number
    drop: number
    earlyDrop: number
    error: number
    searchRestart: number
    clashResolve: number
    chainTooLong: number
  } | null
  statsError?: string
  breakdown: {
    read: number
    truncated: boolean
    byProtocol: PressureCount[]
    byState: PressureCount[]
    topSources: PressureCount[]
    topPorts: PressureCount[]
    unreplied: number
    assured: number
    error?: string
  }
  series: Record<string, SeriesPoint[]>
  since: string
  causes: { kind: string; severity: "info" | "warning"; message: string; evidence: string }[]
  limits: {
    id: number
    name: string
    ports: string
    maxConnections: number
    globalConnections: number
    open: number
    topSources: PressureCount[]
    refused: SeriesPoint[]
    meters?: LimitMeters
  }[]
  checkedAt: string
  basis: string
}

export type QdiscStat = {
  kind: string
  bytes: number
  packets: number
  drops: number
  overlimits: number
  requeues: number
  backlog: number
  /** CAKE's classes with the queueing delay it measured in each. */
  tins?: CakeTin[]
}

export type ShapeDevice = {
  name: string
  kind: string
  root: QdiscStat | null
  leaf: QdiscStat | null
  ingress: boolean
  managed: boolean
  qdisc: string
  egressKbit: number
  ingressKbit: number
  uplink: boolean
  clientPath: boolean
  shapeable: boolean
  guard: string
  verification?: { status: "verified" | "drift" | "unknown"; checkedAt: string; reason?: string }
  sqm?: SQMProfile & {
    ifb: string
    queue: QdiscStat | null
    helper: NonNullable<ShapeDevice["verification"]>
    boot: NonNullable<ShapeDevice["verification"]>
  }
  upload?: UploadProfile
  tree?: QdiscNode[]
  ownership?: ShapeOwnership
  effective?: Record<string, string>
  applied?: AppliedQueue
  offload?: Offload
}

export type SQMProfile = {
  diffserv: "besteffort" | "diffserv3" | "diffserv4"
  flowMode: "dual-dsthost" | "triple-isolate" | "flows"
  nat: boolean
  preserveDscp: boolean
  overhead: number
  mpu: number
  linkLayer: "noatm" | "atm" | "ptm"
  rttMillis: number
}

export type BBRState = {
  available: boolean
  active: boolean
  congestion: string
  algorithms: string[]
  defaultQdisc: string
  managed: boolean
}

export type ShapingView = { devices: ShapeDevice[]; bbr: BBRState; qdiscs: string[] }

export type NetworkConfirmationView = {
  available: boolean
  owned: boolean
  change: NetworkChangeStatus | null
}
