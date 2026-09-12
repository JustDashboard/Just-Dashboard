"use client"

import { useMemo, useState } from "react"
import Link from "next/link"
import { ArrowRight, Heart, Layers, Lifebuoy, Play, Plus, Servers } from "@/components/icons"
import { get } from "@/lib/api"
import { bytes } from "@/lib/format"
import type {
  ComposeStack,
  Container,
  ContainerSpec,
  DockerDiagnosis,
  DockerDiskUsage,
} from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { useConfirm } from "@/components/confirm-dialog"
import { Page, PageHeader, PageState } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { StatusDot } from "@/components/status-dot"
import { EmptyState } from "@/components/state"
import { AttentionPanel, attentionLabel, runtimeLabel } from "@/components/docker/attention"
import { CleanupPanel } from "@/components/docker/cleanup"
import { CreateContainerPanel } from "@/components/docker/create-container"
import { StackStateBadge } from "@/components/docker/stack-state"
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
 */
export default function DockerOverviewPage() {
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  const [creating, setCreating] = useState<ContainerSpec | true | null>(null)

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

  if (list.loading && !list.data) {
    return <PageState eyebrow="Server" title="Docker" />
  }

  const runtime = health.data?.runtime
  const attention = health.data?.attention
  const running = containers.filter((c) => c.state === "running").length
  const reclaimable = disk.data
    ? disk.data.images.reclaimable + disk.data.buildCache.reclaimable
    : 0

  return (
    <Page>
      <PageHeader
        eyebrow="Server"
        title="Docker"
        actions={
          can("service.control") && (
            <Button size="sm" onClick={() => setCreating(true)}>
              <Plus className="size-4" />
              Run a container
            </Button>
          )
        }
      />

      <StatGrid columns={4}>
        <StatTile
          label="Running"
          icon={Play}
          value={`${running} / ${containers.length}`}
          tone={running > 0 ? "success" : "default"}
          hint={
            containers.length === running
              ? "everything on this server is up"
              : `${containers.length - running} not running`
          }
        />

        {/*
          Runtime health, and only runtime health. The hint spells out how many
          containers have no health check at all, because without that number
          "everything is up" quietly includes every container nothing is
          watching.
        */}
        <Link href="/docker/containers" className="block min-w-0">
          <StatTile
            className="h-full transition-colors hover:border-rule-brand"
            label="Runtime health"
            icon={Heart}
            value={runtimeLabel(runtime)}
            tone={
              runtime?.status === "critical"
                ? "danger"
                : runtime?.status === "warning"
                  ? "warning"
                  : "success"
            }
            hint={runtime?.summary ?? "checking"}
          />
        </Link>

        {/*
          Attention is never called health. It counts posture, storage,
          configuration and exposure — none of which stops a service, all of
          which costs something later, and none of which clears itself.
        */}
        <Link href="/docker/containers" className="block min-w-0">
          <StatTile
            className="h-full transition-colors hover:border-rule-brand"
            label="Attention"
            icon={Lifebuoy}
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
        </Link>

        <Link href="/docker/stacks" className="block min-w-0">
          <StatTile
            className="h-full transition-colors hover:border-rule-brand"
            label="Compose stacks"
            icon={Layers}
            value={`${active.length} active`}
            hint={`${detected.length} detected on this server`}
          />
        </Link>
      </StatGrid>

      {/* Problems first. Everything below is context for them. */}
      <AttentionPanel diagnosis={health.data} />

      <Panel>
        <PanelHeader
          icon={Layers}
          title="Compose projects"
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
              description="A stack is a directory with a compose file in it. The dashboard finds them by the labels compose puts on containers, and by looking under the configured compose directories."
            />
          ) : (
            <ul className="divide-y divide-hairline">
              {detected.map((stack) => (
                <li key={stack.name}>
                  <Link
                    href={`/docker/stacks?stack=${encodeURIComponent(stack.name)}`}
                    className="flex min-w-0 items-center justify-between gap-3 px-4 py-2.5 focus-ring-inset hover:bg-row-hover"
                  >
                    <span className="flex min-w-0 items-center gap-2.5">
                      <StatusDot tone={stackTone(stack)} />
                      <span className="truncate text-body font-medium">{stack.name}</span>
                    </span>
                    <StackStateBadge stack={stack} />
                  </Link>
                </li>
              ))}
            </ul>
          )}
        </PanelBody>
      </Panel>

      {/*
        Cleanup appears only when there is something worth reclaiming. A panel
        offering to free 40 MB is a panel that trains people to ignore it.
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

      {disk.data && reclaimable <= 1024 * 1024 * 1024 && (
        <Panel>
          <PanelHeader
            icon={Servers}
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
          <PanelBody>
            <p className="text-body text-muted-foreground">
              {reclaimable > 0
                ? `Only ${bytes(reclaimable)} could be reclaimed — not worth a sweep yet.`
                : "Effectively all of it is in use by something running."}
            </p>
          </PanelBody>
        </Panel>
      )}

      <CreateContainerPanel
        open={creating !== null}
        initialSpec={creating === true ? undefined : (creating ?? undefined)}
        onOpenChange={(open) => !open && setCreating(null)}
        onCreated={() => {
          list.refresh()
          health.refresh()
          stacks.refresh()
        }}
      />
      {dialog}
    </Page>
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
