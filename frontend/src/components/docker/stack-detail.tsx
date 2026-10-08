"use client"

import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import Link from "next/link"
import { useParams, useRouter, useSearchParams } from "next/navigation"
import { Tabs as TabsPrimitive } from "radix-ui"
import {
  ArrowCircleUp,
  ArrowLeft,
  Box,
  Code,
  Cross,
  FloppyDisk,
  Logs,
  Play,
  RefreshClockwise,
  RotateClockwise,
  StopCircle,
  Terminal,
  Warning,
  Wrench,
} from "@/components/icons"
import { composeRemedy } from "@/lib/docker-remedies"
import { dedupeEvents } from "@/lib/docker-events"
import { notify } from "@/lib/toast"
import { get, post, put, ApiError } from "@/lib/api"
import { plural } from "@/lib/format"
import type {
  ComposeValidation,
  Container,
  ContainerSparkline,
  ContainerStats,
  DockerEvent,
  DockerEventFeed,
  StackDetail,
} from "@/lib/types"
import { stackSource } from "@/lib/log-sources"
import { cn } from "@/lib/utils"
import { useSessionState, useViewState } from "@/lib/view-state"
import { useAuth } from "@/hooks/use-auth"
import { useMediaQuery } from "@/hooks/use-mobile"
import { useMetrics } from "@/hooks/use-metrics"
import { usePoll } from "@/hooks/use-poll"
import { useSocket, type Envelope } from "@/hooks/use-socket"
import { useNow } from "@/components/deploy/vocabulary"
import { containerEventsView } from "@/components/docker/container-events"
import { RunConsole, useRunConsole } from "@/components/docker/run-console"
import { Hint, Term } from "@/components/docker/explain"
import { DeployPreviewPanel, DeploymentHistoryPanel } from "@/components/docker/deploy-preview"
import { COMPOSE_ACTIONS, type ComposeActionKey } from "@/components/docker/stack-state"
import { StackMap } from "@/components/docker/stack-map"
import { ServiceRows } from "@/components/docker/stack-services-table"
import { StackUsageBand } from "@/components/docker/stack-usage-band"
import {
  bucketCounts,
  bucketTone,
  networkRates,
  serviceChanges,
  serviceReadings,
  stackNetworks,
  stackVerdict,
  waysIn,
  type NetRate,
  type ServiceBucket,
  type ServiceReading,
} from "@/components/docker/stack-service-readings"
import { CodeEditor } from "@/components/code-editor"
import { ServiceLogs, type ServiceLogSource } from "@/components/logs/service-logs"
import { useConfirm } from "@/components/confirm-dialog"
import { FactDot, HostFact, HostIdentity } from "@/components/metrics/host-identity"
import { Page, PageContext } from "@/components/page"
import { Panel, PanelBody, PanelFooter, PanelHeader } from "@/components/panel"
import { FileBrowser } from "@/components/files/inline-browser"
import {
  ProductLogo,
  ProductLogos,
  containerProduct,
  imageProduct,
  imageProducts,
} from "@/components/product-logo"
import { EmptyState, ErrorState, LoadingRows, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { ChipCount, ChipStrip, FilterChip, tabClasses } from "@/components/tabs"
import { Tag } from "@/components/tag"
import { VerbMenu, type Verb } from "@/components/verbs"
import { Button } from "@/components/ui/button"
import { Tabs, TabsContent } from "@/components/ui/tabs"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { Workspace, WorkspaceHelp } from "@/components/workspace/workspace"

/**
 * A stack, as the application it is rather than as five container rows.
 *
 * Three things make this different from the stack cards it replaces, and all
 * three are borrowed from the tool that does compose best:
 *
 *   - **The file is editable here.** A compose file is the description of the
 *     application; sending someone to a file manager to change it and back
 *     here to apply it is two tools for one thought. It is validated before it
 *     is written and the previous version is kept beside it.
 *   - **Commands are watched, not waited for.** `up` on a stack that pulls and
 *     builds takes minutes.
 *   - **Its logs are one feed.** A stack is one story told by four processes,
 *     and reading it meant opening four panels and matching timestamps by eye.
 *
 * The fourth thing is ours: a stack is a directory, and this dashboard also
 * has a file manager, a git panel and a terminal. Knowing that a stack's
 * directory is a checkout with uncommitted changes, two commits behind its
 * remote, is exactly the context somebody needs before pressing redeploy — and
 * it is one link away rather than a different product.
 *
 * It was a sheet over the stack list until 2026-09-21, and until 2026-10-08 a
 * strip of three facts over a column of grey cards that said only each
 * service's state word: a worker in a restart loop read "restarting" beside
 * a database a step from its memory limit reading "running", and nothing on
 * the page moved. It now takes the Services and PM2 pages' shape (§15):
 *
 *   - the stack's identity line — its services' products, the compose file,
 *     the directory and its checkout, how many services run and how many
 *     ports it publishes — with the verdict at its end, which counts what is
 *     failing and narrows the table to it;
 *   - a picture of how it is reached and what it is wired to (`StackMap`):
 *     its published ports, its services and their networks, each wire
 *     pulsing with the traffic the containers socket measures;
 *   - its services as a table of live readings (`ServiceRows`), each row its
 *     state with how long or how it went down, its processor's hour, its
 *     memory against its limit, its traffic, its ports and its compose verbs,
 *     under state chips that count and narrow;
 *   - and the Services page's band for its services (`StackUsageBand`): what
 *     the stack takes of the machine, and what just happened to it.
 */
export function StackPage() {
  const { name } = useParams<{ name: string }>()
  const stack = decodeURIComponent(name)
  return <StackBody key={stack} name={stack} />
}

/** What each compose verb is doing while it runs, for the row and the line that wait on it. */
const PARTICIPLE: Record<ComposeActionKey, string> = {
  up: "Deploying",
  start: "Starting",
  pull: "Pulling",
  build: "Rebuilding",
  restart: "Restarting",
  update: "Redeploying",
  recreate: "Recreating",
  stop: "Stopping",
  down: "Removing",
}

/**
 * The compose verbs a single service takes, said for one service. The server
 * runs each with the service's name after it (`composeSteps`), and the
 * confirmation carries the command as it runs.
 */
const SERVICE_ACTIONS = {
  restart: {
    label: "Restart",
    verb: "Restart",
    command: (s: string) => `docker compose restart ${s}`,
    blastRadius: (s: string) =>
      `${s} is stopped and started again, interrupted for as long as it takes to come back. Nothing is recreated and no change to the compose file is applied.`,
  },
  stop: {
    label: "Stop",
    verb: "Stop",
    command: (s: string) => `docker compose stop ${s}`,
    blastRadius: (s: string) =>
      `${s} stops and its container stays where it is. Nothing is deleted; Start or Deploy brings it back.`,
  },
  recreate: {
    label: "Recreate service",
    verb: "Recreate",
    command: (s: string) => `docker compose up -d --force-recreate --remove-orphans ${s}`,
    blastRadius: (s: string) =>
      `${s}'s container is replaced with a new one from the compose file, whether or not anything changed. Data written inside the container rather than into a volume is lost; named volumes are untouched.`,
  },
  update: {
    label: "Pull & redeploy",
    verb: "Redeploy",
    command: (s: string) =>
      `docker compose pull ${s} && docker compose up -d --remove-orphans ${s}`,
    blastRadius: (s: string) =>
      `A newer image for ${s} is downloaded, and its container is replaced if the image changed. Data written inside the container is lost; named volumes are untouched.`,
  },
} as const

type ServiceActionKey = keyof typeof SERVICE_ACTIONS

/** The table's chips, in the order they are asked about; the toned ones only while there is one. */
const STATES: { value: ServiceBucket; label: string; tone?: "warning" | "danger" }[] = [
  { value: "running", label: "Running" },
  { value: "failing", label: "Failing", tone: "danger" },
  { value: "starting", label: "Starting", tone: "warning" },
  { value: "missing", label: "Not created", tone: "warning" },
  { value: "stopped", label: "Stopped" },
  { value: "paused", label: "Paused" },
]

function StackBody({ name }: { name: string }) {
  const { can } = useAuth()
  const router = useRouter()
  const { confirm, dialog } = useConfirm()
  const query = useSearchParams()
  const { snapshot } = useMetrics()
  const [rememberedTab, rememberTab] = useViewState("docker.stack.tab", "services")
  const [tab, setRequestedTab] = useState(query.get("tab") ?? rememberedTab)
  const setTab = (value: string) => {
    setRequestedTab(value)
    rememberTab(value)
  }
  const [state, setState] = useSessionState<ServiceBucket | "">(`docker.stack.${name}.state`, "")
  const runner = useRunConsole()
  // The verb in flight, and the service it is for: that row says so while it runs.
  const [pending, setPending] = useState<{ service?: string; label: string }>()

  const { data, error, loading, refresh } = usePoll<StackDetail>(
    (signal) => get<StackDetail>(`/docker/stacks/${encodeURIComponent(name)}`, undefined, signal),
    // Slower while a command is running: the poll would otherwise fight the
    // console for attention, and the interesting output is in the console.
    runner.running ? 0 : 10000,
    [name],
  )

  const reload = useCallback(() => {
    refresh()
  }, [refresh])

  /*
    The stack's containers from the socket the containers page reads, which
    reports a state change the moment Docker makes it and every container's
    frame every two seconds; the stack poll is ten seconds behind it. Network
    rates are measured frame against frame.
  */
  const [containers, setContainers] = useState<Container[]>()
  const [stats, setStats] = useState<Record<string, ContainerStats>>({})
  const [rates, setRates] = useState<Record<string, NetRate>>({})
  const lastFrame = useRef<Record<string, ContainerStats>>({})
  const onContainers = useCallback(
    (envelope: Envelope) => {
      if (envelope.type === "containers") {
        setContainers((envelope.data as Container[]).filter((c) => c.composeStack === name))
      } else if (envelope.type === "stats") {
        const frame = envelope.data as ContainerStats[]
        setRates(networkRates(lastFrame.current, frame))
        lastFrame.current = Object.fromEntries(frame.map((s) => [s.id, s]))
        setStats(lastFrame.current)
      }
    },
    [name],
  )
  useSocket("/docker/containers/stream", { onMessage: onContainers })

  // An hour of shape per container, in one request: this second's reading
  // says nothing about the ten minutes a service spent pinned before it.
  const trends = usePoll<ContainerSparkline[]>(
    (signal) =>
      get<ContainerSparkline[]>(
        "/docker/containers/stats/history",
        { range: "1h", points: 40 },
        signal,
      ),
    120_000,
    [],
  )
  const trendByName = useMemo(
    () => new Map((trends.data ?? []).map((line) => [line.name, line.cpu])),
    [trends.data],
  )

  // What Docker did to the stack's containers: polled, because the polled
  // copy is the one the server laid against the audit log, and followed.
  const feed = usePoll<DockerEventFeed>(
    (signal) => get<DockerEventFeed>("/docker/events", { stack: name, limit: 300 }, signal),
    15_000,
    [name],
  )
  const [liveEvents, setLiveEvents] = useState<DockerEvent[]>([])
  const onEvents = useCallback((envelope: Envelope) => {
    if (envelope.type !== "events") return
    setLiveEvents((prev) => [...(envelope.data as DockerEvent[])].concat(prev).slice(0, 300))
  }, [])
  useSocket("/docker/events/stream", { query: { stack: name }, onMessage: onEvents })
  const events = useMemo(
    () => dedupeEvents([...(feed.data?.events ?? []), ...liveEvents]),
    [feed.data?.events, liveEvents],
  )

  // Whether a restart loop is still the service's state is read against the clock.
  const readAt = useNow(30_000)

  const readings = useMemo(
    () =>
      data
        ? serviceReadings({
            services: data.services,
            orphans: data.orphans,
            containers: containers ?? [],
            stats,
            rates,
            trends: trendByName,
            events,
            now: readAt,
          })
        : [],
    [data, containers, stats, rates, trendByName, events, readAt],
  )
  const changes = useMemo(() => serviceChanges(events), [events])
  const counts = useMemo(() => bucketCounts(readings), [readings])
  const verdict = stackVerdict(readings, data?.deployed ?? false)
  const visible = state ? readings.filter((r) => r.bucket === state) : readings

  const productOf = useCallback(
    (service: string) => {
      const reading = readings.find((r) => r.key === service)
      if (reading?.container) return containerProduct(reading.container)
      return reading?.service.image ? imageProduct(reading.service.image) : undefined
    },
    [readings],
  )
  const openService = useCallback(
    (service: string | ServiceReading) => {
      const reading =
        typeof service === "string" ? readings.find((r) => r.key === service) : service
      if (!reading?.containerId) return
      router.push(`/docker/containers/${encodeURIComponent(reading.containerId)}`)
    },
    [readings, router],
  )

  const run = async (
    action: ComposeActionKey,
    opts: { confirmPhrase?: string; service?: string } = {},
  ) => {
    setPending({ service: opts.service, label: PARTICIPLE[action] })
    const code = await runner
      .run(`/docker/stacks/${encodeURIComponent(name)}/run`, {
        action,
        service: opts.service,
        confirm: opts.confirmPhrase,
      })
      .finally(() => setPending(undefined))
    // `down` removes the containers, and with them the stack this page is
    // about: compose only knows a stack that has some. Staying here would
    // report the disappearance as an error about something the reader just
    // asked for on purpose.
    if (action === "down" && code === 0) {
      router.replace("/docker/stacks")
      return
    }
    reload()
    feed.refresh()
    if (code !== 0) throw new Error(`compose ${action} exited with status ${code}`)
  }

  /**
   * The destructive actions all pause for a confirmation, and `down` is the one
   * that asks for the stack's name to be typed — it is the only one that
   * removes the containers. Update and restart are the ordinary redeploy cycle,
   * run several times in an afternoon, and the server narrows the phrase the
   * same way so the two cannot disagree.
   */
  const confirmRun = (action: ComposeActionKey, title: string, description: React.ReactNode) =>
    confirm({
      title,
      phrase: action === "down" ? name : undefined,
      confirmLabel: title.split(" ")[0],
      description,
      action: (phrase) => run(action, { confirmPhrase: phrase }),
    })

  const control = Boolean(data?.managed) && can("system.admin")
  const busy = runner.running

  /** One service's verbs: compose's, for that service, and the ways into its container. */
  const serviceVerbs = (reading: ServiceReading): Verb[] => {
    const service = reading.key
    const up = reading.state === "running"
    const act = (action: ServiceActionKey) => {
      const meta = SERVICE_ACTIONS[action]
      confirm({
        title: `${meta.verb} ${service}`,
        confirmLabel: meta.verb,
        description: (
          <>
            <p>{meta.blastRadius(service)}</p>
            <p className="font-mono text-hint text-muted-foreground">{meta.command(service)}</p>
          </>
        ),
        action: (phrase) => run(action, { service, confirmPhrase: phrase }),
      })
    }
    const verbs: Verb[] = []
    if (control && can("service.control") && !up && reading.state !== "restarting") {
      verbs.push({
        key: "start",
        label: "Start",
        icon: Play,
        inline: true,
        disabled: busy,
        run: () => void run("start", { service }).catch((err) => notify.error(String(err))),
      })
    }
    if (control && can("destructive") && up) {
      verbs.push({
        key: "restart",
        label: "Restart",
        icon: RotateClockwise,
        inline: true,
        disabled: busy,
        run: () => act("restart"),
      })
    }
    if (control && can("destructive") && (up || reading.state === "restarting")) {
      verbs.push({
        key: "stop",
        label: "Stop",
        icon: StopCircle,
        inline: true,
        disabled: busy,
        run: () => act("stop"),
      })
    }
    if (control && can("destructive")) {
      verbs.push(
        {
          key: "recreate",
          label: "Recreate service",
          icon: RefreshClockwise,
          disabled: busy,
          run: () => act("recreate"),
        },
        {
          key: "update",
          label: "Pull & redeploy",
          icon: ArrowCircleUp,
          disabled: busy,
          run: () => act("update"),
        },
      )
    }
    if (reading.containerId) {
      const container = encodeURIComponent(reading.containerId)
      verbs.push({
        key: "logs",
        label: "Logs",
        icon: Logs,
        run: () => router.push(`/docker/containers/${container}?tab=logs`),
      })
      if (up && can("terminal")) {
        verbs.push({
          key: "shell",
          label: "Open a shell",
          icon: Terminal,
          run: () => router.push(`/docker/containers/${container}?tab=shell`),
        })
      }
    }
    return verbs
  }

  const ports = useMemo(() => waysIn(readings).length, [readings])
  const networks = useMemo(() => stackNetworks(readings).length, [readings])
  const tabs = [
    { value: "services", label: "Services", count: data?.services.length },
    // What a deploy would change, before it changes it.
    { value: "preview", label: "Deploy preview" },
    { value: "compose", label: "Compose file" },
    // The stack's own directory, read where the stack is: the compose file's
    // neighbours — an `.env`, a mounted config, the data a bind mount writes —
    // are what a stack is opened to check.
    ...(data?.workingDir ? [{ value: "files", label: "Files" }] : []),
    { value: "history", label: "History" },
    { value: "logs", label: "Logs" },
  ]

  return (
    <Workspace
      name="Stack"
      stateKey={`stack.${name}.${tab}`}
      refresh={() => {
        reload()
        feed.refresh()
        trends.refresh()
      }}
      search={false}
      escape={() => {
        if (state) {
          setState("")
          return true
        }
        return false
      }}
      commands={[
        {
          id: "failing",
          label: state === "failing" ? "Show every service" : "Show failing services",
          run: () => {
            setTab("services")
            setState(state === "failing" ? "" : "failing")
          },
        },
      ]}
    >
      <Page fill>
        <div className="flex min-w-0 shrink-0 flex-col gap-4">
          <PageContext
            eyebrow={
              <Link
                href="/docker/stacks"
                className="inline-flex items-center gap-1 rounded-sm focus-ring hover:underline"
              >
                <ArrowLeft className="size-3" /> Stacks
              </Link>
            }
            title={name}
            actions={
              data && (
                <>
                  <StackActions data={data} confirmRun={confirmRun} run={run} busy={busy} />
                  <WorkspaceHelp />
                </>
              )
            }
          />
          {data && (
            <HostIdentity
              className="animate-rise border-b-0 pb-0"
              logo={<StackMark stack={data} containers={containers} />}
              title={name}
              facts={
                <StackFacts data={data} running={counts.running + counts.starting} ports={ports} />
              }
              aside={
                pending ? (
                  <span className="flex items-center gap-2 px-1.5 text-xs font-medium">
                    <Status
                      tone="notice"
                      label={<TextShimmer>{`${pending.label}…`}</TextShimmer>}
                    />
                  </span>
                ) : verdict.bucket && verdict.tone !== "running" ? (
                  <button
                    type="button"
                    aria-pressed={state === verdict.bucket}
                    title="Show only these services"
                    onClick={() => {
                      setTab("services")
                      setState(state === verdict.bucket ? "" : (verdict.bucket ?? ""))
                    }}
                    className="rounded-md px-1.5 py-1 focus-ring transition-colors hover:bg-row-hover"
                  >
                    <Status tone={verdict.tone} label={verdict.label} />
                  </button>
                ) : (
                  <span className="px-1.5 py-1">
                    <Status
                      tone={verdict.tone}
                      live={verdict.tone === "running" && containers !== undefined}
                      label={verdict.label}
                    />
                  </span>
                )
              }
            />
          )}
        </div>

        {query.get("remedy") && (
          <Notice title="Review the owning service">
            <p>
              Change only the affected service, validate and save the file, then bring the stack up
              to apply it. Replacing a container discards its writable layer and old logs; retained
              volumes keep their data.
            </p>
            <pre className="mt-2 font-mono text-hint whitespace-pre-wrap">
              {composeRemedy(query.get("remedy") ?? "")}
            </pre>
          </Notice>
        )}
        {error && <ErrorState error={error} />}
        {loading && !data && <LoadingRows />}

        {data && (
          <>
            {!data.managed && (
              <Notice title="Read-only here" icon={Warning}>
                No compose file for this stack is reachable from the dashboard, so it can be watched
                but not acted on. Compose records the project directory on the containers it creates
                — if the stack was started elsewhere, or its directory has moved, that record no
                longer points at anything.
              </Notice>
            )}
            {data.managed && !can("system.admin") && (
              <Notice title="Administrator access required">
                An administrator must create, edit, validate, or run Compose stacks. You can inspect
                this stack here.
              </Notice>
            )}
            {data.declaredError && (
              <Notice title="This stack's compose file does not parse" icon={Warning} tone="danger">
                {data.declaredError}
              </Notice>
            )}
            <RunConsole
              lines={runner.lines}
              state={runner.state}
              exitCode={runner.exitCode}
              title={`compose · ${name}`}
              onDismiss={runner.reset}
            />

            <Tabs value={tab} onValueChange={setTab} className="flex min-h-0 flex-1 flex-col gap-0">
              {/* The same underlined strip every switcher between views of one
                  page wears (§8): the brand underline says where you are and
                  the label stays ink. It was a filled tab list, a control with a
                  face on a page that had stopped drawing boxes. */}
              <TabsPrimitive.List
                aria-label="Stack views"
                className="flex shrink-0 [scrollbar-width:none] gap-1 overflow-x-auto border-b border-hairline [&::-webkit-scrollbar]:hidden"
              >
                {tabs.map((entry) => (
                  <TabsPrimitive.Trigger
                    key={entry.value}
                    value={entry.value}
                    className={tabClasses(tab === entry.value, "h-10")}
                  >
                    {entry.label}
                    {entry.count !== undefined && (
                      <span className="numeric text-hint font-medium text-muted-foreground">
                        {entry.count}
                      </span>
                    )}
                  </TabsPrimitive.Trigger>
                ))}
              </TabsPrimitive.List>
              <TabsContent value="services" className="min-h-0 flex-1 overflow-y-auto pt-4">
                {data.services.length === 0 ? (
                  <EmptyState
                    icon={Box}
                    title="Nothing running"
                    description="This stack has a compose file but no containers. Bring it up to start them."
                  />
                ) : (
                  <div className="flex min-w-0 flex-col gap-8 pb-6">
                    <ServicesPanel
                      readings={visible}
                      all={readings.length}
                      counts={counts}
                      state={state}
                      setState={setState}
                      pending={pending}
                      verbsFor={serviceVerbs}
                      onCreate={
                        control && can("service.control")
                          ? (reading) =>
                              void run("up", { service: reading.key }).catch((err) =>
                                notify.error(String(err)),
                              )
                          : undefined
                      }
                      onOpen={openService}
                      busy={busy}
                    />
                    {data.deployed && (
                      <StackUsageBand
                        readings={readings}
                        changes={changes}
                        snapshot={snapshot}
                        eventsRead={feed.data !== undefined || feed.error !== undefined}
                        productOf={productOf}
                        onOpen={openService}
                      />
                    )}
                    {data.deployed && (
                      /* A plain head over the picture: it is the last block,
                         under a band of headed readings, and without one it
                         read as part of Recent. */
                      <Panel plain aria-label="Ports and networks">
                        <PanelHeader
                          title="Ports and networks"
                          actions={
                            <span className="numeric flex h-7 items-center text-hint text-muted-foreground">
                              {plural(ports, "port")} published · {plural(networks, "network")}
                            </span>
                          }
                        />
                        <PanelBody className="pt-2">
                          <StackMap
                            stack={name}
                            readings={readings}
                            networksRead={containers !== undefined}
                            productOf={productOf}
                            onOpen={openService}
                          />
                        </PanelBody>
                      </Panel>
                    )}
                  </div>
                )}
              </TabsContent>
              <TabsContent value="preview" className="min-h-0 flex-1 overflow-y-auto pt-4">
                {tab === "preview" && <DeployPreviewPanel stack={data.name} />}
              </TabsContent>
              <TabsContent value="compose" className="min-h-0 flex-1 pt-4">
                <ComposeEditor
                  stack={data}
                  onSaved={reload}
                  canWrite={can("system.admin") && can("file.write")}
                  canValidate={can("system.admin")}
                />
              </TabsContent>
              {data.workingDir && (
                <TabsContent value="files" className="min-h-0 flex-1 overflow-y-auto pt-4">
                  {tab === "files" && (
                    <FileBrowser
                      root={data.workingDir}
                      label={data.name}
                      emptyNote="This stack's directory is empty."
                    />
                  )}
                </TabsContent>
              )}
              <TabsContent value="history" className="min-h-0 flex-1 overflow-y-auto pt-4">
                {tab === "history" && <DeploymentHistoryPanel stack={data.name} />}
              </TabsContent>
              <TabsContent value="logs" className="min-h-0 flex-1 overflow-y-auto pt-4">
                {tab === "logs" && <StackLogs stack={data} />}
              </TabsContent>
            </Tabs>
          </>
        )}
        {dialog}
      </Page>
    </Workspace>
  )
}

/**
 * The stack as what it is made of: its services' products overlapping, the
 * tile's size the identity line's, or Compose's own mark when none has one.
 */
function StackMark({ stack, containers }: { stack: StackDetail; containers?: Container[] }) {
  const ids =
    containers && containers.length > 0
      ? containers.map(containerProduct)
      : imageProducts(stack.services.map((s) => s.image).filter(Boolean))
  const products = [...new Set(ids)].filter((id) => id !== "docker")
  if (products.length > 1) return <ProductLogos ids={products} size="md" />
  return (
    <ProductLogo
      id={products[0] ?? "docker-compose"}
      className="size-12 rounded-xl [&_img]:size-7"
    />
  )
}

/**
 * What the stack is, as facts: the compose file it is read from, the
 * directory and the checkout it is (the git line is the load-bearing part —
 * uncommitted changes mean compose will deploy something that is in no
 * commit, and "behind" means a pull would change what deploying does), and
 * how much of it runs and how much of it is reachable.
 */
function StackFacts({
  data,
  running,
  ports,
}: {
  data: StackDetail
  running: number
  ports: number
}) {
  const file = data.configPath?.split("/").pop()
  return (
    <>
      <HostFact product="docker-compose">{file ?? "Compose"}</HostFact>
      {data.workingDir && (
        <>
          <FactDot />
          <span className="truncate font-mono">{data.workingDir}</span>
        </>
      )}
      {data.git && (
        <>
          <FactDot />
          <Link
            href={`/git?repo=${encodeURIComponent(data.git.path)}`}
            title={data.git.subject}
            className="inline-flex min-w-0 items-center gap-1.5 rounded-sm focus-ring hover:text-foreground"
          >
            <HostFact product="git">{data.git.branch ?? "repository"}</HostFact>
            {data.git.dirty && (
              <span className="numeric" style={{ color: "var(--git-modified)" }}>
                {data.git.changes} uncommitted
              </span>
            )}
            {data.git.behind > 0 && <span className="numeric">{data.git.behind} behind</span>}
          </Link>
        </>
      )}
      <FactDot />
      <span className="numeric">
        {running} of {plural(data.total || data.services.length, "service")} running
      </span>
      {ports > 0 && (
        <>
          <FactDot />
          <span className="numeric">{plural(ports, "port")} published</span>
        </>
      )}
    </>
  )
}

/**
 * The stack's services, as the one framed block on the page: a table owns
 * its scrolling, and the edge is what says so (§2). The chips count and
 * narrow; the toned ones are drawn only while something is in them.
 */
function ServicesPanel({
  readings,
  all,
  counts,
  state,
  setState,
  busy,
  ...rows
}: {
  readings: ServiceReading[]
  all: number
  counts: Record<ServiceBucket, number>
  state: ServiceBucket | ""
  setState: (next: ServiceBucket | "") => void
  busy: boolean
} & Pick<
  React.ComponentProps<typeof ServiceRows>,
  "pending" | "verbsFor" | "onCreate" | "onOpen"
>) {
  const wide = useMediaQuery("(min-width: 1280px)")
  const roomy = useMediaQuery("(min-width: 1536px)")
  return (
    <Panel aria-label="Services">
      <PanelHeader
        title={
          <>
            Services
            <span className="numeric ml-2 text-body font-normal text-muted-foreground">{all}</span>
          </>
        }
      >
        <ChipStrip aria-label="State" className="mr-auto">
          {STATES.map(({ value, label, tone }) => {
            const count = counts[value]
            if (count === 0 && state !== value && (tone || value === "paused")) return null
            return (
              <FilterChip
                key={value}
                selected={state === value}
                onClick={() => setState(state === value ? "" : value)}
              >
                <span
                  aria-hidden
                  className={cn(
                    "size-1.5 rounded-full",
                    bucketTone(value) === "danger"
                      ? "bg-destructive"
                      : bucketTone(value) === "warning"
                        ? "bg-warning"
                        : value === "running"
                          ? "bg-success"
                          : "bg-muted-foreground/50",
                  )}
                />
                {label}
                <ChipCount
                  className={cn(
                    tone === "danger" && "text-destructive opacity-100",
                    tone === "warning" && "text-warning opacity-100",
                  )}
                >
                  {count}
                </ChipCount>
              </FilterChip>
            )
          })}
        </ChipStrip>
        {state && (
          <FilterChip
            selected
            aria-label="Show services in every state"
            onClick={() => setState("")}
          >
            {STATES.find((s) => s.value === state)?.label}
            <Cross aria-hidden className="size-3" />
          </FilterChip>
        )}
      </PanelHeader>
      <PanelBody flush>
        {readings.length === 0 ? (
          <EmptyState
            icon={Box}
            title="No service is in that state"
            description="Clear the filter to see every service in the stack."
            className="my-4"
          />
        ) : (
          <ServiceRows rows={readings} wide={wide} roomy={roomy} {...rows} />
        )}
      </PanelBody>
      <PanelFooter className="text-hint text-muted-foreground">
        <span>failing first, then by name</span>
        {busy && (
          <>
            <span className="text-muted-foreground/40">·</span>
            <span>the verbs wait for the command above to finish</span>
          </>
        )}
      </PanelFooter>
    </Panel>
  )
}

function StackActions({
  data,
  run,
  confirmRun,
  busy,
}: {
  data: StackDetail
  run: (action: ComposeActionKey) => Promise<void>
  confirmRun: (action: ComposeActionKey, title: string, description: React.ReactNode) => void
  busy: boolean
}) {
  const { can } = useAuth()
  const router = useRouter()
  if (!data.managed || !can("system.admin")) return null
  const quiet = (fn: () => Promise<void>) => () => {
    fn().catch((err) => notify.error(String(err)))
  }

  /**
   * Compose's verbs, named for what they do to the server.
   *
   * `Up` and `Down` are precise and mean nothing without the compose reference
   * — and `Down` is the worst of the two, because it sounds like the opposite
   * of `Up` and is not: it deletes the containers and the project network. Two
   * are pressed often enough to sit inline; the rest are behind one menu, one
   * word to a line. Every confirmation still carries both
   * the blast radius and the exact command being run, so an operator who knows
   * compose can check the translation.
   */
  const act = (action: ComposeActionKey, extra?: React.ReactNode) => {
    const meta = COMPOSE_ACTIONS[action]
    confirmRun(
      action,
      meta.label,
      <>
        <p>
          <b>{data.name}</b> — {meta.blastRadius}
        </p>
        {extra}
        <p className="font-mono text-hint text-muted-foreground">{meta.command}</p>
      </>,
    )
  }

  const verbs: Verb[] = []
  if (can("destructive")) {
    verbs.push({
      key: "update",
      label: COMPOSE_ACTIONS.update.label,
      icon: ArrowCircleUp,
      disabled: busy,
      run: () => act("update"),
    })
  }
  if (can("service.control")) {
    verbs.push({
      key: "build",
      label: COMPOSE_ACTIONS.build.label,
      icon: Wrench,
      disabled: busy,
      run: () => act("build"),
    })
  }
  if (can("terminal") && data.workingDir) {
    verbs.push({
      key: "shell",
      label: "Open a shell here",
      icon: Terminal,
      run: () => router.push(`/terminal?cwd=${encodeURIComponent(data.workingDir)}`),
    })
  }
  if (can("destructive")) {
    verbs.push({
      key: "down",
      label: COMPOSE_ACTIONS.down.label,
      icon: StopCircle,
      danger: true,
      disabled: busy,
      run: () =>
        act(
          "down",
          <p>
            Deploying afterwards brings the stack back from the same compose file, and the volumes
            it left behind are still there for it.
          </p>,
        ),
    })
  }

  return (
    <>
      {can("service.control") && (
        /* Deploy is not destructive: it starts what is missing and replaces
           what changed. It gets no confirmation for the same reason it is the
           primary button. */
        <Button size="sm" onClick={quiet(() => run("up"))} pending={busy}>
          <Play className="size-3.5" />
          {COMPOSE_ACTIONS.up.label}
        </Button>
      )}
      {can("destructive") && (
        <Button size="sm" variant="outline" disabled={busy} onClick={() => act("restart")}>
          <RotateClockwise className="size-3.5" />
          {COMPOSE_ACTIONS.restart.label}
        </Button>
      )}
      {verbs.length > 0 && <VerbMenu verbs={verbs} label={`More actions for ${data.name}`} />}
    </>
  )
}

/* ---------------------------------------------------------------- editor -- */

function ComposeEditor({
  stack,
  onSaved,
  canWrite,
  canValidate,
}: {
  stack: StackDetail
  onSaved: () => void
  canWrite: boolean
  canValidate: boolean
}) {
  const [content, setContent] = useState<string>()
  const [original, setOriginal] = useState("")
  const [validation, setValidation] = useState<ComposeValidation>()
  const [busy, setBusy] = useState(false)
  const [fetchError, setFetchError] = useState<string>()
  // "There is no file" is a fact about the stack, not something that happens
  // while loading, so it is derived rather than written into state by the
  // effect below.
  const loadError = stack.configPath
    ? fetchError
    : "This stack has no compose file the dashboard can read."

  useEffect(() => {
    if (!stack.configPath) return
    const controller = new AbortController()
    get<{ path: string; content: string }>(
      `/docker/stacks/${encodeURIComponent(stack.name)}/config`,
      undefined,
      controller.signal,
    )
      .then((res) => {
        setContent(res.content)
        setOriginal(res.content)
      })
      .catch((err) => !controller.signal.aborted && setFetchError(String(err)))
    return () => controller.abort()
  }, [stack.name, stack.configPath])

  const dirty = content !== undefined && content !== original

  const check = async () => {
    setBusy(true)
    try {
      const res = await post<ComposeValidation>(
        `/docker/stacks/${encodeURIComponent(stack.name)}/validate`,
        { content },
      )
      setValidation(res)
      if (res.valid) notify.success(`Valid — ${res.services.length} service(s)`)
    } catch (err) {
      notify.error("Could not check the file", err)
    } finally {
      setBusy(false)
    }
  }

  const save = async (force = false) => {
    setBusy(true)
    try {
      const res = await put<{ validation: ComposeValidation }>(
        `/docker/stacks/${encodeURIComponent(stack.name)}/config`,
        { content, force },
      )
      setOriginal(content ?? "")
      setValidation(res.validation)
      // Saying so explicitly, because this is the one thing an editor in a
      // deployment tool is most likely to be misread about.
      notify.success("Saved", {
        description: "Nothing changed yet — bring the stack up to apply it.",
      })
      onSaved()
    } catch (err) {
      if (err instanceof ApiError && err.code === "compose_invalid") {
        setValidation({ valid: false, error: err.message, services: [] })
        notify.error("Compose rejected this file", err.message)
      } else {
        notify.error("Could not save", err)
      }
    } finally {
      setBusy(false)
    }
  }

  if (loadError) return <ErrorState error={new Error(loadError)} />
  if (content === undefined) return <LoadingRows />

  return (
    <div className="flex h-full min-h-0 flex-col gap-2">
      <div className="flex flex-wrap items-center gap-2">
        <Code className="size-3.5 text-muted-foreground" />
        <span className="min-w-0 flex-1 truncate font-mono text-hint text-muted-foreground">
          {stack.configPath}
        </span>
        {dirty && <Tag tone="warning">unsaved</Tag>}
        {canValidate && (
          <Button size="xs" variant="outline" onClick={check} disabled={busy}>
            Check
          </Button>
        )}
        {canWrite && (
          <Button size="xs" onClick={() => save()} disabled={busy || !dirty} pending={busy}>
            <FloppyDisk className="size-3" />
            Save
          </Button>
        )}
      </div>

      <CodeEditor
        value={content}
        onChange={setContent}
        language="yaml"
        readOnly={!canWrite}
        className="min-h-0 flex-1 overflow-hidden rounded-lg border"
      />

      {validation && !validation.valid && (
        <Notice title="Compose will not accept this" icon={Warning} tone="danger">
          <pre className="mt-1 font-mono text-hint whitespace-pre-wrap">{validation.error}</pre>
          {canWrite && (
            <Button size="xs" variant="outline" className="mt-2" onClick={() => save(true)}>
              Save it anyway
            </Button>
          )}
        </Notice>
      )}
      {validation?.valid && (
        <Hint>
          Valid. Defines {validation.services.join(", ") || "no services"}.{" "}
          <Term name="compose">Saving does not deploy</Term> — bring the stack up to apply it.
        </Hint>
      )}
    </div>
  )
}

/* ------------------------------------------------------------------ logs -- */

/**
 * Every container in the stack, as one log.
 *
 * It was a socket of its own that followed the running services only and
 * wrote `db | ` in front of each line — so a crashed service's last words
 * were never in it, a JSON line stopped being JSON, and the only way to read
 * one service was to type its name into the filter. The stack is a log
 * source now (`stack:<project>`): every container's output merged by time,
 * the exited ones included, each line read through its own container's lens
 * and carrying its service as the lane down the left and as a field to
 * narrow by. Events is what Docker did to them, beside it.
 */
function StackLogs({ stack }: { stack: StackDetail }) {
  // As one string: the stack's poll hands a new array every ten seconds.
  const images = stack.services
    .map((service) => service.image)
    .filter(Boolean)
    .join(",")
  const running = stack.services.some((service) => service.state === "running")
  const sources = useMemo<ServiceLogSource[]>(
    () => [
      {
        id: stackSource(stack.name),
        label: stack.name,
        kind: "stack",
        status: running ? "running" : "exited",
        images: images ? images.split(",") : undefined,
        product: "docker-compose",
      },
    ],
    [stack.name, running, images],
  )
  const views = useMemo(() => [containerEventsView({ stack: stack.name })], [stack.name])
  return (
    <ServiceLogs
      sources={sources}
      storageKey={`docker.stack.${stack.name}.logs`}
      views={views}
      readings
      className="h-full min-h-0"
      paneClassName="min-h-[30rem]"
    />
  )
}
