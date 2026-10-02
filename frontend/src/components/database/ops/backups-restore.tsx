"use client"

import { useId, useState } from "react"
import { RotateCounterClockwise, Warning } from "@/components/icons"
import { ApiError, errorMessage, post } from "@/lib/api"
import { bytes, relativeTime } from "@/lib/format"
import type { Job } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import { ChoiceCard, ChoiceCardHint, ChoiceCardTitle, ChoiceGrid } from "@/components/choice-card"
import type { ConfirmRequest } from "@/components/confirm-dialog"
import { Field, FormFact, OptionList, OptionRow } from "@/components/form"
import { Notice } from "@/components/state"
import { Input } from "@/components/ui/input"
import { EngineMark } from "@/components/database/kit"
import {
  holdsWords,
  newDatabaseProblem,
  originWord,
  restoreEffect,
} from "@/components/database/ops/backups-model"
import type {
  DbBackupFile,
  DbDumpSupport,
  DbRestoreRequest,
} from "@/components/database/ops/backups-types"
import { TaskDialog } from "@/components/database/ops/settings-dialog"
import { useDatabase } from "@/components/database/shell/database-context"

type Target = "this" | "new"

/** What a dump is, as the facts under its name. */
export function DumpFacts({ file }: { file: DbBackupFile }) {
  const { engine } = useDatabase()
  const contents = holdsWords(file, engine.nouns)
  return (
    <>
      <FormFact label="Taken">{relativeTime(file.takenAt)}</FormFact>
      <FormFact label="Size">{bytes(file.size)}</FormFact>
      <FormFact label="Written by">{file.tool ?? file.format}</FormFact>
      {contents && <FormFact label="Holds">{contents}</FormFact>}
    </>
  )
}

/**
 * Restore one dump: where it goes, and what happens to what is there.
 *
 * The target is the first thing said. Over this database, the dump replaces
 * what the database holds — so a dump of the database as it is now is taken
 * first unless the reader says otherwise, and the restore is confirmed with
 * the database named before anything runs. Into a new database nothing that
 * exists is touched, and the command runs as it stands.
 *
 * The restore itself is a job on the server; the dialog only begins it.
 */
export function RestoreDump({
  file,
  options,
  confirm,
  onStarted,
  onRunning,
  onClose,
}: {
  file: DbBackupFile
  options: DbDumpSupport
  confirm: (request: ConfirmRequest) => void
  onStarted: (job: Job, into: { newDatabase?: string }) => void
  onRunning: (jobId: string) => void
  onClose: () => void
}) {
  const { id, conn, engine } = useDatabase()
  const { can } = useAuth()
  const field = useId()
  // A server that numbers its databases puts every key back where it came from.
  const numbered = options.databases
  const canNew = options.newDatabase && engine.can("restoreNewDatabase") && can("system.admin")
  const [target, setTarget] = useState<Target>("this")
  const [name, setName] = useState("")
  const [dumpFirst, setDumpFirst] = useState(true)
  const [busy, setBusy] = useState(false)
  const [refusal, setRefusal] = useState<string>()

  // A dump of several numbered databases goes back to each of them.
  const numbers = (file.database ?? "").split(",").filter(Boolean)
  const here = numbered
    ? numbers.length > 1
      ? `databases ${numbers.join(", ")}`
      : numbers.length === 1
        ? `db ${numbers[0]}`
        : "each key's own numbered database"
    : conn.database || conn.name
  const problem =
    target === "new" ? newDatabaseProblem(name, { numbered, current: conn.database }) : undefined

  const send = async (body: DbRestoreRequest, into: { newDatabase?: string }) => {
    try {
      onStarted(await post<Job>(`/databases/${id}/restore`, body), into)
    } catch (err) {
      if (err instanceof ApiError && err.code === "transfer_running" && err.resource) {
        onRunning(err.resource)
        return
      }
      throw err
    }
  }

  const run = async () => {
    if (target === "new") {
      const database = name.trim()
      setBusy(true)
      setRefusal(undefined)
      try {
        await send(
          { file: file.file, target: { newDatabase: database } },
          { newDatabase: database },
        )
      } catch (err) {
        setRefusal(errorMessage(err))
        setBusy(false)
      }
      return
    }
    // Over what is there: asked once more, with the database named.
    onClose()
    confirm({
      title: `Restore over ${here}`,
      confirmLabel: "Restore",
      subject: {
        mark: <EngineMark engine={engine} size="sm" />,
        name: conn.name,
        facts: (
          <>
            <FormFact label="Into" mono>
              {here}
            </FormFact>
            <FormFact label="From" mono>
              {file.file}
            </FormFact>
          </>
        ),
      },
      description: (
        <>
          <p>
            Replaces what <b>{here}</b> holds with the dump taken {relativeTime(file.takenAt)}.{" "}
            {restoreEffect(file)}
          </p>
          <p>
            {dumpFirst
              ? "A dump of the database as it is now is taken first and kept with the others; if that dump fails, nothing is restored."
              : "No dump of the database as it is now is taken first: what it holds now is gone once this runs."}
          </p>
        </>
      ),
      action: async () => {
        await send({ file: file.file, target: "this", dumpFirst }, {})
        return "reported"
      },
    })
  }

  return (
    <TaskDialog
      title="Restore a dump"
      description={`Restore ${file.file} into ${conn.name} or into a new database`}
      size="lg"
      subject={{
        mark: <EngineMark engine={engine} size="sm" />,
        name: <span className="font-mono">{file.file}</span>,
        facts: (
          <>
            <DumpFacts file={file} />
            {originWord(file.origin) && (
              <FormFact label="Origin">{originWord(file.origin)}</FormFact>
            )}
          </>
        ),
      }}
      dirty={name.trim() !== "" || target !== "this" || !dumpFirst}
      busy={busy}
      refusal={refusal}
      note="It runs on the server; this page shows it."
      command={target === "new" ? "Restore into it" : "Restore…"}
      commandIcon={RotateCounterClockwise}
      destructive={target === "this"}
      disabled={Boolean(problem)}
      onRun={() => void run()}
      onClose={onClose}
    >
      {canNew ? (
        <ChoiceGrid columns={2} role="group" aria-label="Where the dump is restored">
          <ChoiceCard selected={target === "this"} onClick={() => setTarget("this")}>
            <ChoiceCardTitle>
              {numbered ? "Back where it came from" : "This database"}
            </ChoiceCardTitle>
            <ChoiceCardHint>
              <span className="font-mono text-foreground">{here}</span> — what it holds now is
              replaced.
            </ChoiceCardHint>
          </ChoiceCard>
          <ChoiceCard selected={target === "new"} onClick={() => setTarget("new")}>
            <ChoiceCardTitle>
              {numbered ? "Another numbered database" : "A new database"}
            </ChoiceCardTitle>
            <ChoiceCardHint>
              {numbered
                ? "One that holds no key yet. Nothing that exists is touched."
                : "Created on the same server. Nothing that exists is touched."}
            </ChoiceCardHint>
          </ChoiceCard>
        </ChoiceGrid>
      ) : (
        <p className="text-body">
          Restores into <span className="font-mono">{here}</span>, replacing what it holds now.
        </p>
      )}

      {target === "new" ? (
        <Field
          label={numbered ? "Database number" : "Name of the new database"}
          htmlFor={field}
          hint={
            numbered
              ? "The restore fails, and changes nothing, if that database already holds keys."
              : "Letters, digits and underscores. The restore fails, and changes nothing, if a database of that name exists."
          }
          error={name.trim() ? problem : undefined}
        >
          <Input
            id={field}
            value={name}
            onChange={(event) => setName(event.target.value)}
            inputMode={numbered ? "numeric" : undefined}
            autoComplete="off"
            spellCheck={false}
            className="font-mono"
          />
        </Field>
      ) : (
        <>
          <Notice tone="warning" icon={Warning} title={`What ${here} holds now is replaced`}>
            {restoreEffect(file)}
          </Notice>
          <OptionList>
            <OptionRow
              title={`Take a dump of ${numbered ? "it" : here} as it is now, first`}
              hint="Kept with the other dumps as a safety dump. If it cannot be taken, nothing is restored."
              checked={dumpFirst}
              onCheckedChange={setDumpFirst}
            />
          </OptionList>
        </>
      )}
    </TaskDialog>
  )
}
