"use client"

import { useEffect, useMemo, useRef, useState } from "react"
import { forgetMemoryState, useMemoryState } from "@/lib/view-state"
import { notify } from "@/lib/toast"
import { ApiError, get, post, put } from "@/lib/api"
import { bytes } from "@/lib/format"
import { unusableReason } from "@/lib/db-connections"
import { cn } from "@/lib/utils"
import type { BackupJob, BackupResource, Container, DbConnection } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { Modal } from "@/components/modal"
import { Group } from "@/components/panel"
import { Field, FieldRow, FormNote, FormSection, OptionList, OptionRow } from "@/components/form"
import { ErrorState, Notice } from "@/components/state"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Textarea } from "@/components/ui/textarea"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { SearchInput } from "@/components/page"
import { RESOURCE_KIND_LABEL, lines } from "@/components/backups/shared"
import { encrypts } from "@/components/backups/encryption"
import {
  backupInputs,
  editPickedInput,
  pickedInputs,
  startResourcePicks,
  toggleResourcePick,
  type BackupInputField,
} from "@/components/backups/resource-picks"
import {
  ScheduleBuilder,
  scheduleExpression,
  scheduleFields,
  scheduleValid,
} from "@/components/schedule-builder"

/** What a job starts out as when it is opened for something in particular. */
export type JobPrefill = {
  resourceKey?: string
  name?: string
  sources?: string[]
  excludes?: string[]
  sqlitePaths?: string[]
  databaseDumps?: number[]
  pauseContainers?: string[]
}

/**
 * The job form: what to back up, where, when, and what keeps the copy
 * honest. It is mounted only while open, so every field starts from the job
 * being edited rather than from whatever the list held when the page loaded.
 *
 * Sources can be typed, or picked from what the dashboard already knows —
 * a volume, a stack, a checkout, a database — and a pick brings its own
 * consistency settings with it: the containers to pause, the SQLite file to
 * snapshot, the connection to dump.
 */
export function JobDialog({
  job,
  prefill,
  resources,
  onOpenChange,
  onDone,
}: {
  job?: BackupJob
  prefill?: JobPrefill
  resources: BackupResource[]
  onOpenChange: (open: boolean) => void
  onDone: () => void
}) {
  // Every field is kept in memory for the tab until the dialog is closed —
  // memory rather than storage, because the destination's keys are typed
  // here too — so checking a path or a volume does not mean starting over.
  const draft = `backups.job.${job?.id ?? "new"}`
  // The revision belongs to the held draft, including after leaving the page;
  // adopting a newer poll while keeping old fields would bypass the guard.
  const [baseline, setBaseline] = useMemoryState<BackupJob | null>(`${draft}.baseline`, null)
  const openedJob = baseline ?? job
  const [conflict, setConflict] = useMemoryState(`${draft}.conflict`, false)
  const [reviewed, setReviewed] = useMemoryState<BackupJob | null>(`${draft}.reviewed`, null)
  const active = useRef(true)
  useEffect(() => {
    active.current = true
    return () => {
      active.current = false
    }
  }, [])
  useEffect(() => {
    if (job && !baseline) setBaseline(job)
  }, [job, baseline, setBaseline])
  useEffect(() => {
    const clear = () => {
      active.current = false
      forgetMemoryState(`${draft}.`)
      onOpenChange(false)
    }
    window.addEventListener("jd-working-state-cleared", clear)
    return () => window.removeEventListener("jd-working-state-cleared", clear)
  }, [draft, onOpenChange])
  const [name, setName] = useMemoryState(`${draft}.name`, openedJob?.name ?? prefill?.name ?? "")
  const [sources, setSources] = useMemoryState(
    `${draft}.sources`,
    (openedJob?.sources ?? prefill?.sources ?? []).join("\n"),
  )
  const [excludes, setExcludes] = useMemoryState(
    `${draft}.excludes`,
    (openedJob?.excludes ?? prefill?.excludes ?? []).join("\n"),
  )
  const [targetKind, setTargetKind] = useMemoryState<BackupJob["targetKind"]>(
    `${draft}.targetKind`,
    openedJob?.targetKind ?? "local",
  )
  const [path, setPath] = useMemoryState(
    `${draft}.path`,
    openedJob?.target.path ?? "/var/backups/just-dashboard",
  )
  const [bucket, setBucket] = useMemoryState(`${draft}.bucket`, openedJob?.target.bucket ?? "")
  const [region, setRegion] = useMemoryState(`${draft}.region`, openedJob?.target.region ?? "")
  const [endpoint, setEndpoint] = useMemoryState(
    `${draft}.endpoint`,
    openedJob?.target.endpoint ?? "",
  )
  const [prefix, setPrefix] = useMemoryState(`${draft}.prefix`, openedJob?.target.prefix ?? "")
  const [accessKey, setAccessKey] = useMemoryState(`${draft}.accessKey`, "")
  const [secretKey, setSecretKey] = useMemoryState(`${draft}.secretKey`, "")
  // Unset follows the destination until the operator turns it either way.
  const [encryptChoice, setEncrypt] = useMemoryState<boolean | null>(
    `${draft}.encrypt`,
    openedJob?.encrypt ?? null,
  )
  const encrypt = encrypts(encryptChoice, targetKind)
  const [schedule, setSchedule] = useMemoryState(
    `${draft}.schedule`,
    scheduleFields(openedJob?.schedule ?? "0 3 * * *"),
  )
  const [retention, setRetention] = useMemoryState(
    `${draft}.retention`,
    String(openedJob?.retention ?? 7),
  )
  const [retentionDays, setRetentionDays] = useMemoryState(
    `${draft}.retentionDays`,
    String(openedJob?.retentionDays ?? 0),
  )
  const [enabled, setEnabled] = useMemoryState(`${draft}.enabled`, openedJob?.enabled ?? true)
  const [sqlitePaths, setSQLitePaths] = useMemoryState(
    `${draft}.sqlitePaths`,
    (openedJob?.sqlitePaths ?? prefill?.sqlitePaths ?? []).join("\n"),
  )
  const [databaseDumps, setDatabaseDumps] = useMemoryState<number[]>(
    `${draft}.databaseDumps`,
    openedJob?.databaseDumps ?? prefill?.databaseDumps ?? [],
  )
  const [pauseContainers, setPauseContainers] = useMemoryState<string[]>(
    `${draft}.pauseContainers`,
    openedJob?.pauseContainers ?? prefill?.pauseContainers ?? [],
  )
  const [resourcePicks, setResourcePicks] = useMemoryState(
    `${draft}.resourcePicks`,
    startResourcePicks(
      backupInputs({
        sources: lines(sources),
        excludes: lines(excludes),
        sqlitePaths: lines(sqlitePaths),
        databaseDumps,
        pauseContainers,
      }),
      job ? undefined : prefill?.resourceKey,
    ),
  )
  const [recoveryEnabled, setRecoveryEnabled] = useMemoryState(
    `${draft}.recoveryEnabled`,
    Boolean(openedJob?.recovery),
  )
  const [recoveryImage, setRecoveryImage] = useMemoryState(
    `${draft}.recoveryImage`,
    openedJob?.recovery?.image ?? "",
  )
  const [recoveryCommand, setRecoveryCommand] = useMemoryState(
    `${draft}.recoveryCommand`,
    (openedJob?.recovery?.command ?? []).join("\n"),
  )
  const [recoverySchema, setRecoverySchema] = useMemoryState(
    `${draft}.recoverySchema`,
    openedJob?.recovery?.schemaVersion ?? "",
  )
  const [expectedOutput, setExpectedOutput] = useMemoryState(`${draft}.expectedOutput`, "")
  const [recoveryAutomatic, setRecoveryAutomatic] = useMemoryState(
    `${draft}.recoveryAutomatic`,
    openedJob?.recovery?.automatic ?? false,
  )
  const [busy, setBusy] = useState(false)

  const connections = usePoll(
    (signal) => get<DbConnection[]>("/databases/", undefined, signal),
    0,
    [],
  )
  const containers = usePoll(
    (signal) => get<Container[]>("/docker/containers/", undefined, signal),
    0,
    [],
  )

  const expression = scheduleExpression(schedule)
  const valid = scheduleValid(expression)

  const sourceList = lines(sources)
  const canSave =
    name.trim().length > 0 &&
    (sourceList.length > 0 || databaseDumps.length > 0) &&
    valid &&
    !busy &&
    (!job || (openedJob?.revision ?? 0) > 0)

  const changeOpen = (open: boolean) => {
    if (!open) {
      active.current = false
      forgetMemoryState(`${draft}.`)
    }
    onOpenChange(open)
  }

  const body = {
    name: name.trim(),
    sources: sourceList,
    excludes: lines(excludes),
    targetKind,
    target: targetKind === "local" ? { path } : { bucket, region, endpoint, prefix },
    schedule: expression,
    retention: Math.max(0, Number(retention) || 0),
    retentionDays: Math.max(0, Number(retentionDays) || 0),
    enabled,
    sqlitePaths: lines(sqlitePaths),
    databaseDumps,
    pauseContainers,
    encrypt,
    recovery: recoveryEnabled
      ? {
          image: recoveryImage.trim(),
          command: recoveryCommand.split("\n").filter((arg) => arg.length > 0),
          schemaVersion: recoverySchema.trim(),
          expectedOutputDigest: openedJob?.recovery?.expectedOutputDigest ?? "",
          timeoutSeconds: openedJob?.recovery?.timeoutSeconds ?? 60,
          maxBytes: openedJob?.recovery?.maxBytes ?? 16 * 1024 ** 3,
          automatic: recoveryAutomatic,
        }
      : null,
    expectedRecoveryOutput: expectedOutput.trim() || undefined,
    // Omitted when blank so editing a schedule does not wipe stored keys.
    secrets:
      accessKey || secretKey ? { accessKeyId: accessKey, secretAccessKey: secretKey } : undefined,
  }
  const submit = async (expectedRevision = openedJob?.revision) => {
    if (!canSave) return
    setBusy(true)
    try {
      if (job) await put(`/backups/${job.id}`, { ...body, expectedRevision })
      else await post("/backups/", body)
      if (!active.current) return
      notify.success(job ? "Job updated" : "Job created")
      changeOpen(false)
      onDone()
    } catch (err) {
      if (!active.current) return
      if (err instanceof ApiError && err.code === "backup_job_conflict") {
        setConflict(true)
        setReviewed(null)
      } else notify.error("Could not save", err)
    } finally {
      if (active.current) setBusy(false)
    }
  }

  const reviewCurrent = async () => {
    if (!job) return
    setBusy(true)
    try {
      const current = await get<BackupJob>(`/backups/${job.id}`)
      if (active.current) setReviewed(current)
    } catch (err) {
      if (active.current) notify.error("Could not read the current job", err)
    } finally {
      if (active.current) setBusy(false)
    }
  }

  const picked = (res: BackupResource) => Boolean(resourcePicks.picks[`${res.kind}:${res.id}`])
  const togglePick = (res: BackupResource, on: boolean) => {
    const next = toggleResourcePick(
      resourcePicks,
      `${res.kind}:${res.id}`,
      backupInputs(res.suggest),
      on,
    )
    setResourcePicks(next)
    const inputs = pickedInputs(next)
    setSources(inputs.sources.join("\n"))
    setExcludes(inputs.excludes.join("\n"))
    setSQLitePaths(inputs.sqlitePaths.join("\n"))
    setPauseContainers(inputs.pauseContainers)
    setDatabaseDumps(inputs.databaseDumps.map(Number).sort((a, b) => a - b))
    if (on && !name.trim() && !job) setName(res.suggest.name)
  }
  const rememberManual = (field: BackupInputField, values: string[]) =>
    setResourcePicks((current) => editPickedInput(current, field, values))

  return (
    <Modal
      open
      onOpenChange={changeOpen}
      size="lg"
      title={job ? `Edit ${job.name}` : "New backup"}
      description="Choose what to back up, where the archives go, when the job runs, and how the copy is kept consistent."
      footer={
        <Button onClick={() => void submit()} disabled={!canSave || conflict}>
          {job ? "Save" : "Create"}
        </Button>
      }
    >
      <div className="space-y-6">
        {conflict && (
          <Notice
            tone="warning"
            title={<span role="alert">This job changed while you were editing</span>}
          >
            <div className="space-y-3">
              <p>Your draft and entered keys are kept. Review the saved job before trying again.</p>
              <Button variant="outline" onClick={reviewCurrent} disabled={busy}>
                Review current job
              </Button>
              {reviewed && (
                <>
                  <JobSaveReview current={reviewed} draft={body} />
                  <p>
                    Saving this draft replaces the reviewed settings.
                    {body.secrets && " Your newly entered destination keys will also be saved."}
                    {body.expectedRecoveryOutput &&
                      " Your new expected recovery output will also be saved."}
                  </p>
                  <div className="flex flex-wrap gap-2">
                    <Button
                      onClick={() => void submit(reviewed.revision)}
                      disabled={!canSave || reviewed.revision <= 0}
                    >
                      Save draft over reviewed job
                    </Button>
                    <Button
                      variant="outline"
                      disabled={busy}
                      onClick={() => {
                        changeOpen(false)
                        onDone()
                      }}
                    >
                      Discard draft and close
                    </Button>
                  </div>
                </>
              )}
            </div>
          </Notice>
        )}
        <FormSection title="What to back up">
          <Field label="Name" htmlFor="job-name">
            <Input id="job-name" value={name} onChange={(e) => setName(e.target.value)} />
          </Field>
          <ResourcePicker resources={resources} picked={picked} onToggle={togglePick} />
          <FormNote>
            Pick resources to add their suggested paths and consistency settings. Unpicking keeps
            values entered manually or used by another pick. A job needs a source path or a database
            dump.
          </FormNote>
          <FieldRow>
            <Field
              label="Sources (one per line)"
              htmlFor="job-sources"
              hint="Absolute paths on this server. Each becomes its own tree in the archive."
            >
              <Textarea
                id="job-sources"
                value={sources}
                onChange={(e) => {
                  setSources(e.target.value)
                  rememberManual("sources", lines(e.target.value))
                }}
                rows={4}
                className="font-mono text-xs"
                placeholder="/srv/app&#10;/etc/nginx"
              />
            </Field>
            <Field
              label="Excludes (glob, one per line)"
              htmlFor="job-excludes"
              hint="Matched against the file name and the full path."
            >
              <Textarea
                id="job-excludes"
                value={excludes}
                onChange={(e) => {
                  setExcludes(e.target.value)
                  rememberManual("excludes", lines(e.target.value))
                }}
                rows={4}
                className="font-mono text-xs"
                placeholder="node_modules&#10;*.log"
              />
            </Field>
          </FieldRow>
        </FormSection>

        <FormSection title="Where the archives go">
          <Field label="Destination" htmlFor="job-target">
            <Select value={targetKind} onValueChange={(v) => setTargetKind(v as typeof targetKind)}>
              <SelectTrigger id="job-target" size="sm" className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="local">A directory on this server</SelectItem>
                <SelectItem value="s3">Amazon S3 or any S3-compatible bucket</SelectItem>
                <SelectItem value="b2">Backblaze B2</SelectItem>
              </SelectContent>
            </Select>
          </Field>
          {targetKind === "local" ? (
            <Field
              label="Directory"
              htmlFor="job-path"
              hint="Created if it does not exist. A copy on the same disk is not a disaster plan — pair it with an off-server destination for anything that matters."
            >
              <Input
                id="job-path"
                value={path}
                onChange={(e) => setPath(e.target.value)}
                className="font-mono text-body"
              />
            </Field>
          ) : (
            <>
              <FieldRow>
                <Field label="Bucket" htmlFor="job-bucket">
                  <Input
                    id="job-bucket"
                    value={bucket}
                    onChange={(e) => setBucket(e.target.value)}
                  />
                </Field>
                <Field label="Region" htmlFor="job-region">
                  <Input
                    id="job-region"
                    value={region}
                    onChange={(e) => setRegion(e.target.value)}
                    placeholder="us-east-1"
                  />
                </Field>
              </FieldRow>
              <FieldRow>
                <Field
                  label="Endpoint"
                  htmlFor="job-endpoint"
                  hint={targetKind === "b2" ? "From the bucket's details page." : "Blank for AWS."}
                >
                  <Input
                    id="job-endpoint"
                    value={endpoint}
                    onChange={(e) => setEndpoint(e.target.value)}
                    placeholder={targetKind === "b2" ? "s3.us-west-004.backblazeb2.com" : ""}
                  />
                </Field>
                <Field label="Prefix" htmlFor="job-prefix" hint="A folder inside the bucket.">
                  <Input
                    id="job-prefix"
                    value={prefix}
                    onChange={(e) => setPrefix(e.target.value)}
                  />
                </Field>
              </FieldRow>
              <FieldRow>
                <Field label="Access key ID" htmlFor="job-access">
                  <Input
                    id="job-access"
                    value={accessKey}
                    onChange={(e) => setAccessKey(e.target.value)}
                    placeholder={job?.hasCredentials ? "unchanged" : ""}
                  />
                </Field>
                <Field
                  label="Secret access key"
                  htmlFor="job-secret"
                  hint="Sealed with the dashboard's master key and never shown again."
                >
                  <Input
                    id="job-secret"
                    type="password"
                    value={secretKey}
                    onChange={(e) => setSecretKey(e.target.value)}
                    placeholder={job?.hasCredentials ? "unchanged" : ""}
                  />
                </Field>
              </FieldRow>
            </>
          )}
          <OptionList>
            <OptionRow
              title="Encrypt archives with this server's backup key"
              hint="Restores, recovery checks and downloads here decrypt as they read. Export the key from Backups to open an archive anywhere else. Archives already taken stay as they were written."
              checked={encrypt}
              onCheckedChange={setEncrypt}
            />
          </OptionList>
        </FormSection>

        <FormSection title="When it runs">
          <ScheduleBuilder
            fields={schedule}
            onChange={setSchedule}
            idPrefix="job"
            label="Runs"
            manual
          />
          <FieldRow>
            <Field
              label="Keep the last"
              htmlFor="job-retention"
              hint="Archives kept per job. 0 keeps every one."
            >
              <Input
                id="job-retention"
                type="number"
                min={0}
                value={retention}
                onChange={(e) => setRetention(e.target.value)}
              />
            </Field>
            <Field
              label="And nothing older than"
              htmlFor="job-retention-days"
              hint="Days. 0 keeps by count alone. The newest archive is never pruned by age."
            >
              <Input
                id="job-retention-days"
                type="number"
                min={0}
                value={retentionDays}
                onChange={(e) => setRetentionDays(e.target.value)}
              />
            </Field>
          </FieldRow>
          <OptionList>
            <OptionRow
              title="Schedule enabled"
              hint="Off pauses the schedule. The job can still be run by hand and its archives stay where they are."
              checked={enabled}
              onCheckedChange={setEnabled}
            />
          </OptionList>
        </FormSection>

        <FormSection
          title="Consistency"
          hint="A file copied while it is being written is a file that may not open. These capture the moving parts at one instant."
        >
          <Field
            label="SQLite files to snapshot (one per line)"
            htmlFor="job-sqlite"
            hint="Must be inside a source above. Each gets a native consistent snapshot in place of its live journals."
          >
            <Textarea
              id="job-sqlite"
              value={sqlitePaths}
              onChange={(e) => {
                setSQLitePaths(e.target.value)
                rememberManual("sqlitePaths", lines(e.target.value))
              }}
              rows={2}
              className="font-mono text-xs"
            />
          </Field>
          <fieldset className="space-y-1.5">
            <legend className="text-body font-medium">Database dumps</legend>
            <FormNote>
              Each saved connection is dumped with its engine&apos;s own tool — pg_dump, mysqldump,
              mongodump — or the dashboard&apos;s built-in dump, and stored in the archive. Redis is
              always the built-in one: its keys as JSON Lines that the dashboard restores, not an
              RDB file. A deployment linked to the database accepts this as its backup coverage.
            </FormNote>
            {connections.error && <ErrorState error={connections.error} />}
            {(connections.data?.length ?? 0) === 0 &&
              !connections.loading &&
              !connections.error && (
                <FormNote>
                  No saved database connections yet. Add one on the Databases page first.
                </FormNote>
              )}
            <div className="grid gap-1.5 sm:grid-cols-2">
              {connections.data?.map((connection) => {
                // A connection that no longer opens cannot be dumped. It can
                // still be taken off a job that names it, and not put on one.
                const unusable = unusableReason(connection)
                const chosen = databaseDumps.includes(connection.id)
                return (
                  <Label
                    key={connection.id}
                    className="flex min-h-8 items-center gap-2 text-xs font-normal"
                  >
                    <Checkbox
                      checked={chosen}
                      disabled={Boolean(unusable) && !chosen}
                      onCheckedChange={(checked) => {
                        const next = checked
                          ? [...new Set([...databaseDumps, connection.id])].sort((a, b) => a - b)
                          : databaseDumps.filter((id) => id !== connection.id)
                        setDatabaseDumps(next)
                        rememberManual("databaseDumps", next.map(String))
                      }}
                      aria-label={`Dump ${connection.name}`}
                    />
                    <span
                      className={cn("min-w-0 truncate", unusable && "text-muted-foreground")}
                      title={unusable}
                    >
                      {connection.name}
                      <span className="text-muted-foreground">
                        {" "}
                        · {connection.driver}
                        {unusable
                          ? ` · cannot be opened: ${unusable}`
                          : connection.database
                            ? ` · ${connection.database}`
                            : ""}
                      </span>
                    </span>
                  </Label>
                )
              })}
            </div>
          </fieldset>
          <fieldset className="space-y-1.5">
            <legend className="text-body font-medium">Containers to pause while archiving</legend>
            <FormNote>
              A paused container is frozen, not stopped: it resumes where it was the moment the
              archive is written, and nothing it was doing is lost. Pause whatever writes to a
              volume you are backing up. A stopped container needs no pause and is skipped.
            </FormNote>
            {containers.error && (
              <FormNote>
                Docker is not reachable, so containers cannot be listed here.
                {pauseContainers.length > 0 && " The job keeps the ones it already names."}
              </FormNote>
            )}
            <div className="grid gap-1.5 sm:grid-cols-2">
              {[
                ...(containers.data ?? []),
                ...pauseContainers
                  .filter((name) => !containers.data?.some((c) => c.name === name))
                  .map((name) => ({ id: name, name, state: "unknown" })),
              ].map((container) => (
                <Label
                  key={container.id}
                  className="flex min-h-8 items-center gap-2 text-xs font-normal"
                >
                  <Checkbox
                    checked={pauseContainers.includes(container.name)}
                    onCheckedChange={(checked) => {
                      const next = checked
                        ? [...new Set([...pauseContainers, container.name])]
                        : pauseContainers.filter((name) => name !== container.name)
                      setPauseContainers(next)
                      rememberManual("pauseContainers", next)
                    }}
                    aria-label={`Pause ${container.name}`}
                  />
                  <span className="min-w-0 truncate">
                    {container.name}
                    <span className="text-muted-foreground"> · {container.state}</span>
                  </span>
                </Label>
              ))}
            </div>
          </fieldset>
        </FormSection>

        <FormSection
          title="Recovery check"
          hint="Proves the archive restores by restoring it: a temporary copy, your application's checker, no network."
        >
          <OptionList>
            <OptionRow
              title="Application recovery check"
              hint="Restored sources appear at /restore/source-0001, /restore/source-0002, in source order. The checker opens the restored data, verifies its schema and canary record, prints the expected result and exits 0."
              checked={recoveryEnabled}
              onCheckedChange={setRecoveryEnabled}
            >
              <div className="space-y-3">
                <Field
                  label="Application image digest"
                  htmlFor="job-recovery-image"
                  hint="Retain or pull this exact image on the server before checking."
                >
                  <Input
                    id="job-recovery-image"
                    value={recoveryImage}
                    onChange={(e) => setRecoveryImage(e.target.value)}
                    placeholder="sha256:… or registry/app@sha256:…"
                    className="font-mono text-xs"
                  />
                </Field>
                <Field
                  label="Checker executable and arguments (one per line)"
                  htmlFor="job-recovery-command"
                >
                  <Textarea
                    id="job-recovery-command"
                    value={recoveryCommand}
                    onChange={(e) => setRecoveryCommand(e.target.value)}
                    rows={3}
                    className="font-mono text-xs"
                    placeholder="/app/check-recovery&#10;/restore/source-0001"
                  />
                </Field>
                <FieldRow>
                  <Field label="Expected schema version" htmlFor="job-recovery-schema">
                    <Input
                      id="job-recovery-schema"
                      value={recoverySchema}
                      onChange={(e) => setRecoverySchema(e.target.value)}
                    />
                  </Field>
                  <Field
                    label="Expected canary output"
                    htmlFor="job-recovery-output"
                    hint="Only its SHA-256 fingerprint is saved."
                  >
                    <Input
                      id="job-recovery-output"
                      value={expectedOutput}
                      onChange={(e) => setExpectedOutput(e.target.value)}
                      placeholder={
                        job?.recovery
                          ? "Blank keeps the saved fingerprint"
                          : "schema-v1:canary-present"
                      }
                    />
                  </Field>
                </FieldRow>
                <OptionList>
                  <OptionRow
                    title="Verify after every successful backup"
                    hint={`Each check is limited to ${job?.recovery?.timeoutSeconds ?? 60} seconds and ${bytes(job?.recovery?.maxBytes ?? 16 * 1024 ** 3)} of restored data.`}
                    checked={recoveryAutomatic}
                    onCheckedChange={setRecoveryAutomatic}
                  />
                </OptionList>
              </div>
            </OptionRow>
          </OptionList>
        </FormSection>
      </div>
    </Modal>
  )
}

/**
 * What the dashboard already knows about, offered as sources. Grouped by
 * kind, filterable, and each with the job it would need — so backing up a
 * volume brings the container that writes to it, and backing up a database
 * connection brings its native dump rather than a copy of its files.
 */
function JobSaveReview({ current, draft }: { current: BackupJob; draft: Record<string, unknown> }) {
  const fields = [
    ["name", "Name"],
    ["sources", "Sources"],
    ["excludes", "Excludes"],
    ["targetKind", "Destination kind"],
    ["target", "Destination settings"],
    ["schedule", "Schedule"],
    ["retention", "Archives to keep"],
    ["retentionDays", "Retention days"],
    ["enabled", "Schedule enabled"],
    ["sqlitePaths", "SQLite snapshots"],
    ["databaseDumps", "Database dumps"],
    ["pauseContainers", "Containers to pause"],
    ["encrypt", "Encrypt archives"],
    ["recovery", "Recovery check"],
  ] as const
  const shown = (value: unknown, key: string) =>
    JSON.stringify(
      value ?? (key === "recovery" ? null : []),
      (_key, part) =>
        part && typeof part === "object" && !Array.isArray(part)
          ? Object.fromEntries(Object.entries(part).sort(([a], [b]) => a.localeCompare(b)))
          : part,
      2,
    )
  const changes = fields.filter(([key]) => shown(current[key], key) !== shown(draft[key], key))
  if (changes.length === 0) return <p>The saved settings match your draft.</p>
  return (
    <div className="max-h-72 overflow-auto rounded-md border border-hairline">
      <table className="w-full text-left text-xs">
        <caption className="sr-only">Differences from the current saved backup job</caption>
        <thead>
          <tr className="border-b border-hairline">
            <th className="p-2">Setting</th>
            <th className="p-2">Current saved value</th>
            <th className="p-2">Your draft</th>
          </tr>
        </thead>
        <tbody>
          {changes.map(([key, label]) => (
            <tr key={key} className="border-b border-hairline last:border-b-0">
              <th className="p-2 align-top font-medium">{label}</th>
              <td className="p-2 align-top">
                <pre className="break-all whitespace-pre-wrap">{shown(current[key], key)}</pre>
              </td>
              <td className="p-2 align-top">
                <pre className="break-all whitespace-pre-wrap">{shown(draft[key], key)}</pre>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

function ResourcePicker({
  resources,
  picked,
  onToggle,
}: {
  resources: BackupResource[]
  picked: (res: BackupResource) => boolean
  onToggle: (res: BackupResource, on: boolean) => void
}) {
  const [open, setOpen] = useState(false)
  const [filter, setFilter] = useState("")
  const visible = useMemo(() => {
    const needle = filter.trim().toLowerCase()
    return resources.filter(
      (r) =>
        !needle ||
        r.name.toLowerCase().includes(needle) ||
        (r.detail ?? "").toLowerCase().includes(needle) ||
        (r.paths ?? []).some((p) => p.toLowerCase().includes(needle)),
    )
  }, [resources, filter])
  const count = resources.filter(picked).length

  if (resources.length === 0) return null
  return (
    <div className="space-y-2">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <Button size="xs" variant="outline" aria-expanded={open} onClick={() => setOpen((v) => !v)}>
          {open ? "Hide what this server has" : "Pick from what this server has"}
        </Button>
        {count > 0 && (
          <span className="numeric text-hint text-muted-foreground">{count} picked</span>
        )}
      </div>
      {open && (
        <Group className="animate-rise space-y-2">
          <SearchInput
            dense
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            placeholder="Filter by name or path"
            aria-label="Filter resources"
            containerClassName="sm:w-full"
          />
          <ul className="-mx-1 max-h-64 divide-y divide-hairline overflow-y-auto px-1">
            {visible.map((res) => (
              <li key={`${res.kind}:${res.id}`}>
                <Label className="flex min-h-9 items-center gap-2.5 py-1.5 text-xs font-normal">
                  <Checkbox
                    checked={picked(res)}
                    onCheckedChange={(checked) => onToggle(res, checked === true)}
                    aria-label={`Back up ${res.name}`}
                  />
                  <span className="min-w-0 flex-1">
                    <span className="flex min-w-0 items-center gap-2">
                      <span className="truncate font-medium">{res.name}</span>
                      <Tag>{RESOURCE_KIND_LABEL[res.kind]}</Tag>
                    </span>
                    <span className="block truncate font-mono text-hint text-muted-foreground">
                      {res.paths?.[0] ?? res.detail}
                    </span>
                  </span>
                  {res.protected && (
                    <span className="shrink-0 text-hint text-muted-foreground">
                      already backed up
                    </span>
                  )}
                </Label>
              </li>
            ))}
            {visible.length === 0 && (
              <li className="py-3 text-center text-hint text-muted-foreground">Nothing matches.</li>
            )}
          </ul>
        </Group>
      )}
    </div>
  )
}
