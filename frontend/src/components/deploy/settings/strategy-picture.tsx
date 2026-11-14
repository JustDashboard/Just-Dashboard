"use client"

import { useRef } from "react"
import { Box } from "@/components/icons"
import { AnimatedBeam } from "@/components/ui/animated-beam"
import { useProject } from "@/components/deploy/project-context"
import { ProjectFavicon } from "@/components/deploy/project-mark"
import { WireHost, WireMark, WireNode, WirePlaceholder } from "@/components/deploy/wire"
import { SettingPicture } from "@/components/deploy/settings/setting-picture"

/**
 * What a release strategy does with the two releases a deployment has for a
 * moment: the live one and the next, and which of them the route sends
 * requests to.
 *
 * The three strategies were three cards of two-line sentences, and the
 * difference between them is a shape: both on the route with the new one
 * waiting for its checks, both on the route with the new one on a share, or
 * one after the other with nothing on the route in between. A line that is
 * drawn solid carries requests under that strategy; a dashed one does not yet,
 * or no longer.
 *
 * Keyed by the caller on the strategy, so choosing another card brings its
 * picture in (§11) and the lines are measured against the words it has.
 */
export function StrategyPicture({
  strategy,
  share,
  window,
  hostname,
}: {
  strategy: "blue_green" | "canary" | "stop_first"
  /** A canary's share of the requests, in percent. */
  share?: number
  /** A canary's window, in words: "10 minutes". */
  window?: string
  /** The first domain the route answers on, when there is one. */
  hostname?: string
}) {
  const project = useProject()
  const container = useRef<HTMLDivElement>(null)
  const liveMark = useRef<HTMLDivElement>(null)
  const nextMark = useRef<HTMLDivElement>(null)
  const routeMark = useRef<HTMLDivElement>(null)
  const live = project.liveRelease
  const words = {
    blue_green: {
      live: "answers until the new one is ready",
      next: "takes every request once its checks pass",
    },
    canary: {
      live: `keeps ${100 - (share ?? 0)}% of the requests`,
      next: `gets ${share ?? 0}%${window ? ` for ${window}` : ""}, then all or none`,
    },
    stop_first: {
      live: "stopped first",
      next: "starts after it: a moment of downtime",
    },
  }[strategy]

  return (
    <SettingPicture
      label="Which release the route sends requests to during a deployment"
      containerRef={container}
      className="py-2"
      lines={
        <>
          <AnimatedBeam
            containerRef={container}
            fromRef={liveMark}
            toRef={routeMark}
            still
            dashed={strategy === "stop_first"}
          />
          <AnimatedBeam
            containerRef={container}
            fromRef={nextMark}
            toRef={routeMark}
            still
            dashed={strategy === "blue_green"}
          />
        </>
      }
      start={[
        <WireNode
          key="live"
          nodeRef={liveMark}
          align="end"
          mark={
            live ? (
              <WireMark tone="logo" size="md">
                <ProjectFavicon deployment={project.detail.deployment} fallback={<Box />} />
              </WireMark>
            ) : (
              <WirePlaceholder size="md">
                <Box />
              </WirePlaceholder>
            )
          }
          eyebrow="Live"
          title={live ? <span className="numeric">Release #{live.number}</span> : "No release yet"}
          hint={words.live}
        />,
        <WireNode
          key="next"
          nodeRef={nextMark}
          align="end"
          mark={
            <WirePlaceholder size="md">
              <Box />
            </WirePlaceholder>
          }
          eyebrow="Next"
          title="The new release"
          hint={words.next}
        />,
      ]}
      end={
        <WireNode
          nodeRef={routeMark}
          align="start"
          mark={<WireHost size="md" />}
          eyebrow="Route"
          title={
            hostname ? <span className="block truncate font-mono">{hostname}</span> : "This server"
          }
          hint="where requests arrive"
        />
      }
    />
  )
}
