"use client"

import { useRef } from "react"
import Link from "next/link"
import { ArrowRight, Box, External, GitBranch, Globe, Layers, Puzzle } from "@/components/icons"
import { relativeTime } from "@/lib/format"
import type {
  DeploymentDomainRoute,
  DeploymentEngineRun,
  DeploymentGitWatch,
  DeploymentRelease,
  DeploymentRuntimeServices,
  DeploymentSummary,
} from "@/lib/types"
import { cn } from "@/lib/utils"
import { Status, type DotTone } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { AnimatedBeam } from "@/components/ui/animated-beam"
import { CertificateStatus } from "@/components/deploy/project-runtime"
import {
  HealthStatus,
  formatDuration,
  hostOf,
  runDurationSeconds,
  type ProjectState,
  type SourceLine,
} from "@/components/deploy/vocabulary"
import { WireMark, WireNode, WirePlaceholder } from "@/components/deploy/wire"

/**
 * How a request reaches this project, drawn as the four things it passes
 * through: the source it was built from, the release that build became, the
 * containers running it, and the names it answers to. The lines between them
 * are the state — a pulse the whole way down while the project is up, a red
 * line where it is unhealthy, a still line while it is stopped, and a dotted
 * one ahead of anything that does not exist yet.
 *
 * These are the same facts the overview used to list under the preview; they
 * are drawn in the order they happen so the column reads as a path rather
 * than a form.
 */
export function ProjectWiring({
  projectId,
  deployment,
  state,
  source,
  liveRelease,
  liveRun,
  runtime,
  domains,
  url,
  watch,
}: {
  projectId: number
  deployment: DeploymentSummary
  state: ProjectState
  source: SourceLine
  liveRelease?: DeploymentRelease
  liveRun?: DeploymentEngineRun
  runtime?: DeploymentRuntimeServices
  /** The routes the operations owner reports, when it could read them. */
  domains?: DeploymentDomainRoute[]
  url?: string
  watch?: DeploymentGitWatch
}) {
  const container = useRef<HTMLDivElement>(null)
  const sourceMark = useRef<HTMLDivElement>(null)
  const releaseMark = useRef<HTMLDivElement>(null)
  const runtimeMark = useRef<HTMLDivElement>(null)
  const domainMark = useRef<HTMLDivElement>(null)

  const up = state === "ready" || state === "deploying"
  const broken = state === "failed" || state === "unhealthy"
  const services =
    runtime?.status === "available"
      ? runtime.services.filter((service) => service.liveRelease)
      : undefined
  const running = services?.filter((service) => service.state === "running") ?? []
  const host = url && hostOf(url)
  const SourceIcon =
    source.kind === "compose"
      ? Layers
      : source.kind === "blueprint"
        ? Puzzle
        : source.kind === "image" || source.kind === "import"
          ? Box
          : GitBranch

  return (
    <div ref={container} className="relative">
      <AnimatedBeam
        containerRef={container}
        fromRef={sourceMark}
        toRef={releaseMark}
        still={!up}
        dashed={!liveRelease}
        tone={liveRelease && !up ? "success" : "default"}
        duration={2.4}
      />
      <AnimatedBeam
        containerRef={container}
        fromRef={releaseMark}
        toRef={runtimeMark}
        still={!up}
        dashed={!liveRelease}
        tone={broken ? "danger" : liveRelease && !up ? "success" : "default"}
        duration={2.4}
        delay={0.5}
      />
      <AnimatedBeam
        containerRef={container}
        fromRef={runtimeMark}
        toRef={domainMark}
        still={!up || !host}
        dashed={!host}
        tone={broken ? "danger" : host && liveRelease && !up ? "success" : "default"}
        duration={2.4}
        delay={1}
      />

      <ol className="flex flex-col gap-5" aria-label="How the project is wired">
        <li>
          <WireNode
            nodeRef={sourceMark}
            mark={
              <WireMark size="md">
                <SourceIcon />
              </WireMark>
            }
            eyebrow="Source"
            title={<span className="truncate">{source.primary}</span>}
            hint={
              <>
                {source.secondary && (
                  <span className={cn("block truncate", source.mono && "font-mono")}>
                    {source.secondary}
                  </span>
                )}
                {deployment.sourceKind === "git" && (
                  <span className="mt-0.5 flex flex-wrap items-center gap-x-3 gap-y-1">
                    {watch ? <AutoDeploy watch={watch} /> : <span>Auto-deploy —</span>}
                    <Link
                      href={`/deploy/${projectId}/settings/general`}
                      className="inline-flex items-center gap-1 rounded-sm focus-ring hover:text-foreground"
                    >
                      Manage <ArrowRight className="size-3" />
                    </Link>
                  </span>
                )}
              </>
            }
          />
        </li>

        <li>
          <WireNode
            nodeRef={releaseMark}
            mark={
              liveRelease ? (
                <WireMark size="md" tone="brand">
                  <span className="numeric text-xs font-semibold">#{liveRelease.number}</span>
                </WireMark>
              ) : (
                <WirePlaceholder size="md">
                  <span className="text-xs font-semibold">#</span>
                </WirePlaceholder>
              )
            }
            eyebrow="Live release"
            title={
              liveRelease ? (
                <span className="numeric">Release #{liveRelease.number}</span>
              ) : (
                <span className="text-muted-foreground">Not deployed yet</span>
              )
            }
            hint={
              liveRelease && liveRun ? (
                <>
                  {liveRun.operation === "rollback" ? "rolled back" : "deployed"}{" "}
                  {relativeTime(liveRun.endedAt ?? liveRun.requestedAt)}
                  {liveRun.actor && ` by ${liveRun.actor}`}
                  {" · "}
                  {formatDuration(runDurationSeconds(liveRun))}
                </>
              ) : liveRelease ? undefined : (
                "The first deployment records it here"
              )
            }
          />
        </li>

        <li>
          <WireNode
            nodeRef={runtimeMark}
            mark={
              services ? (
                <WireMark
                  size="md"
                  tone={
                    broken
                      ? "danger"
                      : running.length > 0 && state === "ready"
                        ? "success"
                        : "neutral"
                  }
                >
                  <Box />
                </WireMark>
              ) : (
                <WirePlaceholder size="md">
                  <Box />
                </WirePlaceholder>
              )
            }
            eyebrow="Runtime"
            title={
              services ? (
                services.length === 0 ? (
                  <span className="text-muted-foreground">No containers</span>
                ) : (
                  <span className="numeric">
                    {running.length} of {services.length}{" "}
                    {services.length === 1 ? "container" : "containers"} running
                  </span>
                )
              ) : (
                <span className="text-muted-foreground">Not observed</span>
              )
            }
            hint={
              <span className="flex flex-wrap items-center gap-x-3 gap-y-1">
                <HealthStatus health={deployment.health} />
                {services && services.length > 0 && (
                  <span className="truncate font-mono">
                    {services.map((service) => service.name).join(", ")}
                  </span>
                )}
                {!services && runtime?.reason && <span className="truncate">{runtime.reason}</span>}
              </span>
            }
          />
        </li>

        <li>
          <WireNode
            nodeRef={domainMark}
            mark={
              host ? (
                <WireMark size="md">
                  <Globe />
                </WireMark>
              ) : (
                <WirePlaceholder size="md">
                  <Globe />
                </WirePlaceholder>
              )
            }
            eyebrow="Domains"
            title={
              domains ? (
                domains.length === 0 ? (
                  <span className="text-muted-foreground">No public domain</span>
                ) : (
                  <ul className="space-y-1">
                    {domains.map((domain) => (
                      <li
                        key={domain.hostname}
                        className="flex min-w-0 flex-wrap items-center gap-2"
                      >
                        <a
                          href={`${domain.https ? "https" : "http"}://${domain.hostname}/`}
                          target="_blank"
                          rel="noopener noreferrer"
                          className="inline-flex min-w-0 items-center gap-1 rounded-sm focus-ring hover:underline"
                        >
                          <span className="truncate">{domain.hostname}</span>
                          <External aria-hidden className="size-3 shrink-0 text-muted-foreground" />
                        </a>
                        <CertificateStatus domain={domain} />
                        {domain.protected && <Tag>Password</Tag>}
                      </li>
                    ))}
                  </ul>
                )
              ) : host ? (
                <a
                  href={url}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="inline-flex min-w-0 items-center gap-1 rounded-sm focus-ring hover:underline"
                >
                  <span className="truncate">{host}</span>
                  <External aria-hidden className="size-3 shrink-0 text-muted-foreground" />
                </a>
              ) : (
                <span className="text-muted-foreground">
                  {deployment.liveReleaseId
                    ? "Private service"
                    : "Appears after your first deployment"}
                </span>
              )
            }
          />
        </li>
      </ol>
    </div>
  )
}

function autoDeployReading(watch: DeploymentGitWatch): { tone: DotTone; label: string } {
  const automatic = watch.policy?.automatic ?? watch.automatic
  if (["unavailable", "stale", "policy_conflict"].includes(watch.status)) {
    return { tone: "warning", label: "Auto-deploy needs attention" }
  }
  if (!automatic) return { tone: "stopped", label: "Manual deployments" }
  if (watch.status === "awaiting_first_deployment") {
    return { tone: "stopped", label: "Auto-deploy after first deployment" }
  }
  return { tone: "running", label: `Auto-deploy on · every ${watch.intervalSeconds}s` }
}

function AutoDeploy({ watch }: { watch: DeploymentGitWatch }) {
  const reading = autoDeployReading(watch)
  return <Status tone={reading.tone} label={reading.label} />
}
