"use client"

import { useMemo, useState } from "react"
import { Cpu, FolderClosed, Information, Servers, Warning, type Icon } from "@/components/icons"
import { notify } from "@/lib/toast"
import { get } from "@/lib/api"
import { bytes, relativeTime } from "@/lib/format"
import type {
  ContainerDetail,
  FileChange,
  MigrationPlan,
  WritableEntry,
  WritableLayerReport,
} from "@/lib/types"
import { cn } from "@/lib/utils"
import { usePoll } from "@/hooks/use-poll"
import { FileBrowser } from "@/components/files/inline-browser"
import { Pane, PaneFooter, PaneHeader, Well } from "@/components/panel"
import { EmptyNote, EmptyState, ErrorState, Notice } from "@/components/state"
import { ProductLogo, imageProduct } from "@/components/product-logo"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { ExplainIcon, Hint, Term } from "@/components/docker/explain"
import { DatabaseStorageWarning, looksLikeDatabase } from "@/components/docker/shared"

/**
 * What this container's storage actually is, said in the order it is asked
 * about.
 *
 * This tab used to render each mount as a bare fenced block with two small-caps
 * tags above it — `VOLUME` `READ-WRITE` — and one monospace line reading
 * `/var/lib/docker/volumes/bet-bot_tracker-data/_data → /data`. Every word in
 * that is true and none of it answers the question somebody opens this tab
 * with, which is *where does this container's data live and will it survive*.
 * The arrow even pointed the wrong way round for how it is read: the host path
 * is the least interesting half and it was first and widest.
 *
 * So the path inside the container leads — that is the one the application's
 * own configuration refers to — the kind of storage is stated in words rather
 * than as a Docker noun, and where it actually lives follows it. The
 * consequence, which is the whole point, is one sentence per kind and one
 * hover card away.
 */
const MOUNT_KIND: Record<
  string,
  {
    label: string
    term: string
    /** Where the data really is, in the form a person would go looking for it. */
    where: (mount: ContainerDetail["mounts"][number]) => string
    /** Whether it outlives the container. The reason anyone reads this tab. */
    survives: boolean
  }
> = {
  volume: {
    label: "Managed volume",
    term: "volume",
    // The volume's name, not the directory Docker keeps it in. `_data` under
    // /var/lib/docker/volumes is an implementation detail of the storage
    // driver; the name is the handle every other screen and command uses.
    where: (mount) => mount.name || mount.source,
    survives: true,
  },
  bind: {
    label: "Folder on this server",
    term: "bind",
    where: (mount) => mount.source,
    survives: true,
  },
  tmpfs: {
    label: "Temporary memory",
    term: "tmpfs",
    where: () => "in RAM",
    survives: false,
  },
}

/**
 * Only a volume or a bind has somewhere to look. A tmpfs mount is memory: it
 * exists in the container's namespace and nowhere on this filesystem, so its
 * row never grows a control that could only fail.
 */
function browsable(mount: ContainerDetail["mounts"][number]) {
  return (mount.type === "volume" || mount.type === "bind") && Boolean(mount.source)
}

/**
 * The mounts, and what is in one of them — already open, as the file manager
 * draws a place: the mounts down a rail, the listing of the picked one beside
 * it, one frame around both.
 *
 * Each mount was a row that had to be expanded before it showed anything, and
 * then a line of grey text over a browser of its own. The question somebody
 * opens this tab with — did the backup land, is this the volume with the
 * database in it — was a click and a scroll away on every visit. Now the
 * first mount there is something to look in is open with the tab, the rail
 * draws each mount as the kind of storage it is in that kind's hue, and with
 * more than one to look in, picking a row is what changes the listing, the
 * way the file manager's sidebar does. The rail's foot says how much of what
 * it writes the container keeps.
 */
export function ContainerStorageTab({ detail }: { detail: ContainerDetail }) {
  const mounts = detail.mounts
  const [selected, setSelected] = useState(() => mounts.findIndex(browsable))
  const choosing = mounts.filter(browsable).length > 1
  const mount = mounts[selected]

  if (mounts.length === 0) {
    return (
      <div className="flex flex-col gap-3">
        <EmptyNote>
          Nothing is attached, so everything this container writes is destroyed when it is replaced.
        </EmptyNote>
        <WritableLayer containerId={detail.id} />
      </div>
    )
  }

  // Writing into a live database's own files is how a volume stops being
  // restorable. A stopped container is not running that database, and telling
  // somebody to stop what is already stopped is noise.
  const databaseFiles =
    mount &&
    detail.state === "running" &&
    looksLikeDatabase(mount.name, mount.destination, mount.source, detail.image)
  const kept = mounts.filter((m) => MOUNT_KIND[m.type]?.survives !== false).length

  return (
    <div className="flex h-full min-h-0 animate-rise flex-col gap-3">
      {databaseFiles && <DatabaseStorageWarning />}
      <div className="flex min-h-[30rem] flex-1 flex-col overflow-hidden rounded-xl border bg-card lg:flex-row">
        <Pane
          flush
          className="shrink-0 border-hairline max-lg:max-h-72 max-lg:border-b lg:w-80 lg:border-r"
        >
          <PaneHeader className="justify-between px-3 py-2">
            <span className="eyebrow">Mounts</span>
            <span className="numeric text-hint text-muted-foreground">{mounts.length}</span>
          </PaneHeader>
          <ul aria-label="Mounts" className="min-h-0 flex-1 space-y-px overflow-auto p-1.5">
            {mounts.map((m, i) => (
              <MountRow
                key={i}
                mount={m}
                image={detail.image}
                selected={choosing && i === selected}
                onSelect={choosing && browsable(m) ? () => setSelected(i) : undefined}
              />
            ))}
          </ul>
          <PaneFooter className="px-3 text-hint text-muted-foreground">
            {kept === mounts.length
              ? "Every mount outlives the container"
              : `${kept} of ${mounts.length} outlive the container`}
          </PaneFooter>
        </Pane>
        {mount && browsable(mount) ? (
          // Flush inside the workbench's one frame, the way the file manager's
          // listing sits beside its sidebar.
          <FileBrowser
            fill
            className="min-h-80 rounded-none border-0"
            root={mount.source}
            label={mount.type === "volume" ? MOUNT_KIND.volume.where(mount) : undefined}
            emptyNote={
              mount.type === "volume"
                ? "Nothing has been written to this volume yet."
                : "This folder is empty."
            }
          />
        ) : (
          <EmptyState
            icon={Cpu}
            title="Nothing here to look in"
            description="Its mounts are memory, which exists inside the container and nowhere on this server's disk."
          />
        )}
      </div>
      <WritableLayer containerId={detail.id} />
    </div>
  )
}

/** Each kind of storage in the hue that names it, the way the file manager colours a folder. */
const KIND_MARK: Record<string, { glyph: Icon; hue: string }> = {
  volume: { glyph: Servers, hue: "var(--tag-blue)" },
  bind: { glyph: FolderClosed, hue: "var(--tag-cyan)" },
  tmpfs: { glyph: Cpu, hue: "var(--tag-violet)" },
}

/**
 * One mount: the path the application inside was configured with, and under
 * it what kind of storage that is and where it really lives. Docker's own
 * socket is drawn in red, because a container holding it can control Docker.
 */
function MountRow({
  mount,
  image,
  selected,
  onSelect,
}: {
  mount: ContainerDetail["mounts"][number]
  image: string
  selected: boolean
  /** Set when there is more than one mount to look in and this is one of them. */
  onSelect?: () => void
}) {
  const kind = MOUNT_KIND[mount.type]
  const where = kind ? kind.where(mount) : mount.source
  const socket = mount.type === "bind" && /(^|\/)docker\.sock$/.test(mount.source)
  const database =
    mount.type === "volume" && looksLikeDatabase(mount.name, mount.destination, mount.source, image)
  const mark = KIND_MARK[mount.type] ?? KIND_MARK.bind
  const Glyph = socket ? Warning : mark.glyph

  const facts = (
    <>
      {database ? (
        <ProductLogo id={imageProduct(image)} size="sm" fallback={Servers} />
      ) : (
        <span
          aria-hidden
          className="flex size-8 shrink-0 items-center justify-center rounded-lg border border-hairline bg-background"
          style={{ color: socket ? "var(--destructive)" : mark.hue }}
        >
          <Glyph className="size-4" />
        </span>
      )}
      <span className="min-w-0 flex-1">
        <span className="block truncate font-mono text-body" title={mount.destination}>
          {mount.destination}
        </span>
        <span className="block truncate text-hint text-muted-foreground" title={where}>
          {socket ? "Docker's own socket" : (kind?.label ?? mount.type)} · {where}
        </span>
      </span>
    </>
  )
  const row = "flex min-w-0 flex-1 items-center gap-2.5 px-2 py-1.5"

  return (
    <li
      className={cn(
        "group flex min-w-0 items-center gap-1 rounded-md pr-1.5 transition-colors",
        selected ? "bg-accent text-accent-foreground" : onSelect && "hover:bg-row-hover",
      )}
    >
      {onSelect ? (
        <button
          type="button"
          aria-pressed={selected}
          onClick={onSelect}
          className={cn(row, "rounded-md text-left focus-ring-inset")}
        >
          {facts}
        </button>
      ) : (
        <span className={row}>{facts}</span>
      )}
      <span className="flex shrink-0 flex-col items-end gap-0.5">
        {!mount.rw && <Tag>read-only</Tag>}
        {kind?.survives === false && <Tag tone="warning">not kept</Tag>}
      </span>
      {kind && <ExplainIcon name={kind.term} />}
    </li>
  )
}

/**
 * Where the writable layer went, and whether any of it matters.
 *
 * "This container has written 38.7 GB into itself" is a true sentence nobody
 * can act on. The questions behind it are which directory holds it, whether
 * that directory looks like data somebody meant to keep, and whether anything
 * is mounted at it — because a writable layer is destroyed by every recreate,
 * so 35 GB under /app/data with no volume there is a database that will vanish
 * on the next image update.
 *
 * The breakdown is measured by running `du` inside the container, which means
 * it needs the container running and needs the image to ship `du`. Both
 * failures are reported as themselves rather than as an empty result: an
 * unmeasurable layer is a different thing from an empty one.
 */
function WritableLayer({ containerId }: { containerId: string }) {
  const [analyzing, setAnalyzing] = useState(false)
  const [report, setReport] = useState<WritableLayerReport | null>(null)
  const [failed, setFailed] = useState<Error | null>(null)

  const changes = usePoll<FileChange[]>(
    (signal) => get<FileChange[]>(`/docker/containers/${containerId}/changes`, undefined, signal),
    0,
    [containerId],
  )

  const interesting = useMemo(() => {
    const noise =
      /^\/(tmp|run|proc|sys|dev|var\/(run|log|cache|tmp|lib\/(apt|dpkg))|etc\/(hosts|hostname|resolv\.conf|mtab))/
    return (changes.data ?? []).filter((c) => c.kind !== "deleted" && !noise.test(c.path))
  }, [changes.data])

  // Directories with children collapse to the directory: fifty files under
  // /var/lib/postgresql/data is one fact, not fifty.
  const roots = useMemo(() => {
    const out: string[] = []
    for (const change of interesting) {
      if (!out.some((r) => change.path === r || change.path.startsWith(r + "/"))) {
        out.push(change.path)
      }
    }
    return out.slice(0, 40)
  }, [interesting])

  const analyze = async (refresh = false) => {
    setAnalyzing(true)
    setFailed(null)
    try {
      setReport(
        await get<WritableLayerReport>(
          `/docker/containers/${containerId}/writable-layer`,
          refresh ? { refresh: true } : undefined,
        ),
      )
    } catch (err) {
      setFailed(err as Error)
    } finally {
      setAnalyzing(false)
    }
  }

  if (changes.loading || (roots.length === 0 && !report)) return null

  return (
    <div className="space-y-3">
      <Notice
        title={`${roots.length} ${roots.length === 1 ? "path is" : "paths are"} written with nothing keeping them`}
        icon={Warning}
        tone="warning"
      >
        <p>
          Nothing above is mounted at these, so they are in{" "}
          <Term name="writableLayer">the container&apos;s own filesystem</Term>. They are not backed
          up, and they are destroyed the next time this container is recreated — which includes
          every image update.
        </p>
        <div className="mt-2 flex max-h-28 flex-wrap gap-1 overflow-auto">
          {roots.map((path) => (
            <Tag key={path} mono className="text-foreground">
              {path}
            </Tag>
          ))}
        </div>
        <Button
          size="xs"
          variant="outline"
          className="mt-2"
          onClick={() => analyze(Boolean(report))}
          pending={analyzing}
        >
          {report ? "Measure again" : "Measure disk usage"}
        </Button>
      </Notice>

      {failed && <ErrorState error={failed} />}
      {report && <WritableLayerReportView report={report} containerId={containerId} />}
    </div>
  )
}

function WritableLayerReportView({
  report,
  containerId,
}: {
  report: WritableLayerReport
  containerId: string
}) {
  if (report.state !== "measured") {
    return (
      <Notice
        title={
          report.state === "failed"
            ? "The breakdown could not be measured"
            : "The breakdown is not available for this container"
        }
        icon={Information}
      >
        <p>{report.reason}</p>
        {report.total > 0 && (
          <p className="mt-1">
            Docker still reports the total: <b>{bytes(report.total)}</b>.
          </p>
        )}
      </Notice>
    )
  }

  const biggest = report.entries.filter((e) => e.size > 0).slice(0, 8)
  return (
    <section className="space-y-2">
      <div className="flex flex-wrap items-baseline justify-between gap-2">
        <p className="eyebrow">Where it went</p>
        <p className="text-hint text-muted-foreground">
          {report.method} · {relativeTime(report.measuredAt)}
        </p>
      </div>

      {/* Eight directories as eight rows of a table read down — path, kind,
          size — with a hairline between them. Framed one by one they were a
          stack of eight boxes whose figures could not be compared. */}
      <ul className="divide-y divide-hairline">
        {biggest.map((entry) => (
          <li key={entry.path} className="flex min-w-0 items-center gap-2 py-1.5 text-xs">
            <span className="min-w-0 flex-1 truncate font-mono text-hint">{entry.path}</span>
            {entry.mounted ? (
              <Tag>on a mount</Tag>
            ) : entry.persistent ? (
              <Tag tone="warning">not backed by storage</Tag>
            ) : (
              <Tag>{entry.kind}</Tag>
            )}
            <span className="numeric w-16 shrink-0 text-right font-mono text-hint">
              {bytes(entry.size)}
            </span>
          </li>
        ))}
      </ul>

      {/*
        The two figures come from two different measurements — Docker's diff
        accounting and `du` counting allocated blocks — so they will not match
        exactly. Stating the gap is more trustworthy than reconciling it
        silently.
      */}
      <Hint>
        Docker reports {bytes(report.total)} for the whole layer; the directories above account for{" "}
        {bytes(report.accounted)}. The two are measured differently — Docker counts the difference
        from the image, `du` counts allocated blocks — so they agree only approximately.
      </Hint>

      {report.unbacked.length > 0 && (
        <MigrationSuggestion containerId={containerId} entries={report.unbacked} />
      )}
    </section>
  )
}

/**
 * A directory holding real data with no volume under it, and what to do about
 * it — described, never performed.
 *
 * The safe version of this migration stops the service, copies data the
 * operator has just been told they cannot afford to lose, edits the compose
 * file and starts it again, with a rollback if any step fails. That is not a
 * thing to do silently behind a button, so the dashboard produces the exact
 * plan and the exact commands and the operator runs them, able to stop between
 * any two.
 */
function MigrationSuggestion({
  containerId,
  entries,
}: {
  containerId: string
  entries: WritableEntry[]
}) {
  const [plan, setPlan] = useState<MigrationPlan | null>(null)
  const [busy, setBusy] = useState(false)
  const worst = entries[0]

  const build = async () => {
    setBusy(true)
    try {
      setPlan(
        await get<MigrationPlan>(`/docker/containers/${containerId}/migration-plan`, {
          path: worst.path,
        }),
      )
    } catch (err) {
      notify.error("Could not work out a migration plan", err)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Notice title="This data will not survive a recreate" icon={Warning} tone="warning">
      <p>
        <span className="font-mono">{worst.path}</span> holds {bytes(worst.size)} and nothing is
        mounted there, so it lives in the container&apos;s own filesystem. Every image update
        replaces that filesystem.
      </p>
      {entries.length > 1 && (
        <p className="mt-1">
          {entries.length - 1} other {entries.length === 2 ? "directory is" : "directories are"} in
          the same position:{" "}
          {entries
            .slice(1)
            .map((e) => e.path)
            .join(", ")}
          .
        </p>
      )}
      {!plan ? (
        <Button size="xs" variant="outline" className="mt-2" onClick={build} pending={busy}>
          Create a migration plan
        </Button>
      ) : (
        <div className="mt-2 space-y-2">
          <ol className="space-y-1.5 text-hint">
            {plan.steps.map((step, i) => (
              <li key={i}>
                <b>
                  {i + 1}. {step.title}
                </b>
                <span className="block text-muted-foreground">{step.detail}</span>
              </li>
            ))}
          </ol>
          <div>
            <p className="eyebrow mb-1">The commands</p>
            <Well className="max-h-48 font-mono text-micro whitespace-pre">
              {plan.commands.join("\n")}
            </Well>
          </div>
          {plan.composePatch && (
            <div>
              <p className="eyebrow mb-1">Add to the compose file</p>
              <Well className="font-mono text-micro whitespace-pre">{plan.composePatch}</Well>
            </div>
          )}
          {plan.warnings.map((warning, i) => (
            <p key={i} className="text-hint text-warning">
              {warning}
            </p>
          ))}
        </div>
      )}
    </Notice>
  )
}
