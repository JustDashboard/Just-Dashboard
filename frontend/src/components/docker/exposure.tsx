"use client"

import { External, Globe, LockClosed, Servers, Shield } from "@/components/icons"
import { cn } from "@/lib/utils"
import type { PortExposure, PortRoute } from "@/lib/types"
import { Tag } from "@/components/tag"
import { HoverCard, HoverCardContent, HoverCardTrigger } from "@/components/ui/hover-card"

/**
 * A published port, drawn so that the two cases that matter look different.
 *
 * `127.0.0.1:3000 → 3000` and `0.0.0.0:443 → 443` differ by one character and
 * by everything else. The first is reachable only by processes on this server,
 * including the reverse proxy in front of it; the second is offered to
 * anything that can route here. The old badge rendered them identically and
 * differed only in whether it was a link.
 *
 * The classification comes from the server — see dockerx/exposure.go — so this
 * component states it rather than deciding it, and the four surfaces that show
 * a port cannot drift apart.
 *
 * It is a `Tag`, like every other fixed property of a row: an icon carries the
 * scope and only the one worth catching — published on every interface — takes
 * a tone. The tinted fills these used to carry meant a container with six ports
 * published to localhost drew six coloured chips and the one open to the world
 * did not stand out at all.
 */

const SCOPE_STYLE = {
  all: { icon: Globe, tone: "warning" },
  private: { icon: Servers, tone: "default" },
  loopback: { icon: LockClosed, tone: "default" },
  internal: { icon: Shield, tone: "default" },
} as const

export function PortTag({ port, className }: { port: PortExposure; className?: string }) {
  const style = SCOPE_STYLE[port.scope] ?? SCOPE_STYLE.internal
  const Icon = style.icon
  const url = portURL(port)

  const label =
    port.scope === "internal"
      ? `${port.containerPort}${port.protocol === "tcp" ? "" : `/${port.protocol}`}`
      : `${port.hostPort} → ${port.containerPort}`

  const body = (
    <Tag
      mono
      tone={style.tone}
      className={cn(port.scope === "internal" && "border-dashed", className)}
    >
      <Icon className="size-2.5 shrink-0" />
      {label}
      {url && <External className="size-2.5 shrink-0 opacity-60" />}
    </Tag>
  )

  return (
    <HoverCard openDelay={150}>
      <HoverCardTrigger asChild>
        {url ? (
          <a
            href={url}
            target="_blank"
            rel="noreferrer"
            className="transition-opacity hover:opacity-80"
          >
            {body}
          </a>
        ) : (
          <button type="button" className="cursor-help">
            {body}
          </button>
        )}
      </HoverCardTrigger>
      <HoverCardContent className="w-80 space-y-1 text-xs leading-relaxed">
        <p className="text-body font-medium">{port.label}</p>
        <p className="text-muted-foreground">{port.summary}</p>
      </HoverCardContent>
    </HoverCard>
  )
}

/**
 * A port on every interface has no single correct host — the answer depends on
 * which of this machine's addresses the reader's browser can reach — so it
 * deliberately gets no link rather than one that leads somewhere else.
 */
function portURL(port: PortExposure): string | undefined {
  if (!port.hostPort) return undefined
  if (port.scope === "loopback" || port.scope === "private") {
    const host = port.ipv6 ? `[${port.hostIp}]` : (port.hostIp ?? "127.0.0.1")
    return `http://${host}:${port.hostPort}`
  }
  return undefined
}

/** Widest reach first, so a cell that can only show one shows the one that matters. */
const SCOPE_RANK: Record<PortExposure["scope"], number> = {
  all: 0,
  private: 1,
  loopback: 2,
  internal: 3,
}

/**
 * The port list for a table cell: most exposed first, the rest behind a count.
 *
 * `max` is a hard limit on *rows*, not a suggestion. A reverse proxy with six
 * published ports wrapped its cell onto three lines while every other cell in
 * the row was one or two, so one container in the list was half again as tall
 * as its neighbours and the table read as broken — the single most visible
 * defect on the containers page. The overflow is a count you can hover, which
 * is both shorter and more informative than three chips and a `+3`.
 */
export function PortList({ ports, max = 3 }: { ports: PortExposure[]; max?: number }) {
  const published = ports
    .filter((p) => p.hostPort)
    .slice()
    .sort((a, b) => SCOPE_RANK[a.scope] - SCOPE_RANK[b.scope])
  if (published.length === 0) {
    return <span className="text-hint text-muted-foreground">not published</span>
  }
  const shown = published.slice(0, max)
  const rest = published.slice(max)
  return (
    <div className="flex min-w-0 items-center gap-1 overflow-hidden">
      {shown.map((port, i) => (
        <PortTag key={`${port.hostIp}-${port.hostPort}-${i}`} port={port} />
      ))}
      {rest.length > 0 && (
        <HoverCard openDelay={150}>
          <HoverCardTrigger asChild>
            <button
              type="button"
              onClick={(event) => event.stopPropagation()}
              className="shrink-0 cursor-help rounded-sm text-micro font-medium text-muted-foreground focus-ring hover:text-foreground"
            >
              +{rest.length}
            </button>
          </HoverCardTrigger>
          <HoverCardContent className="w-72 space-y-1.5">
            <p className="text-hint text-muted-foreground">
              {rest.length} more published port{rest.length === 1 ? "" : "s"}
            </p>
            <div className="flex flex-wrap gap-1">
              {rest.map((port, i) => (
                <PortTag key={`${port.hostIp}-${port.hostPort}-rest-${i}`} port={port} />
              ))}
            </div>
          </HoverCardContent>
        </HoverCard>
      )}
    </div>
  )
}

const REACH_STYLE = {
  external: { label: "Reachable from outside", tone: "text-warning", icon: Globe },
  proxied: { label: "Through the reverse proxy", tone: "text-foreground", icon: Shield },
  "server-only": { label: "This server only", tone: "text-muted-foreground", icon: LockClosed },
  blocked: { label: "Blocked by the firewall", tone: "text-muted-foreground", icon: Shield },
  unknown: { label: "Reachability unknown", tone: "text-muted-foreground", icon: Globe },
} as const

/**
 * One published port traced all the way out: binding, reverse proxy, firewall.
 *
 * The correlation nothing else in this class of tool does, and the place it
 * would be easiest to overclaim. Every verdict carries the reasoning that
 * produced it, and one that was worked out rather than read says so.
 */
export function RouteRow({ route }: { route: PortRoute }) {
  const style = REACH_STYLE[route.reach] ?? REACH_STYLE.unknown
  const Icon = style.icon
  return (
    <div className="space-y-1.5 rounded-md border border-hairline px-3 py-2.5">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <span className="flex items-center gap-2 font-mono text-xs">
          <Icon className={cn("size-3.5 shrink-0", style.tone)} />
          {route.hostIp || "0.0.0.0"}:{route.hostPort} → {route.containerPort}
          {route.protocol !== "tcp" && `/${route.protocol}`}
        </span>
        <span className={cn("text-hint font-medium", style.tone)}>
          {style.label}
          {route.inferred && <span className="ml-1 font-normal opacity-70">· inferred</span>}
        </span>
      </div>

      <p className="text-hint leading-relaxed text-muted-foreground">{route.reasoning}</p>

      {route.url && (
        <a
          href={route.url}
          target="_blank"
          rel="noreferrer"
          className="inline-flex items-center gap-1 text-hint text-primary hover:underline"
        >
          {route.url}
          <External className="size-2.5" />
        </a>
      )}

      {route.firewall.known ? (
        <p className="text-hint text-muted-foreground">
          Firewall: {route.firewall.backend}
          {route.firewall.enabled === false && " (not enabled)"}
          {route.firewall.rule ? ` — ${route.firewall.rule}` : ` — no rule names this port`}
          {route.firewall.dockerBypass && (
            <span className="text-warning">
              {" "}
              · Docker&apos;s NAT rules are consulted before {route.firewall.backend}&apos;s, so
              rules about this port do not apply to it
            </span>
          )}
        </p>
      ) : (
        <p className="text-hint text-muted-foreground">
          No firewall this dashboard can read, so external reachability cannot be judged from here.
        </p>
      )}
    </div>
  )
}
