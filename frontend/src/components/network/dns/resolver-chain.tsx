"use client"

import { useMemo } from "react"
import { Globe, LockClosed, LockOpen, Servers, Terminal } from "@/components/icons"
import type { DNSView } from "@/lib/types"
import { cn } from "@/lib/utils"
import { ProductGlyph } from "@/components/product-logo"
import { WireHost, WireMark, WireNode } from "@/components/wire/wire"
import { WireFlow, type WireFlowHub, type WireFlowNode } from "@/components/wire/wire-flow"
import {
  MOST_UPSTREAMS,
  identify,
  port53Of,
  upstreamsOf,
  viaStub,
  type DNSShape,
  type Upstream,
} from "@/components/network/dns/reading"
import { Bar, GhostMark } from "@/components/network/dns/ghost"
import { splitServer, type Encryption } from "@/components/network/dns/resolvers"

/**
 * In a row of nodes only, where a node is about 120px wide with its words
 * centred under its mark: a flex line centred as the words are, or left out.
 */
const ROW_CENTRED = "lg:in-data-[form=rows]:justify-center"
const ROW_CUT = "lg:in-data-[form=rows]:hidden"

const POLICY: Record<Encryption, string> = {
  required: "DoT required",
  opportunistic: "opportunistic DoT",
  plain: "classic DNS",
}

/**
 * The configured DoT policy is separate from a fresh native transport
 * measurement. In a row of the chain's nodes the policy alone fits, so
 * "Configured" is left to the pointer there; the measurement is under the
 * picture, with the certificates.
 */
export function EncryptionNote({
  encryption,
  className,
}: {
  encryption: Encryption
  className?: string
}) {
  const Lock = encryption === "plain" ? LockOpen : LockClosed
  const policy = POLICY[encryption]
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1",
        encryption === "plain" && "text-warning",
        className,
      )}
      title={`Configured ${policy}`}
    >
      <Lock aria-hidden className="size-3 shrink-0" />
      <span>
        <span className={ROW_CUT}>Configured </span>
        {policy}
      </span>
    </span>
  )
}

/**
 * One line of a node's detail. What stands on it is set on the line's top
 * edge: a mono address or a lock beside words of the body face sat on another
 * baseline and made that line two pixels taller than its neighbours, so no
 * two nodes were one height.
 */
const LINE = "block truncate [&>*]:align-top"

/**
 * A title that is an address. A block, so the title's line is the mono face's
 * own: as a span inside the body face's line it stood on another baseline and
 * made the line two pixels taller than a named node's and than the bar it
 * waits as, so an upstream with no provider's name was taller than one with.
 */
const MONO_TITLE = "block font-mono"

/** A node not read yet, on the four lines an upstream's words stand on. */
function ghost(id: string): WireFlowNode {
  return {
    id,
    placeholder: true,
    mark: (size) => <GhostMark size={size} />,
    eyebrow: <Bar className="w-14" />,
    title: <Bar className="w-24" />,
    hint: (
      <>
        <span className="block">
          <Bar className="w-32" />
        </span>
        <span className="block">
          <Bar className="w-28" />
        </span>
      </>
    ),
    line: { state: "up" },
  }
}

/** Where an upstream with no provider's name comes from, in place of repeating its address. */
function origin(up: Upstream) {
  if (up.scope === "resolv.conf") return "listed in resolv.conf"
  if (up.scope === "global") return up.fallback ? "fallback server" : "global server"
  return `handed out on ${up.scope}`
}

/**
 * How a name gets resolved on this server, drawn as the chain it is: the
 * programs that ask on the left, the stub (or resolv.conf itself) they ask in
 * the middle with how much it answers from its cache and how many lookups it
 * has made, and on the right every upstream the stub can send a name to —
 * each as the provider it is, with whether the path to it is encrypted. The
 * wire into the stub carries while its lookup count rises, the wire to the
 * server in use carries because resolved says it is the one in use; the
 * others are still, and a fallback that only answers when the rest are down
 * is dashed.
 *
 * It is `WireFlow`'s shape (things, one hub, things), so it is drawn by it:
 * from `lg` the upstreams are rows on level lines where their side has room
 * for three across and a column where it has not, and below that the lanes
 * stack with every word kept and the wires not drawn. While the first read is
 * out it stands at the height it had last time (`shape`), on grey bars laid
 * on the lines the words will take, so nothing under it moves when the read
 * lands.
 *
 * Every upstream is four lines in every form. In a row a node is about 120px
 * wide, which holds an address but not all that stands after it: there the
 * eyebrow is the scope and "in use" with the routing domains behind the
 * pointer (the link's card under the picture says them), and a line that is
 * cut says all of itself behind the pointer too.
 */
export function ResolverChain({
  view,
  asking,
  shape,
}: {
  /** Undefined while the first read is out. */
  view?: DNSView
  asking: boolean
  shape: DNSShape
}) {
  const start = useMemo((): WireFlowNode[] => {
    const resolvConf = view?.resolvConf
    return [
      {
        id: "programs",
        mark: (size) => <WireHost size={size} />,
        eyebrow: "This server",
        title: "Programs",
        name: "Programs on this server",
        hint: resolvConf ? (
          <>
            <span className={LINE}>
              ask{" "}
              <span className="font-mono">
                {resolvConf.nameservers.slice(0, 2).join(", ") || "nothing"}
              </span>
            </span>
            {/* Held when there is no search domain, so the node is one height either way. */}
            <span className={LINE}>
              {resolvConf.search.length > 0 ? (
                <>
                  search <span className="font-mono">{resolvConf.search.join(" ")}</span>
                </>
              ) : (
                "\u00a0"
              )}
            </span>
          </>
        ) : (
          <>
            <span className="block">
              <Bar className="w-24" />
            </span>
            <span className="block">
              <Bar className="w-28" />
            </span>
          </>
        ),
        placeholder: !view,
        line: { state: "up", moving: asking, duration: 2.2 },
      },
    ]
  }, [view, asking])

  const end = useMemo((): WireFlowNode[] => {
    if (!view) return Array.from({ length: shape.upstreams }, (_, i) => ghost(`waiting:${i}`))
    return upstreamsOf(view).map((up) => {
      const identity = identify(up, view)
      const { address, tlsName } = splitServer(up.server)
      const scope = up.fallback ? "fallback" : up.scope
      const domains = up.domains.length > 0 ? ` · ${up.domains.slice(0, 2).join(" ")}` : ""
      const detail = identity.detail ? ` · ${identity.detail}` : ""
      return {
        id: up.id,
        mark: (size) => (
          <WireMark tone={up.current ? "logo" : "neutral"} shape="square" size={size}>
            {identity.product ? <ProductGlyph id={identity.product} /> : <Globe aria-hidden />}
          </WireMark>
        ),
        // One line: the scope gives way, "in use" is never the part cut.
        eyebrow: (
          <span
            className={cn("flex min-w-0", ROW_CENTRED, up.current && "text-brand")}
            title={`${scope}${domains}${up.current ? " · in use" : ""}`}
          >
            <span className="truncate">
              {scope}
              {domains && <span className={ROW_CUT}>{domains}</span>}
            </span>
            {up.current && <span className="shrink-0 whitespace-pre"> · in use</span>}
          </span>
        ),
        title: identity.bare ? <span className={MONO_TITLE}>{identity.name}</span> : identity.name,
        name: identity.name,
        hint: (
          <>
            <span className={LINE} title={!identity.bare || tlsName ? up.server : undefined}>
              {!identity.bare || tlsName ? (
                <span className="font-mono">
                  {address}
                  {tlsName && <span className="text-faint">#{tlsName}</span>}
                </span>
              ) : (
                origin(up)
              )}
            </span>
            <span
              className={LINE}
              title={`${identity.tunnel ? "inside the tailnet" : `Configured ${POLICY[up.encryption]}`}${detail}`}
            >
              {identity.tunnel ? (
                "inside the tailnet"
              ) : (
                <EncryptionNote encryption={up.encryption} />
              )}
              {detail}
            </span>
          </>
        ),
        line: { state: "up", moving: up.current, dashed: up.fallback, duration: 2.6 },
      }
    })
  }, [view, shape.upstreams])

  return (
    <div data-slot="dns-chain" data-loading={view ? undefined : ""} aria-busy={!view || undefined}>
      <WireFlow
        start={{ title: "Programs on this server", nodes: start }}
        hub={hubOf(view)}
        end={{
          title: "Upstream servers",
          nodes: end,
          max: MOST_UPSTREAMS,
          more: { words: (hidden) => `and ${hidden} more upstreams` },
          empty: "No upstream is configured, so nothing here can answer a lookup.",
        }}
      />
    </div>
  )
}

/**
 * The resolver's node. The share it answers from its cache and the lookups it
 * has made stood in two of four tiles under the picture; they are the
 * resolver's own readings, so they are said on it. Resolved counts since it
 * started, so both are totals, and a host whose resolver is not
 * systemd-resolved has neither: the node says what programs do instead.
 */
function hubOf(view: DNSView | undefined): WireFlowHub {
  // Room around the mark in the column form, where the lanes are centred on
  // it and make none themselves with one upstream or none. Rows stand on the
  // hub's own level and `WireFlow` leaves this off there.
  const className = "lg:py-20"
  if (!view) {
    return {
      className,
      mark: <GhostMark size="lg" />,
      eyebrow: <Bar className="w-20" />,
      title: <Bar className="w-24" />,
      hint: (
        <>
          <span className="block">
            <Bar className="w-24" />
          </span>
          <span className="block">
            <Bar className="w-20" />
          </span>
        </>
      ),
    }
  }
  const { resolvConf, resolved } = view
  const stubbed = viaStub(view)
  const stats = resolved.statistics
  return {
    className,
    mark: (
      <WireMark tone="logo" shape="square">
        {stubbed ? <ProductGlyph id="systemd" /> : <Terminal aria-hidden />}
      </WireMark>
    ),
    eyebrow: stubbed ? "systemd-resolved" : resolvConf.path,
    title: (
      <span className={MONO_TITLE}>
        {stubbed
          ? (resolvConf.nameservers[0] ?? "127.0.0.53")
          : resolvConf.nameservers.length > 0
            ? "straight to the servers"
            : "no nameserver"}
      </span>
    ),
    hint: stubbed ? (
      stats ? (
        <span data-slot="dns-resolver-readings" className="numeric">
          <span className="block">{Math.round(stats.hitPercent)}% from cache</span>
          <span className="block">{stats.transactions.toLocaleString()} lookups</span>
        </span>
      ) : (
        <>
          <span className="block">stub resolver</span>
          <span className="block">{resolved.statisticsError ?? "no resolver statistics"}</span>
        </>
      )
    ) : (
      <span className={cn("block", !resolved.active && "text-warning")}>
        {resolved.active
          ? "resolved runs, but programs skip it"
          : (resolvConf.managedBy ?? resolvConf.mode)}
      </span>
    ),
  }
}

/**
 * What listens on port 53 of this host, each as the program it is: the stub,
 * and any DNS server or ad-blocker other machines and containers can ask.
 * They are not wired to the chain — whether this host sends its own lookups
 * to one is decided by the upstreams above, where the same address shows as
 * its mark. An ad-blocker the page found takes its own mark and says so; one
 * that is installed but not answering is drawn with what is missing.
 *
 * Every node stands on four lines, so a grid of them is level whichever of
 * them is an ad-blocker.
 */
export function Port53({ view, shape }: { view?: DNSView; shape: DNSShape }) {
  const nodes = useMemo(() => (view ? port53Of(view) : undefined), [view])
  const grid = "grid gap-x-8 gap-y-5 sm:grid-cols-2 xl:grid-cols-3"
  if (!nodes) {
    return (
      <ul aria-hidden className={grid}>
        {Array.from({ length: Math.max(shape.port53, 1) }, (_, i) => (
          <li key={i} className="min-w-0">
            <WireNode
              mark={<GhostMark size="md" />}
              eyebrow={<Bar className="w-28" />}
              title={<Bar className="w-24" />}
              hint={
                <>
                  <span className="block">
                    <Bar className="w-32" />
                  </span>
                  <span className="block">
                    <Bar className="w-16" />
                  </span>
                </>
              }
            />
          </li>
        ))}
      </ul>
    )
  }
  if (nodes.length === 0) {
    return (
      <p className="text-body text-muted-foreground">
        Nothing is listening on port 53: this server answers no one else&rsquo;s lookups.
      </p>
    )
  }
  return (
    <ul className={grid}>
      {nodes.map((node) => (
        <li key={node.id} className="min-w-0">
          <WireNode
            mark={
              <WireMark tone={node.silent ? "neutral" : "logo"} shape="square" size="md">
                {node.product ? <ProductGlyph id={node.product} /> : <Servers aria-hidden />}
              </WireMark>
            }
            eyebrow={
              <span className="align-top font-mono normal-case">
                {node.address
                  ? `${node.address}:53 · ${node.protocols.join(" ")}`
                  : "not on port 53"}
              </span>
            }
            title={node.title}
            hint={
              <>
                <span className={LINE}>{node.detail}</span>
                <span className={LINE}>
                  {node.blocker && (node.silent ? "ad-blocker, not answering" : "ad-blocker")}
                  {node.open && (
                    <span className="text-warning">{node.blocker ? " · " : ""}every interface</span>
                  )}
                  {!node.blocker && !node.open && (node.loopback ? "loopback only" : "one address")}
                </span>
              </>
            }
          />
        </li>
      ))}
    </ul>
  )
}
