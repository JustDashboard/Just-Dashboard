"use client"

import { Fragment, useRef, useState, type RefObject } from "react"
import { Clock, GitBranch, Globe } from "@/components/icons"
import { SourcePull } from "@/components/git/glyphs"
import { describeCron } from "@/lib/cron"
import { cn } from "@/lib/utils"
import { useArrivals } from "@/hooks/use-arrivals"
import type {
  DeploymentGitWatch,
  DeploymentPreview,
  DeploymentRelease,
  DeploymentSchedule,
  DeploymentSummary,
  DeploymentTrigger,
} from "@/lib/types"
import { ProductGlyph, hasProductLogo } from "@/components/product-logo"
import { AnimatedBeam } from "@/components/ui/animated-beam"
import { hostOf, humanize, releaseLabel, shortRevision } from "@/components/deploy/vocabulary"
import { ProjectMark } from "@/components/deploy/project-mark"
import { WireLink, WireMark, WireNode, WirePlaceholder } from "@/components/deploy/wire"
import { SettingPicture } from "@/components/deploy/settings/setting-picture"
import {
  actionOf,
  providerLabel,
  triggerProduct,
  viaApp,
} from "@/components/deploy/settings/automation/marks"

type Ref = RefObject<HTMLDivElement | null>

/**
 * What deploys the project by itself: the watched branch, each webhook and
 * each schedule on one side, the project in the middle, and what comes out
 * of it — the production release and, where a webhook asks for them, the
 * pull-request previews — on the other.
 *
 * On the page's own ground over the dot grid (`SettingPicture`), in the
 * wiring vocabulary the Credentials, Notifications and General pictures
 * already speak: a pulse travels along a line while that sender is on, the
 * line stands still while it is paused or deploys only by hand, and a dashed
 * ring stands where a kind of sender could be — pressing it opens that sheet.
 * Colour comes only from the senders' own logos, the project's favicon and
 * the brand pulse.
 *
 * Senders are named by what they watch — a repository and branch, a cron
 * sentence — never by the name the list below gives them: the list is where
 * a webhook or a schedule is found by name, and this is a picture of them.
 *
 * A ring is its mark and its word, with no line under it about what such a
 * sender is: the sections below open on the same kinds as cards that say it,
 * and a picture of a project with nothing wired was mostly those captions.
 * What changes in place — a sender switched on or off, the release that went
 * live, how many previews are open — rises into its new state rather than
 * being repainted, and a sender added from its sheet rises in.
 */
export function AutomationWiring({
  deployment,
  release,
  gitWatch,
  gitProduct,
  repository,
  triggers,
  schedules,
  previews,
  onAddWebhook,
  onAddSchedule,
  onTurnOnPreviews,
}: {
  deployment: DeploymentSummary
  release?: DeploymentRelease
  gitWatch?: DeploymentGitWatch
  /** The forge the project's own source lives on, drawn on the push node. */
  gitProduct?: string
  repository?: string
  triggers: DeploymentTrigger[]
  schedules: DeploymentSchedule[]
  previews: DeploymentPreview[]
  /** Present for an administrator: the dashed rings open the sheets. */
  onAddWebhook?: () => void
  onAddSchedule?: () => void
  onTurnOnPreviews?: () => void
}) {
  const container = useRef<HTMLDivElement>(null)
  const project = useRef<HTMLDivElement>(null)
  const production = useRef<HTMLDivElement>(null)
  const pulls = useRef<HTMLDivElement>(null)
  const watching = gitWatch && gitWatch.status !== "not_applicable" ? gitWatch : undefined
  const previewing = triggers.filter((trigger) => trigger.config.preview)
  const patterns = [
    ...new Set(
      previewing
        .map((trigger) => trigger.config.previewDomain)
        .filter((domain): domain is string => Boolean(domain)),
    ),
  ]
  const open = previews.filter((preview) => preview.state === "open").length
  const live = Boolean(deployment.liveReleaseId)
  // A sender rises only when it was not drawn a moment ago — one added from
  // its sheet, or a list that landed after the picture — and not each one as
  // the picture itself rises in with the page, which moved them twice.
  const arrived = useArrivals([
    ...(watching ? ["push"] : []),
    ...triggers.map((trigger) => `trigger-${trigger.id}`),
    ...schedules.map((schedule) => `schedule-${schedule.id}`),
  ])

  const sources: React.ReactNode[] = []
  if (watching) {
    sources.push(
      <Source
        key="push"
        containerRef={container}
        toRef={project}
        arrived={arrived.has("push")}
        carries={watching.automatic && watching.status === "watching"}
        mark={
          <WireMark tone="logo" size="md">
            {gitProduct && hasProductLogo(gitProduct) ? (
              <ProductGlyph id={gitProduct} />
            ) : (
              <GitBranch />
            )}
          </WireMark>
        }
        eyebrow="On push"
        title={<span className="font-mono">{watching.branch ?? "the branch"}</span>}
        hint={[
          repository,
          watching.automatic
            ? `checked every ${watching.intervalSeconds}s`
            : "deploys by hand only",
        ]
          .filter(Boolean)
          .join(" · ")}
      />,
    )
  }
  for (const trigger of triggers) {
    const product = triggerProduct(trigger)
    sources.push(
      <Source
        key={`trigger-${trigger.id}`}
        containerRef={container}
        toRef={project}
        arrived={arrived.has(`trigger-${trigger.id}`)}
        carries={trigger.enabled}
        delay={(trigger.id % 4) * 0.35}
        mark={
          <WireMark tone="logo" size="md">
            <ProductGlyph id={product} className={cn(!trigger.enabled && "opacity-50 grayscale")} />
          </WireMark>
        }
        eyebrow={providerLabel(trigger)}
        title={
          trigger.config.repository ? (
            <span className="font-mono">
              <Repository name={trigger.config.repository} at={trigger.config.ref} />
            </span>
          ) : (
            "any signed request"
          )
        }
        // Only the exception is said: every other webhook is signed, so
        // "signed" under each one told the reader nothing about any of them.
        hint={viaApp(trigger) ? "through the GitHub App" : undefined}
      />,
    )
  }
  for (const schedule of schedules) {
    sources.push(
      <Source
        key={`schedule-${schedule.id}`}
        containerRef={container}
        toRef={project}
        arrived={arrived.has(`schedule-${schedule.id}`)}
        carries={schedule.enabled}
        delay={(schedule.id % 4) * 0.35 + 0.2}
        mark={
          <WireMark tone="logo" size="md">
            <Clock className={cn(!schedule.enabled && "opacity-50")} />
          </WireMark>
        }
        eyebrow="On a clock"
        title={describeCron(schedule.expression)}
        hint={`${schedule.steps
          .map((step) => actionOf(step.action)?.label ?? humanize(step.action))
          .join(" → ")} · ${schedule.timezone}`}
      />,
    )
  }
  if (onAddWebhook && triggers.length === 0) {
    sources.push(
      <Source
        key="add-webhook"
        containerRef={container}
        toRef={project}
        placeholder
        mark={
          <WireLink label="Add webhook" onClick={onAddWebhook}>
            <WirePlaceholder size="md" product="webhook" />
          </WireLink>
        }
        title={<span className="text-muted-foreground">Webhook</span>}
      />,
    )
  }
  if (onAddSchedule && schedules.length === 0) {
    sources.push(
      <Source
        key="add-schedule"
        containerRef={container}
        toRef={project}
        placeholder
        mark={
          <WireLink label="Add schedule" onClick={onAddSchedule}>
            <WirePlaceholder size="md" fallback={Clock} />
          </WireLink>
        }
        title={<span className="text-muted-foreground">Schedule</span>}
      />,
    )
  }

  return (
    <SettingPicture
      label="What deploys this project"
      containerRef={container}
      // The picture keeps room under the row for the project's caption, which
      // hangs below its mark. Three senders stand taller than mark and caption
      // together, so the room would only leave a band of grid under them.
      className={sources.length >= 3 ? "lg:[&_ol]:pb-0" : undefined}
      lines={
        <>
          <AnimatedBeam
            containerRef={container}
            fromRef={project}
            toRef={production}
            still={!live}
            dashed={!live}
            duration={2.6}
            delay={0.6}
          />
          <AnimatedBeam
            containerRef={container}
            fromRef={project}
            toRef={pulls}
            still={open === 0}
            dashed={previewing.length === 0}
            duration={2.6}
            delay={1.1}
          />
        </>
      }
      start={
        sources.length > 0
          ? sources
          : [
              <WireNode
                key="none"
                align="end"
                mark={
                  <WirePlaceholder size="md">
                    <GitBranch />
                  </WirePlaceholder>
                }
                // The head over the picture says what deploys it instead.
                title={<span className="text-muted-foreground">Nothing yet</span>}
              />,
            ]
      }
      middle={
        <WireNode
          nodeRef={project}
          align="center"
          mark={<ProjectMark deployment={deployment} size="lg" />}
          title={deployment.name}
          // The commit spelled as the project identity line spells it.
          hint={
            <span className="font-mono">
              {(deployment.liveReleaseId &&
                shortRevision(deployment.sourceRevision || deployment.sourceRef)) ||
                releaseLabel(deployment)}
            </span>
          }
        />
      }
      end={
        <div className="flex flex-col gap-5 lg:gap-4">
          <WireNode
            nodeRef={production}
            mark={
              <WireMark tone="logo" size="md">
                <Globe className={cn(!live && "opacity-50")} />
              </WireMark>
            }
            eyebrow={deployment.environmentName || "Production"}
            title={
              <span className="font-mono">
                {hostOf(deployment.endpoint) ?? (deployment.endpoint || "private")}
              </span>
            }
            hint={
              <Swap>
                {release ? `Release #${release.number} live` : live ? "live" : "no release yet"}
              </Swap>
            }
          />
          <WireNode
            nodeRef={pulls}
            mark={
              previewing.length > 0 ? (
                <WireMark tone="logo" size="md">
                  <SourcePull />
                </WireMark>
              ) : onTurnOnPreviews ? (
                <WireLink label="Turn on previews" onClick={onTurnOnPreviews}>
                  <WirePlaceholder size="md" fallback={SourcePull} />
                </WireLink>
              ) : (
                <WirePlaceholder size="md" fallback={SourcePull} />
              )
            }
            eyebrow="Pull requests"
            title={
              previewing.length > 0 ? (
                <Swap>{`${open} preview${open === 1 ? "" : "s"} open`}</Swap>
              ) : (
                <span className="text-muted-foreground">No previews</span>
              )
            }
            hint={
              previewing.length > 0 && (
                <span className="font-mono break-words">
                  {patterns.length > 0
                    ? patterns.map((pattern, index) => (
                        <Fragment key={pattern}>
                          {index > 0 && ", "}
                          <Hostname value={pattern} />
                        </Fragment>
                      ))
                    : "no address"}
                </span>
              )
            }
          />
        </div>
      }
    />
  )
}

/**
 * One sender on the picture's start edge and its line to the project: a
 * pulse while it is on and carrying, still while paused or manual, dotted for
 * a ring that holds nothing yet.
 *
 * Its mark and words rise when it arrives, and the mark again when it is
 * switched on or off. The motion is on what the node holds and never on the
 * node itself: the lines are measured from the node's box, and a box caught
 * four pixels into its rise drew a line that ended beside the mark.
 */
function Source({
  containerRef,
  toRef,
  arrived = false,
  carries,
  placeholder,
  delay = 0,
  mark,
  eyebrow,
  title,
  hint,
}: {
  containerRef: Ref
  toRef: Ref
  /** Not drawn a moment ago. */
  arrived?: boolean
  carries?: boolean
  placeholder?: boolean
  delay?: number
  mark: React.ReactNode
  eyebrow?: React.ReactNode
  title: React.ReactNode
  hint?: React.ReactNode
}) {
  const node = useRef<HTMLDivElement>(null)
  // Whether it has been switched since it was drawn, adjusted during render
  // as `useArrivals` is: the mark's first state is not news, its next ones are.
  const [drawn, setDrawn] = useState({ carries, switched: false })
  if (drawn.carries !== carries) setDrawn({ carries, switched: true })
  const words = cn("block", arrived && "animate-rise")
  return (
    <>
      <AnimatedBeam
        containerRef={containerRef}
        fromRef={node}
        toRef={toRef}
        still={!carries}
        dashed={placeholder}
        duration={2.4}
        delay={delay}
      />
      <WireNode
        nodeRef={node}
        align="end"
        mark={
          <span
            key={carries ? "on" : "off"}
            className={cn("flex", (arrived || drawn.switched) && "animate-rise")}
          >
            {mark}
          </span>
        }
        eyebrow={eyebrow && <span className={words}>{eyebrow}</span>}
        title={<span className={words}>{title}</span>}
        hint={hint && <span className={words}>{hint}</span>}
      />
    </>
  )
}

/**
 * Words that change in place — the release that went live, how many previews
 * are open — rising into the new ones rather than being repainted (§11). The
 * first words are not news; they arrive with the picture.
 */
function Swap({ children }: { children: string }) {
  const [shown, setShown] = useState({ words: children, changed: false })
  if (shown.words !== children) setShown({ words: children, changed: true })
  return (
    <span key={children} className={cn("block", shown.changed && "animate-rise")}>
      {children}
    </span>
  )
}

/**
 * A repository and the ref it deploys, wrapping after a slash or before the
 * `@` rather than inside a name: the start edge is as narrow as the picture's
 * first column, and `owner/name@ref` set in one piece is often wider.
 */
function Repository({ name, at }: { name: string; at?: string }) {
  return (
    <>
      {name.split(/(?<=\/)/).map((part, index) => (
        <Fragment key={index}>
          {index > 0 && <wbr />}
          {part}
        </Fragment>
      ))}
      {at && (
        <>
          <wbr />@{at}
        </>
      )}
    </>
  )
}

/** A hostname that wraps only between its labels, never inside one. */
function Hostname({ value }: { value: string }) {
  return value.split(".").map((label, index, all) => (
    <Fragment key={index}>
      {label}
      {index < all.length - 1 && (
        <>
          .<wbr />
        </>
      )}
    </Fragment>
  ))
}
