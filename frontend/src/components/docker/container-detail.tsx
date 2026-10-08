"use client"

import { Fragment, useCallback, useEffect, useMemo, useRef, useState } from "react"
import Link from "next/link"
import { useParams, useRouter, useSearchParams } from "next/navigation"
import { Tabs as TabsPrimitive } from "radix-ui"
import {
  ArrowLeft,
  ChevronDown,
  ChevronUp,
  ClockRewind,
  Layers,
  Pencil,
  Warning,
} from "@/components/icons"
import { notify } from "@/lib/toast"
import { get, post, ApiError } from "@/lib/api"
import { relativeTime, timestamp } from "@/lib/format"
import { useSessionState, useViewState } from "@/lib/view-state"
import type { ContainerDetail, DockerDiagnosis, FailureDiagnosis, PortRoute } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { useMediaQuery } from "@/hooks/use-mobile"
import { dockerSource } from "@/lib/log-sources"
import { ServiceLogs, type ServiceLogSource } from "@/components/logs/service-logs"
import { ErrorState, LoadingRows, Notice } from "@/components/state"
import { ContainerUsageTab } from "@/components/docker/container-usage-tab"
import { ContainerInspectTab } from "@/components/docker/container-inspect-tab"
import { ContainerShellTab } from "@/components/docker/container-shell-tab"
import { useContainerControl, useContainerVerbs } from "@/components/docker/container-actions"
import { useDockerFindingActions } from "@/components/docker/finding-actions"
import { ConfigurationRemedy } from "@/components/docker/configuration-remedy"
import { ContainerFindings } from "@/components/docker/attention"
import { containerEventsView } from "@/components/docker/container-events"
import { Hint, Term } from "@/components/docker/explain"
import type { ConfirmFn } from "@/components/docker/shared"
import { containerVerdict, exitWords, restartWords } from "@/components/docker/container"
import { ContainerIdentity } from "@/components/docker/container-identity"
import { ContainerReadings, useContainerFrames } from "@/components/docker/container-readings"
import { ContainerPicture } from "@/components/docker/container-picture"
import { ContainerCompany } from "@/components/docker/container-company"
import { ContainerRecent } from "@/components/docker/container-recent"
import { ContainerStorageTab } from "@/components/docker/container-storage-tab"
import { EnvironmentTable, NetworksTable, PortsTable } from "@/components/docker/container-tables"
import { containerProduct } from "@/components/product-logo"
import { useConfirm } from "@/components/confirm-dialog"
import { Detail, DetailList, Page, PageContext } from "@/components/page"
import { Workspace, WorkspaceHelp } from "@/components/workspace/workspace"
import { Panel, PanelHeader, Well } from "@/components/panel"
import { ChipCount, FilterChip, tabClasses } from "@/components/tabs"
import { IconAction } from "@/components/icon-action"
import { VerbMenu } from "@/components/verbs"
import { ShellWords } from "@/components/deploy/run-evidence"
import { cn } from "@/lib/utils"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
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
 * is what the underlined tab is for; the route-level strip was retired in
 * 0.6.7 and this is not the reason to bring it back (`shell-design.md`). What
 * the panel took as a `focusTab` prop is `?tab=` now — the same job, and it
 * survives a reload and a shared link.
 *
 * The 2026-10-08 overhaul gave it the shape every destination in the product
 * now opens on, because the operator found it the page with no life: a strip
 * of four grey label-and-value pairs over three columns of grey fields, and
 * nothing on it that moved. It opens on the container's identity line (the
 * product it runs, its image, its project, how long it has been up, ticking,
 * and the verdict at the line's end) over the views as the underlined strip,
 * and the Overview reads, in order: why it is not working where it is not;
 * what it is using, live, with its last hour; what is wrong with it; a picture
 * of how it is reached and what it keeps; the containers it runs beside, as a
 * live table; what just happened to it and how it runs; and its ports and
 * networks as tables. Where each old figure went is in `design-system.md` §15.
 */
export function ContainerPage() {
  const { id } = useParams<{ id: string }>()
  const tab = useSearchParams().get("tab")
  return (
    // Keyed on the container *and* the requested tab, so a link to the logs of
    // the container already open still moves to the logs.
    <ContainerDetailPanel key={`${id}:${tab ?? ""}`} containerId={id} focusTab={tab ?? undefined} />
  )
}

const BACK = (
  <Link
    href="/docker/containers"
    className="inline-flex items-center gap-1 rounded-sm focus-ring hover:underline"
  >
    <ArrowLeft className="size-3" /> Containers
  </Link>
)

function ContainerDetailPanel({
  containerId,
  focusTab,
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
}) {
  const { can } = useAuth()
  const router = useRouter()
  const { confirm, dialog } = useConfirm()
  const [detail, setDetail] = useState<ContainerDetail>()
  const [error, setError] = useState<Error>()
  // Which tab a container opens on. Somebody watching a deploy wants Logs
  // every time, and reopening on Overview is a click paid per container. A
  // link that asked for a specific tab overrides the remembered one — it is
  // answering a question rather than arranging furniture.
  const [remembered, remember] = useViewState("docker.container.tab", "overview")
  // Seeded once, because this component is keyed on the container and the
  // requested tab: a link to the logs gets the logs, and the moment the reader
  // moves to another tab that choice becomes the remembered one again.
  const [tab, setTabState] = useState(focusTab ?? remembered)
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
  const [reloads, setReloads] = useState(0)
  const [navigation] = useSessionState<{ id: string; name: string }[]>(
    "docker.containers.navigation",
    [],
  )
  const at = navigation.findIndex((container) => container.id === containerId)
  const adjacent = (direction: number) => {
    const next = at >= 0 ? navigation[at + direction] : undefined
    if (next)
      router.push(
        `/docker/containers/${encodeURIComponent(next.id)}?tab=${encodeURIComponent(tab)}`,
      )
  }

  // The page's own diagnosis pass. As a panel this was handed the container
  // table's single poll, filtered to the open container; on its own route
  // there is nobody above to ask.
  const health = usePoll<DockerDiagnosis>(
    (signal) => get<DockerDiagnosis>("/docker/health", undefined, signal),
    60_000,
  )
  const runFix = useDockerFindingActions({
    confirm,
    onChanged: () => {
      health.refresh()
      setReloads((n) => n + 1)
    },
    open: (id, tab) =>
      id === containerId
        ? setTab(tab ?? "overview")
        : router.push(
            `/docker/containers/${encodeURIComponent(id)}?tab=${encodeURIComponent(tab ?? "overview")}`,
          ),
  })
  // Why it stopped, read once for the page rather than by the tab that says
  // so: the Logs tab opens on the same diagnosis's window, and the verdict at
  // the end of the identity line is what tells a loop from a restart.
  const failure = usePoll<FailureDiagnosis>(
    (signal) =>
      get<FailureDiagnosis>(`/docker/containers/${containerId}/failure`, undefined, signal),
    0,
    [containerId, reloads],
  )
  // Whether the Logs tab is handed that window rather than the tail.
  const [crash, setCrash] = useState(false)
  // Where each published port is reached from — the picture's ways in and the
  // Ports table both draw it.
  const routes = usePoll<PortRoute[]>(
    (signal) => get<PortRoute[]>(`/docker/containers/${containerId}/routes`, undefined, signal),
    0,
    [containerId, reloads],
  )

  // Whether this page has ever had its container, so a 404 can be told apart
  // from a bad address.
  const loaded = useRef(false)

  useEffect(() => {
    const controller = new AbortController()
    get<ContainerDetail>(
      `/docker/containers/${encodeURIComponent(containerId)}`,
      undefined,
      controller.signal,
    )
      .then((next) => {
        loaded.current = true
        setDetail(next)
      })
      .catch((err) => {
        if (controller.signal.aborted) return
        // Removing a container is done from this page, and the thing the page
        // is about then stops existing. An error state about a container the
        // reader just deleted on purpose is not news — the list is.
        if (err instanceof ApiError && err.status === 404 && loaded.current) {
          router.replace("/docker/containers")
          return
        }
        setError(err)
      })
    return () => controller.abort()
  }, [containerId, reloads, router])

  const running = detail?.state === "running"
  const frames = useContainerFrames(containerId, tab === "overview" && running)
  const verdict = useMemo(
    () => (detail ? containerVerdict(detail, failure.data) : undefined),
    [detail, failure.data],
  )
  const traffic = useMemo(() => {
    const { current, previous } = frames
    if (!current || !previous || !current.networkAvailable || !previous.networkAvailable) return
    const seconds = (Date.parse(current.ts) - Date.parse(previous.ts)) / 1000
    if (seconds <= 0) return
    const moved = current.netRx + current.netTx - previous.netRx - previous.netTx
    return moved >= 0 ? moved / seconds : undefined
  }, [frames])

  const shell = can("terminal") && running
  const changed = useCallback(() => {
    setReloads((n) => n + 1)
    health.refresh()
  }, [health])

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
    <Workspace
      name="Container"
      stateKey={`container.${containerId}.${tab}`}
      refresh={changed}
      search={false}
      rows={false}
      commands={[
        {
          id: "previous",
          label: "Previous container",
          keys: "Alt+↑",
          chord: "Alt+ArrowUp",
          disabled: at <= 0,
          run: () => adjacent(-1),
        },
        {
          id: "next",
          label: "Next container",
          keys: "Alt+↓",
          chord: "Alt+ArrowDown",
          disabled: at < 0 || at >= navigation.length - 1,
          run: () => adjacent(1),
        },
      ]}
    >
      <Page fill className="gap-5 md:gap-5">
        <div className="flex min-w-0 shrink-0 flex-col gap-5">
          <PageContext
            eyebrow={BACK}
            title={detail?.name ?? "Container"}
            actions={
              detail && (
                <>
                  {/* Every verb the container has, the lifecycle as words and
                    the rest behind one menu — the page used to draw only the
                    first three, so pausing or removing the container it was
                    about meant going back to the list. */}
                  <ContainerVerbs
                    detail={detail}
                    confirm={confirm}
                    onOpenTab={setTab}
                    onChanged={changed}
                  />
                  <span className="flex items-center">
                    <IconAction
                      label="Previous container"
                      disabled={at <= 0}
                      onClick={() => adjacent(-1)}
                    >
                      <ChevronUp />
                    </IconAction>
                    <IconAction
                      label="Next container"
                      disabled={at < 0 || at >= navigation.length - 1}
                      onClick={() => adjacent(1)}
                    >
                      <ChevronDown />
                    </IconAction>
                    {/* Keyboard shortcuts mean nothing on a touch screen, and
                      the row of verbs already wraps there. */}
                    <span className="max-sm:hidden">
                      <WorkspaceHelp />
                    </span>
                  </span>
                </>
              )
            }
          />
          {detail && verdict && <ContainerIdentity detail={detail} verdict={verdict} />}
        </div>

        {error && <ErrorState error={error} />}
        {!detail && !error && <LoadingRows />}

        {detail && verdict && (
          <Tabs value={tab} onValueChange={(next) => setTab(next)} className="min-h-0 flex-1 gap-0">
            {/* The underlined strip every switcher between views of one thing
              wears (`tabClasses`), with the two views that hold a list
              counting it. */}
            <TabsPrimitive.List
              aria-label={`Views of ${detail.name}`}
              className="flex shrink-0 [scrollbar-width:none] gap-1 overflow-x-auto border-b border-hairline [&::-webkit-scrollbar]:hidden"
            >
              {views.map((view) => (
                <TabsPrimitive.Trigger
                  key={view.key}
                  value={view.key}
                  className={tabClasses(tab === view.key, "h-10")}
                >
                  {view.label}
                  {view.count !== undefined && view.count > 0 && (
                    <ChipCount>{view.count}</ChipCount>
                  )}
                </TabsPrimitive.Trigger>
              ))}
            </TabsPrimitive.List>

            <TabsContent value="overview" className="min-h-0 flex-1 overflow-y-auto pt-6">
              {/*
                Why it is not working, then what it is using, then what is
                wrong with it, then what it is connected to and what it runs
                beside, then what happened and how it runs. An operator who
                opened this page opened it for the first of those.
              */}
              <div className="flex min-w-0 flex-col gap-8 pb-4">
                <FailurePanel
                  data={failure.data}
                  onReadLogs={() => {
                    setCrash(true)
                    setTab("logs")
                  }}
                />
                <ContainerReadings
                  detail={detail}
                  frames={frames}
                  onUsage={() => setTab("usage")}
                />
                <ContainerFindings
                  diagnosis={health.data}
                  containerId={detail.id}
                  onAction={runFix}
                />
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
                <div className="grid gap-8 xl:grid-cols-2 [&>*]:min-w-0">
                  <ContainerRecent detail={detail} onEvents={() => setTab("logs", "events")} />
                  <HowItRuns detail={detail} />
                </div>
                <div className="grid items-start gap-6 2xl:grid-cols-2 [&>*]:min-w-0">
                  <PortsTable detail={detail} routes={routes.data} />
                  <NetworksTable detail={detail} />
                </div>
              </div>
            </TabsContent>

            <TabsContent value="usage" className="min-h-0 flex-1 overflow-y-auto pt-6">
              <ContainerUsageTab
                key={detail.id}
                detail={detail}
                onLimitsSaved={() => {
                  health.refresh()
                  setReloads((n) => n + 1)
                  window.dispatchEvent(new Event("jd:health-changed"))
                }}
              />
            </TabsContent>

            {/* Scrolls on a phone, where the readings and a pane worth reading
              are taller than what is left of the window under the facts. */}
            <TabsContent value="logs" className="min-h-0 flex-1 overflow-y-auto pt-4">
              <ContainerLogs
                detail={detail}
                failure={failure.data}
                crash={crash}
                view={logView}
                onCrashChange={setCrash}
              />
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
                <ConfigurationRemedy
                  detail={detail}
                  findings={(health.data?.findings ?? []).filter(
                    (finding) => finding.targetId === detail.id,
                  )}
                  confirm={confirm}
                  onChanged={() => {
                    health.refresh()
                    setReloads((n) => n + 1)
                  }}
                />
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
      </Page>
    </Workspace>
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
            <Detail label={<Term name="image">Image</Term>} className="font-mono break-all">
              {detail.image}
            </Detail>
            <Detail label="Working dir" className="font-mono break-all">
              {detail.workingDir || "/"}
            </Detail>
            <Detail label={<Term name="containerUser">User</Term>} className="font-mono">
              {detail.user || "the image's own"}
            </Detail>
            <Detail label={<Term name="networkMode">Network mode</Term>}>
              {detail.networkMode === "host" ? (
                <span className="text-warning">host — the server&rsquo;s own network</span>
              ) : (
                detail.networkMode
              )}
            </Detail>
            <Detail label={<Term name="privileged">Privileged</Term>}>
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
            <Detail label={<Term name="restart">Restart policy</Term>}>
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
            <Detail label={<Term name="exitCode">Last exit</Term>}>
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
 * lines that led to it are one page.
 *
 * The failure diagnosis's window is the one question worth a chip: a
 * container that is looping or has died is read at the failure, because by
 * the time anybody looks the tail is the next attempt starting up.
 */
function ContainerLogs({
  detail,
  failure,
  crash,
  view,
  onCrashChange,
}: {
  detail: ContainerDetail
  failure?: FailureDiagnosis
  /** Whether the pane is handed the failure's window. */
  crash: boolean
  /** A view a press elsewhere on the page asked for: Events, from the Overview's Recent. */
  view?: string
  onCrashChange: (on: boolean) => void
}) {
  const product = containerProduct(detail)
  const sources = useMemo<ServiceLogSource[]>(
    () => [
      {
        id: dockerSource(detail.id),
        label: detail.name,
        kind: "docker",
        status: detail.state,
        product,
      },
    ],
    [detail.id, detail.name, detail.state, product],
  )
  const views = useMemo(
    () => [containerEventsView({ containerId: detail.id, healthcheck: detail.hasHealthcheck })],
    [detail.id, detail.hasHealthcheck],
  )
  const logWindow = failure?.logWindow
  // A clean exit has a window too — the minutes before it stopped — and
  // calling that a crash would be the page inventing one.
  const crashed =
    failure?.state === "looping" || (detail.state !== "running" && detail.exitCode !== 0)
  const label = crashed ? "Crash window" : "Before it stopped"
  // The pane lets go of the window the moment the reader moves off it —
  // Live, another range, a zoom, History on an event — so the chip is on
  // exactly while the pane reads the window, and a press brings it back.
  const chip = logWindow && (
    <FilterChip
      selected={crash}
      onClick={() => onCrashChange(!crash)}
      title={`The logs worth reading are ${logWindow.reason}.`}
    >
      <ClockRewind aria-hidden className="size-3 text-muted-foreground" />
      {label}
    </FilterChip>
  )
  // The pane's strip has no room for the chip beside four views on a phone.
  const wide = useMediaQuery("(min-width: 640px)")
  return (
    <div className="flex h-full min-h-0 flex-col gap-3">
      {!wide && chip && <div className="flex shrink-0">{chip}</div>}
      <ServiceLogs
        sources={sources}
        // Kept for the tab, per container: the Environment tab and back is
        // not a reason to lose the question on screen.
        storageKey={`docker.container.${detail.id}.logs`}
        readings
        views={views}
        view={view}
        window={crash && logWindow ? { ...logWindow, label } : undefined}
        onLeaveWindow={() => onCrashChange(false)}
        actions={wide && chip}
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
function ContainerVerbs({
  detail,
  confirm,
  onOpenTab,
  onChanged,
}: {
  detail: ContainerDetail
  confirm: ConfirmFn
  onOpenTab: (tab: string) => void
  onChanged: () => void
}) {
  const { can } = useAuth()
  const { pending, act } = useContainerControl(onChanged)
  const verbs = useContainerVerbs({
    container: detail,
    confirm,
    act,
    onOpenTab,
    onChanged,
  })
  const busy = pending[detail.id]
  const inline = verbs.filter((verb) => verb.inline)
  const rest = verbs.filter((verb) => !verb.inline && verb.key !== "logs" && verb.key !== "shell")
  const composeManaged = Boolean(detail.composeStack)

  return (
    <span className="flex flex-wrap items-center gap-1.5">
      {inline.map((verb) => (
        <Button
          key={verb.key}
          size="sm"
          variant="outline"
          disabled={Boolean(busy)}
          pending={busy === verb.progressive}
          onClick={verb.run}
        >
          <verb.icon className="size-3.5" />
          {verb.label}
        </Button>
      ))}
      {/*
        Compose owns this container, so anything that changes its
        configuration is undone by the next deploy — silently, and days later,
        which is the worst way to find out. Rename moves behind a statement of
        what will happen, and the way to the stack that owns it comes first.
      */}
      {composeManaged ? (
        <>
          <Button size="sm" variant="outline" asChild>
            <Link href={`/docker/stacks/${encodeURIComponent(detail.composeStack ?? "")}`}>
              <Layers className="size-3.5" />
              Open stack
            </Link>
          </Button>
          {can("service.control") && <ComposeDriftMenu detail={detail} onChanged={onChanged} />}
        </>
      ) : (
        can("service.control") && <RenameButton detail={detail} onRenamed={onChanged} />
      )}
      {rest.length > 0 && <VerbMenu verbs={rest} label={`More actions for ${detail.name}`} />}
    </span>
  )
}

/**
 * The configuration changes that compose will undo, behind a statement saying
 * so.
 *
 * Not hidden and not disabled: an operator who genuinely wants to rename a
 * compose container — to get it out of the project, say — should be able to.
 * What they should not be able to do is press it without being told that the
 * next deploy puts it back, because the failure is silent and arrives days
 * later.
 */
function ComposeDriftMenu({
  detail,
  onChanged,
}: {
  detail: ContainerDetail
  onChanged: () => void
}) {
  const [open, setOpen] = useState(false)
  if (!open) {
    return (
      <Button size="sm" variant="outline" onClick={() => setOpen(true)}>
        Advanced
      </Button>
    )
  }
  return (
    <span className="flex flex-wrap items-center gap-1.5">
      <Hint className="basis-full text-warning">
        This container is managed by <b>{detail.composeStack}</b>. Changes made here are replaced
        the next time the stack is deployed — edit the compose file instead if you want them to
        last.
      </Hint>
      <RenameButton detail={detail} onRenamed={onChanged} />
    </span>
  )
}

function RenameButton({ detail, onRenamed }: { detail: ContainerDetail; onRenamed: () => void }) {
  const [editing, setEditing] = useState(false)
  const [name, setName] = useState(detail.name)

  const save = async () => {
    try {
      await post(`/docker/containers/${detail.id}/rename`, { name })
      notify.success(`Renamed to ${name}`)
      setEditing(false)
      onRenamed()
    } catch (err) {
      const message = err instanceof ApiError ? err.message : String(err)
      notify.error("Could not rename it", message)
    }
  }

  if (!editing) {
    return (
      <Button size="sm" variant="ghost" onClick={() => setEditing(true)}>
        <Pencil className="size-3.5" />
        Rename
      </Button>
    )
  }
  return (
    <span className="flex items-center gap-1.5">
      <Input
        autoFocus
        value={name}
        spellCheck={false}
        onChange={(e) => setName(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter") save()
          if (e.key === "Escape") setEditing(false)
        }}
        className="h-8 w-40 font-mono text-xs"
      />
      <Button size="xs" onClick={save} disabled={!name.trim() || name === detail.name}>
        Save
      </Button>
      <Button size="xs" variant="ghost" onClick={() => setEditing(false)}>
        Cancel
      </Button>
    </span>
  )
}

/**
 * Why it stopped, and why it keeps stopping.
 *
 * The facts are already on the page and none of them is an answer: a restart
 * count, an exit code, a memory limit. Somebody who has done this for years
 * reads those three as "it is exceeding its limit and the kernel is killing
 * it"; everybody else reads three numbers. This is the assembly — and because
 * it is an assembly rather than a reading, the conclusion says "likely".
 */
function FailurePanel({
  data,
  onReadLogs,
}: {
  data?: FailureDiagnosis
  /** Opens the Logs tab on the diagnosis's window. */
  onReadLogs: () => void
}) {
  // A container that has been up for a week with nothing to say is told so by
  // the identity line, which ticks its uptime beside its verdict; the
  // sentence that used to open the tab said it a second time.
  if (!data || (data.state === "running" && !data.restarts.looping)) return null

  const tone = data.state === "looping" || data.state === "unhealthy" ? "danger" : "warning"
  return (
    <Notice title={data.headline} icon={Warning} tone={tone}>
      {data.likely && (
        <p>
          {data.likely}
          <span className="ml-1 text-hint text-muted-foreground">
            ({data.confidence === "observed" ? "recorded by Docker" : "inferred from the evidence"})
          </span>
        </p>
      )}

      {/* The evidence as a column of facts, the decisive one in ink: three
          lines of "label: value — source" ran together into one paragraph. */}
      {data.evidence.length > 0 && (
        <dl className="mt-3 grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-1 text-hint sm:grid-cols-[auto_minmax(0,1fr)_auto]">
          {data.evidence.map((item, i) => (
            <Fragment key={i}>
              <dt className="text-muted-foreground">{item.label}</dt>
              <dd
                className={cn(
                  "min-w-0 break-words",
                  item.weight === "decisive" ? "font-medium text-foreground" : "text-foreground/85",
                )}
              >
                {item.value}
              </dd>
              <dd className="text-muted-foreground/70 max-sm:hidden">{item.source}</dd>
            </Fragment>
          ))}
        </dl>
      )}

      {data.suggestions.length > 0 && (
        <ul className="mt-2 space-y-1 text-hint">
          {data.suggestions.map((suggestion, i) => (
            <li key={i} className="text-muted-foreground">
              · {suggestion}
            </li>
          ))}
        </ul>
      )}

      {data.logWindow && (
        <div className="mt-2 flex flex-wrap items-center gap-x-3 gap-y-1.5">
          <p className="min-w-0 flex-1 basis-64 text-hint text-muted-foreground">
            The logs worth reading are {data.logWindow.reason} — {timestamp(data.logWindow.since)}{" "}
            to {timestamp(data.logWindow.until)}. By the time you look, the tail is the next attempt
            starting up.
          </p>
          <Button size="xs" variant="outline" onClick={onReadLogs}>
            <ClockRewind className="size-3" />
            Read those lines
          </Button>
        </div>
      )}
    </Notice>
  )
}
