"use client"

import Link from "next/link"
import { ArrowDown, ArrowUp, CheckCircle, Database } from "@/components/icons"
import { ProductLogo } from "@/components/product-logo"
import { StatusDot } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { VerbActions, type Verb } from "@/components/verbs"
import {
  STANDING_COLOR,
  holderHue,
  holderOf,
  isAnonymous,
  mountLines,
  sizeReading,
  splitName,
  standing,
  standingWords,
  volumeHref,
  volumeProduct,
  type Backup,
  type SortKey,
  type VolumeSort,
} from "@/components/docker/volumes"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { bytes, plural, relativeTime, timestamp } from "@/lib/format"
import type { VolumeDetail } from "@/lib/types"
import { cn } from "@/lib/utils"

/**
 * One height for every row, whatever it holds: two lines, which is a name over
 * its facts, two containers, a state over its backup. A row with one line
 * centres it in the same box, so the row under it never moves.
 */
const ROW = "h-16"

/**
 * Below `xl` the size and the menu give their padding to the name's column:
 * its second line has to hold "prune deletes · no backup" whole at 390px.
 */
const NARROW_PAD = "px-2"

/**
 * The volumes as a table: each row one volume read across — what it is, which
 * containers mount it and where, whether a prune would take it and whether a
 * backup covers it, and its size — that opens its page.
 *
 * It was a column of cards whose second line said "1 container" and nothing
 * about which, or at what path, so the question every volume is opened with —
 * what is using this — took a click to answer. The containers are the widest
 * column now, each by name with its state's dot and the path it sees the
 * volume at, linking to its page. The Images, Processes and Services tables
 * are the precedent: a row that opens a page is still read down its columns.
 *
 * Wide, every reading has its column, the creation date joining at `2xl`.
 * Below `xl` the standing and the backup go to the name's second line and the
 * row keeps its size and its menu — chosen once by the page rather than drawn
 * twice and hidden (§12). The size is a figure in a column of figures: it had
 * a bar under it against the largest volume, which the Held by band above
 * already draws as spans of one bar.
 */
export function VolumeTable({
  volumes,
  productOf,
  backups,
  wide,
  widest,
  arrived,
  removing,
  sort,
  onSort,
  verbs,
  onOpen,
  onHolder,
}: {
  volumes: VolumeDetail[]
  productOf: Map<string, string>
  backups: Map<string, Backup>
  wide: boolean
  widest: boolean
  arrived: Set<string>
  /** The volume a removal is in flight for, so its row can say so. */
  removing?: string
  sort: VolumeSort
  onSort: (key: SortKey) => void
  verbs: (volume: VolumeDetail) => Verb[]
  onOpen: (name: string) => void
  onHolder: (key: string) => void
}) {
  const head = (key: SortKey, label: string, className?: string, right?: boolean) => (
    <SortHead
      label={label}
      active={sort.key === key}
      desc={sort.desc}
      onSort={() => onSort(key)}
      className={className}
      right={right}
    />
  )
  return (
    <Table className="table-fixed">
      <TableHeader>
        <TableRow>
          {head("name", "Volume")}
          {wide && head("mounted", "Mounted by", "w-[32%]")}
          {wide && head("standing", "State", "w-44")}
          {widest && head("created", "Created", "w-28")}
          {head("size", "Size", wide ? "w-28" : cn("w-24", NARROW_PAD), true)}
          <TableHead className={wide ? "w-20" : "w-10 px-1"}>
            <span className="sr-only">Actions</span>
          </TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {volumes.map((volume) => {
          const backup = backups.get(volume.name)
          const facts = wide ? volumeFacts(volume, widest, onHolder) : []
          return (
            <TableRow
              key={volume.name}
              data-workspace-item={volume.name}
              data-workspace-name={volume.name}
              onActivate={() => onOpen(volume.name)}
              className={cn("group", ROW, arrived.has(volume.name) && "animate-rise")}
            >
              <TableCell className={cn("py-0", !wide && "pr-0")}>
                <VolumeName volume={volume} product={volumeProduct(volume, productOf)} />
                {/* Wide, a volume with nothing to add under its name keeps one line. */}
                {(!wide || facts.length > 0) && (
                  <p
                    data-slot="volume-facts"
                    className={cn(
                      "mt-0.5 flex min-w-0 items-center truncate pl-11 text-hint text-muted-foreground",
                      wide ? "gap-1.5" : "gap-1",
                    )}
                  >
                    {wide ? (
                      facts.map((fact, i) => (
                        <span key={i} className="inline-flex min-w-0 items-center gap-1.5">
                          {i > 0 && <span className="text-muted-foreground/40">·</span>}
                          {fact}
                        </span>
                      ))
                    ) : (
                      // The containers are counted in the standing's title and
                      // named on the volume's page: the line has room for where
                      // it stands and whether a backup covers it, whole.
                      <>
                        <StandingWord volume={volume} />
                        {backup && (
                          <>
                            <span className="text-muted-foreground/40">·</span>
                            <BackupWord backup={backup} short />
                          </>
                        )}
                      </>
                    )}
                  </p>
                )}
              </TableCell>
              {wide && (
                <TableCell className="py-0">
                  <MountedBy volume={volume} />
                </TableCell>
              )}
              {wide && (
                <TableCell className="py-0">
                  <span className="flex flex-col gap-0.5">
                    <StandingWord volume={volume} strong />
                    {backup && (
                      <span className="text-hint">
                        <BackupWord backup={backup} />
                      </span>
                    )}
                  </span>
                </TableCell>
              )}
              {widest && (
                <TableCell
                  className="py-0 text-muted-foreground"
                  title={volume.createdAt ? timestamp(volume.createdAt) : undefined}
                >
                  {volume.createdAt ? relativeTime(volume.createdAt) : "—"}
                </TableCell>
              )}
              <TableCell className={cn("numeric py-0 text-right", !wide && NARROW_PAD)}>
                {removing === volume.name ? (
                  <TextShimmer>Removing…</TextShimmer>
                ) : (
                  <Size volume={volume} />
                )}
              </TableCell>
              <TableCell className={cn("py-0", wide ? "px-2" : "px-1")}>
                <VerbActions
                  dim
                  className="justify-end"
                  verbs={wide ? verbs(volume) : verbs(volume).map((v) => ({ ...v, inline: false }))}
                  menuLabel={`More actions for ${volume.name}`}
                />
              </TableCell>
            </TableRow>
          )
        })}
      </TableBody>
    </Table>
  )
}

/**
 * A column's head that orders the rows under it, saying which way through
 * `aria-sort`. The Databases performance table has the same head, in a module
 * that brings the engine kit with it.
 */
function SortHead({
  label,
  active,
  desc,
  onSort,
  right,
  className,
}: {
  label: string
  active: boolean
  desc: boolean
  onSort: () => void
  right?: boolean
  className?: string
}) {
  const Arrow = desc ? ArrowDown : ArrowUp
  return (
    <TableHead
      aria-sort={active ? (desc ? "descending" : "ascending") : "none"}
      className={cn(right && "text-right", className)}
    >
      <button
        type="button"
        onClick={onSort}
        className={cn(
          "-mx-1 inline-flex items-center gap-1 rounded-sm px-1 focus-ring transition-colors hover:text-foreground",
          active && "text-foreground",
        )}
      >
        {label}
        {active && <Arrow aria-hidden className="size-3" />}
      </button>
    </TableHead>
  )
}

/**
 * The volume as the product whose data it holds, then its name — a Compose
 * project's prefix a step back, because down a column of `shop_*` it is the
 * part after the underscore that differs.
 */
function VolumeName({ volume, product }: { volume: VolumeDetail; product?: string }) {
  const { prefix, rest } = splitName(volume)
  const anonymous = isAnonymous(volume)
  return (
    <div className="flex min-w-0 items-center gap-3">
      <ProductLogo id={product} fallback={Database} size="sm" />
      <Link
        href={volumeHref(volume.name)}
        title={volume.name}
        aria-label={volume.name}
        className={cn(
          "max-w-full min-w-0 truncate rounded-sm font-mono text-body font-medium focus-ring hover:underline",
          anonymous && "text-muted-foreground",
        )}
      >
        {anonymous ? (
          <>
            {volume.name.slice(0, 12)}
            <span className="text-muted-foreground/60">…</span>
          </>
        ) : (
          <>
            {prefix && <span className="text-muted-foreground">{prefix}</span>}
            {rest}
          </>
        )}
      </Link>
    </div>
  )
}

/** Who holds it, in that holder's lane hue and pressable; how it is stored; how old. */
function volumeFacts(
  volume: VolumeDetail,
  widest: boolean,
  onHolder: (key: string) => void,
): React.ReactNode[] {
  const holder = holderOf(volume)
  const facts: React.ReactNode[] = []
  if (holder.kind === "stack") {
    facts.push(
      <button
        key="holder"
        type="button"
        aria-label={`Only the volumes ${holder.name} holds`}
        onClick={() => onHolder(holder.key)}
        className="shrink-0 rounded-sm font-medium focus-ring hover:underline"
        style={{ color: holderHue(holder) }}
      >
        {holder.name}
      </button>,
    )
  }
  if (isAnonymous(volume)) facts.push(<span key="anon">anonymous</span>)
  if (volume.driver !== "local") {
    facts.push(
      <span key="driver" className="font-mono">
        {volume.driver}
      </span>,
    )
  } else if (volume.mountType) {
    facts.push(
      <span key="mount" className="font-mono">
        {volume.mountType} mount
      </span>,
    )
  }
  if (!widest && volume.createdAt) {
    facts.push(
      <span key="created" title={timestamp(volume.createdAt)}>
        {relativeTime(volume.createdAt)}
      </span>,
    )
  }
  return facts
}

/**
 * The containers mounting it, each by name with its state's dot and the path
 * it sees the volume at, opening its page. A volume nothing mounts says why
 * that might be, since the answer decides whether its data is wanted.
 */
function MountedBy({ volume }: { volume: VolumeDetail }) {
  if (volume.usedBy.length === 0) {
    const s = standing(volume)
    const holder = holderOf(volume)
    return (
      <span className="block py-0.5 text-muted-foreground">
        {s === "down" ? (
          <>
            nothing — <span className="text-warning">{holder.name} is down</span>
          </>
        ) : s === "anonymous" ? (
          "nothing — its container was removed"
        ) : (
          "nothing"
        )}
      </span>
    )
  }
  const { shown, more } = mountLines(volume.usedBy)
  return (
    <ul className="min-w-0 space-y-0.5">
      {shown.map((u) => (
        <li key={`${u.id}-${u.destination}`} className="flex min-w-0 items-center gap-2">
          <Link
            href={`/docker/containers/${u.id}`}
            title={`${u.name} — ${u.state}`}
            className="inline-flex max-w-[55%] min-w-0 shrink-0 items-center gap-1.5 rounded-sm focus-ring hover:underline"
          >
            <StatusDot state={u.state} />
            <span className="truncate">{u.name}</span>
          </Link>
          <span
            className="min-w-0 truncate font-mono text-hint text-muted-foreground"
            title={u.destination}
          >
            {u.destination}
          </span>
          {u.readOnly && <Tag className="shrink-0">ro</Tag>}
        </li>
      ))}
      {more > 0 && (
        <li className="numeric text-hint text-muted-foreground">
          <Link
            href={volumeHref(volume.name, "mounts")}
            className="rounded-sm focus-ring hover:text-foreground hover:underline"
          >
            +{plural(more, "more container")}
          </Link>
        </li>
      )}
    </ul>
  )
}

/**
 * Where it stands, in the band's colour for it. What is unmounted is said as
 * what a prune does to it, since that is the reason the word is read.
 */
function StandingWord({ volume, strong }: { volume: VolumeDetail; strong?: boolean }) {
  const s = standing(volume)
  const { word, warning } = standingWords(volume, Boolean(strong))
  return (
    <span
      className={cn(
        "inline-flex shrink-0 items-center gap-1.5",
        strong && "text-xs font-medium",
        warning && "text-warning",
        s === "running" && strong && "text-foreground",
      )}
      title={
        s === "stopped"
          ? "A stopped container still holds it; a prune leaves it alone."
          : !strong && volume.usedBy.length > 0
            ? `${plural(volume.usedBy.length, "container")}: ${volume.usedBy.map((u) => u.name).join(", ")}`
            : undefined
      }
    >
      <span
        aria-hidden
        className="size-1.5 shrink-0 rounded-full"
        style={{ background: STANDING_COLOR[s] }}
      />
      {word}
    </span>
  )
}

/** Whether a backup covers it; `short` is the phone's line, which has room for two words. */
function BackupWord({ backup, short }: { backup: Backup; short?: boolean }) {
  const jobs = backup.jobs.map((j) => j.name).join(", ")
  if (backup.state === "protected") {
    const when = backup.lastAt ? `backed up ${relativeTime(backup.lastAt)}` : "backup never ran"
    return (
      <span
        className="inline-flex min-w-0 items-center gap-1 text-success"
        title={short ? `${when} · ${jobs}` : jobs}
      >
        <CheckCircle aria-hidden className="size-3 shrink-0" />
        <span className="truncate">{short ? "backed up" : when}</span>
      </span>
    )
  }
  if (backup.state === "paused") {
    return (
      <span className="truncate text-warning" title={jobs}>
        backup paused
      </span>
    )
  }
  return (
    <span className="truncate text-muted-foreground">{short ? "no backup" : "not backed up"}</span>
  )
}

function Size({ volume }: { volume: VolumeDetail }) {
  const reading = sizeReading(volume)
  if (reading.bytes === undefined) {
    return <span className="text-hint text-muted-foreground">{reading.word}</span>
  }
  return <span>{bytes(reading.bytes)}</span>
}
