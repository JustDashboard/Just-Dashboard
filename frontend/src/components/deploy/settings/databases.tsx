"use client"

import { createRef, useMemo, useRef, useState } from "react"
import type { FormEvent } from "react"
import Link from "next/link"
import {
  Archive,
  ArrowUpRight,
  Connection,
  Copy,
  Database,
  Lightning,
  Pencil,
  Play,
  Plus,
  Trash,
  Warning,
} from "@/components/icons"
import { ApiError, del, get, post, put, refusedIndex } from "@/lib/api"
import { bytes, plural, relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import { copyText } from "@/lib/clipboard"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import type {
  BackupJob,
  DbConnection,
  DeploymentBackupGateEvidence,
  DeploymentBackupJob,
  DeploymentDatabaseLink,
  DeploymentEnvironmentConfiguration,
  DeploymentRunSnapshot,
  DeploymentStorageMount,
  DeploymentVariable,
  DockerVolume,
} from "@/lib/types"
import { JobCard } from "@/components/backups/job-card"
import { destinationProduct } from "@/components/backups/marks"
import { scheduleLabel } from "@/components/backups/shared"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { Field, OptionList, OptionRow } from "@/components/form"
import { IconAction } from "@/components/icon-action"
import { ProductGlyph, ProductGlyphs, ProductLogo, ProductLogos } from "@/components/product-logo"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { EmptyNote, EmptyState, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import type { Tone } from "@/components/tone"
import { VerbActions, type Verb } from "@/components/verbs"
import { useConfirm } from "@/components/confirm-dialog"
import { Button } from "@/components/ui/button"
import { AnimatedBeam } from "@/components/ui/animated-beam"
import {
  InputGroup,
  InputGroupAddon,
  InputGroupInput,
  InputGroupText,
} from "@/components/ui/input-group"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import {
  SettingForm,
  SettingSection,
  SettingsPage,
  settingStatus,
} from "@/components/deploy/settings/setting-card"
import { SettingPicture } from "@/components/deploy/settings/setting-picture"
import { useConfiguration, useSettingDraft } from "@/components/deploy/settings/use-configuration"
import {
  DATABASE_ENGINE_LABELS,
  humanize,
  LINK_STATUS,
  MOUNT_STATUS,
  MountMark,
} from "@/components/deploy/vocabulary"
import { volumeProduct } from "@/components/deploy/service-product"
import { WireMark, WireNode } from "@/components/deploy/wire"
import { ProjectMark } from "@/components/deploy/project-mark"
import { useColumnWidth } from "@/components/deploy/settings/use-column-width"
import { OwnershipSelect } from "@/components/deploy/settings/mounts"
import { useMediaQuery } from "@/hooks/use-mobile"
import { useProject } from "@/components/deploy/project-context"
import { ProjectDatabase } from "@/components/deploy/project-database"
import { withPreviousConnectionShape } from "@/components/deploy/deployment-defaults"

/**
 * Databases & backups — what the release reaches and what protects it.
 *
 * The page read as a framed card of eyebrow-headed blocks whose one Save did
 * not apply to its first block, a backup policy that was a select, a raw cron
 * string and a cluster of underlined links, and readings that truncated to
 * "Not observed rec…". It is three sections now, in the settings frame's
 * rail, under four short readings:
 *
 *   Linked databases — linking is its own write (it also writes the variable
 *   that carries the address), so it sits outside the form whose Save it
 *   never used. Each database is a card drawn as its engine that opens its
 *   connection, saying which variable carries it and whether the policy's job
 *   takes a native dump of it; above them, where there are any, a picture of
 *   how the application reaches them over its managed network, the line
 *   being the link's state.
 *
 *   Backups & volumes — one form, one Save. The job a release gates on is
 *   drawn as the Backups page draws it (`JobCard`): its products, its last
 *   run in colour, where it writes and its last fourteen runs; the policy's
 *   options are sentences under it. A volume the release needs is a row with
 *   its size and whether the live release found it.
 *
 *   Latest backup gate evidence — what the last deployment's gate recorded,
 *   each job a card that opens the job.
 */

type Dependency = DeploymentEnvironmentConfiguration["dependencies"][number]

type BackupDependencyConfig = {
  requiredBeforeDeploy?: boolean
  requireRestoreTest?: boolean
  maxAgeSeconds?: number
}

export function DatabasesSettings({
  projectId,
  environmentId,
}: {
  projectId: number
  environmentId: number
}) {
  const state = useConfiguration(projectId, environmentId)
  const links = usePoll(
    (signal) =>
      get<DeploymentDatabaseLink[]>(
        `/deploy/${projectId}/environments/${environmentId}/database-links`,
        undefined,
        signal,
      ),
    5000,
    [projectId, environmentId],
  )
  // The jobs themselves, not only their names: whether the one this release
  // gates on dumps the databases beside it is the question this page exists to
  // answer before a deployment answers it the hard way.
  const jobs = usePoll((signal) => get<BackupJob[]>("/backups/", undefined, signal), 15000)
  return (
    <SettingsPage
      state={state}
      pageKinds={["dependency"]}
      readings={(configuration) => (
        <DatabaseReadings configuration={configuration} links={links.data} jobs={jobs.data} />
      )}
    >
      {(configuration) => (
        <DatabasesBody
          projectId={projectId}
          environmentId={environmentId}
          configuration={configuration}
          save={state.save}
          refresh={state.refresh}
          links={links}
          jobs={jobs}
        />
      )}
    </SettingsPage>
  )
}

type Poll<T> = ReturnType<typeof usePoll<T>>

function DatabasesBody({
  projectId,
  environmentId,
  configuration,
  save,
  refresh,
  links,
  jobs,
}: {
  projectId: number
  environmentId: number
  configuration: DeploymentEnvironmentConfiguration
  save: ReturnType<typeof useConfiguration>["save"]
  refresh: () => void
  links: Poll<DeploymentDatabaseLink[]>
  jobs: Poll<BackupJob[]>
}) {
  const { can } = useAuth()
  const canAdmin = can("system.admin")
  const canRun = can("service.control")
  const project = useProject()
  const { confirm, dialog } = useConfirm()
  // The three lists share one fields column; readings go beside a name
  // where it has room and under it where it does not.
  const [column, columnWidth] = useColumnWidth()
  const wide = columnWidth >= 480
  // Where SettingPicture lays its marks out in a row rather than a column.
  const pictureRow = useMediaQuery("(min-width: 1024px)")
  const databaseDependencies = configuration.dependencies.filter((d) => d.kind === "database")
  const savedOthers = configuration.dependencies.filter((d) => d.kind !== "database")
  // Backup and volume dependencies stage through one draft and one Save, the
  // way the other settings forms do; a database link is its own mutation (it
  // also writes a variable), so it is never staged here.
  const draft = useSettingDraft(`deploy.${projectId}.settings.dependencies`, savedOthers)
  const otherDependencies = draft.value
  const setOtherDependencies = draft.set
  const [saving, setSaving] = useState(false)
  const [busy, setBusy] = useState<string>()
  // The backup rows whose job is being changed: drawn as the job picker
  // rather than as the job's card.
  const [changing, setChanging] = useState<number[]>([])
  const [dependencyError, setDependencyError] = useState<{
    kind: "database" | "other"
    index: number
    message: string
  }>()

  const linkFor = (resourceId?: string) =>
    links.data?.find((link) => String(link.connectionId) === resourceId)
  const jobFor = (resourceId?: string) => jobs.data?.find((job) => String(job.id) === resourceId)
  const driverOf = (connectionId: number): string | undefined =>
    links.data?.find((link) => link.connectionId === connectionId)?.driver

  const volumeDeps = otherDependencies.filter((d) => d.kind === "storage")
  const volumes = usePoll(
    (signal) => get<DockerVolume[]>("/docker/volumes/", undefined, signal),
    60000,
    [],
    { enabled: volumeDeps.length > 0 },
  )

  const latestRunId = project.runs[0]?.id ?? project.detail.deployment.lastRun?.id
  const latestRun = usePoll(
    (signal) =>
      get<DeploymentRunSnapshot>(`/deploy/${projectId}/runs/${latestRunId}`, undefined, signal),
    0,
    [projectId, latestRunId],
    { enabled: Boolean(latestRunId) },
  )
  const backupGateEvidence = (() => {
    const step = latestRun.data?.steps.find((item) => item.key === "backup_gate")
    const backups = step?.evidence?.backups
    return Array.isArray(backups) ? (backups as DeploymentBackupGateEvidence[]) : []
  })()

  const configBase = {
    revision: configuration.revision,
    build: configuration.build,
    runtime: configuration.runtime,
    checks: configuration.checks,
    domains: configuration.domains,
  }

  // Which variables carry this database. A typed reference is parsed by the
  // backend and reported beside the masked value, so this needs no reveal.
  // A reference may ask for a connection shape or another database on the
  // same server (`5.jdbc`, `5.url.app_cache`); it still reads connection 5.
  const variablesFor = (resourceId?: string): DeploymentVariable[] =>
    configuration.variables.filter(
      (variable) =>
        variable.reference?.kind === "database" &&
        variable.reference.target.split(".")[0] === resourceId,
    )

  const connect = async (connection: DbConnection, url: string, variable: string) => {
    try {
      // The saved copy, not the staged rows: a half-filled "Add backup" row
      // the operator has not pressed Save on has no business riding along on
      // a database link, which the "invalid planned dependency" refusal made
      // painfully literal.
      const nextDependencies: Dependency[] = [
        ...savedOthers,
        ...databaseDependencies,
        {
          kind: "database",
          ownership: "linked",
          resourceKind: "database_connection",
          resourceId: String(connection.id),
          config: {},
        },
      ]
      const updated = await put<DeploymentEnvironmentConfiguration>(
        `/deploy/${projectId}/environments/${environmentId}/configuration`,
        { ...configBase, dependencies: nextDependencies },
      )
      // Relinking keeps the scopes the variable already had: a DATABASE_URL a
      // build reads (a static env import, a prerendered page, Prisma's config)
      // lost its build scope here and the next build failed. A new one reaches
      // the build too, the way a database linked while creating the project does.
      const existing = configuration.variables.find((entry) => entry.name === variable)
      await put(`/deploy/${projectId}/environments/${environmentId}/variables/${variable}`, {
        revision: updated.revision,
        value: withPreviousConnectionShape(
          url,
          existing?.reference?.kind === "database" ? existing.reference.target : undefined,
        ),
        sensitivity: "secret",
        scopes: existing ? [...new Set([...existing.scopes, "runtime"])] : ["runtime", "build"],
      })
      links.refresh()
      refresh()
      notify.success("Database linked")
    } catch (error) {
      notify.error("Could not link database", error)
    }
  }

  /**
   * Removing a database means removing the variable that carries it.
   *
   * Runtime activation decides which databases to attach by reading the
   * resolved variables for a `db-N.jd.internal` host, not by reading the
   * dependency rows. Dropping the row alone left a link the page no longer
   * listed and the next deployment attached anyway, so the variables are named
   * in the confirmation and go with it.
   */
  const removeDatabase = (dependency: Dependency, index: number) => {
    const link = linkFor(dependency.resourceId)
    const name = link?.name ?? `Database ${dependency.resourceId}`
    const carriers = variablesFor(dependency.resourceId)
    const nextDatabaseDeps = databaseDependencies.filter((_, i) => i !== index)
    const subject = {
      mark: <ProductLogo size="sm" id={link?.driver} fallback={Database} />,
      name,
      facts: link && (
        <span className="font-mono">
          {link.hostname} · {link.database}
        </span>
      ),
    }
    const apply = async () => {
      // Every write advances the environment's desired revision, and the next
      // one has to send the revision the last handed back or it is refused as
      // a stale edit. The variables go first: they are what attaches the
      // database, so a failure after them leaves it detached rather than
      // silently still connected.
      let revision = configuration.revision
      for (const variable of carriers) {
        const result = await del<{ desiredRevision: number }>(
          `/deploy/${projectId}/environments/${environmentId}/variables/${variable.name}`,
          { body: { revision } },
        )
        revision = result.desiredRevision
      }
      await put(`/deploy/${projectId}/environments/${environmentId}/configuration`, {
        ...configBase,
        revision,
        dependencies: [...savedOthers, ...nextDatabaseDeps],
      })
      links.refresh()
      refresh()
    }
    if (carriers.length === 0 && !link) {
      void apply()
        .then(() => notify.success("Dependencies saved"))
        .catch((error) => notify.error("Could not remove database", error))
      return
    }
    if (carriers.length === 0) {
      // Bound, but by a value this page cannot identify — an address written
      // literally rather than as the typed reference. It cannot be removed
      // from here without guessing which variable holds it.
      confirm({
        title: `Remove ${name}`,
        confirmLabel: "Remove the link",
        subject,
        description: (
          <>
            <p>
              This page finds no variable referencing {name}, which is bound on the managed network
              all the same. If one holds its address literally, the next deployment attaches {name}{" "}
              again until that variable is removed in Variables.
            </p>
            <p>The database itself, its container and its data are left alone.</p>
          </>
        ),
        action: apply,
      })
      return
    }
    confirm({
      title: `Remove ${name}`,
      confirmLabel: "Remove the link and its variables",
      subject,
      description: (
        <>
          <p>
            {carriers.map((variable) => variable.name).join(", ")}{" "}
            {carriers.length === 1 ? "carries" : "carry"} this database, and{" "}
            {carriers.length === 1 ? "it is" : "they are"} what attaches it when the release starts.
            Removing the link without {carriers.length === 1 ? "the variable" : "the variables"}{" "}
            leaves the database attached on the next deployment.
          </p>
          <p>The database itself, its container and its data are left alone.</p>
        </>
      ),
      action: apply,
    })
  }

  const testConnection = async (connectionId: string, name: string) => {
    setBusy(`ping-${connectionId}`)
    try {
      const health = await get<{ ok: boolean; error?: string }>(`/databases/${connectionId}/ping`)
      if (health.ok) notify.success(`${name} answered`)
      else notify.error(`${name} did not answer`, health.error)
    } catch (error) {
      notify.error(`${name} did not answer`, error)
    } finally {
      setBusy(undefined)
    }
  }

  const copyApplicationURL = async (connectionId: string) => {
    setBusy(`url-${connectionId}`)
    try {
      const revealed = await get<{ url: string }>(`/databases/${connectionId}/url`, {
        target: "container",
      })
      await copyText(revealed.url, "Connection string copied")
    } catch (error) {
      notify.error("Could not read the connection string", error)
    } finally {
      setBusy(undefined)
    }
  }

  const runBackup = async (job: BackupJob) => {
    setBusy(`run-${job.id}`)
    try {
      await post(`/backups/${job.id}/run`)
      notify.success(`${job.name} started`, { description: "Progress appears in Backups." })
      jobs.refresh()
    } catch (error) {
      notify.error("Could not start the backup", error)
    } finally {
      setBusy(undefined)
    }
  }

  /**
   * Adds a linked database to a job's native dumps.
   *
   * The whole job goes back because that is the shape the route takes; the
   * destination's keys are not in it and are preserved precisely because they
   * are absent, and the recovery check's expected-output digest rides along as
   * the value the job already holds.
   */
  const addDump = async (job: BackupJob, connectionId: number) => {
    setBusy(`dump-${job.id}-${connectionId}`)
    try {
      await put(`/backups/${job.id}`, {
        name: job.name,
        sources: job.sources,
        excludes: job.excludes,
        targetKind: job.targetKind,
        target: job.target,
        schedule: job.schedule,
        retention: job.retention,
        retentionDays: job.retentionDays,
        enabled: job.enabled,
        recovery: job.recovery,
        sqlitePaths: job.sqlitePaths,
        databaseDumps: [...(job.databaseDumps ?? []), connectionId],
        pauseContainers: job.pauseContainers,
      })
      notify.success(`${job.name} now dumps this database`)
      jobs.refresh()
    } catch (error) {
      notify.error("Could not add the dump", error)
    } finally {
      setBusy(undefined)
    }
  }

  const onSaveOthers = async (event: FormEvent) => {
    event.preventDefault()
    setSaving(true)
    setDependencyError(undefined)
    const merged = [...databaseDependencies, ...otherDependencies]
    try {
      await save({ dependencies: merged })
      setChanging([])
      notify.success("Dependencies saved")
    } catch (error) {
      const index =
        error instanceof ApiError ? refusedIndex(error.field, "dependencies") : undefined
      if (error instanceof ApiError && index !== undefined && index < merged.length) {
        setDependencyError(
          index < databaseDependencies.length
            ? { kind: "database", index, message: error.message }
            : { kind: "other", index: index - databaseDependencies.length, message: error.message },
        )
      } else {
        notify.error("Could not save dependencies", error)
      }
    } finally {
      setSaving(false)
    }
  }

  const errorFor = (kind: "database" | "other", index: number) =>
    dependencyError?.kind === kind && dependencyError.index === index
      ? dependencyError.message
      : undefined

  const updateDependency = (index: number, next: Dependency) =>
    setOtherDependencies(otherDependencies.map((item, i) => (i === index ? next : item)))
  const removeOther = (index: number) => {
    setOtherDependencies(otherDependencies.filter((_, i) => i !== index))
    setChanging((current) =>
      current.filter((item) => item !== index).map((item) => (item > index ? item - 1 : item)),
    )
  }
  const addOther = (dependency: Dependency) =>
    setOtherDependencies([...otherDependencies, dependency])

  const observedBackups = project.operations?.backups
  const observedStorage = project.operations?.storage
  // A volume is drawn as the product that keeps its data in it, as on Storage.
  const volumeMark = volumeProduct(
    configuration.runtime.image || project.operations?.runtime.services[0]?.image,
    project.detail.deployment.sourceKind,
    project.product,
  )
  const observedBackupFor = (resourceId?: string) =>
    observedBackups?.status === "available"
      ? observedBackups.jobs.find((job) => job.resourceId === resourceId)
      : undefined
  const observedVolumeFor = (resourceId?: string) =>
    observedStorage?.status === "available"
      ? observedStorage.mounts.find((mount) => mount.source === resourceId)
      : undefined

  // A binding can outlive the variable that made it — removing a variable does
  // not detach a database from retained releases — so an observed link is not
  // proof that a variable still holds the address. It only rules out saying
  // nothing carries it.
  const boundWithoutReference = databaseDependencies.some(
    (dependency) =>
      variablesFor(dependency.resourceId).length === 0 && Boolean(linkFor(dependency.resourceId)),
  )
  const allCarriers = [
    ...new Set(
      databaseDependencies.flatMap((dependency) =>
        variablesFor(dependency.resourceId).map((variable) => variable.name),
      ),
    ),
  ]
  const savedBackups = savedOthers
    .filter((dependency) => dependency.kind === "backup")
    .map((dependency) => jobFor(dependency.resourceId))
    .filter((job): job is BackupJob => Boolean(job))

  const backupRows = otherDependencies
    .map((dependency, index) => ({ dependency, index }))
    .filter(({ dependency }) => dependency.kind === "backup")
  const volumeRows = otherDependencies
    .map((dependency, index) => ({ dependency, index }))
    .filter(({ dependency }) => dependency.kind === "storage")
  const observedLinks = databaseDependencies
    .map((dependency) => linkFor(dependency.resourceId))
    .filter((link): link is DeploymentDatabaseLink => Boolean(link))

  return (
    <>
      <SettingSection
        title="Linked databases"
        state={
          // Which variable carries each database is its row's own "via"
          // link; the head says only what the rows cannot — that none does.
          databaseDependencies.length > 0 && (
            <p>
              {allCarriers.length === 0 &&
                (boundWithoutReference
                  ? "Bound on the managed network; no variable names one by reference. "
                  : "No variable carries a linked database. ")}
              Last checked{" "}
              {links.data?.[0]?.checkedAt ? relativeTime(links.data[0].checkedAt) : "never"}.
            </p>
          )
        }
        actions={canAdmin && <ProjectDatabase target="container" onConnect={connect} />}
      >
        <div ref={column} className="min-w-0 space-y-5">
          {databaseDependencies.length === 0 ? (
            <EmptyState
              icon={Database}
              mark={<ProductLogos ids={["postgres", "mysql", "redis", "mongodb"]} size="md" />}
              title="No database is linked"
              description="Start one or connect a saved connection; its address reaches the application through a variable."
            />
          ) : (
            <>
              {/* Narrow, the picture stacks its marks in one column and the
                  lines run straight down them, so two databases side by side
                  read as a chain through each other. Every fact in it is in
                  the rows, so it is drawn only where its shape is true. */}
              {observedLinks.length > 0 && (pictureRow || observedLinks.length === 1) && (
                <DatabasePicture links={observedLinks} deployment={project.detail.deployment} />
              )}
              <ChoiceList aria-label="Linked databases" className="animate-rise">
                {databaseDependencies.map((dependency, index) => {
                  const link = linkFor(dependency.resourceId)
                  const status = LINK_STATUS[link?.status ?? "pending"]
                  // The dependency is what the verbs act on, not the observation:
                  // a link whose binding has not been reported still has to be
                  // removable, which is exactly the state an operator reaches
                  // this page in when it stopped working.
                  const id = dependency.resourceId ?? ""
                  const name = link?.name ?? `Database ${id}`
                  const carriers = variablesFor(dependency.resourceId)
                  const failing = link?.status === "unavailable"
                  // A binding exists because a variable named this database, so
                  // an observed link is proof one carries it even when the value
                  // is a literal URL rather than the typed reference this page
                  // writes. Only an unbound link with no reference is uncarried.
                  const uncarried = carriers.length === 0 && !link
                  const dumpedBy = savedBackups.find((job) =>
                    (job.databaseDumps ?? []).includes(Number(id)),
                  )
                  const verbs: Verb[] = [
                    {
                      key: "test",
                      label: "Test connection",
                      detail: "Opens a connection to the engine and reports what it says.",
                      icon: Lightning,
                      inline: true,
                      disabled: busy === `ping-${id}`,
                      run: () => void testConnection(id, name),
                    },
                  ]
                  if (canAdmin)
                    verbs.push(
                      {
                        key: "copy",
                        label: "Copy application URL",
                        detail: "The address and credentials the container receives. Audited.",
                        icon: Copy,
                        disabled: busy === `url-${id}`,
                        run: () => void copyApplicationURL(id),
                      },
                      {
                        key: "remove",
                        label: "Remove database",
                        detail: "Unlinks it here. The container and its data are left alone.",
                        icon: Trash,
                        danger: true,
                        run: () => removeDatabase(dependency, index),
                      },
                    )
                  const statusMark = <Status tone={status.tone} label={status.label} />
                  return (
                    <DatabaseRowGroup key={`${id}-${index}`}>
                      <ChoiceRow
                        href={`/databases/browse?conn=${id}`}
                        verb={name}
                        busy={busy === `ping-${id}`}
                        leading={<ProductLogo size="sm" id={link?.driver} fallback={Database} />}
                        title={name}
                        description={
                          link ? (
                            <span className="flex min-w-0 items-center gap-1.5">
                              {/* The engine's own name, as the picture spells
                                  it; narrow, the logo beside says it and the
                                  width goes to the address. */}
                              {wide && (
                                <Tag>{DATABASE_ENGINE_LABELS[link.driver] ?? link.driver}</Tag>
                              )}
                              <span className="shrink-0 font-mono">{link.database}</span>
                              <span aria-hidden>·</span>
                              <span className="min-w-0 truncate font-mono">{link.hostname}</span>
                            </span>
                          ) : (
                            "Not observed yet — the first deployment binds it"
                          )
                        }
                        trailing={wide ? statusMark : undefined}
                        actions={
                          <VerbActions dim verbs={verbs} menuLabel={`Actions for ${name}`} />
                        }
                      >
                        <div className="flex min-w-0 flex-wrap items-center gap-x-4 gap-y-1.5 text-hint text-muted-foreground sm:pl-11">
                          {!wide && statusMark}
                          {carriers.length > 0 ? (
                            <span className="flex min-w-0 flex-wrap items-center gap-1.5">
                              via
                              {carriers.map((variable) => (
                                <Link
                                  key={variable.name}
                                  href={`/deploy/${projectId}/settings/variables`}
                                  className="rounded-sm focus-ring hover:underline"
                                >
                                  <Tag mono>{variable.name}</Tag>
                                </Link>
                              ))}
                            </span>
                          ) : (
                            <span>no variable names it</span>
                          )}
                          {savedBackups.length > 0 && (
                            <Status
                              tone={dumpedBy ? "running" : "warning"}
                              label={dumpedBy ? `dumped by ${dumpedBy.name}` : "no native dump"}
                            />
                          )}
                        </div>
                      </ChoiceRow>
                      {failing && (
                        <Notice
                          tone="danger"
                          icon={Warning}
                          title={`${link?.name} needs reconnection`}
                        >
                          {link?.detail ||
                            "The original database container or Compose service is not running. The dashboard retries every five seconds."}
                        </Notice>
                      )}
                      {uncarried && (
                        <Notice
                          tone="warning"
                          icon={Warning}
                          title="No variable carries this database"
                        >
                          The release attaches a database by reading the variable that holds its
                          address. Add it again from Add database, or write the reference into a
                          variable yourself.
                        </Notice>
                      )}
                      {errorFor("database", index) && (
                        <p role="alert" className="text-hint text-destructive">
                          {errorFor("database", index)}
                        </p>
                      )}
                    </DatabaseRowGroup>
                  )
                })}
              </ChoiceList>
            </>
          )}
        </div>
      </SettingSection>

      <SettingForm
        name="Backups & volumes"
        onSubmit={(event) => void onSaveOthers(event)}
        dirty={draft.dirty}
        changes={draft.changes}
        saving={saving}
        canEdit={canAdmin}
        onDiscard={() => {
          setDependencyError(undefined)
          setChanging([])
          draft.discard()
        }}
      >
        <SettingSection
          title="Backup before a release"
          state={
            // Whether the release waits is the policy's own switch and the
            // Backup policy reading; the head keeps only the warning neither
            // of them gives.
            backupRows.length === 0 && databaseDependencies.length > 0
              ? "Persistent storage is not a backup"
              : undefined
          }
          status={settingStatus({ dirty: draft.dirty, refused: Boolean(dependencyError) })}
          actions={
            canAdmin && (
              <Button
                type="button"
                size="sm"
                variant="outline"
                onClick={() =>
                  addOther({
                    kind: "backup",
                    ownership: "linked",
                    resourceKind: "backup_job",
                    resourceId: "",
                    config: { requiredBeforeDeploy: true, maxAgeSeconds: 86400 },
                  })
                }
              >
                <Plus className="size-3.5" /> Add backup
              </Button>
            )
          }
        >
          {backupRows.length === 0 ? (
            <div className="space-y-3">
              <EmptyNote className="px-0 py-0 text-left">
                {databaseDependencies.length > 0
                  ? "No backup policy is declared, so a release takes nothing with it."
                  : "No backup dependency is declared."}
              </EmptyNote>
              {/*
                A database nothing dumps and no job covers: the coverage list
                on Backups already knows how to write that job, so this sends
                the operator to it with the database chosen rather than
                building a second form here.
              */}
              {canAdmin && databaseDependencies.length > 0 && (
                <ChoiceList aria-label="Databases with no backup">
                  {databaseDependencies.map((dependency) => {
                    const link = linkFor(dependency.resourceId)
                    const name = link?.name ?? `database ${dependency.resourceId}`
                    return (
                      <ChoiceRow
                        key={`protect-${dependency.resourceId}`}
                        href={`/backups?database=${dependency.resourceId}`}
                        verb={`Back up ${name}`}
                        leading={<ProductLogo size="sm" id={link?.driver} fallback={Database} />}
                        title={`Back up ${name}`}
                        description="No job dumps it — opens Backups with the native dump chosen"
                      />
                    )
                  })}
                </ChoiceList>
              )}
            </div>
          ) : (
            <div className="divide-y divide-hairline">
              {backupRows.map(({ dependency, index }) => (
                <BackupPolicy
                  key={index}
                  index={index}
                  dependency={dependency}
                  job={jobFor(dependency.resourceId)}
                  jobs={jobs.data}
                  observed={observedBackupFor(dependency.resourceId)}
                  databases={databaseDependencies}
                  driverOf={driverOf}
                  linkFor={linkFor}
                  canAdmin={canAdmin}
                  canRun={canRun}
                  busy={busy}
                  changing={changing.includes(index) || !dependency.resourceId}
                  error={errorFor("other", index)}
                  onChange={(next) => updateDependency(index, next)}
                  onChangeJob={() => setChanging((current) => [...current, index])}
                  onRemove={() => removeOther(index)}
                  onRun={(job) => void runBackup(job)}
                  onAddDump={(job, connectionId) => void addDump(job, connectionId)}
                />
              ))}
            </div>
          )}
        </SettingSection>

        <SettingSection
          title="Volumes this release needs"
          state={
            volumeRows.length > 0 &&
            `${plural(volumeRows.length, "volume")} checked before the release starts`
          }
          actions={
            canAdmin && (
              <Button
                type="button"
                size="sm"
                variant="outline"
                onClick={() =>
                  addOther({
                    kind: "storage",
                    ownership: "linked",
                    resourceKind: "docker_volume",
                    resourceId: "",
                    config: {},
                  })
                }
              >
                <Plus className="size-3.5" /> Add volume
              </Button>
            )
          }
        >
          {volumeRows.length === 0 ? (
            <EmptyNote className="px-0 py-0 text-left">No volume dependency is declared.</EmptyNote>
          ) : (
            <ul aria-label="Volumes this release needs" className="divide-y divide-hairline">
              {volumeRows.map(({ dependency, index }) => (
                <VolumeRow
                  key={index}
                  index={index}
                  dependency={dependency}
                  volumes={volumes}
                  observed={observedVolumeFor(dependency.resourceId)}
                  product={volumeMark}
                  canAdmin={canAdmin}
                  error={errorFor("other", index)}
                  onChange={(next) => updateDependency(index, next)}
                  onRemove={() => removeOther(index)}
                />
              ))}
            </ul>
          )}
        </SettingSection>
      </SettingForm>

      <SettingSection
        title="Latest backup gate evidence"
        state={
          latestRun.data && (
            <Link
              href={`/deploy/${projectId}/runs/${latestRun.data.run.id}`}
              className="inline-flex items-center gap-1 rounded-sm focus-ring hover:text-foreground hover:underline"
            >
              From deployment #{latestRun.data.run.runNumber}
              <ArrowUpRight aria-hidden className="size-3" />
            </Link>
          )
        }
      >
        {!latestRunId ? (
          <EmptyNote className="px-0 text-left">
            No deployment run has produced backup evidence yet.
          </EmptyNote>
        ) : latestRun.loading ? (
          <EmptyNote className="px-0 text-left">Reading the latest run…</EmptyNote>
        ) : backupGateEvidence.length === 0 ? (
          <EmptyNote className="px-0 text-left">
            The latest run did not execute a backup policy.
          </EmptyNote>
        ) : (
          <ChoiceList aria-label="Latest backup gate evidence">
            {backupGateEvidence.map((item, index) => {
              const job = jobFor(String(item.jobId))
              const jobName = job?.name ?? `Job #${item.jobId}`
              const engines = [
                ...new Set(
                  (item.databaseDumps ?? [])
                    .map(driverOf)
                    .filter((driver): driver is string => Boolean(driver)),
                ),
              ]
              const readings = (
                <>
                  <Status
                    tone={item.fresh ? "running" : "warning"}
                    label={item.fresh ? "Fresh" : "Older than the policy allows"}
                  />
                  <Status
                    tone={item.restoreTested ? "running" : "stopped"}
                    label={item.restoreTested ? "Restore tested" : "No restore evidence"}
                  />
                </>
              )
              const dumps = item.databaseDumps?.length ?? 0
              return (
                <ChoiceRow
                  key={`${item.jobId}-${item.runId ?? 0}`}
                  index={index}
                  href={`/backups/${item.jobId}`}
                  verb={`${jobName} gate evidence`}
                  leading={
                    engines.length > 1 ? (
                      <ProductLogos ids={engines} ring="ring-choice-surface" />
                    ) : (
                      <ProductLogo size="sm" id={engines[0]} fallback={Archive} />
                    )
                  }
                  title={jobName}
                  description={
                    <>
                      {item.runId ? <span className="numeric">#{item.runId}</span> : "No run"}
                      {item.endedAt && <> · completed {relativeTime(item.endedAt)}</>}
                      {" · "}
                      {humanize(item.status)}
                      {item.detail ? ` · ${item.detail}` : ""}
                    </>
                  }
                  trailing={wide ? readings : undefined}
                >
                  {(!wide || dumps > 0 || item.restoreVerificationId) && (
                    <div className="flex min-w-0 flex-wrap items-center gap-x-4 gap-y-1 text-hint text-muted-foreground sm:pl-11">
                      {!wide && readings}
                      {dumps > 0 && (
                        <span className="flex items-center gap-1.5">
                          <ProductGlyphs ids={engines} />
                          {dumps === 1
                            ? "1 linked database covered by a native dump"
                            : `${dumps} linked databases covered by native dumps`}
                        </span>
                      )}
                      {item.restoreVerificationId && (
                        <span className="font-mono break-all">
                          Recovery check #{item.restoreVerificationId} · schema{" "}
                          {item.restoreSchemaVersion}
                          {item.restoreApplicationImage ? ` · ${item.restoreApplicationImage}` : ""}
                        </span>
                      )}
                    </div>
                  )}
                </ChoiceRow>
              )
            })}
          </ChoiceList>
        )}
      </SettingSection>
      {dialog}
    </>
  )
}

/**
 * A linked database's card and what has to be said under it. A `ChoiceRow`
 * renders its own list item, so the notices that belong to one database go
 * in an item of their own directly after it.
 */
function DatabaseRowGroup({ children }: { children: React.ReactNode }) {
  const [row, ...rest] = Array.isArray(children) ? children : [children]
  const after = rest.filter(Boolean)
  return (
    <>
      {row}
      {after.length > 0 && <li className="min-w-0 space-y-2">{after}</li>}
    </>
  )
}

/**
 * The four figures at the top, from the saved configuration and what the
 * page has read of the links and the jobs.
 *
 * Coverage is the one worth the space: the gate refuses a release whose job
 * takes no dump of a linked database, and until this reading existed that
 * refusal arrived during the deployment it stopped. A figure that needs a
 * read says "—" until that read lands — and goes on saying it for a reader
 * the read is refused to — rather than stating the answer an empty list would
 * give; each changes its key as its read lands, so it rises into place.
 */
function DatabaseReadings({
  configuration,
  links,
  jobs,
}: {
  configuration: DeploymentEnvironmentConfiguration
  links?: DeploymentDatabaseLink[]
  jobs?: BackupJob[]
}) {
  const databases = configuration.dependencies.filter((d) => d.kind === "database")
  const backupDeps = configuration.dependencies.filter((d) => d.kind === "backup")
  const linked = databases.length
  const observed = databases
    .map((dependency) => links?.find((link) => String(link.connectionId) === dependency.resourceId))
    .filter((link): link is DeploymentDatabaseLink => Boolean(link))
  const engines = [...new Set(observed.map((link) => link.driver))]
  const failing = observed.filter((link) => link.status === "unavailable")
  const stale = observed.filter((link) => link.status === "stale")
  const connected = observed.filter((link) => link.status === "connected").length
  const network = observed[0]?.network

  const declared = backupDeps
    .map((dependency) => jobs?.find((job) => String(job.id) === dependency.resourceId))
    .filter((job): job is BackupJob => Boolean(job))
  const required = backupDeps.some(
    (dependency) => (dependency.config as BackupDependencyConfig)?.requiredBeforeDeploy,
  )
  const dumpedLinks = observed.filter((link) =>
    declared.some((job) => (job.databaseDumps ?? []).includes(link.connectionId)),
  )
  const dumped = databases.filter((dependency) =>
    declared.some((job) => (job.databaseDumps ?? []).includes(Number(dependency.resourceId))),
  ).length
  const newestSuccess = declared
    .map((job) => job.lastSuccessAt)
    .filter((stamp): stamp is string => Boolean(stamp))
    .sort()
    .at(-1)

  const connection: { value: string; tone: Tone; hint?: string } =
    linked === 0
      ? { value: "None yet", tone: "default" }
      : !links
        ? { value: "—", tone: "default" }
        : failing.length > 0
          ? {
              value: `${failing.length} need${failing.length === 1 ? "s" : ""} attention`,
              tone: "danger",
              hint: `${failing[0].name} is not reachable`,
            }
          : stale.length > 0
            ? {
                value: `${stale.length} stale`,
                tone: "warning",
                hint: `${stale[0].name} not observed recently`,
              }
            : connected === linked
              ? { value: "All connected", tone: "success", hint: network && `On ${network}` }
              : { value: "Waiting", tone: "default", hint: "Until the first deployment" }
  const dumps: { value: string; tone: Tone; hint?: string } =
    linked === 0
      ? { value: "None yet", tone: "default" }
      : !jobs
        ? { value: "—", tone: "default" }
        : dumped === linked
          ? { value: `${dumped} of ${linked}`, tone: "success", hint: "Every linked database" }
          : {
              value: `${dumped} of ${linked}`,
              tone: "warning",
              hint: "A database falls back to its files",
            }
  const arrived = (value: string, loaded: unknown) => (
    <span key={loaded ? `value-${value}` : "loading"} className="animate-rise">
      {value}
    </span>
  )

  return (
    <StatGrid columns={4} dense>
      <StatTile
        label="Linked databases"
        value={linked}
        trailing={<ProductGlyphs ids={engines} />}
        hint={linked === 0 ? "Start one or connect a saved one" : network && `On ${network}`}
      />
      <StatTile
        label="Connection"
        value={arrived(connection.value, links || linked === 0)}
        tone={connection.tone}
        hint={connection.hint}
      />
      <StatTile
        label="Backup policy"
        value={backupDeps.length === 0 ? "None" : required ? "Required" : "Declared"}
        tone={backupDeps.length === 0 && linked > 0 ? "warning" : "default"}
        hint={
          backupDeps.length === 0
            ? linked > 0
              ? "A linked database with no backup policy"
              : undefined
            : !jobs
              ? undefined
              : newestSuccess
                ? `Last success ${relativeTime(newestSuccess)}`
                : "Never succeeded"
        }
      />
      <StatTile
        label="Native dumps"
        value={arrived(dumps.value, jobs || linked === 0)}
        tone={dumps.tone}
        trailing={<ProductGlyphs ids={[...new Set(dumpedLinks.map((link) => link.driver))]} />}
        hint={dumps.hint}
      />
    </StatGrid>
  )
}

/**
 * How the application reaches its databases: each database on the left as
 * its engine, the managed network in the middle, the application on the
 * right — framed, because a picture needs an edge to read as one thing (§2).
 * The line is the link's state: a pulse while it is connected, still when it
 * has not been seen lately, red and still when it needs reconnecting, dashed
 * before the first deployment binds it.
 */
function DatabasePicture({
  links,
  deployment,
}: {
  links: DeploymentDatabaseLink[]
  deployment: ReturnType<typeof useProject>["detail"]["deployment"]
}) {
  const container = useRef<HTMLDivElement>(null)
  const network = useRef<HTMLDivElement>(null)
  const application = useRef<HTMLDivElement>(null)
  // One ref per database mark, made again only when the set of databases
  // changes, so each line keeps measuring the mark it was drawn to.
  const ids = links.map((link) => link.connectionId).join(",")
  const marks = useMemo(() => ids.split(",").map(() => createRef<HTMLDivElement>()), [ids])
  const anyConnected = links.some((link) => link.status === "connected")
  const networks = [...new Set(links.map((link) => link.network))]
  return (
    <SettingPicture
      label="How the application reaches its databases"
      containerRef={container}
      lines={
        <>
          {links.map((link, index) => (
            <AnimatedBeam
              key={link.connectionId}
              containerRef={container}
              fromRef={marks[index]}
              toRef={network}
              reverse
              still={link.status !== "connected"}
              dashed={link.status === "pending"}
              tone={link.status === "unavailable" ? "danger" : "default"}
              duration={2.8}
              delay={index * 0.4}
            />
          ))}
          <AnimatedBeam
            containerRef={container}
            fromRef={network}
            toRef={application}
            reverse
            still={!anyConnected}
            duration={2.8}
            delay={0.6}
          />
        </>
      }
      start={links.map((link, index) => (
        <WireNode
          key={link.connectionId}
          nodeRef={marks[index]}
          align="end"
          mark={
            <WireMark tone="logo" size="md">
              <ProductGlyph id={link.driver} />
            </WireMark>
          }
          eyebrow={DATABASE_ENGINE_LABELS[link.driver] ?? link.driver}
          title={link.name}
          hint={`database ${link.database}`}
        />
      ))}
      middle={
        <WireNode
          nodeRef={network}
          align="center"
          mark={
            <WireMark size="md">
              <Connection />
            </WireMark>
          }
          eyebrow="Managed network"
          title={<span className="font-mono">{networks.join(", ")}</span>}
        />
      }
      end={
        <WireNode
          nodeRef={application}
          align="start"
          mark={
            // The WireMarks' own box, so the four words start on one edge.
            <span className="flex size-11 items-center justify-center">
              <ProjectMark deployment={deployment} size="md" />
            </span>
          }
          eyebrow="Application"
          title={deployment.name}
        />
      }
    />
  )
}

/**
 * One backup job a release gates on. A chosen job is the Backups page's own
 * card — its products, its last run in colour, where it writes, its last
 * fourteen runs — with what the live release observed of it added to its
 * second line. A new row, a job being changed, or one that no longer exists
 * is the picker instead, never a bare id.
 */
function BackupPolicy({
  index,
  dependency,
  job,
  jobs,
  observed,
  databases,
  driverOf,
  linkFor,
  canAdmin,
  canRun,
  busy,
  changing,
  error,
  onChange,
  onChangeJob,
  onRemove,
  onRun,
  onAddDump,
}: {
  index: number
  dependency: Dependency
  job?: BackupJob
  jobs?: BackupJob[]
  observed?: DeploymentBackupJob
  databases: Dependency[]
  driverOf: (connectionId: number) => string | undefined
  linkFor: (resourceId?: string) => DeploymentDatabaseLink | undefined
  canAdmin: boolean
  canRun: boolean
  busy?: string
  changing: boolean
  error?: string
  onChange: (next: Dependency) => void
  onChangeJob: () => void
  onRemove: () => void
  onRun: (job: BackupJob) => void
  onAddDump: (job: BackupJob, connectionId: number) => void
}) {
  const config = (dependency.config ?? {}) as BackupDependencyConfig
  const missing = Boolean(dependency.resourceId) && Boolean(jobs) && !job
  const uncovered = job
    ? databases.filter(
        (database) =>
          database.resourceId && !(job.databaseDumps ?? []).includes(Number(database.resourceId)),
      )
    : []
  const products = job
    ? [
        ...new Set(
          (job.databaseDumps ?? [])
            .map(driverOf)
            .filter((driver): driver is string => Boolean(driver)),
        ),
      ]
    : []
  const observation = observed && (
    <Status
      tone={
        observed.status === "present"
          ? "running"
          : observed.status === "missing"
            ? "danger"
            : "unknown"
      }
      label={
        observed.status === "present"
          ? `Observed${observed.lastStatus ? ` · last run ${observed.lastStatus}` : ""}`
          : observed.status === "missing"
            ? "Missing from the live release"
            : "Not observed"
      }
    />
  )
  const verbs: Verb[] = []
  if (job && canRun)
    verbs.push({
      key: "run",
      label: "Run now",
      detail: "Takes a backup with this job now; progress appears in Backups.",
      icon: Play,
      inline: true,
      progressive: "Starting…",
      disabled: busy === `run-${job.id}`,
      run: () => onRun(job),
    })
  if (canAdmin)
    verbs.push(
      {
        key: "change",
        label: "Change job",
        detail: "Gate this release on a different backup job.",
        icon: Pencil,
        run: onChangeJob,
      },
      {
        key: "remove",
        label: "Remove from the policy",
        detail: "The release stops waiting for this job once saved.",
        icon: Trash,
        danger: true,
        run: onRemove,
      },
    )

  return (
    <div className="min-w-0 space-y-4 py-5 first:pt-0 last:pb-0">
      {job && !changing ? (
        <ChoiceList>
          <JobCard
            job={job}
            products={products}
            verbs={verbs}
            verb={`Open the backup job ${job.name}`}
            note={observation}
            working={job.lastRun?.status === "running" || busy === `run-${job.id}`}
          />
        </ChoiceList>
      ) : (
        <div className="grid min-w-0 grid-cols-[minmax(0,1fr)_auto] items-end gap-3">
          <Field label="Backup job" htmlFor={`backup-job-${index}`}>
            <Select
              value={dependency.resourceId ?? ""}
              onValueChange={(resourceId) => onChange({ ...dependency, resourceId })}
              disabled={!canAdmin || !jobs}
            >
              <SelectTrigger id={`backup-job-${index}`} className="w-full">
                <SelectValue placeholder={jobs ? "Choose a backup job" : "Loading…"} />
              </SelectTrigger>
              <SelectContent>
                {missing && (
                  <SelectItem value={dependency.resourceId!}>
                    Job #{dependency.resourceId} — not found
                  </SelectItem>
                )}
                {jobs?.map((item) => (
                  <SelectItem
                    key={item.id}
                    value={String(item.id)}
                    hint={<span aria-hidden>{scheduleLabel(item.schedule)}</span>}
                  >
                    {destinationProduct(item) ? (
                      <ProductGlyph id={destinationProduct(item)!} />
                    ) : (
                      <Archive />
                    )}
                    {item.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </Field>
          {canAdmin && (
            <IconAction
              label="Remove backup dependency"
              className="size-9 text-muted-foreground hover:text-destructive"
              onClick={onRemove}
            >
              <Trash />
            </IconAction>
          )}
          {missing && (
            <Status
              tone="danger"
              label="The job this release gates on no longer exists"
              className="col-span-full"
            />
          )}
        </div>
      )}

      {job &&
        uncovered.map((database) => {
          const link = linkFor(database.resourceId)
          const name = link?.name ?? `database ${database.resourceId}`
          return (
            <Notice
              key={`uncovered-${database.resourceId}`}
              tone="warning"
              icon={Warning}
              title={`${job.name} takes no native dump of ${name}`}
            >
              <p>
                The release falls back to this database&rsquo;s data directory and refuses if the
                job does not cover it. A dump is a transaction boundary the engine chose; a copy of
                its files while it runs is not.
              </p>
              {canAdmin && (
                <Button
                  type="button"
                  size="sm"
                  variant="outline"
                  className="mt-2"
                  pending={busy === `dump-${job.id}-${database.resourceId}`}
                  onClick={() => onAddDump(job, Number(database.resourceId))}
                >
                  <Database className="size-3.5" /> Add the dump to {job.name}
                </Button>
              )}
            </Notice>
          )
        })}

      <OptionList>
        <OptionRow
          title="Backup before deploy — the release waits for this job to finish"
          checked={Boolean(config.requiredBeforeDeploy)}
          disabled={!canAdmin}
          onCheckedChange={(requiredBeforeDeploy) =>
            onChange({ ...dependency, config: { ...config, requiredBeforeDeploy } })
          }
        />
        <OptionRow
          title="Require restore evidence — refuse a release until a restore is verified"
          checked={Boolean(config.requireRestoreTest)}
          disabled={!canAdmin}
          onCheckedChange={(requireRestoreTest) =>
            onChange({ ...dependency, config: { ...config, requireRestoreTest } })
          }
        />
      </OptionList>
      <Field
        label="Maximum age"
        htmlFor={`backup-age-${index}`}
        hint={
          config.maxAgeSeconds
            ? `Refuses the release when the newest backup is older than ${plural(Math.round(config.maxAgeSeconds / 3600), "hour")}.`
            : "Zero accepts a backup of any age."
        }
      >
        <InputGroup className="w-full sm:w-44">
          <InputGroupInput
            id={`backup-age-${index}`}
            type="number"
            min={0}
            readOnly={!canAdmin}
            value={Math.round((config.maxAgeSeconds ?? 0) / 3600)}
            onChange={(event) =>
              onChange({
                ...dependency,
                config: {
                  ...config,
                  maxAgeSeconds: Math.max(0, Number(event.target.value) || 0) * 3600,
                },
              })
            }
            className="numeric"
          />
          <InputGroupAddon align="inline-end">
            <InputGroupText>hours</InputGroupText>
          </InputGroupAddon>
        </InputGroup>
      </Field>
      {error && (
        <p role="alert" className="text-hint text-destructive">
          {error}
        </p>
      )}
    </div>
  )
}

/**
 * One volume the release needs before it starts: the volume, drawn on the
 * volumes tab's tile, chosen from Docker's own list with its size and whether
 * anything uses it; and on the line under it, who owns it and what the live
 * release found — the same two lines a mount on Storage is.
 */
function VolumeRow({
  index,
  dependency,
  volumes,
  observed,
  product,
  canAdmin,
  error,
  onChange,
  onRemove,
}: {
  index: number
  dependency: Dependency
  volumes: Poll<DockerVolume[]>
  observed?: DeploymentStorageMount
  /** The product of the container that keeps its data in the volume. */
  product?: string
  canAdmin: boolean
  error?: string
  onChange: (next: Dependency) => void
  onRemove: () => void
}) {
  const items = volumes.data ?? []
  const value = dependency.resourceId ?? ""
  const volume = items.find((item) => item.name === value)
  const status = observed ? MOUNT_STATUS[observed.status] : undefined
  return (
    <li className="grid min-w-0 grid-cols-[2rem_minmax(0,1fr)_auto] items-end gap-x-3 gap-y-3 py-4 first:pt-0">
      <MountMark source={value} product={product} className="mb-0.5" />
      <Field label="Volume" htmlFor={`volume-${index}`}>
        <Select
          value={value}
          onValueChange={(resourceId) => onChange({ ...dependency, resourceId })}
          disabled={!canAdmin || volumes.loading}
        >
          <SelectTrigger id={`volume-${index}`} className="w-full">
            <SelectValue placeholder={volumes.loading ? "Loading…" : "Choose a volume"} />
          </SelectTrigger>
          <SelectContent>
            {value && !volume && <SelectItem value={value}>{value}</SelectItem>}
            {items.map((item) => (
              <SelectItem
                key={item.name}
                value={item.name}
                hint={
                  <span aria-hidden>
                    {item.size > 0 ? `${bytes(item.size)} · ` : ""}
                    {item.inUse ? "in use" : "unused"}
                  </span>
                }
              >
                {item.name}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </Field>
      <span className="flex gap-0.5">
        {value && (
          <IconAction label="Open the volume" asChild>
            <Link href={`/docker/volumes?volume=${encodeURIComponent(value)}`}>
              <ArrowUpRight />
            </Link>
          </IconAction>
        )}
        {canAdmin && (
          <IconAction
            label="Remove volume dependency"
            className="text-muted-foreground hover:text-destructive"
            onClick={onRemove}
          >
            <Trash />
          </IconAction>
        )}
      </span>
      <div className="col-[2/-1] flex min-w-0 flex-wrap items-center gap-x-4 gap-y-2">
        <OwnershipSelect
          value={dependency.ownership}
          onChange={(ownership) => onChange({ ...dependency, ownership })}
          label={value ? `Ownership of ${value}` : "Ownership"}
          disabled={!canAdmin}
        />
        {status && <Status tone={status.tone} label={status.label} />}
        {observed?.target && <Tag mono>{observed.target}</Tag>}
        {volume && volume.size > 0 && (
          <span className="numeric text-hint text-muted-foreground">{bytes(volume.size)}</span>
        )}
        {observed?.detail && (
          <span className="text-hint text-muted-foreground">{observed.detail}</span>
        )}
      </div>
      {error && (
        <p role="alert" className="col-span-full pl-11 text-hint text-destructive">
          {error}
        </p>
      )}
    </li>
  )
}
