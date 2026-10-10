"use client"

import { useMemo, useState } from "react"
import {
  Archive,
  ArrowLeftRight,
  Box,
  CloudUpload,
  Cpu,
  FileText,
  Globe,
  Layers,
  Monorepo,
  RefreshClockwise,
} from "@/components/icons"
import { cn } from "@/lib/utils"
import { bytes, relativeTime } from "@/lib/format"
import type { LogSource, LogSourceIndex } from "@/lib/types"
import { lensFor } from "@/lib/log-lenses"
import { journalIdSource, kernelSource } from "@/lib/log-sources"
import type { RequestRecord } from "@/components/logs/service-views-model"
import { SearchInput } from "@/components/page"
import { Pane, PaneHeader } from "@/components/panel"
import { ErrorState, LoadingRows } from "@/components/state"
import { StatusDot } from "@/components/status-dot"
import { IconAction } from "@/components/icon-action"
import { ChipCount } from "@/components/tabs"
import {
  ProductLogo,
  ProductLogos,
  imageProduct,
  imageProducts,
  platformProduct,
} from "@/components/product-logo"

/**
 * The groups, in the order somebody actually looks. The raw kind strings are
 * the API's vocabulary, not a reader's: "nginx" is a group of two files that a
 * person thinks of as their web server, and "app" is whatever was dropped into
 * the log roots.
 *
 * The glyph before each group is wayfinding — the same mark the sidebar and
 * the Overview use for the module the logs belong to — so the eye finds
 * "Containers" in the rail without reading down it.
 */
const GROUPS: {
  kind: LogSource["kind"]
  /** Kinds read through the same reader, listed with it rather than as groups of one. */
  also?: LogSource["kind"][]
  label: string
  icon: typeof FileText
}[] = [
  { kind: "system", label: "System", icon: Cpu },
  { kind: "journal", also: ["journal-id", "kernel"], label: "Systemd journal", icon: Monorepo },
  { kind: "docker", label: "Containers", icon: Box },
  { kind: "stack", label: "Stacks", icon: Layers },
  { kind: "nginx", label: "Web server", icon: Globe },
  { kind: "pm2", label: "PM2", icon: FileText },
  { kind: "app", label: "Applications", icon: FileText },
]

/** The glyph a source that is no product keeps on its tile: its group's. */
export function kindIcon(kind: LogSource["kind"]) {
  return GROUPS.find((g) => g.kind === kind || g.also?.includes(kind))?.icon
}

/** Where the Requests group goes among them: before the web server's raw files. */
const REQUESTS_BEFORE = GROUPS.findIndex((group) => group.kind === "nginx")

/** What one source is, as a word beside its name. */
export const KIND_TAG: Record<LogSource["kind"], string> = {
  system: "system log",
  journal: "journal",
  "journal-id": "journal",
  kernel: "kernel ring",
  docker: "container",
  stack: "stack",
  nginx: "web server",
  pm2: "pm2 process",
  app: "application",
}

/**
 * The journal read by program rather than by unit, for a host whose
 * programs write nowhere else: Debian without rsyslog has no auth.log, no
 * kern.log and no cron file, and "sshd's lines" was a search through the
 * whole journal. Each is offered only where no file already holds the same
 * lines — two rows that read one log are a choice with no difference — and
 * the SSH log only to an administrator, since the server refuses it to
 * anyone else (a failed username is often a typed password).
 */
const JOURNAL_READINGS: {
  source: LogSource
  /** The lens of the file that makes the row redundant, and that file's names. */
  file: { lens: string; names: string[] }
  admin?: boolean
}[] = [
  {
    source: {
      id: journalIdSource(["sshd", "sshd-session", "sshd-auth"]),
      label: "SSH log",
      kind: "journal-id",
      lens: "auth",
      detail: "sshd's lines, through the journal",
      rotated: false,
    },
    file: { lens: "auth", names: ["auth.log", "secure"] },
    admin: true,
  },
  {
    source: {
      id: kernelSource(),
      label: "Kernel ring",
      kind: "kernel",
      lens: "kernel",
      detail: "The firewall, the OOM killer, the disks",
      rotated: false,
    },
    file: { lens: "kernel", names: ["kern.log"] },
  },
  {
    source: {
      id: journalIdSource(["CRON", "crond"]),
      label: "Cron",
      kind: "journal-id",
      lens: "cron",
      detail: "Each job cron ran, through the journal",
      rotated: false,
    },
    file: { lens: "cron", names: ["cron", "cron.log"] },
  },
]

/**
 * The sources the rail lists: the server's inventory, and the journal's
 * readings by program where the host has a journal and no file for them.
 * The page looks its selection up in the same list, so a row the rail
 * draws is a row a link can open.
 */
export function railSources(index: LogSourceIndex | undefined, admin: boolean): LogSource[] {
  const listed = index?.sources ?? []
  if (!listed.some((s) => s.kind === "journal")) return listed
  const hasFile = ({ lens, names }: { lens: string; names: string[] }) =>
    listed.some(
      (s) =>
        s.path && (s.lens === lens || names.includes(s.path.slice(s.path.lastIndexOf("/") + 1))),
    )
  const extra = JOURNAL_READINGS.filter(
    (reading) =>
      (admin || !reading.admin) &&
      !hasFile(reading.file) &&
      !listed.some((s) => s.id === reading.source.id),
  ).map((reading) => reading.source)
  return extra.length ? [...listed, ...extra] : listed
}

/**
 * Every log on the host, grouped by what writes it.
 *
 * One column of the logs workbench, sharing its frame with the lines beside
 * it: a rail with its own border next to a console with its own was two
 * boxes floating on the page, and the screen is one working surface.
 */
export function SourceRail({
  index,
  sources,
  records = [],
  loading,
  error,
  selectedId,
  onSelect,
  selectedRecord,
  onSelectRecord,
  onRescan,
  platform,
  className,
}: {
  index: LogSourceIndex | undefined
  /** The rows to draw, from `railSources`: the inventory and the journal's readings. */
  sources: LogSource[]
  /** The Requests group: the deployments' and the sites' request records. */
  records?: RequestRecord[]
  /** The host's distribution, whose mark the system logs carry. */
  platform?: string
  loading: boolean
  error: Error | undefined
  selectedId: string | null
  onSelect: (source: LogSource) => void
  selectedRecord?: string | null
  onSelectRecord?: (record: RequestRecord) => void
  onRescan: () => void
  className?: string
}) {
  const [filter, setFilter] = useState("")
  const total = sources.length + records.length

  const groups = useMemo(() => {
    const needle = filter.trim().toLowerCase()
    const matches = (s: LogSource) =>
      !needle ||
      s.label.toLowerCase().includes(needle) ||
      s.path?.toLowerCase().includes(needle) ||
      s.detail?.toLowerCase().includes(needle)

    return GROUPS.map((group) => {
      const items = sources.filter(
        (s) => (s.kind === group.kind || group.also?.includes(s.kind)) && matches(s),
      )
      // A running container is the one somebody came here for; a stopped one
      // still has its last words and belongs underneath rather than missing.
      // The whole journal leads its own group, before its readings by program.
      items.sort((a, b) => {
        const live = (s: LogSource) => (s.status === "running" || s.status === "online" ? 0 : 1)
        const own = (s: LogSource) => (s.kind === group.kind ? 0 : 1)
        return live(a) - live(b) || own(a) - own(b) || a.label.localeCompare(b.label)
      })
      return { ...group, items }
    }).filter((g) => g.items.length > 0 || (index?.missing[g.kind] && !needle))
  }, [index, sources, filter])

  const shownRecords = useMemo(() => {
    const needle = filter.trim().toLowerCase()
    return records.filter(
      (r) =>
        !needle ||
        r.label.toLowerCase().includes(needle) ||
        r.detail?.toLowerCase().includes(needle),
    )
  }, [records, filter])
  // The request records sit among the logs of what writes them: after the
  // containers and stacks that answer a deployment's requests, before the
  // web server's raw files they are read from.
  const requestsAt = groups.findIndex(
    (g) => GROUPS.findIndex((each) => each.kind === g.kind) >= REQUESTS_BEFORE,
  )
  const before = requestsAt < 0 ? groups : groups.slice(0, requestsAt)
  const after = requestsAt < 0 ? [] : groups.slice(requestsAt)

  const renderGroup = (group: (typeof groups)[number]) => (
    <div key={group.kind}>
      <p className="eyebrow mb-0.5 flex items-center gap-1.5 px-2 py-1">
        <group.icon aria-hidden className="size-3 shrink-0" />
        <span className="truncate">{group.label}</span>
        {group.items.length > 0 && <ChipCount>{group.items.length}</ChipCount>}
      </p>
      {group.items.length === 0 ? (
        // An absent kind explains itself rather than simply not being
        // there: "no containers" and "no Docker on this host" call for
        // completely different next moves.
        <p className="px-2 pb-1 text-hint leading-snug text-muted-foreground">
          {index?.missing[group.kind]}
        </p>
      ) : (
        <div className="space-y-px">
          {group.items.map((source) => (
            <SourceRow
              key={source.id}
              source={source}
              platform={platform}
              selected={selectedId === source.id}
              onSelect={() => onSelect(source)}
            />
          ))}
        </div>
      )}
    </div>
  )

  return (
    <Pane flush aria-label="Log sources" className={cn("w-full", className)}>
      <PaneHeader className="gap-1.5 pl-3">
        <span className="text-xs font-medium">Sources</span>
        {total > 0 && <ChipCount>{total}</ChipCount>}
        <span className="flex-1" />
        <IconAction
          label="Rescan sources"
          className="size-7"
          pending={loading && Boolean(index)}
          onClick={onRescan}
        >
          <RefreshClockwise />
        </IconAction>
      </PaneHeader>
      {/* A filter over five sources is a box with nothing to do. It appears
          once the list is long enough that scanning it stops being faster
          than typing. */}
      {(total > 5 || filter) && (
        <div className="border-b border-hairline px-2 py-1.5">
          <SearchInput
            dense
            value={filter}
            spellCheck={false}
            onChange={(e) => setFilter(e.target.value)}
            placeholder="Filter sources"
            aria-label="Filter sources"
            containerClassName="sm:w-full"
          />
        </div>
      )}
      <div className="min-h-0 flex-1 overflow-y-auto p-1.5">
        {loading && !index && <LoadingRows className="p-1" />}
        {error && <ErrorState error={error} className="m-1" />}
        {index && (
          <div className="animate-rise space-y-3">
            {before.map(renderGroup)}
            {shownRecords.length > 0 && (
              <div>
                <p className="eyebrow mb-0.5 flex items-center gap-1.5 px-2 py-1">
                  <ArrowLeftRight aria-hidden className="size-3 shrink-0" />
                  <span className="truncate">Requests</span>
                  <ChipCount>{shownRecords.length}</ChipCount>
                </p>
                <div className="space-y-px">
                  {shownRecords.map((record) => (
                    <RecordRow
                      key={record.id}
                      record={record}
                      selected={selectedRecord === record.id}
                      onSelect={() => onSelectRecord?.(record)}
                    />
                  ))}
                </div>
              </div>
            )}
            {after.map(renderGroup)}
            {groups.length === 0 && shownRecords.length === 0 && (
              <p className="px-2 py-6 text-center text-xs text-muted-foreground">
                Nothing matches that filter.
              </p>
            )}
          </div>
        )}
      </div>
    </Pane>
  )
}

/**
 * What wrote a source, as the product it is: a container as its image, the
 * web server as nginx, a PM2 process as PM2, and the system's own files as
 * the distribution that writes them (§14). The journal and a stray
 * application log are no product and keep their group's glyph on the same
 * tile, so every name in the rail starts on one line.
 */
export function sourceProduct(source: LogSource, platform: string | undefined) {
  // A lens names a product only where the log is that product's own — a
  // Postgres file under /var/log/postgresql — and says nothing of auth.log,
  // which stays the distribution that writes it.
  const lensProduct = lensFor(source.lens)?.product
  if (lensProduct) return lensProduct
  switch (source.kind) {
    case "docker":
      // A compose service's detail is "stack · image"; the image is the product.
      return imageProduct(source.detail?.split(" · ").pop() || source.label)
    case "nginx":
      return "nginx-static"
    case "pm2":
      return "pm2"
    case "stack":
      return "docker-compose"
    case "kernel":
      // The ring is the kernel's own, whatever distribution it boots.
      return "linux"
    case "system":
      return platformProduct(platform)
    default:
      return undefined
  }
}

function SourceRow({
  source,
  platform,
  selected,
  onSelect,
}: {
  source: LogSource
  platform?: string
  selected: boolean
  onSelect: () => void
}) {
  // A stack is what it runs: its services' products overlapping, as the
  // Docker page draws a project (§14), the one it is named for whole.
  const stack = source.kind === "stack" && source.images?.length ? source.images : undefined
  return (
    <button
      type="button"
      onClick={onSelect}
      title={source.detail ?? source.path}
      aria-current={selected ? "true" : undefined}
      // The chosen source is the neutral fill every selection in the product
      // takes — the terminal's active session, a table's chosen row — so
      // "you are here" reads the same way on every rail.
      className={cn(
        "group flex w-full min-w-0 items-center gap-2.5 rounded-md px-2 py-1.5 text-left focus-ring-inset transition-colors",
        selected ? "bg-accent text-foreground" : "hover:bg-row-hover",
      )}
    >
      {stack ? (
        <ProductLogos
          ids={imageProducts(stack)}
          ring={selected ? "ring-accent" : "ring-card group-hover:ring-row-hover"}
        />
      ) : (
        <ProductLogo
          id={sourceProduct(source, platform)}
          size="sm"
          fallback={kindIcon(source.kind)}
        />
      )}
      <span className="flex min-w-0 flex-1 flex-col">
        <span className="flex min-w-0 items-center gap-1.5">
          <span className={cn("truncate text-body leading-tight", selected && "font-medium")}>
            {source.label}
          </span>
          {source.status && <StatusDot state={source.status} className="shrink-0" />}
          {(source.archives ?? 0) > 0 && (
            <span
              className="numeric ml-auto flex shrink-0 items-center gap-0.5 text-micro text-muted-foreground"
              title={`${source.archives} rotated archives, ${bytes(source.archiveBytes)} — searchable`}
            >
              <Archive className="size-2.5" />
              {source.archives}
            </span>
          )}
        </span>
        <span className="truncate text-hint text-muted-foreground">
          {source.size !== undefined && source.size > 0
            ? `${bytes(source.size)} · ${relativeTime(source.modified)}`
            : (source.detail ?? source.status)}
        </span>
      </span>
    </button>
  )
}

/**
 * A request record, drawn as the thing it is the record of — the
 * deployment as its product, a site as its engine — with its last hour's
 * rate where the fleet's pulse has one, in the failing tone the fleet's
 * cards use.
 */
function RecordRow({
  record,
  selected,
  onSelect,
}: {
  record: RequestRecord
  selected: boolean
  onSelect: () => void
}) {
  return (
    <button
      type="button"
      onClick={onSelect}
      title={record.detail}
      aria-current={selected ? "true" : undefined}
      className={cn(
        "group flex w-full min-w-0 items-center gap-2.5 rounded-md px-2 py-1.5 text-left focus-ring-inset transition-colors",
        selected ? "bg-accent text-foreground" : "hover:bg-row-hover",
      )}
    >
      <ProductLogo
        id={record.product}
        size="sm"
        fallback={record.deployment ? CloudUpload : Globe}
      />
      <span className="flex min-w-0 flex-1 flex-col">
        <span className="flex min-w-0 items-center gap-1.5">
          <span className={cn("truncate text-body leading-tight", selected && "font-medium")}>
            {record.label}
          </span>
          {record.figure && (
            <span
              className={cn(
                "numeric ml-auto shrink-0 text-micro text-muted-foreground",
                record.tone === "danger" && "text-destructive",
                record.tone === "warning" && "text-warning",
              )}
              title="Requests a minute over the last hour"
            >
              {record.figure}
            </span>
          )}
        </span>
        <span className="truncate text-hint text-muted-foreground">
          {record.detail ?? (record.deployment ? "deployment" : "nginx site")}
        </span>
      </span>
    </button>
  )
}
