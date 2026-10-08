"use client"

import Link from "next/link"
import { ArrowDown, ArrowUp, CheckCircle, Database } from "@/components/icons"
import { RowLink } from "@/components/page"
import { ProductLogo } from "@/components/product-logo"
import { StatusDot } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { VerbActions, type Verb } from "@/components/verbs"
import {
  STANDING_COLOR,
  STANDING_WORD,
  VOLUME_HUE,
  holderHue,
  holderOf,
  isAnonymous,
  prunable,
  sizeReading,
  splitName,
  standing,
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

/** How many containers a row names before it counts the rest. */
const NAMED = 2

/**
 * The volumes as a table: each row one volume read across — what it is, which
 * containers mount it and where, whether a prune would take it and whether a
 * backup covers it, and its size against the largest — that opens its sheet.
 *
 * It was a column of cards whose second line said "1 container" and nothing
 * about which, or at what path, so the question every volume is opened with —
 * what is using this — took a click to answer. The containers are the widest
 * column now, each by name with its state's dot and the path it sees the
 * volume at, linking to its page. The Images, Processes and Services tables
 * are the precedent: a row that opens a sheet is still read down its columns.
 *
 * Wide, every reading has its column, the creation date joining at `2xl`.
 * Below `xl` the containers, the standing and the backup go to the name's
 * second line and the row keeps its size and its menu — chosen once by the
 * page rather than drawn twice and hidden (§12).
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
  const largest = Math.max(...volumes.map((v) => v.size), 1)
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
          {head("size", "Size", wide ? "w-28" : "w-24", true)}
          <TableHead className={wide ? "w-20" : "w-12"}>
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
              className={cn("group", arrived.has(volume.name) && "animate-rise")}
            >
              <TableCell>
                <VolumeName
                  volume={volume}
                  product={volumeProduct(volume, productOf)}
                  onOpen={() => onOpen(volume.name)}
                />
                {/* Wide, a volume with nothing to add under its name keeps one line. */}
                {(!wide || facts.length > 0) && (
                  <p className="mt-1 flex min-w-0 items-center gap-1.5 truncate pl-11 text-hint text-muted-foreground">
                    {wide ? (
                      facts.map((fact, i) => (
                        <span key={i} className="inline-flex min-w-0 items-center gap-1.5">
                          {i > 0 && <span className="text-muted-foreground/40">·</span>}
                          {fact}
                        </span>
                      ))
                    ) : (
                      <>
                        <StandingWord volume={volume} />
                        {volume.usedBy.length > 0 && (
                          <>
                            <span className="text-muted-foreground/40">·</span>
                            <span
                              className="numeric shrink-0"
                              title={volume.usedBy.map((u) => u.name).join(", ")}
                            >
                              {plural(volume.usedBy.length, "container")}
                            </span>
                          </>
                        )}
                        {backup && (
                          <>
                            <span className="text-muted-foreground/40">·</span>
                            <BackupWord backup={backup} />
                          </>
                        )}
                      </>
                    )}
                  </p>
                )}
              </TableCell>
              {wide && (
                <TableCell>
                  <MountedBy volume={volume} />
                </TableCell>
              )}
              {wide && (
                <TableCell>
                  <span className="flex flex-col gap-1">
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
                  className="text-muted-foreground"
                  title={volume.createdAt ? timestamp(volume.createdAt) : undefined}
                >
                  {volume.createdAt ? relativeTime(volume.createdAt) : "—"}
                </TableCell>
              )}
              <TableCell className="numeric text-right">
                {removing === volume.name ? (
                  <TextShimmer>Removing…</TextShimmer>
                ) : (
                  <Size volume={volume} largest={largest} />
                )}
              </TableCell>
              <TableCell className="px-2">
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
function VolumeName({
  volume,
  product,
  onOpen,
}: {
  volume: VolumeDetail
  product?: string
  onOpen: () => void
}) {
  const { prefix, rest } = splitName(volume)
  const anonymous = isAnonymous(volume)
  return (
    <div className="flex min-w-0 items-center gap-3">
      <ProductLogo id={product} fallback={Database} size="sm" />
      <RowLink
        mono
        onClick={onOpen}
        title={volume.name}
        className={cn("text-body", anonymous && "text-muted-foreground")}
      >
        <span className="sr-only">{volume.name}</span>
        <span aria-hidden>
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
        </span>
      </RowLink>
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
  } else if (Object.keys(volume.options ?? {}).length > 0) {
    facts.push(
      <span key="options" className="font-mono">
        {volume.options?.type ?? "local"} mount
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
        ) : volume.refCount > 0 ? (
          plural(volume.refCount, "container")
        ) : (
          "nothing"
        )}
      </span>
    )
  }
  const shown = volume.usedBy.slice(0, NAMED)
  return (
    <ul className="min-w-0 space-y-1">
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
      {volume.usedBy.length > NAMED && (
        <li className="numeric text-hint text-muted-foreground">
          +{plural(volume.usedBy.length - NAMED, "more container")}
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
  const taken = prunable(volume)
  const word =
    s === "running" || s === "stopped"
      ? STANDING_WORD[s]
      : taken
        ? strong
          ? "prune deletes it"
          : "prune deletes"
        : "not mounted"
  return (
    <span
      className={cn(
        "inline-flex shrink-0 items-center gap-1.5",
        strong && "text-xs font-medium",
        taken && "text-warning",
        s === "running" && strong && "text-foreground",
      )}
      title={
        s === "stopped" ? "A stopped container still holds it; a prune leaves it alone." : undefined
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

function BackupWord({ backup }: { backup: Backup }) {
  if (backup.state === "protected") {
    return (
      <span
        className="inline-flex min-w-0 items-center gap-1 text-success"
        title={backup.jobs.map((j) => j.name).join(", ")}
      >
        <CheckCircle aria-hidden className="size-3 shrink-0" />
        <span className="truncate">
          {backup.lastAt ? `backed up ${relativeTime(backup.lastAt)}` : "backup never ran"}
        </span>
      </span>
    )
  }
  if (backup.state === "paused") {
    return (
      <span className="truncate text-warning" title={backup.jobs.map((j) => j.name).join(", ")}>
        backup paused
      </span>
    )
  }
  return <span className="truncate text-muted-foreground">not backed up</span>
}

function Size({ volume, largest }: { volume: VolumeDetail; largest: number }) {
  const reading = sizeReading(volume)
  if (reading.bytes === undefined) {
    return <span className="text-hint text-muted-foreground">{reading.word}</span>
  }
  return (
    <span className="inline-flex w-full flex-col items-end gap-1.5">
      <span>{bytes(reading.bytes)}</span>
      <span aria-hidden className="h-0.5 w-14 overflow-hidden rounded-full bg-meter-track">
        <span
          className="block h-full transition-[width] duration-700 ease-out motion-reduce:transition-none"
          style={{ width: `${(reading.bytes / largest) * 100}%`, background: VOLUME_HUE }}
        />
      </span>
    </span>
  )
}
