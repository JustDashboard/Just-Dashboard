"use client"

import { useId, useState } from "react"
import { Copy } from "@/components/icons"
import { ApiError, errorMessage, post } from "@/lib/api"
import type { Job } from "@/lib/types"
import { Field, FormFact, OptionList, OptionRow } from "@/components/form"
import { Input } from "@/components/ui/input"
import { newDatabaseProblem } from "@/components/database/ops/backups-model"
import type { DbCopyRequest, DbDumpSupport } from "@/components/database/ops/backups-types"
import { TaskDialog, databaseSubject } from "@/components/database/ops/settings-dialog"
import { useDatabase } from "@/components/database/shell/database-context"

/**
 * Copy this database into a new one on the same server: a dump and a restore
 * of it as one job, with nothing kept afterwards but the copy. The database
 * copied is not touched.
 */
export function CopyDatabase({
  options,
  onStarted,
  onRunning,
  onClose,
}: {
  options: DbDumpSupport
  onStarted: (job: Job, into: { newDatabase: string }) => void
  onRunning: (jobId: string) => void
  onClose: () => void
}) {
  const { id, conn, engine } = useDatabase()
  const field = useId()
  const numbered = options.databases
  const [name, setName] = useState("")
  const [structureOnly, setStructureOnly] = useState(false)
  const [busy, setBusy] = useState(false)
  const [refusal, setRefusal] = useState<string>()
  const problem = newDatabaseProblem(name, { numbered, current: conn.database })

  const run = async () => {
    const database = name.trim()
    setBusy(true)
    setRefusal(undefined)
    const body: DbCopyRequest = {
      name: database,
      ...(structureOnly ? { structureOnly: true } : {}),
    }
    try {
      onStarted(await post<Job>(`/databases/${id}/copy`, body), { newDatabase: database })
    } catch (err) {
      if (err instanceof ApiError && err.code === "transfer_running" && err.resource) {
        onRunning(err.resource)
        return
      }
      setRefusal(errorMessage(err))
      setBusy(false)
    }
  }

  return (
    <TaskDialog
      title="Copy this database"
      description={`Copy ${conn.name} into a new database on the same server`}
      subject={databaseSubject(
        conn,
        engine,
        <>
          <FormFact label={engine.databaseField} mono>
            {conn.database || "0"}
          </FormFact>
          <FormFact label="Server" mono>
            {conn.host}
            {conn.port ? `:${conn.port}` : ""}
          </FormFact>
        </>,
      )}
      dirty={name.trim() !== "" || structureOnly}
      busy={busy}
      refusal={refusal}
      note="A dump and a restore as one job. Nothing here is changed."
      command="Copy"
      commandIcon={Copy}
      disabled={Boolean(problem)}
      onRun={() => void run()}
      onClose={onClose}
    >
      <Field
        label={numbered ? "Database number to copy into" : "Name of the copy"}
        htmlFor={field}
        hint={
          numbered
            ? "A numbered database that holds no key. The copy fails, and changes nothing, if it does."
            : "Letters, digits and underscores. The copy fails, and changes nothing, if a database of that name exists."
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
      {engine.can("copyStructureOnly") && (
        <OptionList>
          <OptionRow
            title={`Copy the structure only, with no ${engine.nouns.rows}`}
            checked={structureOnly}
            onCheckedChange={setStructureOnly}
          />
        </OptionList>
      )}
    </TaskDialog>
  )
}
