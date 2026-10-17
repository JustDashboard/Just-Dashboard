"use client"

import { useId, useState } from "react"
import { Copy } from "@/components/icons"
import { ApiError, errorMessage, post } from "@/lib/api"
import type { Job } from "@/lib/types"
import { Field, FormFact, OptionList, OptionRow } from "@/components/form"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { newDatabaseProblem } from "@/components/database/ops/backups-model"
import type { DbCopyRequest, DbDumpSupport } from "@/components/database/ops/backups-types"
import { TaskDialog, databaseSubject } from "@/components/database/ops/settings-dialog"
import { useDatabase } from "@/components/database/shell/database-context"
import { useDatabases } from "@/components/database/shell/databases-context"

/** The value of the server field that means the connection's own. */
const THIS_SERVER = "this"

/**
 * Copy this database into a new one: on the same server, as a dump and a
 * restore of it as one job with nothing kept afterwards but the copy, or on
 * another saved connection of the same engine, where the dump goes from one
 * server to the other as it is read. The database copied is not touched.
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
  const { connections } = useDatabases()
  const field = useId()
  const numbered = options.databases
  const [name, setName] = useState("")
  const [structureOnly, setStructureOnly] = useState(false)
  const [server, setServer] = useState(THIS_SERVER)
  const [busy, setBusy] = useState(false)
  const [refusal, setRefusal] = useState<string>()
  // Another server of the same engine that will take a database: not a
  // connection the dashboard cannot open, and not a protected one.
  const others = connections.filter(
    (other) => other.id !== id && other.driver === conn.driver && !other.broken && !other.readOnly,
  )
  const into = others.find((other) => String(other.id) === server)
  const problem = newDatabaseProblem(name, {
    numbered,
    current: (into ?? conn).database,
  })

  const run = async () => {
    const database = name.trim()
    setBusy(true)
    setRefusal(undefined)
    const body: DbCopyRequest = {
      name: database,
      ...(structureOnly ? { structureOnly: true } : {}),
      ...(into ? { to: into.id } : {}),
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
      description={`Copy ${conn.name} into a new database on ${into ? into.name : "the same server"}`}
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
      dirty={name.trim() !== "" || structureOnly || Boolean(into)}
      busy={busy}
      refusal={refusal}
      note={
        into
          ? `Read from ${conn.name} and loaded into ${into.name} as it is read. Nothing here is changed.`
          : "A dump and a restore as one job. Nothing here is changed."
      }
      command="Copy"
      commandIcon={Copy}
      disabled={Boolean(problem)}
      onRun={() => void run()}
      onClose={onClose}
    >
      {others.length > 0 && (
        <Field
          label="Copy into"
          htmlFor={`${field}-server`}
          hint="A server saved here that runs the same engine. The database is made on it."
        >
          <Select value={server} onValueChange={setServer}>
            <SelectTrigger id={`${field}-server`} className="w-full text-xs">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value={THIS_SERVER} className="text-xs">
                This server ({conn.host}
                {conn.port ? `:${conn.port}` : ""})
              </SelectItem>
              {others.map((other) => (
                <SelectItem key={other.id} value={String(other.id)} className="text-xs">
                  {other.name} ({other.host}
                  {other.port ? `:${other.port}` : ""})
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>
      )}
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
