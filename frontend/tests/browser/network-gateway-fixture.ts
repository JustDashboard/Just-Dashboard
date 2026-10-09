import { iso } from "./network-fixture"

/**
 * The Gateway and Protection pages' API, from the shapes `netx` writes, for
 * `mockNetwork(page, mutations, { overrides })` in `network-fixture.ts`, which
 * already answers the session, the exposure and the devices (`/network/links`:
 * ens3 and the dashboard's own bridge among them) these pages read beside
 * their own.
 *
 * The server is the one that fixture describes: a small VPS with its public
 * address on `ens3`, Docker networks on 10.0.x.0/24, a WireGuard server on
 * `wg0` and a lab network the dashboard shares out. Addresses are
 * documentation ranges and private ones; nothing here is a real host's.
 */

const made = { createdAt: iso(60 * 24 * 12), createdBy: "operator" }

const ready = (fields: Record<string, unknown> = {}) => ({
  policy: "installed",
  rules: 1,
  expected: 1,
  forwarding: true,
  admission: "present",
  ready: true,
  reachability: "unverified",
  reason: "Installed and admitted. A visitor reaching the target is still unmeasured.",
  ...fields,
})

const total = (packets: number, resets = 2) => ({
  packets,
  bytes: packets * 900,
  since: iso(60 * 24 * 9),
  resets,
})

/** Counter labels: the table generation the live figures belong to. */
export const counters = {
  generation: 41,
  observedAt: iso(1),
  persistent: true,
  gap: "Traffic counted between the last reading and a table replacement made outside the dashboard (a reboot, nft run by hand) is not included.",
}

export const gateway = {
  capability: { writable: true, firewall: "ufw", docker: true },
  loaded: true,
  forwarding: { ipv4: true, ipv6: false },
  admission: { needed: true, present: true },
  forwards: [
    {
      ...made,
      id: 1,
      name: "Website",
      protocol: "tcp",
      interface: "",
      ports: "8080",
      target: "10.0.4.5",
      targetPort: "80",
      sources: [],
      sourceNat: "auto",
      masquerade: false,
      enabled: true,
      changedAt: iso(60 * 5),
      packets: 184_320,
      bytes: 211_450_000,
      total: total(1_284_320),
      readiness: ready({ reachability: "verified" }),
      external: {
        status: "connected",
        checkId: "c1",
        vantage: "Controlled source A",
        location: "Operator-declared region A",
        placement: "external_host",
        address: "203.0.113.20",
        port: 8080,
        family: "inet",
        checkedAt: iso(30),
        detail: "TCP connected from this enrolled source to this pinned address at this time.",
        current: true,
        basis:
          "A TCP connection from an enrolled source to this server's address and the forward's public port, as that source reported it.",
      },
    },
    {
      ...made,
      id: 2,
      name: "Minecraft",
      protocol: "tcp",
      interface: "ens3",
      ports: "25565",
      target: "10.0.1.9",
      targetPort: "",
      sources: [],
      sourceNat: "auto",
      masquerade: false,
      enabled: true,
      packets: 52_904,
      bytes: 38_120_000,
      total: total(52_904, 0),
      readiness: ready(),
      decision: {
        stored: false,
        current: true,
        drift: true,
        reason:
          "10.0.1.9 is not on a network this server routes; masquerading makes its replies come back through this server.",
      },
    },
    {
      ...made,
      id: 3,
      name: "Game server",
      protocol: "udp",
      interface: "ens3",
      ports: "27015",
      target: "10.0.1.9",
      targetPort: "",
      sources: ["203.0.113.0/24", "198.51.100.7/32", "192.0.2.0/28"],
      sourceNat: "never",
      masquerade: false,
      enabled: true,
      packets: 9_411,
      bytes: 2_980_000,
      readiness: ready({
        ready: false,
        admission: "absent",
        reason:
          "The owned admission rules of its family are not all present; the firewall may refuse it after translation.",
      }),
    },
    {
      ...made,
      id: 4,
      name: "Staging",
      protocol: "tcp",
      interface: "",
      ports: "8443",
      target: "10.0.4.7",
      targetPort: "443",
      sources: ["100.64.0.0/10"],
      sourceNat: "always",
      masquerade: true,
      enabled: false,
      packets: 0,
      bytes: 0,
    },
  ],
  nat: [
    {
      ...made,
      id: 1,
      name: "Lab network",
      source: "10.20.0.0/24",
      interface: "ens3",
      toAddress: "",
      mode: "masquerade",
      translated: "",
      destinations: [],
      owner: "",
      enabled: true,
      packets: 14_882,
      bytes: 9_310_000,
      readiness: ready({ expected: 2, rules: 2 }),
    },
    {
      ...made,
      id: 3,
      name: "Mail host",
      source: "10.0.4.25/32",
      interface: "ens3",
      toAddress: "",
      mode: "one-to-one",
      translated: "203.0.113.25/32",
      destinations: [],
      owner: "",
      enabled: true,
      packets: 4_102,
      bytes: 2_210_000,
      inPackets: 8_877,
      inBytes: 6_010_000,
      readiness: ready({ expected: 3, rules: 3 }),
    },
    {
      ...made,
      id: 2,
      name: "wg0 exit",
      source: "10.8.0.0/24",
      interface: "ens3",
      toAddress: "",
      owner: "wireguard:wg0",
      enabled: true,
      packets: 120_411,
      bytes: 88_730_000,
    },
  ],
  counters,
  flows: [
    {
      entry: "forward:1",
      name: "Website",
      direction: "inbound",
      verdict: "clear",
      layers: [
        {
          family: "ip",
          table: "raw",
          chain: "PREROUTING",
          hook: "prerouting",
          verdict: "clear",
          rule: 0,
          reason: "No rule of a supported form drops it, and the chain's policy accepts.",
        },
      ],
    },
    {
      entry: "forward:3",
      name: "Game server",
      direction: "inbound",
      verdict: "unknown",
      layers: [
        {
          family: "inet",
          table: "crowdsec",
          chain: "forward",
          hook: "forward",
          verdict: "unknown",
          rule: 2,
          reason: "Expression 1 (limit) of this rule cannot be decided for the flow.",
        },
      ],
    },
  ],
}

/** What `POST /gateway/preview` answers for a new forward on 8080. */
export const forwardPreview = {
  valid: true,
  impacts: [
    {
      severity: "warning",
      kind: "listener",
      message:
        "caddy listens on 0.0.0.0:3000; new connections to port 3000 will go to 10.0.4.12 instead of it. Connections already open stay with it.",
    },
    {
      severity: "info",
      kind: "limit",
      entry: "limit:1",
      message: 'The limit "SSH" (22) also judges these connections after translation.',
    },
  ],
  flows: [
    {
      entry: "forward:0",
      name: "Grafana",
      direction: "inbound",
      verdict: "clear",
      layers: [
        {
          family: "ip",
          table: "nat",
          chain: "PREROUTING",
          hook: "prerouting",
          verdict: "clear",
          rule: 0,
          reason: "No rule of a supported form drops it, and the chain's policy accepts.",
        },
      ],
    },
  ],
}

const setting = (
  key: string,
  label: string,
  fields: {
    why: string
    recommended: string
    current: string
    allowed?: string[]
    min?: number
    max?: number
    setHere?: boolean
    available?: boolean
  },
) => {
  const allowed = fields.allowed ?? []
  const available = fields.available ?? true
  return {
    key,
    label,
    why: fields.why,
    recommended: fields.recommended,
    kind: allowed.length > 0 ? "choice" : "number",
    allowed,
    min: fields.min ?? 0,
    max: fields.max ?? 0,
    current: fields.current,
    available,
    setHere: fields.setHere ?? false,
    value: fields.setHere ? fields.current : "",
    atRecommended: available && fields.current === fields.recommended,
  }
}

const onOff = ["0", "1"]

export const settings = [
  setting("net.ipv4.tcp_syncookies", "SYN cookies", {
    why: "Answers a flood of half-open connections without keeping state for each, so a SYN flood cannot fill the connection queue and refuse real visitors.",
    recommended: "1",
    current: "1",
    allowed: onOff,
    setHere: true,
  }),
  setting("net.ipv4.conf.all.rp_filter", "Reverse-path filter (all interfaces)", {
    why: "Drops packets whose source address could not be reached back through the interface they came in on. Loose (2) only requires some route to exist.",
    recommended: "2",
    current: "2",
    allowed: ["0", "2"],
  }),
  setting("net.ipv4.conf.default.rp_filter", "Reverse-path filter (new interfaces)", {
    why: "The same filter for interfaces created after boot, such as a container's or a tunnel's.",
    recommended: "2",
    current: "0",
    allowed: ["0", "2"],
  }),
  setting("net.ipv4.conf.all.accept_redirects", "Accept ICMP redirects (IPv4)", {
    why: "A redirect tells this server to send traffic through another router. A host that is not a client of a LAN should never take that advice.",
    recommended: "0",
    current: "0",
    allowed: onOff,
  }),
  setting("net.ipv6.conf.all.accept_redirects", "Accept ICMP redirects (IPv6)", {
    why: "The IPv6 form of the same redirect, which neighbours on the same link can send.",
    recommended: "0",
    current: "1",
    allowed: onOff,
  }),
  setting("net.ipv4.conf.all.send_redirects", "Send ICMP redirects", {
    why: "Only a router has any business telling a neighbour to use another gateway.",
    recommended: "0",
    current: "1",
    allowed: onOff,
  }),
  setting("net.ipv4.conf.all.accept_source_route", "Accept source-routed packets (IPv4)", {
    why: "A source-routed packet carries the path it wants to take, which lets a sender bypass the routing and filtering the network was built around.",
    recommended: "0",
    current: "0",
    allowed: onOff,
  }),
  setting("net.ipv6.conf.all.accept_source_route", "Accept source-routed packets (IPv6)", {
    why: "The IPv6 form of the same, with the same risk.",
    recommended: "0",
    current: "0",
    allowed: onOff,
  }),
  setting("net.ipv4.icmp_echo_ignore_broadcasts", "Ignore broadcast pings", {
    why: "A ping to a broadcast address makes every host on the network answer; spoofed, it turns this server into an amplifier.",
    recommended: "1",
    current: "1",
    allowed: onOff,
  }),
  setting("net.ipv4.icmp_ignore_bogus_error_responses", "Ignore bogus ICMP errors", {
    why: "Some routers answer a broadcast with malformed error replies; ignoring them keeps the kernel's log from filling.",
    recommended: "1",
    current: "1",
    allowed: onOff,
  }),
  setting("net.ipv4.tcp_rfc1337", "Protect against TIME-WAIT assassination", {
    why: "Stops a stray reset from tearing down a connection that is already closing.",
    recommended: "1",
    current: "0",
    allowed: onOff,
  }),
  setting("net.ipv4.tcp_max_syn_backlog", "SYN backlog", {
    why: "How many half-open connections the kernel remembers per listener. A busy server wants room to absorb a burst before SYN cookies have to take over.",
    recommended: "4096",
    current: "1024",
    min: 128,
    max: 262144,
    setHere: true,
  }),
  setting("net.ipv4.tcp_synack_retries", "SYN-ACK retries", {
    why: "How many times the server repeats its reply to a connection that never completes. Two gives a slow client time and a flood less of it.",
    recommended: "2",
    current: "2",
    min: 1,
    max: 5,
  }),
  setting("net.ipv4.conf.all.log_martians", "Log impossible source addresses", {
    why: "Writes a kernel log line for each packet with a source address that cannot be real. Off is the recommendation, and on is the right temporary choice.",
    recommended: "0",
    current: "0",
    allowed: onOff,
  }),
  setting("net.netfilter.nf_conntrack_max", "Connection tracking table size", {
    why: "How many connections the kernel can track at once. When the table is full new connections are dropped; raise it before the meter reaches the top.",
    recommended: "262144",
    current: "65536",
    min: 65536,
    max: 4194304,
  }),
]

export const protection = {
  loaded: true,
  limits: [
    {
      ...made,
      id: 1,
      name: "SSH",
      protocol: "tcp",
      ports: "22",
      rate: 10,
      per: "minute",
      burst: 5,
      perSource: true,
      maxConnections: 0,
      action: "drop",
      enabled: true,
      packets: 2_481,
      bytes: 148_860,
      total: total(12_481),
      meters: { rateSources: 41, connSources: null, capacity: 65_535, checkedAt: iso(0) },
    },
    {
      ...made,
      id: 2,
      name: "Website",
      protocol: "tcp",
      ports: "443",
      rate: 0,
      per: "",
      burst: 0,
      perSource: true,
      maxConnections: 200,
      globalConnections: 5_000,
      action: "reject",
      enabled: true,
      packets: 37,
      bytes: 2_220,
      globalPackets: 3,
    },
  ],
  blocklists: [
    {
      ...made,
      id: 1,
      name: "China and Russia",
      kind: "country",
      countries: ["cn", "ru"],
      url: "",
      entries: [],
      enabled: true,
      refreshed: iso(60 * 5),
      count: 9_843,
      error: "",
      containsYou: false,
      packets: 48_210,
      bytes: 3_012_000,
      refresh: "24h",
      nextRefresh: new Date(Date.now() + 19 * 3_600_000).toISOString(),
      integrity: "https",
      lastDiff: { at: iso(60 * 5), added: 12, removed: 3, baseline: true },
      coverage: {
        ipv4Addresses: 412_000_000,
        ipv4Share: 0.0959,
        ipv6Slash48s: 1_204_000,
        ipv4Networks: 8_400,
        ipv6Networks: 1_443,
      },
      sources: [
        {
          url: "https://www.ipdeny.com/ipblocks/data/aggregated/cn-aggregated.zone",
          country: "cn",
          family: "ipv4",
          status: "ok",
          fetchedAt: iso(60 * 5),
          bytes: 120_000,
          sha256: "9f2c4c5e8d1b7a6f0e3d2c1b0a998877665544332211ffeeddccbbaa00112233",
          networks: 7_200,
          skipped: 0,
        },
        {
          url: "https://www.ipdeny.com/ipv6/ipaddresses/aggregated/cn-aggregated.zone",
          country: "cn",
          family: "ipv6",
          status: "ok",
          fetchedAt: iso(60 * 5),
          bytes: 30_000,
          sha256: "1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f708192a3b4c5d6e7f809",
          networks: 1_443,
          skipped: 0,
        },
      ],
    },
    {
      ...made,
      id: 2,
      name: "Spamhaus DROP",
      kind: "feed",
      countries: [],
      url: "https://www.spamhaus.org/drop/drop.txt",
      entries: [],
      enabled: true,
      refreshed: iso(60 * 3),
      count: 1_421,
      error: "",
      containsYou: false,
      packets: 3_302,
      bytes: 198_000,
      refresh: "6h",
      nextRefresh: new Date(Date.now() + 3 * 3_600_000).toISOString(),
      integrity: "signed",
      signatureUrl: "https://lists.example/drop.txt.sig",
      lastDiff: { at: iso(60 * 3), added: 0, removed: 0, baseline: true },
    },
    {
      ...made,
      id: 3,
      name: "FireHOL level 1",
      kind: "feed",
      countries: [],
      url: "https://iplists.firehol.org/files/firehol_level1.netset",
      entries: [],
      enabled: true,
      refreshed: iso(60 * 50),
      count: 4_390,
      error: "the feed answered 503; the copy from two days ago is in force",
      containsYou: false,
      packets: 611,
      bytes: 36_660,
      refresh: "24h",
      failures: 3,
      stale: true,
      nextRefresh: new Date(Date.now() + 40 * 60_000).toISOString(),
      integrity: "https",
    },
    {
      ...made,
      id: 4,
      name: "Scanners I found",
      kind: "manual",
      countries: [],
      url: "",
      entries: ["198.51.100.0/24", "203.0.113.77/32", "192.0.2.128/25"],
      enabled: false,
      refreshed: null,
      count: 3,
      error: "",
      containsYou: false,
      packets: 0,
      bytes: 0,
    },
  ],
  presets: [
    {
      id: "spamhaus-drop",
      name: "Spamhaus DROP",
      url: "https://www.spamhaus.org/drop/drop.txt",
      description:
        "Networks Spamhaus has found hijacked or run by spammers and cybercriminals. A few hundred entries, very few false positives.",
    },
    {
      id: "firehol-level1",
      name: "FireHOL level 1",
      url: "https://iplists.firehol.org/files/firehol_level1.netset",
      description:
        "FireHOL's no-false-positives list, folding Spamhaus DROP, DShield and several botnet trackers into one.",
    },
  ],
  trusted: [
    { address: "127.0.0.0/8", origin: "loopback", removable: false },
    { address: "::1/128", origin: "loopback", removable: false },
    { address: "100.64.0.0/10", origin: "allowlist", removable: false },
    {
      address: "100.110.34.9",
      origin: "you",
      removable: false,
      reason: "Kept for the operator's address when a protection entry was saved from it.",
      addedBy: "operator",
      lastSeen: iso(2),
    },
    {
      address: "198.51.100.23",
      origin: "kept",
      removable: true,
      reason: "Old office",
      addedBy: "operator",
      addedAt: iso(60 * 24 * 80),
      lastSeen: null,
      stale: true,
    },
  ],
  client: "100.110.34.9",
  clientTrusted: true,
  settings,
  resetNote:
    "Resetting removes the setting from what the dashboard restores at boot. The running kernel keeps its current value until the next reboot.",
  conntrack: { available: true, count: 11_796, max: 65_536, percent: 18, level: "ok" },
  counters,
  exceptions: [
    {
      ...made,
      id: 7,
      address: "198.51.100.7/32",
      scope: "blocklist:3",
      scopeName: "FireHOL level 1",
      reason: "Partner monitoring",
      expiresAt: new Date(Date.now() + 6 * 3_600_000).toISOString(),
      expired: false,
      packets: 120,
      bytes: 7_200,
    },
  ],
  profiles: [
    {
      id: "ssh",
      name: "SSH",
      why: "A password guesser opens a new connection per attempt. Six a minute per address leaves an operator room to reconnect and slows guessing to a crawl.",
      protocol: "tcp",
      ports: "22",
      rate: 6,
      per: "minute",
      burst: 4,
      perSource: true,
      maxConnections: 10,
      globalConnections: 0,
      action: "drop",
    },
    {
      id: "database",
      name: "Exposed database",
      why: "An application holds a small pool of long connections.",
      protocol: "tcp",
      ports: "5432",
      rate: 30,
      per: "minute",
      burst: 10,
      perSource: true,
      maxConnections: 20,
      globalConnections: 200,
      action: "reject",
    },
  ],
  kernelProfiles: [
    {
      id: "gateway",
      name: "VPN or container gateway",
      why: "The recommendations, with a larger connection table and SYN backlog for the flows a gateway carries for others.",
      values: {
        "net.ipv4.tcp_syncookies": "1",
        "net.ipv4.conf.default.rp_filter": "2",
        "net.netfilter.nf_conntrack_max": "524288",
        "net.ipv4.tcp_max_syn_backlog": "8192",
      },
    },
  ],
  interfaces: {
    interfaces: [
      {
        name: "wg0",
        forwarding: true,
        values: [
          {
            key: "net.ipv4.conf.all.rp_filter",
            own: "1",
            all: "0",
            effective: "1",
            differs: true,
          },
        ],
      },
      {
        name: "ens3",
        forwarding: true,
        values: [
          {
            key: "net.ipv4.conf.all.rp_filter",
            own: "0",
            all: "0",
            effective: "0",
            differs: false,
          },
        ],
      },
    ],
    rules: [
      {
        key: "net.ipv4.conf.all.rp_filter",
        combine: "max",
        explain:
          "The kernel uses the higher of all and the device's own value, so a device at strict (1) stays strict even when all is loose.",
      },
    ],
    omitted: 0,
  },
}

const minute = (ago: number) => Math.floor(Date.now() / 60_000 - ago) * 60

/** `GET /protection/pressure`, an administrator's reading. */
export const pressure = {
  conntrack: protection.conntrack,
  stats: {
    cpus: 2,
    found: 10,
    invalid: 4,
    insert: 0,
    insertFailed: 0,
    drop: 12,
    earlyDrop: 3,
    error: 0,
    searchRestart: 1,
    clashResolve: 0,
    chainTooLong: 0,
  },
  breakdown: {
    read: 11_796,
    truncated: false,
    byProtocol: [
      { key: "tcp", count: 9_100, share: 0.77 },
      { key: "udp", count: 2_696, share: 0.23 },
    ],
    byState: [
      { key: "tcp SYN_RECV", count: 4_100, share: 0.35 },
      { key: "tcp ESTABLISHED", count: 3_900, share: 0.33 },
      { key: "udp UNREPLIED", count: 2_000, share: 0.17 },
    ],
    topSources: [
      { key: "198.51.100.77", count: 3_600, share: 0.31 },
      { key: "203.0.113.4", count: 420, share: 0.04 },
    ],
    topPorts: [
      { key: "443/tcp", count: 6_200, share: 0.53 },
      { key: "53/udp", count: 1_900, share: 0.16 },
    ],
    unreplied: 6_100,
    assured: 3_700,
  },
  series: {
    "conntrack:count": Array.from({ length: 30 }, (_, i) => ({
      t: minute(30 - i),
      value: 6_000 + i * 200,
    })),
    "conntrack:max": Array.from({ length: 30 }, (_, i) => ({ t: minute(30 - i), value: 65_536 })),
    "conntrack:drop": [{ t: minute(4), value: 12 }],
  },
  since: iso(60 * 24),
  causes: [
    {
      kind: "syn",
      severity: "warning",
      message:
        "Half-open TCP connections are a large share: the pattern of a SYN flood, or of many clients that never complete their handshake.",
      evidence: "4100 entries in SYN_RECV (35%)",
    },
    {
      kind: "source",
      severity: "warning",
      message:
        "One source holds a large share of the table: a single client flooding, or many clients behind one address.",
      evidence: "198.51.100.77 holds 3600 entries (31%)",
    },
  ],
  limits: [
    {
      id: 2,
      name: "Website",
      ports: "443",
      maxConnections: 200,
      globalConnections: 5_000,
      open: 4_020,
      topSources: [{ key: "198.51.100.77", count: 180, share: 0.04 }],
      refused: Array.from({ length: 12 }, (_, i) => ({ t: minute(12 - i), value: i % 3 })),
    },
  ],
  checkedAt: iso(0),
  basis:
    "Read from the kernel's connection table over netlink, bounded to the first entries returned. Causes are indications drawn from the shares below, not a diagnosis.",
}

/** Keyed by API path, for `mockNetwork`'s `overrides`. */
export const overrides: Record<string, unknown> = {
  "/network/gateway": gateway,
  "/network/protection": protection,
  "/network/protection/pressure": pressure,
}
