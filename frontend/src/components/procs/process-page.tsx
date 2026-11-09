"use client"

import { useCallback, useEffect, useRef, useState } from "react"
import Link from "next/link"
import { useParams, useRouter, useSearchParams } from "next/navigation"
import { Tabs as TabsPrimitive } from "radix-ui"
import { ArrowLeft, ArrowUpRight, ChevronDown, ChevronUp, Copy, Cpu } from "@/components/icons"
import { ApiError, get } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import { duration, plural, timestamp } from "@/lib/format"
import type { ProcessList, ProcessRow, ProcessTree } from "@/lib/types"
import { cn } from "@/lib/utils"
import { useSessionState, useViewState } from "@/lib/view-state"
import { useMetrics } from "@/hooks/use-metrics"
import { usePoll } from "@/hooks/use-poll"
import { useConfirm } from "@/components/confirm-dialog"
import { useNow } from "@/components/deploy/vocabulary"
import { rowReveal, IconAction } from "@/components/icon-action"
import { FactDot, HostIdentity } from "@/components/metrics/host-identity"
import { Page, PageContext } from "@/components/page"
import {
  ProductGlyph,
  ProductLogo,
  hasProductLogo,
  processProduct,
  unitProduct,
} from "@/components/product-logo"
import { EmptyState, ErrorState, LoadingRows } from "@/components/state"
import { Status, StatusDot } from "@/components/status-dot"
import { ChipCount, tabClasses } from "@/components/tabs"
import { VerbBar } from "@/components/verbs"
import { Tabs, TabsContent } from "@/components/ui/tabs"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { Workspace, WorkspaceHelp } from "@/components/workspace/workspace"
import { useProcessInspect, useProcessReading } from "@/components/procs/process-detail"
import { processHref } from "@/components/procs/process-facts"
import { ProcessOverview } from "@/components/procs/process-overview"
import { ProcessRail } from "@/components/procs/process-rail"
import {
  ProcessControl,
  ProcessEnvironment,
  ProcessFiles,
  ProcessLimits,
  ProcessLogs,
  ProcessNetwork,
  ProcessThreads,
  ownerLogSources,
  useOwner,
} from "@/components/procs/process-views"
import { useProcessControl, useProcessVerbs } from "@/components/procs/process-actions"
import { useInspectionOrder } from "@/components/procs/inspection-order"
import {
  PROCESS_FILTER_KEYS,
  automaticFocus,
  type ProcessSort,
} from "@/components/procs/process-table"
import {
  managerHref,
  managerName,
  ownerName,
  processKey,
  processStateTone,
  uncontrollable,
} from "@/components/procs/shared"

const TABS = [
  "overview",
  "threads",
  "files",
  "network",
  "environment",
  "limits",
  "control",
  "logs",
] as const
type Tab = (typeof TABS)[number]

const BACK = (
  <Link
    href="/processes"
    className="inline-flex items-center gap-1 rounded-sm focus-ring hover:underline"
  >
    <ArrowLeft className="size-3" /> Processes
  </Link>
)

/**
 * One process, as a place of its own.
 *
 * It was a `SidePanel` over the process table until 0.8.6. Six blocks of
 * readings, a lineage, the counters and a priority stepper in a sheet's
 * width, over a table nobody could read behind it, is a process crammed into
 * a drawer — and everything else the kernel keeps about a process (its
 * threads, descriptors, limits, environment, the signals it handles) had
 * nowhere to go. So a process is a destination with a way back, the way a
 * container is (`docker/container-detail`), and its views are the underlined
 * strip under its identity line.
 *
 * What the sheet did well — the table beside the thing being read — is the
 * column on the left from `xl` (`ProcessRail`): the table's own ranking,
 * filters and search folded into a list, so checking the next worker is one
 * press, Alt+↑ and Alt+↓ walk it, and the view on screen goes with you. On a
 * phone the page is the one pane, with the way back and the same two arrows.
 */
export function ProcessPage() {
  const params = useParams<{ pid: string }>()
  const search = useSearchParams()
  const router = useRouter()
  const pid = params.pid && /^\d+$/.test(params.pid) ? Number(params.pid) : null
  const requested = search.get("tab")
  const [remembered, remember] = useViewState<string>("processes.process.tab", "overview")
  const [chosen, setChosen] = useState<string>(requested ?? remembered)
  const tab: Tab = (TABS as readonly string[]).includes(chosen) ? (chosen as Tab) : "overview"
  const setTab = useCallback(
    (next: string) => {
      setChosen(next)
      remember(next)
      // The address says which view, so a link to a process's files opens on
      // its files; replaced rather than pushed, because the view is not a
      // place Back should step through.
      const url = new URL(window.location.href)
      if (next === "overview") url.searchParams.delete("tab")
      else url.searchParams.set("tab", next)
      window.history.replaceState(null, "", `${url.pathname}${url.search}`)
    },
    [remember],
  )
  const open = useCallback((next: number) => router.push(processHref(next, tab)), [router, tab])

  const rail = useRail()
  const at = rail.rows.findIndex((row) => row.pid === pid)
  const adjacent = (direction: number) => {
    const next = at >= 0 ? rail.rows[at + direction] : rail.rows[direction > 0 ? 0 : -1]
    if (next) open(next.pid)
  }
  const reading = useProcessReading(pid)

  return (
    <Workspace
      name="Process"
      stateKey="process.page"
      refresh={reading.refresh}
      commands={[
        {
          id: "previous",
          label: "Previous process",
          keys: "Alt+↑",
          chord: "Alt+ArrowUp",
          disabled: at <= 0,
          run: () => adjacent(-1),
        },
        {
          id: "next",
          label: "Next process",
          keys: "Alt+↓",
          chord: "Alt+ArrowDown",
          disabled: rail.rows.length === 0 || at >= rail.rows.length - 1,
          run: () => adjacent(1),
        },
      ]}
    >
      {/* Held to the window where the rail stands beside it, so the list and
          the identity line stay put while a view scrolls; narrower, the page
          is the one pane and scrolls as a whole, so the header gives the
          screen back to the view. */}
      <Page fill="xl" className="gap-0 md:gap-0">
        <div className="flex min-w-0 flex-1 gap-6 xl:min-h-0">
          <ProcessRail
            className="hidden w-72 shrink-0 xl:flex"
            rows={rail.rows}
            current={pid}
            currentRow={reading.row}
            loading={rail.loading}
            query={rail.query}
            onQuery={rail.setQuery}
            filtered={rail.filtered}
            onClearFilters={rail.clearFilters}
            caption={rail.caption}
            onOpen={open}
            bindings={rail.bindings}
          />
          <ProcessMain
            key={pid ?? "none"}
            pid={pid}
            reading={reading}
            tab={tab}
            setTab={setTab}
            onOpen={open}
            previous={{ disabled: at <= 0, run: () => adjacent(-1) }}
            next={{
              disabled: rail.rows.length === 0 || at >= rail.rows.length - 1,
              run: () => adjacent(1),
            }}
          />
        </div>
      </Page>
    </Workspace>
  )
}

/**
 * The table's list, read for the rail: the same filters, search and ranking
 * the table was left on (they are its session's), at the table's cadence.
 */
function useRail() {
  const [query, setQuery] = useSessionState(PROCESS_FILTER_KEYS.query, "")
  const [user, setUser] = useSessionState(PROCESS_FILTER_KEYS.user, "")
  const [state, setState] = useSessionState(PROCESS_FILTER_KEYS.state, "")
  const [manager, setManager] = useSessionState(PROCESS_FILTER_KEYS.manager, "")
  const [group, setGroup] = useSessionState(PROCESS_FILTER_KEYS.group, "")
  const [sort] = useViewState<ProcessSort>(PROCESS_FILTER_KEYS.sort, "auto")
  const [refreshSeconds] = useViewState(PROCESS_FILTER_KEYS.refresh, 4)
  const { snapshot } = useMetrics()
  const automatic = automaticFocus(snapshot)
  const effective = sort === "auto" ? automatic.sort : sort
  const applied = useDebounced(query, 250)
  const list = usePoll(
    (signal) =>
      get<ProcessList>(
        "/processes/inventory",
        {
          limit: 100,
          q: applied,
          sort: effective,
          user: user || undefined,
          state: state || undefined,
          manager: manager || undefined,
          group: group || undefined,
        },
        signal,
      ),
    Math.max(refreshSeconds, 4) * 1000,
    [applied, user, state, manager, group, effective],
  )
  const listKey = [applied, user, state, manager, group, effective].join("\u0000")
  const inspection = useInspectionOrder(list.data?.processes ?? [], processKey, listKey)
  const filtered = Boolean(user || state || manager || group)
  const words: Record<string, string> = {
    cpu: "highest CPU",
    memory: "highest memory",
    io: "highest disk I/O",
    uptime: "longest-running",
  }
  const data = list.data
  const caption = data
    ? `${data.truncated ? `${data.processes.length} of ${plural(data.total, "process", "processes")}` : plural(data.total, "process", "processes")} · ${words[effective]}`
    : "Reading the processes…"
  return {
    rows: inspection.rows,
    bindings: inspection.bindings,
    loading: list.loading,
    query,
    setQuery,
    filtered,
    clearFilters: () => {
      setUser("")
      setState("")
      setManager("")
      setGroup("")
    },
    caption,
  }
}

type Step = { disabled: boolean; run: () => void }

function ProcessMain({
  pid,
  reading,
  tab,
  setTab,
  onOpen,
  previous,
  next,
}: {
  pid: number | null
  reading: ReturnType<typeof useProcessReading>
  tab: Tab
  setTab: (tab: string) => void
  onOpen: (pid: number) => void
  previous: Step
  next: Step
}) {
  const { confirm, dialog } = useConfirm()
  const { snapshot } = useMetrics()
  const memTotal = snapshot?.memory?.total ?? 0
  const { row, gone } = reading
  const live = Boolean(row) && !gone
  const inspect = useProcessInspect(row, live)
  const tree = usePoll(
    (signal) => get<ProcessTree>(`/processes/${pid}/tree`, undefined, signal),
    6000,
    [pid],
    { enabled: pid !== null && live },
  )
  const { owner, refresh: refreshOwner } = useOwner(row)
  const refreshInspect = inspect.refresh
  const refreshTree = tree.refresh
  const refreshReading = reading.refresh
  const changed = useCallback(() => {
    refreshReading()
    refreshInspect()
    refreshTree()
    refreshOwner()
  }, [refreshReading, refreshInspect, refreshTree, refreshOwner])
  const control = useProcessControl(changed)
  const children = tree.data?.children.length ?? row?.children ?? 0
  const busy = row ? control.pending[processKey(row)] : undefined

  const views = row
    ? [
        { key: "overview", label: "Overview" },
        { key: "threads", label: "Threads", count: inspect.data?.threadsTotal ?? row.threads },
        { key: "files", label: "Files", count: inspect.data?.files.total ?? row.fileDescriptors },
        {
          key: "network",
          label: "Network",
          count:
            (row.listening?.length ?? 0) + (inspect.data?.connectionsTotal ?? row.connections ?? 0),
        },
        { key: "environment", label: "Environment", count: inspect.data?.environment.length },
        { key: "limits", label: "Limits and privileges" },
        { key: "control", label: "Control" },
        ...(ownerLogSources(row, owner).length > 0 ? [{ key: "logs", label: "Logs" }] : []),
      ]
    : []
  const shown = views.some((view) => view.key === tab) ? tab : "overview"

  if (pid === null || (!row && reading.error)) {
    return (
      <div className="flex min-w-0 flex-1 flex-col gap-5">
        <PageContext eyebrow={BACK} title="Process" />
        {reading.error && !(reading.error instanceof ApiError && reading.error.status === 404) ? (
          <ErrorState error={reading.error} />
        ) : (
          <EmptyState
            icon={Cpu}
            title={
              pid === null ? "That is not a process ID" : `No process with PID ${pid} is running`
            }
            description="Short-lived processes can exit between being listed and being opened. Pick another from the list."
            action={
              <Link href="/processes" className="text-body font-medium text-brand hover:underline">
                Back to the processes
              </Link>
            }
          />
        )}
      </div>
    )
  }

  return (
    <div className="flex min-h-0 min-w-0 flex-1 flex-col gap-5">
      <div className="flex min-w-0 shrink-0 flex-col gap-5">
        <PageContext
          eyebrow={BACK}
          title={row?.name ?? `Process ${pid}`}
          actions={
            <>
              {row && (
                <HeaderVerbs
                  process={row}
                  gone={gone}
                  childCount={children}
                  confirm={confirm}
                  signal={control.signal}
                  onOpen={onOpen}
                />
              )}
              <span className="flex items-center">
                <IconAction
                  label="Previous process"
                  disabled={previous.disabled}
                  onClick={previous.run}
                >
                  <ChevronUp />
                </IconAction>
                <IconAction label="Next process" disabled={next.disabled} onClick={next.run}>
                  <ChevronDown />
                </IconAction>
                <span className="max-sm:hidden">
                  <WorkspaceHelp compact />
                </span>
              </span>
            </>
          }
        />
        {row ? (
          <ProcessIdentity
            process={row}
            gone={gone}
            busy={busy}
            waitChannel={inspect.data?.waitChannel}
            onOpen={onOpen}
          />
        ) : (
          <HostIdentity
            className="border-b-0 pb-0"
            title={<TextShimmer>{`Reading PID ${pid}…`}</TextShimmer>}
            facts={<span>&nbsp;</span>}
          />
        )}
      </div>

      {!row && <LoadingRows />}

      {row && (
        <Tabs value={shown} onValueChange={setTab} className="min-h-0 flex-1 gap-0">
          <TabsPrimitive.List
            aria-label={`Views of ${row.name}`}
            className="flex shrink-0 [scrollbar-width:none] gap-1 overflow-x-auto border-b border-hairline [&::-webkit-scrollbar]:hidden"
          >
            {views.map((view) => (
              <TabsPrimitive.Trigger
                key={view.key}
                value={view.key}
                className={tabClasses(shown === view.key, "h-10")}
              >
                {view.label}
                {view.count !== undefined && view.count > 0 && <ChipCount>{view.count}</ChipCount>}
              </TabsPrimitive.Trigger>
            ))}
          </TabsPrimitive.List>

          <TabsContent value="overview" className="min-h-0 flex-1 pt-6 xl:overflow-y-auto">
            <ProcessOverview
              process={row}
              gone={gone}
              inspect={inspect.data}
              tree={tree.data}
              memTotal={memTotal}
              onOpen={onOpen}
              onTab={setTab}
            />
          </TabsContent>
          <TabsContent
            value="threads"
            className="flex min-h-0 flex-1 flex-col pt-4 xl:overflow-y-auto"
          >
            {shown === "threads" && <ProcessThreads inspect={inspect.data} />}
          </TabsContent>
          <TabsContent value="files" className="min-h-0 flex-1 pt-4 xl:overflow-y-auto">
            {shown === "files" && <ProcessFiles inspect={inspect.data} />}
          </TabsContent>
          <TabsContent value="network" className="min-h-0 flex-1 pt-4 xl:overflow-y-auto">
            {shown === "network" && <ProcessNetwork row={row} inspect={inspect.data} />}
          </TabsContent>
          <TabsContent
            value="environment"
            className="flex min-h-0 flex-1 flex-col pt-4 xl:overflow-y-auto"
          >
            {shown === "environment" && <ProcessEnvironment inspect={inspect.data} />}
          </TabsContent>
          <TabsContent value="limits" className="min-h-0 flex-1 pt-4 xl:overflow-y-auto">
            {shown === "limits" && <ProcessLimits row={row} inspect={inspect.data} />}
          </TabsContent>
          <TabsContent value="control" className="min-h-0 flex-1 pt-6 xl:overflow-y-auto">
            {shown === "control" && (
              <ProcessControl
                row={row}
                gone={gone}
                inspect={inspect.data}
                childCount={children}
                owner={owner}
                confirm={confirm}
                signal={control.signal}
                pending={busy}
                onChanged={changed}
              />
            )}
          </TabsContent>
          <TabsContent
            value="logs"
            className="flex min-h-0 flex-1 flex-col pt-4 xl:overflow-y-auto"
          >
            {shown === "logs" && <ProcessLogs row={row} owner={owner} />}
          </TabsContent>
        </Tabs>
      )}
      {dialog}
    </div>
  )
}

/**
 * The page's verbs: Terminate and Kill as words, everything else behind the
 * menu — the other signals, ending it with what it started, and the ways of
 * carrying its identity somewhere else. A process that has gone has none.
 */
function HeaderVerbs({
  process,
  gone,
  childCount,
  confirm,
  signal,
  onOpen,
}: {
  process: ProcessRow
  gone?: string
  childCount: number
  confirm: Parameters<typeof useProcessVerbs>[0]["confirm"]
  signal: Parameters<typeof useProcessVerbs>[0]["signal"]
  onOpen: (pid: number) => void
}) {
  const verbs = useProcessVerbs({ process, confirm, signal, onOpen, children: childCount })
  if (gone) return null
  return <VerbBar verbs={verbs} />
}

/**
 * The line the page opens on, the one every destination in the product opens
 * on (`HostIdentity`): the process as the product it is with its state in the
 * tile's corner, its name and PID, and after them the facts a process is
 * opened to check — who runs it, what supervises it (with that supervisor's
 * own mark, a link to its page), how long it has been up, ticking, and its
 * parent. Its state stands at the line's end, with what it is waiting in when
 * it is waiting.
 */
function ProcessIdentity({
  process: row,
  gone,
  busy,
  waitChannel,
  onOpen,
}: {
  process: ProcessRow
  gone?: string
  busy?: string
  waitChannel?: string
  onOpen: (pid: number) => void
}) {
  const now = useNow(1000, !gone)
  const kind = uncontrollable(row)
  const started = new Date(row.createTime).getTime()
  const up = duration(Math.max(0, (now - started) / 1000))
  const owner = managerHref(row)
  const ownerProduct =
    row.manager === "systemd"
      ? (unitProduct(row.managerName ?? "") ?? "systemd")
      : row.manager === "pm2"
        ? "pm2"
        : row.manager === "container"
          ? (processProduct(row.managerLabel ?? "") ?? "docker")
          : undefined
  const product = processProduct(row.name)
  const state = gone ? "stopped" : processStateTone(row.state)
  return (
    <HostIdentity
      className="animate-rise border-b-0 pb-0"
      logo={
        <span className="relative flex shrink-0">
          <ProductLogo
            id={product ?? (kind === "kernel" ? "linux" : undefined)}
            fallback={Cpu}
            className="size-12 rounded-xl [&_img]:size-7"
          />
          <span
            aria-hidden
            className="absolute -right-1 -bottom-1 flex size-4 items-center justify-center rounded-full bg-background"
          >
            <StatusDot state={state} live={!gone && row.state === "running"} />
          </span>
        </span>
      }
      title={
        <span className="inline-flex min-w-0 items-baseline gap-2">
          <span className="truncate">{row.name}</span>
          <button
            type="button"
            onClick={() => void copyText(String(row.pid), "PID copied")}
            aria-label={`Copy PID ${row.pid}`}
            className="group inline-flex shrink-0 items-center gap-1 rounded-sm font-mono text-body font-normal text-muted-foreground focus-ring hover:text-foreground"
          >
            {row.pid}
            <Copy aria-hidden className={cn("size-3", rowReveal())} />
          </button>
        </span>
      }
      facts={
        <>
          <span className="font-mono">{row.username || "unknown user"}</span>
          <FactDot />
          {owner ? (
            <Link
              href={owner}
              className="inline-flex min-w-0 items-center gap-1.5 rounded-sm text-foreground focus-ring hover:underline"
            >
              {ownerProduct && hasProductLogo(ownerProduct) && <ProductGlyph id={ownerProduct} />}
              <span className="truncate">
                {managerName(row.manager)} · {ownerName(row)}
              </span>
              <ArrowUpRight aria-hidden className="size-3 shrink-0" />
            </Link>
          ) : (
            <span className={cn(row.manager === "unmanaged" && "text-warning")}>
              {managerName(row.manager)}
              {row.manager === "session" && row.managerName ? ` · ${row.managerName}` : ""}
            </span>
          )}
          <FactDot />
          <span
            className={cn("numeric", !gone && "text-foreground")}
            title={timestamp(row.createTime)}
          >
            {gone || kind === "zombie" ? `started ${up} ago` : `up ${up}`}
          </span>
          {row.ppid > 0 && (
            <>
              <FactDot />
              <button
                type="button"
                className="numeric rounded-sm focus-ring hover:text-foreground hover:underline"
                onClick={() => onOpen(row.ppid)}
              >
                parent {row.ppid}
              </button>
            </>
          )}
          <FactDot />
          <span className="numeric">{plural(row.threads, "thread")}</span>
        </>
      }
      aside={
        <span className="flex flex-col items-end gap-0.5 max-sm:items-start">
          {busy ? (
            <span className="inline-flex items-center gap-1.5 text-body font-medium">
              <StatusDot tone="notice" />
              <TextShimmer>{`${busy}…`}</TextShimmer>
            </span>
          ) : (
            <Status
              state={state}
              live={!gone && row.state === "running"}
              label={gone ?? row.state}
              className="text-body"
            />
          )}
          {!gone && !kind && waitChannel && row.state !== "running" && (
            <span className="font-mono text-hint text-muted-foreground">in {waitChannel}</span>
          )}
        </span>
      }
    />
  )
}

function useDebounced<T>(value: T, delay: number): T {
  const [settled, setSettled] = useState(value)
  const first = useRef(true)
  useEffect(() => {
    if (first.current) {
      first.current = false
      return
    }
    const timer = window.setTimeout(() => setSettled(value), delay)
    return () => window.clearTimeout(timer)
  }, [value, delay])
  return settled
}
