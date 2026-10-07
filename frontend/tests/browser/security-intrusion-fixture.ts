import type { Page, Route } from "@playwright/test"
import { mockHostLogs } from "./host-logs-fixture"

/**
 * What Intrusion and SSH read, mocked from the shapes the backend writes: a
 * host with fail2ban, CrowdSec and Suricata all running, and an sshd whose
 * forwarding directives were left in the states a loosened server has them in
 * (so the jump-host preset has four values to stage).
 *
 * Addresses are documentation ranges (RFC 5737, RFC 3849) and the autonomous
 * systems are the documentation block (RFC 5398); the countries are only the
 * flags a table is drawn with. Nothing here is a real host or a real attacker.
 */

export type Mutation = { method: string; path: string; body: unknown }

const now = new Date()
export const iso = (minutesAgo: number) =>
  new Date(now.getTime() - minutesAgo * 60_000).toISOString()

/** The remaining time of a decision, in CrowdSec's own `3h59m12s` spelling. */
const left = (hours: number, minutes = 0) => `${hours}h${minutes}m0s`
const until = (hours: number, minutes = 0) => iso(-(hours * 60 + minutes))

const admin = {
  authenticated: true,
  needsTotp: false,
  needsEnrollment: false,
  require2fa: false,
  capabilities: [
    "read",
    "service.control",
    "file.write",
    "terminal",
    "destructive",
    "system.admin",
  ],
  user: {
    id: 1,
    username: "operator",
    role: "admin",
    totpEnabled: true,
    disabled: false,
    mustChangePassword: false,
    lastLoginAt: iso(60),
    createdAt: iso(60 * 24 * 30),
  },
}

/** An account that may read the section and change nothing in it. */
export const viewer = {
  ...admin,
  capabilities: ["read"],
  user: { ...admin.user, username: "viewer", role: "readonly" },
}

const exposure = {
  grade: "tailscale",
  summary: "Reachable only from your tailnet. Nothing is exposed to the internet.",
  allowlist: ["100.64.0.0/10"],
  interfaces: ["tailscale0"],
  tailscaleIp: "100.110.34.31",
  client: "100.110.34.9",
}

const posture = {
  status: "warning",
  checkedAt: iso(2),
  checks: 7,
  skipped: [],
  findings: [
    {
      id: "ssh.password-auth",
      level: "warning",
      area: "ssh",
      title: "SSH accepts passwords",
      detail: "PasswordAuthentication is yes, and 1 account(s) already have an authorized key.",
      advice: "Turn it off and use keys.",
      fix: "ssh.passwordauthentication=no",
      fixLabel: "Turn off passwords",
    },
  ],
}

const firewall = {
  backend: "ufw",
  available: true,
  enabled: true,
  defaultPolicy: "deny (incoming), allow (outgoing), disabled (routed)",
  policy: { incoming: "deny", outgoing: "allow", routed: "disabled" },
  logging: "low",
  capabilities: {
    editable: true,
    toggle: true,
    defaultPolicy: true,
    logging: true,
    reset: true,
    profiles: true,
  },
  rules: [],
}

const jails = {
  available: true,
  running: true,
  jails: [
    {
      name: "sshd",
      currentlyFailed: 3,
      totalFailed: 812,
      currentlyBanned: 2,
      totalBanned: 41,
      bannedIps: ["203.0.113.9", "198.51.100.4"],
      fileList: ["/var/log/auth.log"],
    },
  ],
}

const offenders = {
  total: 60,
  bans: 41,
  unbans: 19,
  offenders: [
    { ip: "203.0.113.9", bans: 11, jails: ["sshd"], first: iso(60 * 24 * 6), last: iso(30) },
  ],
  byJail: { sshd: 41 },
  perDay: [
    { day: "2026-10-04", count: 12 },
    { day: "2026-10-05", count: 20 },
    { day: "2026-10-06", count: 9 },
  ],
  since: iso(60 * 24 * 6),
}

type Decision = {
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
}

const decision = (
  id: number,
  origin: string,
  scenario: string,
  value: string,
  remaining: [number, number],
  extra: Partial<Decision> = {},
): Decision => ({
  id,
  origin,
  scenario,
  scope: value.includes("/") ? "Range" : "Ip",
  value,
  type: "ban",
  duration: left(...remaining),
  until: until(...remaining),
  alertId: id + 100,
  ...extra,
})

/** Eight decisions: three made here, five from the community list. */
export const crowdsec = {
  installed: true,
  active: true,
  decisions: [
    decision(11, "crowdsec", "crowdsecurity/ssh-bf", "203.0.113.9", [3, 12], {
      country: "RO",
      as: "AS64496 Example Hosting",
    }),
    decision(12, "crowdsec", "crowdsecurity/ssh-slow-bf", "198.51.100.4", [1, 40], {
      country: "DE",
      as: "AS64497 Example Transit",
    }),
    decision(13, "cscli", "manual 'ban' from 'operator'", "192.0.2.77", [22, 5]),
    decision(14, "CAPI", "crowdsecurity/http-probing", "203.0.113.141", [150, 20], {
      country: "CN",
      as: "AS64498 Example Telecom",
    }),
    decision(15, "CAPI", "crowdsecurity/ssh-bf", "198.51.100.200", [96, 3], {
      country: "RU",
      as: "AS64499 Example Broadband",
    }),
    decision(16, "CAPI", "crowdsecurity/http-crawl-non_statics", "192.0.2.130", [71, 48], {
      country: "US",
      as: "AS64500 Example Cloud",
    }),
    decision(17, "CAPI", "crowdsecurity/ssh-bf", "203.0.113.0/24", [120, 9], {
      country: "NL",
      as: "AS64501 Example Colo",
    }),
    decision(18, "CAPI", "crowdsecurity/http-bad-user-agent", "2001:db8:bad::1", [44, 30], {
      country: "BR",
      as: "AS64502 Example Net",
    }),
  ] satisfies Decision[],
  alerts: [
    alert(1, "crowdsecurity/ssh-bf", "203.0.113.9", "RO", "AS64496 Example Hosting", 6, 18),
    alert(2, "crowdsecurity/ssh-slow-bf", "198.51.100.4", "DE", "AS64497 Example Transit", 10, 95),
    alert(3, "crowdsecurity/http-probing", "192.0.2.201", "FR", "AS64503 Example ISP", 11, 140),
    alert(4, "crowdsecurity/ssh-bf", "203.0.113.55", "CN", "AS64498 Example Telecom", 6, 60 * 7),
    alert(
      5,
      "crowdsecurity/http-sensitive-files",
      "198.51.100.31",
      "US",
      "AS64500 Example Cloud",
      5,
      60 * 15,
    ),
    alert(6, "crowdsecurity/ssh-bf", "192.0.2.19", "VN", "AS64504 Example Mobile", 8, 60 * 40),
  ],
  bouncers: [
    {
      name: "firewall-bouncer-nftables",
      ipAddress: "127.0.0.1",
      valid: true,
      lastPull: iso(1),
      type: "crowdsec-firewall-bouncer",
      version: "v0.0.31",
    },
    {
      name: "caddy-bouncer",
      ipAddress: "172.18.0.4",
      valid: true,
      lastPull: iso(2),
      type: "caddy-cs-bouncer",
      version: "v0.9.1",
    },
  ],
}

function alert(
  id: number,
  scenario: string,
  ip: string,
  country: string,
  asName: string,
  eventsCount: number,
  minutesAgo: number,
) {
  return {
    id,
    scenario,
    source: { ip, scope: "Ip", value: ip, country, asName },
    eventsCount,
    createdAt: iso(minutesAgo),
    decisions: 1,
  }
}

type SuricataAlert = {
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
}

const SIGNATURES = {
  mssql: ["ET SCAN Suspicious inbound to MSSQL port 1433", 2010935, "Potentially Bad Traffic", 2],
  ssh: ["ET SCAN Potential SSH Scan", 2001219, "Attempted Information Leak", 2],
  log4j: [
    "ET EXPLOIT Apache Log4j RCE Attempt (http ldap) (CVE-2021-44228)",
    2034647,
    "Attempted Administrator Privilege Gain",
    1,
  ],
  shell: [
    "ET WEB_SERVER Possible CVE-2014-6271 Attempt in HTTP Header",
    2019232,
    "Attempted Administrator Privilege Gain",
    1,
  ],
  nmap: ["ET SCAN NMAP -sS window 1024", 2009582, "Attempted Information Leak", 3],
  telnet: ["GPL TELNET Bad Login", 2101251, "Potentially Bad Traffic", 3],
  ua: ["ET USER_AGENTS Suspicious User-Agent (zgrab)", 2025379, "Misc activity", 3],
} as const

function hit(
  minutesAgo: number,
  key: keyof typeof SIGNATURES,
  src: [string, number],
  dest: [string, number],
  proto: string,
  appProto?: string,
  action: "allowed" | "blocked" = "allowed",
): SuricataAlert {
  const [signature, signatureId, category, severity] = SIGNATURES[key]
  return {
    time: iso(minutesAgo),
    srcIp: src[0],
    srcPort: src[1],
    destIp: dest[0],
    destPort: dest[1],
    proto,
    appProto,
    signature,
    signatureId,
    category,
    severity,
    action,
  }
}

const HOST = "198.51.100.10"

/** Twenty alerts, newest first, across all three severities. */
export const suricata = {
  installed: true,
  active: true,
  version: "7.0.8",
  mode: "ids",
  modeSource: "/etc/default/suricata",
  alerts: [
    hit(2, "log4j", ["203.0.113.141", 51822], [HOST, 443], "TCP", "http"),
    hit(5, "mssql", ["192.0.2.201", 40112], [HOST, 1433], "TCP"),
    hit(9, "ssh", ["203.0.113.9", 36004], [HOST, 22], "TCP", "ssh"),
    hit(14, "nmap", ["198.51.100.31", 44721], [HOST, 80], "TCP"),
    hit(19, "ssh", ["203.0.113.9", 36118], [HOST, 22], "TCP", "ssh"),
    hit(26, "shell", ["192.0.2.19", 58210], [HOST, 8080], "TCP", "http"),
    hit(33, "telnet", ["198.51.100.200", 49310], [HOST, 23], "TCP"),
    hit(41, "mssql", ["192.0.2.201", 40890], [HOST, 1433], "TCP"),
    hit(47, "ua", ["203.0.113.55", 33310], [HOST, 443], "TCP", "http"),
    hit(58, "nmap", ["198.51.100.31", 44910], [HOST, 443], "TCP"),
    hit(72, "ssh", ["198.51.100.4", 52000], [HOST, 22], "TCP", "ssh"),
    hit(88, "log4j", ["203.0.113.141", 51990], [HOST, 443], "TCP", "http"),
    hit(97, "telnet", ["198.51.100.200", 49511], [HOST, 23], "TCP"),
    hit(120, "mssql", ["192.0.2.130", 40010], [HOST, 1433], "TCP"),
    hit(150, "ua", ["203.0.113.55", 33402], [HOST, 80], "TCP", "http"),
    hit(171, "nmap", ["198.51.100.31", 45002], [HOST, 22], "TCP"),
    hit(203, "ssh", ["203.0.113.9", 37200], [HOST, 22], "TCP", "ssh"),
    hit(260, "shell", ["192.0.2.19", 58400], [HOST, 8080], "TCP", "http"),
    hit(310, "mssql", ["192.0.2.130", 40222], [HOST, 1433], "UDP"),
    hit(402, "telnet", ["198.51.100.200", 49602], [HOST, 23], "TCP"),
  ] satisfies SuricataAlert[],
  scanned: 214,
  bySeverity: [
    { severity: 1, label: "High", count: 6 },
    { severity: 2, label: "Medium", count: 58 },
    { severity: 3, label: "Low", count: 150 },
  ],
  topSignatures: [
    {
      signature: SIGNATURES.mssql[0],
      signatureId: SIGNATURES.mssql[1],
      category: SIGNATURES.mssql[2],
      severity: 2,
      count: 61,
    },
    {
      signature: SIGNATURES.nmap[0],
      signatureId: SIGNATURES.nmap[1],
      category: SIGNATURES.nmap[2],
      severity: 3,
      count: 47,
    },
    {
      signature: SIGNATURES.telnet[0],
      signatureId: SIGNATURES.telnet[1],
      category: SIGNATURES.telnet[2],
      severity: 3,
      count: 39,
    },
    {
      signature: SIGNATURES.ssh[0],
      signatureId: SIGNATURES.ssh[1],
      category: SIGNATURES.ssh[2],
      severity: 2,
      count: 33,
    },
    {
      signature: SIGNATURES.ua[0],
      signatureId: SIGNATURES.ua[1],
      category: SIGNATURES.ua[2],
      severity: 3,
      count: 24,
    },
    {
      signature: SIGNATURES.log4j[0],
      signatureId: SIGNATURES.log4j[1],
      category: SIGNATURES.log4j[2],
      severity: 1,
      count: 6,
    },
  ],
  rulesLoaded: 47213,
  logPath: "/var/log/suricata/eve.json",
}

/**
 * sshd as `sshd -T` reports it with the five forwarding directives. The four
 * the jump-host preset sets all differ from it, so staging the preset changes
 * four values; MaxSessions is not one of them.
 */
export const sshd = {
  available: true,
  source: "/usr/sbin/sshd -T",
  ports: ["22"],
  managedFile: "/etc/ssh/sshd_config.d/99-just-dashboard.conf",
  keyedAccounts: [{ user: "ubuntu", keys: 2 }],
  hasMatchBlocks: false,
  socket: { unit: "ssh.socket", ports: ["22"] },
  settings: [
    {
      key: "permitrootlogin",
      label: "Root login",
      value: "prohibit-password",
      recommended: "prohibit-password",
      secure: true,
      detail: "Whether root may log in over SSH at all.",
      options: ["no", "prohibit-password", "forced-commands-only", "yes"],
      kind: "choice",
    },
    {
      key: "passwordauthentication",
      label: "Password authentication",
      value: "yes",
      recommended: "no",
      secure: false,
      detail: "Whether a password alone is enough to get a shell.",
      risk: "With this on, your server's security is whatever the weakest password on it is.",
      options: ["no", "yes"],
      kind: "choice",
    },
    {
      key: "maxauthtries",
      label: "Attempts per connection",
      value: "6",
      recommended: "3 or fewer",
      secure: false,
      detail: "How many guesses one connection gets.",
      risk: "Six passwords per handshake instead of one.",
      kind: "number",
    },
    {
      key: "port",
      label: "Port",
      value: "22",
      recommended: "any",
      secure: true,
      detail: "Where sshd listens.",
      kind: "number",
    },
    {
      key: "allowtcpforwarding",
      label: "TCP forwarding",
      value: "no",
      recommended: "no",
      secure: true,
      detail:
        "Whether a login may ask this server to open connections for it. It is what lets ssh -J use this server as a jump host.",
      options: ["yes", "no", "local", "remote", "all"],
      kind: "choice",
    },
    {
      key: "allowagentforwarding",
      label: "Agent forwarding",
      value: "yes",
      recommended: "no",
      secure: false,
      detail: "Whether a login may forward its ssh-agent to this server.",
      risk: "Anybody who is root here while you are logged in can use your keys.",
      options: ["yes", "no"],
      kind: "choice",
    },
    {
      key: "gatewayports",
      label: "Gateway ports",
      value: "clientspecified",
      recommended: "no",
      secure: false,
      detail: "Whether a forwarded port listens on every interface rather than on loopback.",
      risk: "A reverse tunnel can publish a service on this server's public address.",
      options: ["no", "yes", "clientspecified"],
      kind: "choice",
    },
    {
      key: "permittunnel",
      label: "Tunnel devices",
      value: "ethernet",
      recommended: "no",
      secure: false,
      detail: "Whether a login may open a tun or tap device on this server.",
      risk: "A layer-2 tunnel is a way onto this server's network that no firewall rule names.",
      options: ["no", "yes", "point-to-point", "ethernet"],
      kind: "choice",
    },
    {
      key: "maxsessions",
      label: "Sessions per connection",
      value: "10",
      recommended: "any",
      secure: true,
      detail: "How many shells, forwards and subsystems one network connection may multiplex.",
      kind: "number",
    },
    {
      key: "allowusers",
      label: "Only these accounts may log in",
      value: "",
      recommended: "",
      secure: true,
      detail: "A space-separated list.",
      kind: "list",
    },
  ],
}

/** What `/network/vpn` gives the bastion's suggestions: two tunnel peers, three tailnet machines. */
export const vpn = {
  wireguard: {
    installed: true,
    interfaces: [
      {
        name: "wg0",
        subnet: "10.8.0.0/24",
        peers: [
          { name: "office-nas", publicKey: "k1", address: "10.8.0.2/32", online: true },
          { name: "phone", publicKey: "k2", address: "10.8.0.3/32", online: false },
          { name: "branch-office", publicKey: "k3", address: "192.168.50.0/24", online: true },
        ],
      },
    ],
  },
  tailscale: {
    installed: true,
    peers: [
      {
        id: "n1",
        hostName: "build-box",
        tailscaleIps: ["100.64.0.12", "fd7a:115c:a1e0::c"],
        online: true,
      },
      { id: "n2", hostName: "laptop", tailscaleIps: ["100.64.0.20"], online: false },
      { id: "n3", hostName: "db-replica", tailscaleIps: ["100.64.0.31"], online: true },
    ],
  },
  headscale: {},
}

export const overview = {
  dockerNetworks: [
    {
      id: "n-1",
      name: "app_default",
      subnets: ["172.18.0.0/16"],
      containers: [
        { name: "web", image: "caddy:2" },
        { name: "api", image: "app:latest" },
      ],
    },
    { id: "n-2", name: "monitoring", subnets: ["172.19.0.0/24"], containers: [] },
  ],
}

function json(route: Route, body: unknown, status = 200) {
  return route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) })
}

/**
 * Intrusion's and SSH's API, answered from the fixtures above; every write is
 * recorded. `overrides` answers a GET route with a body of its own (a
 * tool that is not installed, a log Suricata may not read) and `refuse`
 * answers a write — keyed `"POST /path"` — with an error, which is how a
 * ban that would lock the operator out is drawn.
 */
export async function mockIntrusion(
  page: Page,
  mutations: Mutation[] = [],
  options: {
    session?: typeof admin
    overrides?: Record<string, unknown>
    /** GET routes the account may not read, answered 403 as the server does. */
    forbidden?: string[]
    refuse?: Record<string, { status: number; code: string; message: string }>
  } = {},
) {
  await page.route("**/api/v1/**", async (route) => {
    const request = route.request()
    const path = new URL(request.url()).pathname.replace(/^\/api\/v1/, "")
    if (request.method() !== "GET") {
      mutations.push({ method: request.method(), path, body: request.postDataJSON() })
      const refusal = options.refuse?.[`${request.method()} ${path}`]
      if (refusal) {
        return json(
          route,
          { error: { code: refusal.code, message: refusal.message } },
          refusal.status,
        )
      }
      return json(route, { output: "ok" })
    }
    if (options.forbidden?.includes(path)) {
      return json(route, { error: { code: "forbidden", message: "Not allowed." } }, 403)
    }
    const overrides = options.overrides ?? {}
    if (path in overrides) return json(route, overrides[path])
    switch (path) {
      case "/auth/session":
        return json(route, options.session ?? admin)
      case "/exposure":
        return json(route, exposure)
      case "/security/posture":
        return json(route, posture)
      case "/firewall/":
        return json(route, firewall)
      case "/fail2ban/":
        return json(route, jails)
      case "/fail2ban/offenders":
        return json(route, offenders)
      case "/security/crowdsec/":
        return json(route, crowdsec)
      case "/security/suricata/":
        return json(route, suricata)
      case "/ssh/config":
        return json(route, sshd)
      case "/network/vpn":
        return json(route, vpn)
      case "/network/overview":
        return json(route, overview)
      case "/updates/self":
        return json(route, { current: "0.7.1", latest: "0.7.1" })
      default:
        return json(route, [])
    }
  })
  return mockHostLogs(page)
}
