"use client"

import { useState } from "react"
import Link from "next/link"
import { useRouter } from "next/navigation"
import {
  ArrowCircleUp,
  ArrowRight,
  External,
  FolderClosed,
  LockClosed,
  LockOpen,
} from "@/components/icons"
import { del } from "@/lib/api"
import { relativeTime } from "@/lib/format"
import { cn } from "@/lib/utils"
import type {
  DeploymentDomainRoute,
  DeploymentEngineRun,
  DeploymentEnvironmentConfiguration,
  DeploymentSummary,
  DeployProject,
  BlueprintDetail,
} from "@/lib/types"
import { Page, PageContext } from "@/components/page"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { VerbMenu } from "@/components/verbs"
import { useAuth } from "@/hooks/use-auth"
import { useConfirm } from "@/components/confirm-dialog"
import { ProductGlyph, buildMethodProduct, hasProductLogo } from "@/components/product-logo"
import { BranchChip } from "@/components/git/marks"
import { useProject } from "@/components/deploy/project-context"
import { projectCommand, useProjectVerbs } from "@/components/deploy/project-verbs"
import { PROJECT_NAV, PROJECT_SETTINGS_NAV } from "@/components/nav"
import { useNavScope, type NavScope } from "@/components/nav-scope"
import { DeployVersionDialog } from "@/components/deploy/deploy-version-dialog"
import { DuplicateProjectDialog } from "@/components/deploy/duplicate-dialog"
import { ProjectMark } from "@/components/deploy/project-mark"
import { PullRequestPicker } from "@/components/deploy/pull-request-picker"
import { EnvironmentSwitch } from "@/components/deploy/environment-switch"
import { AddStagingDialog } from "@/components/deploy/add-staging-dialog"
import { PromoteDialog } from "@/components/deploy/promote-dialog"
import {
  BUILD_METHOD_SHORT,
  CERTIFICATE_LABEL,
  PENDING_KIND_PAGE,
  ProjectStatus,
  RECIPE_SHORT,
  autoDeployReading,
  deploymentURL,
  frameworkLabel,
  hostOf,
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
 * draws is the project context over whichever of those pages you picked.
 *
 * Nothing here is framed. The whole context is one compact header at the top
 * of the page, because it repeats above all fourteen pages and every row it
 * takes is a row of logs or settings pushed under the fold: the project drawn
 * as itself on the tile — its site's icon, else the product it is — then where
 * it answers, with the certificate's state in the lock's colour, its state and
 * what needs attention on that same line, and the command at the far end. One
 * line of facts runs under the address: the source, branch and runtime, the
 * live release's age and whether it deploys itself. The way back is the rail's
 * own, the address is the way to the site, and commit and actor details belong
 * to the deployment history, where the run gives them context. On a phone the
 * state and the facts take the full width under the tile, address and command.
 *
 * The verbs are the projects grid's (`useProjectVerbs`): the command is the
 * first of View, Start, Deploy and Redeploy, and every other verb goes in the
 * menu beside it, grouped the way that hook declares them.
 */
export function ProjectShell({ children }: { children: React.ReactNode }) {
  const router = useRouter()
  const { confirm, dialog } = useConfirm()
  const [versionOpen, setVersionOpen] = useState(false)
  const [testPullOpen, setTestPullOpen] = useState(false)
  const [duplicateOpen, setDuplicateOpen] = useState(false)
  const [addStagingOpen, setAddStagingOpen] = useState(false)
  const [promote, setPromote] = useState<{ open: boolean; releaseId?: number }>({ open: false })
  const { can } = useAuth()
  const project = useProject()
  const { deployment, project: record, runtime } = project.detail
  const url = deploymentURL(deployment.endpoint)
  const activeRun = deployment.activeRun
  const base = `/deploy/${project.projectId}`
  const onStaging = project.environment === "staging"
  // Promoting needs both ends, and what deploying production needs.
  const canPromote =
    Boolean(project.staging && project.production) && can("service.control") && !project.archived

  const verbs = useProjectVerbs(deployment, {
    confirm,
    refresh: project.refresh,
    start: project.start,
    starting: project.starting,
    runtime,
    archived: project.archived,
    onVersion: () => setVersionOpen(true),
    onTestPull: () => setTestPullOpen(true),
    onDuplicate: () => setDuplicateOpen(true),
    onArchived: () => router.push("/deploy"),
    hasStaging: Boolean(project.staging),
    promotionOnly: !onStaging && Boolean(project.production?.promotionOnly),
    onPromote: canPromote
      ? () =>
          setPromote({ open: true, releaseId: onStaging ? deployment.liveReleaseId : undefined })
      : undefined,
    onAddStaging: () => setAddStagingOpen(true),
    onRemoveStaging: () =>
      confirm({
        title: `Remove ${record.name}'s staging`,
        confirmLabel: "Remove staging",
        subject: {
          mark: <ProjectMark deployment={deployment} size="sm" />,
          name: `${record.name} · staging`,
          facts: url ? hostOf(url) : undefined,
        },
        description: (
          <>
            <p>
              Staging&apos;s containers, its route and the volumes adding staging created are
              removed. Production is not touched, and neither is an image production was promoted
              from.
            </p>
            <p>Staging can be added again; it starts from production&apos;s settings then.</p>
          </>
        ),
        action: async () => {
          await del(`${base}/environments/${deployment.environmentId}`, {
            body: { volumes: true },
          })
        },
        onDone: () => {
          project.selectEnvironment("production")
          project.refresh()
          router.push(base)
        },
      }),
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
    <Page className="pt-4 md:pt-5">
      <PageContext title={record.name} />

      <section
        aria-label={`About ${record.name}`}
        data-slot="project-header"
        className="grid min-w-0 grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-x-3 gap-y-2 border-b border-hairline pb-4 sm:gap-y-0.5"
      >
        <ProjectMark deployment={deployment} product={project.product} className="sm:row-span-2" />
        <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 max-sm:contents">
          {/* A flex box, so the address sits on the row's centre rather than
              on the baseline of a line box taller than its type. */}
          <p className="flex max-w-full min-w-0 text-title font-semibold tracking-tight">
            <Address deployment={deployment} url={url} domain={domain} />
          </p>
          <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 max-sm:order-2 max-sm:col-span-3">
            <ProjectStatus
              summary={deployment}
              runtime={runtime}
              archived={project.archived}
              live={Boolean(activeRun)}
            />
          </div>
        </div>
        <div className="flex shrink-0 items-center gap-2 max-sm:order-1 sm:row-span-2">
          <EnvironmentSwitch className="max-sm:hidden" />
          {/* Staging's way to production is its own button: it is the step
              staging exists for, not one more item in the menu. On a phone
              the row is the address's and the command's, and the menu
              carries Promote instead. */}
          {onStaging && canPromote && deployment.liveReleaseId && !activeRun && (
            <Button
              size="sm"
              variant="outline"
              className="max-sm:hidden"
              onClick={() => setPromote({ open: true, releaseId: deployment.liveReleaseId })}
            >
              <ArrowCircleUp className="size-3.5" />
              Promote
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
        </div>
        <div className="col-span-3 flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground max-sm:order-3 sm:col-span-1 sm:col-start-2">
          <SourceFact
            deployment={deployment}
            record={record}
            configuration={project.configuration}
            blueprint={project.blueprint}
          />
          <BranchFact deployment={deployment} record={record} />
          <RuntimeFact deployment={deployment} configuration={project.configuration} />
          {project.liveRelease && (
            <span className="inline-flex max-w-full min-w-0 items-center gap-1.5">
              <span className="numeric shrink-0 text-foreground">
                Release #{project.liveRelease.number}
              </span>
              <ReleaseLine run={project.liveRun} activatedAt={project.liveRelease.activatedAt} />
            </span>
          )}
          <AutoDeployFact
            reading={project.gitWatch && autoDeployReading(project.gitWatch)}
            href={`${base}/settings/general#automatic-deployment`}
          />
        </div>
      </section>

      {/* On a phone the header's row has no room for the switch beside the
          command, so it takes a row of its own under the facts. */}
      {project.staging && <EnvironmentSwitch className="-mt-2 sm:hidden" />}
      {children}
      {dialog}
      <DeployVersionDialog open={versionOpen} onOpenChange={setVersionOpen} />
      <AddStagingDialog open={addStagingOpen} onOpenChange={setAddStagingOpen} />
      {project.staging && project.production && (
        <PromoteDialog
          open={promote.open}
          onOpenChange={(open) => setPromote((current) => ({ ...current, open }))}
          projectId={project.projectId}
          productionId={project.production.id}
          stagingId={project.staging.id}
          remote={deployment.sourceRemote}
          initialReleaseId={promote.releaseId}
        />
      )}
      <PullRequestPicker
        open={testPullOpen}
        onOpenChange={setTestPullOpen}
        projectId={project.projectId}
        projectName={record.name}
      />
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
      mark: <ProjectMark deployment={deployment} size="xs" />,
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
      return <span className="truncate text-muted-foreground">Not deployed yet</span>
    return (
      <span className="truncate">
        Private service
        {deployment.internalPort && (
          <span className="font-mono text-body font-normal text-muted-foreground">
            {" "}
            :{deployment.internalPort}
          </span>
        )}
      </span>
    )
  }
  const https = url.startsWith("https:")
  const Lock = https ? LockClosed : LockOpen
  return (
    <a
      href={url}
      target="_blank"
      rel="noopener noreferrer"
      className="inline-flex max-w-full min-w-0 items-center gap-1.5 rounded-sm focus-ring hover:underline"
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
      {/* On a phone the command shares this row, and the address needs the width. */}
      <External aria-hidden className="size-3.5 shrink-0 text-muted-foreground max-sm:hidden" />
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
    <span className={cn("inline-flex max-w-full min-w-0 items-center gap-1.5", className)}>
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
 * The tracked branch belongs beside its repository. The exact live commit is
 * read in the Overview's wiring and deployment history rather than repeated
 * in the shared header on every settings page.
 */
function BranchFact({
  deployment,
  record,
}: {
  deployment: DeploymentSummary
  record: DeployProject
}) {
  if (deployment.sourceKind !== "git" && deployment.sourceKind !== "local") return null
  return (
    <BranchChip branch={deployment.sourceRef || record.branch || "main"} className="max-w-40" />
  )
}

/**
 * What a repository runs as: the framework detection found on the language's
 * own mark, else the language and its version, a Dockerfile as Docker, a static
 * site on nginx. The mark already says the language under a framework, and the
 * version and package manager are read where they are set, in Settings → Build.
 * An image, a stack and a template already said what they are in the source
 * fact.
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
      recipe === "python"
        ? build?.pythonVersion
        : recipe === "go"
          ? build?.goVersion
          : recipe === "node"
            ? build?.nodeVersion
            : recipe === "php"
              ? build?.phpVersion
              : recipe === "java"
                ? build?.javaVersion
                : recipe === "dotnet"
                  ? build?.dotnetVersion
                  : undefined
    return (
      <Fact mark={mark}>
        {framework
          ? frameworkLabel(framework)
          : [RECIPE_SHORT[recipe], version].filter(Boolean).join(" ")}
      </Fact>
    )
  }
  if (method === "static") return <Fact mark={mark}>Static on nginx</Fact>
  if (method === "dockerfile") return <Fact mark={mark}>{BUILD_METHOD_SHORT.dockerfile}</Fact>
  return null
}

/**
 * The live release's age stays visible, and says so only when it got there by
 * a rollback or a promotion from staging; who shipped it is read with that
 * deployment's full provenance in the history.
 */
function ReleaseLine({ run, activatedAt }: { run?: DeploymentEngineRun; activatedAt?: string }) {
  if (!run)
    return activatedAt ? <span className="truncate">{relativeTime(activatedAt)}</span> : null
  return (
    <span className="truncate">
      {run.operation === "rollback" && "rolled back "}
      {run.operation === "promote" && "promoted "}
      {relativeTime(run.endedAt ?? run.requestedAt)}
    </span>
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
      className="flex rounded-sm focus-ring"
      aria-label={`${reading.label} · automatic deployment settings`}
    >
      <Status tone={reading.tone} label={reading.label} />
    </Link>
  )
}
