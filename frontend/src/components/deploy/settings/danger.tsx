"use client"

import Link from "next/link"
import { useRouter } from "next/navigation"
import { Fragment, useState } from "react"
import {
  Archive,
  Box,
  Database,
  External,
  FolderClosed,
  Globe,
  NetworkDevice,
  Play,
  RefreshClockwise,
  RotateCounterClockwise,
  StopCircle,
  Trash,
  type Icon,
} from "@/components/icons"
import { ApiError, get, post } from "@/lib/api"
import { plural, relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import type {
  DeploymentEnvironmentConfiguration,
  DeploymentPreview,
  DeploymentRemovalPlan,
  DeploymentRemovalTarget,
  DeploymentSchedule,
  DeploymentTrigger,
} from "@/lib/types"
import { GroupRule } from "@/components/flow"
import { FormFact, FormFacts, FormNote } from "@/components/form"
import { DimActions, IconAction } from "@/components/icon-action"
import { Panel, Well } from "@/components/panel"
import { ProductLogo, imageProduct } from "@/components/product-logo"
import { Row, RowList } from "@/components/row-list"
import { EmptyNote, ErrorState, LoadingRows } from "@/components/state"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { useConfirm } from "@/components/confirm-dialog"
import { SettingSection, SettingsPage } from "@/components/deploy/settings/setting-card"
import { useConfiguration } from "@/components/deploy/settings/use-configuration"
import { humanize, projectState } from "@/components/deploy/vocabulary"
import { ProjectMark } from "@/components/deploy/project-mark"
import { useProject } from "@/components/deploy/project-context"
import { StageStrip } from "@/components/deploy/run-pipeline"
import { archiveRequest, purgeRequest, useProjectVerbs } from "@/components/deploy/project-verbs"

/**
 * Danger zone — the acts that change what this deployment is rather than
 * what it runs, in the order a deployment's life takes them: stop it, archive
 * it (or bring it back), remove what it made on the server, forget it.
 *
 * One frame, in the danger rule, around all of them: it is the page's only
 * block, and everything inside it changes what the deployment is, so the
 * edge says "careful" once instead of four red cards saying it four times.
 * Each act is one row — its name, one sentence, what exactly it touches as
 * data, and its one button beside them — so the button is never a thousand
 * pixels from the sentence that explains it. An act the reader cannot take
 * *yet* stays drawn, quiet, saying what comes first; an act their role cannot
 * take is not drawn at all.
 */
export function DangerZoneSettings({
  projectId,
  environmentId,
}: {
  projectId: number
  environmentId: number
}) {
  const state = useConfiguration(projectId, environmentId)
  return (
    <SettingsPage state={state}>
      {(configuration) => <DangerZone configuration={configuration} />}
    </SettingsPage>
  )
}

function DangerZone({ configuration }: { configuration: DeploymentEnvironmentConfiguration }) {
  const { can } = useAuth()
  const project = useProject()
  const { deployment, runtime } = project.detail
  const destructive = can("destructive")
  const stopped = projectState(deployment, runtime) === "stopped"
  // Stopping takes the service down, so it carries the destructive capability
  // the Docker stop verb carries; starting it again does not.
  const stopStart =
    can("service.control") &&
    project.normalized &&
    Boolean(deployment.liveReleaseId) &&
    !project.archived &&
    (stopped || destructive)
  const restore = project.archived && can("system.admin")
  const rows = [
    stopStart && <StopStartRow key="stop" configuration={configuration} stopped={stopped} />,
    destructive && !project.archived && <ArchiveRow key="archive" configuration={configuration} />,
    restore && <RestoreRow key="restore" />,
    destructive && <RemovalRow key="removal" />,
    destructive && <PurgeRow key="purge" configuration={configuration} />,
  ].filter(Boolean)

  return (
    <SettingSection
      title="Danger zone"
      tone="danger"
      state={<Lifecycle archived={project.archived} />}
    >
      {rows.length === 0 ? (
        <EmptyNote className="px-0 text-left">
          Your role cannot stop, archive or delete this deployment.
        </EmptyNote>
      ) : (
        <Panel className="divide-y divide-hairline border-rule-danger">{rows}</Panel>
      )}
    </SettingSection>
  )
}

/**
 * Where the deployment is in its life — in use, archived, forgotten — as the
 * three steps of a path with the current one lit, so the order the rows
 * below go in is seen before it is read.
 */
function Lifecycle({ archived }: { archived: boolean }) {
  const steps = ["In use", "Archived", "Deleted"]
  const at = archived ? 1 : 0
  return <StageStrip steps={steps} current={at} label={`Now: ${steps[at]}`} />
}

/**
 * One act: its name, one sentence, what it touches, and its button beside
 * them. With no button it is an act that cannot be taken yet, and its title
 * goes quiet.
 */
function DangerRow({
  title,
  sentence,
  facts,
  action,
  children,
}: {
  title: string
  sentence: React.ReactNode
  facts?: React.ReactNode
  action?: React.ReactNode
  children?: React.ReactNode
}) {
  return (
    <div className="min-w-0 px-5 py-4">
      <div className="grid min-w-0 gap-x-8 gap-y-3 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-center">
        <div className="min-w-0 space-y-1">
          {/* Under the section's own "Danger zone" heading, not beside it. */}
          <h4 className={cn("text-sm font-medium", !action && "text-muted-foreground")}>{title}</h4>
          <p className="max-w-prose text-hint leading-relaxed text-muted-foreground">{sentence}</p>
          {facts && <div className="space-y-0.5 pt-1">{facts}</div>}
        </div>
        {action && <div className="flex max-sm:[&>*]:w-full">{action}</div>}
      </div>
      {children}
    </div>
  )
}

/**
 * What an act touches, as a label and the things themselves. It wraps where
 * a fact line would cut it short: on a phone the list is the part to read.
 */
function Reach({
  label,
  mono,
  children,
}: {
  label: string
  mono?: boolean
  children: React.ReactNode
}) {
  return (
    <p className="text-hint leading-relaxed text-muted-foreground">
      {/* The gap FormFact puts between its label and value, beside them in one row. */}
      <span className="mr-1">{label}</span>{" "}
      <span className={cn("text-foreground", mono && "font-mono break-words")}>{children}</span>
    </p>
  )
}

/**
 * Stop or start, as the header's menu offers it: the same verb, so the same
 * confirmation before the live containers go down. Stop is not drawn in the
 * danger colour — it is undone by Start, and the verb list does not mark it.
 */
function StopStartRow({
  configuration,
  stopped,
}: {
  configuration: DeploymentEnvironmentConfiguration
  stopped: boolean
}) {
  const project = useProject()
  const { confirm, dialog } = useConfirm()
  const { deployment, runtime } = project.detail
  const operation = stopped ? "start" : "stop"
  const verb = useProjectVerbs(deployment, {
    confirm,
    refresh: project.refresh,
    start: project.start,
    starting: project.starting,
    runtime,
  }).find((entry) => entry.key === operation)
  const containers = runtime?.services.length ?? deployment.serviceCount
  const hosts = configuration.domains.map((domain) => domain.hostname)
  return (
    <DangerRow
      title={stopped ? "Start the application" : "Stop the application"}
      sentence={
        stopped
          ? "Start the stopped containers and check they answer."
          : "Stop the live containers. Visitors get an error until you start it again."
      }
      facts={
        <>
          <FormFacts>
            {project.liveRelease && (
              <FormFact label="Release">#{project.liveRelease.number}</FormFact>
            )}
            {containers !== undefined && (
              <FormFact label="Runs">{plural(containers, "container")}</FormFact>
            )}
          </FormFacts>
          {hosts.length > 0 && (
            <Reach label={stopped ? "Waiting to start" : "Stop answering"} mono>
              {/* A line breaks between hosts, never inside one — unless one
                  host is wider than the line itself. */}
              {hosts.map((host, index) => (
                <Fragment key={host}>
                  {index > 0 && " "}
                  <span className="inline-block max-w-full break-words">
                    {host}
                    {index < hosts.length - 1 && ","}
                  </span>
                </Fragment>
              ))}
            </Reach>
          )}
        </>
      }
      action={
        <Button
          variant="outline"
          // A run in flight holds both verbs back; Start is not offered at all then.
          disabled={!verb || verb.disabled}
          pending={project.starting === operation}
          onClick={verb?.run}
        >
          {stopped ? <Play className="size-3.5" /> : <StopCircle className="size-3.5" />}
          {project.starting === operation
            ? stopped
              ? "Starting…"
              : "Stopping…"
            : stopped
              ? "Start"
              : "Stop"}
        </Button>
      }
    >
      {dialog}
    </DangerRow>
  )
}

/**
 * What archiving turns off and what it leaves, read once from the records it
 * touches: the senders it disables, and the things on the server it keeps.
 */
function useArchiveReach(configuration: DeploymentEnvironmentConfiguration) {
  const project = useProject()
  const base = `/deploy/${project.projectId}/environments/${project.environmentId}`
  const engine = { enabled: project.normalized && !project.archived }
  const triggers = usePoll(
    (signal) => get<DeploymentTrigger[]>(`${base}/triggers`, undefined, signal),
    0,
    [base],
    engine,
  )
  const schedules = usePoll(
    (signal) => get<DeploymentSchedule[]>(`${base}/schedules`, undefined, signal),
    0,
    [base],
    engine,
  )
  const previews = usePoll(
    (signal) =>
      get<DeploymentPreview[]>(`/deploy/${project.projectId}/previews`, undefined, signal),
    0,
    [project.projectId],
    engine,
  )
  const watch = project.gitWatch
  const stops = [
    watch?.automatic && watch.branch && `git watching on ${watch.branch}`,
    triggers.data?.filter((one) => one.enabled).length &&
      plural(triggers.data.filter((one) => one.enabled).length, "webhook"),
    schedules.data?.filter((one) => one.enabled).length &&
      plural(schedules.data.filter((one) => one.enabled).length, "schedule"),
    previews.data?.filter((one) => one.state === "open").length &&
      plural(previews.data.filter((one) => one.state === "open").length, "preview environment"),
  ].filter(Boolean) as string[]
  const volumes = (configuration.runtime.mounts ?? []).filter(
    (mount) => !mount.source.startsWith("/"),
  )
  const keeps = [
    "its runtime",
    configuration.domains.length > 0 && plural(configuration.domains.length, "domain"),
    volumes.length > 0 && plural(volumes.length, "volume"),
    project.releases.length > 0 && plural(project.releases.length, "release"),
  ].filter(Boolean) as string[]
  return { stops, keeps }
}

function ArchiveRow({ configuration }: { configuration: DeploymentEnvironmentConfiguration }) {
  const project = useProject()
  const { confirm, dialog } = useConfirm()
  const { stops, keeps } = useArchiveReach(configuration)
  const facts = (
    <>
      <Reach label="Stops">{stops.length > 0 ? stops.join(" · ") : "nothing is automatic"}</Reach>
      <Reach label="Keeps">{keeps.join(" · ")}</Reach>
    </>
  )
  return (
    <DangerRow
      title="Archive this deployment"
      sentence="Turn off everything that deploys it by itself and take it off the active list. What runs keeps running."
      facts={facts}
      action={
        <Button
          variant="destructive"
          onClick={() =>
            confirm(
              archiveRequest(
                {
                  id: project.projectId,
                  name: project.detail.deployment.name,
                  mark: <ProjectMark deployment={project.detail.deployment} size="sm" />,
                },
                { reach: facts, onDone: () => project.markArchived() },
              ),
            )
          }
        >
          <Archive className="size-3.5" /> Archive
        </Button>
      }
    >
      {dialog}
    </DangerRow>
  )
}

/**
 * The way back from an archive. Restoring brings the name and the listing
 * back and nothing else: archiving switched the webhooks and schedules off,
 * and they stay off until someone turns them on again, which the row says
 * before it is pressed.
 */
function RestoreRow() {
  const project = useProject()
  const [busy, setBusy] = useState(false)
  const archivedAt = project.detail.project.archivedAt
  const restore = async () => {
    setBusy(true)
    try {
      await post(`/deploy/${project.projectId}/unarchive`, {})
      notify.success(`${project.detail.deployment.name} restored`)
      project.markUnarchived()
    } catch (error) {
      notify.error(
        "Could not restore the deployment",
        error instanceof ApiError && error.code === "name_taken"
          ? `Another deployment is already called ${project.detail.deployment.name}. Rename that one first.`
          : error,
      )
    } finally {
      setBusy(false)
    }
  }
  return (
    <DangerRow
      title="Restore this deployment"
      sentence="Put it back on the active list under its own name."
      facts={
        <FormFacts>
          <FormFact label="Archived">{archivedAt ? relativeTime(archivedAt) : "just now"}</FormFact>
          <span>webhooks and schedules stay off until you turn them back on</span>
        </FormFacts>
      }
      action={
        <Button variant="outline" pending={busy} onClick={() => void restore()}>
          <RotateCounterClockwise className="size-3.5" />
          {busy ? "Restoring…" : "Restore"}
        </Button>
      }
    />
  )
}

/** What each kind of managed resource is drawn as. */
const TARGET_GLYPH: Record<string, Icon> = {
  docker_volume: Archive,
  bind_path: FolderClosed,
  deployment_database_network: NetworkDevice,
  proxy_site: Globe,
  backup_job: Archive,
  database_connection: Database,
}

function targetProduct(target: DeploymentRemovalTarget) {
  switch (target.kind) {
    case "docker_container":
      return "docker"
    case "compose_stack":
      return "docker-compose"
    case "docker_image":
      return imageProduct(target.displayName.split(" · ")[0])
    default:
      return undefined
  }
}

const OWNER_WORD: Record<string, string> = {
  docker: "Docker",
  proxy: "Proxy",
  backups: "Backups",
  backup: "Backups",
  databases: "Databases",
  database: "Databases",
}

/** Ids, paths and digests are literals from the host, and read as such. */
function literal(target: DeploymentRemovalTarget) {
  return /^[/.]|^sha256:|^[0-9a-f]{12,}$|[/:@]/.test(target.displayName)
}

/**
 * What this deployment made on the server and can take away again: its
 * containers, volumes, routes and the rest, each drawn as the thing it is and
 * grouped by who owns it, with the ones that hold data marked. It is read
 * as soon as the deployment is archived. Before then the row stays drawn and
 * says archiving comes first, so a reader of a live deployment can see how
 * deletion works without being handed a button for it.
 */
function RemovalRow() {
  const project = useProject()
  const { confirm, dialog } = useConfirm()
  const plan = usePoll(
    (signal) =>
      post<DeploymentRemovalPlan>(`/deploy/${project.projectId}/removal-plan`, {}, { signal }),
    0,
    [project.projectId, project.archived],
    { enabled: project.archived },
  )
  // The poll says loading only before its first answer, so a Refresh pressed
  // after it is its own flag, down again once a new answer or error lands.
  const [asked, setAsked] = useState<{ data: unknown; error: unknown }>()
  const refreshing = asked !== undefined && asked.data === plan.data && asked.error === plan.error
  const targets = plan.data?.targets ?? []
  const owners = [...new Set(targets.map((target) => target.owner))]
  const holding = targets.filter((target) => target.data).length

  const remove = (target: DeploymentRemovalTarget) => {
    const current = plan.data
    if (!current) return
    confirm({
      title: `Remove ${target.displayName}`,
      confirmLabel: "Remove managed resource",
      phrase: target.confirmationType === "typed" ? target.confirmationPhrase : undefined,
      subject: {
        mark: <TargetMark target={target} />,
        name: target.displayName,
        facts: <FormFact label="Kind">{humanize(target.kind)}</FormFact>,
      },
      description: (
        <>
          <p>Only this exact managed {humanize(target.kind).toLowerCase()} is removed.</p>
          <Well className="break-all">{target.resourceId}</Well>
          {target.data && (
            <FormNote tone="danger">It holds persistent data, which goes with it.</FormNote>
          )}
        </>
      ),
      action: async (confirmation) => {
        await post(
          `/deploy/${project.projectId}/remove-managed`,
          { planDigest: current.digest, targetIds: [target.id] },
          { confirm: confirmation },
        )
        plan.refresh()
      },
    })
  }

  if (!project.archived)
    return (
      <DangerRow
        title="Remove managed resources"
        sentence="Remove the containers, volumes, routes and other resources this deployment created. Linked and observed resources are never touched."
        facts={
          <FormFacts>
            <span>Archive the deployment first</span>
          </FormFacts>
        }
      />
    )

  return (
    <DangerRow
      title="Remove managed resources"
      sentence="Remove the containers, volumes, routes and other resources this deployment created. Linked and observed resources are never touched."
      facts={
        plan.data && (
          <FormFacts>
            <span>
              <span className="numeric text-foreground">
                {plural(targets.length, "managed resource")}
              </span>
              {holding > 0 && (
                <>
                  {" · "}
                  <span className="numeric text-warning">{holding} hold data</span>
                </>
              )}
            </span>
          </FormFacts>
        )
      }
      action={
        <Button
          variant="ghost"
          size="sm"
          pending={plan.loading || refreshing}
          onClick={() => {
            setAsked({ data: plan.data, error: plan.error })
            plan.refresh()
          }}
        >
          <RefreshClockwise className="size-3.5" /> Refresh
        </Button>
      }
    >
      <div className="mt-3 space-y-3">
        {plan.error && !plan.data ? (
          <ErrorState error={plan.error} onRetry={plan.refresh} />
        ) : plan.loading && !plan.data ? (
          <LoadingRows rows={2} />
        ) : targets.length === 0 ? (
          <EmptyNote className="px-0 py-2 text-left">
            No managed resource is left to remove.
          </EmptyNote>
        ) : (
          owners.map((owner) => {
            const owned = targets.filter((target) => target.owner === owner)
            return (
              <section key={owner} className="animate-rise space-y-1">
                <GroupRule label={OWNER_WORD[owner] ?? humanize(owner)} count={owned.length} />
                <RowList aria-label={`Managed resources, ${OWNER_WORD[owner] ?? owner}`}>
                  {owned.map((target) => (
                    <Row
                      key={target.id}
                      className="-mx-2 px-2"
                      leading={<TargetMark target={target} />}
                      title={
                        <span className={cn(literal(target) && "font-mono")}>
                          {target.displayName}
                        </span>
                      }
                      subtitle={[
                        humanize(target.kind),
                        target.confirmationType === "typed" && "type its name to remove",
                      ]
                        .filter(Boolean)
                        .join(" · ")}
                      trailing={
                        <>
                          {target.data && (
                            <Tag tone="warning" className="max-sm:hidden">
                              holds data
                            </Tag>
                          )}
                          <DimActions>
                            {target.deepLink && (
                              <IconAction label={`Open ${target.displayName}`} asChild>
                                <Link href={target.deepLink}>
                                  <External />
                                </Link>
                              </IconAction>
                            )}
                            {plan.data?.archived && (
                              <Button
                                size="xs"
                                variant="destructive"
                                onClick={() => remove(target)}
                              >
                                <Trash /> Remove
                              </Button>
                            )}
                          </DimActions>
                        </>
                      }
                    />
                  ))}
                </RowList>
              </section>
            )
          })
        )}
      </div>
      {dialog}
    </DangerRow>
  )
}

function TargetMark({ target }: { target: DeploymentRemovalTarget }) {
  return (
    <ProductLogo size="sm" id={targetProduct(target)} fallback={TARGET_GLYPH[target.kind] ?? Box} />
  )
}

function PurgeRow({ configuration }: { configuration: DeploymentEnvironmentConfiguration }) {
  const router = useRouter()
  const project = useProject()
  const { confirm, dialog } = useConfirm()
  const record = project.detail.project
  // Archived here, not when the next detail poll brings archivedAt: the
  // server refuses a purge of a live deployment on its own.
  const ready = project.archived
  const facts = (
    <>
      <Reach label="Forgets">
        configuration (revision {configuration.revision}) ·{" "}
        {plural(configuration.variables.length, "variable")} ·{" "}
        {plural(project.releases.length, "release")} and their logs
      </Reach>
      <Reach label="Leaves">
        containers, routes, images, files, data — remove them above first
      </Reach>
    </>
  )
  if (!ready)
    return (
      <DangerRow
        title="Delete permanently"
        sentence="Forget this deployment's configuration, variables and history. Host resources are left as they are."
        facts={
          <FormFacts>
            <span>Archive the deployment first</span>
          </FormFacts>
        }
      />
    )
  return (
    <DangerRow
      title="Delete permanently"
      sentence="Forget this deployment's configuration, variables and history. Host resources are left as they are."
      facts={facts}
      action={
        <Button
          variant="destructive"
          onClick={() =>
            confirm(
              purgeRequest(
                {
                  id: project.projectId,
                  name: record.name,
                  mark: <ProjectMark deployment={project.detail.deployment} size="sm" />,
                },
                {
                  variables: configuration.variables.length,
                  releases: project.releases.length,
                  onDone: () => router.push("/deploy?view=archived"),
                },
              ),
            )
          }
        >
          <Trash className="size-3.5" /> Delete permanently
        </Button>
      }
    >
      {dialog}
    </DangerRow>
  )
}
