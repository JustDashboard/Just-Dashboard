"use client"

import Link from "next/link"
import {
  ArrowUpRight,
  ChevronRight,
  Copy,
  Cross,
  Crosshair,
  Layers,
  Logs,
} from "@/components/icons"
import { IconAction, rowReveal } from "@/components/icon-action"
import { KEYCAP } from "@/components/command-search/keycaps"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { copyText } from "@/lib/clipboard"
import { plural } from "@/lib/format"
import {
  SYSTEM_LABEL,
  systemCensus,
  systemTitle,
  type System,
  type SystemNode,
} from "@/lib/server-systems"
import { cn } from "@/lib/utils"
import { VIA_LABEL } from "@/components/map/edges"
import { ItemMark, KIND_GLYPH, TONE_DOT } from "@/components/map/marks"
import { nodeKindLabel } from "@/components/map/nodes"

type Point = (id: string | null) => void

/**
 * What the chosen box says about itself. The canvas draws a link as a line;
 * the inspector names it and opens the other end.
 *
 * Every view has one shape: a head naming what kind of thing this is; the
 * thing itself — its mark, its name, its state — with what can be done to it
 * right under the name; then its sections, each a heading over a hairline. A
 * row in any of them is a way somewhere, and pointing at it rings its box on
 * the canvas. With a system and no box chosen it is that system's account —
 * what it holds, box by box — and with neither, the server's list of systems.
 */
export function MapInspector({
  systems,
  system,
  node,
  onChoose,
  onHover,
  onFocus,
  onFrame,
  onClose,
}: {
  systems: System[]
  system: System | undefined
  node: SystemNode | undefined
  /** Choose a box by its node id. */
  onChoose: (id: string) => void
  onHover: Point
  /** Fly to a system and keep it as the one being read. */
  onFocus: (id: string) => void
  /** Bring the chosen box into view with what it is wired to. */
  onFrame: (id: string) => void
  onClose: () => void
}) {
  const HeadGlyph = node ? KIND_GLYPH[node.kind] : system ? Layers : undefined
  return (
    <aside
      aria-label="Inspector"
      className="flex min-h-0 min-w-0 flex-col border-hairline max-lg:border-t lg:border-l"
    >
      <div className="flex h-11 shrink-0 items-center gap-2 border-b border-hairline pr-2 pl-4">
        {HeadGlyph && <HeadGlyph aria-hidden className="size-3.5 text-muted-foreground" />}
        <span className="eyebrow">
          {node ? nodeKindLabel(node) : system ? SYSTEM_LABEL[system.kind] : "This server"}
        </span>
        <span className="flex-1" />
        <IconAction label="Close the inspector (I)" className="size-7" onClick={onClose}>
          <Cross />
        </IconAction>
      </div>

      <div
        key={node?.id ?? system?.id ?? "server"}
        className="min-h-0 flex-1 animate-rise overflow-y-auto"
      >
        {node && system ? (
          <NodeView
            system={system}
            systems={systems}
            node={node}
            onChoose={onChoose}
            onHover={onHover}
            onFocus={onFocus}
            onFrame={onFrame}
          />
        ) : system ? (
          <SystemView system={system} onChoose={onChoose} onHover={onHover} />
        ) : (
          <ServerView systems={systems} onFocus={onFocus} />
        )}
      </div>

      <div className="flex shrink-0 flex-wrap items-center gap-x-3 gap-y-1.5 border-t border-hairline px-4 py-2.5 text-hint text-muted-foreground max-lg:hidden">
        <Key cap="/">Find</Key>
        <Key cap="F">Fit</Key>
        <Key cap="I">Panel</Key>
        <Key cap="↵">Open</Key>
        <Key cap="Esc">Let go</Key>
      </div>
    </aside>
  )
}

function Key({ cap, children }: { cap: string; children: React.ReactNode }) {
  return (
    <span className="flex items-center gap-1.5">
      <kbd className={KEYCAP}>{cap}</kbd>
      {children}
    </span>
  )
}

function Identity({
  mark,
  title,
  line,
  actions,
}: {
  mark: React.ReactNode
  title: React.ReactNode
  line: React.ReactNode
  actions?: React.ReactNode
}) {
  return (
    <div className="space-y-4 p-4">
      <div className="flex min-w-0 items-start gap-3">
        {mark}
        <div className="min-w-0 flex-1 pt-0.5">
          <h2 className="text-title leading-snug font-semibold break-words">{title}</h2>
          <div className="mt-1 flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-hint text-muted-foreground">
            {line}
          </div>
        </div>
      </div>
      {actions && <div className="flex items-center gap-1.5">{actions}</div>}
    </div>
  )
}

function Section({
  title,
  count,
  children,
}: {
  title: string
  count?: number
  children: React.ReactNode
}) {
  return (
    <section className="border-t border-hairline px-4 py-4">
      <h3 className="mb-2.5 flex items-center gap-2">
        <span className="eyebrow">{title}</span>
        {count !== undefined && (
          <span className="numeric text-hint text-muted-foreground">{count}</span>
        )}
      </h3>
      {children}
    </section>
  )
}

function Facts({ facts }: { facts: { label: string; value: string }[] }) {
  return (
    <dl className="grid grid-cols-[minmax(0,6.5rem)_minmax(0,1fr)] gap-x-3 gap-y-2 text-body">
      {facts.map((fact) => (
        <div key={fact.label} className="contents">
          <dt className="truncate text-muted-foreground">{fact.label}</dt>
          <dd className="min-w-0 break-words">{fact.value}</dd>
        </div>
      ))}
    </dl>
  )
}

function Row({
  mark,
  title,
  line,
  trailing,
  onPress,
  onEnter,
  onLeave,
}: {
  mark: React.ReactNode
  title: React.ReactNode
  line?: React.ReactNode
  trailing?: React.ReactNode
  onPress: () => void
  onEnter?: () => void
  onLeave?: () => void
}) {
  return (
    <li>
      <button
        type="button"
        className="group/row flex w-full min-w-0 items-center gap-3 rounded-lg px-2 py-1.5 text-left focus-ring transition-colors hover:bg-row-hover"
        onClick={onPress}
        onMouseEnter={onEnter}
        onMouseLeave={onLeave}
        onFocus={onEnter}
        onBlur={onLeave}
      >
        {mark}
        <span className="min-w-0 flex-1">
          <span className="block truncate text-body font-medium">{title}</span>
          {line && (
            <span className="flex min-w-0 items-center gap-1.5 text-hint text-muted-foreground">
              {line}
            </span>
          )}
        </span>
        {trailing}
        <ChevronRight
          aria-hidden
          className={cn("size-3.5 shrink-0 text-muted-foreground", rowReveal("row"))}
        />
      </button>
    </li>
  )
}

function NodeView({
  system,
  systems,
  node,
  onChoose,
  onHover,
  onFocus,
  onFrame,
}: {
  system: System
  systems: System[]
  node: SystemNode
  onChoose: (id: string) => void
  onHover: Point
  onFocus: (id: string) => void
  onFrame: (id: string) => void
}) {
  const { item } = node
  const byId = new Map(system.nodes.map((n) => [n.id, n]))
  const wired = system.edges
    .filter((e) => e.source === node.id || e.target === node.id)
    .map((e) => ({ edge: e, other: byId.get(e.source === node.id ? e.target : e.source) }))
    .filter((w): w is { edge: (typeof w)["edge"]; other: SystemNode } => w.other !== undefined)
  const logs = system.nodes.find((n) => n.host === node.id && n.kind === "logs")
  const shared = node.shared
    .map((id) => systems.find((s) => s.id === id))
    .filter((s): s is System => s !== undefined)
  const facts = item.facts?.length
    ? item.facts
    : item.detail
      ? [{ label: "About", value: item.detail }]
      : []
  return (
    <>
      <Identity
        mark={<ItemMark item={item} kind={node.kind} className="size-11" />}
        title={item.name}
        line={
          <>
            <Status tone={TONE_DOT[item.tone]} label={item.state} />
            <span aria-hidden>·</span>
            <span>in {systemTitle(system)}</span>
          </>
        }
        actions={
          <>
            <Button size="sm" asChild className="min-w-0 flex-1">
              <Link href={item.href}>
                <span className="truncate">{node.kind === "logs" ? "Read the logs" : "Open"}</span>
                <ArrowUpRight />
              </Link>
            </Button>
            {logs && (
              <IconAction label="Read its logs" variant="outline" size="icon-sm" asChild>
                <Link href={logs.item.href}>
                  <Logs />
                </Link>
              </IconAction>
            )}
            <IconAction
              label="Show it on the map with what it is wired to"
              variant="outline"
              size="icon-sm"
              onClick={() => onFrame(node.id)}
            >
              <Crosshair />
            </IconAction>
            <IconAction
              label="Copy the name"
              variant="outline"
              size="icon-sm"
              onClick={() => void copyText(item.name, "Name copied")}
            >
              <Copy />
            </IconAction>
          </>
        }
      />
      {facts.length > 0 && (
        <Section title="Details">
          <Facts facts={facts} />
        </Section>
      )}
      {wired.length > 0 && (
        <Section title="Wired to" count={wired.length}>
          <ul className="-mx-2 space-y-0.5">
            {wired.map(({ edge, other }) => (
              <Row
                key={edge.id}
                mark={<ItemMark item={other.item} kind={other.kind} />}
                title={other.item.name}
                line={<Status tone={TONE_DOT[other.item.tone]} label={other.item.state} />}
                trailing={
                  <Tag
                    title={
                      edge.also.length
                        ? `Also: ${edge.also.map((v) => VIA_LABEL[v] ?? v).join(", ")}`
                        : undefined
                    }
                    className="shrink-0"
                  >
                    {VIA_LABEL[edge.via] ?? edge.via}
                  </Tag>
                }
                onPress={() => onChoose(other.id)}
                onEnter={() => onHover(other.id)}
                onLeave={() => onHover(null)}
              />
            ))}
          </ul>
        </Section>
      )}
      {shared.length > 0 && (
        <Section title="Also drawn in" count={shared.length}>
          <ul className="-mx-2 space-y-0.5">
            {shared.map((s) => (
              <Row
                key={s.id}
                mark={<ItemMark item={s.anchor} kind={s.anchorKind} />}
                title={systemTitle(s)}
                line={SYSTEM_LABEL[s.kind]}
                onPress={() => onFocus(s.id)}
              />
            ))}
          </ul>
        </Section>
      )}
    </>
  )
}

const ORDER = [
  "dashboard",
  "projects",
  "stacks",
  "domains",
  "streams",
  "tunnels",
  "containers",
  "services",
  "pm2",
  "databases",
  "volumes",
  "backups",
  "repositories",
  "certificates",
  "logs",
]

function SystemView({
  system,
  onChoose,
  onHover,
}: {
  system: System
  onChoose: (id: string) => void
  onHover: Point
}) {
  const census = systemCensus(system)
  const nodes = [...system.nodes].sort(
    (a, b) =>
      ORDER.indexOf(a.kind) - ORDER.indexOf(b.kind) || a.item.name.localeCompare(b.item.name),
  )
  const loose = system.kind === "loose"
  return (
    <>
      <Identity
        mark={
          loose ? (
            <span className="flex size-11 items-center justify-center rounded-lg border border-hairline bg-background">
              <Layers className="size-4 text-muted-foreground" />
            </span>
          ) : (
            <ItemMark item={system.anchor} kind={system.anchorKind} className="size-11" />
          )
        }
        title={systemTitle(system)}
        line={
          loose ? (
            <span>What no system above takes</span>
          ) : (
            <>
              <Status tone={TONE_DOT[system.anchor.tone]} label={system.anchor.state} />
              <span aria-hidden>·</span>
              <span>{SYSTEM_LABEL[system.kind]}</span>
            </>
          )
        }
        actions={
          !loose && (
            <Button size="sm" asChild className="min-w-0 flex-1">
              <Link href={system.anchor.href}>
                <span className="truncate">Open {systemTitle(system)}</span>
                <ArrowUpRight />
              </Link>
            </Button>
          )
        }
      />
      <Section title="Holds">
        <Facts
          facts={[
            { label: "Containers", value: String(census.containers) },
            { label: "Databases", value: String(census.databases) },
            { label: "Domains", value: String(census.domains) },
            { label: "Volumes", value: String(census.volumes) },
            { label: "Backups", value: String(census.backups) },
          ].filter((f) => f.value !== "0")}
        />
      </Section>
      <Section title="On it" count={nodes.length}>
        <ul className="-mx-2 space-y-0.5">
          {nodes.map((n) => (
            <Row
              key={n.id}
              mark={<ItemMark item={n.item} kind={n.kind} />}
              title={n.item.name}
              line={
                <>
                  <Status tone={TONE_DOT[n.item.tone]} label={n.item.state} />
                  <span aria-hidden>·</span>
                  <span className="truncate">{nodeKindLabel(n)}</span>
                </>
              }
              onPress={() => onChoose(n.id)}
              onEnter={() => onHover(n.id)}
              onLeave={() => onHover(null)}
            />
          ))}
        </ul>
      </Section>
    </>
  )
}

function ServerView({ systems, onFocus }: { systems: System[]; onFocus: (id: string) => void }) {
  // Each thing once, however many systems draw it.
  const seen = new Map<string, string>()
  for (const s of systems) {
    for (const n of s.nodes) if (n.kind !== "logs") seen.set(n.itemId, n.kind)
    for (const f of s.frames) seen.set(f.itemId, "stacks")
  }
  const count = (...kinds: string[]) => [...seen.values()].filter((k) => kinds.includes(k)).length
  const totals = [
    { label: "Deployments", value: count("projects") },
    { label: "Stacks", value: count("stacks") },
    { label: "Containers", value: count("containers") },
    { label: "Databases", value: count("databases") },
    { label: "Domains", value: count("domains") },
    { label: "Volumes", value: count("volumes") },
    { label: "Backups", value: count("backups") },
    { label: "Apps and services", value: count("pm2", "services") },
  ].filter((t) => t.value > 0)
  return (
    <>
      <Identity
        mark={
          <span className="flex size-11 items-center justify-center rounded-lg border border-hairline bg-background">
            <Layers className="size-4 text-muted-foreground" />
          </span>
        }
        title={plural(systems.length, "system")}
        line={<span>Each one is drawn on its own, with what it is wired to</span>}
      />
      {totals.length > 0 && (
        <Section title="On this server">
          <Facts
            facts={totals.map((t) => ({ label: t.label, value: t.value.toLocaleString("en-US") }))}
          />
        </Section>
      )}
      {/* The rail lists them beside the canvas; on a narrow window it is here. */}
      <div className="lg:hidden">
        <Section title="Systems" count={systems.length}>
          <ul className="-mx-2 space-y-0.5">
            {systems.map((s) => (
              <Row
                key={s.id}
                mark={<SystemMark system={s} />}
                title={systemTitle(s)}
                line={SYSTEM_LABEL[s.kind]}
                onPress={() => onFocus(s.id)}
              />
            ))}
          </ul>
        </Section>
      </div>
    </>
  )
}

export function SystemMark({ system, className }: { system: System; className?: string }) {
  if (system.kind === "loose") {
    return (
      <span
        className={cn(
          "flex size-8 shrink-0 items-center justify-center rounded-lg border border-hairline bg-background",
          className,
        )}
      >
        <Layers className="size-3.5 text-muted-foreground" />
      </span>
    )
  }
  return <ItemMark item={system.anchor} kind={system.anchorKind} size="sm" className={className} />
}
