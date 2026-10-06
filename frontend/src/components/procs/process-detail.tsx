"use client"

import { useMemo, useState } from "react"
import Link from "next/link"
import { ArrowUpRight, Copy, Cpu, Minus, Plus } from "@/components/icons"
import { ApiError, get, put } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import { bytes, duration, percent, plural, timestamp } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { ProcessLink, ProcessRow, ProcessTree } from "@/lib/types"
import { cn } from "@/lib/utils"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { useConfirm } from "@/components/confirm-dialog"
import { ShellWords } from "@/components/deploy/run-evidence"
import { useNow } from "@/components/deploy/vocabulary"
import { IconAction } from "@/components/icon-action"
import { TileTrend } from "@/components/metrics/sparkline"
import { HUE, LiveBytes, LiveFigure } from "@/components/overview/readings"
import { Detail, DetailList } from "@/components/page"
import { Panel, PanelBody, PanelHeader, Well } from "@/components/panel"
import { ProductLogo, processProduct } from "@/components/product-logo"
import { Row, RowList } from "@/components/row-list"
import { SidePanel } from "@/components/side-panel"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { EmptyState, ErrorState, LoadingRows, Notice, Spinner } from "@/components/state"
import { Status, StatusDot } from "@/components/status-dot"
import { VerbBar } from "@/components/verbs"
import { Button } from "@/components/ui/button"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { useProcessControl, useProcessVerbs } from "@/components/procs/process-actions"
import {
  cpuTone,
  managerHref,
  managerName,
  ownerName,
  processKey,
  processStateTone,
  uncontrollable,
} from "@/components/procs/shared"

/** An open sheet reads its process this often: the figures are the point of opening it. */
const DETAIL_POLL = 2000

/** Children drawn before the rest fold behind a count. */
const CHILDREN_SHOWN = 8

type Seen = {
  pid: number | null
  /** The last reading of the process this sheet was opened on. */
  row?: ProcessRow
  /** Why the readings stopped: the process exited, or its PID now names another. */
  gone?: "exited" | "replaced"
}

/**
 * One process, opened. Addressed by PID rather than by the row that was
 * clicked, so a child in the lineage and a deep link both open the same sheet.
 *
 * The sheet holds on to the process it opened on. A process that exits while
 * it is open used to leave its last reading on screen under live-looking
 * verbs, polled into a 404 nobody saw — so the answer to a Kill was a sheet
 * that went on saying "running" — and a PID reused in the meantime silently
 * became somebody else's process under the same title. Either now stops the
 * reading, keeps the last one, and says which happened.
 */
export function ProcessDetailSheet({
  pid,
  memTotal,
  onOpenChange,
  onSelect,
  onChanged,
}: {
  pid: number | null
  memTotal: number
  onOpenChange: (open: boolean) => void
  /** Opens another process in this sheet — a parent or a child. */
  onSelect: (pid: number) => void
  onChanged: () => void
}) {
  const { confirm, dialog } = useConfirm()
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
  const { pending, signal } = useProcessControl(() => {
    detail.refresh()
    onChanged()
  })
  const busy = row ? pending[processKey(row)] : undefined

  return (
    <SidePanel
      open={pid !== null}
      onOpenChange={onOpenChange}
      // The sheet opens on the thing itself: its mark, its name, its PID.
      title={
        <>
          <ProductLogo id={row ? processProduct(row.name) : undefined} size="sm" fallback={Cpu} />
          <span className="min-w-0 truncate">{row?.name ?? `PID ${pid ?? ""}`}</span>
          {row && (
            <span className="numeric font-mono text-body font-normal text-muted-foreground">
              {row.pid}
            </span>
          )}
        </>
      }
      description={row ? `PID ${row.pid} · ${row.username || "unknown user"}` : "Process detail"}
      width="lg"
      actions={
        row && (
          <ProcessSheetActions
            process={row}
            gone={seen.gone}
            busy={busy}
            confirm={confirm}
            signal={signal}
            onSelect={onSelect}
          />
        )
      }
      bodyClassName="flex min-h-0 flex-1 flex-col overflow-y-auto"
    >
      {pid !== null && !row && !detail.error && <Spinner />}
      {pid !== null && !row && detail.error && (
        <EmptyState
          icon={Cpu}
          title="This process is no longer running"
          description="Short-lived processes can exit between opening the row and reading its details."
          className="m-5"
        />
      )}
      {row && (
        <ProcessDetail
          key={pid}
          process={row}
          gone={seen.gone}
          memTotal={memTotal}
          onSelect={onSelect}
          onChanged={() => {
            detail.refresh()
            onChanged()
          }}
        />
      )}
      {dialog}
    </SidePanel>
  )
}

function ProcessSheetActions({
  process,
  gone,
  busy,
  confirm,
  signal,
  onSelect,
}: {
  process: ProcessRow
  gone?: Seen["gone"]
  busy?: string
  confirm: Parameters<typeof useProcessVerbs>[0]["confirm"]
  signal: Parameters<typeof useProcessVerbs>[0]["signal"]
  onSelect: (pid: number) => void
}) {
  const verbs = useProcessVerbs({ process, confirm, signal, onOpen: onSelect })
  if (gone) {
    return <Status tone="stopped" label={gone === "replaced" ? "replaced" : "exited"} />
  }
  return (
    <>
      {busy ? (
        <span className="inline-flex items-center gap-1.5 text-xs font-medium">
          <StatusDot tone="notice" />
          <TextShimmer>{`${busy}…`}</TextShimmer>
        </span>
      ) : (
        <Status state={processStateTone(process.state)} label={process.state} />
      )}
      <VerbBar verbs={verbs} />
    </>
  )
}

/**
 * The sheet's body, one scroll rather than two tabs: what the process is
 * doing now, what it is, where it sits, and what can be changed.
 *
 * Its readings are four tiles that glide to each two-second read, each over
 * the process's recent shape — the sampler keeps a point for every window it
 * measures, the table's polls and this sheet's alike, so a process opened
 * from a table that has been open a few minutes opens on those minutes.
 * Under them, the command as it was run, coloured as a command is; the chain
 * of parents as a path and the children as rows; the counters; and the
 * priority.
 */
function ProcessDetail({
  process: row,
  gone,
  memTotal,
  onSelect,
  onChanged,
}: {
  process: ProcessRow
  gone?: Seen["gone"]
  memTotal: number
  onSelect: (pid: number) => void
  onChanged: () => void
}) {
  // Uptime is a fact about the process that changes while the sheet is open,
  // so it ticks — once a second, and not at all once the process is gone.
  const now = useNow(1000, !gone)
  const owner = managerHref(row)
  const kind = uncontrollable(row)
  const history = row.history ?? []
  const memShare = memTotal > 0 ? (row.rss / memTotal) * 100 : (row.memPercent ?? 0)
  const fdShare =
    row.fdReady !== false && row.openFilesLimit && row.fileDescriptors !== undefined
      ? (row.fileDescriptors / row.openFilesLimit) * 100
      : undefined
  const io = (row.ioReadRate ?? 0) + (row.ioWriteRate ?? 0)
  const started = new Date(row.createTime).getTime()

  return (
    <div className="flex animate-rise flex-col gap-6 px-5 pt-4 pb-6">
      {/* What this process is, as the facts after a name — the line every
          identity in the product draws. */}
      <p className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted-foreground">
        <span>{row.username || "unknown user"}</span>
        <Dot />
        {owner ? (
          <Link
            href={owner}
            className="inline-flex items-center gap-1 text-foreground hover:underline"
          >
            {managerName(row.manager)} · {ownerName(row)}
            <ArrowUpRight aria-hidden className="size-3" />
          </Link>
        ) : (
          <span>
            {managerName(row.manager)}
            {row.manager === "session" && row.managerName ? ` · ${row.managerName}` : ""}
          </span>
        )}
        <Dot />
        <span className="numeric" title={timestamp(row.createTime)}>
          {gone || kind === "zombie"
            ? `started ${duration(Math.max(0, (now - started) / 1000))} ago`
            : `up ${duration(Math.max(0, (now - started) / 1000))}`}
        </span>
        {row.ppid > 0 && (
          <>
            <Dot />
            <button
              type="button"
              className="numeric hover:text-foreground hover:underline"
              onClick={() => onSelect(row.ppid)}
            >
              parent {row.ppid}
            </button>
          </>
        )}
      </p>

      {gone && (
        <Notice
          tone="warning"
          title={
            gone === "replaced"
              ? "This PID now belongs to another process"
              : "This process has exited"
          }
        >
          {gone === "replaced"
            ? `${row.name} exited and PID ${row.pid} was given to a new process. What is below is ${row.name}'s last reading.`
            : `What is below is its last reading. A supervisor that restarts it starts it under a new PID.`}
        </Notice>
      )}
      {!gone && kind === "zombie" && (
        <Notice tone="danger" title="Exited, and not yet reaped">
          What is left is its exit status, which only its parent can collect; it holds no memory and
          takes no signal. One is harmless. Many mean the parent is not reaping its children, and
          restarting or ending the parent clears them.
        </Notice>
      )}
      {!gone && kind === "kernel" && (
        <Notice title="A kernel thread">
          The kernel runs it for itself. It has no command line, memory or files of its own, and
          does not take signals; its priority can still be set.
        </Notice>
      )}

      {kind !== "zombie" && (
        <StatGrid columns={4} dense className="-mx-5 border-y border-hairline">
          <StatTile
            className="px-5"
            label="CPU"
            tone={cpuTone(row.cpuPercent)}
            value={
              row.cpuReady === false ? (
                <span className="text-muted-foreground">—</span>
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
                className="px-5"
                label="Memory"
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
                className="px-5"
                label="Disk I/O"
                value={
                  row.ioReady ? (
                    <LiveBytes value={io} suffix="/s" />
                  ) : (
                    <span className="text-muted-foreground">—</span>
                  )
                }
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
                className="px-5"
                label="Open files"
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
                    <span className="text-muted-foreground">—</span>
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
      )}

      {row.cmdline && (
        <Panel plain>
          <PanelHeader
            title="Command"
            actions={
              <IconAction
                label="Copy command line"
                onClick={() => void copyText(row.cmdline, "Command line copied")}
              >
                <Copy />
              </IconAction>
            }
          />
          <PanelBody className="space-y-3 pt-3">
            <Well className="max-h-40 text-xs leading-relaxed break-all whitespace-pre-wrap">
              <ShellWords command={row.cmdline} />
            </Well>
            <DetailList>
              <Detail label="Executable" className="font-mono break-all">
                {row.exe || "Not readable"}
              </Detail>
              <Detail label="Working directory" className="font-mono break-all">
                {row.cwd ? (
                  <Link
                    href={`/files?path=${encodeURIComponent(row.cwd)}`}
                    className="hover:underline"
                  >
                    {row.cwd}
                  </Link>
                ) : (
                  "Not readable"
                )}
              </Detail>
            </DetailList>
          </PanelBody>
        </Panel>
      )}

      {row.listening && row.listening.length > 0 && (
        <Panel plain>
          <PanelHeader
            title="Listening"
            actions={
              <span className="numeric text-hint text-muted-foreground">
                {plural(row.connections ?? 0, "open connection")}
              </span>
            }
          />
          <ul className="divide-y divide-hairline">
            {row.listening.map((port) => (
              <li
                key={`${port.proto}/${port.address}/${port.port}`}
                className="flex min-w-0 items-baseline gap-3 py-2 text-body"
              >
                <span className="numeric w-16 shrink-0 font-mono font-medium">:{port.port}</span>
                <span className="w-12 shrink-0 font-mono text-hint text-muted-foreground uppercase">
                  {port.proto}
                </span>
                <span className="min-w-0 truncate text-muted-foreground">
                  {reach(port.address)}
                </span>
                <span className="ml-auto truncate font-mono text-hint text-muted-foreground">
                  {port.address || "*"}
                </span>
              </li>
            ))}
          </ul>
        </Panel>
      )}

      {!gone && <Lineage pid={row.pid} onSelect={onSelect} />}

      <Panel plain>
        <PanelHeader title="Counters" />
        <PanelBody className="pt-3">
          <dl className="grid grid-cols-2 gap-x-6 gap-y-1.5 sm:grid-cols-3">
            <Counter label="Started">{timestamp(row.createTime)}</Counter>
            <Counter label="Child processes">{row.children ?? 0}</Counter>
            <Counter label="Threads">{row.threads}</Counter>
            <Counter label="Read since start">{bytes(row.ioReadBytes ?? 0)}</Counter>
            <Counter label="Written since start">{bytes(row.ioWriteBytes ?? 0)}</Counter>
            {kind !== "kernel" && <Counter label="Shared memory">{bytes(row.shared ?? 0)}</Counter>}
          </dl>
        </PanelBody>
      </Panel>

      {!gone && kind !== "zombie" && <PriorityPanel process={row} onChanged={onChanged} />}
    </div>
  )
}

function Dot() {
  return <span className="text-muted-foreground/40">·</span>
}

function Counter({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex min-w-0 items-baseline justify-between gap-3 border-b border-hairline py-1">
      <dt className="truncate text-xs text-muted-foreground">{label}</dt>
      <dd className="numeric truncate font-mono text-xs">{children}</dd>
    </div>
  )
}

/** Who can reach a listening address. */
function reach(address: string) {
  if (!address || address === "0.0.0.0" || address === "::" || address === "*")
    return "every interface"
  if (address.startsWith("127.") || address === "::1") return "this machine only"
  return "one address"
}

function PriorityPanel({
  process: row,
  onChanged,
}: {
  process: ProcessRow
  onChanged: () => void
}) {
  const { can } = useAuth()
  // The value being set, against the value it was set from. A nice changed
  // elsewhere while the sheet is open moves the stepper with it until the
  // operator touches it; after that the edit is theirs.
  const [draft, setDraft] = useState<{ from: number; value: number }>({
    from: row.nice,
    value: row.nice,
  })
  const [saving, setSaving] = useState(false)
  if (draft.from !== row.nice && draft.value === draft.from) {
    setDraft({ from: row.nice, value: row.nice })
  }
  if (!can("system.admin")) return null
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
    <Panel plain>
      <PanelHeader
        title="Priority"
        actions={
          <span className="text-hint text-muted-foreground">−20 runs first, 19 runs last</span>
        }
      />
      <PanelBody className="space-y-2 pt-3">
        {/*
          A stepper, not the forty-option dropdown it replaces. The dropdown
          rendered blank whenever the kernel reported a value outside its
          −20…19 list, with no way to tell an unset control from a broken
          one; a number that is always drawn cannot fail that way.
        */}
        <div className="flex flex-wrap items-center gap-2">
          <Button
            size="sm"
            variant="outline"
            aria-label="Lower nice value (higher priority)"
            disabled={nice <= -20 || saving}
            onClick={() => setNice(nice - 1)}
          >
            <Minus className="size-3.5" />
          </Button>
          <span className="numeric w-10 text-center font-mono text-title font-medium">{nice}</span>
          <Button
            size="sm"
            variant="outline"
            aria-label="Raise nice value (lower priority)"
            disabled={nice >= 19 || saving}
            onClick={() => setNice(nice + 1)}
          >
            <Plus className="size-3.5" />
          </Button>
          <span className="min-w-0 flex-1 truncate text-body text-muted-foreground">
            {niceMeaning(nice)}
          </span>
          <Button
            size="sm"
            variant="outline"
            disabled={nice === row.nice || saving}
            onClick={() => void save()}
          >
            {saving ? "Saving…" : "Save priority"}
          </Button>
        </div>
        {(row.nice < -20 || row.nice > 19) && (
          <p className="text-hint text-warning">
            Reported as {row.nice}, outside the −20…19 range this control can set. Saving applies
            the shown value clamped into range.
          </p>
        )}
      </PanelBody>
    </Panel>
  )
}

/**
 * The chain above and the processes below. The remedy for a runaway worker is
 * usually its supervisor — killing a child that gunicorn or PM2 respawns at
 * once is the loop this exists to break — so the parents are a path you can
 * climb, and each child opens in the same sheet.
 */
function Lineage({ pid, onSelect }: { pid: number; onSelect: (pid: number) => void }) {
  const [all, setAll] = useState(false)
  const tree = usePoll(
    (signal) => get<ProcessTree>(`/processes/${pid}/tree`, undefined, signal),
    6000,
    [pid],
  )
  const children = useMemo(() => tree.data?.children ?? [], [tree.data])
  const ancestors = tree.data?.ancestors ?? []
  const shown = all ? children : children.slice(0, CHILDREN_SHOWN)

  return (
    <Panel plain>
      <PanelHeader
        title="Lineage"
        actions={
          tree.data && (
            <span className="numeric text-hint text-muted-foreground">
              {children.length === 0 ? "no children" : plural(children.length, "child", "children")}
            </span>
          )
        }
      />
      <PanelBody className="space-y-2 pt-3">
        {tree.loading && !tree.data && <LoadingRows rows={2} />}
        {tree.error && !tree.data && <ErrorState error={tree.error} />}
        {tree.data && (
          <>
            <nav aria-label="Parents" className="flex min-w-0 flex-wrap items-center gap-1 text-xs">
              {ancestors.length === 0 && (
                <span className="text-muted-foreground">The top of its tree.</span>
              )}
              {ancestors.map((link) => (
                <span key={link.pid} className="inline-flex min-w-0 items-center gap-1">
                  <button
                    type="button"
                    title={link.cmdline || link.name}
                    onClick={() => onSelect(link.pid)}
                    className="inline-flex max-w-56 min-w-0 items-center gap-1.5 rounded-md px-1.5 py-1 focus-ring-inset transition-colors hover:bg-row-hover"
                  >
                    <ProductLogo
                      id={processProduct(link.name)}
                      size="sm"
                      fallback={Cpu}
                      className="size-5 rounded-md [&_img]:size-3 [&_svg]:size-3"
                    />
                    <span className="truncate font-medium">{link.name}</span>
                    <span className="numeric font-mono text-muted-foreground">{link.pid}</span>
                  </button>
                  <span aria-hidden className="text-muted-foreground/50">
                    ›
                  </span>
                </span>
              ))}
              <span className="px-1.5 font-medium text-foreground">this process</span>
            </nav>
            {children.length > 0 && (
              <RowList>
                {shown.map((link) => (
                  <ProcessLinkRow key={link.pid} link={link} onClick={() => onSelect(link.pid)} />
                ))}
              </RowList>
            )}
            {children.length > CHILDREN_SHOWN && (
              <Button size="xs" variant="ghost" onClick={() => setAll((v) => !v)}>
                {all ? "Show fewer" : `Show all ${children.length}`}
              </Button>
            )}
          </>
        )}
      </PanelBody>
    </Panel>
  )
}

function ProcessLinkRow({ link, onClick }: { link: ProcessLink; onClick: () => void }) {
  return (
    <Row
      onClick={onClick}
      mono
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
