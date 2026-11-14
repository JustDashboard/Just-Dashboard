import type { DNSView } from "@/lib/types"
import {
  KIND_NAME,
  KIND_PRODUCT,
  encryptionOf,
  presetFor,
  sameServer,
  splitServer,
  type Encryption,
} from "@/components/network/dns/resolvers"

/**
 * What the DNS page says about the resolver, worked out once: the upstreams a
 * lookup can end at, who each is, what answers on port 53, and the identity
 * line's words. Nothing here draws, so the picture, the identity line and the
 * tests read one answer.
 */

export type Upstream = {
  id: string
  server: string
  /** Which scope handed it out: the global configuration, a link, or resolv.conf itself. */
  scope: string
  /** The routing domains of a link's scope — `~ts.net` — which say what the link is the answer for. */
  domains: string[]
  encryption: Encryption
  /** Resolved is using this one now, so its wire is the one that moves. */
  current: boolean
  fallback: boolean
}

/** Programs reach resolved's stub, not the servers, when resolv.conf is its link and resolved is running. */
export function viaStub(view: DNSView) {
  return view.resolved.active && view.resolvConf.mode === "stub"
}

/**
 * Every server a lookup can end up at, from whichever scope names it. With the
 * stub in front, that is resolved's own picture — the global servers, each
 * link's, and the fallback that only answers when the rest do not. Without it
 * programs read resolv.conf themselves, so its nameservers are the upstreams
 * and they are always plain: no program speaks TLS to a nameserver line.
 */
export function upstreamsOf(view: DNSView): Upstream[] {
  const { resolved, resolvConf } = view
  if (!viaStub(view)) {
    return resolvConf.nameservers.map((server, index) => ({
      id: `rc:${server}`,
      server,
      scope: "resolv.conf",
      domains: [],
      encryption: "plain" as const,
      current: index === 0,
      fallback: false,
    }))
  }
  const out: Upstream[] = []
  const global = resolved.global
  const globalEncryption = encryptionOf(global.dnsOverTLS)
  for (const server of global.servers) {
    out.push({
      id: `g:${server}`,
      server,
      scope: "global",
      domains: global.domains,
      encryption: globalEncryption,
      current: sameServer(global.currentServer, server),
      fallback: false,
    })
  }
  for (const link of resolved.links) {
    for (const server of link.servers) {
      out.push({
        id: `l:${link.name}:${server}`,
        server,
        scope: link.name,
        domains: link.domains.filter((d) => d.startsWith("~") && !d.endsWith(".arpa")),
        encryption: encryptionOf(link.dnsOverTLS),
        current: sameServer(link.currentServer, server),
        fallback: false,
      })
    }
  }
  for (const server of global.fallbackServers) {
    out.push({
      id: `f:${server}`,
      server,
      scope: "global",
      domains: [],
      encryption: globalEncryption,
      current: false,
      fallback: true,
    })
  }
  return out
}

export type UpstreamIdentity = {
  product?: string
  name: string
  detail?: string
  /** The name is the address itself, so the line under it need not say it twice. */
  bare?: boolean
  /** The path to it is already a tunnel's, so plain DNS on it is not readable from outside. */
  tunnel?: boolean
  /** It is a program listening on this server, so a question to it does not leave the host. */
  local?: boolean
}

/**
 * Who an upstream is, by the address alone: a public resolver the presets
 * name, a listener on this host (an ad-blocker's container, say), Tailscale's
 * MagicDNS on its fixed address — and otherwise just the address, drawn as a
 * globe, because naming a provider from a guess would be the picture lying.
 */
export function identify(up: Upstream, view: DNSView): UpstreamIdentity {
  const { address } = splitServer(up.server)
  const preset = presetFor(up.server, view.presets)
  if (preset) {
    return {
      product: preset.product,
      name: preset.name.split(",")[0],
      detail: preset.blocksAds ? "blocks ads" : preset.blocksMalware ? "blocks malware" : undefined,
    }
  }
  const listener = view.listeners.find((l) => l.address === address)
  if (listener) {
    const adblocker = view.adblock.find((a) => a.kind === listener.kind)
    return {
      product: KIND_PRODUCT[listener.kind],
      name: adblocker?.name ?? KIND_NAME[listener.kind],
      detail: adblocker ? "blocks ads" : undefined,
      local: true,
    }
  }
  if (address === "100.100.100.100" || up.scope.startsWith("tailscale")) {
    return { product: "tailscale", name: "MagicDNS", tunnel: true }
  }
  return { name: address, bare: true }
}

export type Port53 = {
  id: string
  product?: string
  address?: string
  protocols: string[]
  title: string
  /** An ad-blocker the page found, whether or not it is on the port. */
  blocker: boolean
  detail: string
  /** It answers on every interface, so anyone who reaches the host can ask it. */
  open: boolean
  /** It answers on a loopback address, so only this host can ask it. */
  loopback: boolean
  /** Installed and not answering: "installed" is not a filter. */
  silent: boolean
}

/** Port 53 is also listened on by things that are not in the chain: a DNS filter other machines ask. */
function isOpenAddress(address: string) {
  return address === "0.0.0.0" || address === "::" || address === "*"
}

/**
 * What listens on port 53 of this host, each as the program it is. One socket
 * per protocol is listed; a program that holds both is one thing answering,
 * so they are folded into a line of protocols. An ad-blocker that is installed
 * but holds no socket follows, with what is missing.
 */
export function port53Of(view: DNSView): Port53[] {
  const adblockers = [...view.adblock]
  const sockets = new Map<string, { listener: DNSView["listeners"][number]; protocols: string[] }>()
  for (const l of view.listeners) {
    const key = `${l.address}|${l.kind}|${l.container ?? l.pid ?? l.process ?? ""}`
    const held = sockets.get(key)
    if (held) held.protocols.push(l.protocol)
    else sockets.set(key, { listener: l, protocols: [l.protocol] })
  }
  const out: Port53[] = [...sockets.entries()].map(([key, { listener: l, protocols }]) => {
    const match = adblockers.findIndex((a) => a.kind === l.kind)
    const blocker = match >= 0 ? adblockers.splice(match, 1)[0] : undefined
    return {
      id: key,
      product: KIND_PRODUCT[l.kind],
      address: l.address,
      protocols,
      title: blocker?.name ?? (l.container ? l.container : KIND_NAME[l.kind]),
      blocker: Boolean(blocker),
      detail: l.container
        ? `container${l.process ? ` · ${l.process}` : ""}`
        : (l.process ?? "unknown process") + (l.pid ? ` · pid ${l.pid}` : ""),
      open: isOpenAddress(l.address),
      loopback: l.loopback,
      silent: false,
    }
  })
  for (const a of adblockers) {
    out.push({
      id: `adblock:${a.kind}:${a.container ?? a.name}`,
      product: KIND_PRODUCT[a.kind],
      protocols: [],
      title: a.name,
      blocker: true,
      detail: a.runsAs === "container" ? `container ${a.container ?? ""}`.trim() : "process",
      open: false,
      loopback: false,
      silent: !a.answering,
    })
  }
  return out
}

export type ResolverIdentity = {
  /** What programs ask: the stub by its name, or the file they read themselves. */
  name: string
  /** The name is a path, drawn in mono. */
  path: boolean
  product?: string
  /** The address programs send their questions to. */
  address?: string
  state: { tone: "running" | "notice" | "warning"; label: string }
}

/**
 * The thing the page is about: systemd-resolved where programs ask its stub,
 * and otherwise the file they read for themselves, with the reason said as
 * its state rather than left to a figure.
 */
export function resolverOf(view: DNSView): ResolverIdentity {
  const { resolved, resolvConf } = view
  const address = resolvConf.nameservers[0]
  if (viaStub(view)) {
    return {
      name: "systemd-resolved",
      path: false,
      product: "systemd",
      address: address ?? "127.0.0.53",
      state: { tone: "running", label: "Answering" },
    }
  }
  return {
    name: resolvConf.path,
    path: true,
    address,
    state: !address
      ? { tone: "warning", label: "No nameserver" }
      : resolved.installed && !resolved.active
        ? { tone: "warning", label: "systemd-resolved is not running" }
        : resolved.active
          ? { tone: "notice", label: "Programs skip systemd-resolved" }
          : { tone: "notice", label: "No local resolver" },
  }
}

const TRANSPORT: Record<Encryption, string> = {
  required: "over TLS",
  opportunistic: "over opportunistic TLS",
  plain: "unencrypted",
}

function namesOf(names: string[]) {
  const unique = [...new Set(names)]
  if (unique.length <= 2) return unique.join(" and ")
  return `${unique[0]} and ${unique.length - 1} more`
}

/**
 * Where a lookup goes, as the identity line says it: "Cloudflare over
 * opportunistic TLS". The global servers are the default route; a host with
 * none leans on each link's, and the line says that rather than name one.
 */
export function upstreamWords(view: DNSView): { words: string; product?: string } {
  const all = upstreamsOf(view).filter((up) => !up.fallback)
  if (all.length === 0) return { words: "no upstream" }
  const global = all.filter((up) => up.scope === "global" || up.scope === "resolv.conf")
  const chosen = global.length > 0 ? global : all
  const identities = chosen.map((up) => identify(up, view))
  // Without the stub the address programs ask is the upstream, and the line
  // has said it already: what is left to say is that nothing stands between.
  if (!viaStub(view) && identities.every((i) => i.bare)) {
    return {
      words:
        chosen.length > 1
          ? `asked directly with ${chosen.length - 1} more, unencrypted`
          : "asked directly, unencrypted",
    }
  }
  // A resolver on this host is asked over loopback: "unencrypted" would warn
  // of a path nobody else is on.
  if (identities.every((i) => i.local)) {
    const products = [...new Set(identities.map((i) => i.product))]
    return {
      words: `${namesOf(identities.map((i) => i.name))} on this server`,
      product: products.length === 1 ? products[0] : undefined,
    }
  }
  const products = [...new Set(identities.map((i) => i.product))]
  const transports = [...new Set(chosen.map((up) => up.encryption))]
  const transport = identities.every((i) => i.tunnel)
    ? "inside the tailnet"
    : transports.length === 1
      ? TRANSPORT[transports[0]]
      : "partly encrypted"
  return {
    words: `${namesOf(identities.map((i) => i.name))} ${transport}${
      global.length === 0 ? ", per link" : ""
    }`,
    product: products.length === 1 ? products[0] : undefined,
  }
}

type Statistics = NonNullable<DNSView["resolved"]["statistics"]>

/** More than one lookup in fifty failing is worth the amber. */
export function failing(stats: Statistics) {
  return stats.failures > stats.transactions * 0.02
}

/** The lookups reading: what went wrong first, and nothing wrong said plainly. */
export function lookupWords(stats: Statistics) {
  const total = stats.transactions.toLocaleString()
  if (stats.failures === 0 && stats.timeouts === 0) return `None of ${total} failed`
  const parts = [
    stats.failures > 0 && `${stats.failures.toLocaleString()} failed`,
    stats.timeouts > 0 && `${stats.timeouts.toLocaleString()} timed out`,
  ].filter(Boolean)
  return `${parts.join(", ")} of ${total}`
}

/** The DNSSEC reading: a bogus answer leads, because it is the one that was refused. */
export function dnssecWords(stats: Statistics) {
  if (stats.dnssecBogus > 0) return `${stats.dnssecBogus.toLocaleString()} bogus`
  if (stats.dnssecSecure === 0) return "No verdicts"
  return `${stats.dnssecSecure.toLocaleString()} secure, none bogus`
}

/** How many nodes each block draws, remembered so the next visit waits at that height. */
export type DNSShape = { upstreams: number; port53: number; links: number; readings: boolean }

export const SHAPE_GUESS: DNSShape = { upstreams: 3, port53: 2, links: 1, readings: true }

/** Past this many upstreams the picture says how many more there are rather than draw them. */
export const MOST_UPSTREAMS = 8

export function shapeOf(view: DNSView): DNSShape {
  const upstreams = upstreamsOf(view).length
  return {
    // What `fitColumn` draws: the fitted nodes and one tile for the rest.
    upstreams: upstreams > MOST_UPSTREAMS + 1 ? MOST_UPSTREAMS + 1 : upstreams,
    port53: port53Of(view).length,
    links: view.resolved.links.length,
    readings: view.resolved.active,
  }
}
