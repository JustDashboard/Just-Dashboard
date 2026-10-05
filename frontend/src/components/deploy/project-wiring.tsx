"use client"

import { useRef } from "react"
import Link from "next/link"
import {
  ArrowRight,
  Box,
  External,
  FolderClosed,
  Globe,
  GridMasonry,
  Layers,
} from "@/components/icons"
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
import { StatusDot, type DotTone } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { AnimatedBeam } from "@/components/ui/animated-beam"
import { ProductGlyph, hasProductLogo } from "@/components/product-logo"
import { BranchChip } from "@/components/git/marks"
import { SourceBranch } from "@/components/git/glyphs"
import { RunStrip } from "@/components/deploy/run-marks"
import {
  CertificateReading,
  HealthStatus,
  autoDeployReading,
  formatDuration,
  hostOf,
  runDurationSeconds,
  type ProjectState,
} from "@/components/deploy/vocabulary"
import { WireMark, WireNode, WirePlaceholder } from "@/components/deploy/wire"
import { serviceProduct } from "@/components/deploy/service-product"

/** How many of the environment's runs the release node's strip draws. */
const STRIP = 14

/**
 * How a request reaches this project, drawn as the four things it passes
 * through: the source it was built from, the release that build became, the
 * containers running it, and the names it answers to. The lines between them
 * are the state — a pulse the whole way down while the project is up, a red
 * line where it is unhealthy, a still line while it is stopped, and a dotted
 * one ahead of anything that does not exist yet.
 *
 * Each mark is the thing itself (§14): the forge the repository lives on, the
 * image or the template the project is, and what the containers run, on a
 * product's tile with their state as a dot in its corner — where a GitHub
 * repository used to be three connected nodes and every runtime the same box.
 * The release is its number on a neutral disc: the brand is this dashboard,
 * and a reading drawn in it would spend the brand on a number.
 *
 * Beside each goes only what the identity line over every project page does
 * not already say — it names the commit, its author, who shipped the release
 * and whether the branch deploys itself, and this said all of it again a
 * hand's width lower. So the source carries the branch and how often it is
 * checked, the release how long it took and how its last runs went, the
 * containers the products their images are, and each address its
 * certificate's issuer, state and the days it has left.
 */
export function ProjectWiring({
  projectId,
  deployment,
  state,
  branch,
  title,
  sourceMark,
  sourceDetail,
  runtimeProduct,
  product,
  liveRelease,
  liveRun,
  runs,
  runtime,
  domains,
  url,
  watch,
}: {
  projectId: number
  deployment: DeploymentSummary
  state: ProjectState
  /** The branch a repository follows, as the project records it. */
  branch: string
  /** What the source is called: the repository, the image, the template. */
  title: React.ReactNode
  /** Where the source lives, as a `product-logo` id. */
  sourceMark?: string
  /** What a source without a commit resolved to: a template's version and image, a digest. */
  sourceDetail?: string
  /** What the containers run, as a `product-logo` id. */
  runtimeProduct?: string
  /** What the project is, which a container of its own build is drawn as (`serviceProduct`). */
  product?: string
  liveRelease?: DeploymentRelease
  liveRun?: DeploymentEngineRun
  /** This environment's runs, newest first. */
  runs: DeploymentEngineRun[]
  runtime?: DeploymentRuntimeServices
  /** The routes the operations owner reports, when it could read them. */
  domains?: DeploymentDomainRoute[]
  url?: string
  watch?: DeploymentGitWatch
}) {
  const container = useRef<HTMLDivElement>(null)
  const sourceNode = useRef<HTMLDivElement>(null)
  const releaseNode = useRef<HTMLDivElement>(null)
  const runtimeNode = useRef<HTMLDivElement>(null)
  const domainNode = useRef<HTMLDivElement>(null)

  const up = state === "ready" || state === "deploying"
  const broken = state === "failed" || state === "unhealthy"
  const services =
    runtime?.status === "available"
      ? runtime.services.filter((service) => service.liveRelease)
      : undefined
  const running = services?.filter((service) => service.state === "running") ?? []
  const host = url && hostOf(url)
  const git = deployment.sourceKind === "git" || deployment.sourceKind === "local"
  const reading = watch && autoDeployReading(watch)
  // The containers' own reading, not the project's: a release being
  // replaced is still serving from containers that are all up.
  const runtimeTone: DotTone = broken
    ? "danger"
    : running.length === 0
      ? "stopped"
      : running.length === services?.length
        ? "running"
        : "warning"
  const SourceGlyph =
    deployment.sourceKind === "compose"
      ? Layers
      : deployment.sourceKind === "blueprint"
        ? GridMasonry
        : deployment.sourceKind === "local"
          ? FolderClosed
          : git
            ? SourceBranch
            : Box

  return (
    <div ref={container} className="relative">
      <AnimatedBeam
        containerRef={container}
        fromRef={sourceNode}
        toRef={releaseNode}
        still={!up}
        dashed={!liveRelease}
        tone={liveRelease && !up ? "success" : "default"}
        duration={2.4}
      />
      <AnimatedBeam
        containerRef={container}
        fromRef={releaseNode}
        toRef={runtimeNode}
        still={!up}
        dashed={!liveRelease}
        tone={broken ? "danger" : liveRelease && !up ? "success" : "default"}
        duration={2.4}
        delay={0.5}
      />
      <AnimatedBeam
        containerRef={container}
        fromRef={runtimeNode}
        toRef={domainNode}
        still={!up || !host}
        dashed={!host}
        tone={broken ? "danger" : host && liveRelease && !up ? "success" : "default"}
        duration={2.4}
        delay={1}
      />

      <ol className="flex flex-col gap-5" aria-label="How the project is wired">
        <li>
          <WireNode
            nodeRef={sourceNode}
            mark={
              <WireMark tone="logo" size="md">
                {hasProductLogo(sourceMark) ? (
                  <ProductGlyph id={sourceMark} />
                ) : (
                  <SourceGlyph aria-hidden />
                )}
              </WireMark>
            }
            eyebrow="Source"
            title={<span className={cn("block truncate", git && "font-mono")}>{title}</span>}
            hint={
              git ? (
                <span className="mt-0.5 flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1">
                  <BranchChip branch={branch} className="max-w-40 shrink-0" />
                  {reading?.interval ? <span>checked every {reading.interval}s</span> : null}
                  {deployment.sourceKind === "git" && (
                    <Link
                      href={`/deploy/${projectId}/settings/general#automatic-deployment`}
                      aria-label="Manage automatic deployment"
                      className="inline-flex items-center gap-1 rounded-sm focus-ring hover:text-foreground"
                    >
                      Manage <ArrowRight className="size-3" />
                    </Link>
                  )}
                </span>
              ) : (
                sourceDetail && <span className="block truncate font-mono">{sourceDetail}</span>
              )
            }
          />
        </li>

        <li>
          <WireNode
            nodeRef={releaseNode}
            mark={
              liveRelease ? (
                <WireMark size="md">
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
                <span className="text-muted-foreground">No release yet</span>
              )
            }
            // The strip is the release's reading, so it sits after the words
            // that describe the release rather than at the column's far end.
            hint={
              <span className="mt-0.5 flex min-w-0 flex-wrap items-center gap-x-4 gap-y-1">
                {!liveRelease ? (
                  <span>The first deployment records it here</span>
                ) : liveRun ? (
                  <span className="numeric">
                    {liveRun.operation === "rollback" ? "rolled back" : "released"} in{" "}
                    {formatDuration(runDurationSeconds(liveRun))}
                  </span>
                ) : (
                  liveRelease.activatedAt && (
                    <span>live {relativeTime(liveRelease.activatedAt)}</span>
                  )
                )}
                {runs.length > 0 && (
                  <Link
                    href={`/deploy/${projectId}/deployments`}
                    className="inline-flex rounded-sm py-1 focus-ring"
                  >
                    <RunStrip runs={runs.slice(0, STRIP)} />
                  </Link>
                )}
              </span>
            }
          />
        </li>

        <li>
          <WireNode
            nodeRef={runtimeNode}
            mark={
              services ? (
                <span className="relative flex">
                  <WireMark tone="logo" size="md">
                    {hasProductLogo(runtimeProduct) ? (
                      <ProductGlyph id={runtimeProduct} />
                    ) : (
                      <Box aria-hidden />
                    )}
                  </WireMark>
                  {/* The containers' state in the tile's corner, the way a
                      session's system sits on its browser's mark. */}
                  <span className="absolute -right-0.5 -bottom-0.5 flex size-3.5 items-center justify-center rounded-full bg-background">
                    <StatusDot tone={runtimeTone} />
                  </span>
                </span>
              ) : (
                // Nothing observed is not something to add: no product, so no
                // "+" in the ring's corner.
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
              services ? (
                <span className="mt-0.5 flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1">
                  {/* A check that passed before the containers stopped is not
                      a reading of containers that are not running. */}
                  {running.length > 0 && <HealthStatus health={deployment.health} />}
                  {services.map((service) => (
                    <span
                      key={service.containerId}
                      className="inline-flex min-w-0 items-center gap-1 font-mono"
                    >
                      <ProductGlyph
                        id={serviceProduct(service.image, deployment.sourceKind, product)}
                      />
                      <span className="truncate">{service.name}</span>
                    </span>
                  ))}
                </span>
              ) : (
                runtime?.reason || "Docker has not reported this release yet"
              )
            }
          />
        </li>

        <li>
          <WireNode
            nodeRef={domainNode}
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
                  // From `sm` the names are one column and their certificates
                  // another, so every domain is one line of the same shape
                  // rather than wrapping where its name happens to be long.
                  <ul className="grid grid-cols-1 gap-y-1 sm:grid-cols-[minmax(0,max-content)_minmax(0,1fr)] sm:gap-x-3">
                    {domains.map((domain) => (
                      <li
                        key={domain.hostname}
                        className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-0.5 sm:col-span-2 sm:grid sm:grid-cols-subgrid"
                      >
                        <a
                          href={`${domain.https ? "https" : "http"}://${domain.hostname}/`}
                          target="_blank"
                          rel="noopener noreferrer"
                          className="inline-flex min-w-0 items-center gap-1 rounded-sm focus-ring hover:underline"
                        >
                          <span className="truncate font-mono">{domain.hostname}</span>
                          <External aria-hidden className="size-3 shrink-0 text-muted-foreground" />
                        </a>
                        <span className="flex min-w-0 flex-wrap items-center gap-x-2 font-normal">
                          <CertificateReading domain={domain} />
                          {domain.protected && <Tag>Password</Tag>}
                        </span>
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
                  <span className="truncate font-mono">{host}</span>
                  <External aria-hidden className="size-3 shrink-0 text-muted-foreground" />
                </a>
              ) : (
                <span className="text-muted-foreground">
                  {deployment.liveReleaseId
                    ? "No public address"
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
