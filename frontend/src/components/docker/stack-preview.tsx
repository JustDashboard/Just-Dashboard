"use client"

import {
  ArrowRight,
  CheckCircle,
  Database,
  Information,
  Play,
  Plus,
  RefreshClockwise,
  Trash,
  Warning,
} from "@/components/icons"
import { get } from "@/lib/api"
import { plural } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { DeployPreview, ServiceChange } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useMediaQuery } from "@/hooks/use-mobile"
import { Panel, PanelBody, PanelFooter, PanelHeader } from "@/components/panel"
import { EmptyNote, ErrorState, LoadingRows } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import {
  stickyTableHeader,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { ComposeDiff, DiffCount, ServiceLabel } from "@/components/docker/stack-diff"
import { bucketTone, type ServiceReading } from "@/components/docker/stack-service-readings"
import { diffCounts, splitImage } from "@/components/docker/stack-views"

type Kind = ServiceChange["change"]

/**
 * What each change is called, the colour it is drawn in and its mark. The
 * colour says what it costs: a removal destroys, a recreate interrupts, a
 * create or a start only adds, and unchanged is the quiet rest.
 */
const KIND: Record<
  Kind,
  { word: string; color: string; text: string; icon: React.ComponentType<{ className?: string }> }
> = {
  remove: { word: "Remove", color: "var(--destructive)", text: "text-destructive", icon: Trash },
  recreate: {
    word: "Recreate",
    color: "var(--warning)",
    text: "text-warning",
    icon: RefreshClockwise,
  },
  create: { word: "Create", color: "var(--chart-2)", text: "text-[var(--chart-2)]", icon: Plus },
  start: { word: "Start", color: "var(--success)", text: "text-success", icon: Play },
  unchanged: {
    word: "Unchanged",
    color: "color-mix(in oklab, var(--muted-foreground) 30%, transparent)",
    text: "text-muted-foreground",
    icon: CheckCircle,
  },
}

/** Most consequential first: what is removed, then replaced, created, started, and the rest. */
const ORDER: Kind[] = ["remove", "recreate", "create", "start", "unchanged"]

/**
 * The sentence the view opens on, built from the counts rather than taken
 * from the server's summary: that one also lists what stays and what
 * survives, which the bar and the volumes beside it already say.
 */
function headline(preview: DeployPreview) {
  const parts = [
    preview.remove > 0 && `remove ${plural(preview.remove, "orphan")}`,
    preview.recreate > 0 && `recreate ${plural(preview.recreate, "service")}`,
    preview.create > 0 &&
      `create ${preview.recreate > 0 ? preview.create : plural(preview.create, "service")}`,
    preview.start > 0 &&
      `start ${preview.recreate + preview.create > 0 ? preview.start : plural(preview.start, "service")}`,
  ].filter(Boolean) as string[]
  if (parts.length === 0) return "Deploy would change nothing"
  const last = parts.pop()
  return `Deploy will ${parts.length ? `${parts.join(", ")} and ${last}` : last}`
}

/**
 * What pressing Deploy is going to do, before it does it.
 *
 * A compose deploy is the most consequential button on this page and the
 * least predictable: `up` recreates whatever it decides has changed, and
 * what it decides is invisible until afterwards. So the view opens on that
 * decision as one sentence over a bar of the stack's services, each in the
 * colour of what happens to it; lists each service with why, what it is
 * running now and the image it moves to; and closes on the file's changes,
 * headed by the service each one changes, beside the volumes — the one
 * number that means data is destroyed, said whether or not it is zero.
 *
 * Deploy is here too, with a fresh read beside it: the reader who has just
 * read what will happen should not have to scroll back up to act on it.
 */
export function StackPreview({
  stack,
  epoch,
  readings,
  productOf,
  onDeploy,
  busy,
  onEdit,
}: {
  stack: string
  /** Changes after each compose command the page runs: the comparison is read again. */
  epoch: number
  readings: ServiceReading[]
  productOf: (service: string) => string | undefined
  /** Absent where the reader may not deploy. */
  onDeploy?: () => void
  busy: boolean
  onEdit: () => void
}) {
  const wide = useMediaQuery("(min-width: 1280px)")
  const { data, error, loading, refresh } = usePoll<DeployPreview>(
    (signal) =>
      get<DeployPreview>(`/docker/stacks/${encodeURIComponent(stack)}/preview`, undefined, signal),
    0,
    [stack, epoch],
  )

  if (loading && !data) return <LoadingRows />
  if (error) return <ErrorState error={error} />
  if (!data) return null

  const services = [...data.services].sort(
    (a, b) => ORDER.indexOf(a.change) - ORDER.indexOf(b.change) || a.name.localeCompare(b.name),
  )
  const reading = (name: string) => readings.find((r) => r.key === name)
  const counts = diffCounts(data.diff)
  const destroys = data.volumesRemoved.length > 0
  const tone = destroys || data.remove > 0 ? "danger" : data.recreate > 0 ? "warning" : "default"

  return (
    // This content mounts when its tab opens — a disclosure — so its arrival
    // is said once, quietly, by the token that means "not here a moment ago".
    <div className="flex min-w-0 animate-rise flex-col gap-6 pb-6">
      <section
        aria-label="What Deploy will do"
        className="flex min-w-0 flex-col gap-3 border-b border-hairline pb-5"
      >
        <div className="flex min-w-0 flex-wrap items-start justify-between gap-x-6 gap-y-3">
          <div className="min-w-0">
            <p className="eyebrow">If you deploy now</p>
            <h2
              className={cn(
                "mt-1 text-title leading-tight font-semibold tracking-tight",
                tone === "danger" && "text-destructive",
              )}
            >
              {headline(data)}
            </h2>
            <p className="mt-1 text-hint leading-relaxed text-muted-foreground">
              {data.diffAgainst
                ? `Compared against ${data.diffAgainst}.`
                : "No earlier deployment is recorded, so this is read against what is running now."}
            </p>
          </div>
          <div className="flex shrink-0 items-center gap-1.5">
            <Button size="sm" variant="ghost" onClick={refresh} disabled={loading}>
              <RefreshClockwise className="size-3.5" />
              Check again
            </Button>
            {onDeploy && (
              <Button size="sm" onClick={onDeploy} pending={busy}>
                <Play className="size-3.5" />
                Deploy
              </Button>
            )}
          </div>
        </div>
        <ChangeBar services={services} />
        <ul className="flex flex-wrap items-center gap-x-4 gap-y-1 text-hint">
          {ORDER.map((kind) => {
            const n = services.filter((s) => s.change === kind).length
            if (n === 0) return null
            return (
              <li key={kind} className="flex items-center gap-1.5">
                <span
                  aria-hidden
                  className="size-2 rounded-sm"
                  style={{ background: KIND[kind].color }}
                />
                <span className="text-muted-foreground">{KIND[kind].word}</span>
                <span className="numeric font-medium">{n}</span>
              </li>
            )
          })}
          <li
            className={cn(
              "flex items-center gap-1.5",
              destroys ? "text-destructive" : "text-muted-foreground",
            )}
          >
            {destroys ? (
              <Warning aria-hidden className="size-3.5" />
            ) : (
              <CheckCircle aria-hidden className="size-3.5 text-success" />
            )}
            {destroys
              ? `${plural(data.volumesRemoved.length, "volume")} destroyed`
              : "No volume is removed"}
          </li>
        </ul>
      </section>

      <Panel aria-label="Services">
        <PanelHeader
          title={
            <>
              Services
              <span className="numeric ml-2 text-body font-normal text-muted-foreground">
                {services.length}
              </span>
            </>
          }
        />
        <PanelBody flush>
          {wide ? (
            <Table className="table-fixed">
              <TableHeader className={stickyTableHeader}>
                <TableRow>
                  <TableHead className="w-48 pl-5">Service</TableHead>
                  <TableHead className="w-36">Change</TableHead>
                  <TableHead>Why</TableHead>
                  <TableHead className="w-60">Image</TableHead>
                  <TableHead className="w-44 pr-5">Now</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {services.map((change) => (
                  <ChangeRow
                    key={change.name}
                    change={change}
                    reading={reading(change.name)}
                    product={productOf(change.name)}
                  />
                ))}
              </TableBody>
            </Table>
          ) : (
            <ul className="divide-y divide-hairline">
              {services.map((change) => (
                <ChangeItem
                  key={change.name}
                  change={change}
                  reading={reading(change.name)}
                  product={productOf(change.name)}
                />
              ))}
            </ul>
          )}
        </PanelBody>
        <PanelFooter className="text-hint text-muted-foreground">
          Compose makes the final call; this is what it is expected to do.
        </PanelFooter>
      </Panel>

      <div className="grid min-w-0 gap-6 xl:grid-cols-[minmax(0,1fr)_20rem]">
        <Panel plain aria-label="Compose file changes">
          <PanelHeader
            title="Compose file changes"
            actions={
              data.diff.length > 0 && <DiffCount added={counts.added} removed={counts.removed} />
            }
          />
          <PanelBody className="pt-2">
            {data.diff.length > 0 ? (
              <ComposeDiff lines={data.diff} productOf={productOf} />
            ) : (
              <EmptyNote>
                {data.diffAgainst
                  ? "The compose file is the one the last deployment used."
                  : "There is no earlier file to compare this one with."}
              </EmptyNote>
            )}
            <Button size="xs" variant="ghost" className="mt-3" onClick={onEdit}>
              Open the compose file
              <ArrowRight className="size-3" />
            </Button>
          </PanelBody>
        </Panel>

        <div className="flex min-w-0 flex-col gap-6">
          <Panel plain aria-label="Volumes">
            <PanelHeader title="Volumes" />
            <PanelBody className="pt-2">
              <Volumes preview={data} />
            </PanelBody>
          </Panel>
          {data.caveats.length > 0 && (
            <Panel plain aria-label="What this cannot know">
              <PanelHeader title="What this cannot know" />
              <PanelBody className="space-y-2 pt-2">
                {data.caveats.map((caveat, i) => (
                  <p
                    key={i}
                    className="flex items-start gap-2 text-hint leading-relaxed text-muted-foreground"
                  >
                    <Information aria-hidden className="mt-0.5 size-3 shrink-0" />
                    <span>{caveat}</span>
                  </p>
                ))}
              </PanelBody>
            </Panel>
          )}
        </div>
      </div>
    </div>
  )
}

/**
 * The stack's services as equal steps of one bar, each in the colour of what
 * Deploy does to it: a stack about to recreate four of six is mostly amber
 * before a word of it is read.
 */
function ChangeBar({ services }: { services: ServiceChange[] }) {
  return (
    <div
      role="img"
      aria-label={services.map((s) => `${s.name}: ${KIND[s.change].word}`).join(", ")}
      className="flex h-2.5 w-full gap-0.5"
    >
      {services.map((service) => (
        <span
          key={service.name}
          title={`${service.name} · ${KIND[service.change].word}`}
          className="h-full min-w-0 flex-1 rounded-sm first:rounded-l-md last:rounded-r-md"
          style={{ background: KIND[service.change].color }}
        />
      ))}
    </div>
  )
}

function ChangeWord({ kind }: { kind: Kind }) {
  const { word, text, icon: Icon } = KIND[kind]
  return (
    <span className={cn("inline-flex items-center gap-1.5 text-body font-medium", text)}>
      <Icon aria-hidden className="size-3.5 shrink-0" />
      {word}
    </span>
  )
}

/** Why: the keys that changed, as the tags they are, over the server's sentence. */
function Why({ change }: { change: ServiceChange }) {
  return (
    <div className="min-w-0">
      {change.fields.length > 0 && (
        <span className="mb-1 flex flex-wrap gap-1">
          {change.fields.map((field) => (
            <Tag key={field} tone={change.change === "recreate" ? "warning" : "default"}>
              {field}
            </Tag>
          ))}
        </span>
      )}
      <p
        className={cn(
          "text-hint leading-relaxed text-muted-foreground",
          change.change === "unchanged" && "line-clamp-1",
        )}
        title={change.reason}
      >
        {change.reason}
      </p>
    </div>
  )
}

/** The image the service runs, and where it moves to when the tag changed. */
function ImageCell({ change, reading }: { change: ServiceChange; reading?: ServiceReading }) {
  if (change.imageBefore && change.imageAfter) {
    const before = splitImage(change.imageBefore)
    const after = splitImage(change.imageAfter)
    if (before.repo === after.repo) {
      return (
        <span className="flex min-w-0 flex-wrap items-baseline gap-x-1.5 font-mono text-hint">
          <span className="truncate">{after.repo}</span>
          <span className="text-destructive line-through decoration-1">{before.tag}</span>
          <ArrowRight aria-hidden className="size-3 shrink-0 self-center text-muted-foreground" />
          <span className="text-success">{after.tag}</span>
        </span>
      )
    }
    return (
      <span className="block min-w-0 font-mono text-hint">
        <span className="block truncate text-destructive line-through">{change.imageBefore}</span>
        <span className="block truncate text-success">{change.imageAfter}</span>
      </span>
    )
  }
  const image = reading?.service.image
  return (
    <span className="block truncate font-mono text-hint text-muted-foreground" title={image}>
      {image || "built from source"}
    </span>
  )
}

/** What the service is doing now, so "recreate" is read as "interrupt this running one". */
function NowCell({ change, reading }: { change: ServiceChange; reading?: ServiceReading }) {
  if (!reading) {
    return <span className="text-hint text-muted-foreground">Not in this stack now</span>
  }
  const up = reading.state === "running"
  return (
    <span className="block min-w-0">
      <Status tone={bucketTone(reading.bucket)} label={reading.word} live={up} />
      {up && change.change === "recreate" && (
        <span className="mt-0.5 block text-hint text-muted-foreground">
          down while it is replaced
        </span>
      )}
    </span>
  )
}

function ChangeRow({
  change,
  reading,
  product,
}: {
  change: ServiceChange
  reading?: ServiceReading
  product?: string
}) {
  const quiet = change.change === "unchanged"
  return (
    <TableRow data-change={change.change} className={cn(quiet && "text-muted-foreground")}>
      <TableCell className="py-2.5 pl-5">
        <ServiceLabel name={change.name} product={product} muted={quiet} />
      </TableCell>
      <TableCell className="py-2.5">
        <ChangeWord kind={change.change} />
      </TableCell>
      <TableCell className="py-2.5 whitespace-normal">
        <Why change={change} />
      </TableCell>
      <TableCell className="py-2.5">
        <ImageCell change={change} reading={reading} />
      </TableCell>
      <TableCell className="py-2.5 pr-5">
        <NowCell change={change} reading={reading} />
      </TableCell>
    </TableRow>
  )
}

function ChangeItem({
  change,
  reading,
  product,
}: {
  change: ServiceChange
  reading?: ServiceReading
  product?: string
}) {
  const quiet = change.change === "unchanged"
  return (
    <li data-change={change.change} className="flex min-w-0 flex-col gap-1.5 px-5 py-3">
      <span className="flex min-w-0 items-center justify-between gap-3">
        <ServiceLabel name={change.name} product={product} muted={quiet} />
        <ChangeWord kind={change.change} />
      </span>
      <Why change={change} />
      <span className="flex min-w-0 flex-wrap items-center justify-between gap-x-4 gap-y-1">
        <ImageCell change={change} reading={reading} />
        <NowCell change={change} reading={reading} />
      </span>
    </li>
  )
}

/** Every named volume, kept or destroyed — the destroyed ones first and in red. */
function Volumes({ preview }: { preview: DeployPreview }) {
  const rows = [
    ...preview.volumesRemoved.map((name) => ({ name, removed: true })),
    ...preview.volumesKept.map((name) => ({ name, removed: false })),
  ]
  if (rows.length === 0) {
    return <EmptyNote>This stack keeps no named volumes, so a deploy has none to remove.</EmptyNote>
  }
  return (
    <ul className="divide-y divide-hairline">
      {rows.map((row) => (
        <li key={row.name} className="flex min-w-0 items-center gap-2.5 py-2 first:pt-0">
          <Database
            aria-hidden
            className={cn(
              "size-3.5 shrink-0",
              row.removed ? "text-destructive" : "text-muted-foreground",
            )}
          />
          <span className="min-w-0 flex-1 truncate font-mono text-hint">{row.name}</span>
          {row.removed ? (
            <span className="text-hint font-medium text-destructive">destroyed</span>
          ) : (
            <span className="flex items-center gap-1 text-hint text-success">
              <CheckCircle aria-hidden className="size-3" />
              kept
            </span>
          )}
        </li>
      ))}
    </ul>
  )
}
