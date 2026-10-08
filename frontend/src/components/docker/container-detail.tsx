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
  Copy,
  Eye,
  EyeOff,
  Information,
  Layers,
  Pencil,
  ShieldOff,
  Warning,
} from "@/components/icons"
import { notify } from "@/lib/toast"
import { get, post, patch, ApiError } from "@/lib/api"
import { bytes, relativeTime, timestamp } from "@/lib/format"
import { useSessionState, useViewState } from "@/lib/view-state"
import type {
  ContainerDetail,
  DockerDiagnosis,
  FailureDiagnosis,
  FileChange,
  MigrationPlan,
  PortRoute,
  WritableEntry,
  WritableLayerReport,
} from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { useMediaQuery } from "@/hooks/use-mobile"
import { dockerSource } from "@/lib/log-sources"
import { ServiceLogs, type ServiceLogSource } from "@/components/logs/service-logs"
import { XtermPane } from "@/components/xterm-pane"
import { EmptyNote, ErrorState, LoadingRows, Notice } from "@/components/state"
import { ContainerUsage } from "@/components/docker/container-usage"
import { ContainerLiveUsage } from "@/components/docker/container-live-usage"
import { useContainerControl, useContainerVerbs } from "@/components/docker/container-actions"
import { useDockerFindingActions } from "@/components/docker/finding-actions"
import { ConfigurationRemedy } from "@/components/docker/configuration-remedy"
import { ContainerFindings } from "@/components/docker/attention"
import { containerEventsView } from "@/components/docker/container-events"
import { ExplainIcon, Hint, Term } from "@/components/docker/explain"
import {
  DatabaseStorageWarning,
  looksLikeDatabase,
  type ConfirmFn,
} from "@/components/docker/shared"
import { containerVerdict, exitWords, restartWords } from "@/components/docker/container"
import { ContainerIdentity } from "@/components/docker/container-identity"
import { ContainerReadings, useContainerFrames } from "@/components/docker/container-readings"
import { ContainerPicture } from "@/components/docker/container-picture"
import { ContainerCompany } from "@/components/docker/container-company"
import { ContainerRecent } from "@/components/docker/container-recent"
import {
  EnvironmentTable,
  NetworksTable,
  PortsTable,
  isSecretEnvKey,
} from "@/components/docker/container-tables"
import { FileBrowser } from "@/components/files/inline-browser"
import { containerProduct } from "@/components/product-logo"
import { useConfirm } from "@/components/confirm-dialog"
import { Detail, DetailList, Page, PageContext } from "@/components/page"
import { Workspace, WorkspaceHelp } from "@/components/workspace/workspace"
import { Group, Panel, PanelHeader, Well } from "@/components/panel"
import { ChipCount, FilterChip, tabClasses } from "@/components/tabs"
import { Tag } from "@/components/tag"
import { IconAction } from "@/components/icon-action"
import { VerbMenu } from "@/components/verbs"
import { ShellWords } from "@/components/deploy/run-evidence"
import { cn } from "@/lib/utils"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Tabs, TabsContent } from "@/components/ui/tabs"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { copyText } from "@/lib/clipboard"

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

            <TabsContent value="usage" className="min-h-0 flex-1 space-y-6 overflow-y-auto pt-6">
              <ContainerLiveUsage key={detail.id} detail={detail} />
              <ResourceLimitsEditor
                detail={detail}
                onSaved={() => {
                  health.refresh()
                  setReloads((n) => n + 1)
                  window.dispatchEvent(new Event("jd:health-changed"))
                }}
              />
              <ContainerUsage containerId={detail.id} name={detail.name} plain />
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
              <div className="flex h-full min-h-0 flex-col gap-3">
                <MountList detail={detail} />
                <WritableLayer containerId={detail.id} />
              </div>
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
              {tab === "inspect" && <RawInspect containerId={detail.id} />}
            </TabsContent>

            {shell && (
              <TabsContent value="shell" className="min-h-0 flex-1 pt-4">
                {tab === "shell" && <ContainerShell detail={detail} />}
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
 * A shell *inside* the container — which is the whole point of it, and the
 * thing most easily mistaken for the Terminal page.
 *
 * The two answer different questions. This one lands wherever the image says:
 * as the image's USER, in its WORKDIR. For most images that is root in `/` or
 * `/app`, and that is not a bug to be fixed — a container shell that quietly
 * became a host login would leave no way to look inside a container at all.
 * The Terminal page is the host; this is the box running on it.
 *
 * Docker already honours the image's user by default, so "default" sends no
 * user at all rather than guessing one. Root is offered because the common
 * reason to open this at all is that something needs installing or reading in
 * an image that deliberately runs unprivileged.
 */
function ContainerShell({ detail }: { detail: ContainerDetail }) {
  const [asRoot, setAsRoot] = useState(false)

  // Most images declare no USER, so the container already runs as root and
  // there is no second account to offer. The toggle used to be drawn anyway,
  // from `detail.user || "root"` — which rendered two buttons both labelled
  // "root" that switched between a request with no user and a request for
  // root, i.e. between the same thing twice.
  const imageUser = detail.user.trim()
  const runsAsRoot =
    imageUser === "" || imageUser === "root" || imageUser.startsWith("0:") || imageUser === "0"
  const account = asRoot || runsAsRoot ? "root" : imageUser

  return (
    <div className="flex h-full min-h-0 flex-col gap-2">
      <div className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
        <span className="min-w-0 flex-1">
          Inside <span className="font-mono text-foreground">{detail.name}</span>, not on the host —
          the Terminal page is the server itself.
        </span>
        {runsAsRoot ? (
          // Worth stating rather than leaving blank: "which account am I" is
          // the first thing you need to know in a container shell, and this
          // one is the answer most people assume without checking.
          <span className="shrink-0">
            as <span className="font-mono text-foreground">root</span> — this image sets no user
          </span>
        ) : (
          <ToggleGroup
            type="single"
            size="sm"
            variant="outline"
            value={asRoot ? "root" : "default"}
            onValueChange={(v) => v && setAsRoot(v === "root")}
          >
            <ToggleGroupItem value="default" className="px-2 text-hint">
              {imageUser}
            </ToggleGroupItem>
            <ToggleGroupItem value="root" className="px-2 text-hint">
              root
            </ToggleGroupItem>
          </ToggleGroup>
        )}
      </div>
      <XtermPane
        // Keyed on the account, so switching it opens a new exec rather than
        // leaving you in the previous one with a stale label above it.
        key={account}
        path={`/docker/containers/${detail.id}/exec`}
        // No user at all when the image's own is wanted: Docker already
        // honours it, and naming it would override a `user:group` form with
        // just the user half.
        query={{ rows: 30, cols: 100, user: asRoot && !runsAsRoot ? "root" : undefined }}
        className="min-h-0 flex-1"
        subtitle={`${account}@${detail.name} · container shell`}
      />
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
  return (
    <Panel plain className="animate-rise">
      <PanelHeader title="How it runs" />
      <div className="flex min-w-0 flex-col gap-4 pt-3">
        {detail.command && (
          <Well className="max-h-28 text-xs leading-relaxed break-all whitespace-pre-wrap">
            <ShellWords command={detail.command} />
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
 * The mounts, and what is in one of them — already open.
 *
 * Each mount was a row that had to be expanded before it showed anything, and
 * what it expanded into was a browser indented under the row, capped at a
 * third of the screen, with the rest of the tab empty below it. The question
 * somebody opens this tab with — did the backup land, is this the volume with
 * the database in it — was a click and a scroll away on every visit.
 *
 * So the first mount there is something to look in is open when the tab is,
 * and the browser takes the height the tab has. The mounts sit above it as one
 * line each; with more than one to look in, picking a line is what changes the
 * listing, the way the file manager's sidebar does.
 */
function MountList({ detail }: { detail: ContainerDetail }) {
  const mounts = detail.mounts
  const [selected, setSelected] = useState(() => mounts.findIndex(browsable))
  const choosing = mounts.filter(browsable).length > 1
  const mount = mounts[selected]

  if (mounts.length === 0) {
    return (
      <EmptyNote>
        Nothing is attached, so everything this container writes is destroyed when it is replaced.
      </EmptyNote>
    )
  }

  // Writing into a live database's own files is how a volume stops being
  // restorable. A stopped container is not running that database, and telling
  // somebody to stop what is already stopped is noise.
  const databaseFiles =
    mount &&
    detail.state === "running" &&
    looksLikeDatabase(mount.name, mount.destination, mount.source, detail.image)

  return (
    <>
      <ul aria-label="Mounts" className="shrink-0 space-y-0.5">
        {mounts.map((m, i) => (
          <MountRow
            key={i}
            mount={m}
            selected={choosing && i === selected}
            onSelect={choosing && browsable(m) ? () => setSelected(i) : undefined}
          />
        ))}
      </ul>
      {databaseFiles && <DatabaseStorageWarning />}
      {mount && (
        <FileBrowser
          fill
          className="min-h-80"
          root={mount.source}
          label={mount.type === "volume" ? MOUNT_KIND.volume.where(mount) : undefined}
          emptyNote={
            mount.type === "volume"
              ? "Nothing has been written to this volume yet."
              : "This folder is empty."
          }
        />
      )}
    </>
  )
}

/**
 * One mount on one line: the path the application inside was configured with,
 * where that really is, and what kind of storage it is at the edge.
 */
function MountRow({
  mount,
  selected,
  onSelect,
}: {
  mount: ContainerDetail["mounts"][number]
  selected: boolean
  /** Set when there is more than one mount to look in and this is one of them. */
  onSelect?: () => void
}) {
  const kind = MOUNT_KIND[mount.type]
  const where = kind ? kind.where(mount) : mount.source

  const facts = (
    <>
      <span className="min-w-0 truncate font-mono text-body" title={mount.destination}>
        {mount.destination}
      </span>
      <span className="min-w-0 flex-1 truncate text-hint text-muted-foreground" title={where}>
        {where}
      </span>
      {!mount.rw && <Tag>read-only</Tag>}
      <Tag tone={kind?.survives === false ? "warning" : "default"}>{kind?.label ?? mount.type}</Tag>
    </>
  )
  const row = "flex min-w-0 flex-1 items-baseline gap-3 px-2.5 py-1.5"

  return (
    <li
      className={cn(
        "flex min-w-0 items-center rounded-md pr-2.5 transition-colors",
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

/**
 * Masks credential-shaped environment values inside a decoded inspect document.
 *
 * Mirrors what the server does for anyone below system.admin. For an admin the
 * server sends the real values — correctly, they are allowed to see them — but
 * the Environment tab still puts them behind a deliberate reveal, and one tab
 * over printing the same secrets unprompted made that gesture worthless. The
 * threat here is a screen, not a permission.
 *
 * Only the two places the Engine puts an environment are walked, not every
 * string in the document: a blanket scrub mangles labels and commands that
 * legitimately contain the word "key".
 */
function maskRawEnv(doc: Record<string, unknown>): {
  doc: Record<string, unknown>
  masked: number
} {
  let masked = 0
  const config = doc.Config
  if (!config || typeof config !== "object") return { doc, masked }
  const env = (config as Record<string, unknown>).Env
  if (!Array.isArray(env)) return { doc, masked }

  const maskedEnv = env.map((entry) => {
    if (typeof entry !== "string") return entry
    const eq = entry.indexOf("=")
    if (eq === -1) return entry
    const name = entry.slice(0, eq)
    if (!isSecretEnvKey(name)) return entry
    masked++
    return `${name}=••••••••••••`
  })
  if (masked === 0) return { doc, masked }
  return {
    doc: { ...doc, Config: { ...(config as Record<string, unknown>), Env: maskedEnv } },
    masked,
  }
}

/**
 * The Engine's own inspect output.
 *
 * Every panel here is a chosen subset of something, and eventually somebody
 * needs the field nobody chose. This is also the check on the rest of the
 * page: an operator who suspects the dashboard is misreporting something can
 * see what it was reading. Credential-shaped environment values are masked on
 * the server for anyone below system.admin — the raw route is not a way around
 * that — and masked again here for the admin who is allowed to read them, so
 * that opening a tab is never by itself what puts a master key on screen.
 */
function RawInspect({ containerId }: { containerId: string }) {
  const { data, error, loading } = usePoll<Record<string, unknown>>(
    (signal) =>
      get<Record<string, unknown>>(`/docker/containers/${containerId}/raw`, undefined, signal),
    0,
    [containerId],
  )
  const [revealed, setRevealed] = useState(false)
  const { text, masked } = useMemo(() => {
    if (!data) return { text: "", masked: 0 }
    const result = maskRawEnv(data)
    return {
      text: JSON.stringify(revealed ? data : result.doc, null, 2),
      masked: result.masked,
    }
  }, [data, revealed])

  if (error) return <ErrorState error={error} />
  if (loading) return <LoadingRows />
  return (
    <div className="flex h-full min-h-0 flex-col gap-2">
      <div className="flex items-center gap-2">
        <Hint className="flex-1">
          What Docker itself reports about this container. Everything above is a reading of this.
        </Hint>
        {masked > 0 && (
          <Button size="xs" variant="ghost" onClick={() => setRevealed((r) => !r)}>
            {revealed ? <EyeOff className="size-3" /> : <Eye className="size-3" />}
            {revealed ? "Hide" : "Reveal"}
          </Button>
        )}
        {/* Copies what is on screen, masked included: a raw inspect pasted
            into a ticket is the other way these values get away. */}
        <Button
          size="xs"
          variant="outline"
          onClick={() =>
            void copyText(text, revealed || masked === 0 ? "Copied" : "Copied, credentials masked")
          }
        >
          <Copy className="size-3" />
          Copy
        </Button>
      </div>
      {masked > 0 && (
        <p className="flex items-start gap-2 text-xs text-muted-foreground">
          <ShieldOff className="mt-px size-3.5 shrink-0" />
          <span>
            {masked} {masked === 1 ? "value looks" : "values look"} like a credential and{" "}
            {masked === 1 ? "is" : "are"} hidden here too, the same as on the Environment tab.
          </span>
        </p>
      )}
      <Well className="min-h-0 flex-1 whitespace-pre">{text}</Well>
    </div>
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

/**
 * The limits, editable in place.
 *
 * Almost nothing about a container can be changed after it is created — this
 * is the exception, and it is worth surfacing separately for that reason: an
 * operator who set a memory limit and got the number wrong should not have to
 * destroy the container to fix it. Docker applies the change to the running
 * cgroup immediately.
 *
 * It sits next to the usage charts because that is where the mistake becomes
 * visible: a container repeatedly touching its ceiling, or one with no ceiling
 * at all climbing towards the host's.
 */
function ResourceLimitsEditor({
  detail,
  onSaved,
}: {
  detail: ContainerDetail
  onSaved: () => void
}) {
  const { can } = useAuth()
  const [memory, setMemory] = useState("")
  const [cpus, setCpus] = useState("")
  const [busy, setBusy] = useState(false)
  const [open, setOpen] = useState(false)

  if (!can("service.control")) return null

  const save = async () => {
    setBusy(true)
    try {
      const res = await patch<{ warnings: string[] }>(
        `/docker/containers/${detail.id}/resources`,
        {
          memoryMb: Number(memory) || undefined,
          cpus: Number(cpus) || undefined,
        },
        {},
      )
      if (res.warnings?.length) {
        notify.warning("Applied, with a caveat", { description: res.warnings[0] })
      } else {
        notify.success("Limits updated")
      }
      setOpen(false)
      onSaved()
    } catch (err) {
      const message = err instanceof ApiError ? err.message : String(err)
      notify.error("Could not change the limits", message)
    } finally {
      setBusy(false)
    }
  }

  if (!open) {
    return (
      <Group className="flex flex-wrap items-center gap-2">
        <span className="min-w-0 flex-1 text-xs text-muted-foreground">
          <Term name="memoryLimit">Limits</Term> can be changed without recreating this container —
          resource limits can be updated without replacing it.
        </span>
        <Button size="xs" variant="outline" onClick={() => setOpen(true)}>
          <Pencil className="size-3" />
          Change limits
        </Button>
      </Group>
    )
  }

  return (
    <Group className="space-y-2">
      <div className="flex flex-wrap items-end gap-3">
        <div className="w-32">
          <label className="text-micro text-muted-foreground" htmlFor="limit-memory">
            Memory (MB)
          </label>
          <Input
            id="limit-memory"
            type="number"
            value={memory}
            placeholder="unlimited"
            onChange={(e) => setMemory(e.target.value)}
            className="h-8 text-xs"
          />
        </div>
        <div className="w-28">
          <label className="text-micro text-muted-foreground" htmlFor="limit-cpus">
            CPU cores
          </label>
          <Input
            id="limit-cpus"
            type="number"
            step="0.5"
            value={cpus}
            placeholder="unlimited"
            onChange={(e) => setCpus(e.target.value)}
            className="h-8 text-xs"
          />
        </div>
        <Button size="sm" onClick={save} pending={busy}>
          Apply
        </Button>
        <Button size="sm" variant="ghost" onClick={() => setOpen(false)} disabled={busy}>
          Cancel
        </Button>
      </div>
      {detail.composeStack && (
        <p className="text-hint text-muted-foreground">
          This changes the live container.{" "}
          <Link
            className="underline"
            href={`/docker/stacks/${encodeURIComponent(detail.composeStack)}?tab=compose&remedy=nomemorylimit`}
          >
            Update the owning Compose service
          </Link>{" "}
          too, so the limit survives deployment.
        </p>
      )}
      <Hint>
        Leaving a field empty means no change. A memory limit is what makes the kernel kill this
        container rather than choosing a victim across the whole server; the trade is that it will
        be killed when it exceeds it.
      </Hint>
    </Group>
  )
}
