"use client"

import { useCallback, useMemo, useState } from "react"
import Link from "next/link"
import { useRouter } from "next/navigation"
import { ArrowRight, Box, Clipboard, Cross, Layers, Sparkles } from "@/components/icons"
import { get } from "@/lib/api"
import { bytes, plural } from "@/lib/format"
import type {
  ComposeStack,
  Container,
  ContainerSparkline,
  ContainerStats,
  DockerDiagnosis,
  DockerDiskUsage,
  DockerEvent,
  DockerEventFeed,
  DockerInfo,
} from "@/lib/types"
import { cn } from "@/lib/utils"
import { useSessionState } from "@/lib/view-state"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { useMetrics } from "@/hooks/use-metrics"
import { useSocket, type Envelope } from "@/hooks/use-socket"
import { useConfirm } from "@/components/confirm-dialog"
import { useNow } from "@/components/deploy/vocabulary"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { FactDot, HostFact, HostIdentity } from "@/components/metrics/host-identity"
import { ProductLogos, imageProducts, platformProduct } from "@/components/product-logo"
import { Page, PageContext, PageState, SearchInput } from "@/components/page"
import { Panel, PanelBody, PanelFooter, PanelHeader, PanelToolbar } from "@/components/panel"
import { Row, RowList } from "@/components/row-list"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { Status } from "@/components/status-dot"
import { EmptyState, ErrorState } from "@/components/state"
import { Workspace, WorkspaceHelp } from "@/components/workspace/workspace"
import { useDockerFindingActions } from "@/components/docker/finding-actions"
import { AttentionPanel } from "@/components/docker/attention"
import { CleanupPanel } from "@/components/docker/cleanup"
import { ContainerBand } from "@/components/docker/container-band"
import { ContainerRows, ProjectKey } from "@/components/docker/container-table"
import { DiskSummary } from "@/components/docker/disk-panel"
import { ExplainIcon } from "@/components/docker/explain"
import { StackStateBadge } from "@/components/docker/stack-state"
import { useContainerControl } from "@/components/docker/container-actions"
import {
  BUCKET_ORDER,
  containerBucket,
  crashed,
  mergeEvents,
  overviewOrder,
  recentChanges,
  type ContainerBucket,
} from "@/components/docker/overview"
import { Button } from "@/components/ui/button"

/** The state chips, in the order they are asked about; the toned two only while there is one. */
const STATES: { value: ContainerBucket; label: string; tone?: "danger" | "warning" }[] = [
  { value: "running", label: "Running" },
  { value: "failing", label: "Failing", tone: "danger" },
  { value: "starting", label: "Starting", tone: "warning" },
  { value: "stopped", label: "Stopped" },
]

const BUCKETS = new Set<string>(BUCKET_ORDER)

/** The project chip for containers started by hand rather than by compose. */
const STANDALONE = "\u0000standalone"

/**
 * Is Docker okay right now, and what is it doing?
 *
 * The daemon first, as the identity line Services and Live open on — Docker
 * drawn as itself, its version, the host it runs on, the storage driver, how
 * many containers are running and how many compose projects are up — with the
 * verdicts at its right end: the containers failing, which narrows the table
 * to them, and the issues Attention holds, which goes to it.
 *
 * Then `ContainerBand`: the containers using the most processor and memory as
 * spans of one bar the size of the machine, and the last thing that happened
 * to each container, live off the daemon's events.
 *
 * It replaced four tiles (§15 pass 2 names the exit), and each figure went
 * where it is said better:
 *
 *   **Running** is the identity line's fact and the table's state chips, which
 *   count *and* narrow — failing in its tone and first in the table, where the
 *   old page had a separate "not running" list above everything because the
 *   tile could only count them.
 *
 *   **Runtime health** is the verdict at the line's end, and each container's
 *   health is its State cell: up for how long, and whether a check passes,
 *   fails, or does not exist.
 *
 *   **Attention** is the second verdict, a link to the Attention list, which
 *   stayed as it was — posture is still never called health — and each
 *   container's issues are counted beside its name in the table.
 *
 *   **Compose stacks** is the identity line's "3 of 4 projects up" and the
 *   Compose projects head, where each project now draws its services as a
 *   strip of their states.
 *
 * Then every container as a table — framed, because it is one (§2) — read
 * from the containers socket so its figures move with every frame, each row a
 * container you open. New containers rise into place; a container changing
 * state changes in place. Disk and cleanup stay where they were, beside the
 * projects.
 */
export default function DockerOverviewPage() {
  const { can } = useAuth()
  const router = useRouter()
  const { confirm, dialog } = useConfirm()
  const { host, snapshot } = useMetrics()
  const now = useNow(60_000)

  const [query, setQuery] = useSessionState("docker.overview.query", "")
  const [rememberedState, setState] = useSessionState("docker.overview.state", "")
  const state = (BUCKETS.has(rememberedState) ? rememberedState : "") as ContainerBucket | ""
  const [project, setProject] = useSessionState("docker.overview.project", "")
  const [, rememberNavigation] = useSessionState<{ id: string; name: string }[]>(
    "docker.containers.navigation",
    [],
  )

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

  const trends = usePoll<ContainerSparkline[]>(
    (signal) =>
      get<ContainerSparkline[]>(
        "/docker/containers/stats/history",
        { range: "1h", points: 40 },
        signal,
      ),
    120_000,
  )
  const health = usePoll<DockerDiagnosis>(
    (signal) => get<DockerDiagnosis>("/docker/health", undefined, signal),
    60_000,
  )
  const runFix = useDockerFindingActions({ confirm, onChanged: health.refresh })
  const stacks = usePoll<ComposeStack[]>(
    (signal) => get<ComposeStack[]>("/docker/stacks/", undefined, signal),
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

  const containers = useMemo(() => streamed ?? list.data ?? [], [streamed, list.data])
  const detected = useMemo(() => stacks.data ?? [], [stacks.data])
  const trendByName = useMemo(
    () => new Map((trends.data ?? []).map((line) => [line.name, line])),
    [trends.data],
  )
  const changes = useMemo(
    () => recentChanges(mergeEvents(feed.data?.events ?? [], live), now),
    [feed.data, live, now],
  )

  const counts = useMemo(() => {
    const bucket: Record<ContainerBucket, number> = {
      failing: 0,
      starting: 0,
      running: 0,
      stopped: 0,
    }
    const projects = new Map<string, number>()
    for (const c of containers) {
      bucket[containerBucket(c)]++
      const key = c.composeStack || STANDALONE
      projects.set(key, (projects.get(key) ?? 0) + 1)
    }
    return { bucket, projects }
  }, [containers])

  const visible = useMemo(() => {
    const needle = query.trim().toLowerCase()
    return overviewOrder(
      containers.filter((c) => {
        if (state && containerBucket(c) !== state) return false
        if (project && (c.composeStack || STANDALONE) !== project) return false
        if (!needle) return true
        return (
          c.name.toLowerCase().includes(needle) ||
          c.image.toLowerCase().includes(needle) ||
          c.composeStack?.toLowerCase().includes(needle) === true
        )
      }),
    )
  }, [containers, query, state, project])

  const healthRefresh = health.refresh
  const { pending, act } = useContainerControl(healthRefresh)

  /** Goes to one container, with the table's order kept for its page's previous and next. */
  const open = useCallback(
    (id: string, tab?: string) => {
      rememberNavigation(
        Array.from(
          document.querySelectorAll<HTMLElement>(
            "[data-native-workspace='Docker'] [data-workspace-item]",
          ),
        )
          .slice(0, 500)
          .map((item) => ({
            id: item.dataset.workspaceItem!,
            name: item.dataset.workspaceName ?? "",
          })),
      )
      const suffix = tab ? `?tab=${encodeURIComponent(tab)}` : ""
      router.push(`/docker/containers/${encodeURIComponent(id)}${suffix}`)
    },
    [router, rememberNavigation],
  )
  // A change names a container by the name a redeploy keeps; the id it had
  // may be gone, and Docker resolves the name to whichever has it now.
  const openChange = useCallback(
    (target: { id?: string; name: string }) =>
      open(containers.find((c) => c.name === target.name)?.id ?? target.name),
    [containers, open],
  )

  if (!list.data && !streamed) {
    if (list.error) {
      return (
        <Page>
          <PageContext title="Docker" />
          <ErrorState error={list.error} />
        </Page>
      )
    }
    return <PageState eyebrow="Server" title="Docker" />
  }

  const failing = counts.bucket.failing
  const starting = counts.bucket.starting
  const running = containers.filter((c) => c.state === "running").length
  const attention = health.data?.attention
  const active = detected.filter((s) => s.deployed)
  const reclaimable = disk.data
    ? disk.data.images.reclaimable + disk.data.buildCache.reclaimable
    : 0
  const narrowed = Boolean(query.trim() || state || project)
  const projectChips = [...counts.projects.entries()].sort(([a], [b]) =>
    a === STANDALONE ? 1 : b === STANDALONE ? -1 : a.localeCompare(b),
  )

  const header = <PageContext title="Docker" />

  // A server with nothing on it is not an error state, and it is the one
  // moment where the page should be teaching rather than reporting.
  if (containers.length === 0 && detected.length === 0 && stacks.data) {
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
        health.refresh()
        trends.refresh()
        stacks.refresh()
        disk.refresh()
        feed.refresh()
      }}
      escape={() => {
        if (query) {
          setQuery("")
          return true
        }
        if (project) {
          setProject("")
          return true
        }
        if (state) {
          setState("")
          return true
        }
        return false
      }}
      commands={[
        {
          id: "failing",
          label: state === "failing" ? "Show every container" : "Show failing containers",
          run: () => setState(state === "failing" ? "" : "failing"),
        },
      ]}
    >
      {/* The page rises once, when its first container list lands — the same
          arrival the host Overview makes. */}
      <Page className="animate-rise">
        {header}

        <HostIdentity
          mark="docker"
          fallback={Box}
          title={info.data?.ServerVersion ? `Docker ${info.data.ServerVersion}` : "Docker"}
          facts={
            <>
              {(host?.hostname || info.data?.Name) && (
                <>
                  <HostFact product={platformProduct(host?.platform)}>
                    {host?.hostname || info.data?.Name}
                  </HostFact>
                  <FactDot />
                </>
              )}
              {info.data?.Driver && (
                <>
                  <span>
                    {info.data.Driver}
                    {info.data.CgroupVersion && ` on cgroup v${info.data.CgroupVersion}`}
                  </span>
                  <FactDot />
                </>
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
                <>
                  <FactDot />
                  <span className="numeric">
                    {plural(info.data.Images, "image")}
                    {disk.data && ` · ${bytes(disk.data.layersSize)}`}
                  </span>
                </>
              )}
            </>
          }
          aside={
            <div className="flex flex-wrap items-center gap-3">
              {failing > 0 ? (
                <button
                  type="button"
                  aria-pressed={state === "failing"}
                  onClick={() => setState(state === "failing" ? "" : "failing")}
                  className="rounded-md px-1.5 py-1 focus-ring transition-colors hover:bg-row-hover"
                >
                  <Status tone="danger" label={`${plural(failing, "container")} failing`} />
                </button>
              ) : starting > 0 ? (
                <Status tone="warning" label={`${plural(starting, "container")} starting`} />
              ) : (
                <Status tone="running" label="Nothing failing" />
              )}
              {attention && attention.issues > 0 && (
                <a
                  href="#attention"
                  className="rounded-md px-1.5 py-1 focus-ring transition-colors hover:bg-row-hover"
                >
                  <Status
                    verdict={attention.critical > 0 ? "critical" : "warning"}
                    label={`${plural(attention.issues, "issue")} to look at`}
                  />
                </a>
              )}
              <WorkspaceHelp compact />
            </div>
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
          onOpen={openChange}
        />

        {socketError && <ErrorState error={new Error(socketError)} />}

        {/* Framed, because it is a table: the grid owns a scroll region and
            the edge is what says so (§2). Everything around it stays plain. */}
        <Panel>
          <PanelHeader
            title={
              <span className="inline-flex items-center gap-1.5">
                Containers
                <span className="numeric text-body font-normal text-muted-foreground">
                  {containers.length}
                </span>
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
          >
            <ChipStrip role="group" aria-label="State" className="mr-auto">
              {STATES.map(({ value, label, tone }) => {
                const count = counts.bucket[value]
                if ((tone || value === "stopped") && count === 0 && state !== value) return null
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
                        tone === "danger"
                          ? "bg-destructive"
                          : tone === "warning"
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
          </PanelHeader>
          <PanelToolbar>
            <SearchInput
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              placeholder="Name, image or project"
              containerClassName="sm:w-64"
            />
            {projectChips.length > 1 && (
              <ChipStrip role="group" aria-label="Project">
                <FilterChip selected={project === ""} onClick={() => setProject("")}>
                  Every project <ChipCount>{containers.length}</ChipCount>
                </FilterChip>
                {projectChips.map(([name, count]) => (
                  <FilterChip
                    key={name}
                    selected={project === name}
                    title={name === STANDALONE ? "Started by hand rather than by compose" : name}
                    onClick={() => setProject(project === name ? "" : name)}
                  >
                    {name !== STANDALONE && <ProjectKey project={name} />}
                    {name === STANDALONE ? "Standalone" : name}
                    <ChipCount>{count}</ChipCount>
                  </FilterChip>
                ))}
              </ChipStrip>
            )}
            {narrowed && (
              <FilterChip
                selected
                className="ml-auto"
                aria-label="Clear every filter"
                onClick={() => {
                  setQuery("")
                  setState("")
                  setProject("")
                }}
              >
                Clear
                <Cross aria-hidden className="size-3" />
              </FilterChip>
            )}
          </PanelToolbar>
          <PanelBody flush>
            {visible.length === 0 ? (
              <EmptyState
                icon={Box}
                title={narrowed ? "No containers match" : "No containers yet"}
                description={
                  narrowed
                    ? "Clear a filter, or search for a different name, image or project."
                    : "Compose projects are on this server, but none of them is deployed."
                }
                className="my-4"
              />
            ) : (
              <ContainerRows
                key={[query, state, project].join("\u0000")}
                rows={visible}
                stats={stats}
                trends={trendByName}
                diagnosis={health.data}
                pending={pending}
                confirm={confirm}
                act={act}
                onOpen={open}
                onChanged={healthRefresh}
              />
            )}
          </PanelBody>
          <PanelFooter className="text-hint text-muted-foreground">
            <span className="numeric">{plural(visible.length, "container")}</span>
            <span className="text-muted-foreground/40">·</span>
            <span>failing first, then by name</span>
            <span className="text-muted-foreground/40">·</span>
            <span>CPU is a share of one core</span>
          </PanelFooter>
        </Panel>

        {/* Problems that do not stop a service: posture, storage,
            configuration and exposure. The verdict above links here. */}
        <div id="attention" className="scroll-mt-6">
          <AttentionPanel diagnosis={health.data} onRescan={health.refresh} onAction={runFix} />
        </div>

        <div className="grid min-w-0 items-start gap-x-10 gap-y-8 lg:grid-cols-2">
          <ComposeProjects stacks={detected} canDeploy={can("service.control")} />

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
                health.refresh()
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
 * The compose projects, as lit cards because each opens its project (§16):
 * what it is made of as its services' products, its name in the hue the
 * table draws its containers' project in, the directory it was found in, its
 * state — and under them its services as one strip of their states, so a
 * project with one service down reads as a gap in a green line before the
 * word "Partially" is read.
 */
function ComposeProjects({ stacks, canDeploy }: { stacks: ComposeStack[]; canDeploy: boolean }) {
  const active = stacks.filter((s) => s.deployed).length
  return (
    <Panel plain>
      <PanelHeader
        title={
          <span className="inline-flex items-center gap-1.5">
            Compose projects
            <span className="numeric text-body font-normal text-muted-foreground">
              {active} of {stacks.length} up
            </span>
            <ExplainIcon name="compose" />
          </span>
        }
        actions={
          <span className="flex flex-wrap items-center gap-3">
            <Link
              href="/docker/stacks"
              className="flex items-center gap-1 rounded-md text-hint font-medium text-muted-foreground focus-ring hover:text-foreground"
            >
              Manage <ArrowRight className="size-3" />
            </Link>
            {canDeploy && (
              <Button size="sm" asChild>
                <Link href="/deploy">Open Deploy</Link>
              </Button>
            )}
          </span>
        }
      />
      <PanelBody flush>
        {stacks.length === 0 ? (
          <EmptyState
            icon={Layers}
            title="No compose stacks"
            description="A stack is a directory with a compose file in it — one file describing several containers that belong together. The dashboard finds them by the labels compose puts on containers, and by looking under the configured compose directories."
          />
        ) : (
          <ChoiceList aria-label="Compose projects" className="animate-rise">
            {stacks.map((stack) => (
              <ChoiceRow
                key={stack.name}
                href={`/docker/stacks/${encodeURIComponent(stack.name)}`}
                verb={`Open ${stack.name}`}
                leading={<StackMark stack={stack} />}
                title={
                  <span className="flex min-w-0 items-center gap-2">
                    <ProjectKey project={stack.name} />
                    <span className="truncate">{stack.name}</span>
                  </span>
                }
                description={<span className="font-mono">{stack.workingDir}</span>}
                trailing={<StackStateBadge stack={stack} />}
              >
                <ServiceStrip stack={stack} />
              </ChoiceRow>
            ))}
          </ChoiceList>
        )}
      </PanelBody>
    </Panel>
  )
}

/**
 * Each service the project declares as one segment, in its state's tone: up,
 * failing, stopped, or not created. A project that was never deployed is a
 * row of empty track, which is what it is.
 */
function ServiceStrip({ stack }: { stack: ComposeStack }) {
  const byName = new Map(stack.services.map((s) => [s.name, s]))
  const names = [...new Set([...stack.declared, ...stack.services.map((s) => s.name)])]
  if (names.length === 0) return null
  const segments = names.map((name) => {
    const service = byName.get(name)
    if (!service || service.missing) return { name, tone: "bg-meter-track", word: "not created" }
    if (service.state === "running") {
      return service.health === "unhealthy"
        ? { name, tone: "bg-destructive", word: "unhealthy" }
        : service.health === "starting"
          ? { name, tone: "bg-warning", word: "starting" }
          : { name, tone: "bg-success", word: "running" }
    }
    if (service.state === "restarting" || service.state === "dead") {
      return { name, tone: "bg-destructive", word: service.state }
    }
    return crashed(service)
      ? { name, tone: "bg-destructive", word: service.status }
      : { name, tone: "bg-muted-foreground/40", word: service.status || service.state }
  })
  return (
    <div
      role="img"
      aria-label={segments.map((s) => `${s.name} ${s.word}`).join(", ")}
      className="flex h-1 max-w-80 gap-0.5"
    >
      {segments.map((s) => (
        <span
          key={s.name}
          title={`${s.name}: ${s.word}`}
          className={cn("h-full flex-1 rounded-full transition-colors", s.tone)}
        />
      ))}
    </div>
  )
}

/** What a project is made of: its services' products, or Compose itself. */
function StackMark({ stack }: { stack: ComposeStack }) {
  const images = stack.services.map((service) => service.image).filter(Boolean)
  return (
    <ProductLogos
      ids={images.length > 0 ? imageProducts(images) : ["docker-compose"]}
      ring="ring-choice-surface"
    />
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
              <Link href="/deploy">Open Deploy</Link>
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
