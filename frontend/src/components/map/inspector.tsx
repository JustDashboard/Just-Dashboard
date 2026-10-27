"use client"

import Link from "next/link"
import {
  ArrowUpRight,
  ChevronRight,
  Copy,
  Cross,
  Crosshair,
  Plus,
  RotateCounterClockwise,
} from "@/components/icons"
import { IconAction, rowReveal } from "@/components/icon-action"
import { KEYCAP } from "@/components/command-search/keycaps"
import { WireHost } from "@/components/deploy/wire"
import { Meter, utilisationTone } from "@/components/meter"
import { ProductGlyph, platformProduct } from "@/components/product-logo"
import { Status, StatusDot } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { useMetrics } from "@/hooks/use-metrics"
import { copyText } from "@/lib/clipboard"
import { duration, percent } from "@/lib/format"
import { BRANCHES, itemIndex, kindOf, relatedTo, shelfId, type KindDef } from "@/lib/server-map"
import type { MapItem, ServerMap } from "@/lib/types"
import { cn } from "@/lib/utils"
import { VIA_LABEL } from "@/components/map/edges"
import {
  HueTile,
  ItemMark,
  KIND_GLYPH,
  TONE_DOT,
  itemProduct,
  strongestProducts,
} from "@/components/map/marks"

type Point = (id: string | null) => void

/**
 * What the chosen box says about itself. The canvas draws a link as a line;
 * the inspector names it and opens the other end.
 *
 * Every view has one shape, so the panel reads the same whatever is chosen:
 * a head naming what kind of thing this is; the thing itself — its mark, its
 * name, its state — with what can be done to it right under the name, where
 * the eye already is; then its sections, each a heading over a hairline. A
 * row in any of them is a way somewhere, and pointing at it rings its box on
 * the canvas, so the list and the picture are read together. With nothing
 * chosen it is the server's own account: how hard it is working now, and
 * what is on it, branch by branch.
 *
 * It is the ER diagram's inspector in place (a column of the workbench, a
 * hairline from the canvas, the lower part of the frame on a narrow window).
 */
export function MapInspector({
  map,
  kinds,
  selected,
  onChoose,
  onHover,
  onFrame,
  onClose,
  onReturn,
  placed,
  onAdd,
  canAdd,
  folded,
}: {
  map: ServerMap
  kinds: KindDef[]
  selected: string | null
  /** Choose a thing or a shelf, bringing it into view. */
  onChoose: (id: string) => void
  /** Point at a box from a row: ring it on the canvas. */
  onHover: Point
  /** Bring the chosen box into view with what it is wired to. */
  onFrame: (id: string) => void
  onClose: () => void
  onReturn: (id: string) => void
  placed: (id: string) => boolean
  onAdd: (kind: KindDef) => void
  canAdd: (kind: KindDef) => boolean
  folded: (id: string) => boolean
}) {
  const found = selected ? itemIndex(map).get(selected) : undefined
  const shelf = selected?.startsWith("shelf:") ? kindOf(selected.slice(6)) : undefined
  const head = found ? kindOf(found.kind) : shelf
  const HeadGlyph = head ? KIND_GLYPH[head.key] : undefined
  return (
    <aside
      aria-label="Inspector"
      className="flex min-h-0 min-w-0 flex-col border-hairline max-lg:border-t lg:border-l"
    >
      <div className="flex h-11 shrink-0 items-center gap-2 border-b border-hairline pr-2 pl-4">
        {HeadGlyph && (
          <span data-branch={head?.branch} className="flex text-(--hue)">
            <HeadGlyph aria-hidden className="size-3.5" />
          </span>
        )}
        <span className="eyebrow">{found ? head?.label : shelf ? "Shelf" : "This server"}</span>
        <span className="flex-1" />
        <IconAction label="Close the inspector (I)" className="size-7" onClick={onClose}>
          <Cross />
        </IconAction>
      </div>

      {/* Keyed by what is shown, so each choice arrives rather than morphs. */}
      <div key={selected ?? "server"} className="min-h-0 flex-1 animate-rise overflow-y-auto">
        {found ? (
          <ItemView
            map={map}
            item={found.item}
            kind={found.kind}
            onChoose={onChoose}
            onHover={onHover}
            onFrame={onFrame}
            placed={placed(found.item.id)}
            onReturn={() => onReturn(found.item.id)}
          />
        ) : shelf ? (
          <ShelfView
            map={map}
            kind={shelf}
            onChoose={onChoose}
            onHover={onHover}
            onAdd={onAdd}
            canAdd={canAdd(shelf)}
            folded={folded(shelfId(shelf.key))}
          />
        ) : (
          <ServerView map={map} kinds={kinds} onChoose={onChoose} onHover={onHover} />
        )}
      </div>

      <div className="flex shrink-0 flex-wrap items-center gap-x-3 gap-y-1.5 border-t border-hairline px-4 py-2.5 text-hint text-muted-foreground max-lg:hidden">
        <Key cap="/">Find</Key>
        <Key cap="F">Fit</Key>
        <Key cap="I">Panel</Key>
        <Key cap="Esc">Let go</Key>
        <Key cap="Alt">Drag one box</Key>
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

/** The thing itself: its mark, its name, its state, and what can be done to it. */
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

/** Label and value, the facts a box's own page leads with. */
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

/**
 * A row that is a way to another box: pressing it chooses that box,
 * pointing at it rings the box on the canvas.
 */
function BoxRow({
  id,
  mark,
  title,
  line,
  trailing,
  onChoose,
  onHover,
}: {
  id: string
  mark: React.ReactNode
  title: React.ReactNode
  line?: React.ReactNode
  trailing?: React.ReactNode
  onChoose: (id: string) => void
  onHover: Point
}) {
  return (
    <li>
      <button
        type="button"
        className="group/row flex w-full min-w-0 items-center gap-3 rounded-lg px-2 py-1.5 text-left focus-ring transition-colors hover:bg-row-hover"
        onClick={() => onChoose(id)}
        onMouseEnter={() => onHover(id)}
        onMouseLeave={() => onHover(null)}
        onFocus={() => onHover(id)}
        onBlur={() => onHover(null)}
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

function ItemView({
  map,
  item,
  kind,
  onChoose,
  onHover,
  onFrame,
  placed,
  onReturn,
}: {
  map: ServerMap
  item: MapItem
  kind: string
  onChoose: (id: string) => void
  onHover: Point
  onFrame: (id: string) => void
  placed: boolean
  onReturn: () => void
}) {
  const def = kindOf(kind)
  const related = relatedTo(map, item.id)
  const facts = item.facts?.length
    ? item.facts
    : item.detail
      ? [{ label: "About", value: item.detail }]
      : []
  return (
    <>
      <Identity
        mark={<ItemMark item={item} kind={kind} className="size-11" />}
        title={item.name}
        line={
          <>
            <Status tone={TONE_DOT[item.tone]} label={item.state} />
            {def && (
              <>
                <span aria-hidden>·</span>
                <span>{def.noun[0].toUpperCase() + def.noun.slice(1)}</span>
              </>
            )}
          </>
        }
        actions={
          <>
            <Button size="sm" asChild className="min-w-0 flex-1">
              <Link href={item.href}>
                <span className="truncate">Open {def?.noun ?? "it"}</span>
                <ArrowUpRight />
              </Link>
            </Button>
            <IconAction
              label="Show it on the map with what it is wired to"
              variant="outline"
              size="icon-sm"
              onClick={() => onFrame(item.id)}
            >
              <Crosshair />
            </IconAction>
            {placed && (
              <IconAction
                label="Put it back on its shelf"
                variant="outline"
                size="icon-sm"
                onClick={onReturn}
              >
                <RotateCounterClockwise />
              </IconAction>
            )}
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

      <Section title="Wired to" count={related.length}>
        {related.length === 0 ? (
          <p className="text-hint text-muted-foreground">
            The server knows of nothing on the map this is wired to.
          </p>
        ) : (
          <ul className="-mx-2 space-y-0.5">
            {related.map((one) => (
              <BoxRow
                key={one.item.id}
                id={one.item.id}
                mark={<ItemMark item={one.item} kind={one.kind} />}
                title={one.item.name}
                line={
                  <>
                    <StatusDot tone={TONE_DOT[one.item.tone]} />
                    <span className="truncate">{one.item.state}</span>
                  </>
                }
                trailing={
                  <Tag
                    className="shrink-0"
                    title={
                      one.also.length > 0
                        ? [one.via, ...one.also]
                            .map((via) => linkSentence(via, one.direction))
                            .join(" · ")
                        : undefined
                    }
                  >
                    {linkSentence(one.via, one.direction)}
                  </Tag>
                }
                onChoose={onChoose}
                onHover={onHover}
              />
            ))}
          </ul>
        )}
      </Section>
    </>
  )
}

/**
 * How a link reads from the chosen box to the row: a domain "routes to" its
 * project, a project is "routed from" its domain, a database is "bound to it"
 * (the deployment in the row).
 */
function linkSentence(via: string, direction: "to" | "from") {
  const forward = VIA_LABEL[via] ?? via
  if (direction === "to") return forward
  switch (via) {
    case "route":
      return "routed from"
    case "release":
      return "its project"
    case "member":
      return "its stack"
    case "binding":
      return "bound to it"
    case "mount":
      return "mounted by"
    case "network":
      return "a member"
    case "certificate":
      return "covers it"
    case "backup":
      return "backs it up"
    case "image":
      return "run by"
    case "serves":
      return "served by"
    case "reference":
      return "referred to by"
    case "env":
      return "in its env"
    case "session":
      return "connected from"
    case "dependency":
      return "needed by"
    case "source":
      return "source of"
    case "workdir":
      return "folder of"
    case "starts":
      return "started by"
    case "user":
      return "runs it"
    case "notifies":
      return "notified by"
    case "owner":
      return "owns it"
    case "access":
      return "reached by"
    case "pooler":
      return "pooled by"
    case "self":
      return "runs it"
  }
  return forward
}

const SHELF_ROWS = 60

function ShelfView({
  map,
  kind,
  onChoose,
  onHover,
  onAdd,
  canAdd,
  folded,
}: {
  map: ServerMap
  kind: KindDef
  onChoose: (id: string) => void
  onHover: Point
  onAdd: (kind: KindDef) => void
  canAdd: boolean
  folded: boolean
}) {
  const reading = map.kinds.find((k) => k.key === kind.key)
  const items = reading?.items ?? []
  const total = reading ? Math.max(reading.total, items.length) : 0
  const down = items.filter((item) => item.tone === "danger").length
  return (
    <>
      <Identity
        mark={<HueTile kind={kind.key} className="size-11" />}
        title={kind.label}
        line={
          kind.tool ? (
            <span>{kind.tool.detail}</span>
          ) : reading?.state === "unread" ? (
            <Status tone="warning" label="Not read" />
          ) : (
            <>
              <span>
                <span className="numeric text-foreground">{total.toLocaleString("en-US")}</span>{" "}
                {total === 1 ? kind.noun : `${kind.noun}s`}
              </span>
              {down > 0 && (
                <>
                  <span aria-hidden>·</span>
                  <Status tone="danger" label={`${down} down`} />
                </>
              )}
              {folded && (
                <>
                  <span aria-hidden>·</span>
                  <span>folded</span>
                </>
              )}
            </>
          )
        }
        actions={
          <>
            <Button size="sm" asChild className="min-w-0 flex-1">
              <Link href={kind.href}>
                <span className="truncate">Open {kind.label}</span>
                <ArrowUpRight />
              </Link>
            </Button>
            {kind.add && canAdd && (
              <Button size="sm" variant="outline" onClick={() => onAdd(kind)}>
                <Plus />
                {kind.add.label}
              </Button>
            )}
          </>
        }
      />

      {reading?.reason && (
        <p
          className={cn(
            "border-t border-hairline px-4 py-3 text-hint",
            reading.state === "unread" ? "text-warning" : "text-muted-foreground",
          )}
        >
          {reading.reason}
        </p>
      )}

      {items.length > 0 && (
        <Section title="On this shelf" count={total}>
          <ul className="-mx-2 space-y-0.5">
            {items.slice(0, SHELF_ROWS).map((item) => (
              <BoxRow
                key={item.id}
                id={item.id}
                mark={<ItemMark item={item} kind={kind.key} />}
                title={item.name}
                line={
                  <>
                    <StatusDot tone={TONE_DOT[item.tone]} />
                    <span
                      className={cn(
                        "truncate",
                        item.tone === "danger" && "text-destructive",
                        item.tone === "warning" && "text-warning",
                      )}
                    >
                      {item.state}
                    </span>
                  </>
                }
                onChoose={onChoose}
                onHover={onHover}
              />
            ))}
          </ul>
          {total > SHELF_ROWS && (
            <Link
              href={kind.href}
              className="mt-2 inline-flex items-center gap-1 text-hint text-muted-foreground focus-ring hover:text-foreground"
            >
              {(total - SHELF_ROWS).toLocaleString("en-US")} more on its page
              <ArrowUpRight className="size-3" />
            </Link>
          )}
        </Section>
      )}
    </>
  )
}

function ServerView({
  map,
  kinds,
  onChoose,
  onHover,
}: {
  map: ServerMap
  kinds: KindDef[]
  onChoose: (id: string) => void
  onHover: Point
}) {
  const { host, snapshot, connection } = useMetrics()
  const platform = platformProduct(host?.platform)
  const readings = new Map(map.kinds.map((k) => [k.key, k]))
  const live = connection === "open"
  return (
    <>
      <Identity
        mark={<WireHost size="md" />}
        title={host?.hostname ?? map.host.hostname}
        line={
          host ? (
            <>
              {platform && <ProductGlyph id={platform} className="size-3" />}
              <span>
                {host.platform} {host.platformVersion}
              </span>
              <span aria-hidden>·</span>
              <span className="truncate">{host.kernelVersion}</span>
            </>
          ) : (
            <span>Reading the host…</span>
          )
        }
      />

      <Section title="Now">
        <div className="space-y-3">
          <Reading label="CPU" value={snapshot?.cpu.totalPercent} live={live} />
          <Reading label="Memory" value={snapshot?.memory.usedPercent} live={live} />
        </div>
        <dl className="mt-4 grid grid-cols-4 overflow-hidden rounded-lg border border-hairline">
          <Cell label="Up">{snapshot ? duration(snapshot.uptimeSeconds) : "—"}</Cell>
          <Cell label="Load">{snapshot ? snapshot.cpu.loadAvg1.toFixed(2) : "—"}</Cell>
          <Cell label="Cores">{snapshot?.cpu.cores ?? host?.cpuCores ?? "—"}</Cell>
          <Cell label="Tasks">{snapshot?.procs.total ?? host?.processes ?? "—"}</Cell>
        </dl>
      </Section>

      <Section title="On this server">
        <div className="space-y-4">
          {BRANCHES.map((branch) => {
            const here = kinds.filter((kind) => kind.branch === branch.key && !kind.tool)
            if (here.length === 0) return null
            const things = here.reduce((sum, kind) => {
              const reading = readings.get(kind.key)
              return sum + (reading ? Math.max(reading.total, reading.items.length) : 0)
            }, 0)
            return (
              <div key={branch.key} data-branch={branch.key}>
                {/* A branch heads its kinds in weight and in its hue, rather
                    than with a glyph: the first kind's glyph beside it was the
                    same mark twice. The hue makes this list the canvas's key. */}
                <div className="mb-1 flex items-baseline justify-between px-0.5">
                  <span className="flex items-center gap-2 text-xs font-semibold text-foreground">
                    <span aria-hidden className="size-2 rounded-full bg-(--hue)" />
                    {branch.label}
                  </span>
                  <span className="numeric text-hint text-muted-foreground">
                    {things.toLocaleString("en-US")}
                  </span>
                </div>
                <ul className="-mx-2">
                  {here.map((kind) => (
                    <KindRow
                      key={kind.key}
                      kind={kind}
                      reading={readings.get(kind.key)}
                      onChoose={onChoose}
                      onHover={onHover}
                    />
                  ))}
                </ul>
              </div>
            )
          })}
        </div>
      </Section>
    </>
  )
}

/** One kind on the server: how many, how many down, and its shelf a press away. */
function KindRow({
  kind,
  reading,
  onChoose,
  onHover,
}: {
  kind: KindDef
  reading?: ServerMap["kinds"][number]
  onChoose: (id: string) => void
  onHover: Point
}) {
  const total = reading ? Math.max(reading.total, reading.items.length) : 0
  const down = reading?.items.filter((item) => item.tone === "danger").length ?? 0
  const Glyph = KIND_GLYPH[kind.key]
  const id = shelfId(kind.key)
  const products = strongestProducts(
    (reading?.items ?? []).map((item) => itemProduct(item, kind.key)),
    3,
  )
  return (
    <li>
      <button
        type="button"
        className="group/row flex w-full items-center gap-2.5 rounded-md px-2 py-1 text-left text-body focus-ring transition-colors hover:bg-row-hover"
        onClick={() => onChoose(id)}
        onMouseEnter={() => onHover(id)}
        onMouseLeave={() => onHover(null)}
        onFocus={() => onHover(id)}
        onBlur={() => onHover(null)}
      >
        {Glyph && <Glyph aria-hidden className="size-3.5 text-(--hue)" />}
        <span className="min-w-0 flex-1 truncate">{kind.label}</span>
        {products.length > 0 && (
          <span aria-hidden className="flex shrink-0 items-center gap-1">
            {products.map((product) => (
              <ProductGlyph key={product} id={product} />
            ))}
          </span>
        )}
        {down > 0 && (
          <span className="flex items-center gap-1 text-hint text-destructive">
            <StatusDot tone="danger" />
            <span className="numeric">{down}</span>
          </span>
        )}
        <span className="numeric w-9 text-right text-muted-foreground">
          {reading?.state === "unread" ? "—" : total.toLocaleString("en-US")}
        </span>
      </button>
    </li>
  )
}

function Reading({ label, value, live }: { label: string; value?: number; live: boolean }) {
  return (
    <div className="space-y-1.5">
      <div className="flex items-baseline justify-between text-hint">
        <span className="flex items-center gap-1.5 text-muted-foreground">
          {live && <StatusDot tone="running" live />}
          {label}
        </span>
        <span className="numeric text-body text-foreground">
          {value === undefined ? "—" : percent(value, 0)}
        </span>
      </div>
      <Meter value={value ?? 0} tone={utilisationTone(value ?? 0)} size="thin" />
    </div>
  )
}

function Cell({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="min-w-0 border-l border-hairline px-2.5 py-2 first:border-l-0">
      <dt className="eyebrow truncate">{label}</dt>
      <dd className="numeric mt-0.5 truncate text-body">{children}</dd>
    </div>
  )
}
