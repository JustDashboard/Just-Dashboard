"use client"

import { useMemo, useState } from "react"
import type { Tone } from "@/components/tone"
import Link from "next/link"
import { Cpu, Minus, Plus } from "@/components/icons"
import { get, put } from "@/lib/api"
import { bytes, duration, percent, relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { ProcessLink, ProcessRow, ProcessTree } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { useConfirm } from "@/components/confirm-dialog"
import { Detail, DetailList } from "@/components/page"
import { Panel, PanelBody, PanelHeader, Well } from "@/components/panel"
import { Row, RowList } from "@/components/row-list"
import { SidePanel } from "@/components/side-panel"
import { EmptyNote, EmptyState, ErrorState, LoadingRows, Spinner } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Meter } from "@/components/meter"
import { Tag } from "@/components/tag"
import { VerbBar } from "@/components/verbs"
import { Button } from "@/components/ui/button"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { useProcessControl, useProcessVerbs } from "@/components/procs/process-actions"
import { cpuTone, managerHref, managerName, processStateTone } from "@/components/procs/shared"

/**
 * One process, opened. Addressed by PID rather than by the row that was
 * clicked, so a child in the tree tab and a deep link both open the same
 * sheet — and so the sheet can outlive a poll that has already dropped the
 * row because the process exited.
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
  const { pending, signal } = useProcessControl(onChanged)
  const detail = usePoll(
    (abort) => get<ProcessRow>(`/processes/${pid}`, undefined, abort),
    4000,
    [pid],
    { enabled: pid !== null },
  )
  const row = detail.data
  const busy = row ? pending[`${row.pid}-${row.createTime}`] : undefined

  return (
    <SidePanel
      open={pid !== null}
      onOpenChange={onOpenChange}
      title={row?.name ?? `PID ${pid ?? ""}`}
      description={row ? `PID ${row.pid} · ${row.username || "unknown user"}` : "Process detail"}
      width="md"
      actions={
        row && <ProcessSheetActions process={row} busy={busy} confirm={confirm} signal={signal} />
      }
      bodyClassName="flex min-h-0 flex-1 flex-col p-4"
    >
      {pid !== null && detail.loading && !row && <Spinner />}
      {pid !== null && detail.error && !row && (
        <EmptyState
          icon={Cpu}
          title="This process is no longer running"
          description="Short-lived processes can exit between opening the row and reading its details."
        />
      )}
      {row && (
        <ProcessDetail
          key={pid}
          process={row}
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
  busy,
  confirm,
  signal,
}: {
  process: ProcessRow
  busy?: string
  confirm: Parameters<typeof useProcessVerbs>[0]["confirm"]
  signal: Parameters<typeof useProcessVerbs>[0]["signal"]
}) {
  const verbs = useProcessVerbs({ process, confirm, signal })
  return (
    <>
      <Status
        state={busy ? "activating" : processStateTone(process.state)}
        label={busy ? `${busy}…` : process.state}
      />
      <VerbBar verbs={verbs} />
    </>
  )
}

function ProcessDetail({
  process: row,
  memTotal,
  onSelect,
  onChanged,
}: {
  process: ProcessRow
  memTotal: number
  onSelect: (pid: number) => void
  onChanged: () => void
}) {
  const [tab, setTab] = useState("overview")
  // Read once when the sheet opens: uptime is a fact about the process, and
  // a clock that ticks during render is a render that never settles.
  const [openedAt] = useState(() => Date.now())
  const owner = managerHref(row)
  const memShare = memTotal > 0 ? (row.rss / memTotal) * 100 : (row.memPercent ?? 0)
  const fdShare =
    row.openFilesLimit && row.fileDescriptors ? (row.fileDescriptors / row.openFilesLimit) * 100 : 0

  return (
    <Tabs value={tab} onValueChange={setTab} className="flex min-h-0 flex-1 flex-col gap-4">
      <TabsList className="w-fit shrink-0">
        <TabsTrigger value="overview">Overview</TabsTrigger>
        <TabsTrigger value="tree">Tree</TabsTrigger>
      </TabsList>

      <TabsContent value="overview" className="flex min-h-0 flex-1 animate-rise flex-col gap-6">
        {/* What this process is: the facts that were the header's caption,
            as data. */}
        <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-body text-muted-foreground">
          <span className="numeric font-mono">PID {row.pid}</span>
          <Dot />
          <span>{row.username || "unknown user"}</span>
          <Dot />
          <span>started {relativeTime(row.createTime)}</span>
          <Tag>{managerName(row.manager)}</Tag>
        </div>

        <Panel plain>
          <PanelHeader title="Identity" />
          <PanelBody className="space-y-4">
            <DetailList>
              <Detail label="Managed by">
                {owner ? (
                  <Link href={owner} className="font-medium hover:underline">
                    {row.managerName}
                  </Link>
                ) : (
                  <>
                    {managerName(row.manager)}
                    {row.managerName ? ` · ${row.managerName}` : ""}
                  </>
                )}
              </Detail>
              <Detail label="Parent">
                {row.ppid > 0 ? (
                  <button
                    type="button"
                    className="numeric font-mono hover:underline"
                    onClick={() => onSelect(row.ppid)}
                  >
                    PID {row.ppid}
                  </button>
                ) : (
                  "—"
                )}
              </Detail>
              <Detail label="Executable" className="font-mono break-all">
                {row.exe || "Not reported"}
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
                  "Not reported"
                )}
              </Detail>
              <Detail label="Uptime">
                {duration(Math.max(0, (openedAt - new Date(row.createTime).getTime()) / 1000))}
              </Detail>
              <Detail label="Nice value" className="numeric">
                {row.nice}
              </Detail>
            </DetailList>
            <Well className="max-h-36 whitespace-pre-wrap">{row.cmdline || row.name}</Well>
          </PanelBody>
        </Panel>

        <Panel plain>
          <PanelHeader title="Resources" />
          <PanelBody className="space-y-4">
            {/*
              Bars rather than history charts: per-process usage is a point in
              time — nothing records this process's series, so a chart would
              draw an empty grid that fills in while the panel is open.
            */}
            <div className="space-y-3">
              <Reading
                label="CPU"
                value={percent(row.cpuPercent)}
                pct={row.cpuPercent}
                tone={cpuTone(row.cpuPercent)}
              />
              <Reading
                label="Memory"
                value={
                  memTotal > 0
                    ? `${bytes(row.rss)} · ${memShare.toFixed(1)}% of host`
                    : bytes(row.rss)
                }
                pct={memShare}
                tone={memShare >= 25 ? "warning" : "default"}
              />
              {row.openFilesLimit ? (
                <Reading
                  label="Open files"
                  value={`${row.fileDescriptors ?? 0} of ${row.openFilesLimit}`}
                  pct={fdShare}
                  tone={fdShare >= 90 ? "danger" : fdShare >= 75 ? "warning" : "default"}
                />
              ) : null}
            </div>
            <DetailList>
              <Detail label="Virtual memory">{bytes(row.vms)}</Detail>
              <Detail label="Disk read">
                {bytes(row.ioReadBytes ?? 0)}
                {(row.ioReadRate ?? 0) > 0 && (
                  <span className="ml-1 text-muted-foreground">{bytes(row.ioReadRate)}/s</span>
                )}
              </Detail>
              <Detail label="Disk written">
                {bytes(row.ioWriteBytes ?? 0)}
                {(row.ioWriteRate ?? 0) > 0 && (
                  <span className="ml-1 text-muted-foreground">{bytes(row.ioWriteRate)}/s</span>
                )}
              </Detail>
              <Detail label="Threads" className="numeric">
                {row.threads}
              </Detail>
              <Detail label="Child processes" className="numeric">
                {row.children ?? 0}
              </Detail>
              {!row.openFilesLimit && (
                <Detail label="File descriptors" className="numeric">
                  {row.fileDescriptors ?? "Not reported"}
                </Detail>
              )}
            </DetailList>
          </PanelBody>
        </Panel>

        <Panel plain>
          <PanelHeader
            title="Network"
            actions={
              row.connections ? (
                <span className="numeric text-hint text-muted-foreground">
                  {row.connections} open {row.connections === 1 ? "connection" : "connections"}
                </span>
              ) : undefined
            }
          />
          <PanelBody>
            {row.listening && row.listening.length > 0 ? (
              <div className="flex flex-wrap gap-1.5">
                {row.listening.map((port) => (
                  <Tag mono key={`${port.proto}/${port.address}/${port.port}`}>
                    {port.proto} {port.address || "*"}:{port.port}
                  </Tag>
                ))}
              </div>
            ) : (
              <EmptyNote className="py-3 text-left">Not listening on any port.</EmptyNote>
            )}
          </PanelBody>
        </Panel>

        <PriorityPanel process={row} onChanged={onChanged} />
      </TabsContent>

      <TabsContent value="tree" className="min-h-0 flex-1 overflow-y-auto">
        {tab === "tree" && <TreeTab pid={row.pid} onSelect={onSelect} />}
      </TabsContent>
    </Tabs>
  )
}

function Dot() {
  return <span className="text-muted-foreground/40">·</span>
}

/** One point-in-time share with its scale, for values that have no history. */
function Reading({
  label,
  value,
  pct,
  tone,
}: {
  label: string
  value: React.ReactNode
  pct: number
  tone: Tone
}) {
  return (
    <div>
      <div className="flex items-baseline justify-between gap-2 text-xs">
        <span className="text-muted-foreground">{label}</span>
        <span className="numeric font-mono font-medium">{value}</span>
      </div>
      <Meter value={Number.isFinite(pct) ? pct : 0} tone={tone} label={label} className="mt-1.5" />
    </div>
  )
}

function PriorityPanel({
  process: row,
  onChanged,
}: {
  process: ProcessRow
  onChanged: () => void
}) {
  const { can } = useAuth()
  const [nice, setNice] = useState(row.nice)
  if (!can("system.admin")) return null

  const save = async () => {
    await put(`/processes/${row.pid}/priority`, { nice, startedAt: row.createTime })
    notify.success(`Priority updated for ${row.name}`)
    onChanged()
  }

  return (
    <Panel plain>
      <PanelHeader title="Priority" />
      <PanelBody className="space-y-3">
        <p className="text-xs text-muted-foreground">
          How eagerly the scheduler runs this process. Lower is sooner: −20 runs first, 19 runs
          last, 0 is ordinary.
        </p>
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
            disabled={nice <= -20}
            onClick={() => setNice((n) => clampNice(n - 1))}
          >
            <Minus className="size-3.5" />
          </Button>
          <div className="min-w-24 flex-1 text-center">
            <p className="numeric font-mono text-title leading-none font-medium">{nice}</p>
            <p className="mt-1 text-hint text-muted-foreground">{niceMeaning(nice)}</p>
          </div>
          <Button
            size="sm"
            variant="outline"
            aria-label="Raise nice value (lower priority)"
            disabled={nice >= 19}
            onClick={() => setNice((n) => clampNice(n + 1))}
          >
            <Plus className="size-3.5" />
          </Button>
          <Button
            size="sm"
            variant="outline"
            disabled={nice === row.nice}
            onClick={() => save().catch((error) => notify.error("Could not set priority", error))}
          >
            Save priority
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
 * once is the loop this tab exists to break — so the parents are a list you
 * can climb, and each child opens in the same sheet.
 */
function TreeTab({ pid, onSelect }: { pid: number; onSelect: (pid: number) => void }) {
  const tree = usePoll(
    (signal) => get<ProcessTree>(`/processes/${pid}/tree`, undefined, signal),
    6000,
    [pid],
  )
  const children = useMemo(() => tree.data?.children ?? [], [tree.data])
  if (tree.loading && !tree.data) return <LoadingRows rows={4} />
  if (tree.error && !tree.data) return <ErrorState error={tree.error} />
  const ancestors = tree.data?.ancestors ?? []

  return (
    <div className="flex animate-rise flex-col gap-6">
      <Panel plain>
        <PanelHeader title="Parent chain" />
        <PanelBody className="py-2">
          {ancestors.length === 0 ? (
            <EmptyNote className="py-3 text-left">This is the top of its tree.</EmptyNote>
          ) : (
            <RowList>
              {ancestors.map((link) => (
                <ProcessLinkRow key={link.pid} link={link} onClick={() => onSelect(link.pid)} />
              ))}
            </RowList>
          )}
        </PanelBody>
      </Panel>
      <Panel plain>
        <PanelHeader
          title="Children"
          actions={
            <span className="numeric text-hint text-muted-foreground">{children.length}</span>
          }
        />
        <PanelBody className="py-2">
          {children.length === 0 ? (
            <EmptyNote className="py-3 text-left">No child processes.</EmptyNote>
          ) : (
            <RowList>
              {children.map((link) => (
                <ProcessLinkRow key={link.pid} link={link} onClick={() => onSelect(link.pid)} />
              ))}
            </RowList>
          )}
        </PanelBody>
      </Panel>
    </div>
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
      subtitle={link.cmdline || "Kernel worker"}
      trailing={
        <>
          <span className="numeric font-mono text-hint text-muted-foreground">
            {percent(link.cpuPercent)}
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
