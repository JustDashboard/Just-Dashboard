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
  restoreReach,
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
  const contents = holdsWords(file, engine.nouns, engine.can("dumpDatabases"))
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
 * On a server that numbers its databases a dump goes back to the numbers it
 * was taken from, whichever one this connection is on. A dump that reaches a
 * number that is not the connection's own is therefore never the target the
 * dialog opens on: the reader presses it, having read which numbers it writes.
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
  const reach = restoreReach(file, conn.database)
  // The dump writes into a number that is not this connection's, or does not say which it holds.
  const wide = numbered && (reach.others.length > 0 || reach.numbers.length === 0)
  // Only a dump of one numbered database can be loaded into another number.
  const canNew =
    options.newDatabase &&
    engine.can("restoreNewDatabase") &&
    can("system.admin") &&
    (!numbered || reach.numbers.length === 1)
  const [target, setTarget] = useState<Target | undefined>(wide ? undefined : "this")
  const [name, setName] = useState("")
  const [dumpFirst, setDumpFirst] = useState(true)
  const [busy, setBusy] = useState(false)
  const [refusal, setRefusal] = useState<string>()

  const own = Number(conn.database || "0")
  const holdsOwn = reach.numbers.some((one) => Number(one) === own)
  const here = numbered ? reach.here : conn.database || conn.name
  const hold = numbered && reach.several ? "hold" : "holds"
  const beyond =
    numbered && reach.others.length > 0
      ? `${reach.others.length === 1 ? "db" : "databases"} ${reach.others.join(", ")}`
      : ""
  // What a reader has to know before a dump goes anywhere but this connection's own number.
  const reachWords = !beyond
    ? "A restore writes every key back to the number it came from, whichever those are."
    : holdsOwn
      ? `It also holds ${beyond}, and a restore writes every key back to the number it came from: it cannot be held to one of them. For db ${own} alone, take a dump of only that database and restore that.`
      : `It holds ${beyond}, and a restore writes every key back to the number it came from.`
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
            Replaces what <b>{here}</b> {hold} with the dump taken {relativeTime(file.takenAt)}.{" "}
            {restoreEffect(file)}
          </p>
          {beyond && (
            <p>
              <b>
                It writes into {beyond}, which {reach.others.length === 1 ? "is" : "are"} not the
                database this connection is on.
              </b>{" "}
              Whatever else uses those numbers gets the dump&rsquo;s keys back.
            </p>
          )}
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
      dirty={name.trim() !== "" || target !== (wide ? undefined : "this") || !dumpFirst}
      busy={busy}
      refusal={refusal}
      note="It runs on the server; this page shows it."
      command={target === "new" ? "Restore into it" : "Restore…"}
      commandIcon={RotateCounterClockwise}
      destructive={target === "this"}
      disabled={target === undefined || Boolean(problem)}
      onRun={() => void run()}
      onClose={onClose}
    >
      {canNew || wide ? (
        <ChoiceGrid columns={2} role="group" aria-label="Where the dump is restored">
          <ChoiceCard selected={target === "this"} onClick={() => setTarget("this")}>
            <ChoiceCardTitle>
              {numbered ? "Back where it came from" : "This database"}
            </ChoiceCardTitle>
            <ChoiceCardHint>
              <span className="font-mono text-foreground">{here}</span> — what{" "}
              {numbered && reach.several ? "they hold" : "it holds"} now is replaced.
            </ChoiceCardHint>
          </ChoiceCard>
          {canNew && (
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
          )}
        </ChoiceGrid>
      ) : (
        <p className="text-body">
          Restores into <span className="font-mono">{here}</span>, replacing what it holds now.
        </p>
      )}

      {wide && target !== "this" && (
        <Notice
          tone="warning"
          icon={Warning}
          title={
            !beyond
              ? "This dump does not say which numbered databases it holds"
              : holdsOwn
                ? `This dump reaches past db ${own}, the database this connection is on`
                : `This dump is not of db ${own}, the database this connection is on`
          }
        >
          {reachWords}
          {target === undefined && " Press where it goes to go on."}
        </Notice>
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
      ) : target === "this" ? (
        <>
          <Notice tone="warning" icon={Warning} title={`What ${here} ${hold} now is replaced`}>
            {wide && `${reachWords} `}
            {restoreEffect(file)}
          </Notice>
          <OptionList>
            <OptionRow
              title={`Take a dump of ${numbered ? (reach.several ? "them as they are" : "it as it is") : `${here} as it is`} now, first`}
              hint="Kept with the other dumps as a safety dump. If it cannot be taken, nothing is restored."
              checked={dumpFirst}
              onCheckedChange={setDumpFirst}
            />
          </OptionList>
        </>
      ) : null}
    </TaskDialog>
  )
}
