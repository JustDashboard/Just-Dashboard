"use client"

import { useCallback, useMemo, useState } from "react"
import Link from "next/link"
import { useParams, useRouter, useSearchParams } from "next/navigation"
import { Tabs as TabsPrimitive } from "radix-ui"
import { ArrowLeft, ChartActivity, ChevronDown, ChevronUp } from "@/components/icons"
import { get } from "@/lib/api"
import { duration, plural } from "@/lib/format"
import type { PM2Daemon, PM2Inventory, PM2Process, ProcessRow } from "@/lib/types"
import { cn } from "@/lib/utils"
import { useSessionState, useViewState } from "@/lib/view-state"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { useConfirm } from "@/components/confirm-dialog"
import { useNow } from "@/components/deploy/vocabulary"
import { IconAction } from "@/components/icon-action"
import { FactDot, HostIdentity } from "@/components/metrics/host-identity"
import { Page, PageContext } from "@/components/page"
import { ProductLogo, ProductLogos, hasProductLogo, pm2Product } from "@/components/product-logo"
import { EmptyState, LoadingRows } from "@/components/state"
import { Status, StatusDot } from "@/components/status-dot"
import { ChipCount, tabClasses } from "@/components/tabs"
import { VerbMenu } from "@/components/verbs"
import { Button } from "@/components/ui/button"
import { Tabs, TabsContent } from "@/components/ui/tabs"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { Workspace, WorkspaceHelp } from "@/components/workspace/workspace"
import { useInspectionOrder } from "@/components/procs/inspection-order"
import { useDaemonVerbs, usePM2Control } from "@/components/procs/pm2-actions"
import { usePM2AppControl, usePM2AppVerbs } from "@/components/procs/pm2-app-actions"
import { PM2AppOverview } from "@/components/procs/pm2-app-overview"
import { PM2AppRail } from "@/components/procs/pm2-app-rail"
import {
  PM2AppControl,
  PM2AppEnvironment,
  PM2AppInstances,
  PM2AppLogs,
} from "@/components/procs/pm2-app-views"
import {
  PM2_FILTER_KEYS,
  pm2AppHref,
  pm2AppState,
  pm2Lead,
  pm2Matches,
  pm2ModeWords,
  pm2Reading,
  pm2Resolve,
  type PM2StateFilter,
} from "@/components/procs/pm2-facts"
import { PM2FactsLine, PM2Missing, PM2Unanswered } from "@/components/procs/pm2-missing"
import { PM2ScaleDialog } from "@/components/procs/pm2-scale-dialog"
import {
  isCluster,
  pm2Apps,
  pm2Order,
  savedDrift,
  type PM2App,
} from "@/components/procs/pm2-shared"

const TABS = ["overview", "instances", "logs", "environment", "control"] as const
type Tab = (typeof TABS)[number]

/** PM2 reads its list every five seconds, and so does this page. */
const POLL = 5000

/** Workers whose PIDs the process table is asked about; a larger cluster's rest keep PM2's figures. */
const WORKERS_READ = 16

/** A start this recent is the restart the counter just counted. */
const RECENT_START = 5 * 60

const BACK = (
  <Link
    href="/processes/pm2"
    className="inline-flex items-center gap-1 rounded-sm focus-ring hover:underline"
  >
    <ArrowLeft className="size-3" /> PM2
  </Link>
)

/**
 * One PM2 application, as a place of its own.
 *
 * It was a `SidePanel` over the PM2 list until 0.8.7, with two tabs: four
 * tiles, a cluster's workers, the command and PM2's settings in a drawer's
 * width, and the log in the drawer's second tab — and the newest page in the
 * product, a process's, linked straight into it. So an application is a
 * destination with a way back, the way a process and a service are
 * (`process-page`, `service-page`): the line it opens on, its views as the
 * underlined strip under that line with the view in the address, and the
 * list beside it.
 *
 * The page is the application's, not one worker's. PM2 lists a cluster once
 * per worker and the sheet opened one of them under the application's name;
 * here a cluster is one page whose readings are its workers summed and whose
 * verbs reach all of them, with each worker a row under Instances that opens
 * its process's own page.
 *
 * The list stays beside it from `xl` (`PM2AppRail`): its search, its state
 * chip and its order, so the next application to check is one press, Alt+↑
 * and Alt+↓ walk it, and the view on screen goes with you. On a phone the
 * page is the one pane, with the way back and the same two arrows.
 */
export function PM2AppPage() {
  const params = useParams<{ app: string }>()
  const search = useSearchParams()
  const router = useRouter()
  const name = decodeName(params.app)
  const account = search.get("account") || undefined
  const worker = search.get("worker")
  const requested = search.get("tab")
  const [remembered, remember] = useViewState<string>("processes.pm2.app.tab", "overview")
  const [chosen, setChosen] = useState<string>(requested ?? remembered)
  const tab: Tab = (TABS as readonly string[]).includes(chosen) ? (chosen as Tab) : "overview"
  const setTab = useCallback(
    (next: string) => {
      setChosen(next)
      remember(next)
      // The address says which view, so a link to an application's log opens
      // on it; replaced rather than pushed, because the view is not a place
      // Back should step through.
      const url = new URL(window.location.href)
      if (next === "overview") url.searchParams.delete("tab")
      else url.searchParams.set("tab", next)
      if (next !== "logs") url.searchParams.delete("worker")
      window.history.replaceState(null, "", `${url.pathname}${url.search}`)
    },
    [remember],
  )

  const inventory = usePoll((signal) => get<PM2Inventory>("/pm2/", undefined, signal), POLL)
  const processes = useMemo(() => pm2Order(inventory.data?.processes ?? []), [inventory.data])
  const daemons = useMemo(() => inventory.data?.daemons ?? [], [inventory.data])
  const resolved = useMemo(() => pm2Resolve(processes, name, account), [processes, name, account])
  const open = useCallback(
    (app: PM2App) => {
      // The account is in the address only where the name alone would not say which.
      const shared = processes.some((p) => p.name === app.name && p.daemonId !== app.daemonId)
      router.push(pm2AppHref(app, { tab, account: shared }))
    },
    [router, tab, processes],
  )

  const rail = useRail(processes)
  const current = resolved.kind === "found" ? resolved.app.key : null
  const at = rail.apps.findIndex((app) => app.key === current)
  const adjacent = (direction: number) => {
    const next = at >= 0 ? rail.apps[at + direction] : rail.apps[direction > 0 ? 0 : -1]
    if (next) open(next)
  }
  const previous = { disabled: at <= 0, run: () => adjacent(-1) }
  const next = {
    disabled: rail.apps.length === 0 || at >= rail.apps.length - 1,
    run: () => adjacent(1),
  }
  const installed = inventory.data?.available !== false

  return (
    <Workspace
      name="Application"
      stateKey="pm2.app.page"
      refresh={inventory.refresh}
      commands={[
        {
          id: "previous",
          label: "Previous application",
          keys: "Alt+↑",
          chord: "Alt+ArrowUp",
          disabled: previous.disabled,
          run: previous.run,
        },
        {
          id: "next",
          label: "Next application",
          keys: "Alt+↓",
          chord: "Alt+ArrowDown",
          disabled: next.disabled,
          run: next.run,
        },
      ]}
    >
      {/* Held to the window where the rail stands beside it, so the list and
          the identity line stay put while a view scrolls; narrower, the page
          is the one pane and scrolls as a whole. */}
      <Page fill="xl" className="gap-0 md:gap-0">
        <div className="flex min-w-0 flex-1 gap-6 xl:min-h-0">
          {/* No list to stand beside a server that has no PM2. */}
          {installed && (
            <PM2AppRail
              className="hidden w-72 shrink-0 xl:flex"
              apps={rail.apps}
              current={current}
              several={daemons.length > 1}
              loading={inventory.loading}
              query={rail.query}
              onQuery={rail.setQuery}
              filtered={rail.filtered}
              onClearFilters={rail.clearFilters}
              caption={inventory.data ? rail.caption : "Reading PM2…"}
              onOpen={open}
              bindings={rail.bindings}
            />
          )}
          {resolved.kind === "found" ? (
            <AppMain
              key={resolved.app.key}
              app={resolved.app}
              processes={processes}
              daemons={daemons}
              tab={tab}
              setTab={setTab}
              worker={worker && /^\d+$/.test(worker) ? Number(worker) : null}
              onChanged={inventory.refresh}
              previous={previous}
              next={next}
            />
          ) : (
            <AppAbsent
              name={name}
              inventory={inventory}
              resolved={resolved}
              tab={tab}
              previous={previous}
              next={next}
            />
          )}
        </div>
      </Page>
    </Workspace>
  )
}

/** The route's segment as an application's name. */
function decodeName(segment: string | undefined): string {
  if (!segment) return ""
  try {
    return decodeURIComponent(segment)
  } catch {
    return segment
  }
}

/**
 * The list, read for the rail: the search, state chip and band choice the
 * list was left on (they are its session's), a cluster folded into one row,
 * in the list's order — what is crashing first.
 */
function useRail(processes: PM2Process[]) {
  const [query, setQuery] = useSessionState(PM2_FILTER_KEYS.query, "")
  const [state, setState] = useSessionState<PM2StateFilter>(PM2_FILTER_KEYS.state, "")
  const [app, setApp] = useSessionState(PM2_FILTER_KEYS.app, "")
  const matching = useMemo(
    () => pm2Apps(processes.filter((p) => pm2Matches(p, { query, state, app }))),
    [processes, query, state, app],
  )
  const inspection = useInspectionOrder(
    matching,
    (row) => row.key,
    JSON.stringify([query, state, app]),
  )
  const all = useMemo(() => pm2Apps(processes).length, [processes])
  const filtered = Boolean(state || app)
  const caption = `${
    filtered || query ? `${matching.length} of ${all}` : plural(all, "application")
  }${state ? ` · ${state}` : " · crashing first"}`
  return {
    apps: inspection.rows,
    bindings: inspection.bindings,
    query,
    setQuery,
    filtered,
    clearFilters: () => {
      setState("")
      setApp("")
    },
    caption,
  }
}

type Step = { disabled: boolean; run: () => void }

/** The two arrows that walk the list from the page, and the shortcuts beside them. */
function Steps({ previous, next }: { previous: Step; next: Step }) {
  return (
    <span className="flex items-center">
      <IconAction label="Previous application" disabled={previous.disabled} onClick={previous.run}>
        <ChevronUp />
      </IconAction>
      <IconAction label="Next application" disabled={next.disabled} onClick={next.run}>
        <ChevronDown />
      </IconAction>
      <span className="max-sm:hidden">
        <WorkspaceHelp compact />
      </span>
    </span>
  )
}

/**
 * What the page says when the address names no application it can open: the
 * list still being read, PM2 not answering, PM2 not installed, a name two
 * accounts share, or a name nothing runs under any more. Each is the page's
 * own shape with one sentence and the next step, never a list with nothing
 * open and no word about why.
 */
function AppAbsent({
  name,
  inventory,
  resolved,
  tab,
  previous,
  next,
}: {
  name: string
  inventory: ReturnType<typeof usePoll<PM2Inventory>>
  resolved: Exclude<ReturnType<typeof pm2Resolve>, { kind: "found" }>
  tab: Tab
  previous: Step
  next: Step
}) {
  // The same title line as the page proper, arrows and all: it is where the
  // line under it stands, and the way off a name that opens nothing.
  const context = (
    <PageContext
      eyebrow={BACK}
      title={name || "Application"}
      actions={<Steps previous={previous} next={next} />}
    />
  )
  if (!inventory.data) {
    if (inventory.error) {
      return (
        <div className="flex min-w-0 flex-1 flex-col gap-5">
          {context}
          <PM2Unanswered error={inventory.error} onRetry={inventory.refresh} />
        </div>
      )
    }
    return (
      <div className="flex min-w-0 flex-1 flex-col gap-5">
        {context}
        <HostIdentity
          slot="pm2-app-identity"
          mark="pm2"
          fallback={ChartActivity}
          title={<TextShimmer>{`Reading ${name}…`}</TextShimmer>}
          state={<span className="text-xs">&nbsp;</span>}
          facts={<PM2FactsLine />}
          className="border-b-0 pb-0"
        />
        <LoadingRows />
      </div>
    )
  }
  if (!inventory.data.available) {
    return (
      <div className="flex min-w-0 flex-1 flex-col gap-5">
        {context}
        <PM2Missing />
      </div>
    )
  }
  const mark = hasProductLogo("pm2") ? <ProductLogos ids={["pm2"]} /> : undefined
  return (
    <div className="flex min-w-0 flex-1 flex-col gap-5">
      {context}
      {resolved.kind === "ambiguous" ? (
        <EmptyState
          icon={ChartActivity}
          mark={mark}
          title={`${resolved.apps.length === 2 ? "Two" : resolved.apps.length} accounts run an application named ${name}`}
          description="Each account has its own PM2. Pick whose to open."
          action={
            <span className="flex flex-wrap justify-center gap-2">
              {resolved.apps.map((app) => (
                <Button key={app.key} size="sm" variant="outline" asChild>
                  <Link href={pm2AppHref(app, { tab, account: true })}>
                    {name} <span className="font-mono text-muted-foreground">{app.daemonId}</span>
                  </Link>
                </Button>
              ))}
            </span>
          }
        />
      ) : (
        <EmptyState
          icon={ChartActivity}
          mark={mark}
          title={`No application named ${name || "that"} under PM2`}
          description="It may have been deleted from PM2, or the name in the link is misspelt."
          action={
            <Button size="sm" variant="outline" asChild>
              <Link href="/processes/pm2">Back to PM2</Link>
            </Button>
          }
        />
      )}
    </div>
  )
}

function AppMain({
  app,
  processes,
  daemons,
  tab,
  setTab,
  worker,
  onChanged,
  previous,
  next,
}: {
  app: PM2App
  processes: PM2Process[]
  daemons: PM2Daemon[]
  tab: Tab
  setTab: (tab: string) => void
  /** The worker a link asked the log for. */
  worker: number | null
  onChanged: () => void
  previous: Step
  next: Step
}) {
  const { can } = useAuth()
  const router = useRouter()
  const { confirm, dialog } = useConfirm()
  const [scaling, setScaling] = useState(false)
  const [logWorker, setLogWorker] = useState(worker)
  const lead = pm2Lead(app)
  const cluster = isCluster(lead)
  const daemon = daemons.find((d) => d.account === app.daemonId)
  const drift = daemon ? savedDrift(daemon, processes) : null
  const unsaved = Boolean(drift?.unsaved.includes(app.name))

  // The process table's reading of each running worker, every two seconds
  // for one process and every four for a cluster: its history is the
  // sampler's, so a PID the live table has been watching opens on those
  // minutes. A worker that restarted between two reads answers 404 for the
  // PID it had; the next read of PM2's list has the new one.
  const pids = useMemo(
    () =>
      app.instances
        .filter((p) => p.status === "online" && p.pid > 0)
        .map((p) => p.pid)
        .sort((a, b) => a - b)
        .slice(0, WORKERS_READ),
    [app.instances],
  )
  const table = usePoll(
    (signal) =>
      Promise.all(
        pids.map((pid) =>
          get<ProcessRow>(`/processes/${pid}`, undefined, signal).catch(() => null),
        ),
      ),
    pids.length > 1 ? 4000 : 2000,
    [pids.join(",")],
    { enabled: pids.length > 0 },
  )
  const rows = useMemo(
    () => (table.data ?? []).filter((row) => row === null || pids.includes(row.pid)),
    [table.data, pids],
  )
  const reading = useMemo(() => pm2Reading(app, rows), [app, rows])
  const leadRow = rows.find((row) => row?.pid === lead.pid && lead.pid > 0) ?? undefined

  const refreshTable = table.refresh
  const changed = useCallback(() => {
    onChanged()
    refreshTable()
  }, [onChanged, refreshTable])
  const control = usePM2AppControl(changed)
  const workers = usePM2Control(changed)
  const openScale = useCallback(() => setScaling(true), [])
  const deleted = useCallback(() => router.push("/processes/pm2"), [router])
  const verbs = usePM2AppVerbs({
    app,
    confirm,
    act: control.act,
    busy: Boolean(control.pending),
    onScale: openScale,
    onDeleted: deleted,
  })
  const command = verbs.find((verb) => verb.key === "start" || verb.key === "reload")
  const menu = verbs.filter((verb) => verb !== command)
  const daemonVerbs = useDaemonVerbs({ daemons: daemon ? [daemon] : [], confirm, onChanged })
  const save = daemonVerbs.find((verb) => verb.key === "save")?.run

  // Uptime is a fact that changes while the page is open, so it ticks: from
  // the process's own start time once the table has read it, and until then
  // from PM2's uptime, held across its five-second reads.
  const online = lead.status === "online" && lead.pid > 0
  const now = useNow(1000, online)
  const held = useHeldStart(lead, now)
  const started = leadRow ? Date.parse(leadRow.createTime) : held
  const uptime = online && started !== undefined ? Math.max(0, (now - started) / 1000) : 0

  const controllable = can("service.control") || can("destructive")
  const views: { key: Tab; label: string; count?: number }[] = [
    { key: "overview", label: "Overview" },
    // A fork is one process, and the picture already draws it.
    ...(cluster
      ? [{ key: "instances" as const, label: "Instances", count: app.instances.length }]
      : []),
    { key: "logs", label: "Logs" },
    { key: "environment", label: "Environment" },
    // Nothing on it is this account's to press.
    ...(controllable ? [{ key: "control" as const, label: "Control" }] : []),
  ]
  const shown = views.some((view) => view.key === tab) ? tab : "overview"
  const boot = !daemon
    ? { word: "PM2's daemon was not read", ok: false }
    : !daemon.startupUnit
      ? { word: `No boot hook for ${daemon.account}`, ok: false }
      : !daemon.dumpSavedAt
        ? { word: "Never saved: a reboot brings nothing back", ok: false }
        : unsaved
          ? { word: `${app.name} is not in the saved list`, ok: false }
          : { word: `${app.name} comes back after a reboot`, ok: true }

  return (
    <div className="flex min-h-0 min-w-0 flex-1 flex-col gap-5">
      <div className="flex min-w-0 shrink-0 flex-col gap-5">
        <PageContext
          eyebrow={BACK}
          title={app.name}
          actions={<Steps previous={previous} next={next} />}
        />
        <AppIdentity
          app={app}
          lead={lead}
          several={daemons.length > 1}
          unsaved={unsaved}
          uptime={uptime}
          busy={control.pending}
          command={
            command && (
              <Button size="sm" disabled={command.disabled} onClick={command.run}>
                <command.icon className="size-3.5" />
                {command.label}
              </Button>
            )
          }
          menu={menu.length > 0 && <VerbMenu verbs={menu} label={`More for ${app.name}`} />}
        />
      </div>

      <Tabs value={shown} onValueChange={setTab} className="min-h-0 flex-1 gap-0">
        <TabsPrimitive.List
          aria-label={`Views of ${app.name}`}
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
          <PM2AppOverview
            app={app}
            lead={lead}
            rows={rows}
            reading={reading}
            daemon={daemon}
            unsaved={unsaved}
            uptime={uptime}
            onTab={setTab}
            onSave={save}
          />
        </TabsContent>
        <TabsContent value="instances" className="min-h-0 flex-1 pt-4 xl:overflow-y-auto">
          {shown === "instances" && (
            <PM2AppInstances
              app={app}
              rows={rows}
              now={now}
              pending={workers.pending}
              confirm={confirm}
              act={workers.act}
              onLogs={(one) => {
                setLogWorker(one.id)
                setTab("logs")
              }}
              onScale={openScale}
              onChanged={changed}
            />
          )}
        </TabsContent>
        <TabsContent value="logs" className="flex min-h-0 flex-1 flex-col pt-4 xl:overflow-y-auto">
          {/* Keyed on the worker asked for, so "Logs" pressed on another worker's
              row starts on that worker's stream with a clean buffer. */}
          {shown === "logs" && (
            <PM2AppLogs key={logWorker ?? "lead"} app={app} worker={logWorker} />
          )}
        </TabsContent>
        <TabsContent
          value="environment"
          className="flex min-h-0 flex-1 flex-col pt-4 xl:overflow-y-auto"
        >
          {shown === "environment" && <PM2AppEnvironment app={app} row={leadRow} />}
        </TabsContent>
        <TabsContent value="control" className="min-h-0 flex-1 pt-6 xl:overflow-y-auto">
          {shown === "control" && (
            <PM2AppControl
              app={app}
              verbs={verbs}
              pending={control.pending}
              boot={boot}
              onSave={save}
            />
          )}
        </TabsContent>
      </Tabs>

      {scaling && (
        <PM2ScaleDialog process={lead} onClose={() => setScaling(false)} onChanged={changed} />
      )}
      {dialog}
    </div>
  )
}

/**
 * When PM2 last started the application, held across reads. Its uptime comes
 * from a list cached for a few seconds, so a start recomputed from each read
 * moves back and forth by that much; a move larger than that is a restart.
 */
function useHeldStart(process: PM2Process, now: number): number | undefined {
  const fresh = process.uptimeMs > 0 ? now - process.uptimeMs : undefined
  const [held, setHeld] = useState<{ uptimeMs: number; start?: number }>({
    uptimeMs: process.uptimeMs,
    start: fresh,
  })
  if (held.uptimeMs !== process.uptimeMs) {
    const keep =
      fresh !== undefined && held.start !== undefined && Math.abs(held.start - fresh) < 10_000
    setHeld({ uptimeMs: process.uptimeMs, start: keep ? held.start : fresh })
  }
  return held.start
}

/**
 * The line the page opens on, laid out as a project's header is
 * (`HostIdentity` with a state and a command): the application as what runs
 * it — Node, Bun, Python — its name with its state beside it, then the facts
 * an application is opened to check: its script, how it runs, how often PM2
 * has restarted it and how long it has been up (ticking), and whose daemon
 * runs it where more than one account has one. At the far end the one command
 * that changes whether it is serving, and the rest behind the menu.
 */
function AppIdentity({
  app,
  lead,
  several,
  unsaved,
  uptime,
  busy,
  command,
  menu,
}: {
  app: PM2App
  lead: PM2Process
  several: boolean
  unsaved: boolean
  uptime: number
  busy?: string
  command: React.ReactNode
  menu: React.ReactNode
}) {
  const state = pm2AppState(app)
  const restarts = app.instances.reduce((n, p) => n + p.restarts, 0)
  const unstable = app.instances.reduce((n, p) => n + p.unstableRestarts, 0)
  return (
    <HostIdentity
      label={`About ${app.name}`}
      slot="pm2-app-identity"
      className="animate-rise border-b-0 pb-0"
      logo={
        <ProductLogo
          id={pm2Product(app.interpreter)}
          fallback={ChartActivity}
          className="size-10 rounded-lg [&_img]:size-6"
        />
      }
      title={<span className="truncate">{app.name}</span>}
      state={
        busy ? (
          <span className="inline-flex items-center gap-1.5 text-xs font-medium">
            <StatusDot tone="notice" />
            <TextShimmer>{`${busy}…`}</TextShimmer>
          </span>
        ) : state.partial ? (
          <Status tone="warning" label={state.word} />
        ) : (
          <Status state={state.status} live={state.status === "online"} label={state.word} />
        )
      }
      command={command}
      menu={menu}
      facts={
        <PM2FactsLine>
          <span className="max-w-full min-w-0 truncate font-mono" title={lead.scriptPath}>
            {lead.scriptPath}
          </span>
          <FactDot />
          <span className="text-foreground">{pm2ModeWords(app)}</span>
          <FactDot />
          <span className="numeric">
            {plural(restarts, "restart")}
            {unstable > 0 && <span className="text-destructive"> ({unstable} unstable)</span>}
          </span>
          {uptime > 0 && (
            <>
              <FactDot />
              <span
                className={cn(
                  "numeric",
                  uptime < RECENT_START && restarts > 0 ? "text-warning" : "text-foreground",
                )}
              >
                up {duration(uptime)}
              </span>
            </>
          )}
          {/* Whose daemon, only where there is more than one to tell apart. */}
          {several && (
            <>
              <FactDot />
              <span className="font-mono">{app.daemonId}</span>
            </>
          )}
          {lead.watching && (
            <>
              <FactDot />
              <span className="text-warning">restarts on file change</span>
            </>
          )}
          {unsaved && (
            <>
              <FactDot />
              <span
                className="text-warning"
                title="Started since the startup list was saved: a reboot would not bring it back"
              >
                not in the saved list
              </span>
            </>
          )}
        </PM2FactsLine>
      }
    />
  )
}
