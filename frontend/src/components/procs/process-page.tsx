"use client"

import { INLINE_LINK } from "@/components/inline-link"
import { useCallback } from "react"
import Link from "next/link"
import { useParams, useRouter } from "next/navigation"
import { ArrowUpRight, Copy, Cpu, Logs } from "@/components/icons"
import { ApiError, get } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import { duration, plural, timestamp } from "@/lib/format"
import type { ProcessList, ProcessRow, ProcessTree } from "@/lib/types"
import { cn } from "@/lib/utils"
import { useSessionState, useViewState } from "@/lib/view-state"
import { useMetrics } from "@/hooks/use-metrics"
import { usePoll } from "@/hooks/use-poll"
import { useConfirm } from "@/components/confirm-dialog"
import { useNow } from "@/hooks/use-now"
import { rowReveal } from "@/components/icon-action"
import { DetailPage, useDetailTab, useSiblingSteps } from "@/components/detail-page"
import { Fact } from "@/components/fact-line"
import { IdentityHeader, IdentityVerbs } from "@/components/identity-header"
import { VerbMenu, type Verb } from "@/components/verbs"
import {
  ProductGlyph,
  ProductLogo,
  hasProductLogo,
  processProduct,
  unitProduct,
} from "@/components/product-logo"
import { EmptyState, ErrorState } from "@/components/state"
import { LoadingRows } from "@/components/loading"
import { Status, StatusDot } from "@/components/status-dot"
import { ViewTabList, type TabView } from "@/components/tabs"
import { Tabs, TabsContent } from "@/components/ui/tabs"
import { TextShimmer } from "@/components/ui/text-shimmer"
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
  ownerKeepsLogs,
  ownerName,
  processHasCommand,
  processKey,
  processStateTone,
  uncontrollable,
} from "@/components/procs/shared"
import { useDebounced } from "@/hooks/use-debounced"

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

const BACK = { href: "/processes", label: "Processes" }

/**
 * One process, as a place of its own.
 *
 * It was a `SidePanel` over the process table until 0.8.6. Six blocks of
 * readings, a lineage, the counters and a priority stepper in a sheet's
 * width, over a table nobody could read behind it, is a process crammed into
 * a drawer — and everything else the kernel keeps about a process (its
 * threads, descriptors, limits, environment, the signals it handles) had
 * nowhere to go. So a process is a destination with a way back, the way a
 * container is (`docker/container-detail`), on the shared detail frame
 * (`DetailPage`), and its views are the view strip under its identity
 * line.
 *
 * What the sheet did well — the table beside the thing being read — is the
 * column on the left from `xl` (`ProcessRail`): the table's own ranking,
 * filters and search folded into a list, so checking the next worker is one
 * press, Alt+↑ and Alt+↓ walk it, and the view on screen goes with you. On a
 * phone the page is the one pane, with the way back and the same two arrows.
 * The view is remembered, as on every detail page: a reader checking each
 * worker's files opens the next one on its files.
 */
export function ProcessPage() {
  const params = useParams<{ pid: string }>()
  const router = useRouter()
  const pid = params.pid && /^\d+$/.test(params.pid) ? Number(params.pid) : null
  const [tab, setTab] = useDetailTab(TABS, { remember: "processes.process.tab" })
  const open = useCallback((next: number) => router.push(processHref(next, tab)), [router, tab])

  const rail = useRail()
  const steps = useSiblingSteps(
    rail.rows.map((row) => row.pid),
    pid,
    open,
  )
  const reading = useProcessReading(pid)
  const railRow = rail.rows.find((candidate) => candidate.pid === pid)
  const { confirm, dialog } = useConfirm()
  const { snapshot } = useMetrics()
  const memTotal = snapshot?.memory?.total ?? 0
  const { row, gone } = reading
  const live = Boolean(row) && !gone
  const inspect = useProcessInspect(row, live)
  // usePoll never lends one resource's answer to another, so a process
  // opened from the rail never draws the last one's lineage while its own loads.
  const tree = usePoll(
    (signal) => get<ProcessTree>(`/processes/${pid}/tree`, undefined, signal),
    6000,
    [pid],
    { enabled: pid !== null && live },
  )
  const { owner, pending: ownerPending, refresh: refreshOwner } = useOwner(row)
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
  const missing = pid === null || (!row && reading.error)

  const views: TabView<Tab>[] = row
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
        // From the row alone, so the view and the line's Logs command are
        // there from the first read, not once a PM2 app's read lands.
        ...(ownerKeepsLogs(row) ? [{ key: "logs" as const, label: "Logs" }] : []),
      ]
    : []
  const shown = views.some((view) => view.key === tab) ? tab : "overview"

  return (
    <DetailPage
      name="Process"
      what="process"
      stateKey="process.page"
      refresh={reading.refresh}
      steps={steps}
      back={BACK}
      title={missing ? "Process" : (row?.name ?? `Process ${pid}`)}
      rail={
        <ProcessRail
          rows={rail.rows}
          current={pid}
          currentRow={row}
          loading={rail.loading}
          error={rail.error}
          onRetry={rail.retry}
          query={rail.query}
          onQuery={rail.setQuery}
          filtered={rail.filtered}
          onClearFilters={rail.clearFilters}
          caption={rail.caption}
          onOpen={open}
          bindings={rail.bindings}
        />
      }
      identity={
        missing ? undefined : row ? (
          <ProcessIdentity
            process={row}
            gone={gone}
            busy={busy}
            waitChannel={inspect.data?.waitChannel}
            childCount={children}
            confirm={confirm}
            signal={control.signal}
            onOpen={open}
            onLogs={views.some((view) => view.key === "logs") ? () => setTab("logs") : undefined}
            logsOpen={shown === "logs"}
          />
        ) : (
          // The line's final shape while the process is read, its verbs'
          // place held, so nothing under it moves when it lands (D11).
          <IdentityHeader
            loading
            className="border-b-0 pb-0"
            title={<TextShimmer>{`Reading PID ${pid}…`}</TextShimmer>}
            state
            // The rail's row, when it is in, says whether this process will
            // have a command; the slot is held only for one that will.
            command={railRow ? processHasCommand(railRow) : true}
            menu={false}
          />
        )
      }
    >
      {missing ? (
        reading.error && !(reading.error instanceof ApiError && reading.error.status === 404) ? (
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
        )
      ) : !row ? (
        <LoadingRows />
      ) : (
        <Tabs key={pid} value={shown} onValueChange={setTab} className="gap-0">
          <ViewTabList label={`Views of ${row.name}`} views={views} value={shown} sticky />

          <TabsContent value="overview" className="pt-6">
            <ProcessOverview
              process={row}
              gone={gone}
              inspect={inspect.data}
              tree={tree.data}
              memTotal={memTotal}
              onOpen={open}
              onTab={setTab}
            />
          </TabsContent>
          <TabsContent value="threads" className="flex flex-col pt-4">
            {shown === "threads" && <ProcessThreads inspect={inspect.data} />}
          </TabsContent>
          <TabsContent value="files" className="pt-4">
            {shown === "files" && <ProcessFiles inspect={inspect.data} />}
          </TabsContent>
          <TabsContent value="network" className="pt-4">
            {shown === "network" && <ProcessNetwork row={row} inspect={inspect.data} />}
          </TabsContent>
          <TabsContent value="environment" className="flex flex-col pt-4">
            {shown === "environment" && <ProcessEnvironment inspect={inspect.data} />}
          </TabsContent>
          <TabsContent value="limits" className="pt-4">
            {shown === "limits" && <ProcessLimits row={row} inspect={inspect.data} />}
          </TabsContent>
          <TabsContent value="control" className="pt-6">
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
          <TabsContent value="logs" className="flex flex-col pt-4">
            {shown === "logs" && <ProcessLogs row={row} owner={owner} pending={ownerPending} />}
          </TabsContent>
        </Tabs>
      )}
      {dialog}
    </DetailPage>
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
    error: list.data ? undefined : list.error,
    retry: list.refresh,
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

/**
 * The line the page opens on, the one every destination in the product opens
 * on (`IdentityHeader`): the process as the product it is with its state in
 * the tile's corner, its name and PID, its state beside them, and after them
 * the facts a process is opened to check — who runs it, what supervises it
 * (with that supervisor's own mark, a link to its page), how long it has been
 * up, ticking, and its parent. A process nothing supervises leads the facts,
 * because that is the one a reader acts on. Its verbs are the line's own:
 * its logs as the command, where its owner keeps any (a zombie's parent, its
 * remedy, before them), and every signal behind `⋯`, Terminate and Kill last
 * in the destructive ink. No destructive verb takes the command: with no
 * logs and no parent to open, the line has only its menu. A process that has
 * gone has none.
 */
function ProcessIdentity({
  process: row,
  gone,
  busy,
  waitChannel,
  childCount,
  confirm,
  signal,
  onOpen,
  onLogs,
  logsOpen,
}: {
  process: ProcessRow
  gone?: string
  busy?: string
  waitChannel?: string
  childCount: number
  confirm: Parameters<typeof useProcessVerbs>[0]["confirm"]
  signal: Parameters<typeof useProcessVerbs>[0]["signal"]
  onOpen: (pid: number) => void
  /** Opens the Logs view; absent when the process's owner keeps none. */
  onLogs?: () => void
  /**
   * The Logs view is the one open: the command stays, so the line holds
   * still, but it has nothing to do there and says so by being off.
   */
  logsOpen?: boolean
}) {
  const now = useNow(1000, !gone)
  const signals = useProcessVerbs({ process: row, confirm, signal, onOpen, children: childCount })
  const parent = signals.find((verb) => verb.inline)
  const logs: Verb | undefined = onLogs && {
    key: "logs",
    label: "Open logs",
    short: "Logs",
    icon: Logs,
    inline: true,
    disabled: logsOpen,
    run: onLogs,
  }
  const verbs = [
    ...(parent ? [parent] : []),
    ...(logs ? [logs] : []),
    ...signals.filter((verb) => verb !== parent),
  ]
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
  const unmanaged = row.manager === "unmanaged"
  const supervisor = owner ? (
    <Fact>
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
    </Fact>
  ) : (
    <Fact className={cn(unmanaged && "text-warning")}>
      {managerName(row.manager)}
      {row.manager === "session" && row.managerName ? ` · ${row.managerName}` : ""}
    </Fact>
  )
  return (
    <IdentityHeader
      className="animate-rise border-b-0 pb-0"
      logo={
        <span className="relative flex shrink-0">
          <ProductLogo
            id={product ?? (kind === "kernel" ? "linux" : undefined)}
            fallback={Cpu}
            className="size-10 rounded-lg [&_img]:size-6"
          />
          <StatusDot corner="lg" state={state} live={!gone && row.state === "running"} />
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
      state={
        busy ? (
          <span className="inline-flex items-center gap-1.5 text-body font-medium">
            <StatusDot tone="notice" />
            <TextShimmer>{`${busy}…`}</TextShimmer>
          </span>
        ) : (
          <Status state={state} live={!gone && row.state === "running"} label={gone ?? row.state} />
        )
      }
      facts={
        <>
          {unmanaged && supervisor}
          <Fact mono>{row.username || "unknown user"}</Fact>
          {!unmanaged && supervisor}
          <Fact
            className={cn("numeric", !gone && "text-foreground")}
            title={timestamp(row.createTime)}
          >
            {gone || kind === "zombie" ? `started ${up} ago` : `up ${up}`}
          </Fact>
          {row.ppid > 0 && (
            <Fact>
              <button
                type="button"
                className={cn("numeric hit-area hover:text-foreground", INLINE_LINK)}
                onClick={() => onOpen(row.ppid)}
              >
                parent {row.ppid}
              </button>
            </Fact>
          )}
          <Fact className="numeric">{plural(row.threads, "thread")}</Fact>
          {!gone && !kind && waitChannel && row.state !== "running" && (
            <Fact mono>in {waitChannel}</Fact>
          )}
        </>
      }
      {...(gone
        ? { menu: false }
        : parent || logs
          ? IdentityVerbs({ verbs, name: row.name })
          : {
              menu: verbs.length > 0 && (
                <VerbMenu verbs={verbs} label={`More actions for ${row.name}`} />
              ),
            })}
    />
  )
}
