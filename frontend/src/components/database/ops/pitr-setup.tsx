"use client"

import { useEffect, useId, useState } from "react"
import { ClockRewind, Warning } from "@/components/icons"
import { ApiError, errorMessage, post } from "@/lib/api"
import type { BackupJob, Job } from "@/lib/types"
import { targetLabel } from "@/components/backups/shared"
import { Segments } from "@/components/deploy/settings/segments"
import { Field, FieldRow, FormFact, Statement } from "@/components/form"
import { ScheduleBuilder, scheduleExpression, scheduleFields } from "@/components/schedule-builder"
import { Notice } from "@/components/state"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import {
  PLAN_AFTER_MS,
  commandLine,
  setupProblem,
  type SetupForm,
} from "@/components/database/ops/pitr-model"
import type { DbPitr, PitrPlan, PitrSetupRequest } from "@/components/database/ops/pitr-types"
import { TaskDialog, databaseSubject } from "@/components/database/ops/settings-dialog"
import { useDatabase } from "@/components/database/shell/database-context"

/**
 * Set point-in-time recovery up for a server: where its log and base backups
 * go, when a base backup is taken, and how many days of moments are kept.
 *
 * The destinations are a backup job's: a directory on this server, an S3
 * bucket or a Backblaze B2 one — or the destination of a backup job that
 * already exists, keys and all, so a bucket's keys are not typed twice. The
 * server is restarted once to start archiving, and the command line it will
 * be started with is shown as the server answers it, before anything runs.
 *
 * Setting it up is a job on the server: the dialog only begins it.
 */
export function SetupPitr({
  pitr,
  jobs,
  onStarted,
  onRunning,
  onClose,
}: {
  pitr: DbPitr
  /** The backup jobs, whose destinations can be reused. */
  jobs: BackupJob[]
  onStarted: (job: Job) => void
  /** Another job is running for the server: its id. */
  onRunning: (jobId: string) => void
  onClose: () => void
}) {
  const { id, conn, engine } = useDatabase()
  const ids = useId()
  const [form, setForm] = useState<SetupForm>({
    source: "local",
    path: pitr.defaults.directory,
    bucket: "",
    endpoint: "",
    accessKey: "",
    secretKey: "",
    retentionDays: String(pitr.defaults.retentionDays),
  })
  const [region, setRegion] = useState("")
  const [prefix, setPrefix] = useState("")
  const [schedule, setSchedule] = useState(() => scheduleFields(pitr.defaults.schedule))
  const [plan, setPlan] = useState<{ plan?: PitrPlan; error?: string }>({})
  const [busy, setBusy] = useState(false)
  const [refusal, setRefusal] = useState<string>()
  const set = (patch: Partial<SetupForm>) => setForm((held) => ({ ...held, ...patch }))
  const problem = setupProblem(form)

  const request = (preview: boolean): PitrSetupRequest => {
    const base = {
      schedule: scheduleExpression(schedule),
      retentionDays: Number(form.retentionDays),
      preview: preview || undefined,
    }
    if (form.source === "job") return { ...base, fromJob: form.fromJob }
    if (form.source === "local") {
      return { ...base, targetKind: "local", target: { path: form.path.trim() } }
    }
    return {
      ...base,
      targetKind: form.source,
      target: {
        bucket: form.bucket.trim(),
        region: region.trim(),
        endpoint: form.endpoint.trim(),
        prefix: prefix.trim(),
      },
      secrets: form.accessKey.trim()
        ? { accessKeyId: form.accessKey.trim(), secretAccessKey: form.secretKey }
        : undefined,
    }
  }
  const body = JSON.stringify(request(true))

  // What the server will be started with is the server's answer, asked again
  // when what decides it changes — and not on every keystroke.
  useEffect(() => {
    if (problem) return
    const controller = new AbortController()
    const timer = window.setTimeout(() => {
      post<PitrPlan>(`/databases/${id}/pitr`, JSON.parse(body), { signal: controller.signal })
        .then((answer) => setPlan({ plan: answer }))
        .catch((err) => {
          if (!controller.signal.aborted) setPlan({ error: errorMessage(err) })
        })
    }, PLAN_AFTER_MS)
    return () => {
      controller.abort()
      window.clearTimeout(timer)
    }
  }, [id, body, problem])

  const run = async () => {
    setBusy(true)
    setRefusal(undefined)
    try {
      onStarted(await post<Job>(`/databases/${id}/pitr`, request(false)))
    } catch (err) {
      if (err instanceof ApiError && err.code === "pitr_running" && err.resource) {
        onRunning(err.resource)
        return
      }
      setRefusal(errorMessage(err))
      setBusy(false)
    }
  }

  const dirty =
    form.source !== "local" ||
    form.path !== pitr.defaults.directory ||
    form.retentionDays !== String(pitr.defaults.retentionDays) ||
    scheduleExpression(schedule) !== pitr.defaults.schedule
  const usable = jobs.filter((job) => job.targetKind)
  return (
    <TaskDialog
      title="Set up point-in-time recovery"
      description={`Archive the write-ahead log of ${pitr.container ?? conn.name} so it can be recovered to any moment`}
      size="lg"
      subject={databaseSubject(
        conn,
        engine,
        <>
          <FormFact label="Server" mono>
            {pitr.container}
          </FormFact>
        </>,
      )}
      dirty={dirty}
      busy={busy}
      refusal={refusal}
      note="It runs on the server; this page shows it."
      command="Set up and restart"
      commandIcon={ClockRewind}
      destructive
      disabled={Boolean(problem)}
      onRun={() => void run()}
      onClose={onClose}
    >
      <Field label="Where the log and base backups go">
        <Segments
          label="Destination"
          value={form.source}
          onChange={(source) => set({ source })}
          options={[
            { value: "local", label: "A directory" },
            { value: "s3", label: "S3" },
            { value: "b2", label: "Backblaze B2" },
            ...(usable.length > 0 ? [{ value: "job" as const, label: "A backup job's" }] : []),
          ]}
        />
      </Field>

      {form.source === "local" && (
        <Field
          label="Directory on this server"
          htmlFor={`${ids}-path`}
          hint="Created if it does not exist; each server's archive is a folder of its own inside it. A copy on the same disk is not a disaster plan."
          error={form.path && problem?.includes("path") ? problem : undefined}
        >
          <Input
            id={`${ids}-path`}
            value={form.path}
            onChange={(event) => set({ path: event.target.value })}
            spellCheck={false}
            className="font-mono text-body"
          />
        </Field>
      )}

      {(form.source === "s3" || form.source === "b2") && (
        <>
          <FieldRow>
            <Field label="Bucket" htmlFor={`${ids}-bucket`}>
              <Input
                id={`${ids}-bucket`}
                value={form.bucket}
                onChange={(event) => set({ bucket: event.target.value })}
                spellCheck={false}
              />
            </Field>
            <Field label="Region" htmlFor={`${ids}-region`}>
              <Input
                id={`${ids}-region`}
                value={region}
                onChange={(event) => setRegion(event.target.value)}
                placeholder="us-east-1"
                spellCheck={false}
              />
            </Field>
          </FieldRow>
          <FieldRow>
            <Field
              label="Endpoint"
              htmlFor={`${ids}-endpoint`}
              hint={form.source === "b2" ? "From the bucket's details page." : "Blank for AWS."}
            >
              <Input
                id={`${ids}-endpoint`}
                value={form.endpoint}
                onChange={(event) => set({ endpoint: event.target.value })}
                placeholder={form.source === "b2" ? "s3.us-west-004.backblazeb2.com" : ""}
                spellCheck={false}
              />
            </Field>
            <Field label="Prefix" htmlFor={`${ids}-prefix`} hint="A folder inside the bucket.">
              <Input
                id={`${ids}-prefix`}
                value={prefix}
                onChange={(event) => setPrefix(event.target.value)}
                spellCheck={false}
              />
            </Field>
          </FieldRow>
          <FieldRow>
            <Field label="Access key ID" htmlFor={`${ids}-access`}>
              <Input
                id={`${ids}-access`}
                value={form.accessKey}
                onChange={(event) => set({ accessKey: event.target.value })}
                autoComplete="off"
                spellCheck={false}
              />
            </Field>
            <Field
              label="Secret access key"
              htmlFor={`${ids}-secret`}
              hint="Sealed with the dashboard's master key and never shown again."
            >
              <Input
                id={`${ids}-secret`}
                type="password"
                value={form.secretKey}
                onChange={(event) => set({ secretKey: event.target.value })}
                autoComplete="new-password"
              />
            </Field>
          </FieldRow>
        </>
      )}

      {form.source === "job" && (
        <Field
          label="Backup job"
          htmlFor={`${ids}-job`}
          hint="Its destination and keys are used; a directory gets a folder of its own beside the job's archives."
        >
          <Select
            value={form.fromJob ? String(form.fromJob) : ""}
            onValueChange={(value) => set({ fromJob: Number(value) })}
          >
            <SelectTrigger id={`${ids}-job`} size="sm" className="w-full">
              <SelectValue placeholder="Choose a job" />
            </SelectTrigger>
            <SelectContent>
              {usable.map((job) => (
                <SelectItem key={job.id} value={String(job.id)}>
                  {job.name} · {targetLabel(job)}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>
      )}

      <ScheduleBuilder
        fields={schedule}
        onChange={setSchedule}
        idPrefix={`${ids}-schedule`}
        label="Base backups"
        manual
      />
      <Field
        label="Keep"
        htmlFor={`${ids}-days`}
        hint="Days of moments that can be recovered to. Older base backups and log are deleted after each base backup; 0 keeps everything."
        error={problem?.startsWith("Keep") ? problem : undefined}
      >
        <Input
          id={`${ids}-days`}
          value={form.retentionDays}
          onChange={(event) => set({ retentionDays: event.target.value })}
          inputMode="numeric"
          className="w-28"
        />
      </Field>

      <Notice tone="warning" icon={Warning} title={`${pitr.container} restarts once`}>
        It stops for a few seconds and starts again with archiving on; connections in the meantime
        are refused. The first base backup is taken straight after.
      </Notice>
      <Statement
        label="The server starts again with"
        sql={plan.plan ? commandLine(plan.plan.command) : ""}
        placeholder={problem ?? plan.error ?? "Reading what the server will be started with…"}
      />
      {plan.plan && (
        <p className="text-hint text-muted-foreground">
          Mounted: <span className="font-mono wrap-anywhere">{plan.plan.mounts.join(", ")}</span>.
          Archived into <span className="font-mono wrap-anywhere">{plan.plan.location}</span>,
          encrypted to a key made for it.
        </p>
      )}
    </TaskDialog>
  )
}
