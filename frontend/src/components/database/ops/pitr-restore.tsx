"use client"

import { useEffect, useId, useState } from "react"
import { ClockRewind, Information, Warning } from "@/components/icons"
import { ApiError, errorMessage, post } from "@/lib/api"
import type { Job } from "@/lib/types"
import { ChoiceCard, ChoiceCardHint, ChoiceCardTitle, ChoiceGrid } from "@/components/choice-card"
import { Field, FormFact, FormFacts, Statement } from "@/components/form"
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
  RESTORE_POINT_RULE,
  fromLocalInput,
  momentWords,
  pastTheEnd,
  settingsText,
  targetProblem,
  toLocalInput,
  windowWords,
} from "@/components/database/ops/pitr-model"
import type {
  PitrArchive,
  PitrRestorePlan,
  PitrRestorePoint,
  PitrRestoreRequest,
  RecoveryTarget,
  WalWindow,
} from "@/components/database/ops/pitr-types"
import { TaskDialog, databaseSubject } from "@/components/database/ops/settings-dialog"
import { useDatabase } from "@/components/database/shell/database-context"

type Kind = RecoveryTarget["kind"]

/**
 * Recover a server to a moment: the latest one, a moment to the second, or
 * a restore point named in its log. The copy is a new server beside the one
 * it is recovered from, which is never touched: its own container, its own
 * volume and its own connection, on a port of its own on this server.
 *
 * What the recovery will do is the server's answer, read as the target
 * changes: which base backup it starts from, where the copy will run, and
 * the settings it recovers with.
 */
export function RestorePitr({
  archive,
  covers,
  moment: initialMoment,
  points,
  onStarted,
  onRunning,
  onClose,
}: {
  archive: PitrArchive
  /** What the archive covers. */
  covers: WalWindow
  /** A moment to open on, handed over from elsewhere — a release's recorded data point. */
  moment?: string
  points: PitrRestorePoint[]
  onStarted: (job: Job) => void
  onRunning: (jobId: string) => void
  onClose: () => void
}) {
  const { id, conn, engine } = useDatabase()
  const ids = useId()
  const [kind, setKind] = useState<Kind>(initialMoment ? "time" : "latest")
  const [moment, setMoment] = useState(() => toLocalInput(initialMoment ?? covers.to))
  const [point, setPoint] = useState(points[0]?.name ?? "")
  const [plan, setPlan] = useState<{ plan?: PitrRestorePlan; error?: string }>({})
  const [busy, setBusy] = useState(false)
  const [refusal, setRefusal] = useState<string>()

  const target: RecoveryTarget =
    kind === "time"
      ? { kind, time: fromLocalInput(moment) }
      : kind === "name"
        ? { kind, name: point.trim() }
        : { kind }
  const problem = targetProblem(target, covers)
  const request: PitrRestoreRequest = { archiveId: archive.id, target }
  const body = JSON.stringify(request)

  useEffect(() => {
    if (problem) return
    const controller = new AbortController()
    const timer = window.setTimeout(() => {
      post<PitrRestorePlan>(
        `/databases/${id}/pitr/restore`,
        { ...JSON.parse(body), preview: true },
        { signal: controller.signal },
      )
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
      onStarted(await post<Job>(`/databases/${id}/pitr/restore`, request))
    } catch (err) {
      if (err instanceof ApiError && err.code === "pitr_running" && err.resource) {
        onRunning(err.resource)
        return
      }
      setRefusal(errorMessage(err))
      setBusy(false)
    }
  }

  return (
    <TaskDialog
      title="Recover to a moment"
      description={`Recover ${archive.container} into a new server, to the moment chosen here`}
      size="lg"
      subject={databaseSubject(
        conn,
        engine,
        <>
          <FormFact label="Server" mono>
            {archive.container}
          </FormFact>
          <FormFact label="Archive">{windowWords(covers)}</FormFact>
        </>,
      )}
      dirty={kind !== "latest"}
      busy={busy}
      refusal={refusal}
      note="It runs on the server; this page shows it."
      command="Recover into a new server"
      commandIcon={ClockRewind}
      disabled={Boolean(problem)}
      onRun={() => void run()}
      onClose={onClose}
    >
      <ChoiceGrid columns={3} role="group" aria-label="What the copy is recovered to">
        <ChoiceCard selected={kind === "latest"} onClick={() => setKind("latest")}>
          <ChoiceCardTitle>The latest moment</ChoiceCardTitle>
          <ChoiceCardHint>
            Everything the archive holds, up to {momentWords(covers.to)}.
          </ChoiceCardHint>
        </ChoiceCard>
        <ChoiceCard selected={kind === "time"} onClick={() => setKind("time")}>
          <ChoiceCardTitle>A moment</ChoiceCardTitle>
          <ChoiceCardHint>
            To the second: what was committed by then, and nothing after.
          </ChoiceCardHint>
        </ChoiceCard>
        <ChoiceCard selected={kind === "name"} onClick={() => setKind("name")}>
          <ChoiceCardTitle>A restore point</ChoiceCardTitle>
          <ChoiceCardHint>A moment named in the log before a migration or a change.</ChoiceCardHint>
        </ChoiceCard>
      </ChoiceGrid>

      {kind === "time" && (
        <Field
          label="Moment, in this browser's time"
          htmlFor={`${ids}-moment`}
          hint={windowWords(covers)}
          error={moment ? problem : undefined}
        >
          <Input
            id={`${ids}-moment`}
            type="datetime-local"
            step={1}
            min={toLocalInput(covers.from)}
            value={moment}
            onChange={(event) => setMoment(event.target.value)}
            className="w-64 font-mono"
          />
        </Field>
      )}
      {kind === "time" && pastTheEnd(target, covers) && (
        <Notice tone="default" icon={Information} title="That is after the newest archived segment">
          Nothing later than {momentWords(covers.to)} is archived yet, so the copy is recovered to
          the end of the archive, which is the same state.
        </Notice>
      )}

      {kind === "name" &&
        (points.length > 0 ? (
          <Field label="Restore point" htmlFor={`${ids}-point`}>
            <Select value={point} onValueChange={setPoint}>
              <SelectTrigger id={`${ids}-point`} size="sm" className="w-full">
                <SelectValue placeholder="Choose a restore point" />
              </SelectTrigger>
              <SelectContent>
                {points.map((one) => (
                  <SelectItem key={one.id} value={one.name}>
                    {one.name} · {momentWords(one.createdAt)}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </Field>
        ) : (
          <Field
            label="Restore point's name"
            htmlFor={`${ids}-point`}
            hint={`No restore point was marked from this page; name one made with pg_create_restore_point. ${RESTORE_POINT_RULE}`}
            error={point ? problem : undefined}
          >
            <Input
              id={`${ids}-point`}
              value={point}
              onChange={(event) => setPoint(event.target.value)}
              spellCheck={false}
              autoComplete="off"
              className="font-mono"
            />
          </Field>
        ))}

      <Notice tone="default" icon={Information} title={`${archive.container} is not touched`}>
        The copy is a new server with its own container, volume and connection. Once it has
        recovered, keep both, or switch the copy in for {archive.container}.
      </Notice>
      {plan.plan && (
        <FormFacts>
          <FormFact label="Starts from">
            <span className="font-mono">{plan.plan.backup}</span> ·{" "}
            {momentWords(plan.plan.backupAt)}
          </FormFact>
          <FormFact label="Runs as" mono>
            {plan.plan.container} · 127.0.0.1:{plan.plan.port}
          </FormFact>
        </FormFacts>
      )}
      {plan.error && !problem && (
        <Notice tone="warning" icon={Warning} title="The recovery could not be planned">
          {plan.error}
        </Notice>
      )}
      <Statement
        label="The copy recovers with"
        sql={plan.plan ? settingsText(plan.plan.settings) : ""}
        placeholder={problem ?? "Reading what the copy will be started with…"}
      />
    </TaskDialog>
  )
}
