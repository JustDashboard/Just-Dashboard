"use client"

import { useCallback, useMemo } from "react"
import Link from "next/link"
import { useRouter } from "next/navigation"
import { ArrowRight, Box, Clipboard, Layers, Sparkles } from "@/components/icons"
import { get } from "@/lib/api"
import { bytes } from "@/lib/format"
import type { ComposeStack, Container, DockerDiagnosis, DockerDiskUsage } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { useConfirm } from "@/components/confirm-dialog"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { Page, PageHeader, PageState } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Row, RowList } from "@/components/row-list"
import { StatGrid, StatLink, StatTile } from "@/components/stat-tile"
import { Status, StatusDot } from "@/components/status-dot"
import { EmptyState } from "@/components/state"
import { AttentionPanel, attentionLabel, runtimeLabel } from "@/components/docker/attention"
import { CleanupPanel } from "@/components/docker/cleanup"
import { DiskSummary } from "@/components/docker/disk-panel"
import { ExplainIcon } from "@/components/docker/explain"
import { StackStateBadge } from "@/components/docker/stack-state"
import {
  ContainerRowActions,
  useContainerControl,
  useContainerVerbs,
} from "@/components/docker/container-actions"
import { Button } from "@/components/ui/button"

/**
 * Is Docker okay right now?
 *
 * The overview should answer that in five seconds, and the version this
 * replaces could not: its Health tile read the worst of *everything* the
 * diagnosis found, so a server whose containers were all up and one of which
 * mounted the Docker socket showed "All good" beside a Containers page full of
 * warnings. Runtime and Attention are separate tiles here because they are
 * separate questions — see components/docker/attention.tsx.
 *
 * The stack count had the same disease from the other direction. This page
 * grouped containers by compose label and the Stacks page listed compose files
 * found on disk, so "3 stacks" and "7 stacks" were both true and neither was
 * checkable. Both now read the same endpoint and say "3 active · 7 detected".
 *
 * Two things the 0.6.7 polish pass added, both of them gaps rather than
 * decoration:
 *
 *   **A stopped container was invisible here.** Attention deliberately excludes
 *   runtime findings, and the runtime tile summarises rather than names — so a
 *   service that had been down since a reboot showed up as the digit 7 turning
 *   into a 6. It now has a panel with the containers in it and a Start button
 *   beside each, because "what is not running" is the single most common reason
 *   somebody opens this page.
 *
 *   **Disk was a sentence.** "Only 412 MB could be reclaimed" is four numbers
 *   collapsed into an opinion about one of them. The bar says what the space
 *   actually is, which is the thing a reader needs before deciding whether a
 *   sweep is the answer or whether it is the volumes holding their data.
 */
export default function DockerOverviewPage() {
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()

  const list = usePoll<Container[]>(
    (signal) => get<Container[]>("/docker/containers/", undefined, signal),
    30_000,
  )
  const health = usePoll<DockerDiagnosis>(
    (signal) => get<DockerDiagnosis>("/docker/health", undefined, signal),
    60_000,
  )
  const stacks = usePoll<ComposeStack[]>(
    (signal) => get<ComposeStack[]>("/docker/stacks/", undefined, signal),
    60_000,
  )
  const disk = usePoll<DockerDiskUsage>(
    (signal) => get<DockerDiskUsage>("/docker/disk-usage", undefined, signal),
    120_000,
  )

  const containers = useMemo(() => list.data ?? [], [list.data])
  const detected = useMemo(() => stacks.data ?? [], [stacks.data])
  const active = useMemo(() => detected.filter((s) => s.deployed), [detected])
  const idle = useMemo(() => containers.filter((c) => c.state !== "running"), [containers])

  const listRefresh = list.refresh
  const healthRefresh = health.refresh
  const refreshContainers = useCallback(() => {
    listRefresh()
    healthRefresh()
  }, [listRefresh, healthRefresh])
  const { pending, act } = useContainerControl(refreshContainers)

  if (list.loading && !list.data) {
    return <PageState eyebrow="Server" title="Docker" />
  }

  const runtime = health.data?.runtime
  const attention = health.data?.attention
  const running = containers.length - idle.length
  const reclaimable = disk.data
    ? disk.data.images.reclaimable + disk.data.buildCache.reclaimable
    : 0

  return (
    // The page rises once, when its first container list lands — the same
    // arrival the host Overview makes.
    <Page className="animate-rise">
      <PageHeader
        eyebrow="Server"
        title={
          <span className="inline-flex items-center gap-2">
            Docker
            {/*
              The one word on this page that has to be understood before any of
              the others, and the section's front page never said what it was.
              A hover card rather than a paragraph: an operator who knows Docker
              never sees it, and somebody who does not is one gesture from an
              answer written for them.
            */}
            <ExplainIcon name="docker" className="translate-y-0.5" />
          </span>
        }
        /*
          The page's one command (§15 pass 6). New containers come from the
          Deploy pages — there is no standalone create flow here — so the way
          *on* to this server is the only thing the overview asks you to press,
          and everything else on it is a reading or a way through to one. It
          used to appear only on an empty server, which left a populated Docker
          page with no brand ink anywhere on it and nothing that looked like
          the action.
        */
        actions={
          can("service.control") && (
            <Button size="sm" asChild>
              <Link href="/deploy">Open Deploy</Link>
            </Button>
          )
        }
      />

      {/*
        Four readings, four destinations. Every one of them is a link now: three
        were and one was not, which taught the reader that a tile is sometimes a
        button and sometimes furniture — and the one that was not is "Running",
        the tile most likely to be pressed.
      */}
      <StatGrid columns={4}>
        <StatLink href="/docker/containers" label="Running containers">
          <StatTile
            className="h-full transition-colors group-hover:bg-row-hover"
            label="Running"
            value={`${running} / ${containers.length}`}
            tone={running > 0 ? "success" : "default"}
            hint={
              containers.length === 0
                ? "nothing on this server yet"
                : containers.length === running
                  ? "everything on this server is up"
                  : `${containers.length - running} not running`
            }
          />
        </StatLink>

        {/*
          Runtime health, and only runtime health. The hint spells out how many
          containers have no health check at all, because without that number
          "everything is up" quietly includes every container nothing is
          watching.
        */}
        <StatLink href="/docker/containers" label="Runtime health">
          <StatTile
            className="h-full transition-colors group-hover:bg-row-hover"
            label="Runtime health"
            value={runtimeLabel(runtime)}
            /*
              "notice" is a stopped container, not a failure and not a success:
              it used to fall through to `success`, so the tile printed
              "Something is stopped" in green.
            */
            tone={
              runtime?.status === "critical"
                ? "danger"
                : runtime?.status === "warning"
                  ? "warning"
                  : // An empty server is not a healthy one. "Nothing running"
                    // in green is the page congratulating somebody for having
                    // no services yet.
                    runtime?.status === "ok" && runtime.total > 0
                    ? "success"
                    : "default"
            }
            hint={runtime?.summary ?? "checking"}
          />
        </StatLink>

        {/*
          Attention is never called health. It counts posture, storage,
          configuration and exposure — none of which stops a service, all of
          which costs something later, and none of which clears itself.
        */}
        <StatLink href="/docker/containers" label="Things needing attention">
          <StatTile
            className="h-full transition-colors group-hover:bg-row-hover"
            label="Attention"
            value={attentionLabel(attention)}
            tone={
              attention?.critical
                ? "danger"
                : attention?.warning
                  ? "warning"
                  : attention?.total
                    ? "default"
                    : "success"
            }
            hint={
              attention?.total
                ? `${attention.recommendations} recommendation${attention.recommendations === 1 ? "" : "s"} besides`
                : "nothing to act on"
            }
          />
        </StatLink>

        <StatLink href="/docker/stacks" label="Compose stacks">
          <StatTile
            className="h-full transition-colors group-hover:bg-row-hover"
            label="Compose stacks"
            value={`${active.length} active`}
            hint={`${detected.length} detected on this server`}
          />
        </StatLink>
      </StatGrid>

      {/* A server with nothing on it is not an error state, and it is the one
          moment where the page should be teaching rather than reporting. */}
      {containers.length === 0 && detected.length === 0 ? (
        <FirstRun />
      ) : (
        <>
          {/*
            Not running, by name. The runtime tile counts them and the attention
            list deliberately does not carry them, so until this panel existed
            the only way to find out *which* service had been down since the
            reboot was to open another page and read a column.
          */}
          {idle.length > 0 && (
            <Panel plain>
              <PanelHeader
                title={`${idle.length} not running`}
                actions={
                  <Link
                    href="/docker/containers"
                    className="flex items-center gap-1 rounded-md text-hint font-medium text-muted-foreground focus-ring hover:text-foreground"
                  >
                    All containers <ArrowRight className="size-3" />
                  </Link>
                }
              />
              <PanelBody flush>
                {/*
                  Destinations, so they are choices (§16): every row here is a
                  container to open, and the edge that answers the pointer is
                  what says so. The hairlines are gone with the `RowList` —
                  each card owns its own edge.
                */}
                <ChoiceList aria-label="Containers that are not running" className="animate-rise">
                  {idle.slice(0, 6).map((container) => (
                    <IdleRow
                      key={container.id}
                      container={container}
                      confirm={confirm}
                      act={act}
                      pending={pending[container.id]}
                      onChanged={refreshContainers}
                    />
                  ))}
                </ChoiceList>
                {idle.length > 6 && (
                  // Aligned with the card titles above it rather than with the
                  // cards' own edges, and with no rule over it: a hairline
                  // under a gapped list reads as an edge the last card lost.
                  <p className="px-3 pt-2 text-hint text-muted-foreground">
                    and {idle.length - 6} more.
                  </p>
                )}
              </PanelBody>
            </Panel>
          )}

          {/* Problems first. Everything below is context for them. */}
          <AttentionPanel diagnosis={health.data} onRescan={health.refresh} />

          <Panel plain>
            <PanelHeader
              title={
                <span className="inline-flex items-center gap-1.5">
                  Compose projects
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
              {detected.length === 0 ? (
                <EmptyState
                  icon={Layers}
                  title="No compose stacks"
                  description="A stack is a directory with a compose file in it — one file describing several containers that belong together. The dashboard finds them by the labels compose puts on containers, and by looking under the configured compose directories."
                />
              ) : (
                // A run of destinations, not of readings: every one of these
                // rows is a project to enter, so §16 makes them choices and
                // gives them the lit edge. The row still says the same four
                // things — the dot, the name, the directory it was found in,
                // and what state it is in.
                <ChoiceList aria-label="Compose projects" className="animate-rise">
                  {detected.map((stack) => (
                    <ChoiceRow
                      key={stack.name}
                      href={`/docker/stacks?stack=${encodeURIComponent(stack.name)}`}
                      verb={`Open ${stack.name}`}
                      leading={
                        <StatusDot tone={stackTone(stack)} live={stack.state === "running"} />
                      }
                      title={stack.name}
                      description={<span className="font-mono">{stack.workingDir}</span>}
                      trailing={<StackStateBadge stack={stack} />}
                    />
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
          {can("destructive") && reclaimable > 1024 * 1024 * 1024 && (
            <CleanupPanel
              confirm={confirm}
              onDone={() => {
                disk.refresh()
                health.refresh()
                list.refresh()
              }}
            />
          )}

          {/* Plain, like everything above it: a bar and a legend are a reading. */}
          {disk.data && reclaimable <= 1024 * 1024 * 1024 && (
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
          )}
        </>
      )}

      {dialog}
    </Page>
  )
}

/**
 * One container that is not running, and the one button that changes that.
 *
 * Deliberately not the full verb set: this panel exists to answer "what is
 * down", and a row of five controls would turn it into a second containers
 * page. Start is inline because it is the answer; everything else is one click
 * away in the menu beside it.
 *
 * A `ChoiceRow` rather than a hand-laid `<li>`, because the row *goes*
 * somewhere (§16). The verbs sit in its actions slot, dimmed until the pointer
 * arrives rather than hidden behind it, so they still exist on a touch screen.
 */
function IdleRow({
  container,
  confirm,
  act,
  pending,
  onChanged,
}: {
  container: Container
  confirm: ReturnType<typeof useConfirm>["confirm"]
  act: (c: Container, action: string, progressive: string, phrase?: string) => Promise<void>
  pending?: string
  onChanged: () => void
}) {
  const router = useRouter()
  const href = `/docker/containers?container=${encodeURIComponent(container.id)}`
  const verbs = useContainerVerbs({
    container,
    confirm,
    act,
    // There is no detail panel on this page, so the verbs that open one — logs,
    // a shell — go to the page that has it rather than quietly doing nothing.
    onOpenTab: () => router.push(href),
    onChanged,
  })

  return (
    <ChoiceRow
      href={href}
      verb={`Open ${container.name}`}
      title={container.name}
      description={<span className="font-mono">{container.image}</span>}
      trailing={
        <Status
          state={container.state}
          label={pending ? `${pending}…` : container.status}
          className="shrink-0"
        />
      }
      actions={<ContainerRowActions verbs={verbs} reveal={false} dim />}
      className={pending ? "opacity-70" : undefined}
    />
  )
}

/**
 * The first five minutes.
 *
 * A fresh server shows four zeros and an empty compose list, which is an
 * accurate and completely unhelpful description of a machine somebody has just
 * decided to put something on. The three routes are the same three the create
 * panel opens with, stated here because this is where the question is actually
 * asked.
 *
 * The way out is the page header's own command and is not repeated here: two
 * brand faces on one screen is the "one, not two" failure §16 names, and on an
 * empty server this panel and that button were nine inches apart saying the
 * same word.
 */
function FirstRun() {
  return (
    // Plain, and the three routes are rows rather than three framed cards in
    // a framed panel: on a page that draws no other box, the first thing a
    // new server showed was four of them.
    <Panel plain className="animate-rise">
      <PanelHeader title="Nothing is running on this server yet" />
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

function stackTone(stack: ComposeStack) {
  switch (stack.state) {
    case "running":
      return "running" as const
    case "degraded":
    case "partial":
      return "warning" as const
    case "stopped":
      return "stopped" as const
    default:
      return "unknown" as const
  }
}
