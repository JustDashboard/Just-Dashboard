"use client"

import { useState } from "react"
import Link from "next/link"
import { usePathname, useRouter } from "next/navigation"
import {
  ArrowLeft,
  ArrowRight,
  External,
  FolderClosed,
  LockClosed,
  LockOpen,
} from "@/components/icons"
import { relativeTime } from "@/lib/format"
import { cn } from "@/lib/utils"
import type {
  DeploymentDiagnosis,
  DeploymentDomainRoute,
  DeploymentEngineRun,
  DeploymentEnvironmentConfiguration,
  DeploymentRelease,
  DeploymentSummary,
  DeployProject,
  BlueprintDetail,
} from "@/lib/types"
import { Page, PageHeader } from "@/components/page"
import { Status, type Verdict } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { VerbMenu } from "@/components/verbs"
import { useConfirm } from "@/components/confirm-dialog"
import { HostIdentity } from "@/components/metrics/host-identity"
import { ProductGlyph, buildMethodProduct, hasProductLogo } from "@/components/product-logo"
import { AuthorMark, BranchChip, ShortSha } from "@/components/git/marks"
import { useProject } from "@/components/deploy/project-context"
import { projectCommand, useProjectVerbs } from "@/components/deploy/project-verbs"
import { RunActorMark, runActorProduct } from "@/components/deploy/run-marks"
import { PROJECT_NAV, PROJECT_SETTINGS_NAV } from "@/components/nav"
import { useNavScope, type NavScope } from "@/components/nav-scope"
import { DeployVersionDialog } from "@/components/deploy/deploy-version-dialog"
import { DuplicateProjectDialog } from "@/components/deploy/duplicate-dialog"
import { ProjectMark } from "@/components/deploy/project-mark"
import {
  BUILD_METHOD_SHORT,
  CERTIFICATE_LABEL,
  PENDING_KIND_PAGE,
  ProjectStatus,
  RECIPE_SHORT,
  RUN_LABELS,
  autoDeployReading,
  deploymentURL,
  frameworkLabel,
  hostOf,
  runActor,
  runCommit,
  runTriggerLine,
  sourceProduct,
} from "@/components/deploy/vocabulary"

/**
 * The project's frame: its name, the one command, and what the project is.
 *
 * Its pages are not here. A project is a place with fourteen destinations, and
 * a strip of tabs across the top of each of them could hold eight before it
 * scrolled sideways — so the rail drills into the project instead, which is
 * where the section above it already goes. What this registers is that list,
 * with a mark on each settings page that holds a change not yet live; what it
 * draws is the header over whichever of those pages you picked.
 *
 * Nothing here is framed. The name and the command sit in the page header the
 * way every page's do. Under them is the identity line the host Overview,
 * Metrics and the account pages open on (`HostIdentity`), so a project is
 * described the way the machine is: the project drawn as itself on the tile —
 * its site's icon, else the product it is — then where it answers, with the
 * certificate's state in the lock's colour, then what it is made of as facts
 * that each open on their own mark (the forge, the branch, the commit and its
 * author, the runtime, the release and who shipped it, whether it deploys
 * itself), and its state at the far end with what needs attention under it.
 * It was a row of grey words joined by dots, which stranded a dot at the end
 * of every line on a phone and repeated, on the Overview, what the wiring said
 * a few pixels below.
 *
 * The verbs are the projects grid's (`useProjectVerbs`): the command is the
 * first of View, Start, Deploy and Redeploy, and every other verb goes in the
 * menu beside it, grouped the way that hook declares them.
 */
export function ProjectShell({ children }: { children: React.ReactNode }) {
  const router = useRouter()
  const { confirm, dialog } = useConfirm()
  const [versionOpen, setVersionOpen] = useState(false)
  const [duplicateOpen, setDuplicateOpen] = useState(false)
  const project = useProject()
  const { deployment, project: record, runtime } = project.detail
  const url = deploymentURL(deployment.endpoint)
  const activeRun = deployment.activeRun
  const base = `/deploy/${project.projectId}`
  const overview = usePathname() === base

  const verbs = useProjectVerbs(deployment, {
    confirm,
    refresh: project.refresh,
    start: project.start,
    starting: project.starting,
    runtime,
    archived: project.archived,
    onVersion: () => setVersionOpen(true),
    onDuplicate: () => setDuplicateOpen(true),
    onArchived: () => router.push("/deploy"),
  })
  const command = projectCommand(verbs)
  const menu = verbs.filter((verb) => verb !== command)

  useProjectNavScope({
    deployment,
    name: record.name,
    archived: project.archived,
    pendingKinds: project.configuration?.pending.changes.map((change) => change.kind),
  })

  const domain = project.operations?.domains.domains.find((route) => route.hostname === hostOf(url))

  return (
    <Page>
      <PageHeader
        eyebrow={
          <Link
            href="/deploy"
            className="inline-flex items-center gap-1 rounded-sm focus-ring hover:underline"
          >
            <ArrowLeft className="size-3" /> Deployments
          </Link>
        }
        title={record.name}
        actions={
          <>
            {url && (
              <Button variant="outline" size="sm" asChild>
                <a href={url} target="_blank" rel="noopener noreferrer">
                  <External className="size-3.5" /> Visit
                </a>
              </Button>
            )}
            {command &&
              (command.key === "view" && activeRun ? (
                <Button size="sm" asChild>
                  <Link href={`${base}/runs/${activeRun.id}`}>
                    {command.label} <ArrowRight className="size-3.5" />
                  </Link>
                </Button>
              ) : (
                <Button
                  size="sm"
                  disabled={command.disabled}
                  pending={project.starting === command.key}
                  onClick={command.run}
                >
                  <command.icon className="size-3.5" />
                  {project.starting === command.key ? command.progressive : command.label}
                </Button>
              ))}
            {menu.length > 0 && <VerbMenu verbs={menu} label="Deployment actions" />}
          </>
        }
      />

      <section aria-label={`About ${record.name}`}>
        <HostIdentity
          logo={<ProjectMark deployment={deployment} product={project.product} size="lg" />}
          title={<Address deployment={deployment} url={url} domain={domain} />}
          facts={
            // Held to a measure, so the facts wrap under the title and the state
            // keeps its place at the line's far end rather than dropping below.
            <span className="flex max-w-[40rem] min-w-0 flex-1 flex-wrap items-center gap-x-4 gap-y-1.5">
              <SourceFact
                deployment={deployment}
                record={record}
                configuration={project.configuration}
                blueprint={project.blueprint}
              />
              {/* On a phone only the Overview keeps the commit and the runtime:
                  its wiring is where they are read, and on every other page
                  they pushed the page's first field below the fold. */}
              <span className={cn("contents", !overview && "max-sm:hidden")}>
                <CommitFact
                  deployment={deployment}
                  record={record}
                  run={project.liveRun}
                  release={project.liveRelease}
                />
                <RuntimeFact deployment={deployment} configuration={project.configuration} />
              </span>
              {project.liveRelease && (
                <span className="inline-flex min-w-0 items-center gap-1.5">
                  <span className="numeric shrink-0 text-foreground">
                    Release #{project.liveRelease.number}
                  </span>
                  <ReleaseLine
                    run={project.liveRun}
                    activatedAt={project.liveRelease.activatedAt}
                    remote={deployment.sourceRemote}
                  />
                </span>
              )}
              <AutoDeployFact
                reading={project.gitWatch && autoDeployReading(project.gitWatch)}
                href={`${base}/settings/general#automatic-deployment`}
              />
            </span>
          }
          aside={
            // Beside the facts the two readings stack at the line's end; where the
            // line has wrapped them under the facts they read across instead.
            <div className="flex min-w-0 flex-wrap items-center gap-x-4 gap-y-1 xl:block xl:space-y-1 xl:text-right">
              <ProjectStatus
                summary={deployment}
                runtime={runtime}
                archived={project.archived}
                live={Boolean(activeRun)}
                className="text-body"
              />
              {activeRun ? (
                <Link
                  href={`${base}/runs/${activeRun.id}`}
                  className="flex items-center gap-1.5 rounded-sm text-xs focus-ring xl:justify-end"
                >
                  <TextShimmer>
                    {activeRun.currentStep?.label ?? RUN_LABELS[activeRun.state]}
                  </TextShimmer>
                  <span className="numeric text-muted-foreground">#{activeRun.runNumber}</span>
                </Link>
              ) : (
                !project.archived && (
                  <Assessment
                    diagnosis={project.operations?.diagnosis}
                    href={`${base}#attention`}
                  />
                )
              )}
            </div>
          }
        />
      </section>

      {children}
      {dialog}
      <DeployVersionDialog open={versionOpen} onOpenChange={setVersionOpen} />
      <DuplicateProjectDialog open={duplicateOpen} onOpenChange={setDuplicateOpen} />
    </Page>
  )
}

/**
 * The rail's third level: inside Deployments, inside this project. It is
 * registered by the page because the three things the rail cannot read off
 * the URL are in the page's hands — what the project is called, that a game
 * server has two pages nothing else has, and that its configuration has moved
 * on from what is live. The project's own pages register it from the shell,
 * and a run's page — outside the project layout, but one of its Deployments —
 * registers the same panel, so opening a run keeps the rail the reader came
 * from.
 *
 * Each settings page that holds a saved change not yet live carries the mark
 * the group label has always had, so the reader lands on the page with the
 * change rather than on the group; `pendingKinds` are the kinds of those
 * changes, where the caller has read them.
 */
export function useProjectNavScope(
  project:
    | { deployment: DeploymentSummary; name: string; archived: boolean; pendingKinds?: string[] }
    | undefined,
) {
  let scope: NavScope | null = null
  if (project) {
    const { deployment, name, archived, pendingKinds = [] } = project
    const base = `/deploy/${deployment.id}`
    const url = deploymentURL(deployment.endpoint)
    const pending = Boolean(deployment.pendingChanges) && !archived
    const pendingPages = new Set(
      pending
        ? pendingKinds.flatMap((kind) => {
            const page = PENDING_KIND_PAGE[kind]
            return page ? [page.split("#")[0]] : []
          })
        : [],
    )
    scope = {
      path: base,
      title: name,
      caption: url ? hostOf(url) : undefined,
      mark: <ProjectMark deployment={deployment} size="xs" className="size-3.5" />,
      groups: [
        {
          items: PROJECT_NAV.filter((entry) => !entry.game || deployment.profile === "game").map(
            (entry) => ({
              title: entry.title,
              href: `${base}${entry.path}`,
              icon: entry.icon,
            }),
          ),
        },
        {
          label: "Settings",
          pending,
          items: PROJECT_SETTINGS_NAV.map((entry) => ({
            title: entry.title,
            href: `${base}${entry.path}`,
            icon: entry.icon,
            pending: pendingPages.has(entry.path),
          })),
        },
      ],
    }
  }
  useNavScope(scope)
}

/**
 * Where the project answers, as the identity line's title: the address with
 * its certificate's state in the lock's colour — the one reading a visitor's
 * browser would show before anything else — or, with no address, what it is
 * instead.
 */
function Address({
  deployment,
  url,
  domain,
}: {
  deployment: DeploymentSummary
  url?: string
  domain?: DeploymentDomainRoute
}) {
  if (!url) {
    if (!deployment.liveReleaseId)
      return <span className="text-muted-foreground">Not deployed yet</span>
    return (
      <>
        Private service
        {deployment.internalPort && (
          <span className="font-mono text-body font-normal text-muted-foreground">
            {" "}
            :{deployment.internalPort}
          </span>
        )}
      </>
    )
  }
  const https = url.startsWith("https:")
  const Lock = https ? LockClosed : LockOpen
  return (
    <a
      href={url}
      target="_blank"
      rel="noopener noreferrer"
      className="inline-flex max-w-full min-w-0 items-center gap-1.5 rounded-sm align-bottom focus-ring hover:underline"
    >
      <span title={domain ? CERTIFICATE_LABEL[domain.certificate] : undefined} className="flex">
        <Lock
          aria-hidden
          className={cn(
            "size-3.5 shrink-0",
            !https || !domain
              ? "text-muted-foreground"
              : domain.certificate === "valid"
                ? "text-success"
                : domain.certificate === "expiring"
                  ? "text-warning"
                  : domain.certificate === "expired" || domain.certificate === "missing"
                    ? "text-destructive"
                    : "text-muted-foreground",
          )}
        />
      </span>
      <span className="truncate">{hostOf(url)}</span>
      {/* The lock's colour is the certificate's state; this is it in words. */}
      <span className="sr-only">
        {domain ? `, ${CERTIFICATE_LABEL[domain.certificate]}` : https ? "" : ", not encrypted"}
      </span>
      <External aria-hidden className="size-3.5 shrink-0 text-muted-foreground" />
    </a>
  )
}

/** One fact of the identity line, opening on the mark of what it names. */
function Fact({
  mark,
  mono,
  className,
  children,
}: {
  mark?: React.ReactNode
  mono?: boolean
  className?: string
  children: React.ReactNode
}) {
  return (
    <span className={cn("inline-flex min-w-0 items-center gap-1.5", className)}>
      {mark}
      <span className={cn("truncate", mono && "font-mono")}>{children}</span>
    </span>
  )
}

/**
 * Where the project comes from: the repository on its forge's mark, the image
 * as the product it is, the stack, the template by its own name, a checkout
 * on this server as a folder.
 */
function SourceFact({
  deployment,
  record,
  configuration,
  blueprint,
}: {
  deployment: DeploymentSummary
  record: DeployProject
  configuration?: DeploymentEnvironmentConfiguration
  blueprint?: BlueprintDetail
}) {
  const product = sourceProduct(deployment, configuration?.source)
  const glyph = hasProductLogo(product) ? <ProductGlyph id={product} /> : undefined
  switch (deployment.sourceKind) {
    case "git":
      return (
        <Fact mark={glyph} mono className="text-foreground">
          {deployment.sourceRepository ||
            configuration?.identity?.repository ||
            configuration?.source?.url ||
            record.repoPath}
        </Fact>
      )
    case "local":
      return (
        <Fact mark={<FolderClosed aria-hidden className="size-3.5 shrink-0" />} mono>
          {configuration?.source?.localPath || record.repoPath}
        </Fact>
      )
    case "image":
      return (
        <Fact mark={glyph} mono className="text-foreground">
          {deployment.sourceRepository || deployment.sourceRef}
        </Fact>
      )
    case "compose":
      return (
        <Fact mark={glyph}>
          Compose
          {deployment.serviceCount ? ` · ${deployment.serviceCount} services` : " stack"}
        </Fact>
      )
    case "blueprint":
      return (
        <Fact mark={glyph}>
          {blueprint?.name ?? deployment.sourceRepository?.split("@")[0] ?? "Template"} template
        </Fact>
      )
    default:
      return null
  }
}

/**
 * The commit that is live, the way a forge writes one: the branch, the short
 * id, who wrote it and what it says. Only a repository has one. It is the
 * live release's — from the run that recorded it, else the release's own
 * revision — and only a project with nothing live yet names its newest run's,
 * so a build that failed or is still verifying is never drawn as what is live.
 */
function CommitFact({
  deployment,
  record,
  run,
  release,
}: {
  deployment: DeploymentSummary
  record: DeployProject
  run?: DeploymentEngineRun
  release?: DeploymentRelease
}) {
  if (deployment.sourceKind !== "git" && deployment.sourceKind !== "local") return null
  const commit = runCommit(run) ?? (release ? undefined : runCommit(deployment.lastRun))
  return (
    <span className="inline-flex min-w-0 items-center gap-1.5">
      <BranchChip
        branch={deployment.sourceRef || record.branch || "main"}
        className="max-w-40 shrink-0"
      />
      <ShortSha sha={commit?.sha ?? release?.sourceRevision ?? deployment.sourceRevision} />
      {commit?.subject && (
        <>
          <AuthorMark name={commit.author} />
          <span className="max-w-[28ch] min-w-0 truncate text-foreground/80">{commit.subject}</span>
        </>
      )}
    </span>
  )
}

/**
 * What a repository runs as: the framework detection found on the language's
 * own mark, a Dockerfile as Docker, a static site on nginx. An image, a stack
 * and a template already said what they are in the source fact.
 */
function RuntimeFact({
  deployment,
  configuration,
}: {
  deployment: DeploymentSummary
  configuration?: DeploymentEnvironmentConfiguration
}) {
  if (deployment.sourceKind !== "git" && deployment.sourceKind !== "local") return null
  const build = configuration?.build
  const method = build?.method ?? deployment.buildMethod
  const recipe = build?.recipe ?? deployment.recipe
  const framework = build?.framework ?? deployment.framework
  const product = buildMethodProduct(method, { recipe, packageManager: build?.packageManager })
  const mark = hasProductLogo(product) ? <ProductGlyph id={product} /> : undefined
  if (method === "recipe" && recipe) {
    const version =
      recipe === "python" ? build?.pythonVersion : recipe === "go" ? build?.goVersion : undefined
    const language = [RECIPE_SHORT[recipe], version].filter(Boolean).join(" ")
    return (
      <Fact mark={mark}>
        {framework ? `${frameworkLabel(framework)} · ${language}` : language}
        {build?.packageManager && build.packageManager !== "npm" && ` · ${build.packageManager}`}
      </Fact>
    )
  }
  if (method === "static") return <Fact mark={mark}>Static on nginx</Fact>
  if (method === "dockerfile") return <Fact mark={mark}>{BUILD_METHOD_SHORT.dockerfile}</Fact>
  return null
}

/**
 * When the live release went live and who shipped it — a person as their
 * face in the hue the rail gives them, anything else in its words.
 */
function ReleaseLine({
  run,
  activatedAt,
  remote,
}: {
  run?: DeploymentEngineRun
  activatedAt?: string
  remote?: string
}) {
  if (!run)
    return activatedAt ? <span className="truncate">live {relativeTime(activatedAt)}</span> : null
  const actor = runActor(run)
  const product = runActorProduct(run, remote)
  return (
    <>
      <span className="shrink-0">
        {run.operation === "rollback" ? "rolled back" : "deployed"}{" "}
        {relativeTime(run.endedAt ?? run.requestedAt)}
      </span>
      {actor.kind === "person" ? (
        <span className="inline-flex min-w-0 items-center gap-1">
          by
          <RunActorMark run={run} size="xs" className="size-4" />
          <span className="truncate">{actor.name}</span>
        </span>
      ) : (
        <span className="inline-flex min-w-0 items-center gap-1">
          {hasProductLogo(product) && <ProductGlyph id={product} />}
          <span className="truncate">{runTriggerLine(run)}</span>
        </span>
      )}
    </>
  )
}

/** Whether the branch deploys itself, as a way to the setting that decides it. */
function AutoDeployFact({
  reading,
  href,
}: {
  reading?: ReturnType<typeof autoDeployReading>
  href: string
}) {
  if (!reading) return null
  return (
    <Link
      href={href}
      className="rounded-sm focus-ring"
      aria-label={`${reading.label} · automatic deployment settings`}
    >
      <Status tone={reading.tone} label={reading.label} />
    </Link>
  )
}

const WORST: Verdict[] = ["critical", "warning", "notice"]

/**
 * The diagnosis under the state: how many findings need attention, as a way
 * to them on the Overview, or that every owner was read and nothing was
 * wrong. A diagnosis that could not read every owner says so rather than
 * claiming a pass.
 */
function Assessment({ diagnosis, href }: { diagnosis?: DeploymentDiagnosis; href: string }) {
  if (!diagnosis) return null
  const count = diagnosis.findings.length
  if (count > 0) {
    const worst =
      WORST.find((level) => diagnosis.findings.some((finding) => finding.severity === level)) ??
      "notice"
    return (
      <Link href={href} className="block rounded-sm focus-ring hover:underline">
        <Status
          verdict={worst}
          label={count === 1 ? "1 needs attention" : `${count} need attention`}
        />
      </Link>
    )
  }
  if (diagnosis.status === "partial")
    return <p className="text-xs text-muted-foreground">Partly assessed</p>
  return <Status verdict="ok" label="All checks passed" />
}
