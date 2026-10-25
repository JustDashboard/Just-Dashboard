"use client"

import Link from "next/link"
import { ArrowUpRight, Cross, Plus, RotateCounterClockwise } from "@/components/icons"
import { IconAction } from "@/components/icon-action"
import { WireHost } from "@/components/deploy/wire"
import { Status, StatusDot } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { useMetrics } from "@/hooks/use-metrics"
import { duration, plural } from "@/lib/format"
import { BRANCHES, itemIndex, kindOf, relatedTo, shelfId, type KindDef } from "@/lib/server-map"
import type { MapItem, ServerMap } from "@/lib/types"
import { cn } from "@/lib/utils"
import { VIA_LABEL } from "@/components/map/edges"
import { ItemMark, KIND_GLYPH, TONE_DOT } from "@/components/map/marks"

/**
 * What the chosen box says about itself, in words: the canvas draws a link
 * as a line, the inspector says what it is and opens the other end. With
 * nothing chosen it is the server's own account of itself — what it runs and
 * how much of each.
 *
 * It is the ER diagram's inspector in shape (a column of the workbench, a
 * hairline from the canvas, the lower part of the frame on a narrow window),
 * and every row in it is a way somewhere: a link opens the other end on the
 * map, a thing's verb opens its page.
 */
export function MapInspector({
  map,
  kinds,
  selected,
  onChoose,
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
  onChoose: (id: string) => void
  onClose: () => void
  onReturn: (id: string) => void
  placed: (id: string) => boolean
  onAdd: (kind: KindDef) => void
  canAdd: (kind: KindDef) => boolean
  folded: (id: string) => boolean
}) {
  const found = selected ? itemIndex(map).get(selected) : undefined
  const shelf = selected?.startsWith("shelf:") ? kindOf(selected.slice(6)) : undefined
  return (
    <aside
      aria-label="Inspector"
      className="flex min-h-0 min-w-0 flex-col border-hairline max-lg:border-t lg:border-l"
    >
      <div className="flex h-10 shrink-0 items-center gap-2 border-b border-hairline px-3">
        <span className="eyebrow">
          {found ? kindOf(found.kind)?.label : shelf ? "Shelf" : "This server"}
        </span>
        <span className="flex-1" />
        <IconAction label="Close the inspector (I)" className="size-7" onClick={onClose}>
          <Cross />
        </IconAction>
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto">
        {found ? (
          <ItemInspector
            map={map}
            item={found.item}
            kind={found.kind}
            onChoose={onChoose}
            placed={placed(found.item.id)}
            onReturn={() => onReturn(found.item.id)}
          />
        ) : shelf ? (
          <ShelfInspector
            map={map}
            kind={shelf}
            onChoose={onChoose}
            onAdd={onAdd}
            canAdd={canAdd(shelf)}
            folded={folded(shelfId(shelf.key))}
          />
        ) : (
          <ServerInspector map={map} kinds={kinds} />
        )}
      </div>
    </aside>
  )
}

function ItemInspector({
  map,
  item,
  kind,
  onChoose,
  placed,
  onReturn,
}: {
  map: ServerMap
  item: MapItem
  kind: string
  onChoose: (id: string) => void
  placed: boolean
  onReturn: () => void
}) {
  const related = relatedTo(map, item.id)
  return (
    <div className="animate-rise space-y-5 p-3">
      <div className="flex min-w-0 items-start gap-3">
        <ItemMark item={item} kind={kind} className="size-10" />
        <div className="min-w-0 flex-1 space-y-1">
          <div className="text-title font-semibold break-words">{item.name}</div>
          <Status tone={TONE_DOT[item.tone]} label={item.state} />
        </div>
      </div>
      {item.detail && <p className="text-body break-words text-muted-foreground">{item.detail}</p>}
      <div className="flex flex-wrap gap-2">
        <Button size="sm" asChild>
          <Link href={item.href}>
            Open
            <ArrowUpRight />
          </Link>
        </Button>
        {placed && (
          <Button size="sm" variant="outline" onClick={onReturn}>
            <RotateCounterClockwise />
            Back on its shelf
          </Button>
        )}
      </div>

      <section className="space-y-2">
        <h3 className="eyebrow">Wired to</h3>
        {related.length === 0 ? (
          <p className="text-hint text-muted-foreground">
            The server knows of nothing on the map this is wired to.
          </p>
        ) : (
          <ul className="-mx-1.5 space-y-0.5">
            {related.map((one) => (
              <li key={one.item.id}>
                <button
                  type="button"
                  className="flex w-full min-w-0 items-center gap-2.5 rounded-md px-1.5 py-1.5 text-left focus-ring transition-colors hover:bg-row-hover"
                  onClick={() => onChoose(one.item.id)}
                >
                  <ItemMark item={one.item} kind={one.kind} />
                  <span className="min-w-0 flex-1">
                    <span className="block truncate text-body font-medium">{one.item.name}</span>
                    <span className="flex min-w-0 items-center gap-1.5 text-hint text-muted-foreground">
                      <StatusDot tone={TONE_DOT[one.item.tone]} />
                      <span className="truncate">
                        {linkSentence(one.via, one.direction)} · {one.item.state}
                      </span>
                    </span>
                  </span>
                </button>
              </li>
            ))}
          </ul>
        )}
      </section>
    </div>
  )
}

/** How a link reads from the chosen end: "routes to" one way, "routed from" the other. */
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
      return "a container of it"
    case "serves":
      return "served by"
  }
  return forward
}

function ShelfInspector({
  map,
  kind,
  onChoose,
  onAdd,
  canAdd,
  folded,
}: {
  map: ServerMap
  kind: KindDef
  onChoose: (id: string) => void
  onAdd: (kind: KindDef) => void
  canAdd: boolean
  folded: boolean
}) {
  const reading = map.kinds.find((k) => k.key === kind.key)
  const Glyph = KIND_GLYPH[kind.key]
  const items = reading?.items ?? []
  const total = reading ? Math.max(reading.total, items.length) : 0
  const down = items.filter((item) => item.tone === "danger").length
  return (
    <div className="animate-rise space-y-4 p-3">
      <div className="flex items-center gap-3">
        <span className="flex size-10 items-center justify-center rounded-lg border border-hairline bg-background text-muted-foreground [&>svg]:size-5">
          {Glyph && <Glyph aria-hidden />}
        </span>
        <div className="min-w-0">
          <div className="text-title font-semibold">{kind.label}</div>
          <div className="text-hint text-muted-foreground">
            {kind.tool
              ? kind.tool.detail
              : reading?.state === "unread"
                ? "Not read"
                : plural(total, kind.noun, `${kind.noun}s`)}
            {down > 0 && <span className="text-destructive"> · {down} down</span>}
            {folded && " · folded"}
          </div>
        </div>
      </div>
      {reading?.reason && (
        <p
          className={cn(
            "text-hint",
            reading.state === "unread" ? "text-warning" : "text-muted-foreground",
          )}
        >
          {reading.reason}
        </p>
      )}
      <div className="flex flex-wrap gap-2">
        <Button size="sm" variant="outline" asChild>
          <Link href={kind.href}>
            Open {kind.label}
            <ArrowUpRight />
          </Link>
        </Button>
        {kind.add && canAdd && (
          <Button size="sm" variant="outline" onClick={() => onAdd(kind)}>
            <Plus />
            {kind.add.label}
          </Button>
        )}
      </div>
      {items.length > 0 && (
        <ul className="-mx-1.5 space-y-0.5">
          {items.slice(0, 60).map((item) => (
            <li key={item.id}>
              <button
                type="button"
                className="flex w-full min-w-0 items-center gap-2 rounded-md px-1.5 py-1 text-left focus-ring transition-colors hover:bg-row-hover"
                onClick={() => onChoose(item.id)}
              >
                <StatusDot tone={TONE_DOT[item.tone]} />
                <span className="min-w-0 flex-1 truncate text-body">{item.name}</span>
                <span className="shrink-0 text-hint text-muted-foreground">{item.state}</span>
              </button>
            </li>
          ))}
          {items.length > 60 && (
            <li className="px-1.5 py-1 text-hint text-muted-foreground">
              and {items.length - 60} more on its page
            </li>
          )}
        </ul>
      )}
    </div>
  )
}

function ServerInspector({ map, kinds }: { map: ServerMap; kinds: KindDef[] }) {
  const { host, snapshot } = useMetrics()
  const readings = new Map(map.kinds.map((k) => [k.key, k]))
  return (
    <div className="animate-rise space-y-5 p-3">
      <div className="flex items-center gap-3">
        <WireHost size="md" />
        <div className="min-w-0">
          <div className="truncate text-title font-semibold">
            {host?.hostname ?? map.host.hostname}
          </div>
          <div className="truncate text-hint text-muted-foreground">
            {host ? `${host.platform} ${host.platformVersion} · ${host.kernelVersion}` : "—"}
          </div>
        </div>
      </div>
      {snapshot && (
        <dl className="grid grid-cols-2 gap-x-4 gap-y-2 text-hint">
          <Fact label="Up">{duration(snapshot.uptimeSeconds)}</Fact>
          <Fact label="Load">{snapshot.cpu.loadAvg1.toFixed(2)}</Fact>
          <Fact label="Cores">{snapshot.cpu.cores}</Fact>
          <Fact label="Processes">{snapshot.procs.total}</Fact>
        </dl>
      )}
      <p className="text-hint text-muted-foreground">
        Choose a box to see what it is wired to. Drag a branch to move it with everything on it, or
        hold Alt to move one box alone.
      </p>
      {BRANCHES.map((branch) => {
        const here = kinds.filter((kind) => kind.branch === branch.key && !kind.tool)
        if (here.length === 0) return null
        return (
          <section key={branch.key} className="space-y-1.5">
            <h3 className="eyebrow">{branch.label}</h3>
            <ul className="space-y-1">
              {here.map((kind) => {
                const reading = readings.get(kind.key)
                const total = reading ? Math.max(reading.total, reading.items.length) : 0
                const down = reading?.items.filter((item) => item.tone === "danger").length ?? 0
                return (
                  <li key={kind.key} className="flex items-center gap-2 text-body">
                    <span className="min-w-0 flex-1 truncate text-muted-foreground">
                      {kind.label}
                    </span>
                    {down > 0 && (
                      <span className="numeric text-hint text-destructive">{down} down</span>
                    )}
                    <span className="numeric">
                      {reading?.state === "unread" ? "—" : total.toLocaleString("en-US")}
                    </span>
                  </li>
                )
              })}
            </ul>
          </section>
        )
      })}
    </div>
  )
}

function Fact({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="min-w-0">
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="numeric truncate text-foreground">{children}</dd>
    </div>
  )
}
