"use client"

import { useState } from "react"
import Link from "next/link"
import { Plus, Trash } from "@/components/icons"
import { ApiError, get, put, refusedIndex } from "@/lib/api"
import { relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import type {
  BackupJob,
  DbConnection,
  DeploymentBackupGateEvidence,
  DeploymentEnvironmentConfiguration,
  DeploymentRunSnapshot,
  DockerVolume,
} from "@/lib/types"
import { Field, FormFact, FormFacts, OptionRow } from "@/components/form"
import { Group, Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyNote } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { IconAction } from "@/components/icon-action"
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

type DatabaseLink = {
  connectionId: number
  name: string
  network: string
  hostname: string
  status: string
  checkedAt?: string
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
  const project = useProject()
  const databaseDependencies = configuration.dependencies.filter((d) => d.kind === "database")
  // Backup and volume dependencies batch through one local copy and one Save,
  // the way the other settings sections do; a database link is its own
  // mutation (it also writes a variable), so it is never staged here.
  const [otherDependencies, setOtherDependencies] = useState<Dependency[]>(() =>
    configuration.dependencies.filter((d) => d.kind !== "database"),
  )
  const [saving, setSaving] = useState(false)
  const [dependencyError, setDependencyError] = useState<{ target: Dependency; message: string }>()

  const links = usePoll(
    (signal) =>
      get<DatabaseLink[]>(
        `/deploy/${projectId}/environments/${environmentId}/database-links`,
        undefined,
        signal,
      ),
    5000,
    [projectId, environmentId],
  )
  const linkFor = (resourceId?: string) =>
    links.data?.find((link) => String(link.connectionId) === resourceId)

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

  const removeDatabase = async (index: number) => {
    try {
      // Same reasoning as `connect`: build the kept half from the saved
      // configuration, not the staged rows, so removing a database can never
      // commit an unrelated unfinished edit along with it.
      const savedOtherDependencies = configuration.dependencies.filter(
        (dependency) => dependency.kind !== "database",
      )
      const nextDatabaseDeps = databaseDependencies.filter((_, i) => i !== index)
      await save({ dependencies: [...savedOtherDependencies, ...nextDatabaseDeps] })
      notify.success("Dependencies saved")
    } catch (error) {
      notify.error("Could not remove database", error)
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

  const backups = project.operations?.backups

  return (
    <div className="space-y-6">
      <PendingChanges pending={configuration.pending} />

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
            <ul aria-label="Linked databases" className="divide-y divide-hairline">
              {databaseDependencies.map((dependency, index) => {
                const link = linkFor(dependency.resourceId)
                const connected = link?.status === "connected"
                return (
                  <li
                    key={`${dependency.resourceId}-${index}`}
                    className="min-w-0 space-y-1 py-3 first:pt-0 last:pb-0"
                  >
                    <div className="flex min-w-0 flex-wrap items-center justify-between gap-2">
                      <Link
                        href={`/databases?conn=${dependency.resourceId}`}
                        className="min-w-0 truncate rounded-sm text-body font-medium focus-ring hover:underline"
                      >
                        {link?.name ?? `Database ${dependency.resourceId}`}
                      </Link>
                      <div className="flex shrink-0 items-center gap-2">
                        <Status
                          tone={connected ? "running" : "warning"}
                          label={connected ? "Connected" : "Needs reconnection"}
                        />
                        {canAdmin && (
                          <IconAction
                            label="Remove database"
                            className="text-destructive"
                            onClick={() => void removeDatabase(index)}
                          >
                            <Trash />
                          </IconAction>
                        )}
                      </div>
                    </div>
                    {link?.hostname && (
                      <p className="truncate font-mono text-hint text-muted-foreground">
                        {link.hostname}
                      </p>
                    )}
                    {!connected && (
                      <p className="text-hint text-warning">
                        Check that the original database container or Compose service is running.
                        The dashboard retries the connection every five seconds.
                      </p>
                    )}
                    {link?.checkedAt && (
                      <p className="text-hint text-muted-foreground">
                        Last checked {relativeTime(link.checkedAt)}
                      </p>
                    )}
                    {dependencyError?.target === dependency && (
                      <p role="alert" className="text-hint text-destructive">
                        {dependencyError.message}
                      </p>
                    )}
                  </li>
                )
              })}
            </ul>
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
            <EmptyNote>No backup dependency is declared.</EmptyNote>
          ) : (
            <div className="space-y-3">
              {backupDeps.map((dependency, index) => {
                const config = (dependency.config ?? {}) as {
                  requiredBeforeDeploy?: boolean
                  requireRestoreTest?: boolean
                  maxAgeSeconds?: number
                }
                return (
                  <Group key={index} className="space-y-3">
                    <div className="grid min-w-0 gap-3 sm:grid-cols-[minmax(0,1fr)_auto]">
                      <ResourceSelect<BackupJob>
                        id={`backup-job-${index}`}
                        label="Backup job"
                        path="/backups/"
                        value={dependency.resourceId ?? ""}
                        onChange={(resourceId) =>
                          updateDependency(dependency, { ...dependency, resourceId })
                        }
                        disabled={!canAdmin}
                        toId={(job) => String(job.id)}
                        toLabel={(job) => job.name}
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
                    <Field label="Maximum age (hours)" htmlFor={`backup-age-${index}`}>
                      <Input
                        id={`backup-age-${index}`}
                        type="number"
                        min={0}
                        readOnly={!canAdmin}
                        value={Math.round((config.maxAgeSeconds ?? 0) / 3600)}
                        onChange={(event) =>
                          updateDependency(dependency, {
                            ...dependency,
                            config: { ...config, maxAgeSeconds: Number(event.target.value) * 3600 },
                          })
                        }
                        className="w-32"
                      />
                    </Field>
                    {dependency.resourceId && (
                      <Link
                        href={`/backups?job=${dependency.resourceId}`}
                        className="inline-flex min-h-9 items-center text-hint underline underline-offset-4 focus-ring"
                      >
                        Open the backup job
                      </Link>
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
              {volumeDeps.map((dependency, index) => (
                <Group
                  key={index}
                  className="grid min-w-0 gap-3 sm:grid-cols-[minmax(0,1fr)_9rem_auto] sm:items-end"
                >
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
                  {dependency.resourceId && (
                    <Link
                      href="/docker/volumes"
                      className="inline-flex min-h-9 w-fit items-center text-hint underline underline-offset-4 focus-ring sm:col-span-3"
                    >
                      Open the volume
                    </Link>
                  )}
                  {dependencyError?.target === dependency && (
                    <p
                      role="alert"
                      className="text-hint text-destructive sm:col-span-2 lg:col-span-3"
                    >
                      {dependencyError.message}
                    </p>
                  )}
                </Group>
              ))}
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
                    <FormFact label="Backup job">#{item.jobId}</FormFact>
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

      <Panel plain>
        <PanelHeader title="Backups" />
        <PanelBody>
          {!backups || backups.status !== "available" ? (
            <EmptyNote>{backups?.reason ?? "The Backups module could not be read."}</EmptyNote>
          ) : backups.jobs.length === 0 ? (
            <EmptyNote>{backups.reason ?? "This release declares no backup policy."}</EmptyNote>
          ) : (
            <ul aria-label="Backup evidence" className="divide-y divide-hairline">
              {backups.jobs.map((job) => (
                <li key={job.resourceId} className="min-w-0 space-y-1.5 py-3 first:pt-0 last:pb-0">
                  <div className="flex min-w-0 flex-wrap items-center gap-2">
                    <span className="text-body font-medium">Backup job {job.resourceId}</span>
                    <Status
                      tone={
                        job.status === "present"
                          ? "running"
                          : job.status === "missing"
                            ? "danger"
                            : "unknown"
                      }
                      label={
                        job.status === "present"
                          ? "Present"
                          : job.status === "missing"
                            ? "Missing"
                            : "Not observed"
                      }
                    />
                    {job.required && <Tag tone={job.fresh ? "success" : "warning"}>required</Tag>}
                  </div>
                  <p className="text-hint text-muted-foreground">
                    {job.status === "present"
                      ? `Last run ${job.lastStatus ?? "unknown"}${job.required ? (job.fresh ? " · within maximum age" : " · outside maximum age") : ""}`
                      : (job.detail ?? "No observation was returned.")}
                  </p>
                  {job.deepLink && (
                    <Link
                      href={job.deepLink}
                      className="inline-flex min-h-9 items-center text-hint underline underline-offset-4 focus-ring"
                    >
                      Open the backup job
                    </Link>
                  )}
                </li>
              ))}
            </ul>
          )}
        </PanelBody>
      </Panel>
    </div>
  )
}

function ResourceSelect<T>({
  id,
  label,
  path,
  value,
  onChange,
  disabled,
  toId,
  toLabel,
}: {
  id: string
  label: string
  path: string
  value: string
  onChange: (id: string) => void
  disabled?: boolean
  toId: (item: T) => string
  toLabel: (item: T) => string
}) {
  const resources = usePoll((signal) => get<T[]>(path, undefined, signal), 0, [path])
  const items = resources.data ?? []
  return (
    <Field label={label} htmlFor={id}>
      <Select value={value} onValueChange={onChange} disabled={disabled || resources.loading}>
        <SelectTrigger id={id} className="w-full">
          <SelectValue placeholder={resources.loading ? "Loading…" : "Choose a resource"} />
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
