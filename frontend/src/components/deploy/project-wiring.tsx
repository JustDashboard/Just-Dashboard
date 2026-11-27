"use client"

import { useRef } from "react"
import Link from "next/link"
import {
  ArrowRight,
  Box,
  ChevronDown,
  External,
  FolderClosed,
  Globe,
  GridMasonry,
  Layers,
} from "@/components/icons"
import { relativeTime } from "@/lib/format"
import { promotionOrigin } from "@/lib/pull-requests"
import type {
  DeploymentDomainRoute,
  DeploymentEngineRun,
  DeploymentGitWatch,
  DeploymentRelease,
  DeploymentRuntimeServices,
  DeploymentSummary,
  DeploymentTailnetAddress,
} from "@/lib/types"
import { cn } from "@/lib/utils"
import { Status, StatusDot, type DotTone } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { AnimatedBeam } from "@/components/ui/animated-beam"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import { ProductGlyph, hasProductLogo } from "@/components/product-logo"
import { BranchChip } from "@/components/git/marks"
import { SourceBranch } from "@/components/git/glyphs"
import { RunStrip } from "@/components/deploy/run-marks"
import {
  CertificateReading,
  CertificatesReading,
  HealthStatus,
  autoDeployReading,
  formatDuration,
  hostOf,
  runDurationSeconds,
  runPromotion,
  type ProjectState,
} from "@/components/deploy/vocabulary"
import { WireMark, WireNode, WirePlaceholder } from "@/components/wire/wire"
import { serviceProduct } from "@/components/deploy/service-product"
import { replicaReading } from "@/components/deploy/replicas-model"
import {
  LISTED_DOMAINS,
  leadFirst,
  listedServices,
  serviceLabel,
} from "@/components/deploy/wiring-model"

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
 * The release is its number on the tile every other mark stands on: the brand
 * is this dashboard, and a reading drawn in it would spend the brand on a
 * number.
 *
 * Beside each goes only what the identity line over every project page does
 * not already say — it names the commit, its author, who shipped the release
 * and whether the branch deploys itself, and this said all of it again a
 * hand's width lower. So the source carries the branch and how often it is
 * checked, the release how long it took and how its last runs went, the
 * containers the products their images are, and each address its
 * certificate's issuer, state and the days it has left.
 *
 * A project of many parts keeps the picture's height: past four containers
 * the node names three and counts the rest into the Runtime page, and past
 * two names it leads with the deployment's own address, puts every name
 * behind "N more" and says the certificates in one line (`wiring-model.ts`).
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
  tailnet,
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
  /** The tailnet address and who may open it, for a live release that is tailnet-only. */
  tailnet?: DeploymentTailnetAddress
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
  const listed = listedServices(services ?? [])
  // A replicated release is read as its replicas, against the count it was
  // started with, so a replica Docker no longer lists is still one missing.
  const replicas = replicaReading(services)
  const host = url && hostOf(url)
  // A stack's apps or a site's aliases: the lead name, the rest behind a press.
  const folded =
    domains && domains.length > LISTED_DOMAINS ? leadFirst(domains, host || undefined) : undefined
  const git = deployment.sourceKind === "git" || deployment.sourceKind === "local"
  const reading = watch && autoDeployReading(watch)
  // The containers' own reading, not the project's: a release being
  // replaced is still serving from containers that are all up.
  const runtimeTone: DotTone = broken
    ? "danger"
    : running.length === 0
      ? "stopped"
      : replicas
        ? replicas.healthy === replicas.total
          ? "running"
          : "warning"
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
                <WireMark tone="logo" size="md">
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
                  <span className="numeric" data-slot="live-release-origin">
                    {liveRun.operation === "rollback" ? (
                      "rolled back"
                    ) : liveRun.operation === "promote" ? (
                      <PromotedFrom
                        origin={promotionOrigin(liveRelease.promotion ?? runPromotion(liveRun))}
                      />
                    ) : (
                      "released"
                    )}{" "}
                    in {formatDuration(runDurationSeconds(liveRun))}
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
                  <StatusDot tone={runtimeTone} corner="md" />
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
                ) : replicas ? (
                  <span className="numeric">
                    {replicas.healthy} of {replicas.total} replicas healthy
                  </span>
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
                  {listed.shown.map((service) => {
                    const label = serviceLabel(service, services.length > 1)
                    const image = serviceProduct(service.image, deployment.sourceKind, product)
                    return (
                      <span
                        key={service.containerId}
                        title={service.name}
                        className="inline-flex min-w-0 items-center gap-1 font-mono"
                      >
                        {/* A stack's app is drawn as itself where its image
                            names nothing more than Docker. */}
                        <ProductGlyph
                          id={image === "docker" && hasProductLogo(label) ? label : image}
                        />
                        <span className="truncate">{label}</span>
                      </span>
                    )
                  })}
                  {listed.hidden > 0 && (
                    <Link
                      href={`/deploy/${projectId}/runtime`}
                      className="numeric rounded-sm focus-ring hover:text-foreground"
                    >
                      {listed.hidden} more
                    </Link>
                  )}
                </span>
              ) : (
                runtime?.reason || "Docker has not reported this release yet"
              )
            }
          />
        </li>

        {tailnet && (
          <li>
            <TailnetNode nodeRef={domainNode} tailnet={tailnet} />
          </li>
        )}
        {/* A tailnet-only release has no public names: its address is drawn
            in this node's place, above. */}
        <li hidden={Boolean(tailnet)}>
          <WireNode
            nodeRef={tailnet ? undefined : domainNode}
            mark={
              host ? (
                <WireMark tone="logo" size="md">
                  <Globe />
                </WireMark>
              ) : (
                <WirePlaceholder size="md">
                  <Globe />
                </WirePlaceholder>
              )
            }
            eyebrow="Domains"
            // Folded, the certificates are one reading under the lead name.
            hint={folded && <CertificatesReading domains={folded} />}
            title={
              domains ? (
                domains.length === 0 ? (
                  <span className="text-muted-foreground">No public domain</span>
                ) : folded ? (
                  <span className="flex min-w-0 items-center gap-2">
                    <DomainLink domain={folded[0]} />
                    <AllDomains domains={folded} />
                  </span>
                ) : (
                  <DomainLines domains={domains} />
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

/** A name as a way to it, in a new tab. */
function DomainLink({ domain }: { domain: DeploymentDomainRoute }) {
  return (
    <a
      href={`${domain.https ? "https" : "http"}://${domain.hostname}/`}
      target="_blank"
      rel="noopener noreferrer"
      className="inline-flex min-w-0 items-center gap-1 rounded-sm focus-ring hover:underline"
    >
      <span className="truncate font-mono">{domain.hostname}</span>
      <External aria-hidden className="size-3 shrink-0 text-muted-foreground" />
    </a>
  )
}

/**
 * One or two names, each a line with its certificate. From `sm` the names
 * are one column and their certificates another, so both lines are the same
 * shape rather than wrapping where a name happens to be long.
 */
function DomainLines({ domains }: { domains: DeploymentDomainRoute[] }) {
  return (
    <ul className="grid grid-cols-1 gap-y-1 sm:grid-cols-[minmax(0,max-content)_minmax(0,1fr)] sm:gap-x-3">
      {domains.map((domain) => (
        <li
          key={domain.hostname}
          className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-0.5 sm:col-span-2 sm:grid sm:grid-cols-subgrid"
        >
          <DomainLink domain={domain} />
          <span className="flex min-w-0 flex-wrap items-center gap-x-2 font-normal">
            <CertificateReading domain={domain} />
            {domain.protected && <Tag>Password</Tag>}
          </span>
        </li>
      ))}
    </ul>
  )
}

/**
 * Every name, behind "N more" beside the lead one: the list the node used to
 * stack, each name with its own certificate, in a popover over the page
 * rather than down it.
 */
function AllDomains({ domains }: { domains: DeploymentDomainRoute[] }) {
  const more = domains.length - 1
  return (
    <Popover>
      <PopoverTrigger asChild>
        {/* Its wash reaches past the line's height rather than growing it,
            so the node stands as tall as Runtime over it. */}
        <button
          type="button"
          className="numeric -my-0.5 inline-flex shrink-0 items-center gap-1 rounded-md px-1.5 py-0.5 text-hint font-normal text-muted-foreground focus-ring transition-colors hover:bg-row-hover hover:text-foreground aria-expanded:bg-row-hover aria-expanded:text-foreground"
        >
          {more} more<span className="sr-only"> domains</span>
          <ChevronDown aria-hidden className="size-3" />
        </button>
      </PopoverTrigger>
      <PopoverContent align="start" className="w-max p-3">
        <p className="eyebrow numeric px-1 pb-2">{domains.length} domains</p>
        {/* As the node's own lines: names one column, certificates another,
            and on a phone each certificate under its name. */}
        <ul
          aria-label="Domains"
          className="grid grid-cols-1 sm:grid-cols-[minmax(0,max-content)_minmax(0,max-content)] sm:gap-x-4"
        >
          {domains.map((domain) => (
            <li
              key={domain.hostname}
              className="flex min-h-8 min-w-0 flex-col justify-center px-1 py-1 text-body sm:col-span-2 sm:grid sm:grid-cols-subgrid sm:items-center sm:py-0"
            >
              <DomainLink domain={domain} />
              <span className="flex min-w-0 flex-wrap items-center gap-x-2">
                <CertificateReading domain={domain} />
                {domain.protected && <Tag>Password</Tag>}
              </span>
            </li>
          ))}
        </ul>
      </PopoverContent>
    </Popover>
  )
}

/**
 * Where a tailnet-only release answers, in the Domains node's place: its
 * address on the tailnet as Tailscale's mark, and who may open it — the
 * logins and tags the release admits, or the whole tailnet — with the proxy
 * that asks who each visitor is.
 */
function TailnetNode({
  nodeRef,
  tailnet,
}: {
  nodeRef: React.RefObject<HTMLDivElement | null>
  tailnet: DeploymentTailnetAddress
}) {
  const address = tailnet.url && hostOf(tailnet.url)
  const gate =
    tailnet.gate === "caddy"
      ? "checked by the dashboard's Caddy"
      : tailnet.gate === "nginx"
        ? "checked by nginx"
        : undefined
  return (
    <WireNode
      nodeRef={nodeRef}
      mark={
        address && tailnet.published ? (
          <WireMark tone="logo" size="md">
            <ProductGlyph id="tailscale" />
          </WireMark>
        ) : (
          <WirePlaceholder size="md" product="tailscale" />
        )
      }
      eyebrow="Tailnet only"
      title={
        address ? (
          <span className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-0.5">
            <a
              href={tailnet.url}
              target="_blank"
              rel="noopener noreferrer"
              className="inline-flex min-w-0 items-center gap-1 rounded-sm focus-ring hover:underline"
            >
              <span className="truncate font-mono">{address}</span>
              <External aria-hidden className="size-3 shrink-0 text-muted-foreground" />
            </a>
            {!tailnet.published && (
              <Status tone="warning" label="Not reachable · redeploy" className="font-normal" />
            )}
          </span>
        ) : (
          <span className="text-muted-foreground">Gets its address on the next deployment</span>
        )
      }
      hint={
        <span className="mt-0.5 flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
          {tailnet.allow.length === 0 ? (
            <span>Everyone on the tailnet</span>
          ) : (
            <>
              <span>Only</span>
              {tailnet.allow.map((entry) => (
                <Tag key={entry} mono>
                  {entry}
                </Tag>
              ))}
            </>
          )}
          {gate && <span>· {gate}</span>}
        </span>
      }
    />
  )
}

/**
 * Where a promoted live release came from: staging, or — for staging's own —
 * the pull request whose preview built the image, at its commit.
 */
function PromotedFrom({ origin }: { origin: ReturnType<typeof promotionOrigin> }) {
  if (origin?.kind !== "preview") return <>promoted from staging</>
  return (
    <>
      promoted from {origin.from}
      {origin.sha && <span className="font-mono"> ({origin.sha})</span>}
    </>
  )
}
