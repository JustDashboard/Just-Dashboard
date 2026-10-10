"use client"

import { createRef, useMemo, useRef, useState, type RefObject } from "react"
import { Globe, LockClosed, LockOpen, Servers, Terminal } from "@/components/icons"
import type { DNSView } from "@/lib/types"
import { cn } from "@/lib/utils"
import { ProductGlyph } from "@/components/product-logo"
import { WireHost, WireMark, WireNode } from "@/components/deploy/wire"
import { AnimatedBeam } from "@/components/ui/animated-beam"
import {
  KIND_NAME,
  KIND_PRODUCT,
  encryptionOf,
  presetFor,
  sameServer,
  splitServer,
  type Encryption,
} from "@/components/network/dns/resolvers"

/** Past this many upstreams the picture says how many more there are rather than draw them. */
const MOST_UPSTREAMS = 8

type Upstream = {
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

/** Programs reach resolved's stub, not the servers, when resolv.conf is its link and resolved is running. */
export function viaStub(view: DNSView) {
  return view.resolved.active && view.resolvConf.mode === "stub"
}

type Identity = {
  product?: string
  name: React.ReactNode
  detail?: string
  /** The name is the address itself, so the line under it need not say it twice. */
  bare?: boolean
  /** The path to it is already a tunnel's, so plain DNS on it is not readable from outside. */
  tunnel?: boolean
}

/**
 * Who an upstream is, by the address alone: a public resolver the presets
 * name, a listener on this host (an ad-blocker's container, say), Tailscale's
 * MagicDNS on its fixed address — and otherwise just the address, drawn as a
 * globe, because naming a provider from a guess would be the picture lying.
 */
function identify(up: Upstream, view: DNSView): Identity {
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
    }
  }
  if (address === "100.100.100.100" || up.scope.startsWith("tailscale")) {
    return { product: "tailscale", name: "MagicDNS", tunnel: true }
  }
  return { name: <span className="font-mono">{address}</span>, bare: true }
}

/** The configured DoT policy is separate from a fresh native transport measurement. */
export function EncryptionNote({
  encryption,
  className,
}: {
  encryption: Encryption
  className?: string
}) {
  const Lock = encryption === "plain" ? LockOpen : LockClosed
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1",
        encryption === "plain" && "text-warning",
        className,
      )}
    >
      <Lock aria-hidden className="size-3 shrink-0" />
      {encryption === "required"
        ? "Configured DoT required"
        : encryption === "opportunistic"
          ? "Configured opportunistic DoT"
          : "Configured classic DNS"}
    </span>
  )
}

/** Port 53 is also listened on by things that are not in the chain: a DNS filter other machines ask. */
function isOpenAddress(address: string) {
  return address === "0.0.0.0" || address === "::" || address === "*"
}

/**
 * How a name gets resolved on this server, drawn as the chain it is: the
 * programs that ask on the left, the stub (or resolv.conf itself) they ask in
 * the middle, and on the right every upstream the stub can send it to — each
 * as the provider it is, with whether the path to it is encrypted. The wire
 * to the server in use carries a pulse; the others are still, and a fallback
 * that only answers when the rest are down is dashed.
 *
 * Under the chain, what answers on port 53 of this host: the stub, and any DNS
 * server or ad-blocker other machines and containers can ask. They are not
 * wired to the chain — whether this host sends its own lookups to one is
 * decided by the upstreams above, where the same address shows as its mark.
 *
 * From `lg` the lanes sit side by side with wires; narrower, they stack, the
 * wires are not drawn and each node says it in words.
 */
export function ResolverChain({ view, asking }: { view: DNSView; asking: boolean }) {
  const container = useRef<HTMLDivElement>(null)
  const programs = useRef<HTMLDivElement>(null)
  const stub = useRef<HTMLDivElement>(null)
  const [focus, setFocus] = useState<string | null>(null)

  const { shown, hidden, ids } = useMemo(() => {
    const all = upstreamsOf(view)
    const shown = all.slice(0, MOST_UPSTREAMS)
    return { shown, hidden: all.length - shown.length, ids: shown.map((u) => u.id).join("\n") }
  }, [view])
  const refs = useMemo(() => {
    const map = new Map<string, RefObject<HTMLDivElement | null>>()
    for (const id of ids.split("\n")) if (id) map.set(id, createRef<HTMLDivElement>())
    return map
  }, [ids])

  const stubbed = viaStub(view)
  const { resolvConf, resolved } = view
  const stats = resolved.statistics
  const port = 26
  const watch = (id: string) => ({
    onPointerEnter: () => setFocus(id),
    onPointerLeave: () => setFocus((held) => (held === id ? null : held)),
    onFocus: () => setFocus(id),
    onBlur: () => setFocus((held) => (held === id ? null : held)),
  })

  return (
    <div className="relative animate-rise py-4">
      <div aria-hidden className="wire-grid pointer-events-none absolute -inset-x-4 inset-y-0" />
      <div className="relative">
        <div ref={container} className="relative">
          <AnimatedBeam
            containerRef={container}
            fromRef={programs}
            toRef={stub}
            shape="s"
            startXOffset={port}
            endXOffset={-port}
            still={!asking}
            duration={2.2}
            className="max-lg:hidden"
          />
          {shown.map((up, index) => {
            const ref = refs.get(up.id)
            if (!ref) return null
            const lit = focus === null || focus === up.id
            return (
              <AnimatedBeam
                key={up.id}
                containerRef={container}
                fromRef={stub}
                toRef={ref}
                shape="s"
                startXOffset={port}
                endXOffset={-port}
                still={!up.current}
                dashed={up.fallback}
                duration={2.6}
                delay={(index % 4) * 0.3}
                className={cn("transition-opacity max-lg:hidden", !lit && "opacity-15")}
              />
            )
          })}

          <div className="relative grid gap-y-8 lg:grid-cols-[minmax(0,0.9fr)_clamp(2.5rem,6vw,6rem)_minmax(0,0.9fr)_clamp(2.5rem,6vw,6rem)_minmax(0,1.2fr)] lg:items-center">
            <section aria-label="Programs on this server" className="min-w-0">
              <WireNode
                nodeRef={programs}
                align="end"
                mark={<WireHost />}
                eyebrow="This server"
                title="Programs"
                hint={
                  <>
                    <span className="block truncate">
                      ask{" "}
                      <span className="font-mono">
                        {resolvConf.nameservers.slice(0, 2).join(", ") || "nothing"}
                      </span>
                    </span>
                    {resolvConf.search.length > 0 && (
                      <span className="block truncate">
                        search <span className="font-mono">{resolvConf.search.join(" ")}</span>
                      </span>
                    )}
                  </>
                }
              />
            </section>

            <div aria-hidden className="max-lg:hidden" />

            <section aria-label="Resolver" className="flex min-w-0 lg:justify-center">
              <WireNode
                nodeRef={stub}
                align="center"
                mark={
                  <WireMark tone="logo" shape="square">
                    {stubbed ? <Servers aria-hidden /> : <Terminal aria-hidden />}
                  </WireMark>
                }
                eyebrow={stubbed ? "systemd-resolved" : resolvConf.path}
                title={
                  <span className="font-mono">
                    {stubbed
                      ? (resolvConf.nameservers[0] ?? "127.0.0.53")
                      : resolvConf.nameservers.length > 0
                        ? "straight to the servers"
                        : "no nameserver"}
                  </span>
                }
                hint={
                  stubbed ? (
                    stats ? (
                      <span className="numeric block">
                        {stats.cacheSize.toLocaleString()} cached answers
                      </span>
                    ) : (
                      <span className="block">stub resolver</span>
                    )
                  ) : (
                    <span className={cn("block", !resolved.active && "text-warning")}>
                      {resolved.active
                        ? "resolved runs, but programs skip it"
                        : (resolvConf.managedBy ?? resolvConf.mode)}
                    </span>
                  )
                }
              />
            </section>

            <div aria-hidden className="max-lg:hidden" />

            <section aria-label="Upstream servers" className="min-w-0">
              <p className="eyebrow mb-4 lg:hidden">Upstreams</p>
              {shown.length === 0 ? (
                <p className="text-body text-muted-foreground">
                  No upstream is configured, so nothing here can answer a lookup.
                </p>
              ) : (
                <ol className="flex flex-col gap-5">
                  {shown.map((up) => (
                    <UpstreamItem
                      key={up.id}
                      up={up}
                      identity={identify(up, view)}
                      nodeRef={refs.get(up.id)}
                      dim={focus !== null && focus !== up.id}
                      {...watch(up.id)}
                    />
                  ))}
                  {hidden > 0 && (
                    <li className="text-hint text-muted-foreground">
                      and {hidden} more upstream{hidden === 1 ? "" : "s"}
                    </li>
                  )}
                </ol>
              )}
            </section>
          </div>
        </div>

        <Listeners view={view} />
      </div>
    </div>
  )
}

function UpstreamItem({
  up,
  identity,
  nodeRef,
  dim,
  ...handlers
}: {
  up: Upstream
  identity: Identity
  nodeRef?: RefObject<HTMLDivElement | null>
  dim: boolean
} & React.HTMLAttributes<HTMLLIElement>) {
  const { address, tlsName } = splitServer(up.server)
  return (
    <li {...handlers} className={cn("min-w-0 transition-opacity", dim && "opacity-40")}>
      <WireNode
        nodeRef={nodeRef}
        mark={
          <WireMark tone={up.current ? "logo" : "neutral"} shape="square" size="md">
            {identity.product ? <ProductGlyph id={identity.product} /> : <Globe aria-hidden />}
          </WireMark>
        }
        eyebrow={
          <span className={cn(up.current && "text-brand")}>
            {up.fallback ? "fallback" : up.scope}
            {up.domains.length > 0 && ` · ${up.domains.slice(0, 2).join(" ")}`}
            {up.current ? " · in use" : ""}
          </span>
        }
        title={identity.name}
        hint={
          <>
            {(!identity.bare || tlsName) && (
              <span className="block truncate font-mono">
                {address}
                {tlsName && <span className="text-muted-foreground/70">#{tlsName}</span>}
              </span>
            )}
            <span className="block truncate">
              {identity.tunnel ? (
                "inside the tailnet"
              ) : (
                <EncryptionNote encryption={up.encryption} />
              )}
              {identity.detail && ` · ${identity.detail}`}
            </span>
          </>
        }
      />
    </li>
  )
}

/**
 * What listens on port 53 of this host, each as the program it is. An
 * ad-blocker the page found (AdGuard Home, Pi-hole) takes its own mark and
 * says so; one that is installed but not answering is drawn with what is
 * missing, because "installed" is not a filter.
 */
function Listeners({ view }: { view: DNSView }) {
  const nodes = useMemo(() => {
    const adblockers = [...view.adblock]
    // One socket per protocol is listed; a program that holds both is one
    // thing answering, so they are folded into a line of protocols.
    const sockets = new Map<
      string,
      { listener: DNSView["listeners"][number]; protocols: string[] }
    >()
    for (const l of view.listeners) {
      const key = `${l.address}|${l.kind}|${l.container ?? l.pid ?? l.process ?? ""}`
      const held = sockets.get(key)
      if (held) held.protocols.push(l.protocol)
      else sockets.set(key, { listener: l, protocols: [l.protocol] })
    }
    const out = [...sockets.entries()].map(([key, { listener: l, protocols }]) => {
      const match = adblockers.findIndex((a) => a.kind === l.kind)
      const blocker = match >= 0 ? adblockers.splice(match, 1)[0] : undefined
      return {
        id: key,
        product: KIND_PRODUCT[l.kind],
        eyebrow: `${l.address}:53 · ${protocols.join(" ")}`,
        title: blocker?.name ?? (l.container ? l.container : KIND_NAME[l.kind]),
        blocker: Boolean(blocker),
        detail: l.container
          ? `container${l.process ? ` · ${l.process}` : ""}`
          : (l.process ?? "unknown process") + (l.pid ? ` · pid ${l.pid}` : ""),
        open: isOpenAddress(l.address),
        silent: false,
      }
    })
    for (const a of adblockers) {
      out.push({
        id: `adblock:${a.kind}:${a.container ?? a.name}`,
        product: KIND_PRODUCT[a.kind],
        eyebrow: "not on port 53",
        title: a.name,
        blocker: true,
        detail: a.runsAs === "container" ? `container ${a.container ?? ""}`.trim() : "process",
        open: false,
        silent: !a.answering,
      })
    }
    return out
  }, [view.listeners, view.adblock])

  if (nodes.length === 0) {
    return (
      <p className="relative mt-8 text-body text-muted-foreground">
        Nothing is listening on port 53: this server answers no one else&rsquo;s lookups.
      </p>
    )
  }
  return (
    <section aria-label="Answering on port 53" className="relative mt-10">
      <p className="eyebrow mb-4">Answering on port 53</p>
      <ul className="grid gap-x-8 gap-y-5 sm:grid-cols-2 xl:grid-cols-3">
        {nodes.map((node) => (
          <li key={node.id} className="min-w-0">
            <WireNode
              mark={
                <WireMark tone={node.silent ? "neutral" : "logo"} shape="square" size="md">
                  {node.product ? <ProductGlyph id={node.product} /> : <Servers aria-hidden />}
                </WireMark>
              }
              eyebrow={<span className="font-mono normal-case">{node.eyebrow}</span>}
              title={node.title}
              hint={
                <>
                  <span className="block truncate">{node.detail}</span>
                  {(node.blocker || node.open || node.silent) && (
                    <span className="block truncate">
                      {node.blocker && (node.silent ? "ad-blocker, not answering" : "ad-blocker")}
                      {node.open && (
                        <span className="text-warning">
                          {node.blocker ? " · " : ""}every interface
                        </span>
                      )}
                    </span>
                  )}
                </>
              }
            />
          </li>
        ))}
      </ul>
    </section>
  )
}
