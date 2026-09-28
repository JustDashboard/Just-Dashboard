"use client"

import { useCallback, useEffect, useMemo, useState } from "react"
import { useSessionState } from "@/lib/view-state"
import Link from "next/link"
import { useRouter, useSearchParams } from "next/navigation"
import { Box, Warning } from "@/components/icons"
import { get, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import { prune, pruneSummary, RECLAIM_SAFE } from "@/lib/docker-prune"
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
import { useAuth } from "@/hooks/use-auth"
import { useMediaQuery } from "@/hooks/use-mobile"
import { useConfirm } from "@/components/confirm-dialog"
import { Page, PageContext, SearchInput } from "@/components/page"
import { Panel, PanelBody, PanelHeader, PanelToolbar } from "@/components/panel"
import { ChipCount, FilterChip } from "@/components/tabs"
import { ChoiceList, GroupRule } from "@/components/flow"
import { EmptyState, ErrorState } from "@/components/state"
import { AttentionPanel, RuntimeHealthPanel } from "@/components/docker/attention"
import { ExplainIcon } from "@/components/docker/explain"
import { ContainerCard } from "@/components/docker/container-card"
import {
  useContainerControl,
  useContainerVerbs,
  type PendingMap,
} from "@/components/docker/container-actions"
import type { ConfirmFn } from "@/components/docker/shared"
import { Button } from "@/components/ui/button"

/**
 * What is running on this server, and is any of it unhappy.
 *
 * Two things changed in the 0.6.7 polish pass, and both were about the reader
 * rather than about the data:
 *
 *   The nine-column table is gone at every width: each container is a card
 *   you open, its readings held to the right on a wide screen and beneath its
 *   name on a narrow one — see `container-card.tsx` for why a list of places
 *   to go is cards rather than cells.
 *
 *   The row's verbs are words rather than five glyphs. Start, restart and
 *   stop stay as icons because they are pressed constantly and their shapes
 *   are universal; everything else — update, pause, shell, remove — moved
 *   into a menu where each one is named.
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
  const [filter, setFilter] = useSessionState("docker.containers.query", "")
  const [state, setState] = useSessionState<StateFilter>("docker.containers.state", "all")

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

  /** Goes to one container, optionally straight at a tab. */
  const open = useCallback(
    (id: string, tab?: string) => {
      const query = tab ? `?tab=${encodeURIComponent(tab)}` : ""
      router.push(`/docker/containers/${encodeURIComponent(id)}${query}`)
    },
    [router],
  )

  /*
    `?container=` opened a sheet on this page until 2026-09-21, and
    `useQuerySelection` also put a remembered one back on arrival. Both are
    addresses that exist in the wild — a bookmark, a tab restored by the
    browser, a link pasted into a ticket — so they land on the container
    instead of on a list that quietly ignores them.
  */
  const legacy = useSearchParams().get("container")
  useEffect(() => {
    if (legacy) router.replace(`/docker/containers/${encodeURIComponent(legacy)}`)
  }, [legacy, router])

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

  // What needs you first, as on the Git page: only while the list is whole,
  // because once a filter is on, its name is the group, and a rule repeating
  // it over one run of cards is a rule with nothing on either side of it.
  const groups = narrowed
    ? [{ key: "all", label: "", rows: visible }]
    : [
        {
          key: "attention",
          label: "Needs attention",
          rows: visible.filter((c) => flagged.has(c.id)),
        },
        {
          key: "running",
          label: "Running",
          rows: visible.filter((c) => !flagged.has(c.id) && c.state === "running"),
        },
        {
          key: "stopped",
          label: "Not running",
          rows: visible.filter((c) => !flagged.has(c.id) && c.state !== "running"),
        },
      ].filter((group) => group.rows.length > 0)

  // One answer for every card, rather than a listener per row.
  const wide = useMediaQuery("(min-width: 1280px)")

  const shared = {
    wide,
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
    <Page className="animate-rise">
      {/* Containers are deployed from the Deploy pages — there is no standalone
          create flow here anymore. */}
      <PageContext eyebrow="Docker" title="Containers" />

      {/*
        Runtime first, then everything else. They are separate panels because
        they answer separate questions: one clears itself when the thing it
        describes recovers, the other does not.
      */}
      <RuntimeHealthPanel runtime={health.data?.runtime} />
      {attention > 0 && (
        <AttentionPanel diagnosis={health.data} onAction={runFix} onRescan={health.refresh} />
      )}

      {socketError && <ErrorState error={new Error(socketError)} />}

      {/* Plain: every container is a card with its own edge now, and a frame
          around framed cards is the nesting §12 refuses. A title and a hairline
          mark where the list begins. */}
      <Panel plain>
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
                  : "A container is one application, packaged with everything it needs. Everything here is deployed from the Deploy pages."
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
                    <Button size="sm" asChild>
                      <Link href="/deploy">Open Deploy</Link>
                    </Button>
                  )
                )
              }
            />
          ) : (
            <div className="flex min-w-0 animate-rise flex-col gap-4">
              {groups.map((group) => (
                <section key={group.key} className="flex min-w-0 flex-col gap-2">
                  {group.label && groups.length > 1 && (
                    <GroupRule label={group.label} count={group.rows.length} />
                  )}
                  <ChoiceList aria-label={group.label || "Containers"}>
                    {group.rows.map((container) => (
                      <ContainerItem key={container.id} container={container} {...shared} />
                    ))}
                  </ChoiceList>
                </section>
              ))}
            </div>
          )}
        </PanelBody>
      </Panel>

      {dialog}
    </Page>
  )
}

/** Everything a row needs that does not come from the container itself. */
type RowContext = {
  wide: boolean
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
 * One container's card. A component rather than a closure in the page's
 * `map`, because the verbs it offers come from a hook and a hook cannot be
 * called in a loop.
 */
function ContainerItem({
  container,
  wide,
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
      wide={wide}
      onOpen={() => open(container.id)}
      onOpenIssues={() => open(container.id, "overview")}
    />
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
