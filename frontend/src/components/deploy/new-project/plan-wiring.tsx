"use client"

import { useRef } from "react"
import { Cpu, Globe, Wrench } from "@/components/icons"
import type { DeploymentConfiguration, DeploymentDraftSource, WorkloadProfile } from "@/lib/types"
import { AnimatedBeam } from "@/components/ui/animated-beam"
import {
  SOURCE_KIND_LABELS,
  SourceMark,
  frameworkLabel,
  humanize,
  isGitHubSource,
} from "@/components/deploy/vocabulary"
import { WireMark, WireNode, WirePlaceholder } from "@/components/deploy/wire"

/**
 * What this setup is about to create, drawn as the four things a request will
 * pass through once it exists: the source it is built from, the build that
 * produces the artifact, the container that runs it and the name it answers
 * to.
 *
 * It is the same picture, in the same vocabulary, that the project overview
 * draws for a deployment that already exists (`project-wiring.tsx`) — so the
 * screen where a project is planned and the screen where it is read agree
 * about what a project *is*, and the reader who has understood one has
 * understood the other.
 *
 * Nothing pulses. A pulse means live traffic on the overview, and there is no
 * traffic on a plan; a step that has not been decided yet is a dashed line to
 * a dashed ring, exactly as an undeployed project's release is there. Every
 * node is pressable and opens the section that decides it, which is what turns
 * the picture into the form's own table of contents — the memory limit and the
 * release strategy are readable here without opening Advanced to find them.
 */
/**
 * One step of the plan, pressable.
 *
 * Not `WireLink`: that one is an `inline-block`, which shrink-to-fits to its
 * content — and a node whose title is a non-wrapping truncated hostname has a
 * min-content width of the whole hostname, so the button grew past this
 * column and the text ran off the page instead of clipping inside it. A block
 * that is told to be the column's width lets `min-w-0` reach the span that
 * does the truncating.
 */
function PlanStep({
  label,
  onClick,
  children,
}: {
  label: string
  onClick: () => void
  children: React.ReactNode
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-label={label}
      className="block w-full min-w-0 rounded-md text-left focus-ring transition-colors hover:text-foreground"
    >
      {children}
    </button>
  )
}

export function PlanWiring({
  profile,
  source,
  sourceLabel,
  branch,
  framework,
  configuration,
  onOpenSection,
}: {
  profile: WorkloadProfile
  source: DeploymentDraftSource
  sourceLabel: string
  branch?: string
  framework?: string
  configuration: DeploymentConfiguration
  /** Jumps to the fields that decide one of the four steps. */
  onOpenSection: (section: "source" | "build" | "runtime" | "address") => void
}) {
  const container = useRef<HTMLDivElement>(null)
  const sourceMark = useRef<HTMLDivElement>(null)
  const buildMark = useRef<HTMLDivElement>(null)
  const runtimeMark = useRef<HTMLDivElement>(null)
  const addressMark = useRef<HTMLDivElement>(null)

  const build = configuration.build
  const runtime = configuration.runtime
  const domain = configuration.domains[0]
  const domainCount = configuration.domains.length
  const worker = profile === "worker"

  // A build method of "image" or "none" is not a build: the artifact is
  // pulled or adopted, and saying "no build" is more use than naming a method
  // that never runs.
  const builds = build.method !== "image" && build.method !== "none"
  const buildTitle = builds
    ? build.method === "recipe"
      ? `Automatic recipe${build.recipe ? ` · ${frameworkLabel(framework ?? build.recipe)}` : ""}`
      : humanize(build.method)
    : source.kind === "import"
      ? "Adopted as it runs"
      : "No build"
  const buildHint = builds
    ? build.method === "dockerfile"
      ? build.dockerfile || "Dockerfile"
      : build.buildCommand || "No build command"
    : source.kind === "import"
      ? "The workload is recorded, never rebuilt"
      : "The image is pulled at its digest"

  const limits = [
    runtime.memoryMb ? `${runtime.memoryMb} MB` : undefined,
    runtime.cpus ? `${runtime.cpus} CPU` : undefined,
  ].filter(Boolean)
  const port = runtime.internalPort ?? 0

  return (
    <div ref={container} className="relative">
      <AnimatedBeam
        containerRef={container}
        fromRef={sourceMark}
        toRef={buildMark}
        still
        dashed={!builds}
      />
      <AnimatedBeam
        containerRef={container}
        fromRef={buildMark}
        toRef={runtimeMark}
        still
        dashed={port === 0 && !worker}
      />
      <AnimatedBeam
        containerRef={container}
        fromRef={runtimeMark}
        toRef={addressMark}
        still
        dashed={!domain}
      />

      <ol className="flex flex-col gap-5" aria-label="What this setup will create">
        <li>
          <PlanStep label="Change the source" onClick={() => onOpenSection("source")}>
            <WireNode
              nodeRef={sourceMark}
              mark={
                /* The mark the source was chosen under, drawn from the one
                   mapping the chooser also reads: this node used to pick its
                   own, so a repository chosen under GitHub's mark was redrawn
                   here as a share glyph. */
                /* `ink` is the tone this vocabulary keeps for a third
                   party's own mark, and it is how the Credentials picture
                   already draws GitHub: the octocat is a filled silhouette,
                   and in the neutral plate — which is sized for a 5px-stroke
                   outline glyph — it read as a smudge rather than as a logo. */
                <WireMark size="md" tone={isGitHubSource(source) ? "ink" : "neutral"}>
                  <SourceMark source={source} />
                </WireMark>
              }
              eyebrow="Source"
              title={<span className="block truncate">{sourceLabel}</span>}
              hint={
                <span className="block truncate">
                  {branch ? (
                    <span className="font-mono">{branch}</span>
                  ) : (
                    SOURCE_KIND_LABELS[source.kind]
                  )}
                  {framework && ` · ${frameworkLabel(framework)}`}
                </span>
              }
            />
          </PlanStep>
        </li>

        <li>
          <PlanStep label="Change the build settings" onClick={() => onOpenSection("build")}>
            <WireNode
              nodeRef={buildMark}
              mark={
                builds ? (
                  <WireMark size="md">
                    <Wrench />
                  </WireMark>
                ) : (
                  <WirePlaceholder size="md">
                    <Wrench />
                  </WirePlaceholder>
                )
              }
              eyebrow="Build"
              title={buildTitle}
              hint={<span className="block truncate font-mono">{buildHint}</span>}
            />
          </PlanStep>
        </li>

        <li>
          <PlanStep label="Change the runtime settings" onClick={() => onOpenSection("runtime")}>
            <WireNode
              nodeRef={runtimeMark}
              mark={
                <WireMark size="md">
                  <Cpu />
                </WireMark>
              }
              eyebrow="Runtime"
              title={
                worker
                  ? "No port · runs in the background"
                  : port > 0
                    ? `Port ${port} · ${runtime.strategy === "blue_green" ? "candidate first" : "stop first"}`
                    : "No port set"
              }
              hint={
                limits.length > 0 ? (
                  limits.join(" · ")
                ) : (
                  // Said here rather than left to be discovered inside
                  // Advanced: on one server an unbounded container is the
                  // thing that takes the dashboard down with it.
                  <span className="text-warning">No memory or CPU limit</span>
                )
              }
            />
          </PlanStep>
        </li>

        <li>
          <PlanStep label="Change the public address" onClick={() => onOpenSection("address")}>
            <WireNode
              nodeRef={addressMark}
              mark={
                domain ? (
                  <WireMark size="md" tone="brand">
                    <Globe />
                  </WireMark>
                ) : (
                  <WirePlaceholder size="md">
                    <Globe />
                  </WirePlaceholder>
                )
              }
              eyebrow="Address"
              title={
                domain ? (
                  <span className="block truncate">{domain.hostname || "No hostname yet"}</span>
                ) : (
                  <span className="text-muted-foreground">
                    {worker ? "Not published" : "No public address"}
                  </span>
                )
              }
              hint={
                domain
                  ? [
                      domain.https ? "HTTPS" : "HTTP",
                      domainCount > 1 && `+${domainCount - 1} more`,
                      domain.protection && "password protected",
                    ]
                      .filter(Boolean)
                      .join(" · ")
                  : worker
                    ? "A worker answers no requests"
                    : "Reach it through the port it publishes"
              }
            />
          </PlanStep>
        </li>
      </ol>
    </div>
  )
}
