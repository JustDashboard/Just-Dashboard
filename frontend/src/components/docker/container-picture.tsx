"use client"

import { createRef, useMemo, useRef, useState, type RefObject } from "react"
import {
  Box,
  Cpu,
  FolderClosed,
  Globe,
  LockClosed,
  NetworkDevice,
  Servers,
  Warning,
  type Icon,
} from "@/components/icons"
import { bytes, percent } from "@/lib/format"
import type { ContainerDetail, PortRoute } from "@/lib/types"
import { cn } from "@/lib/utils"
import { ProductGlyph, containerProduct, hasProductLogo } from "@/components/product-logo"
import { StatusDot } from "@/components/status-dot"
import { AnimatedBeam } from "@/components/ui/animated-beam"
import { WireMark, WireNode, WirePlaceholder } from "@/components/deploy/wire"
import { looksLikeDatabase } from "@/components/docker/shared"
import { portRows, reachWords, type Verdict } from "@/components/docker/container"

/** Below this a wire is still: a kilobyte a second is a health check, not traffic. */
const CARRYING = 1024

type Tone = "default" | "warning" | "danger"

type Node = {
  id: string
  mark: React.ReactNode
  eyebrow: string
  title: React.ReactNode
  hint?: React.ReactNode
  /** The line to the container, in the wiring vocabulary's states. */
  wire: { tone: Tone; dashed: boolean; carries: boolean }
}

/**
 * How the container is reached and what it keeps, drawn as the runtime map
 * draws a release: the ways in on the left — each published port as where it
 * is reached from, and each network it is joined to — the container in the
 * middle as the product it runs, and on the right every volume, folder and
 * piece of memory mounted into it.
 *
 * The line is the state, in the vocabulary every picture in the product
 * speaks (`deploy/wire`): amber where a port answers the internet around the
 * firewall, red where Docker's own socket is mounted, dashed where a thing
 * does not outlive the container or the container is not running. A way in
 * pulses while the container moves more than a kilobyte a second; Docker
 * counts traffic for the container rather than per port, so the pulse says
 * traffic is flowing in, not through which door.
 *
 * It was three lists in three formats on the Overview tab — tags for ports,
 * rows for networks, a separate "Reachable at" list — and the Storage tab for
 * the mounts, so "what is this connected to" was four places. Three lanes
 * from `lg`; narrower they stack, the wires are not drawn, and each node says
 * in words what it is.
 */
export function ContainerPicture({
  detail,
  verdict,
  routes,
  traffic,
  readings,
}: {
  detail: ContainerDetail
  verdict: Verdict
  routes?: PortRoute[]
  /** Bytes a second in and out, from the live frames; undefined until two have arrived. */
  traffic?: number
  /** The container's live readings in words, for the caption under its mark. */
  readings?: { cpu?: number; memory?: number }
}) {
  const container = useRef<HTMLDivElement>(null)
  const [focus, setFocus] = useState<string | null>(null)
  const running = detail.state === "running"
  const flowing = running && (traffic ?? 0) > CARRYING

  const ways = useMemo(
    () => waysIn(detail, routes, running, flowing),
    [detail, routes, running, flowing],
  )
  const keeps = useMemo(() => storesOf(detail, running), [detail, running])

  const ids = ["c", ...ways.map((node) => node.id), ...keeps.map((node) => node.id)].join("\n")
  const refs = useMemo(() => {
    const map = new Map<string, RefObject<HTMLSpanElement | null>>()
    for (const id of ids.split("\n")) map.set(id, createRef<HTMLSpanElement>())
    return map
  }, [ids])
  const center = refs.get("c")!

  const watch = (id: string) => ({
    onPointerEnter: () => setFocus(id),
    onPointerLeave: () => setFocus((held) => (held === id ? null : held)),
  })
  const dim = (id: string) => focus !== null && focus !== id && focus !== "c"
  // A wire meets a mark at its edge, not under its logo: half of `md`, and a breath.
  const port = 26
  const product = containerProduct(detail)

  return (
    <div
      role="group"
      aria-label="How it is reached and what it keeps"
      className="relative animate-rise py-4"
    >
      <div aria-hidden className="wire-grid pointer-events-none absolute -inset-x-4 inset-y-0" />
      <div ref={container} className="relative">
        {ways.map((node, index) => (
          <AnimatedBeam
            key={`${node.id}→c`}
            containerRef={container}
            fromRef={refs.get(node.id)!}
            toRef={center}
            shape="s"
            startXOffset={port}
            endXOffset={-port - 4}
            still={!node.wire.carries}
            dashed={node.wire.dashed}
            tone={node.wire.tone}
            duration={2.4}
            delay={(index % 6) * 0.35}
            className={cn("transition-opacity max-lg:hidden", dim(node.id) && "opacity-15")}
          />
        ))}
        {keeps.map((node, index) => (
          <AnimatedBeam
            key={`c→${node.id}`}
            containerRef={container}
            fromRef={center}
            toRef={refs.get(node.id)!}
            shape="s"
            startXOffset={port + 4}
            endXOffset={-port}
            still={!node.wire.carries}
            dashed={node.wire.dashed}
            tone={node.wire.tone}
            duration={3.2}
            delay={(index % 6) * 0.35}
            className={cn("transition-opacity max-lg:hidden", dim(node.id) && "opacity-15")}
          />
        ))}

        <div className="relative grid gap-y-8 lg:grid-cols-[minmax(0,1fr)_clamp(2.5rem,5vw,5rem)_minmax(0,0.8fr)_clamp(2.5rem,5vw,5rem)_minmax(0,1fr)] lg:items-center">
          <Lane title="Reached at" count={ways.length} className="lg:text-right">
            {ways.length === 0 ? (
              <li>
                <WireNode
                  align="end"
                  mark={
                    <WirePlaceholder size="md">
                      <Globe />
                    </WirePlaceholder>
                  }
                  title={<span className="text-muted-foreground">Nothing reaches it</span>}
                  hint="No port is published and it is on no network"
                />
              </li>
            ) : (
              ways.map((node) => (
                <li
                  key={node.id}
                  {...watch(node.id)}
                  className={cn("min-w-0 transition-opacity", dim(node.id) && "opacity-40")}
                >
                  <WireNode
                    align="end"
                    mark={
                      <span ref={refs.get(node.id)} className="flex">
                        {node.mark}
                      </span>
                    }
                    eyebrow={node.eyebrow}
                    title={node.title}
                    hint={node.hint}
                  />
                </li>
              ))
            )}
          </Lane>

          <div aria-hidden className="hidden lg:block" />

          {/* The padding is the room the caption hanging under the mark takes,
              so a picture of one port and one mount does not drop it onto the
              block below. */}
          <div className="min-w-0 lg:flex lg:justify-center lg:pb-16">
            <WireNode
              align="center"
              mark={
                <span ref={center} className="relative flex" {...watch("c")}>
                  <WireMark tone="logo" size="lg">
                    {hasProductLogo(product) ? <ProductGlyph id={product} /> : <Box aria-hidden />}
                  </WireMark>
                  <span
                    aria-hidden
                    className="absolute -right-0.5 -bottom-0.5 flex size-4 items-center justify-center rounded-full bg-background"
                  >
                    <StatusDot tone={verdict.tone} live={verdict.live} />
                  </span>
                </span>
              }
              eyebrow={
                detail.composeService
                  ? `${detail.composeStack} · ${detail.composeService}`
                  : "Container"
              }
              title={<span className="block truncate">{detail.name}</span>}
              hint={
                <span className="numeric">
                  {running
                    ? [
                        readings?.cpu !== undefined ? `${percent(readings.cpu)} CPU` : undefined,
                        readings?.memory !== undefined ? bytes(readings.memory) : undefined,
                      ]
                        .filter(Boolean)
                        .join(" · ") || verdict.word
                    : verdict.word}
                </span>
              }
            />
          </div>

          <div aria-hidden className="hidden lg:block" />

          <Lane title="Keeps" count={keeps.length}>
            {keeps.length === 0 ? (
              <li>
                <WireNode
                  mark={
                    <WirePlaceholder size="md">
                      <Servers />
                    </WirePlaceholder>
                  }
                  title={<span className="text-muted-foreground">Nothing mounted</span>}
                  hint="Everything it writes is lost when it is replaced"
                />
              </li>
            ) : (
              keeps.map((node) => (
                <li
                  key={node.id}
                  {...watch(node.id)}
                  className={cn("min-w-0 transition-opacity", dim(node.id) && "opacity-40")}
                >
                  <WireNode
                    mark={
                      <span ref={refs.get(node.id)} className="flex">
                        {node.mark}
                      </span>
                    }
                    eyebrow={node.eyebrow}
                    title={node.title}
                    hint={node.hint}
                  />
                </li>
              ))
            )}
          </Lane>
        </div>
      </div>
    </div>
  )
}

function Lane({
  title,
  count,
  className,
  children,
}: {
  title: string
  count: number
  className?: string
  children: React.ReactNode
}) {
  return (
    <div className="min-w-0 self-stretch lg:flex lg:flex-col lg:justify-center">
      <p className={cn("eyebrow mb-4", className)}>
        {title}
        {count > 0 && (
          <span className="numeric ml-1.5 font-medium tracking-normal text-muted-foreground/70">
            {count}
          </span>
        )}
      </p>
      <ol className="flex flex-col gap-5" aria-label={title}>
        {children}
      </ol>
    </div>
  )
}

function Mark({
  glyph: Glyph,
  tone = "logo",
}: {
  glyph: Icon
  tone?: "logo" | "warning" | "danger"
}) {
  return (
    <WireMark tone={tone} size="md">
      <Glyph aria-hidden />
    </WireMark>
  )
}

/** The published ports, then the networks: every way something reaches the container. */
function waysIn(
  detail: ContainerDetail,
  routes: PortRoute[] | undefined,
  running: boolean,
  flowing: boolean,
): Node[] {
  const nodes: Node[] = []
  for (const row of portRows(detail.exposure ?? [], routes)) {
    if (row.hostPort === undefined) continue
    const reach = reachWords(row)
    const route = row.route
    const open = reach.tone === "warning"
    const inside = `${row.containerPort}/${row.protocol}`
    if (route?.reach === "proxied" && route.vhost) {
      nodes.push({
        id: `p:${row.key}`,
        mark: <Mark glyph={route.tls ? LockClosed : Globe} />,
        eyebrow: route.tls ? "HTTPS · through the proxy" : "HTTP · through the proxy",
        title: (
          <a
            href={route.url ?? `${route.tls ? "https" : "http"}://${route.vhost}/`}
            target="_blank"
            rel="noopener noreferrer"
            className="block truncate rounded-sm font-mono focus-ring hover:underline"
          >
            {route.vhost}
          </a>
        ),
        hint: `${row.published} → ${inside}`,
        wire: { tone: "default", dashed: !running, carries: flowing },
      })
      continue
    }
    nodes.push({
      id: `p:${row.key}`,
      mark: <Mark glyph={open ? Warning : Globe} tone={open ? "warning" : "logo"} />,
      eyebrow: open
        ? "Every interface"
        : row.scope === "loopback"
          ? "This server only"
          : "Published",
      title: <span className="block truncate font-mono">{row.published}</span>,
      hint: (
        <>
          → {inside} · {reach.word}
          {route?.firewall.dockerBypass && route.firewall.verdict === "denied"
            ? " · the firewall's deny does not apply"
            : ""}
        </>
      ),
      wire: {
        tone: open ? "warning" : "default",
        dashed: !running || route?.reach === "blocked",
        carries: flowing && route?.reach !== "blocked",
      },
    })
  }

  if (detail.networkMode === "host") {
    nodes.push({
      id: "n:host",
      mark: <Mark glyph={NetworkDevice} tone="warning" />,
      eyebrow: "Host network",
      title: "The server's own",
      hint: "Every port it opens is open on the server",
      wire: { tone: "warning", dashed: !running, carries: flowing },
    })
  }
  for (const network of detail.networkDetails) {
    const alias = network.aliases.find((name) => name !== detail.id.slice(0, 12))
    nodes.push({
      id: `n:${network.networkId || network.name}`,
      mark: <Mark glyph={NetworkDevice} />,
      eyebrow: "Network",
      title: <span className="block truncate">{network.name}</span>,
      hint: (
        <span className="numeric">
          {network.ipAddress || "no address"}
          {alias ? ` · reached as ${alias}` : ""}
        </span>
      ),
      wire: { tone: "default", dashed: !running || !network.ipAddress, carries: false },
    })
  }
  return nodes
}

/** Every mount, said as where the data really is and whether it outlives the container. */
function storesOf(detail: ContainerDetail, running: boolean): Node[] {
  return detail.mounts.map((mount, index) => {
    const id = `k:${index}`
    const where = mount.destination
    const access = mount.rw ? "" : " · read-only"
    if (mount.type === "bind" && /(^|\/)docker\.sock$/.test(mount.source)) {
      return {
        id,
        mark: <Mark glyph={Warning} tone="danger" />,
        eyebrow: "Docker's own socket",
        title: <span className="block truncate font-mono">{mount.source}</span>,
        hint: `${where} · it can control Docker itself`,
        wire: { tone: "danger", dashed: false, carries: false },
      }
    }
    if (mount.type === "tmpfs") {
      return {
        id,
        mark: <Mark glyph={Cpu} />,
        eyebrow: "Memory",
        title: <span className="block truncate font-mono">{where}</span>,
        hint: "gone when it stops",
        wire: { tone: "default", dashed: true, carries: false },
      }
    }
    const database = looksLikeDatabase(mount.name, mount.destination, mount.source, detail.image)
    if (mount.type === "volume") {
      const product = containerProduct(detail)
      return {
        id,
        mark:
          database && hasProductLogo(product) ? (
            <WireMark tone="logo" size="md">
              <ProductGlyph id={product} />
            </WireMark>
          ) : (
            <Mark glyph={Servers} />
          ),
        eyebrow: database ? "Volume · its data" : "Volume",
        title: <span className="block truncate">{mount.name || mount.source}</span>,
        hint: (
          <span className="font-mono">
            {where}
            {access}
          </span>
        ),
        wire: { tone: "default", dashed: !running, carries: false },
      }
    }
    return {
      id,
      mark: <Mark glyph={FolderClosed} />,
      eyebrow: "Folder on this server",
      title: (
        <span className="block truncate font-mono" title={mount.source}>
          {mount.source}
        </span>
      ),
      hint: (
        <span className="font-mono">
          {where}
          {access}
        </span>
      ),
      wire: { tone: "default", dashed: !running, carries: false },
    }
  })
}
