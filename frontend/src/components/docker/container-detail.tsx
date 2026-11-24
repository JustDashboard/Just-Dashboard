"use client"

import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { useParams, useRouter, useSearchParams } from "next/navigation"
import { ChevronDown, ChevronUp, Layers, Pencil } from "@/components/icons"
import { notify } from "@/lib/toast"
import { get, post, ApiError } from "@/lib/api"
import { relativeTime, timestamp } from "@/lib/format"
import { useViewState } from "@/lib/view-state"
import type { Container, ContainerDetail, PortRoute } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { dockerSource, journalSource, stackSource } from "@/lib/log/sources"
import { kindViews } from "@/components/logs/kind-views"
import { ServiceLogs, type ServiceLogSource } from "@/components/logs/service-logs"
import { ErrorState } from "@/components/state"
import { LoadingRows } from "@/components/loading"
import { ContainerUsageTab } from "@/components/docker/container-usage-tab"
import { ContainerInspectTab } from "@/components/docker/container-inspect-tab"
import { ContainerShellTab } from "@/components/docker/container-shell-tab"
import { useContainerControl, useContainerVerbs } from "@/components/docker/container-actions"
import { containerEventsView } from "@/components/docker/container-events"
import { Definition } from "@/components/docker/explain"
import type { ConfirmFn } from "@/components/docker/shared"
import { containerVerdict, restartWords, type Verdict } from "@/components/docker/container"
import { exitWords } from "@/components/docker/words"
import { ContainerIdentity } from "@/components/docker/container-identity"
import { ContainerRail, useContainerRail } from "@/components/docker/container-rail"
import { ContainerReadings, useContainerFrames } from "@/components/docker/container-readings"
import { ContainerPicture } from "@/components/docker/container-picture"
import { ContainerCompany } from "@/components/docker/container-company"
import { ContainerRecent } from "@/components/docker/container-recent"
import { ContainerConfiguration } from "@/components/docker/container-configuration"
import { ContainerStorageTab } from "@/components/docker/container-storage-tab"
import { EnvironmentTable, NetworksTable, PortsTable } from "@/components/docker/container-tables"
import { containerProduct } from "@/components/product-logo"
import { useConfirm } from "@/components/confirm-dialog"
import { Page, PageContext } from "@/components/page"
import { Detail, DetailList } from "@/components/detail-list"
import { Workspace, WorkspaceHelp } from "@/components/workspace/workspace"
import { Panel, PanelHeader, Well } from "@/components/panel"
import { ViewTabList } from "@/components/tabs"
import { IconAction } from "@/components/icon-action"
import type { Verb } from "@/components/verbs"
import { IdentityVerbs } from "@/components/identity-header"
import { usePrompt } from "@/components/prompt-dialog"
import { ShellWords } from "@/components/deploy/run-evidence"
import { cn } from "@/lib/utils"
import { Tabs, TabsContent } from "@/components/ui/tabs"

/**
 * One container, as a place of its own.
 *
 * This was a `SidePanel` over the container table until 2026-09-21. Seven
 * tabs, a recorded-usage chart, a live log socket and an interactive shell is
 * not a thing you glance at with the list showing behind it: an xterm inside a
 * sheet's `sm:max-w-3xl` is about ninety columns, and the table it half-covers
 * is not context anybody is using while they read a deploy's output. So a
 * container is its own destination with a breadcrumb back, the way a
 * deployment run is one.
 *
 * The tabs stayed *in* the page. They switch between views of one thing, which
 * is what the view strip is for; the route-level strip was retired in
 * 0.6.7 and this is not the reason to bring it back (`shell.md`). What
 * the panel took as a `focusTab` prop is `?tab=` now — the same job, and it
 * survives a reload and a shared link.
 *
 * The 2026-10-08 overhaul gave it the shape every destination in the product
 * now opens on, because the operator found it the page with no life: a strip
 * of four grey label-and-value pairs over three columns of grey fields, and
 * nothing on it that moved. It opens on the container's identity line (the
 * product it runs, its image, its project, how long it has been up, ticking,
 * and the verdict at the line's end) over the views as the view strip,
 * and the Overview reads, in order: what it is using, live, with its last
 * hour; a picture
 * of how it is reached and what it keeps; the containers it runs beside, as a
 * live table; what just happened to it and how it runs; and its ports and
 * networks as tables. Where each old figure went is in `design-system.md` §15.
 *
 * The Containers table stays beside it from `xl` (`ContainerRail`), as the
 * process table does beside a process: the table's own search, chips and
 * order, the container on screen filled, Alt+↑ and Alt+↓ to walk it with the
 * open view carried along. Until 0.8.7 the walk followed whatever list the
 * container had been opened from, which nothing on the page showed.
 */
export function ContainerPage() {
  const { id } = useParams<{ id: string }>()
  const requested = useSearchParams().get("tab")
  const router = useRouter()
  const [remembered] = useViewState("docker.container.tab", "overview")
  // The panel is keyed on the container *and* the requested tab, so a link to
  // the logs of the container already open still moves to the logs.
  const seed = `${id}:${requested ?? ""}`
  // The view the panel is on, said back by it, so the walk carries it along.
  const [view, setView] = useState<{ seed: string; tab: string }>()
  const tab = view?.seed === seed ? view.tab : (requested ?? remembered)
  const onView = useCallback((next: string) => setView({ seed, tab: next }), [seed])
  const refresh = useRef<() => void>(() => {})
  const onRefresh = useCallback((run: () => void) => {
    refresh.current = run
  }, [])

  const rail = useContainerRail()
  const here = (container: Container) => container.id === id || container.name === id
  const at = rail.rows.findIndex(here)
  const open = useCallback(
    (next: string) =>
      router.push(`/docker/containers/${encodeURIComponent(next)}?tab=${encodeURIComponent(tab)}`),
    [router, tab],
  )
  const adjacent = (direction: number) => {
    const next = at >= 0 ? rail.rows[at + direction] : rail.rows[direction > 0 ? 0 : -1]
    if (next) open(next.id)
  }
  const previous = { disabled: at <= 0, run: () => adjacent(-1) }
  const next = {
    disabled: rail.rows.length === 0 || at >= rail.rows.length - 1,
    run: () => adjacent(1),
  }

  return (
    <Workspace
      name="Container"
      stateKey={`container.${id}.${tab}`}
      refresh={() => refresh.current()}
      search={false}
      rows={false}
      commands={[
        {
          id: "previous",
          label: "Previous container",
          keys: "Alt+↑",
          chord: "Alt+ArrowUp",
          disabled: previous.disabled,
          run: previous.run,
        },
        {
          id: "next",
          label: "Next container",
          keys: "Alt+↓",
          chord: "Alt+ArrowDown",
          disabled: next.disabled,
          run: next.run,
        },
      ]}
    >
      <Page fill className="gap-5 md:gap-5">
        {/* The table stays beside the container from `xl`, as it does beside
            a process and a service: the list holds still while a view
            scrolls. Narrower, the page is the one pane. */}
        <div className="flex min-h-0 min-w-0 flex-1 gap-6">
          <ContainerRail
            className="hidden w-72 shrink-0 xl:flex"
            rows={rail.rows}
            current={id}
            currentRow={rail.all.find(here)}
            loading={rail.loading}
            stats={rail.stats}
            oomKilled={rail.oomKilled}
            query={rail.query}
            onQuery={rail.setQuery}
            filtered={rail.filtered}
            onClearFilters={rail.clearFilters}
            caption={rail.caption}
            onOpen={open}
            bindings={rail.bindings}
          />
          <ContainerDetailPanel
            key={seed}
            containerId={id}
            focusTab={requested ?? undefined}
            onView={onView}
            onRefresh={onRefresh}
            previous={previous}
            next={next}
          />
        </div>
      </Page>
    </Workspace>
  )
}

type Step = { disabled: boolean; run: () => void }

function ContainerDetailPanel({
  containerId,
  focusTab,
  onView,
  onRefresh,
  previous,
  next,
}: {
  containerId: string
  /**
   * Which tab to land on, when the thing that linked here was asking a
   * particular question — "show me the logs", "open a shell", a finding whose
   * evidence is the usage chart. Without it every one of those routes arrived
   * at Overview and cost a second click, which is the reason a log button that
   * merely selects a row never feels like a log button.
   */
  focusTab?: string
  /** Says which view is on screen, for the page that walks to the next container on it. */
  onView: (tab: string) => void
  /** Hands the page what its refresh key reads again. */
  onRefresh: (run: () => void) => void
  previous: Step
  next: Step
}) {
  const { can } = useAuth()
  const router = useRouter()
  const { confirm, dialog } = useConfirm()
  const detailPoll = usePoll(
    (signal) =>
      get<ContainerDetail>(
        `/docker/containers/${encodeURIComponent(containerId)}`,
        undefined,
        signal,
      ),
    10_000,
    [containerId],
  )
  const { data: detail, error, refresh: refreshDetail } = detailPoll
  // Which tab a container opens on. Somebody watching a deploy wants Logs
  // every time, and reopening on Overview is a click paid per container. A
  // link that asked for a specific tab overrides the remembered one — it is
  // answering a question rather than arranging furniture.
  const [remembered, remember] = useViewState("docker.container.tab", "overview")
  // Seeded once, because this component is keyed on the container and the
  // requested tab: a link to the logs gets the logs, and the moment the reader
  // moves to another tab that choice becomes the remembered one again.
  const [requestedTab, setTabState] = useState(focusTab ?? remembered)
  const running = detail?.state === "running"
  const shell = can("terminal") && running
  const tab =
    ["overview", "usage", "logs", "env", "mounts", "inspect", "configure"].includes(requestedTab) ||
    (requestedTab === "shell" && shell)
      ? requestedTab
      : "overview"
  // The logs' view a press asked for — Events, from the Overview's Recent —
  // handed to the pane once, as a window is.
  const [logView, setLogView] = useState<string>()
  const setTab = useCallback(
    (next: string, view?: string) => {
      setTabState(next)
      setLogView(view)
      remember(next)
    },
    [remember],
  )
  // Where each published port is reached from — the picture's ways in and the
  // Ports table both draw it.
  const routes = usePoll<PortRoute[]>(
    (signal) => get<PortRoute[]>(`/docker/containers/${containerId}/routes`, undefined, signal),
    10_000,
    [containerId],
  )

  // Whether this page has ever had its container, so a 404 can be told apart
  // from a bad address.
  const loaded = useRef(false)

  useEffect(() => {
    if (detail) loaded.current = true
    if (error instanceof ApiError && error.status === 404 && loaded.current) {
      router.replace("/docker/containers")
    }
  }, [detail, error, router])

  const frames = useContainerFrames(containerId, tab === "overview" && running)
  const verdict = useMemo(() => (detail ? containerVerdict(detail) : undefined), [detail])
  const traffic = useMemo(() => {
    const { current, previous } = frames
    if (!current || !previous || !current.networkAvailable || !previous.networkAvailable) return
    const seconds = (Date.parse(current.ts) - Date.parse(previous.ts)) / 1000
    if (seconds <= 0) return
    const moved = current.netRx + current.netTx - previous.netRx - previous.netTx
    return moved >= 0 ? moved / seconds : undefined
  }, [frames])

  const refreshRoutes = routes.refresh
  const changed = useCallback(() => {
    refreshDetail()
    refreshRoutes()
  }, [refreshDetail, refreshRoutes])
  useEffect(() => onRefresh(changed), [onRefresh, changed])
  useEffect(() => onView(tab), [onView, tab])

  const views = detail
    ? [
        { key: "overview", label: "Overview" },
        { key: "usage", label: "Usage" },
        { key: "logs", label: "Logs" },
        { key: "env", label: "Environment", count: detail.env.length },
        { key: "mounts", label: "Storage", count: detail.mounts.length },
        { key: "inspect", label: "Inspect" },
        { key: "configure", label: "Configuration" },
        ...(shell ? [{ key: "shell", label: "Shell" }] : []),
      ]
    : []

  return (
    <div className="flex min-h-0 min-w-0 flex-1 flex-col gap-5">
      <div className="flex min-w-0 shrink-0 flex-col gap-5">
        {/* The back line holds the way back, the walk and the help (the `?`
            cap, which a touch screen does not draw); the verbs are the
            identity line's, one command and the rest behind ⋯, as on every
            detail page (PGH-02). */}
        <PageContext
          back={{ href: "/docker/containers", label: "Containers" }}
          title={detail?.name ?? "Container"}
          actions={
            detail && (
              <span className="flex items-center max-sm:hidden">
                <IconAction
                  label="Previous container"
                  disabled={previous.disabled}
                  onClick={previous.run}
                >
                  <ChevronUp />
                </IconAction>
                <IconAction label="Next container" disabled={next.disabled} onClick={next.run}>
                  <ChevronDown />
                </IconAction>
                <WorkspaceHelp />
              </span>
            )
          }
        />
        {detail && verdict && (
          <ContainerHead
            detail={detail}
            verdict={verdict}
            confirm={confirm}
            onOpenTab={setTab}
            onChanged={changed}
          />
        )}
      </div>

      {error && <ErrorState error={error} />}
      {!detail && !error && <LoadingRows />}

      {detail && verdict && (
        <Tabs value={tab} onValueChange={(next) => setTab(next)} className="min-h-0 flex-1 gap-0">
          <ViewTabList label={`Views of ${detail.name}`} views={views} value={tab} />

          <TabsContent value="overview" className="min-h-0 flex-1 overflow-y-auto pt-6">
            {/*
                Native usage, connections, neighboring containers, events and configuration
                remain readable without a separate diagnosis.
              */}
            <div className="flex min-w-0 flex-col gap-8 pb-4">
              <ContainerReadings detail={detail} frames={frames} onUsage={() => setTab("usage")} />

              <ContainerPicture
                detail={detail}
                verdict={verdict}
                routes={routes.data}
                traffic={traffic}
                readings={
                  running && frames.current
                    ? {
                        cpu: frames.current.cpuReady ? frames.current.cpuPercent : undefined,
                        memory: frames.current.memUsage,
                      }
                    : undefined
                }
              />
              <ContainerCompany container={detail} tab="overview" />
              <div className="grid gap-8 min-[1600px]:grid-cols-2 [&>*]:min-w-0">
                <ContainerRecent detail={detail} onEvents={() => setTab("logs", "events")} />
                <HowItRuns detail={detail} />
              </div>
              <div className="grid items-start gap-6 min-[1856px]:grid-cols-2 [&>*]:min-w-0">
                <PortsTable detail={detail} routes={routes.data} />
                <NetworksTable detail={detail} />
              </div>
            </div>
          </TabsContent>

          <TabsContent value="usage" className="min-h-0 flex-1 overflow-y-auto pt-6">
            <ContainerUsageTab key={detail.id} detail={detail} onLimitsSaved={changed} />
          </TabsContent>

          {/* Scrolls on a phone, where the readings and a pane worth reading
              are taller than what is left of the window under the facts. */}
          <TabsContent value="logs" className="min-h-0 flex-1 overflow-y-auto pt-4">
            <ContainerLogs detail={detail} view={logView} />
          </TabsContent>

          <TabsContent value="env" className="min-h-0 flex-1 overflow-y-auto pt-4">
            <EnvironmentTable env={detail.env} />
          </TabsContent>

          {/* The listing takes the tab's height and scrolls inside itself, so
              the tab only scrolls once a writable-layer report outgrows it. */}
          <TabsContent value="mounts" className="min-h-0 flex-1 overflow-y-auto pt-4">
            <ContainerStorageTab detail={detail} />
          </TabsContent>

          <TabsContent value="configure" className="min-h-0 flex-1 overflow-y-auto pt-4">
            {tab === "configure" && (
              <ContainerConfiguration detail={detail} confirm={confirm} onChanged={changed} />
            )}
          </TabsContent>
          <TabsContent value="inspect" className="min-h-0 flex-1 pt-4">
            {tab === "inspect" && <ContainerInspectTab containerId={detail.id} />}
          </TabsContent>

          {shell && (
            <TabsContent value="shell" className="min-h-0 flex-1 pt-4">
              {tab === "shell" && <ContainerShellTab detail={detail} />}
            </TabsContent>
          )}
        </Tabs>
      )}
      {dialog}
    </div>
  )
}

/**
 * How the container runs, as the facts behind the readings above.
 *
 * It was three columns of label-and-value pairs headed "What it is running",
 * "How it behaves" and "What it can reach", with the networks and the ports
 * as two more groups under them. The ports and networks are tables of their
 * own now and the picture draws both; what is left is one block: the command
 * coloured as a command rather than printed as a string, and two columns of
 * facts — what it runs as, and how it comes back. The two fields that decide
 * how much of the server is on the other side of the container wall keep
 * their tone.
 */
function HowItRuns({ detail }: { detail: ContainerDetail }) {
  const running = detail.state === "running"
  // Docker keeps the entrypoint apart from the command it is handed, and the
  // process the container runs is the two in that order.
  const command = [...detail.entrypoint, detail.command].filter(Boolean).join(" ")
  return (
    <Panel plain className="animate-rise">
      <PanelHeader title="How it runs" />
      <div className="flex min-w-0 flex-col gap-4 pt-3">
        {command && (
          <Well className="max-h-28 text-xs leading-relaxed break-all whitespace-pre-wrap">
            <ShellWords command={command} />
          </Well>
        )}
        <div className="grid min-w-0 gap-x-8 gap-y-1.5 sm:grid-cols-2">
          <DetailList className="content-start">
            <Detail
              label={<Definition name="image">Image</Definition>}
              className="font-mono break-all"
            >
              {detail.image}
            </Detail>
            <Detail label="Working dir" className="font-mono break-all">
              {detail.workingDir || "/"}
            </Detail>
            <Detail
              label={<Definition name="containerUser">User</Definition>}
              className="font-mono"
            >
              {detail.user || "the image's own"}
            </Detail>
            <Detail label={<Definition name="networkMode">Network mode</Definition>}>
              {detail.networkMode === "host" ? (
                <span className="text-warning">host — the server&rsquo;s own network</span>
              ) : (
                detail.networkMode
              )}
            </Detail>
            <Detail label={<Definition name="privileged">Privileged</Definition>}>
              {detail.privileged ? (
                <span className="text-destructive">yes — full host access</span>
              ) : (
                "no"
              )}
            </Detail>
            {detail.capAdd.length > 0 && (
              <Detail label="Capabilities" className="font-mono text-warning">
                {detail.capAdd.join(", ")}
              </Detail>
            )}
          </DetailList>
          <DetailList className="content-start">
            <Detail label="Started" className="numeric">
              {detail.startedAt && Date.parse(detail.startedAt) > 0
                ? `${timestamp(detail.startedAt)} · ${relativeTime(detail.startedAt)}`
                : "never"}
            </Detail>
            <Detail label="Created" className="numeric">
              {timestamp(detail.createdAt)}
            </Detail>
            <Detail label={<Definition name="restart">Restart policy</Definition>}>
              <span title={detail.restartPolicy || "no"}>{restartWords(detail.restartPolicy)}</span>
            </Detail>
            {/* A restart count is a number until it is a symptom: anything
                above zero on a container nobody restarted by hand means it
                has been crashing and coming back. */}
            <Detail
              label="Restarts"
              className={cn("numeric", detail.restartCount > 0 && "text-warning")}
            >
              {detail.restartCount}
            </Detail>
            <Detail label={<Definition name="exitCode">Last exit</Definition>}>
              {running ? (
                <span className="text-muted-foreground">still running</span>
              ) : (
                <span className={cn(detail.exitCode !== 0 && "text-destructive")}>
                  {exitWords(detail.exitCode)}
                </span>
              )}
            </Detail>
          </DetailList>
        </div>
      </div>
    </Panel>
  )
}

/**
 * The container's output, read where the container is.
 *
 * It was a raw tail — the text, a filter box and Save — which could not
 * look further back than the socket's first five hundred lines, read every
 * line as the same grey string, and saved only what happened to be on
 * screen. It is the service logs every page embeds now, on this container:
 * Live, History and Insights over its whole log, read through the lens its
 * image names (a Postgres container's lines as Postgres events, an nginx
 * one's as requests), the lens's readings above it, and Events — what
 * Docker did to it — as a view of its own beside them, so an exit and the
 * lines that led to it are one page. A stack's container also offers the
 * stack's merged output, and every one the Docker daemon's journal, for what
 * the runtime did before the container had a log to write.
 *
 * The failure diagnosis's window is the one question worth a chip: a
 * container that is looping or has died is read at the failure, because by
 * the time anybody looks the tail is the next attempt starting up.
 */
function ContainerLogs({
  detail,
  view,
}: {
  detail: ContainerDetail
  /** A view a press elsewhere on the page asked for: Events, from the Overview's Recent. */
  view?: string
}) {
  const product = containerProduct(detail)
  const stack = detail.composeStack
  const sources = useMemo<ServiceLogSource[]>(
    () => [
      {
        id: dockerSource(detail.id),
        label: detail.name,
        kind: "docker",
        status: detail.state,
        product,
      },
      // The stack's neighbours explain a container that waits on one, and the
      // daemon's journal what a container's own output cannot: an image that
      // would not pull, a start the runtime refused.
      ...(stack
        ? [
            {
              id: stackSource(stack),
              label: stack,
              kind: "stack" as const,
              product: "docker-compose",
              detail: "Every container of this stack, merged by time",
            },
          ]
        : []),
      {
        id: journalSource("docker.service"),
        label: "docker.service",
        kind: "journal",
        product: "docker",
        detail: "The Docker daemon's own log",
      },
    ],
    [detail.id, detail.name, detail.state, product, stack],
  )
  // This container's Events know its health check; the others' are the
  // plain ones.
  const views = (source: ServiceLogSource) =>
    source.id === dockerSource(detail.id)
      ? [containerEventsView({ containerId: detail.id, healthcheck: detail.hasHealthcheck })]
      : kindViews(source)
  return (
    <div className="flex h-full min-h-0 flex-col gap-3">
      <ServiceLogs
        sources={sources}
        // Kept for the tab, per container: the Environment tab and back is
        // not a reason to lose the question on screen.
        storageKey={`docker.container.${detail.id}.logs`}
        readings="lines"
        views={views}
        view={view}
        className="min-h-0 flex-1"
        paneClassName="min-h-[30rem]"
      />
    </div>
  )
}

/**
 * Everything that can be done to the container, beside the way back: the
 * lifecycle as named buttons, because this is the page somebody opens to act
 * on what they found, and every other verb behind one menu where it gets its
 * word (`useContainerVerbs`, so the confirmation a stop raises here is the
 * sentence the table raises, and a capability that hides a verb in one place
 * hides it in both).
 *
 * It used to draw only the inline three. Pause, Remove and Copy id were in the
 * table's menu and nowhere here, so removing the container this page is
 * about — which it then handles by going back to the list — meant leaving it
 * first. Logs and the shell are views of this page, so they stay tabs.
 */
/**
 * The line the container's page opens on, with its verbs: the first of its
 * lifecycle as the one command (Restart, Start, Resume) and every other verb
 * behind ⋯ — Stop, the way to the stack that owns it, Rename, Pause, Remove.
 * Four equal buttons on the back line used to wrap above the name on a phone.
 *
 * Rename asks for the name in a dialog. On a compose container the dialog
 * says first that the next deploy puts the old name back, which an Advanced
 * button used to gate.
 */
function ContainerHead({
  detail,
  verdict,
  confirm,
  onOpenTab,
  onChanged,
}: {
  detail: ContainerDetail
  verdict: Verdict
  confirm: ConfirmFn
  onOpenTab: (tab: string) => void
  onChanged: () => void
}) {
  const { can } = useAuth()
  const router = useRouter()
  const { pending, act } = useContainerControl(onChanged)
  const { prompt, dialog } = usePrompt()
  const lifecycle = useContainerVerbs({
    container: detail,
    confirm,
    act,
    onOpenTab,
    onChanged,
  })
  const busy = Boolean(pending[detail.id])
  const stack = detail.composeStack
  // The logs and the shell are views of this page, in its strip.
  const verbs: Verb[] = lifecycle
    .filter((verb) => verb.key !== "logs" && verb.key !== "shell")
    .map((verb) => ({ ...verb, disabled: verb.progressive ? busy : undefined }))
  if (stack) {
    // After the lifecycle, before the rest: the stack is where its settings live.
    const at = verbs.findIndex((verb) => !verb.inline)
    verbs.splice(at < 0 ? verbs.length : at, 0, {
      key: "stack",
      label: "Open stack",
      icon: Layers,
      href: `/docker/stacks/${encodeURIComponent(stack)}`,
      internal: true,
      run: () => router.push(`/docker/stacks/${encodeURIComponent(stack)}`),
    })
  }
  if (can("service.control")) {
    const at = verbs.findIndex((v) => v.key === "copy-id")
    verbs.splice(at < 0 ? verbs.length : at, 0, {
      key: "rename",
      label: "Rename…",
      icon: Pencil,
      run: () =>
        prompt({
          title: "Rename container",
          label: "Name",
          initial: detail.name,
          verb: "Rename",
          mono: true,
          hint: stack ? (
            <span className="text-warning">
              Compose manages this container as part of <b>{stack}</b>, and the next deploy puts the
              old name back. Edit the compose file to keep a new one.
            </span>
          ) : undefined,
          validate: (name) =>
            !name.trim()
              ? "A container needs a name."
              : name === detail.name
                ? "That is its name now."
                : undefined,
          onSubmit: async (name) => {
            await post(`/docker/containers/${detail.id}/rename`, { name })
            notify.success(`Renamed to ${name}`)
            onChanged()
          },
          refusal: "Could not rename it",
        }),
    })
  }

  return (
    <>
      <ContainerIdentity
        detail={detail}
        verdict={verdict}
        {...IdentityVerbs({ verbs, name: detail.name })}
      />
      {dialog}
    </>
  )
}
