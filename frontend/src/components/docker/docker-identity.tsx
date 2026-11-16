"use client"

import { Box, type Icon } from "@/components/icons"
import type { HostInfo } from "@/lib/types"
import { Fact } from "@/components/fact-line"
import { IdentityHeader } from "@/components/identity-header"
import { platformProduct } from "@/components/product-logo"
import { WorkspaceHelp } from "@/components/workspace/workspace"
import { useMetricsValue } from "@/hooks/use-metrics"
import type { MetricsState } from "@/lib/metrics/store"

/**
 * The engine's name, as every Docker page says it: "Docker" and its version
 * after it in the weight of a fact. Four pages had three names for one daemon
 * ("Docker 29.2.0", "Docker Engine 29.2.0" and the server's hostname).
 */
export function DockerTitle({ version }: { version?: string }) {
  return (
    <span className="inline-flex min-w-0 items-baseline gap-2">
      Docker{" "}
      {version && (
        <span className="truncate font-mono text-body font-normal text-muted-foreground">
          {version}
        </span>
      )}
    </span>
  )
}

/**
 * The line every Docker section page opens on, laid out as a project's header
 * (`IdentityHeader` with `state`): Docker as the mark, the page's name, its
 * verdict beside the name, Docker and its version as the first fact, the
 * server it runs on after it and the page's own facts after that, and at the
 * far end the page's one command with the shortcuts before it.
 *
 * The page's name is the title (PGH-16). The list pages opened on "Docker
 * 29.2.0" one after another, the h1 was for screen readers only, and with the
 * phone's sidebar closed nothing above the fold said which page this was. The
 * Overview, which is Docker itself, passes no `page` and keeps Docker as its
 * name.
 *
 * It is one component so the rule is written once: the Overview, Containers,
 * Stacks, Images, Volumes, Networks and Events all open on it.
 */
export function DockerIdentity({
  page,
  version,
  host,
  hostDetail,
  fallback = Box,
  state,
  facts,
  controls,
  command,
  menu,
  className,
}: {
  /** The page's name ("Containers"), as the title; Docker becomes the first fact. */
  page?: string
  version?: string
  /** The server, drawn as a fact from `sm`; a phone keeps the line to the page's own. */
  host?: Pick<HostInfo, "hostname" | "platform"> | { hostname?: string; platform?: string }
  /** What the server is, after its name: its distribution, on the page that says it. */
  hostDetail?: string
  /** The glyph on the tile when Docker's own logo cannot be drawn. */
  fallback?: Icon
  /** The page's verdict, a `Status` or a `VerdictButton` holding one. */
  state?: React.ReactNode
  facts: React.ReactNode
  /** What changes the page's reading — a window — before the command. */
  controls?: React.ReactNode
  /** The page's one brand-faced command. */
  command?: React.ReactNode
  menu?: React.ReactNode
  className?: string
}) {
  return (
    <IdentityHeader
      className={className}
      mark="docker"
      fallback={fallback}
      title={page ?? <DockerTitle version={version} />}
      state={
        state ?? (
          // The verdict's line, held open: on a phone it is a row of its own,
          // and the blocks under the line must not move when it lands.
          <span aria-hidden className="text-xs">
            &nbsp;
          </span>
        )
      }
      facts={
        <>
          {page && <Fact>{version ? `Docker ${version}` : "Docker"}</Fact>}
          {host?.hostname && (
            <Fact product={platformProduct(host.platform)} className="max-sm:hidden">
              {host.hostname}
              {hostDetail && ` · ${hostDetail}`}
            </Fact>
          )}
          {facts}
        </>
      }
      command={
        <>
          <span className="max-sm:hidden">
            <WorkspaceHelp compact />
          </span>
          {controls}
          {command}
        </>
      }
      menu={menu}
    />
  )
}

const selectHost = (state: MetricsState) => state.host

/**
 * The server, for a Docker page that reads nothing else from the metrics
 * stream: it re-renders when the host changes, not on every frame.
 */
export function useDockerHost() {
  return useMetricsValue(selectHost) ?? undefined
}

/**
 * A verdict that narrows or opens something when pressed. A flex box, so the
 * button is as tall as the dot and word in it and the line does not grow when
 * the verdict lands.
 */
export function VerdictButton({
  pressed,
  label,
  onPress,
  children,
}: {
  pressed?: boolean
  label?: string
  onPress: () => void
  children: React.ReactNode
}) {
  return (
    <button
      type="button"
      aria-pressed={pressed}
      aria-label={label}
      onClick={onPress}
      className="inline-flex items-center rounded-sm focus-ring hover:underline"
    >
      {children}
    </button>
  )
}
