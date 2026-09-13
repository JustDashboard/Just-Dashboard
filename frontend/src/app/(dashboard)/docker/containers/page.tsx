"use client"

import { useCallback, useMemo, useState } from "react"
import { useRouter } from "next/navigation"
import { Box, Plus, Warning } from "@/components/icons"
import { get, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import { prune, pruneSummary, RECLAIM_SAFE } from "@/lib/docker-prune"
import { percent, truncateMiddle } from "@/lib/format"
import type {
  Container,
  ContainerSparkline,
  ContainerSpec,
  ContainerStats,
  DockerDiagnosis,
  DockerFinding,
} from "@/lib/types"
import { useSocket, type Envelope } from "@/hooks/use-socket"
import { usePoll } from "@/hooks/use-poll"
import { useQuerySelection } from "@/hooks/use-query-selection"
import { useAuth } from "@/hooks/use-auth"
import { useConfirm } from "@/components/confirm-dialog"
import { Page, PageHeader, SearchInput } from "@/components/page"
import { Panel, PanelBody, PanelHeader, PanelToolbar } from "@/components/panel"
import { ChipCount, FilterChip } from "@/components/tabs"
import { Sparkline } from "@/components/metrics/sparkline"
import { EmptyState, ErrorState } from "@/components/state"
import { ContainerDetailSheet } from "@/components/docker/container-detail"
import { CreateContainerPanel } from "@/components/docker/create-container"
import { AttentionPanel, RuntimeHealthPanel } from "@/components/docker/attention"
import { ExplainIcon } from "@/components/docker/explain"
import { PortList } from "@/components/docker/exposure"
import {
  ContainerName,
  ContainerStatus,
  CpuCell,
  IssuesCell,
  MemoryCell,
} from "@/components/docker/container-cells"
import { ContainerCard } from "@/components/docker/container-card"
import {
  ContainerRowActions,
  useContainerControl,
  useContainerVerbs,
  type PendingMap,
} from "@/components/docker/container-actions"
import type { ConfirmFn } from "@/components/docker/shared"
import { Button } from "@/components/ui/button"
import {
  stickyTableHeader,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"

/**
 * What is running on this server, and is any of it unhappy.
 *
 * Two things changed in the 0.6.7 polish pass, and both were about the reader
 * rather than about the data:
 *
 *   The nine-column table is now the *wide* layout rather than the only one.
 *   Below `lg` the same containers are drawn down the row instead of across it
 *   — see `container-card.tsx` — because a table with five of its nine columns
 *   removed is a table somebody is reading the remains of.
 *
 *   The row's verbs are a word and a sentence rather than five glyphs. Start,
 *   restart and stop stay as icons because they are pressed constantly and
 *   their shapes are universal; everything else — update, pause, shell, remove
 *   — moved into a menu where each one gets a line of plain English under it.
 *   `ArrowCircleUp` is not a word that means "pull a newer image and rebuild
 *   this container with the same settings", and a control nobody dares press is
 *   a control that is not there.
 *
 * The filter row above the list is the other half. A server with thirty
 * containers has one question most mornings — which of these is not running —
 * and answering it by reading a column was the only way to.
 */

type StateFilter = "all" | "running" | "stopped" | "attention"

const FILTER_LABEL: Record<StateFilter, string> = {
  all: "All",
  running: "Running",
  stopped: "Not running",
  attention: "Needs attention",
}

export default function ContainersPage() {
  const router = useRouter()
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  const [containers, setContainers] = useState<Container[]>([])
  const [stats, setStats] = useState<Record<string, ContainerStats>>({})
  const [socketError, setSocketError] = useState<string>()
  const [selected, setSelected] = useQuerySelection("container")
  const [focusTab, setFocusTab] = useState<string>()
  const [creating, setCreating] = useState<ContainerSpec | true | null>(null)
  const [filter, setFilter] = useState("")
  const [state, setState] = useState<StateFilter>("all")

  /**
   * An hour of shape per container, in one request. The live socket shows what
   * every container is doing this second, which is the wrong question once
   * something already went wrong: a container that pinned a core for ten
   * minutes and then settled reads as idle.
   */
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
  const trendByName = useMemo(() => {
    const map = new Map<string, ContainerSparkline>()
    for (const line of trends.data ?? []) map.set(line.name, line)
    return map
  }, [trends.data])

  const health = usePoll<DockerDiagnosis>(
    (signal) => get<DockerDiagnosis>("/docker/health", undefined, signal),
    60_000,
  )

  const onMessage = useCallback((envelope: Envelope) => {
    if (envelope.type === "containers") {
      setContainers(envelope.data as Container[])
      setSocketError(undefined)
    } else if (envelope.type === "stats") {
      const rows = envelope.data as ContainerStats[]
      setStats(Object.fromEntries(rows.map((r) => [r.id, r])))
    } else if (envelope.type === "error") {
      setSocketError(envelope.error)
    }
  }, [])

  useSocket("/docker/containers/stream", { onMessage })

  // `refresh` is stable, so the verbs a row memoises stay stable with it.
  const { pending, act } = useContainerControl(health.refresh)

  /** Opens one container's detail panel, optionally straight at a tab. */
  const open = useCallback(
    (id: string, tab?: string) => {
      setFocusTab(tab)
      setSelected(id)
    },
    [setSelected],
  )

  /**
   * A finding's remedy, carried out. This is what separates a diagnosis from a
   * warning list: the server names an action it knows how to do, and pressing
   * the button here does it. The ones that are not fixes — "show me the
   * evidence" — open the panel or the sibling page that holds it.
   */
  const runFix = useCallback(
    (finding: DockerFinding) => {
      switch (finding.action) {
        case "logs":
          open(finding.targetId ?? "", "logs")
          break
        case "usage":
          open(finding.targetId ?? "", "usage")
          break
        case "unpause":
          if (finding.targetId) {
            post(`/docker/containers/${finding.targetId}/unpause`)
              .then(() => {
                notify.success(`${finding.target} resumed`)
                health.refresh()
              })
              .catch((err) => notify.error(String(err)))
          }
          break
        case "set-restart":
          if (finding.targetId && finding.target) {
            confirm({
              title: "Set a restart policy",
              confirmLabel: "Apply",
              description: (
                <>
                  <p>
                    <b>{finding.target}</b> will be replaced by an identical container that comes
                    back after a reboot. Docker cannot change this on a container that already
                    exists, so the only way to set it is to rebuild it.
                  </p>
                  <p>
                    Its volumes and settings come with it; the service is interrupted for as long as
                    it takes to start.
                  </p>
                </>
              ),
              action: async (phrase) => {
                const spec = await get<ContainerSpec>(`/docker/containers/${finding.targetId}/spec`)
                await post(
                  `/docker/containers/${finding.targetId}/recreate`,
                  { spec: { ...spec, restartPolicy: "unless-stopped" } },
                  { confirm: phrase },
                )
                health.refresh()
              },
            })
          }
          break
        case "cap-logs":
          if (finding.targetId && finding.target) {
            confirm({
              title: "Cap the log size",
              confirmLabel: "Apply",
              description: (
                <>
                  <p>
                    <b>{finding.target}</b> will be replaced by an identical container that keeps 10
                    MB of logs across three files instead of every line it has ever printed. Docker
                    cannot change a log driver on a container that already exists, so the only way
                    to set it is to rebuild it.
                  </p>
                  <p>
                    Its volumes and settings come with it; the existing log file goes with the old
                    container, and the service is interrupted for as long as it takes to start.
                  </p>
                </>
              ),
              action: async (phrase) => {
                const spec = await get<ContainerSpec>(`/docker/containers/${finding.targetId}/spec`)
                await post(
                  `/docker/containers/${finding.targetId}/recreate`,
                  {
                    spec: {
                      ...spec,
                      logging: {
                        driver: "json-file",
                        options: { "max-size": "10m", "max-file": "3" },
                      },
                    },
                  },
                  { confirm: phrase },
                )
                health.refresh()
              },
            })
          }
          break
        case "stack.up":
          router.push("/docker/stacks")
          break
        case "volumes":
          router.push("/docker/volumes")
          break
        case "prune":
          // Runs the sweep rather than linking to it. This used to push to the
          // image list, where the only control prunes *dangling* images — so a
          // finding announcing tens of gigabytes was answered by a button that
          // on most hosts frees nothing, which is precisely how a working page
          // came to read as broken. The scope here is the finding's own
          // arithmetic: unused images plus build cache, never volumes.
          confirm({
            title: finding.title,
            confirmLabel: "Reclaim",
            description: (
              <>
                <p>
                  Removes every image no container is using and the whole build cache, along with
                  stopped containers and unused networks.
                </p>
                <p>
                  Nothing a running container needs is touched, and <b>no volume is</b> — the images
                  come back from their registries and the cache rebuilds itself, more slowly, on the
                  next build.
                </p>
              </>
            ),
            action: async () => {
              const reports = await prune(RECLAIM_SAFE)
              const { reclaimed, message, failed } = pruneSummary(reports)
              if (failed.length && reclaimed === 0) notify.error(message)
              else notify.success(message)
              health.refresh()
            },
          })
          break
        default:
          if (finding.targetId) open(finding.targetId)
      }
    },
    [health, confirm, router, open],
  )

  /** Which containers the dashboard has something to say about. */
  const flagged = useMemo(() => {
    const ids = new Set<string>()
    for (const finding of health.data?.findings ?? []) {
      if (finding.targetId && (finding.severity === "critical" || finding.severity === "warning")) {
        ids.add(finding.targetId)
      }
    }
    return ids
  }, [health.data])

  const counts = useMemo(
    () => ({
      all: containers.length,
      running: containers.filter((c) => c.state === "running").length,
      stopped: containers.filter((c) => c.state !== "running").length,
      attention: containers.filter((c) => flagged.has(c.id)).length,
    }),
    [containers, flagged],
  )

  const visible = useMemo(() => {
    const needle = filter.trim().toLowerCase()
    return containers.filter((c) => {
      if (state === "running" && c.state !== "running") return false
      if (state === "stopped" && c.state === "running") return false
      if (state === "attention" && !flagged.has(c.id)) return false
      if (!needle) return true
      return (
        c.name.toLowerCase().includes(needle) ||
        c.image.toLowerCase().includes(needle) ||
        c.composeStack?.toLowerCase().includes(needle) === true
      )
    })
  }, [containers, filter, state, flagged])

  const attention = health.data?.attention.total ?? 0
  const narrowed = filter.trim().length > 0 || state !== "all"

  const shared = {
    stats,
    trendByName,
    diagnosis: health.data,
    confirm,
    act,
    pending,
    open,
    onChanged: health.refresh,
  }

  return (
    <Page>
      <PageHeader
        eyebrow="Docker"
        title="Containers"
        actions={
          can("service.control") && (
            <Button size="sm" onClick={() => setCreating(true)}>
              <Plus className="size-4" />
              Run a container
            </Button>
          )
        }
      />

      {/*
        Runtime first, then everything else. They are separate panels because
        they answer separate questions: one clears itself when the thing it
        describes recovers, the other does not.
      */}
      <RuntimeHealthPanel runtime={health.data?.runtime} />
      {attention > 0 && <AttentionPanel diagnosis={health.data} onAction={runFix} />}

      {socketError && <ErrorState error={new Error(socketError)} />}

      <Panel>
        <PanelHeader
          title={
            <span className="inline-flex items-center gap-1.5">
              Containers
              <ExplainIcon name="container" />
            </span>
          }
        />
        <PanelToolbar>
          <SearchInput
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            placeholder="Filter by name, image or stack"
          />
          {/*
            "Which of these is down" is what this page is opened with most
            mornings, and answering it meant reading a column of thirty rows.
            The counts sit on the chips themselves, so the answer is often
            already on screen before anything is pressed — and a state nothing
            is in does not get a chip, because a filter that can only ever
            return nothing is furniture.
          */}
          <div className="flex min-w-0 flex-wrap gap-1">
            {(["all", "running", "stopped", "attention"] as const).map((key) =>
              key === "all" || counts[key] > 0 ? (
                <FilterChip
                  key={key}
                  selected={state === key}
                  onClick={() => setState(key)}
                  className={attentionChipTone(key, counts.attention)}
                >
                  {FILTER_LABEL[key]}
                  <ChipCount>{counts[key]}</ChipCount>
                </FilterChip>
              ) : null,
            )}
          </div>
        </PanelToolbar>

        <PanelBody flush>
          {visible.length === 0 ? (
            <EmptyState
              icon={narrowed ? Warning : Box}
              title={narrowed ? "Nothing matches those filters" : "Nothing running yet"}
              description={
                narrowed
                  ? "Clear the filter, or look under a different state."
                  : "A container is one application, packaged with everything it needs. Start from a common image, paste a docker run command you found in a README, or fill in the form yourself."
              }
              action={
                narrowed ? (
                  <Button
                    size="sm"
                    variant="outline"
                    onClick={() => {
                      setFilter("")
                      setState("all")
                    }}
                  >
                    Clear filters
                  </Button>
                ) : (
                  can("service.control") && (
                    <Button size="sm" onClick={() => setCreating(true)}>
                      <Plus className="size-4" />
                      Run a container
                    </Button>
                  )
                )
              }
            />
          ) : (
            <>
              {/*
                Below `xl` the table is replaced rather than squeezed. The
                boundary is 1280 and not 1024 because of the sidebar: at 1024 a
                nine-column table has about 750px to live in, so it appeared
                already scrolling sideways inside its own panel with Issues and
                the row's actions past the right edge — a table that arrives
                broken. `CPU · 1h` waits for `2xl`, which is where the ninth
                column stops being the one that pushes the rest out.
              */}
              <ul className="divide-y divide-hairline xl:hidden">
                {visible.map((container) => (
                  <ContainerListItem key={container.id} container={container} {...shared} />
                ))}
              </ul>

              <div className="hidden xl:block">
                <Table containerClassName="max-h-[calc(100svh-21rem)]">
                  <TableHeader className={stickyTableHeader}>
                    <TableRow>
                      <TableHead className="w-full">Container</TableHead>
                      <TableHead>Image</TableHead>
                      {/*
                        Status is runtime and nothing else. It used to carry the
                        worst finding about the container underneath the state,
                        so a perfectly healthy container reading "Running" also
                        read "publishes PostgreSQL on every interface" in the
                        same cell — two different kinds of fact in one column.
                        Diagnostics have their own column now.
                      */}
                      <TableHead>Status</TableHead>
                      <TableHead className="text-right">CPU</TableHead>
                      <TableHead className="text-right">Memory</TableHead>
                      {/* Named, because a sparkline cannot say what it is charting. */}
                      <TableHead className="hidden text-right 2xl:table-cell">CPU · 1h</TableHead>
                      <TableHead>Ports</TableHead>
                      <TableHead className="text-center">Issues</TableHead>
                      <TableHead className="w-px" />
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {visible.map((container) => (
                      <ContainerTableRow key={container.id} container={container} {...shared} />
                    ))}
                  </TableBody>
                </Table>
              </div>
            </>
          )}
        </PanelBody>
      </Panel>

      <ContainerDetailSheet
        containerId={selected}
        focusTab={focusTab}
        onOpenChange={(isOpen) => !isOpen && setSelected(null)}
        diagnosis={health.data}
        confirm={confirm}
        onChanged={() => health.refresh()}
        onDuplicate={(spec) => {
          setSelected(null)
          setCreating(spec)
        }}
      />
      <CreateContainerPanel
        open={creating !== null}
        initialSpec={creating === true ? undefined : (creating ?? undefined)}
        onOpenChange={(isOpen) => !isOpen && setCreating(null)}
        onCreated={() => {
          trends.refresh()
          health.refresh()
        }}
      />
      {dialog}
    </Page>
  )
}

/** Everything a row needs that does not come from the container itself. */
type RowContext = {
  stats: Record<string, ContainerStats>
  trendByName: Map<string, ContainerSparkline>
  diagnosis?: DockerDiagnosis
  confirm: ConfirmFn
  act: (c: Container, action: string, progressive: string, phrase?: string) => Promise<void>
  pending: PendingMap
  open: (id: string, tab?: string) => void
  onChanged: () => void
}

/**
 * One row of the wide table.
 *
 * A component rather than a closure in the page's `map`, because the verbs it
 * offers come from a hook and a hook cannot be called in a loop — and because
 * the two layouts genuinely are two components that happen to show the same
 * container.
 */
function ContainerTableRow({
  container,
  stats,
  trendByName,
  diagnosis,
  confirm,
  act,
  pending,
  open,
  onChanged,
}: RowContext & { container: Container }) {
  const verbs = useContainerVerbs({
    container,
    confirm,
    act,
    onOpenTab: (tab) => open(container.id, tab),
    onChanged,
  })
  const stat = stats[container.id]
  const busy = pending[container.id]

  return (
    <TableRow
      className={busy ? "group opacity-70" : "group"}
      aria-busy={busy ? true : undefined}
      onActivate={() => open(container.id)}
    >
      <TableCell>
        <ContainerName container={container} onOpen={() => open(container.id)} />
      </TableCell>
      <TableCell className="font-mono text-hint text-muted-foreground">
        {truncateMiddle(container.image, 34)}
      </TableCell>
      <TableCell>
        <ContainerStatus container={container} pending={busy} />
      </TableCell>
      <TableCell className="text-right">
        <CpuCell stat={stat} container={container} />
      </TableCell>
      <TableCell className="text-right">
        <MemoryCell stat={stat} container={container} />
      </TableCell>
      <TableCell className="hidden text-right 2xl:table-cell">
        <ContainerTrend trend={trendByName.get(container.name)} />
      </TableCell>
      {/* One row, always. See the note on `PortList`: a proxy publishing six
          ports used to wrap this cell onto three lines and make its row half
          again as tall as every other. */}
      <TableCell className="max-w-40">
        <PortList ports={container.exposure ?? []} max={1} />
      </TableCell>
      <TableCell>
        <IssuesCell
          diagnosis={diagnosis}
          containerId={container.id}
          onOpen={() => open(container.id)}
        />
      </TableCell>
      {/*
        Drawn at rest rather than revealed on hover. Reserving a column for
        controls and then leaving it empty is a ninth column of nothing —
        thirteen rows of blank space ending in one row that suddenly has
        buttons in it, which is what made this table look unfinished. They are
        dimmed until the pointer is on the row, which keeps a long list calm
        without pretending the column is not there.
      */}
      <TableCell>
        <ContainerRowActions verbs={verbs} reveal={false} dim />
      </TableCell>
    </TableRow>
  )
}

/** The same container, on a screen too narrow for nine columns. */
function ContainerListItem({
  container,
  stats,
  trendByName,
  diagnosis,
  confirm,
  act,
  pending,
  open,
  onChanged,
}: RowContext & { container: Container }) {
  const verbs = useContainerVerbs({
    container,
    confirm,
    act,
    onOpenTab: (tab) => open(container.id, tab),
    onChanged,
  })
  return (
    <ContainerCard
      container={container}
      stat={stats[container.id]}
      trend={trendByName.get(container.name)}
      diagnosis={diagnosis}
      verbs={verbs}
      pending={pending[container.id]}
      onOpen={() => open(container.id)}
    />
  )
}

/**
 * One container's last hour, in a table cell. The peak is spelled out beside
 * the line because a sparkline cannot carry a scale: two rows whose lines look
 * identical may be a container that touched 4% and one that pinned two cores.
 */
function ContainerTrend({ trend }: { trend?: ContainerSparkline }) {
  if (!trend || trend.cpu.length === 0) {
    return <span className="text-hint text-muted-foreground">—</span>
  }
  return (
    <span className="flex items-center justify-end gap-2">
      <Sparkline
        values={trend.cpu}
        label={`CPU over the last hour, peaking at ${percent(trend.cpuPeak)}`}
        color="var(--chart-1)"
        className="animate-rise"
      />
      <span className="numeric w-11 shrink-0 text-right font-mono text-hint text-muted-foreground">
        {percent(trend.cpuPeak, 0)}
      </span>
    </span>
  )
}

/**
 * The attention chip is the one filter that carries a tone, and only while
 * there is something in it. A row of four neutral chips where one of them
 * means "two of your containers have a problem" is a row that hides the thing
 * it exists to surface; four coloured chips would be four alarms.
 */
function attentionChipTone(key: StateFilter, count: number) {
  return key === "attention" && count > 0 ? "text-warning hover:text-warning" : undefined
}
