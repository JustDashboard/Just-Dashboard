"use client"

import { useState } from "react"
import { Cpu, Minus, Plus } from "@/components/icons"
import { ApiError, get, put } from "@/lib/api"
import { bytes, percent, plural } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { ProcessInspect, ProcessLink, ProcessRow } from "@/lib/types"
import { cn } from "@/lib/utils"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { TileTrend } from "@/components/metrics/sparkline"
import { HUE, LiveBytes, LiveFigure, SeriesKey } from "@/components/overview/readings"
import { ProductLogo, processProduct } from "@/components/product-logo"
import { Row } from "@/components/row-list"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { OOM_PRESETS, oomWords } from "@/components/procs/process-facts"
import { cpuTone, processStateTone, uncontrollable } from "@/components/procs/shared"

/** An open process reads its live figures this often: they are the point of opening it. */
const DETAIL_POLL = 2000

type Seen = {
  pid: number | null
  /** The last reading of the process this page was opened on. */
  row?: ProcessRow
  /** Why the readings stopped: the process exited, or its PID now names another. */
  gone?: "exited" | "replaced"
}

/**
 * One process's live reading, addressed by PID and held to the process it
 * opened on.
 *
 * A process that exits while it is open used to leave its last reading on
 * screen under live-looking verbs, polled into a 404 nobody saw — so the
 * answer to a Kill was a page that went on saying "running" — and a PID reused
 * in the meantime silently became somebody else's process under the same
 * title. Either now stops the reading, keeps the last one, and says which
 * happened.
 */
export function useProcessReading(pid: number | null) {
  const [seen, setSeen] = useState<Seen>({ pid })
  if (seen.pid !== pid) setSeen({ pid })
  const detail = usePoll(
    (abort) => get<ProcessRow>(`/processes/${pid}`, undefined, abort),
    DETAIL_POLL,
    [pid],
    { enabled: pid !== null && seen.pid === pid && !seen.gone },
  )
  const fresh = detail.data
  if (seen.pid === pid && !seen.gone) {
    if (fresh && fresh.pid === pid && fresh !== seen.row) {
      setSeen(
        seen.row && seen.row.createTime !== fresh.createTime
          ? { ...seen, gone: "replaced" }
          : { ...seen, row: fresh },
      )
    } else if (seen.row && detail.error instanceof ApiError && detail.error.status === 404) {
      setSeen({ ...seen, gone: "exited" })
    }
  }
  const row = seen.pid === pid ? seen.row : undefined
  return {
    row,
    gone: seen.pid === pid ? seen.gone : undefined,
    error: row ? undefined : detail.error,
    loading: !row && !detail.error,
    refresh: detail.refresh,
  }
}

/**
 * Everything else the kernel keeps about the process, read every few seconds
 * rather than every two: what it holds open and who it runs as change slowly,
 * and a descriptor list is not worth reading on every frame. It stops with
 * the live reading once the process has gone, and is dropped if the PID it
 * answers for has started a different process.
 */
export function useProcessInspect(row: ProcessRow | undefined, live: boolean) {
  const pid = row?.pid ?? null
  const inspect = usePoll(
    (abort) => get<ProcessInspect>(`/processes/${pid}/inspect`, undefined, abort),
    8000,
    [pid],
    { enabled: pid !== null && live },
  )
  const data =
    inspect.data && row && inspect.data.createTime === row.createTime ? inspect.data : undefined
  return { data, error: data ? undefined : inspect.error, refresh: inspect.refresh }
}

/**
 * What the process is using, as the four readings the host Overview opens on:
 * each figure gliding to the two-second read over the process's recent shape —
 * the sampler keeps a point for every window it measures, the table's polls
 * and this page's alike, so a process opened from a table that has been open
 * a few minutes opens on those minutes.
 */
export function ProcessReadings({
  process: row,
  memTotal,
}: {
  process: ProcessRow
  memTotal: number
}) {
  const kind = uncontrollable(row)
  if (kind === "zombie") return null
  const history = row.history ?? []
  const memShare = memTotal > 0 ? (row.rss / memTotal) * 100 : (row.memPercent ?? 0)
  const fdShare =
    row.fdReady !== false && row.openFilesLimit && row.fileDescriptors !== undefined
      ? (row.fileDescriptors / row.openFilesLimit) * 100
      : undefined
  const io = (row.ioReadRate ?? 0) + (row.ioWriteRate ?? 0)
  const waiting = <span className="text-muted-foreground">—</span>
  return (
    // A kernel thread has its CPU and nothing else: one tile the width of four
    // read as a broken grid.
    <StatGrid
      columns={kind === "kernel" ? 2 : 4}
      dense
      data-testid="process-readings"
      className={kind === "kernel" ? "max-w-2xl" : undefined}
    >
      <StatTile
        label={
          <>
            <SeriesKey color={HUE.cpu} />
            CPU
          </>
        }
        tone={cpuTone(row.cpuPercent)}
        value={
          row.cpuReady === false ? (
            waiting
          ) : (
            <LiveFigure value={row.cpuPercent} decimals={1} unit="%" />
          )
        }
        trailing={row.cpuReady === false ? "measuring" : "of a core"}
        trend={
          <TileTrend
            values={history.map((p) => p.cpu)}
            color={HUE.cpu}
            label="CPU over the last minutes"
          />
        }
        hint={plural(row.threads, "thread")}
      />
      {kind !== "kernel" && (
        <>
          <StatTile
            label={
              <>
                <SeriesKey color={HUE.mem} />
                Memory
              </>
            }
            tone={memShare >= 25 ? "warning" : "default"}
            value={<LiveBytes value={row.rss} />}
            trailing={memTotal > 0 ? `${percent(memShare)} of host` : undefined}
            trend={
              <TileTrend
                values={history.map((p) => p.rss)}
                color={HUE.mem}
                label="Resident memory over the last minutes"
              />
            }
            hint={`${bytes(row.vms)} virtual${row.swap ? ` · ${bytes(row.swap)} swapped` : ""}`}
          />
          <StatTile
            label={
              <>
                <SeriesKey color={HUE.disk} />
                Disk I/O
              </>
            }
            value={row.ioReady ? <LiveBytes value={io} suffix="/s" /> : waiting}
            trend={
              <TileTrend
                values={history.map((p) => p.read + p.write)}
                color={HUE.disk}
                label="Disk reads and writes over the last minutes"
              />
            }
            hint={
              row.ioReady
                ? `read ${bytes(row.ioReadRate ?? 0)}/s · write ${bytes(row.ioWriteRate ?? 0)}/s`
                : "measuring"
            }
          />
          <StatTile
            label={
              <>
                <SeriesKey color={HUE.net} />
                Open files
              </>
            }
            tone={
              fdShare === undefined
                ? "default"
                : fdShare >= 90
                  ? "danger"
                  : fdShare >= 75
                    ? "warning"
                    : "default"
            }
            value={
              row.fdReady === false || row.fileDescriptors === undefined ? (
                waiting
              ) : (
                <LiveFigure value={row.fileDescriptors} />
              )
            }
            trailing={row.openFilesLimit ? `of ${row.openFilesLimit}` : undefined}
            meter={fdShare}
            meterLabel="Open files against the limit"
            hint={
              row.fdReady === false
                ? "not readable as this account"
                : row.connections
                  ? plural(row.connections, "open connection")
                  : "no open connections"
            }
          />
        </>
      )}
    </StatGrid>
  )
}

/** A counter in a grid of them; the unit sheet's last run draws its own the same way. */
export function Counter({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex min-w-0 items-baseline justify-between gap-3 border-b border-hairline py-1">
      <dt className="truncate text-xs text-muted-foreground">{label}</dt>
      <dd className="numeric truncate font-mono text-xs">{children}</dd>
    </div>
  )
}

/**
 * The nice value as a stepper, not the forty-option dropdown it replaced. The
 * dropdown rendered blank whenever the kernel reported a value outside its
 * −20…19 list, with no way to tell an unset control from a broken one; a
 * number that is always drawn cannot fail that way.
 */
export function PriorityControl({
  process: row,
  onChanged,
}: {
  process: ProcessRow
  onChanged: () => void
}) {
  const { can } = useAuth()
  // The value being set, against the value it was set from. A nice changed
  // elsewhere while the page is open moves the stepper with it until the
  // operator touches it; after that the edit is theirs.
  const [draft, setDraft] = useState<{ from: number; value: number }>({
    from: row.nice,
    value: row.nice,
  })
  const [saving, setSaving] = useState(false)
  if (draft.from !== row.nice && draft.value === draft.from) {
    setDraft({ from: row.nice, value: row.nice })
  }
  const allowed = can("system.admin")
  const nice = draft.value
  const setNice = (value: number) => setDraft((d) => ({ ...d, value: clampNice(value) }))

  const save = async () => {
    setSaving(true)
    try {
      await put(`/processes/${row.pid}/priority`, { nice, startedAt: row.createTime })
      notify.success(`Priority updated for ${row.name}`)
      setDraft({ from: nice, value: nice })
      onChanged()
    } catch (error) {
      notify.error("Could not set priority", error)
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="space-y-2">
      <div className="flex flex-wrap items-center gap-2">
        <Button
          size="sm"
          variant="outline"
          aria-label="Lower nice value (higher priority)"
          disabled={!allowed || nice <= -20 || saving}
          onClick={() => setNice(nice - 1)}
        >
          <Minus className="size-3.5" />
        </Button>
        <span
          aria-label="Nice value"
          className="numeric w-10 text-center font-mono text-title font-medium"
        >
          {nice}
        </span>
        <Button
          size="sm"
          variant="outline"
          aria-label="Raise nice value (lower priority)"
          disabled={!allowed || nice >= 19 || saving}
          onClick={() => setNice(nice + 1)}
        >
          <Plus className="size-3.5" />
        </Button>
        <span className="min-w-0 flex-1 truncate text-body text-muted-foreground">
          {niceMeaning(nice)}
        </span>
        {allowed && (
          <Button
            size="sm"
            variant="outline"
            disabled={nice === row.nice || saving}
            onClick={() => void save()}
          >
            {saving ? "Saving…" : "Save priority"}
          </Button>
        )}
      </div>
      {!allowed && (
        <p className="text-hint text-muted-foreground">Changing it needs system administration.</p>
      )}
      {(row.nice < -20 || row.nice > 19) && (
        <p className="text-hint text-warning">
          Reported as {row.nice}, outside the −20…19 range this control can set. Saving applies the
          shown value clamped into range.
        </p>
      )}
    </div>
  )
}

/**
 * The bias the kernel's out-of-memory killer applies to the process, as the
 * five settings anybody means by it, with the score it gives the process now.
 */
export function OOMControl({
  process: row,
  inspect,
  onChanged,
}: {
  process: ProcessRow
  inspect: ProcessInspect | undefined
  onChanged: () => void
}) {
  const { can } = useAuth()
  const [saving, setSaving] = useState<number | null>(null)
  const memory = inspect?.memory
  if (!memory?.oomReady) {
    return <p className="text-hint text-muted-foreground">Not readable as this account.</p>
  }
  const allowed = can("system.admin")
  const words = oomWords(memory.oomScore, memory.oomScoreAdj)
  const set = async (adj: number) => {
    setSaving(adj)
    try {
      await put(`/processes/${row.pid}/oom`, { adj, startedAt: row.createTime })
      notify.success(`Out-of-memory bias for ${row.name} set to ${adj}`)
      onChanged()
    } catch (error) {
      notify.error("Could not set the out-of-memory bias", error)
    } finally {
      setSaving(null)
    }
  }
  return (
    <div className="space-y-3">
      <p className="text-body">
        Scored <span className="numeric font-mono font-medium">{memory.oomScore}</span>
        {memory.oomScoreAdj !== 0 && (
          <span className="text-muted-foreground">
            {" "}
            with a bias of <span className="numeric font-mono">{memory.oomScoreAdj}</span>
          </span>
        )}
        :{" "}
        <span
          className={cn(
            words.tone === "warning" && "text-warning",
            words.tone === "success" && "text-success",
          )}
        >
          {words.word}
        </span>{" "}
        <span className="text-muted-foreground">when memory runs out.</span>
      </p>
      <div role="group" aria-label="Out-of-memory bias" className="flex flex-wrap gap-1.5">
        {OOM_PRESETS.map((preset) => {
          const current = preset.adj === memory.oomScoreAdj
          return (
            <Button
              key={preset.adj}
              size="xs"
              variant={current ? "secondary" : "outline"}
              aria-pressed={current}
              disabled={!allowed || saving !== null}
              onClick={() => void set(preset.adj)}
            >
              {saving === preset.adj ? "Saving…" : preset.label}
              <span className="numeric font-mono text-muted-foreground">{preset.adj}</span>
            </Button>
          )
        })}
      </div>
      {!allowed && (
        <p className="text-hint text-muted-foreground">Changing it needs system administration.</p>
      )}
    </div>
  )
}

/** A process as seen from another's page: a child, a parent. */
export function ProcessLinkRow({ link, onClick }: { link: ProcessLink; onClick: () => void }) {
  return (
    <Row
      onClick={onClick}
      mono
      leading={
        <ProductLogo id={processProduct(link.name)} size="sm" fallback={Cpu} className="size-7" />
      }
      title={
        <span className="flex min-w-0 items-center gap-2">
          <span className="truncate">{link.name}</span>
          <span className="numeric font-mono text-hint font-normal text-muted-foreground">
            {link.pid}
          </span>
        </span>
      }
      subtitle={link.cmdline || "kernel thread"}
      trailing={
        <>
          <span
            className={cn(
              "numeric font-mono text-hint",
              cpuTone(link.cpuPercent) === "default" ? "text-muted-foreground" : "text-warning",
            )}
          >
            {link.cpuReady === false ? "—" : percent(link.cpuPercent)}
          </span>
          <span className="numeric font-mono text-hint text-muted-foreground">
            {bytes(link.rss)}
          </span>
          <Status state={processStateTone(link.state)} label={link.state} />
        </>
      }
    />
  )
}

function clampNice(value: number): number {
  if (!Number.isFinite(value)) return 0
  return Math.max(-20, Math.min(19, Math.round(value)))
}

function niceMeaning(value: number): string {
  if (value < 0) return "Higher priority than normal"
  if (value > 0) return "Lower priority than normal"
  return "Normal priority"
}
