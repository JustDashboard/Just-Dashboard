"use client"

import { Box } from "@/components/icons"
import type { HostInfo } from "@/lib/types"
import { FactDot, HostFact, HostIdentity } from "@/components/metrics/host-identity"
import { platformProduct } from "@/components/product-logo"
import { WorkspaceHelp } from "@/components/workspace/workspace"

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
 * (`HostIdentity` with `state`): Docker as the mark, its name and version, the
 * page's verdict beside the name, the server it runs on as the first fact and
 * the page's own facts after it, and at the far end the page's one command
 * with the shortcuts before it.
 *
 * It is one component so the rule is written once. The overview, Containers,
 * Events and Stacks draw it; Images, Volumes and Networks open the same way
 * and can take it as they are.
 */
export function DockerIdentity({
  version,
  host,
  hostDetail,
  state,
  facts,
  controls,
  command,
  menu,
  className,
}: {
  version?: string
  /** The server, drawn as the first fact from `sm`; a phone keeps the line to the page's own. */
  host?: Pick<HostInfo, "hostname" | "platform"> | { hostname?: string; platform?: string }
  /** What the server is, after its name: its distribution, on the page that says it. */
  hostDetail?: string
  /** The page's verdict, a `Status` or a button holding one. */
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
    <HostIdentity
      className={className}
      mark="docker"
      fallback={Box}
      title={<DockerTitle version={version} />}
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
          {host?.hostname && (
            <span className="contents max-sm:hidden">
              <HostFact product={platformProduct(host.platform)}>
                {host.hostname}
                {hostDetail && ` · ${hostDetail}`}
              </HostFact>
              <FactDot />
            </span>
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
