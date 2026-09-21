"use client"

import { useState } from "react"
import Link from "next/link"
import { Copy, Database, Lightning, Play, Plus, ShieldCheck, Trash } from "@/components/icons"
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
  DeploymentDatabaseLink,
  DeploymentEnvironmentConfiguration,
  DeploymentRunSnapshot,
  DeploymentVariable,
  DockerVolume,
} from "@/lib/types"
import { Field, FormFact, FormFacts, OptionRow } from "@/components/form"
import { Group, Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyNote, Notice } from "@/components/state"
import { Row, RowList } from "@/components/row-list"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { Status, type DotTone } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { IconAction } from "@/components/icon-action"
import { VerbActions, type Verb } from "@/components/verbs"
import { useConfirm } from "@/components/confirm-dialog"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { SettingCard } from "@/components/deploy/settings/setting-card"
import { PendingChanges } from "@/components/deploy/settings/pending-changes"
import {
  ConfigurationState,
  useConfiguration,
} from "@/components/deploy/settings/use-configuration"
import { humanize } from "@/components/deploy/vocabulary"
import { useProject } from "@/components/deploy/project-context"
import { ProjectDatabase } from "@/components/deploy/project-database"

type Dependency = DeploymentEnvironmentConfiguration["dependencies"][number]

type BackupDependencyConfig = {
  requiredBeforeDeploy?: boolean
  requireRestoreTest?: boolean
  maxAgeSeconds?: number
}

const LINK_STATUS: Record<DeploymentDatabaseLink["status"], { label: string; tone: DotTone }> = {
  connected: { label: "Connected", tone: "running" },
  stale: { label: "Not observed recently", tone: "warning" },
  pending: { label: "Waiting for the first deployment", tone: "unknown" },
  unavailable: { label: "Needs reconnection", tone: "danger" },
}

export function DatabasesSettings({
  projectId,
  environmentId,
}: {
  projectId: number
  environmentId: number
}) {
  const state = useConfiguration(projectId, environmentId)
  return (
    <ConfigurationState state={state}>
      {(configuration) => (
        <DatabasesForm
          key={configuration.revision}
          projectId={projectId}
          environmentId={environmentId}
          configuration={configuration}
          save={state.save}
          refresh={state.refresh}
        />
      )}
    </ConfigurationState>
  )
}

function DatabasesForm({
  projectId,
  environmentId,
  configuration,
  save,
  refresh,
}: {
  projectId: number
  environmentId: number
  configuration: DeploymentEnvironmentConfiguration
  save: ReturnType<typeof useConfiguration>["save"]
  refresh: () => void
}) {
  const { can } = useAuth()
  const canAdmin = can("system.admin")
  const canRun = can("service.control")
  const project = useProject()
  const { confirm, dialog } = useConfirm()
  const databaseDependencies = configuration.dependencies.filter((d) => d.kind === "database")
  // Backup and volume dependencies batch through one local copy and one Save,
  // the way the other settings sections do; a database link is its own
  // mutation (it also writes a variable), so it is never staged here.
  const [otherDependencies, setOtherDependencies] = useState<Dependency[]>(() =>
    configuration.dependencies.filter((d) => d.kind !== "database"),
  )
  const [saving, setSaving] = useState(false)
  const [busy, setBusy] = useState<string>()
  const [dependencyError, setDependencyError] = useState<{ target: Dependency; message: string }>()

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
  const linkFor = (resourceId?: string) =>
    links.data?.find((link) => String(link.connectionId) === resourceId)

  // The jobs themselves, not only their names: whether the one this release
  // gates on dumps the databases beside it is the question this page exists to
  // answer before a deployment answers it the hard way.
  const jobs = usePoll((signal) => get<BackupJob[]>("/backups/", undefined, signal), 15000)
  const jobFor = (resourceId?: string) => jobs.data?.find((job) => String(job.id) === resourceId)

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
  const variablesFor = (resourceId?: string): DeploymentVariable[] =>
    configuration.variables.filter(
      (variable) =>
        variable.reference?.kind === "database" && variable.reference.target === resourceId,
    )

  const connect = async (connection: DbConnection, url: string, variable: string) => {
    try {
      // The saved copy, not the staged `otherDependencies` rows: a
      // half-filled "Add backup"/"Add volume" row the operator has not
      // pressed Save on has no business riding along on a database link,
      // which the "invalid planned dependency" refusal made painfully literal.
      const savedOtherDependencies = configuration.dependencies.filter(
        (dependency) => dependency.kind !== "database",
      )
      const nextDependencies: Dependency[] = [
        ...savedOtherDependencies,
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
      await put(`/deploy/${projectId}/environments/${environmentId}/variables/${variable}`, {
        revision: updated.revision,
        value: url,
        sensitivity: "secret",
        scopes: ["runtime"],
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
    const savedOtherDependencies = configuration.dependencies.filter(
      (item) => item.kind !== "database",
    )
    const nextDatabaseDeps = databaseDependencies.filter((_, i) => i !== index)
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
        dependencies: [...savedOtherDependencies, ...nextDatabaseDeps],
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

  const onSaveOthers = async () => {
    setSaving(true)
    setDependencyError(undefined)
    const merged = [...databaseDependencies, ...otherDependencies]
    try {
      await save({ dependencies: merged })
      notify.success("Dependencies saved")
    } catch (error) {
      const index =
        error instanceof ApiError ? refusedIndex(error.field, "dependencies") : undefined
      const target = index !== undefined ? merged[index] : undefined
      if (error instanceof ApiError && target) {
        setDependencyError({ target, message: error.message })
      } else {
        notify.error("Could not save dependencies", error)
      }
    } finally {
      setSaving(false)
    }
  }

  const backupDeps = otherDependencies.filter((d) => d.kind === "backup")
  const volumeDeps = otherDependencies.filter((d) => d.kind === "storage")
  const updateDependency = (target: Dependency, next: Dependency) =>
    setOtherDependencies(otherDependencies.map((item) => (item === target ? next : item)))
  const removeOther = (target: Dependency) =>
    setOtherDependencies(otherDependencies.filter((item) => item !== target))

  const observedBackups = project.operations?.backups
  const observedStorage = project.operations?.storage
  const observedBackupFor = (resourceId?: string) =>
    observedBackups?.status === "available"
      ? observedBackups.jobs.find((job) => job.resourceId === resourceId)
      : undefined
  const observedVolumeFor = (resourceId?: string) =>
    observedStorage?.status === "available"
      ? observedStorage.mounts.find((mount) => mount.source === resourceId)
      : undefined

  const readings = summarise(databaseDependencies, links.data, backupDeps, jobs.data)
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

  return (
    <div className="space-y-6">
      <PendingChanges pending={configuration.pending} />

      <StatGrid columns={4} className="animate-rise">
        <StatTile
          label="Linked databases"
          value={readings.linked}
          hint={readings.engines || undefined}
        />
        <StatTile
          label="Connection"
          value={readings.connection.value}
          tone={readings.connection.tone}
        />
        <StatTile
          label="Backup policy"
          value={readings.policy.value}
          tone={readings.policy.tone}
          hint={readings.policy.hint}
        />
        <StatTile
          label="Native dumps"
          value={readings.dumps.value}
          tone={readings.dumps.tone}
          hint={readings.dumps.hint}
        />
      </StatGrid>

      <SettingCard
        title="Databases & backups"
        note="Applies on the next deployment."
        action={
          canAdmin && (
            <Button size="sm" onClick={onSaveOthers} pending={saving}>
              Save
            </Button>
          )
        }
        bodyClassName="space-y-5"
      >
        <div className="space-y-3">
          <p className="eyebrow">Linked databases</p>
          {databaseDependencies.length === 0 ? (
            <EmptyNote>No database is linked to this environment.</EmptyNote>
          ) : (
            <RowList aria-label="Linked databases">
              {databaseDependencies.map((dependency, index) => {
                const link = linkFor(dependency.resourceId)
                const status = LINK_STATUS[link?.status ?? "pending"]
                // The dependency is what the verbs act on, not the
                // observation: a link whose binding has not been reported
                // still has to be removable, which is exactly the state an
                // operator reaches this page in when it stopped working.
                const id = dependency.resourceId ?? ""
                const name = link?.name ?? `Database ${id}`
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
                if (canAdmin) {
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
                }
                return (
                  <Row
                    key={`${id}-${index}`}
                    title={
                      <span className="flex min-w-0 items-center gap-2">
                        <Link
                          href={`/databases?conn=${id}`}
                          className="min-w-0 truncate rounded-sm focus-ring hover:underline"
                        >
                          {name}
                        </Link>
                        {link?.driver && <Tag>{link.driver}</Tag>}
                        {link?.database && <Tag mono>{link.database}</Tag>}
                      </span>
                    }
                    subtitle={link?.hostname}
                    mono
                    trailing={
                      <>
                        <Status tone={status.tone} label={status.label} />
                        <VerbActions verbs={verbs} menuLabel={`${name} actions`} dim />
                      </>
                    }
                  />
                )
              })}
            </RowList>
          )}
          {/*
            The rows above are the state; these are the facts about each link
            that do not fit on one line — which variables carry it, and why the
            last reconciliation could not repair it.
          */}
          {databaseDependencies.map((dependency, index) => {
            const link = linkFor(dependency.resourceId)
            const carriers = variablesFor(dependency.resourceId)
            const failing = link?.status === "unavailable"
            // A binding exists because a variable named this database, so an
            // observed link is proof one carries it even when the value is a
            // literal URL rather than the typed reference this page writes.
            // Only an unbound link with no reference is really uncarried.
            const uncarried = carriers.length === 0 && !link
            if (!failing && !uncarried && !dependencyError) return null
            return (
              <div key={`facts-${dependency.resourceId}-${index}`} className="space-y-2">
                {failing && (
                  <Notice tone="danger" title={`${link?.name} needs reconnection`}>
                    {link?.detail ||
                      "The original database container or Compose service is not running. The dashboard retries every five seconds."}
                  </Notice>
                )}
                {uncarried && (
                  <Notice tone="warning" title="No variable carries this database">
                    The release attaches a database by reading the variable that holds its address.
                    Add it again from Add database, or write the reference into a variable yourself.
                  </Notice>
                )}
                {dependencyError?.target === dependency && (
                  <p role="alert" className="text-hint text-destructive">
                    {dependencyError.message}
                  </p>
                )}
              </div>
            )
          })}
          {databaseDependencies.length > 0 && (
            <p className="text-hint text-muted-foreground">
              {allCarriers.length > 0
                ? `Carried by ${allCarriers.join(", ")}.`
                : boundWithoutReference
                  ? "Bound on the managed network; no variable names one by reference."
                  : "No variable carries a linked database."}{" "}
              Last checked{" "}
              {links.data?.[0]?.checkedAt ? relativeTime(links.data[0].checkedAt) : "never"}.
            </p>
          )}
          {canAdmin && <ProjectDatabase target="container" onConnect={connect} />}
        </div>

        <div className="space-y-3 border-t border-hairline pt-4">
          <div className="flex items-center justify-between gap-3">
            <p className="eyebrow">Backups</p>
            {canAdmin && (
              <Button
                size="sm"
                variant="outline"
                onClick={() =>
                  setOtherDependencies([
                    ...otherDependencies,
                    {
                      kind: "backup",
                      ownership: "linked",
                      resourceKind: "backup_job",
                      resourceId: "",
                      config: { requiredBeforeDeploy: true, maxAgeSeconds: 86400 },
                    },
                  ])
                }
              >
                <Plus className="size-3.5" /> Add backup
              </Button>
            )}
          </div>
          {backupDeps.length === 0 ? (
            <EmptyNote>
              {databaseDependencies.length > 0
                ? "No backup policy is declared. Persistent storage is not a backup."
                : "No backup dependency is declared."}
            </EmptyNote>
          ) : (
            <div className="space-y-3">
              {backupDeps.map((dependency, index) => {
                const config = (dependency.config ?? {}) as BackupDependencyConfig
                const job = jobFor(dependency.resourceId)
                const observed = observedBackupFor(dependency.resourceId)
                const uncovered = job
                  ? databaseDependencies.filter(
                      (database) =>
                        database.resourceId &&
                        !(job.databaseDumps ?? []).includes(Number(database.resourceId)),
                    )
                  : []
                return (
                  <Group key={index} className="space-y-3">
                    <div className="grid min-w-0 gap-3 sm:grid-cols-[minmax(0,1fr)_auto]">
                      <ResourceSelect<BackupJob>
                        id={`backup-job-${index}`}
                        label="Backup job"
                        path="/backups/"
                        given={jobs.data}
                        value={dependency.resourceId ?? ""}
                        onChange={(resourceId) =>
                          updateDependency(dependency, { ...dependency, resourceId })
                        }
                        disabled={!canAdmin}
                        toId={(item) => String(item.id)}
                        toLabel={(item) => item.name}
                      />
                      {canAdmin && (
                        <IconAction
                          label="Remove backup dependency"
                          className="mt-6 text-destructive"
                          onClick={() => removeOther(dependency)}
                        >
                          <Trash />
                        </IconAction>
                      )}
                    </div>

                    {job && (
                      <FormFacts>
                        <FormFact label="Schedule">
                          {job.enabled ? job.schedule || "manual only" : "paused"}
                        </FormFact>
                        <FormFact label="Last success">
                          {job.lastSuccessAt ? relativeTime(job.lastSuccessAt) : "never"}
                        </FormFact>
                        <FormFact label="Next run">
                          {job.enabled && job.nextRun ? relativeTime(job.nextRun) : "—"}
                        </FormFact>
                        <FormFact label="Stored">
                          {job.stored.runs > 0
                            ? `${plural(job.stored.runs, "artifact")} · ${bytes(job.stored.bytes)}`
                            : "nothing yet"}
                        </FormFact>
                      </FormFacts>
                    )}
                    {job && (
                      <div className="flex min-w-0 flex-wrap items-center gap-2">
                        {observed && (
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
                        )}
                        {job.overdue && <Tag tone="warning">overdue</Tag>}
                        {!job.enabled && <Tag tone="warning">paused</Tag>}
                        <Tag mono>{job.targetKind}</Tag>
                        {canRun && (
                          <Button
                            size="sm"
                            variant="outline"
                            pending={busy === `run-${job.id}`}
                            onClick={() => void runBackup(job)}
                          >
                            <Play className="size-3.5" /> Run now
                          </Button>
                        )}
                        <Link
                          href={`/backups/${job.id}`}
                          className="inline-flex min-h-9 items-center text-hint underline underline-offset-4 focus-ring"
                        >
                          Open the backup job
                        </Link>
                      </div>
                    )}
                    {observed?.detail && (
                      <p className="text-hint text-muted-foreground">{observed.detail}</p>
                    )}

                    {job &&
                      uncovered.map((database) => {
                        const link = linkFor(database.resourceId)
                        const name = link?.name ?? `database ${database.resourceId}`
                        return (
                          <Notice
                            key={`uncovered-${database.resourceId}`}
                            tone="warning"
                            icon={ShieldCheck}
                            title={`${job.name} takes no native dump of ${name}`}
                          >
                            <p>
                              The release falls back to this database&rsquo;s data directory and
                              refuses if the job does not cover it. A dump is a transaction boundary
                              the engine chose; a copy of its files while it runs is not.
                            </p>
                            {canAdmin && (
                              <Button
                                size="sm"
                                variant="outline"
                                className="mt-2"
                                pending={busy === `dump-${job.id}-${database.resourceId}`}
                                onClick={() => void addDump(job, Number(database.resourceId))}
                              >
                                <Database className="size-3.5" /> Add the dump to {job.name}
                              </Button>
                            )}
                          </Notice>
                        )
                      })}

                    <OptionRow
                      title="Backup before deploy"
                      hint="Runs the job and gates the release on it finishing."
                      checked={Boolean(config.requiredBeforeDeploy)}
                      disabled={!canAdmin}
                      onCheckedChange={(requiredBeforeDeploy) =>
                        updateDependency(dependency, {
                          ...dependency,
                          config: { ...config, requiredBeforeDeploy },
                        })
                      }
                    />
                    <OptionRow
                      title="Require restore evidence"
                      hint="Refuses the release unless a restore of this job's artifact has been verified."
                      checked={Boolean(config.requireRestoreTest)}
                      disabled={!canAdmin}
                      onCheckedChange={(requireRestoreTest) =>
                        updateDependency(dependency, {
                          ...dependency,
                          config: { ...config, requireRestoreTest },
                        })
                      }
                    />
                    <Field
                      label="Maximum age (hours)"
                      htmlFor={`backup-age-${index}`}
                      hint={
                        config.maxAgeSeconds
                          ? `Refuses the release when the newest backup is older than ${plural(Math.round(config.maxAgeSeconds / 3600), "hour")}.`
                          : "Zero accepts a backup of any age."
                      }
                    >
                      <Input
                        id={`backup-age-${index}`}
                        type="number"
                        min={0}
                        readOnly={!canAdmin}
                        value={Math.round((config.maxAgeSeconds ?? 0) / 3600)}
                        onChange={(event) =>
                          updateDependency(dependency, {
                            ...dependency,
                            config: {
                              ...config,
                              maxAgeSeconds: Math.max(0, Number(event.target.value) || 0) * 3600,
                            },
                          })
                        }
                        className="w-32"
                      />
                    </Field>
                    {dependencyError?.target === dependency && (
                      <p role="alert" className="text-hint text-destructive">
                        {dependencyError.message}
                      </p>
                    )}
                  </Group>
                )
              })}
            </div>
          )}
          {/*
            A database nothing dumps and no job covers: the coverage list on
            Backups already knows how to write that job, so this sends the
            operator to it with the database chosen rather than building a
            second form here.
          */}
          {canAdmin && backupDeps.length === 0 && databaseDependencies.length > 0 && (
            <div className="flex flex-wrap gap-2">
              {databaseDependencies.map((dependency) => (
                <Button
                  key={`protect-${dependency.resourceId}`}
                  size="sm"
                  variant="outline"
                  asChild
                >
                  <Link href={`/backups?database=${dependency.resourceId}`}>
                    <Plus className="size-3.5" /> Back up{" "}
                    {linkFor(dependency.resourceId)?.name ?? `database ${dependency.resourceId}`}
                  </Link>
                </Button>
              ))}
            </div>
          )}
        </div>

        <div className="space-y-3 border-t border-hairline pt-4">
          <div className="flex items-center justify-between gap-3">
            <p className="eyebrow">Volumes</p>
            {canAdmin && (
              <Button
                size="sm"
                variant="outline"
                onClick={() =>
                  setOtherDependencies([
                    ...otherDependencies,
                    {
                      kind: "storage",
                      ownership: "linked",
                      resourceKind: "docker_volume",
                      resourceId: "",
                      config: {},
                    },
                  ])
                }
              >
                <Plus className="size-3.5" /> Add volume
              </Button>
            )}
          </div>
          {volumeDeps.length === 0 ? (
            <EmptyNote>No volume dependency is declared.</EmptyNote>
          ) : (
            <div className="space-y-3">
              {volumeDeps.map((dependency, index) => {
                const observed = observedVolumeFor(dependency.resourceId)
                return (
                  <Group key={index} className="space-y-3">
                    <div className="grid min-w-0 gap-3 sm:grid-cols-[minmax(0,1fr)_9rem_auto] sm:items-end">
                      <ResourceSelect<DockerVolume>
                        id={`volume-${index}`}
                        label="Volume"
                        path="/docker/volumes/"
                        value={dependency.resourceId ?? ""}
                        onChange={(resourceId) =>
                          updateDependency(dependency, { ...dependency, resourceId })
                        }
                        disabled={!canAdmin}
                        toId={(volume) => volume.name}
                        toLabel={(volume) => volume.name}
                      />
                      <Field label="Ownership" htmlFor={`volume-ownership-${index}`}>
                        <Select
                          value={dependency.ownership}
                          onValueChange={(ownership: Dependency["ownership"]) =>
                            updateDependency(dependency, { ...dependency, ownership })
                          }
                          disabled={!canAdmin}
                        >
                          <SelectTrigger id={`volume-ownership-${index}`} className="w-full">
                            <SelectValue />
                          </SelectTrigger>
                          <SelectContent>
                            <SelectItem value="managed">Managed</SelectItem>
                            <SelectItem value="linked">Linked</SelectItem>
                            <SelectItem value="observed">Observed</SelectItem>
                          </SelectContent>
                        </Select>
                      </Field>
                      {canAdmin && (
                        <IconAction
                          label="Remove volume dependency"
                          className="text-destructive"
                          onClick={() => removeOther(dependency)}
                        >
                          <Trash />
                        </IconAction>
                      )}
                    </div>
                    <div className="flex min-w-0 flex-wrap items-center gap-2">
                      {observed && (
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
                              ? "Present"
                              : observed.status === "missing"
                                ? "Missing"
                                : "Not observed"
                          }
                        />
                      )}
                      {observed?.target && <Tag mono>{observed.target}</Tag>}
                      {dependency.resourceId && (
                        <Link
                          href={`/docker/volumes?volume=${encodeURIComponent(dependency.resourceId)}`}
                          className="inline-flex min-h-9 items-center text-hint underline underline-offset-4 focus-ring"
                        >
                          Open the volume
                        </Link>
                      )}
                    </div>
                    {observed?.detail && (
                      <p className="text-hint text-muted-foreground">{observed.detail}</p>
                    )}
                    {dependencyError?.target === dependency && (
                      <p role="alert" className="text-hint text-destructive">
                        {dependencyError.message}
                      </p>
                    )}
                  </Group>
                )
              })}
            </div>
          )}
        </div>
      </SettingCard>

      <Panel plain>
        <PanelHeader title="Latest backup gate evidence" />
        <PanelBody>
          {!latestRunId ? (
            <EmptyNote>No deployment run has produced backup evidence yet.</EmptyNote>
          ) : latestRun.loading ? (
            <EmptyNote>Reading the latest run…</EmptyNote>
          ) : backupGateEvidence.length === 0 ? (
            <EmptyNote>The latest run did not execute a backup policy.</EmptyNote>
          ) : (
            <div className="space-y-3">
              {backupGateEvidence.map((item) => (
                <Group key={`${item.jobId}-${item.runId ?? 0}`} className="space-y-1.5">
                  <FormFacts>
                    <FormFact label="Backup job">
                      {jobFor(String(item.jobId))?.name ?? `#${item.jobId}`}
                    </FormFact>
                    <FormFact label="Run">{item.runId ? `#${item.runId}` : "—"}</FormFact>
                    <FormFact label="Fresh">{item.fresh ? "Yes" : "No"}</FormFact>
                    <FormFact label="Restore tested">
                      {item.restoreTested ? "Yes" : "No evidence"}
                    </FormFact>
                  </FormFacts>
                  {(item.databaseDumps?.length ?? 0) > 0 && (
                    <p className="text-hint text-muted-foreground">
                      {item.databaseDumps!.length === 1
                        ? "1 linked database covered by a native dump"
                        : `${item.databaseDumps!.length} linked databases covered by native dumps`}
                    </p>
                  )}
                  {item.restoreVerificationId && (
                    <p className="text-hint break-all text-muted-foreground">
                      Recovery check #{item.restoreVerificationId} · Schema{" "}
                      {item.restoreSchemaVersion}
                      {item.restoreApplicationImage ? ` · ${item.restoreApplicationImage}` : ""}
                    </p>
                  )}
                  {item.endedAt && (
                    <p className="text-hint text-muted-foreground">
                      Completed {relativeTime(item.endedAt)} · {humanize(item.status)}
                      {item.detail ? ` · ${item.detail}` : ""}
                    </p>
                  )}
                </Group>
              ))}
            </div>
          )}
        </PanelBody>
      </Panel>
      {dialog}
    </div>
  )
}

/**
 * The four figures at the top, from what the page already holds.
 *
 * Coverage is the one worth the space: the gate refuses a release whose job
 * takes no dump of a linked database, and until this reading existed that
 * refusal arrived during the deployment it stopped.
 */
function summarise(
  databases: Dependency[],
  links: DeploymentDatabaseLink[] | undefined,
  backupDeps: Dependency[],
  jobs: BackupJob[] | undefined,
) {
  const linked = databases.length
  const observed = databases
    .map((dependency) => links?.find((link) => String(link.connectionId) === dependency.resourceId))
    .filter((link): link is DeploymentDatabaseLink => Boolean(link))
  const engines = [...new Set(observed.map((link) => link.driver))].join(", ")
  const failing = observed.filter((link) => link.status === "unavailable").length
  const stale = observed.filter((link) => link.status === "stale").length

  const declared = backupDeps
    .map((dependency) => jobs?.find((job) => String(job.id) === dependency.resourceId))
    .filter((job): job is BackupJob => Boolean(job))
  const required = backupDeps.some(
    (dependency) => (dependency.config as BackupDependencyConfig)?.requiredBeforeDeploy,
  )
  const dumped = databases.filter((dependency) =>
    declared.some((job) => (job.databaseDumps ?? []).includes(Number(dependency.resourceId))),
  ).length
  const newestSuccess = declared
    .map((job) => job.lastSuccessAt)
    .filter((stamp): stamp is string => Boolean(stamp))
    .sort()
    .at(-1)

  return {
    linked,
    engines,
    connection:
      linked === 0
        ? { value: "—", tone: "default" as const }
        : failing > 0
          ? {
              value: `${failing} need${failing === 1 ? "s" : ""} attention`,
              tone: "danger" as const,
            }
          : stale > 0
            ? { value: "Not observed recently", tone: "warning" as const }
            : observed.length === linked
              ? { value: "All connected", tone: "success" as const }
              : { value: "Awaiting deployment", tone: "default" as const },
    policy:
      backupDeps.length === 0
        ? {
            value: "None",
            tone: (linked > 0 ? "warning" : "default") as "warning" | "default",
            hint: linked > 0 ? "A linked database with no backup policy" : undefined,
          }
        : {
            value: required ? "Required" : "Declared",
            tone: "default" as const,
            hint: newestSuccess ? `Last success ${relativeTime(newestSuccess)}` : "Never succeeded",
          },
    dumps:
      linked === 0
        ? { value: "—", tone: "default" as const, hint: undefined }
        : {
            value: `${dumped} of ${linked}`,
            tone: (dumped === linked ? "success" : "warning") as "success" | "warning",
            hint:
              dumped === linked ? "Every linked database" : "A database falls back to its files",
          },
  }
}

function ResourceSelect<T>({
  id,
  label,
  path,
  given,
  value,
  onChange,
  disabled,
  toId,
  toLabel,
}: {
  id: string
  label: string
  path: string
  /** Already held by the caller, so the page reads the list once. */
  given?: T[]
  value: string
  onChange: (id: string) => void
  disabled?: boolean
  toId: (item: T) => string
  toLabel: (item: T) => string
}) {
  const resources = usePoll((signal) => get<T[]>(path, undefined, signal), 0, [path], {
    enabled: given === undefined,
  })
  const items = given ?? resources.data ?? []
  const loading = given === undefined && resources.loading
  return (
    <Field label={label} htmlFor={id}>
      <Select value={value} onValueChange={onChange} disabled={disabled || loading}>
        <SelectTrigger id={id} className="w-full">
          <SelectValue placeholder={loading ? "Loading…" : "Choose a resource"} />
        </SelectTrigger>
        <SelectContent>
          {value && !items.some((item) => toId(item) === value) && (
            <SelectItem value={value}>{value}</SelectItem>
          )}
          {items.map((item) => (
            <SelectItem key={toId(item)} value={toId(item)}>
              {toLabel(item)}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </Field>
  )
}
