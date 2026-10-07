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
      packets: 184_320,
      bytes: 211_450_000,
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
      owner: "",
      enabled: true,
      packets: 14_882,
      bytes: 9_310_000,
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
      action: "reject",
      enabled: true,
      packets: 37,
      bytes: 2_220,
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
    { address: "100.110.34.9", origin: "you", removable: false },
    { address: "198.51.100.23", origin: "kept", removable: true },
  ],
  client: "100.110.34.9",
  clientTrusted: true,
  settings,
  resetNote:
    "Resetting removes the setting from what the dashboard restores at boot. The running kernel keeps its current value until the next reboot.",
  conntrack: { available: true, count: 11_796, max: 65_536, percent: 18, level: "ok" },
}

/** Keyed by API path, for `mockNetwork`'s `overrides`. */
export const overrides: Record<string, unknown> = {
  "/network/gateway": gateway,
  "/network/protection": protection,
}
