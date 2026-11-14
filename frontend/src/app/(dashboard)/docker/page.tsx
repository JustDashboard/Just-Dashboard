"use client"

import { useCallback, useMemo, useState } from "react"
import Link from "next/link"
import { useRouter } from "next/navigation"
import { ArrowRight, Box, Clipboard, Layers, Sparkles } from "@/components/icons"
import { get } from "@/lib/api"
import { bytes, plural } from "@/lib/format"
import type {
  ComposeStack,
  Container,
  ContainerStats,
  DockerDiskUsage,
  DockerEvent,
  DockerEventFeed,
  DockerInfo,
  DockerNetwork,
  VolumeDetail,
} from "@/lib/types"
import { useSessionState } from "@/lib/view-state"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { useMetrics } from "@/hooks/use-metrics"
import { useSocket, type Envelope } from "@/hooks/use-socket"
import { useConfirm } from "@/components/confirm-dialog"
import { useNow } from "@/components/deploy/vocabulary"
import { ChoiceList } from "@/components/flow"
import { FactDot } from "@/components/metrics/host-identity"
import { Page, PageContext } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Row, RowList } from "@/components/row-list"
import { Status } from "@/components/status-dot"
import { EmptyState, ErrorState } from "@/components/state"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { Workspace } from "@/components/workspace/workspace"
import { CleanupPanel } from "@/components/docker/cleanup"
import { ContainerBand } from "@/components/docker/container-band"
import { DiskSummary } from "@/components/docker/disk-panel"
import { DockerIdentity, VerdictButton } from "@/components/docker/docker-identity"
import { EnginePicture } from "@/components/docker/engine-picture"
import { ExplainIcon } from "@/components/docker/explain"
import { StackChoiceRow } from "@/components/docker/stack-row"
import { stackLines } from "@/components/docker/stack-readings"
import { containerBucket, oomKilledIds, recentEntries } from "@/components/docker/containers"
import { mergeEvents, recentChanges } from "@/components/docker/overview"
import { Button } from "@/components/ui/button"

/**
 * Is Docker okay right now, and what is it made of?
 *
 * The daemon first, as the line every Docker page opens on
 * (`DockerIdentity`): Docker drawn as itself with its version, the verdict
 * beside its name, the host it runs on, the storage driver, how many
 * containers are running, how many compose projects are up and how many
 * images it holds.
 *
 * Then `ContainerBand`: the containers using the most processor and memory as
 * spans of one bar the size of the machine, and the last thing that happened
 * to each container, live off the daemon's events.
 *
 * Then the engine as a picture (`EnginePicture`): the compose projects and
 * the containers started by hand on one side of Docker, the networks and
 * volumes it keeps on the other, the failing ones lit. It replaced a table of
 * every container — the one `/docker/containers` draws, a press away from the
 * section's title and from the verdict, which opens that table narrowed to
 * what is failing. A second copy of the table answered "what is each
 * container using", which the band above and that page already do; the
 * picture answers what the engine holds and where the trouble is.
 *
 * Last, the compose projects as cards that open them, beside what Docker
 * keeps on disk (or the cleanup, once there is a gigabyte worth reclaiming).
 *
 * The page has its shape before anything is read: the line, the band and the
 * picture are drawn waiting, so nothing moves when the answers land.
 */
export default function DockerOverviewPage() {
  const { can } = useAuth()
  const router = useRouter()
  const { confirm, dialog } = useConfirm()
  const { host, snapshot } = useMetrics()
  const now = useNow(60_000)
  // The Containers page's own state chip, so the verdict opens that table
  // already narrowed to what is failing.
  const [, setContainersState] = useSessionState<string>("docker.containers.state.075", "")

  // The list arrives once over REST, so the page draws without waiting for
  // the socket; the socket then carries every change and every reading.
  const list = usePoll<Container[]>(
    (signal) => get<Container[]>("/docker/containers/", undefined, signal),
    0,
  )
  const [streamed, setStreamed] = useState<Container[]>()
  const [stats, setStats] = useState<Record<string, ContainerStats>>({})
  const [socketError, setSocketError] = useState<string>()
  const onContainers = useCallback((envelope: Envelope) => {
    if (envelope.type === "containers") {
      setStreamed(envelope.data as Container[])
      setSocketError(undefined)
    } else if (envelope.type === "stats") {
      const rows = envelope.data as ContainerStats[]
      setStats(Object.fromEntries(rows.map((r) => [r.id, r])))
    } else if (envelope.type === "error") {
      setSocketError(envelope.error)
    }
  }, [])
  useSocket("/docker/containers/stream", { onMessage: onContainers })

  const [live, setLive] = useState<DockerEvent[]>([])
  const onEvents = useCallback((envelope: Envelope) => {
    if (envelope.type !== "events") return
    setLive((previous) => mergeEvents(previous, envelope.data as DockerEvent[]))
  }, [])
  useSocket("/docker/events/stream", { onMessage: onEvents, query: { kinds: "container" } })
  const feed = usePoll<DockerEventFeed>(
    (signal) => get<DockerEventFeed>("/docker/events", { kinds: "container", limit: 200 }, signal),
    // Once: the socket carries everything after, and replays its buffer on
    // every reconnect.
    0,
  )

  const stacks = usePoll<ComposeStack[]>(
    (signal) => get<ComposeStack[]>("/docker/stacks/", undefined, signal),
    60_000,
  )
  // What the picture's other side is made of. Read rarely: a network or a
  // volume appears with a deploy, and the containers socket says when one
  // of those has happened.
  const networks = usePoll<DockerNetwork[]>(
    (signal) => get<DockerNetwork[]>("/docker/networks/", undefined, signal),
    60_000,
  )
  const volumes = usePoll<VolumeDetail[]>(
    (signal) => get<VolumeDetail[]>("/docker/volumes/", undefined, signal),
    60_000,
  )
  const disk = usePoll<DockerDiskUsage>(
    (signal) => get<DockerDiskUsage>("/docker/disk-usage", undefined, signal),
    120_000,
  )
  const info = usePoll<DockerInfo>(
    (signal) => get<DockerInfo>("/docker/info", undefined, signal),
    300_000,
  )

  const read = streamed !== undefined || list.data !== undefined
  const containers = useMemo(() => streamed ?? list.data ?? [], [streamed, list.data])
  const detected = useMemo(() => stacks.data ?? [], [stacks.data])
  const lines = useMemo(
    () => stackLines(detected, containers, stats),
    [detected, containers, stats],
  )
  const events = useMemo(() => mergeEvents(feed.data?.events ?? [], live), [feed.data, live])
  const changes = useMemo(() => recentChanges(events, now), [events, now])
  // Which containers are failing is the Containers page's reading, with its
  // event log telling a kill for memory from a stop: the verdict opens that
  // page's table, and the two must count the same containers.
  const oomKilled = useMemo(() => oomKilledIds(recentEntries(events)), [events])
  const buckets = useMemo(
    () => containers.map((c) => containerBucket(c, oomKilled.has(c.id))),
    [containers, oomKilled],
  )

  const open = useCallback(
    (id: string) => router.push(`/docker/containers/${encodeURIComponent(id)}`),
    [router],
  )
  // A change names a container by the name a redeploy keeps; the id it had
  // may be gone, and Docker resolves the name to whichever has it now.
  const openChange = useCallback(
    (target: { id?: string; name: string }) =>
      open(containers.find((c) => c.name === target.name)?.id ?? target.name),
    [containers, open],
  )
  const openFailing = useCallback(() => {
    setContainersState("failing")
    router.push("/docker/containers")
  }, [router, setContainersState])

  const header = <PageContext title="Docker" />

  if (!read && list.error) {
    return (
      <Page>
        {header}
        <ErrorState error={list.error} onRetry={list.refresh} />
      </Page>
    )
  }

  const failing = buckets.filter((bucket) => bucket === "failing").length
  const starting = buckets.filter((bucket) => bucket === "starting").length
  const running = containers.filter((c) => c.state === "running").length
  const active = detected.filter((s) => s.deployed)
  const reclaimable = disk.data
    ? disk.data.images.reclaimable + disk.data.buildCache.reclaimable
    : 0
  const version = info.data?.ServerVersion
  const settled = (poll: { data?: unknown; error?: unknown }) =>
    poll.data !== undefined || poll.error !== undefined

  // A server with nothing on it is not an error state, and it is the one
  // moment where the page should be teaching rather than reporting.
  if (read && containers.length === 0 && detected.length === 0 && stacks.data) {
    return (
      <Page className="animate-rise">
        {header}
        <FirstRun canDeploy={can("service.control")} />
        {dialog}
      </Page>
    )
  }

  return (
    <Workspace
      name="Docker"
      refresh={() => {
        list.refresh()
        stacks.refresh()
        networks.refresh()
        volumes.refresh()
        disk.refresh()
        feed.refresh()
      }}
      commands={[
        {
          id: "failing",
          label: "Show failing containers",
          disabled: failing === 0,
          run: openFailing,
        },
      ]}
    >
      <Page>
        {header}

        <DockerIdentity
          version={version}
          host={{ hostname: host?.hostname || info.data?.Name, platform: host?.platform }}
          state={
            !read ? undefined : failing > 0 ? (
              <VerdictButton onPress={openFailing}>
                <Status tone="danger" label={`${plural(failing, "container")} failing`} />
              </VerdictButton>
            ) : starting > 0 ? (
              <Status tone="warning" label={`${plural(starting, "container")} starting`} />
            ) : (
              <Status tone="running" label="Nothing failing" />
            )
          }
          facts={
            !read ? (
              <TextShimmer>Reading the containers…</TextShimmer>
            ) : (
              <>
                {info.data?.Driver && (
                  <span className="contents max-sm:hidden">
                    <span>
                      {info.data.Driver}
                      {info.data.CgroupVersion && ` on cgroup v${info.data.CgroupVersion}`}
                    </span>
                    <FactDot />
                  </span>
                )}
                <span className="numeric">
                  {running} of {plural(containers.length, "container")} running
                </span>
                {detected.length > 0 && (
                  <>
                    <FactDot />
                    <span className="numeric">
                      {active.length} of {plural(detected.length, "project")} up
                    </span>
                  </>
                )}
                {info.data?.Images !== undefined && (
                  <span className="contents max-sm:hidden">
                    <FactDot />
                    <span className="numeric">
                      {plural(info.data.Images, "image")}
                      {disk.data && ` · ${bytes(disk.data.layersSize)}`}
                    </span>
                  </span>
                )}
              </>
            )
          }
          command={
            can("service.control") && (
              <Button size="sm" asChild>
                <Link href="/deploy">Open Deployments</Link>
              </Button>
            )
          }
        />

        <ContainerBand
          containers={containers}
          stats={stats}
          snapshot={snapshot}
          hostCpus={info.data?.NCPU ?? 0}
          hostMemory={info.data?.MemTotal ?? 0}
          changes={changes}
          listening={feed.data?.listening ?? true}
          loaded={read}
          eventsRead={settled(feed) || live.length > 0}
          onOpen={openChange}
        />

        {socketError && <ErrorState error={new Error(socketError)} />}

        {/* Plain: a picture is a reading, and the frame is for the table it
            replaced (§2). The title keeps the count and the way to that
            table. */}
        <Panel plain aria-label="What the engine holds">
          <PanelHeader
            title={
              <span className="inline-flex items-center gap-1.5">
                Containers
                {read && (
                  <span className="numeric text-body font-normal text-muted-foreground">
                    {containers.length}
                  </span>
                )}
                <ExplainIcon name="container" />
              </span>
            }
            actions={
              <Link
                href="/docker/containers"
                className="flex items-center gap-1 rounded-md text-hint font-medium text-muted-foreground focus-ring hover:text-foreground"
              >
                All containers <ArrowRight className="size-3" />
              </Link>
            }
          />
          <PanelBody className="pt-8 pb-0">
            <EnginePicture
              containers={containers}
              stacks={detected}
              networks={networks.data}
              volumes={volumes.data}
              oomKilled={oomKilled}
              version={version}
              loading={!read || !settled(stacks) || !settled(networks) || !settled(volumes)}
            />
          </PanelBody>
        </Panel>

        <div className="grid min-w-0 items-start gap-x-10 gap-y-8 lg:grid-cols-2">
          <Panel plain>
            <PanelHeader
              title={
                <span className="inline-flex items-center gap-1.5">
                  Compose projects
                  {stacks.data && (
                    <span className="numeric text-body font-normal text-muted-foreground">
                      {active.length} of {detected.length} up
                    </span>
                  )}
                  <ExplainIcon name="compose" />
                </span>
              }
              actions={
                <Link
                  href="/docker/stacks"
                  className="flex items-center gap-1 rounded-md text-hint font-medium text-muted-foreground focus-ring hover:text-foreground"
                >
                  Manage <ArrowRight className="size-3" />
                </Link>
              }
            />
            <PanelBody flush>
              {!stacks.data && stacks.error ? (
                <ErrorState error={stacks.error} onRetry={stacks.refresh} className="mt-3" />
              ) : !stacks.data ? (
                // The cards' own height, so the list lands where it waited.
                <ul aria-hidden className="grid gap-2 pt-3">
                  {[0, 1, 2].map((row) => (
                    <li key={row} className="h-14 animate-pulse rounded-xl bg-muted/40" />
                  ))}
                </ul>
              ) : detected.length === 0 ? (
                <EmptyState
                  icon={Layers}
                  title="No compose stacks"
                  description="A stack is a directory with a compose file in it. Containers started by hand are in the picture above."
                />
              ) : (
                <ChoiceList aria-label="Compose projects" className="animate-rise pt-3">
                  {lines.map((line) => (
                    <StackChoiceRow key={line.stack.name} line={line} figures={false} />
                  ))}
                </ChoiceList>
              )}
            </PanelBody>
          </Panel>

          {/*
            Cleanup appears only when there is something worth reclaiming. A
            panel offering to free 40 MB is a panel that trains people to
            ignore it.
          */}
          {can("destructive") && reclaimable > 1024 * 1024 * 1024 ? (
            <CleanupPanel
              confirm={confirm}
              onDone={() => {
                disk.refresh()
                list.refresh()
              }}
            />
          ) : (
            disk.data && (
              // Plain, like everything around it: a bar and a legend are a reading.
              <Panel plain className="animate-rise">
                <PanelHeader
                  title="Disk"
                  actions={
                    <Link
                      href="/docker/images"
                      className="flex items-center gap-1 rounded-md text-hint font-medium text-muted-foreground focus-ring hover:text-foreground"
                    >
                      Images <ArrowRight className="size-3" />
                    </Link>
                  }
                />
                <PanelBody className="space-y-3">
                  <DiskSummary usage={disk.data} />
                  <p className="text-hint text-muted-foreground">
                    {reclaimable > 0
                      ? `Only ${bytes(reclaimable)} of that could be reclaimed — not worth a sweep yet.`
                      : "Effectively all of it is in use by something running."}
                  </p>
                </PanelBody>
              </Panel>
            )
          )}
        </div>

        {dialog}
      </Page>
    </Workspace>
  )
}

/**
 * The first five minutes.
 *
 * A fresh server shows an empty table and an empty compose list, which is an
 * accurate and completely unhelpful description of a machine somebody has
 * just decided to put something on. The three routes are the same three the
 * create panel opens with, stated here because this is where the question is
 * actually asked.
 */
function FirstRun({ canDeploy }: { canDeploy: boolean }) {
  return (
    <Panel plain className="animate-rise">
      <PanelHeader
        title="Nothing is running on this server yet"
        actions={
          canDeploy && (
            <Button size="sm" asChild>
              <Link href="/deploy">Open Deployments</Link>
            </Button>
          )
        }
      />
      <PanelBody className="space-y-4">
        <p className="max-w-prose text-body leading-relaxed text-muted-foreground">
          A <b className="font-medium text-foreground">container</b> is one application packaged
          with everything it needs to run — a database, a web server, a photo library. It cannot
          disturb anything else on this machine, and removing it leaves nothing behind.
        </p>
        <RowList>
          <Route
            icon={Sparkles}
            title="Start from a template"
            detail="Postgres, Nginx, Uptime Kuma — filled in with the ports and storage each actually needs."
          />
          <Route
            icon={Clipboard}
            title="Paste a Docker command"
            detail="A docker run line from a README becomes a form you can read before anything runs."
          />
          <Route
            icon={Box}
            title="Start custom"
            detail="An empty form, with every field explained beside it."
          />
        </RowList>
      </PanelBody>
    </Panel>
  )
}

/** One way in. The glyph is wayfinding: it is the mark the Deploy page draws on the same route. */
function Route({
  icon: Icon,
  title,
  detail,
}: {
  icon: React.ComponentType<{ className?: string }>
  title: string
  detail: string
}) {
  return (
    <Row
      leading={<Icon className="size-3.5 text-brand" />}
      title={title}
      subtitle={detail}
      className="py-2.5"
    />
  )
}
