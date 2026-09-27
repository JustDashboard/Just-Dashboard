import type { Page } from "@playwright/test"

/**
 * A host's own logs, as the server reads them through their lenses: the auth
 * log, the firewall's, fail2ban's and the package manager's. Each line is one
 * the research found on a real host (`research-system-logs.md`), with the
 * event and values `backend/internal/logsx` reads out of it — run through the
 * lenses themselves to write these down — stamped relative to now so a
 * reading over the last day counts them.
 *
 * Searches and live tails are answered from these lines, narrowed by the
 * filter they carry, so a quick view or a reading asks the question a server
 * would answer and gets the answer these lines give. Every request is
 * recorded, so a spec can say what was — or was not — asked.
 */

type Line = {
  text: string
  timestamp?: string
  level?: string
  event?: string
  attrs?: Record<string, string>
}

const ago = (minutes: number) => new Date(Date.now() - minutes * 60_000)
const iso = (minutes: number) => ago(minutes).toISOString()

/** rsyslog's RFC 3339 prefix, the way Ubuntu writes auth.log and ufw.log. */
function syslog(
  minutes: number,
  program: string,
  message: string,
  event: string | undefined,
  attrs: Record<string, string>,
  level = "info",
): Line {
  return {
    text: `${iso(minutes).replace("Z", "+00:00")} web-1 ${program}: ${message}`,
    timestamp: iso(minutes),
    level,
    event,
    attrs,
  }
}

function sshd(minutes: number, pid: string, message: string, event: string, attrs: object) {
  const level = event === "ssh_max_attempts" ? "warn" : "info"
  return syslog(
    minutes,
    `sshd-session[${pid}]`,
    message,
    event,
    { pid, program: "sshd-session", ...attrs },
    level,
  )
}

const KEY = "SHA256:q0M5HxL6lJ4DfN2Wd0hIuW6xUQY0r3C0yJ4o3m0cP1E"

export const AUTH_LINES: Line[] = [
  sshd(
    1300,
    "2025802",
    `Accepted publickey for deploy from 198.51.100.20 port 50122 ssh2: ED25519 ${KEY}`,
    "ssh_accepted",
    { client: "198.51.100.20", port: "50122", method: "publickey", key: KEY, user: "deploy" },
  ),
  sshd(
    1300,
    "2025802",
    "pam_unix(sshd:session): session opened for user deploy(uid=1001) by deploy(uid=0)",
    "session_opened",
    { user: "deploy" },
  ),
  sshd(610, "3110301", "Invalid user admin from 203.0.113.42 port 30358", "ssh_invalid_user", {
    client: "203.0.113.42",
    port: "30358",
    user: "admin",
  }),
  sshd(
    610,
    "3110301",
    "Failed password for invalid user admin from 203.0.113.42 port 30358 ssh2",
    "ssh_failed",
    { client: "203.0.113.42", port: "30358", method: "password", user: "admin" },
  ),
  sshd(420, "3195216", "Failed password for root from 203.0.113.197 port 9046 ssh2", "ssh_failed", {
    client: "203.0.113.197",
    port: "9046",
    method: "password",
    user: "root",
  }),
  sshd(
    420,
    "3195216",
    "Connection closed by authenticating user root 203.0.113.197 port 9046 [preauth]",
    "ssh_preauth_closed",
    { client: "203.0.113.197", port: "9046", user: "root" },
  ),
  sshd(300, "3195301", "Connection closed by 203.0.113.250 port 51902 [preauth]", "ssh_scan", {
    client: "203.0.113.250",
    port: "51902",
  }),
  sshd(240, "3110377", "Invalid user ekala from 203.0.113.42 port 30412", "ssh_invalid_user", {
    client: "203.0.113.42",
    port: "30412",
    user: "ekala",
  }),
  sshd(
    240,
    "3110377",
    "Failed password for invalid user ekala from 203.0.113.42 port 30412 ssh2",
    "ssh_failed",
    { client: "203.0.113.42", port: "30412", method: "password", user: "ekala" },
  ),
  sshd(
    180,
    "3195300",
    "Failed password for root from 198.51.100.61 port 40022 ssh2",
    "ssh_failed",
    {
      client: "198.51.100.61",
      port: "40022",
      method: "password",
      user: "root",
    },
  ),
  sshd(
    179,
    "3195300",
    "error: maximum authentication attempts exceeded for root from 198.51.100.61 port 40022 ssh2 [preauth]",
    "ssh_max_attempts",
    { client: "198.51.100.61", port: "40022", user: "root" },
  ),
  syslog(
    120,
    "sudo",
    "  ubuntu : TTY=pts/0 ; PWD=/home/ubuntu ; USER=root ; COMMAND=/usr/bin/systemctl restart nginx",
    "sudo",
    {
      command: "/usr/bin/systemctl restart nginx",
      program: "sudo",
      pwd: "/home/ubuntu",
      runas: "root",
      tty: "pts/0",
      user: "ubuntu",
    },
  ),
  syslog(
    60,
    "sudo",
    "     bob : 3 incorrect password attempts ; TTY=pts/1 ; PWD=/home/bob ; USER=root ; COMMAND=/usr/bin/apt update",
    "sudo_failed",
    {
      command: "/usr/bin/apt update",
      program: "sudo",
      pwd: "/home/bob",
      runas: "root",
      tty: "pts/1",
      user: "bob",
    },
    "warn",
  ),
  sshd(
    30,
    "2025716",
    "Accepted password for ubuntu from 100.64.12.7 port 63210 ssh2",
    "ssh_accepted",
    { client: "100.64.12.7", port: "63210", method: "password", user: "ubuntu" },
  ),
  sshd(
    30,
    "2025716",
    "pam_unix(sshd:session): session opened for user ubuntu(uid=1000) by ubuntu(uid=0)",
    "session_opened",
    { user: "ubuntu" },
  ),
  syslog(
    15,
    "CRON[40211]",
    "pam_unix(cron:session): session opened for user root(uid=0) by root(uid=0)",
    "cron_session",
    { pid: "40211", program: "CRON", user: "root" },
    "debug",
  ),
  sshd(8, "3195412", "Failed password for root from 203.0.113.197 port 9051 ssh2", "ssh_failed", {
    client: "203.0.113.197",
    port: "9051",
    method: "password",
    user: "root",
  }),
]

const MAC = "MAC=52:54:00:12:34:56:52:54:00:65:43:21:08:00"

function ufw(
  minutes: number,
  action: string,
  src: string,
  proto: string,
  spt: string,
  dpt: string,
): Line {
  const event = action === "LIMIT BLOCK" ? "limit" : action === "ALLOW" ? "allow" : "block"
  return syslog(
    minutes,
    "kernel",
    `[UFW ${action}] IN=ens3 OUT= ${MAC} SRC=${src} DST=198.51.100.87 LEN=40 TOS=0x00 PREC=0x00 TTL=238 ID=50812 PROTO=${proto} SPT=${spt} DPT=${dpt} WINDOW=1024 RES=0x00 SYN URGP=0 `,
    event,
    { client: src, dpt, dst: "198.51.100.87", iface: "ens3", len: "40", proto, spt },
    event === "limit" ? "warn" : "info",
  )
}

export const FIREWALL_LINES: Line[] = [
  ufw(1200, "BLOCK", "203.0.113.11", "TCP", "54703", "4448"),
  ufw(900, "BLOCK", "203.0.113.27", "UDP", "60800", "5060"),
  ufw(640, "BLOCK", "203.0.113.11", "TCP", "54703", "3389"),
  ufw(420, "BLOCK", "203.0.113.11", "TCP", "54711", "23"),
  ufw(181, "LIMIT BLOCK", "198.51.100.61", "TCP", "40112", "22"),
  ufw(120, "ALLOW", "198.51.100.20", "TCP", "50122", "22"),
  ufw(40, "BLOCK", "203.0.113.27", "TCP", "61022", "8080"),
  ufw(9, "LIMIT BLOCK", "203.0.113.197", "TCP", "9051", "22"),
]

/** fail2ban's own stamp, "2026-09-27 09:20:10,244", and its padded logger and level. */
function fail2ban(
  minutes: number,
  logger: string,
  pid: string,
  word: string,
  message: string,
  event: string,
  attrs: Record<string, string>,
): Line {
  const stamp = iso(minutes)
  return {
    text: `${stamp.slice(0, 10)} ${stamp.slice(11, 19)},${stamp.slice(20, 23)} ${`fail2ban.${logger}`.padEnd(24)}[${pid}]: ${word.padEnd(8)}${message}`,
    timestamp: stamp,
    level: event === "ban" ? "warn" : "info",
    event,
    attrs: { pid, ...attrs },
  }
}

const found = (minutes: number, ip: string) =>
  fail2ban(
    minutes,
    "filter",
    "700",
    "INFO",
    `[sshd] Found ${ip} - ${iso(minutes).slice(0, 10)} ${iso(minutes).slice(11, 19)}`,
    "found",
    { client: ip, jail: "sshd" },
  )

export const FAIL2BAN_LINES: Line[] = [
  fail2ban(1400, "jail", "700", "INFO", "Jail 'sshd' started", "jail_started", { jail: "sshd" }),
  fail2ban(1400, "jail", "700", "INFO", "Jail 'nginx-http-auth' started", "jail_started", {
    jail: "nginx-http-auth",
  }),
  found(620, "203.0.113.42"),
  found(615, "203.0.113.42"),
  found(611, "203.0.113.42"),
  fail2ban(610, "actions", "992", "NOTICE", "[sshd] Ban 203.0.113.42", "ban", {
    client: "203.0.113.42",
    jail: "sshd",
  }),
  found(421, "203.0.113.197"),
  found(420, "203.0.113.197"),
  fail2ban(419, "actions", "992", "NOTICE", "[sshd] Ban 203.0.113.197", "ban", {
    client: "203.0.113.197",
    jail: "sshd",
  }),
  fail2ban(600, "actions", "992", "NOTICE", "[sshd] Unban 203.0.113.42", "unban", {
    client: "203.0.113.42",
    jail: "sshd",
  }),
  found(241, "203.0.113.42"),
  fail2ban(240, "actions", "992", "NOTICE", "[sshd] Ban 203.0.113.42", "ban", {
    client: "203.0.113.42",
    jail: "sshd",
  }),
  found(180, "198.51.100.61"),
  fail2ban(
    30,
    "observer",
    "992",
    "NOTICE",
    "[sshd] Increase Ban 203.0.113.9 (3 # 4:00:00 -> 2026-09-27 13:31:02)",
    "increase",
    { client: "203.0.113.9", jail: "sshd" },
  ),
  found(8, "203.0.113.197"),
].sort((a, b) => a.timestamp!.localeCompare(b.timestamp!))

/** apt's history.log: one transaction a block, its start carried onto every line of it. */
function apt(minutes: number, rows: [string, string | undefined, Record<string, string>][]) {
  const stamp = iso(minutes)
  const start = `${stamp.slice(0, 10)}  ${stamp.slice(11, 19)}`
  return [
    { text: `Start-Date: ${start}`, timestamp: stamp, level: "info", event: "transaction_start" },
    ...rows.map(([text, event, attrs]) => ({
      text,
      timestamp: stamp,
      level: "info",
      event,
      attrs,
    })),
    // The blank line between transactions, which the server reads as nothing.
    { text: "" },
  ]
}

export const APT_LINES: Line[] = [
  ...apt(60 * 72, [
    [
      "Commandline: apt-get install -y postgresql",
      "command",
      { command: "apt-get install -y postgresql" },
    ],
    [
      "Requested-By: ubuntu (1000)",
      undefined,
      { command: "apt-get install -y postgresql", user: "ubuntu" },
    ],
    [
      "Install: postgresql-client-16:amd64 (16.3-0ubuntu0.24.04.1, automatic), postgresql-16:amd64 (16.3-0ubuntu0.24.04.1, automatic), postgresql:amd64 (16+257build1)",
      "install",
      {
        command: "apt-get install -y postgresql",
        package: "postgresql, postgresql-client-16, postgresql-16",
        packages: "3",
        user: "ubuntu",
      },
    ],
  ]),
  ...apt(60 * 50, [
    ["Commandline: apt remove htop", "command", { command: "apt remove htop" }],
    ["Requested-By: ubuntu (1000)", undefined, { command: "apt remove htop", user: "ubuntu" }],
    [
      "Remove: htop:amd64 (3.3.0-4build1)",
      "remove",
      { command: "apt remove htop", package: "htop", packages: "1", user: "ubuntu" },
    ],
  ]),
  ...apt(60 * 20, [
    [
      "Commandline: /usr/bin/unattended-upgrade",
      "command",
      { command: "/usr/bin/unattended-upgrade" },
    ],
    [
      "Upgrade: openssl:amd64 (3.0.13-0ubuntu3, 3.0.13-0ubuntu3.4), libssl3t64:amd64 (3.0.13-0ubuntu3, 3.0.13-0ubuntu3.4)",
      "upgrade",
      { command: "/usr/bin/unattended-upgrade", package: "openssl, libssl3t64", packages: "2" },
    ],
  ]),
]

function dpkg(minutes: number, rest: string, event?: string, attrs?: Record<string, string>) {
  const stamp = iso(minutes)
  return {
    text: `${stamp.slice(0, 10)} ${stamp.slice(11, 19)} ${rest}`,
    timestamp: stamp,
    level: "info",
    event,
    attrs,
  }
}

export const DPKG_LINES: Line[] = [
  dpkg(60 * 50, "remove htop:amd64 3.3.0-4build1 <none>", "remove", {
    package: "htop",
    version: "3.3.0-4build1",
  }),
  dpkg(60 * 20, "upgrade openssl:amd64 3.0.13-0ubuntu3 3.0.13-0ubuntu3.4", "upgrade", {
    package: "openssl",
    version: "3.0.13-0ubuntu3.4",
    old_version: "3.0.13-0ubuntu3",
  }),
  dpkg(60 * 20, "status half-configured openssl:amd64 3.0.13-0ubuntu3"),
  dpkg(60 * 20, "configure openssl:amd64 3.0.13-0ubuntu3.4 <none>", "configure", {
    package: "openssl",
    version: "3.0.13-0ubuntu3.4",
  }),
  dpkg(60 * 20, "status installed openssl:amd64 3.0.13-0ubuntu3.4"),
]

const file = (path: string, label: string, lens: string, size: number, detail: string) => ({
  id: `file:${path}`,
  label,
  kind: "system",
  path,
  size,
  modified: iso(8),
  lens,
  rotated: true,
  archives: 4,
  archiveBytes: size * 6,
  detail,
})

export const HOST_LOG_SOURCES = [
  file(
    "/var/log/apt/history.log",
    "apt history",
    "packages",
    48_201,
    "Package transactions as apt recorded them, with the command that ran",
  ),
  file(
    "/var/log/auth.log",
    "auth",
    "auth",
    1_240_112,
    "Logins, sudo and sshd — who got in and who was refused",
  ),
  file(
    "/var/log/dpkg.log",
    "dpkg",
    "packages",
    210_442,
    "Every package installed, upgraded or removed, with timestamps",
  ),
  file(
    "/var/log/fail2ban.log",
    "fail2ban",
    "fail2ban",
    88_120,
    "Bans, unbans and the jails that issued them",
  ),
  file(
    "/var/log/kern.log",
    "kernel",
    "kernel",
    3_100_870,
    "Kernel messages: hardware, filesystems, the OOM killer",
  ),
  file(
    "/var/log/ufw.log",
    "ufw",
    "firewall",
    402_993,
    "Packets the firewall blocked or allowed, if logging is on",
  ),
  {
    id: "journal:",
    label: "systemd journal",
    kind: "journal",
    detail: "Every unit on the host — pick one below to narrow it",
    lens: "syslog",
    rotated: false,
  },
]

/** The lines each source id holds: a file, or the journal's reading of the same program. */
function linesOf(source: string): Line[] {
  if (source === "file:/var/log/auth.log" || source.startsWith("journal-id:sshd")) {
    return AUTH_LINES
  }
  if (source === "file:/var/log/ufw.log" || source === "file:/var/log/kern.log") {
    return FIREWALL_LINES
  }
  if (source === "kernel:") return FIREWALL_LINES
  if (source === "file:/var/log/fail2ban.log" || source === "journal:fail2ban.service") {
    return FAIL2BAN_LINES
  }
  if (source === "file:/var/log/apt/history.log") return APT_LINES
  if (source === "file:/var/log/dpkg.log") return DPKG_LINES
  return []
}

function valueOf(line: Line, key: string) {
  if (key === "event") return line.event
  if (key === "level") return line.level
  return line.attrs?.[key]
}

/** The filter a search or a socket carries, as `logsx.Filter` reads it — the part these specs use. */
function matches(line: Line, params: URLSearchParams) {
  const levels = (params.get("levels") ?? "").split(",").filter(Boolean)
  if (levels.length > 0 && !levels.includes(line.level ?? "unknown")) return false
  const q = params.get("q")?.toLowerCase()
  if (q && !line.text.toLowerCase().includes(q)) return false
  const byKey = new Map<string, string[]>()
  for (const predicate of params.getAll("f")) {
    const at = predicate.indexOf(":")
    const key = predicate.slice(0, at)
    byKey.set(key, [...(byKey.get(key) ?? []), predicate.slice(at + 1)])
  }
  for (const [key, values] of byKey) {
    const actual = valueOf(line, key)?.toLowerCase()
    const test = (value: string) =>
      value === "*" ? actual !== undefined : value.replace(/^=/, "").toLowerCase() === actual
    const wanted = values.filter((v) => !v.startsWith("!"))
    const refused = values.filter((v) => v.startsWith("!")).map((v) => v.slice(1))
    if (wanted.length > 0 && !wanted.some(test)) return false
    if (refused.some(test)) return false
  }
  return true
}

/** The line with its numbers, addresses and quoted strings folded, as `pattern` is. */
function patternOf(line: Line) {
  const message = line.text.replace(/^\S+ \S+ [^:]+: /, "")
  return message.replace(/\d+(?:[.:]\d+)*/g, "<*>")
}

function facet(lines: Line[], key: string, limit: number, sample: string[]) {
  const values = new Map<
    string,
    {
      value: string
      count: number
      errors: number
      first?: string
      last?: string
      samples: Record<string, string>
    }
  >()
  let missing = 0
  for (const line of lines) {
    const value = key === "pattern" ? patternOf(line) : valueOf(line, key)
    if (value === undefined) {
      missing++
      continue
    }
    const entry = values.get(value) ?? { value, count: 0, errors: 0, samples: {} }
    entry.count++
    if (line.level === "error" || line.level === "critical") entry.errors++
    entry.first ??= line.timestamp
    entry.last = line.timestamp ?? entry.last
    for (const k of sample) {
      const v = valueOf(line, k)
      if (v !== undefined) entry.samples[k] = v
    }
    values.set(value, entry)
  }
  const ranked = [...values.values()].sort((a, b) => b.count - a.count)
  return { values: ranked.slice(0, limit), distinct: ranked.length, other: 0, missing }
}

/** Twelve buckets from the first matching line to now, each counted by the key asked for. */
function histogram(lines: Line[], by: string) {
  const stamped = lines.filter((line) => line.timestamp)
  if (stamped.length === 0) return { buckets: [], seconds: 60 }
  const first = Math.min(...stamped.map((line) => Date.parse(line.timestamp!)))
  // Whole minutes, or whole hours past one, as the server's buckets are.
  const step = (ms: number) => (ms > 3_600_000 ? 3_600_000 : 60_000)
  const raw = Math.max(Date.now() - first, 3_600_000) / 12
  const width = Math.ceil(raw / step(raw)) * step(raw)
  const buckets = Array.from({ length: 12 }, (_, i) => ({
    start: new Date(first + i * width).toISOString(),
    total: 0,
    counts: {} as Record<string, number>,
  }))
  for (const line of stamped) {
    const at = Math.min(11, Math.floor((Date.parse(line.timestamp!) - first) / width))
    const key = valueOf(line, by) ?? "unknown"
    buckets[at].total++
    buckets[at].counts[key] = (buckets[at].counts[key] ?? 0) + 1
  }
  return { buckets, seconds: Math.round(width / 1000) }
}

function search(params: URLSearchParams) {
  // Numbered as a search numbers them, by their place in the file.
  const all = linesOf(params.get("source") ?? "").map((line, i) => ({ ...line, no: i + 1 }))
  const hits = all.filter((line) => matches(line, params))
  const limit = Number(params.get("limit") ?? 500)
  const keys = (params.get("facets") ?? "").split(",").filter(Boolean)
  const sample = (params.get("sample") ?? "").split(",").filter(Boolean)
  const by = params.get("histogramBy") ?? "level"
  const { buckets, seconds } = histogram(hits, by)
  return {
    lines: params.get("order") === "asc" ? hits.slice(0, limit) : hits.slice(-limit),
    scanned: all.length,
    matched: hits.length,
    truncated: false,
    complete: true,
    files: [],
    histogram: buckets,
    bucketSeconds: seconds,
    histogramBy: params.get("histogramBy") ?? undefined,
    tookMillis: 3,
    lens: params.get("lens") ?? undefined,
    facets: Object.fromEntries(
      keys.map((key, i) => [
        key,
        facet(hits, key, Number(params.get("facetLimit") ?? 12), i === 0 ? sample : []),
      ]),
    ),
  }
}

export type HostLogMocks = {
  /** Every `/logs/*` request, by path, in order. */
  requests: string[]
  /** Each live socket's query. */
  sockets: URLSearchParams[]
  /** Each `/logs/search`. */
  searches: URLSearchParams[]
}

/**
 * The logs routes over these lines. `sources` is what `/logs/sources` lists —
 * a host without auth.log, or without fail2ban's file, is the default list
 * with it taken out.
 */
export async function mockHostLogs(
  page: Page,
  { sources = HOST_LOG_SOURCES }: { sources?: typeof HOST_LOG_SOURCES } = {},
): Promise<HostLogMocks> {
  const recorded: HostLogMocks = { requests: [], sockets: [], searches: [] }
  await page.route("**/api/v1/logs/**", (route) => {
    const url = new URL(route.request().url())
    recorded.requests.push(url.pathname.replace(/^\/api\/v1/, ""))
    const reply = (body: unknown) =>
      route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(body) })
    if (url.pathname.endsWith("/logs/sources")) {
      return reply({ sources, units: [], roots: ["/var/log"], missing: {} })
    }
    if (url.pathname.endsWith("/logs/source")) {
      const id = url.searchParams.get("source")
      return reply(sources.find((s) => s.id === id) ?? {})
    }
    if (url.pathname.endsWith("/logs/search")) {
      recorded.searches.push(url.searchParams)
      return reply(search(url.searchParams))
    }
    return reply({})
  })
  await page.routeWebSocket("**/api/v1/logs/stream**", (socket) => {
    const params = new URL(socket.url()).searchParams
    recorded.sockets.push(params)
    recorded.requests.push("/logs/stream")
    socket.send(
      JSON.stringify({
        type: "meta",
        data: {
          kind: "system",
          label: params.get("source"),
          filtered: params.has("f") || params.has("q") || params.has("levels"),
          lens: params.get("lens") ?? undefined,
        },
        ts: Date.now(),
      }),
    )
    const lines = linesOf(params.get("source") ?? "").filter((line) => matches(line, params))
    socket.send(JSON.stringify({ type: "logs", data: lines, ts: Date.now() }))
  })
  return recorded
}
