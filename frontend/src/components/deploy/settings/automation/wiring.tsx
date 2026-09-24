"use client"

import { Fragment, useRef, type RefObject } from "react"
import { Clock, GitBranch, Globe } from "@/components/icons"
import { SourcePull } from "@/components/git/glyphs"
import { describeCron } from "@/lib/cron"
import { cn } from "@/lib/utils"
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
 * Framed, because it is a picture (§2's exception for the deployment section's
 * pictures), in the wiring vocabulary the Credentials, Notifications and
 * General pictures already speak: a pulse travels along a line while that
 * sender is on, the line stands still while it is paused or deploys only by
 * hand, and a dashed ring stands where a kind of sender could be — pressing
 * it opens that sheet. Colour comes only from the senders' own logos, the
 * project's favicon and the brand pulse.
 *
 * Senders are named by what they watch — a repository and branch, a cron
 * sentence — never by the name the list below gives them: the list is where
 * a webhook or a schedule is found by name, and this is a picture of them.
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

  const sources: React.ReactNode[] = []
  if (watching) {
    sources.push(
      <Source
        key="push"
        containerRef={container}
        toRef={project}
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
              {trigger.config.repository}
              {trigger.config.ref && `@${trigger.config.ref}`}
            </span>
          ) : (
            "any signed request"
          )
        }
        // A hook with no repository is already "any signed request".
        hint={
          viaApp(trigger)
            ? "through the GitHub App"
            : trigger.config.repository
              ? "signed"
              : undefined
        }
      />,
    )
  }
  for (const schedule of schedules) {
    sources.push(
      <Source
        key={`schedule-${schedule.id}`}
        containerRef={container}
        toRef={project}
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
        hint="a forge, a CI job, anything that signs"
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
        hint="deploy or restart on a clock"
      />,
    )
  }

  return (
    <SettingPicture
      label="What deploys this project"
      containerRef={container}
      // The frame keeps room under the row for the project's caption, which
      // hangs below its mark. Three senders stand taller than mark and caption
      // together, so the room would only sit the picture high in its frame.
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
                title={<span className="text-muted-foreground">Nothing yet</span>}
                hint="it deploys when you press Deploy"
              />,
            ]
      }
      middle={
        <WireNode
          nodeRef={project}
          align="center"
          mark={<ProjectMark deployment={deployment} size="lg" />}
          title={deployment.name}
          // The commit spelled as the page header spells it.
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
            hint={release ? `Release #${release.number} live` : live ? "live" : "no release yet"}
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
                `${open} preview${open === 1 ? "" : "s"} open`
              ) : (
                <span className="text-muted-foreground">No previews</span>
              )
            }
            hint={
              previewing.length > 0 ? (
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
              ) : (
                "one environment per pull request"
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
 */
function Source({
  containerRef,
  toRef,
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
  carries?: boolean
  placeholder?: boolean
  delay?: number
  mark: React.ReactNode
  eyebrow?: React.ReactNode
  title: React.ReactNode
  hint?: React.ReactNode
}) {
  const node = useRef<HTMLDivElement>(null)
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
        mark={mark}
        eyebrow={eyebrow}
        title={title}
        hint={hint}
      />
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
