"use client"

import { useRef, type RefObject } from "react"
import { CloudUpload, Database, FolderOpen } from "@/components/icons"
import { bytes, plural, relativeTime } from "@/lib/format"
import type { BackupJob, BackupResource, BackupResourceKind, Container } from "@/lib/types"
import { cn } from "@/lib/utils"
import { useMediaQuery } from "@/hooks/use-mobile"
import { AnimatedBeam } from "@/components/ui/animated-beam"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { ProductLogo } from "@/components/product-logo"
import { WireHost, WireLink, WireMark, WireNode, WirePlaceholder } from "@/components/deploy/wire"
import { LastRun } from "@/components/backups/job-card"
import {
  DestinationGlyph,
  ResourceMark,
  destinations,
  resourceProducts,
  type Destination,
} from "@/components/backups/marks"
import {
  RESOURCE_KIND_GROUP,
  RESOURCE_KIND_LABEL,
  RESOURCE_KIND_ORDER,
  scheduleLabel,
} from "@/components/backups/shared"

type Line = { still: boolean; dashed?: boolean; tone: "default" | "success" | "warning" | "danger" }

/** One kind of thing on the server, and how much of it a job covers. */
type KindGroup = {
  kind: BackupResourceKind
  total: number
  /** Covered by an enabled job. */
  covered: number
  /** Covered only by a paused job, which is the same as not covered on the night it matters. */
  paused: number
  products: string[]
  /** A job covering one of these is taking a backup now. */
  running: boolean
}

/**
 * Where this server's data goes: what it has, by kind, wired into this server,
 * and this server wired out to every place a job writes — in the vocabulary
 * Notifications, Credentials and the configuration's request path already
 * speak (`deploy/wire`), so a reader who has understood one of those pictures
 * has understood this one.
 *
 * The lines are the state, and nothing else on them is decoration:
 *
 * - from a kind, **green** when an enabled job covers all of it, **amber**
 *   when only part of it is covered or the job that covers it is paused, and
 *   **dashed** when nothing does — the hop that is missing;
 * - to a destination, **green** when every job writing there last succeeded,
 *   **red** when one failed, **amber** when one has gone quiet, still and grey
 *   while they are paused, and dashed before anything has been written;
 * - and a pulse travels along both while a job is taking a backup, because
 *   that is when something is actually moving down them (§11 *live*).
 *
 * A server whose every archive is on its own disk gets a dashed ring where an
 * off-site copy would go: the disk failing is the thing a backup is for, and a
 * copy beside the original does not survive it. Pressing it (or any ring) is
 * the way to write that job.
 *
 * Pressing a kind narrows the coverage list below to it. Wide, the three
 * columns balance across the page; on a phone the coverage list already names
 * every thing, so the picture shrinks to its marks with the lines running down.
 */
export function ProtectionMap({
  jobs,
  resources,
  containers,
  onKind,
  onAdd,
}: {
  /** Worst first, as the cards below are, so the worst destination leads its column. */
  jobs: BackupJob[]
  resources: BackupResource[]
  containers: Container[]
  onKind: (kind: BackupResourceKind) => void
  onAdd?: () => void
}) {
  const wide = useMediaQuery("(min-width: 1024px)")
  const container = useRef<HTMLDivElement>(null)
  const hub = useRef<HTMLDivElement>(null)
  const running = new Set(jobs.filter((job) => job.lastRun?.status === "running").map((j) => j.id))
  const groups = kindGroups(resources, containers, running)
  const places = destinations(jobs)
  const busy = jobs.filter((job) => running.has(job.id))
  const next = jobs
    .filter((job) => job.enabled && job.nextRun)
    .map((job) => job.nextRun!)
    .sort()[0]
  const stored = jobs.reduce((sum, job) => sum + job.stored.bytes, 0)

  const hint =
    busy.length > 0 ? (
      <TextShimmer>{`Backing up ${busy.map((job) => job.name).join(", ")}`}</TextShimmer>
    ) : next ? (
      `next backup ${relativeTime(next)}`
    ) : jobs.length > 0 ? (
      "nothing scheduled"
    ) : (
      "nothing leaves this server yet"
    )
  const title =
    jobs.length > 0 ? `${plural(jobs.length, "job")} · ${bytes(stored)}` : "No backup jobs"

  const kinds = groups.map((group) => (
    <KindNode
      key={group.kind}
      group={group}
      compact={!wide}
      containerRef={container}
      hubRef={hub}
      onPress={() => onKind(group.kind)}
    />
  ))
  const targets = (
    <>
      {places.map((place) => (
        <DestinationNode
          key={place.key}
          destination={place}
          compact={!wide}
          containerRef={container}
          hubRef={hub}
        />
      ))}
      {!places.some((place) => place.offsite) && (
        <OffsiteRing
          compact={!wide}
          containerRef={container}
          hubRef={hub}
          hasJobs={jobs.length > 0}
          onAdd={onAdd}
        />
      )}
    </>
  )
  return (
    <Picture
      wide={wide}
      containerRef={container}
      hubRef={hub}
      title={title}
      hint={hint}
      from={kinds}
      fromLabel="What this server has"
      to={targets}
      toLabel="Where the archives go"
    />
  )
}

/**
 * The layout both pictures share: a column of things on the left wired into
 * this server, and this server wired out to a column on the right.
 *
 * Wide, the three balance around the lines across the page. Narrow, the words
 * go over the marks and the two columns become rows above and below this
 * server, so the lines run downwards over nothing but the page's own ground.
 */
function Picture({
  wide,
  containerRef,
  hubRef,
  title,
  hint,
  from,
  fromLabel,
  to,
  toLabel,
}: {
  wide: boolean
  containerRef: RefObject<HTMLDivElement | null>
  hubRef: RefObject<HTMLDivElement | null>
  title: React.ReactNode
  hint: React.ReactNode
  from: React.ReactNode
  fromLabel: string
  to: React.ReactNode
  toLabel: string
}) {
  return (
    <div ref={containerRef} className="relative mx-auto max-w-6xl">
      {wide ? (
        // The gaps have a floor wide enough that a wire from the bottom of
        // the column has turned level before it reaches the words under
        // this server's mark; and the row is tall enough for those words,
        // which hang beneath the mark rather than sharing its line, when
        // the columns either side are short.
        <div className="grid min-h-44 grid-cols-[minmax(0,1fr)_minmax(6rem,0.45fr)_auto_minmax(6rem,0.45fr)_minmax(0,1.3fr)] items-center">
          <div role="group" aria-label={fromLabel} className="flex flex-col items-end gap-3">
            {from}
          </div>
          <div aria-hidden />
          <WireNode
            nodeRef={hubRef}
            align="center"
            mark={<WireHost />}
            eyebrow="This server"
            title={title}
            hint={hint}
          />
          <div aria-hidden />
          <div role="group" aria-label={toLabel} className="flex flex-col gap-5">
            {to}
          </div>
        </div>
      ) : (
        <div className="flex flex-col items-center text-center">
          <p className="eyebrow">This server</p>
          <p className="text-body leading-snug font-medium">{title}</p>
          <p className="text-hint leading-snug text-muted-foreground">{hint}</p>
          <div
            role="group"
            aria-label={fromLabel}
            className="mt-5 flex flex-wrap justify-center gap-3"
          >
            {from}
          </div>
          <div ref={hubRef} className="relative z-10 mt-10 flex">
            <WireHost />
          </div>
          <div
            role="group"
            aria-label={toLabel}
            className="mt-10 flex flex-wrap justify-center gap-4"
          >
            {to}
          </div>
        </div>
      )}
    </div>
  )
}

function kindGroups(
  resources: BackupResource[],
  containers: Container[],
  running: Set<number>,
): KindGroup[] {
  return RESOURCE_KIND_ORDER.flatMap((kind) => {
    const items = resources.filter((r) => r.kind === kind)
    if (items.length === 0) return []
    return [
      {
        kind,
        total: items.length,
        covered: items.filter((r) => r.protected).length,
        paused: items.filter((r) => !r.protected && r.coveredBy.some((c) => !c.enabled)).length,
        products: [...new Set(items.flatMap((r) => resourceProducts(r, containers, resources)))],
        running: items.some((r) => r.coveredBy.some((c) => c.enabled && running.has(c.jobId))),
      },
    ]
  })
}

function kindLine(group: KindGroup): Line {
  if (group.running) return { still: false, tone: "default" }
  if (group.covered === group.total) return { still: true, tone: "success" }
  if (group.covered > 0 || group.paused > 0) return { still: true, tone: "warning" }
  return { still: true, dashed: true, tone: "default" }
}

function kindWords(group: KindGroup) {
  if (group.running) return <TextShimmer>backing up now</TextShimmer>
  if (group.covered === group.total)
    return group.total === 1 ? "backed up" : `all ${group.total} backed up`
  const paused = group.paused > 0 ? ` · ${group.paused} paused` : ""
  if (group.covered === 0) {
    const none = group.total === 1 ? "not backed up" : `none of ${group.total} backed up`
    return `${none}${paused}`
  }
  return `${group.covered} of ${group.total} backed up${paused}`
}

/**
 * One kind of thing on this server, drawn as the products it holds — the
 * engines its databases speak, the images its volumes belong to — with its
 * line into this server. The name is a button that narrows the coverage list
 * to the kind.
 */
function KindNode({
  group,
  compact,
  containerRef,
  hubRef,
  onPress,
}: {
  group: KindGroup
  compact: boolean
  containerRef: RefObject<HTMLDivElement | null>
  hubRef: RefObject<HTMLDivElement | null>
  onPress: () => void
}) {
  const mark = useRef<HTMLDivElement>(null)
  const line = kindLine(group)
  const name = RESOURCE_KIND_GROUP[group.kind]
  // On a phone seven stacks do not fit across, so a kind is its first product.
  const drawn = (
    <ResourceMark kind={group.kind} ids={compact ? group.products.slice(0, 1) : group.products} />
  )
  const beam = (
    <AnimatedBeam
      containerRef={containerRef}
      fromRef={mark}
      toRef={hubRef}
      shape="s"
      still={line.still}
      dashed={line.dashed}
      tone={line.tone}
      duration={2.4}
    />
  )
  const words = kindWords(group)
  if (compact)
    return (
      <div className="min-w-0">
        {beam}
        <button
          type="button"
          onClick={onPress}
          aria-label={`Show ${name.toLowerCase()} in coverage`}
          className="rounded-lg focus-ring"
        >
          <div ref={mark} className="relative z-10 flex">
            {drawn}
          </div>
        </button>
      </div>
    )
  return (
    <div className="max-w-full min-w-0">
      {beam}
      <WireNode
        nodeRef={mark}
        align="end"
        mark={drawn}
        title={
          <button
            type="button"
            onClick={onPress}
            aria-label={`Show ${name.toLowerCase()} in coverage`}
            className="rounded-sm focus-ring hover:underline"
          >
            {name}
          </button>
        }
        hint={
          <span
            className={cn(
              line.tone === "warning" && "text-warning",
              line.tone === "success" && "text-success",
            )}
          >
            {words}
          </span>
        }
      />
    </div>
  )
}

function destinationLine(place: Destination): Line {
  const jobs = place.jobs
  if (jobs.some((job) => job.lastRun?.status === "running"))
    return { still: false, tone: "default" }
  const active = jobs.filter((job) => job.enabled || !job.schedule)
  if (active.length === 0) return { still: true, tone: "default" }
  if (active.some((job) => job.lastRun?.status === "failed")) return { still: true, tone: "danger" }
  if (active.some((job) => job.overdue)) return { still: true, tone: "warning" }
  if (active.every((job) => !job.lastRun)) return { still: true, dashed: true, tone: "default" }
  return { still: true, tone: "success" }
}

/**
 * One place archives land, drawn as the service that holds them, with the
 * jobs that write there and what they keep. Its last outcome sits at the far
 * end of the column, in the colour of how it went — the worst of its jobs',
 * since the jobs come worst first.
 */
function DestinationNode({
  destination,
  compact,
  containerRef,
  hubRef,
  outcome = true,
}: {
  destination: Destination
  compact: boolean
  containerRef: RefObject<HTMLDivElement | null>
  hubRef: RefObject<HTMLDivElement | null>
  /** The last run at the column's far end; a job's own page has it on its identity line. */
  outcome?: boolean
}) {
  const mark = useRef<HTMLDivElement>(null)
  const line = destinationLine(destination)
  const { jobs } = destination
  const held = jobs.reduce((sum, job) => sum + job.stored.bytes, 0)
  const drawn = (
    <WireMark tone="logo" size={compact ? "sm" : "md"}>
      <DestinationGlyph destination={destination} />
    </WireMark>
  )
  const beam = (
    <AnimatedBeam
      containerRef={containerRef}
      fromRef={hubRef}
      toRef={mark}
      shape="s"
      still={line.still}
      dashed={line.dashed}
      tone={line.tone}
      duration={2.2}
      delay={0.4}
    />
  )
  const writers = jobs.length === 1 ? jobs[0].name : plural(jobs.length, "job")
  if (compact)
    return (
      <div className="min-w-0">
        {beam}
        <div ref={mark} className="relative z-10 flex">
          {drawn}
        </div>
        <span className="sr-only">{`${destination.service}: ${destination.place} · ${writers}`}</span>
      </div>
    )
  return (
    <div className="min-w-0">
      {beam}
      <WireNode
        nodeRef={mark}
        mark={drawn}
        eyebrow={destination.service}
        title={<span className="block truncate font-mono">{destination.place}</span>}
        hint={
          <span className="block truncate">
            {writers} · <span className="numeric">{bytes(held)}</span>
          </span>
        }
        aside={outcome ? <LastRun job={jobs[0]} /> : undefined}
      />
    </div>
  )
}

/**
 * Where an off-site copy would go, while there is none: a dashed ring holding
 * a cloud, pressed to write the job that makes one.
 */
function OffsiteRing({
  compact,
  containerRef,
  hubRef,
  hasJobs,
  onAdd,
}: {
  compact: boolean
  containerRef: RefObject<HTMLDivElement | null>
  hubRef: RefObject<HTMLDivElement | null>
  hasJobs: boolean
  onAdd?: () => void
}) {
  const mark = useRef<HTMLDivElement>(null)
  const drawn = (
    <WireLink label="Write a job that copies off this server" onClick={onAdd}>
      <WirePlaceholder size={compact ? "sm" : "md"} fallback={CloudUpload} />
    </WireLink>
  )
  const beam = (
    <AnimatedBeam
      containerRef={containerRef}
      fromRef={hubRef}
      toRef={mark}
      shape="s"
      still
      dashed
    />
  )
  if (compact)
    return (
      <div className="min-w-0">
        {beam}
        <div ref={mark} className="relative z-10 flex">
          {drawn}
        </div>
      </div>
    )
  return (
    <div className="min-w-0">
      {beam}
      <WireNode
        nodeRef={mark}
        mark={drawn}
        eyebrow="Off-site"
        title={<span className="font-normal text-muted-foreground">No copy off this server</span>}
        hint={hasJobs ? "every archive is on this server's disk" : "a bucket on S3 or Backblaze B2"}
      />
    </div>
  )
}

/** One thing a job takes, as the job's picture draws it. */
type Source = {
  key: string
  /** What kind of capture it is: a volume, a native dump, a SQLite snapshot. */
  eyebrow: string
  name: string
  detail: string
  mono: boolean
  mark: React.ReactNode
}

/**
 * One job's picture: what it takes — each path as the thing on the server it
 * is, each native dump as its database's engine, each SQLite snapshot as
 * SQLite — wired into this server, and this server wired out to where the job
 * writes. The page's own picture, narrowed to one job.
 *
 * The wire to the destination is the last run's: green when it landed, red
 * when it did not, still and grey while the job is paused. The wires in are
 * grey, because a failed run says the archive did not arrive and not which of
 * the things going into it broke — the log says that. Everything pulses while
 * a backup is being taken.
 */
export function JobMap({
  job,
  resources,
  containers,
}: {
  job: BackupJob
  resources: BackupResource[]
  containers: Container[]
}) {
  const wide = useMediaQuery("(min-width: 1024px)")
  const container = useRef<HTMLDivElement>(null)
  const hub = useRef<HTMLDivElement>(null)
  const sources = jobSources(job, resources, containers)
  const shown = sources.slice(0, 6)
  const rest = sources.length - shown.length
  const running = job.lastRun?.status === "running"
  const place = destinations([job])[0]
  const paused = job.pauseContainers ?? []

  const title = running ? <TextShimmer>Backing up now</TextShimmer> : scheduleLabel(job.schedule)
  const hint =
    paused.length > 0
      ? `pauses ${paused.join(", ")} while it archives`
      : !job.schedule
        ? "runs by hand"
        : job.enabled && job.nextRun
          ? `next ${relativeTime(job.nextRun)}`
          : "schedule paused"

  const from = (
    <>
      {shown.map((source) => (
        <SourceNode
          key={source.key}
          source={source}
          compact={!wide}
          running={running}
          containerRef={container}
          hubRef={hub}
        />
      ))}
      {rest > 0 && (
        <p className="text-hint text-muted-foreground lg:pr-12">{`and ${plural(rest, "more")}`}</p>
      )}
    </>
  )
  return (
    <Picture
      wide={wide}
      containerRef={container}
      hubRef={hub}
      title={title}
      hint={hint}
      from={from}
      fromLabel={`What ${job.name} takes`}
      to={
        <DestinationNode
          destination={place}
          compact={!wide}
          containerRef={container}
          hubRef={hub}
          outcome={false}
        />
      }
      toLabel="Where its archives go"
    />
  )
}

function jobSources(
  job: BackupJob,
  resources: BackupResource[],
  containers: Container[],
): Source[] {
  const mark = (resource: BackupResource) => (
    <ResourceMark kind={resource.kind} ids={resourceProducts(resource, containers, resources)} />
  )
  const paths: Source[] = job.sources.map((path) => {
    // The thing on the server whose directory this is, or that it is inside.
    const resource = resources.find(
      (r) =>
        r.kind !== "database" &&
        r.paths?.some((root) => path === root || path.startsWith(`${root.replace(/\/$/, "")}/`)),
    )
    return {
      key: `path:${path}`,
      eyebrow: resource ? RESOURCE_KIND_LABEL[resource.kind] : "Directory",
      name: resource?.name ?? basename(path),
      detail: path,
      mono: true,
      mark: resource ? mark(resource) : <ProductLogo size="sm" fallback={FolderOpen} />,
    }
  })
  const dumps: Source[] = (job.databaseDumps ?? []).map((id) => {
    const resource = resources.find((r) => r.kind === "database" && r.connectionId === id)
    return {
      key: `dump:${id}`,
      eyebrow: "Native dump",
      name: resource?.name ?? `Connection ${id}`,
      detail: resource?.detail ?? "a saved connection",
      mono: false,
      mark: resource ? mark(resource) : <ProductLogo size="sm" fallback={Database} />,
    }
  })
  const snapshots: Source[] = (job.sqlitePaths ?? []).map((path) => ({
    key: `sqlite:${path}`,
    eyebrow: "SQLite snapshot",
    name: basename(path),
    detail: path,
    mono: true,
    mark: <ProductLogo id="sqlite" size="sm" />,
  }))
  return [...dumps, ...paths, ...snapshots]
}

function basename(path: string) {
  return path.replace(/\/+$/, "").split("/").pop() || path
}

function SourceNode({
  source,
  compact,
  running,
  containerRef,
  hubRef,
}: {
  source: Source
  compact: boolean
  running: boolean
  containerRef: RefObject<HTMLDivElement | null>
  hubRef: RefObject<HTMLDivElement | null>
}) {
  const mark = useRef<HTMLDivElement>(null)
  const beam = (
    <AnimatedBeam
      containerRef={containerRef}
      fromRef={mark}
      toRef={hubRef}
      shape="s"
      still={!running}
      duration={2.4}
    />
  )
  if (compact)
    return (
      <div className="min-w-0">
        {beam}
        <div ref={mark} className="relative z-10 flex">
          {source.mark}
        </div>
        <span className="sr-only">{`${source.eyebrow}: ${source.name}, ${source.detail}`}</span>
      </div>
    )
  return (
    <div className="max-w-full min-w-0">
      {beam}
      <WireNode
        nodeRef={mark}
        align="end"
        mark={source.mark}
        eyebrow={source.eyebrow}
        title={source.name}
        hint={
          <span className={cn("block truncate", source.mono && "font-mono")} title={source.detail}>
            {source.detail}
          </span>
        }
      />
    </div>
  )
}
