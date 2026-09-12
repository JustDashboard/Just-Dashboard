"use client"

import { useCallback, useMemo, useState } from "react"
import { useRouter } from "next/navigation"
import {
  ArrowCircleUp,
  Box,
  Pause,
  Play,
  Plus,
  RotateClockwise,
  StopCircle,
  Terminal,
  Trash,
} from "@/components/icons"
import { notify } from "@/lib/toast"
import { del, get, post } from "@/lib/api"
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
import { Sparkline } from "@/components/metrics/sparkline"
import { EmptyState, ErrorState } from "@/components/state"
import { Status } from "@/components/status-dot"
import { IconAction, RowActions } from "@/components/icon-action"
import { ContainerDetailSheet } from "@/components/docker/container-detail"
import { CreateContainerPanel } from "@/components/docker/create-container"
import { AttentionPanel, RuntimeHealthPanel } from "@/components/docker/attention"
import { PortList } from "@/components/docker/exposure"
import { ContainerName, CpuCell, IssuesCell, MemoryCell } from "@/components/docker/container-cells"
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

export default function ContainersPage() {
  const router = useRouter()
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  const [containers, setContainers] = useState<Container[]>([])
  const [stats, setStats] = useState<Record<string, ContainerStats>>({})
  const [socketError, setSocketError] = useState<string>()
  const [selected, setSelected] = useQuerySelection("container")
  const [creating, setCreating] = useState<ContainerSpec | true | null>(null)
  const [filter, setFilter] = useState("")

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

  const act = async (container: Container, action: string, confirmText?: string) => {
    try {
      await post(`/docker/containers/${container.id}/${action}`, undefined, {
        confirm: confirmText,
      })
      notify.success(`${container.name} ${action}ed`)
    } catch (err) {
      notify.error(`Could not ${action} ${container.name}`, err)
      throw err
    }
  }

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
        case "usage":
          setSelected(finding.targetId ?? null)
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
          if (finding.targetId) setSelected(finding.targetId)
      }
    },
    [health, confirm, router, setSelected],
  )

  const visible = useMemo(() => {
    const needle = filter.toLowerCase()
    if (!needle) return containers
    return containers.filter(
      (c) =>
        c.name.toLowerCase().includes(needle) ||
        c.image.toLowerCase().includes(needle) ||
        c.composeStack?.toLowerCase().includes(needle),
    )
  }, [containers, filter])

  const attention = health.data?.attention.total ?? 0

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
          icon={Box}
          title="Containers"
        />
        <PanelToolbar>
          <SearchInput
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            placeholder="Filter by name, image or stack"
          />
        </PanelToolbar>
        <PanelBody flush>
          <Table containerClassName="max-h-[calc(100svh-21rem)]">
            <TableHeader className={stickyTableHeader}>
              <TableRow>
                <TableHead className="w-full">Container</TableHead>
                <TableHead className="hidden md:table-cell">Image</TableHead>
                {/*
                  Status is runtime and nothing else. It used to carry the
                  worst finding about the container underneath the state, so a
                  perfectly healthy container reading "Running" also read
                  "publishes PostgreSQL on every interface" in the same cell —
                  two different kinds of fact in one column. Diagnostics have
                  their own column now.
                */}
                <TableHead>Status</TableHead>
                <TableHead className="hidden text-right sm:table-cell">CPU</TableHead>
                <TableHead className="hidden text-right sm:table-cell">Memory</TableHead>
                {/* Named, because a sparkline cannot say what it is charting. */}
                <TableHead className="hidden text-right xl:table-cell">CPU · 1h</TableHead>
                <TableHead className="hidden lg:table-cell">Ports</TableHead>
                <TableHead className="text-center">Issues</TableHead>
                <TableHead className="w-px" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {visible.map((container) => {
                const stat = stats[container.id]
                return (
                  <TableRow
                    key={container.id}
                    className="group"
                    onActivate={() => setSelected(container.id)}
                  >
                    <TableCell>
                      <ContainerName
                        container={container}
                        onOpen={() => setSelected(container.id)}
                      />
                    </TableCell>
                    <TableCell className="hidden font-mono text-hint text-muted-foreground md:table-cell">
                      {truncateMiddle(container.image, 34)}
                    </TableCell>
                    <TableCell>
                      {/*
                        Runtime only. "Running · Healthy · 2h 18m" is one kind
                        of fact; what is wrong with the container's
                        configuration is another, and it has its own column.
                      */}
                      <Status state={container.state} label={container.status} />
                      <p className="mt-0.5 text-hint text-muted-foreground">
                        {container.state === "running"
                          ? container.health
                            ? container.health
                            : container.inspected
                              ? "no health check"
                              : ""
                          : ""}
                      </p>
                    </TableCell>
                    <TableCell className="hidden text-right sm:table-cell">
                      <CpuCell stat={stat} container={container} />
                    </TableCell>
                    <TableCell className="hidden text-right sm:table-cell">
                      <MemoryCell stat={stat} container={container} />
                    </TableCell>
                    <TableCell className="hidden text-right xl:table-cell">
                      <ContainerTrend trend={trendByName.get(container.name)} />
                    </TableCell>
                    <TableCell className="hidden lg:table-cell">
                      <PortList ports={container.exposure ?? []} />
                    </TableCell>
                    <TableCell>
                      <IssuesCell
                        diagnosis={health.data}
                        containerId={container.id}
                        onOpen={() => setSelected(container.id)}
                      />
                    </TableCell>
                    <TableCell>
                      <RowActions>
                        {container.state === "running" ? (
                          <>
                            {can("service.control") && (
                              <IconAction
                                label="Pause"
                                onClick={() => act(container, "pause").catch(() => undefined)}
                              >
                                <Pause />
                              </IconAction>
                            )}
                            {can("destructive") && (
                              <>
                                <IconAction
                                  label="Restart"
                                  onClick={() =>
                                    confirm({
                                      title: "Restart container",
                                      confirmLabel: "Restart",
                                      description: (
                                        <p>
                                          <b>{container.name}</b> will be stopped and started again.
                                          Anything it is serving will be interrupted.
                                        </p>
                                      ),
                                      action: (c) => act(container, "restart", c),
                                    })
                                  }
                                >
                                  <RotateClockwise />
                                </IconAction>
                                <IconAction
                                  label="Stop"
                                  onClick={() =>
                                    confirm({
                                      title: "Stop container",
                                      confirmLabel: "Stop",
                                      description: (
                                        <p>
                                          <b>{container.name}</b> will stop serving immediately.
                                        </p>
                                      ),
                                      action: (c) => act(container, "stop", c),
                                    })
                                  }
                                >
                                  <StopCircle />
                                </IconAction>
                              </>
                            )}
                          </>
                        ) : (
                          can("service.control") && (
                            <IconAction
                              label="Start"
                              onClick={() => act(container, "start").catch(() => undefined)}
                            >
                              <Play />
                            </IconAction>
                          )
                        )}
                        {can("destructive") && !container.composeStack && (
                          <IconAction
                            label="Update to a newer image"
                            onClick={() =>
                              confirm({
                                title: "Update container",
                                confirmLabel: "Update",
                                description: (
                                  <>
                                    <p>
                                      Pulls a newer <b>{container.image}</b> and replaces{" "}
                                      <b>{container.name}</b> with a container built from it,
                                      keeping every setting it has now.
                                    </p>
                                    <p>
                                      Its volumes come with it. Anything written inside the
                                      container rather than into a volume does not.
                                    </p>
                                  </>
                                ),
                                action: async (c) => {
                                  await post(
                                    `/docker/containers/${container.id}/recreate`,
                                    { pullLatest: true },
                                    { confirm: c },
                                  )
                                  health.refresh()
                                },
                              })
                            }
                          >
                            <ArrowCircleUp />
                          </IconAction>
                        )}
                        {can("terminal") && container.state === "running" && (
                          <IconAction label="Shell" onClick={() => setSelected(container.id)}>
                            <Terminal />
                          </IconAction>
                        )}
                        {can("destructive") && (
                          <IconAction
                            label="Remove"
                            className="text-destructive"
                            onClick={() =>
                              confirm({
                                title: "Remove container",
                                confirmLabel: "Remove",
                                description: (
                                  <p>
                                    <b>{container.name}</b> will be deleted. This cannot be undone;
                                    its anonymous volumes are kept.
                                  </p>
                                ),
                                action: async (c) => {
                                  await del(`/docker/containers/${container.id}`, {
                                    confirm: c,
                                    query: { force: true },
                                  })
                                },
                              })
                            }
                          >
                            <Trash />
                          </IconAction>
                        )}
                      </RowActions>
                    </TableCell>
                  </TableRow>
                )
              })}
              {visible.length === 0 && (
                <TableRow>
                  <TableCell colSpan={9} className="p-0">
                    <EmptyState
                      icon={Box}
                      title={filter ? "No containers match that filter" : "Nothing running yet"}
                      description={
                        filter
                          ? undefined
                          : "Start from a common image, paste a docker run command you found, or fill in the form yourself."
                      }
                      action={
                        !filter &&
                        can("service.control") && (
                          <Button size="sm" onClick={() => setCreating(true)}>
                            <Plus className="size-4" />
                            Run a container
                          </Button>
                        )
                      }
                    />
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        </PanelBody>
      </Panel>

      <ContainerDetailSheet
        containerId={selected}
        onOpenChange={(open) => !open && setSelected(null)}
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
        onOpenChange={(open) => !open && setCreating(null)}
        onCreated={() => {
          trends.refresh()
          health.refresh()
        }}
      />
      {dialog}
    </Page>
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
      />
      <span className="numeric w-11 shrink-0 text-right font-mono text-hint text-muted-foreground">
        {percent(trend.cpuPeak, 0)}
      </span>
    </span>
  )
}
