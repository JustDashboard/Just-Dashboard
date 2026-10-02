"use client"

import { useEffect, useId, useState } from "react"
import { Pencil, Router } from "@/components/icons"
import { errorMessage, get, post, put } from "@/lib/api"
import { timestamp } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { DbConnection } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import { Segments } from "@/components/deploy/settings/segments"
import {
  Field,
  FieldRow,
  FormFact,
  FormNote,
  FormSection,
  OptionList,
  OptionRow,
} from "@/components/form"
import { Detail, DetailList } from "@/components/page"
import { Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Skeleton } from "@/components/ui/skeleton"
import { Textarea } from "@/components/ui/textarea"
import {
  EnvironmentField,
  environmentProblem,
} from "@/components/database/connect/environment-field"
import { CONNECTION_NAME } from "@/components/database/connect/rules"
import type { DbTestResponse } from "@/components/database/fleet/types"
import { EnvironmentTag, ProtectedTag } from "@/components/database/kit"
import { TaskDialog, databaseSubject } from "@/components/database/ops/settings-dialog"
import { maskedDsn, patchDsn, readDsn } from "@/components/database/ops/settings-dsn"
import {
  connectionChanges,
  draftOf,
  notesProblem,
  type ConnectionDraft,
} from "@/components/database/ops/settings-model"
import { UNDER_STRIP } from "@/components/database/ops/settings-nav"
import { useDatabase } from "@/components/database/shell/database-context"
import { useDatabases } from "@/components/database/shell/databases-context"
import type { DbConnectionSummary } from "@/components/database/shell/types"

/**
 * The connection itself: what the dashboard calls it, what it is for, whether
 * the dashboard may change what is in it, and the address it dials.
 *
 * The labels are a form with one save, which sends only what changed. The
 * address is not a field of that form: it is sealed on the server and read
 * back only when an administrator asks to change it, in its own dialog.
 */
export function ConnectionSection() {
  const { id, conn, engine, summary, status } = useDatabase()
  const { connections, refresh } = useDatabases()
  const { can } = useAuth()
  const admin = can("system.admin")
  const field = useId()
  const saved = draftOf(conn)
  const [edits, setEdits] = useState<Partial<ConnectionDraft>>({})
  const [saving, setSaving] = useState(false)
  const [refusal, setRefusal] = useState<string>()
  const [editing, setEditing] = useState(false)
  const [testing, setTesting] = useState(false)
  const [tested, setTested] = useState<{ ok: boolean; words: string }>()

  const draft: ConnectionDraft = { ...saved, ...edits }
  const changes = connectionChanges(saved, draft)
  const count = Object.keys(changes).length
  const set = (patch: Partial<ConnectionDraft>) => {
    setEdits((held) => ({ ...held, ...patch }))
    setRefusal(undefined)
  }

  const typedName = draft.name.trim()
  const nameProblem = !typedName
    ? "A connection needs a name."
    : !CONNECTION_NAME.test(typedName) && typedName !== saved.name
      ? "Letters, digits, spaces, dots, dashes and underscores, starting with a letter or digit."
      : connections.some(
            (other) => other.id !== id && other.name.toLowerCase() === typedName.toLowerCase(),
          )
        ? "Another connection already has this name."
        : undefined
  const invalid =
    Boolean(nameProblem) ||
    Boolean(environmentProblem(draft.environment)) ||
    Boolean(notesProblem(draft.notes))

  const save = async () => {
    if (count === 0 || invalid || saving) return
    setSaving(true)
    setRefusal(undefined)
    try {
      await put<DbConnection>(`/databases/${id}`, changes)
      notify.success(`Saved ${typedName}`)
      setEdits({})
      refresh()
      status.refresh()
    } catch (err) {
      setRefusal(errorMessage(err))
    } finally {
      setSaving(false)
    }
  }

  const test = async () => {
    setTesting(true)
    try {
      const answer = await get<DbConnectionSummary>(`/databases/${id}`)
      setTested(
        answer.ok
          ? {
              ok: true,
              words: `Answered${answer.latencyMs ? ` in ${answer.latencyMs} ms` : ""}${answer.version ? `: ${answer.version}` : ""}`,
            }
          : {
              ok: false,
              words: answer.error ?? `The server is ${answer.state}; it was not dialled.`,
            },
      )
    } catch (err) {
      setTested({ ok: false, words: errorMessage(err) })
    } finally {
      setTesting(false)
      status.refresh()
    }
  }

  return (
    <FormSection
      aside
      id="connection"
      className={UNDER_STRIP}
      title="Connection"
      hint={
        <>
          <span className="block">
            {engine.label}
            {summary?.versionNumber ? ` ${summary.versionNumber}` : ""}
          </span>
          <span className="block font-mono wrap-anywhere">
            {conn.port ? `${conn.host}:${conn.port}` : conn.database}
          </span>
        </>
      }
    >
      {admin ? (
        <form
          aria-label="Connection labels"
          className="space-y-5"
          onSubmit={(event) => {
            event.preventDefault()
            void save()
          }}
        >
          <Field
            label="Name"
            htmlFor={`${field}-name`}
            hint="How it is listed."
            error={nameProblem}
          >
            <Input
              id={`${field}-name`}
              value={draft.name}
              onChange={(event) => set({ name: event.target.value })}
              autoComplete="off"
              maxLength={64}
            />
          </Field>
          <EnvironmentField
            id={`${field}-environment`}
            value={draft.environment}
            onChange={(environment) => set({ environment })}
          />
          <Field
            label="Notes"
            htmlFor={`${field}-notes`}
            hint="Anything worth remembering about it. Only the dashboard keeps them."
            error={notesProblem(draft.notes)}
          >
            <Textarea
              id={`${field}-notes`}
              value={draft.notes}
              onChange={(event) => set({ notes: event.target.value })}
              rows={3}
            />
          </Field>
          <OptionList>
            <OptionRow
              title="Protected: refuse every change made to its data or schema through this dashboard"
              hint="For every role. A dump can still be taken. It reads what a request says: a statement that calls a function which writes still passes, and only an account that cannot write stops the engine itself."
              checked={draft.readOnly}
              onCheckedChange={(readOnly) => set({ readOnly })}
            />
          </OptionList>
          {refusal && (
            <FormNote role="alert" tone="danger" className="break-words">
              It was not saved. {refusal}
            </FormNote>
          )}
          <div className="flex flex-wrap items-center justify-end gap-2">
            {count > 0 && (
              <FormNote className="mr-auto">
                <span className="flex">
                  <Status
                    tone="warning"
                    label={count === 1 ? "1 unsaved change" : `${count} unsaved changes`}
                  />
                </span>
              </FormNote>
            )}
            {count > 0 && (
              <Button
                type="button"
                variant="ghost"
                size="sm"
                disabled={saving}
                onClick={() => {
                  setEdits({})
                  setRefusal(undefined)
                }}
              >
                Discard
              </Button>
            )}
            <Button
              type="submit"
              size="sm"
              variant={count > 0 ? "default" : "outline"}
              disabled={count === 0 || invalid}
              pending={saving}
            >
              Save
            </Button>
          </div>
        </form>
      ) : (
        <DetailList className="gap-y-2.5">
          <Detail label="Name">{conn.name}</Detail>
          <Detail label="Environment">
            {conn.environment ? (
              <span className="flex">
                <EnvironmentTag environment={conn.environment} />
              </span>
            ) : (
              <span className="text-muted-foreground">None</span>
            )}
          </Detail>
          <Detail label="Protected">
            {conn.readOnly ? (
              <span className="flex">
                <ProtectedTag />
              </span>
            ) : (
              "No: its data and schema can be changed from the dashboard"
            )}
          </Detail>
          {conn.notes && (
            <Detail label="Notes" className="whitespace-pre-wrap">
              {conn.notes}
            </Detail>
          )}
        </DetailList>
      )}

      <div className="space-y-3 border-t border-hairline pt-5" data-slot="connection-address">
        <div className="flex min-w-0 flex-wrap items-center justify-between gap-x-4 gap-y-2">
          <h4 className="text-sm font-medium">Address</h4>
          <div className="flex flex-wrap items-center gap-2">
            <Button size="sm" variant="outline" pending={testing} onClick={() => void test()}>
              <Router />
              Test
            </Button>
            {admin && (
              <Button size="sm" variant="outline" onClick={() => setEditing(true)}>
                <Pencil />
                Edit
              </Button>
            )}
          </div>
        </div>
        <DetailList className="gap-y-2.5">
          {conn.host && engine.can("server") && (
            <Detail label="Server" className="font-mono wrap-anywhere">
              {conn.host}
              {conn.port ? `:${conn.port}` : ""}
            </Detail>
          )}
          {conn.user && (
            <Detail label="Account" className="font-mono wrap-anywhere">
              {conn.user}
            </Detail>
          )}
          {conn.database && (
            <Detail label={engine.databaseField} className="font-mono wrap-anywhere">
              {conn.database}
            </Detail>
          )}
          <Detail label="Added">{timestamp(conn.createdAt)}</Detail>
          {conn.broken && (
            <Detail label="State">
              <span className="text-destructive">
                {conn.brokenReason ?? "It cannot be opened."}
              </span>
            </Detail>
          )}
        </DetailList>
        <div aria-live="polite">
          {tested &&
            (tested.ok ? (
              <span className="flex" data-slot="test-result">
                <Status tone="running" label={tested.words} />
              </span>
            ) : (
              <Notice tone="danger" title="It did not answer">
                <span className="break-words whitespace-pre-wrap">{tested.words}</span>
              </Notice>
            ))}
        </div>
      </div>

      {editing && (
        <EditAddress
          onClose={() => setEditing(false)}
          onSaved={() => {
            setEditing(false)
            setTested(undefined)
            refresh()
            status.refresh()
          }}
        />
      )}
    </FormSection>
  )
}

type Tested = { dsn: string; answer: DbTestResponse }

/**
 * Change the address the dashboard dials, or the password it signs in with.
 *
 * The saved string is read from the server — a read that is recorded — and
 * changed in place: host, port, account, password and database are the
 * characters that say them, and every option the string carries (an
 * `sslmode`, a `replicaSet`, a timeout) stays exactly as saved and is listed,
 * so nothing is lost by changing a password. A string with something the
 * fields do not cover is edited whole.
 *
 * The two editors are one string. Going to the whole string writes what the
 * fields have made of it, every time; coming back from a string that was
 * retyped lays the fields over that string. Neither holds a copy the other
 * has moved on from — the whole string is hidden as a password is, so a stale
 * one could not be seen.
 *
 * Test dials the new string before anything is saved. Save is live once that
 * exact string has answered, or once the reader says to save it untested.
 */
function EditAddress({ onClose, onSaved }: { onClose: () => void; onSaved: () => void }) {
  const { id, conn, engine, summary } = useDatabase()
  const field = useId()
  const [stored, setStored] = useState<string>()
  // The string the fields are laid over: the saved one, until the whole string is retyped.
  const [base, setBase] = useState<string>()
  const [unread, setUnread] = useState<string>()
  const [shape, setShape] = useState<"fields" | "whole">("fields")
  const [edits, setEdits] = useState<{
    host?: string
    port?: string
    user?: string
    password?: string
    database?: string
  }>({})
  const [whole, setWhole] = useState("")
  const [unfielded, setUnfielded] = useState(false)
  const [untested, setUntested] = useState(false)
  const [tested, setTested] = useState<Tested>()
  const [testing, setTesting] = useState(false)
  const [saving, setSaving] = useState(false)
  const [refusal, setRefusal] = useState<string>()

  useEffect(() => {
    let cancelled = false
    get<{ url: string }>(`/databases/${id}/url`, { target: "host" })
      .then((answer) => {
        if (cancelled) return
        setStored(answer.url)
        setBase(answer.url)
      })
      .catch((err) => {
        if (cancelled) return
        // A row that can no longer be opened has no string to read: a new one
        // is typed whole, which is how such a row is repaired.
        setUnread(errorMessage(err))
        setShape("whole")
      })
    return () => {
      cancelled = true
    }
  }, [id])

  const parts = base !== undefined ? readDsn(base) : undefined
  // A file's connection is its path; every other engine's string is an address.
  const file = stored !== undefined && readDsn(stored).shape === "path"
  // An empty password field keeps the saved one; the other fields say what they hold.
  const change = {
    ...edits,
    ...(edits.password === undefined || edits.password === "" ? { password: undefined } : {}),
  }
  const fromFields = base !== undefined ? patchDsn(base, change) : ""
  const dsn = shape === "whole" ? whole.trim() : fromFields
  const changed = dsn !== "" && dsn !== stored
  const passed = tested?.dsn === dsn && tested.answer.ok ? tested.answer : undefined
  const failed = tested?.dsn === dsn && !tested.answer.ok ? tested.answer.error : undefined
  const set = (patch: typeof edits) => {
    setEdits((held) => ({ ...held, ...patch }))
    setRefusal(undefined)
  }
  const value = (key: "host" | "port" | "user" | "database") => edits[key] ?? parts?.[key] ?? ""

  const test = async () => {
    if (!dsn) return
    setTesting(true)
    setRefusal(undefined)
    try {
      const answer = await post<DbTestResponse>("/databases/test", { driver: conn.driver, dsn })
      setTested({ dsn, answer })
    } catch (err) {
      setTested({ dsn, answer: { ok: false, error: errorMessage(err) } })
    } finally {
      setTesting(false)
    }
  }

  const save = async () => {
    setSaving(true)
    setRefusal(undefined)
    try {
      await put<DbConnection>(`/databases/${id}`, { dsn })
      notify.success(`Saved the address of ${conn.name}`)
      onSaved()
    } catch (err) {
      setRefusal(errorMessage(err))
      setSaving(false)
    }
  }

  return (
    <TaskDialog
      title="Edit the address"
      description={`Change where ${conn.name} is dialled, or the password it signs in with`}
      size="lg"
      subject={databaseSubject(
        conn,
        engine,
        <FormFact label="Engine">
          {engine.label}
          {summary?.versionNumber ? ` ${summary.versionNumber}` : ""}
        </FormFact>,
      )}
      dirty={changed}
      busy={saving}
      refusal={refusal && `It was not saved. ${refusal}`}
      note={
        passed ? (
          <span className="flex" data-slot="test-result">
            <Status tone="running" label={`Answered: ${passed.version}`} />
          </span>
        ) : (
          "The string is sealed on the server and never sent back to a browser without being asked for."
        )
      }
      secondary={
        <Button
          type="button"
          variant="outline"
          disabled={!dsn || saving}
          pending={testing}
          onClick={() => void test()}
        >
          <Router />
          Test
        </Button>
      }
      command="Save"
      disabled={!changed || !(passed || untested)}
      onRun={() => void save()}
      onClose={onClose}
    >
      {!file && (
        <Segments
          label="How the address is edited"
          value={shape}
          disabled={stored === undefined}
          onChange={(next) => {
            if (next === shape) return
            setRefusal(undefined)
            setUnfielded(false)
            const typed = whole.trim()
            if (next === "fields" && typed && typed !== fromFields) {
              // A string the fields cannot take apart stays where it can be read whole.
              if (readDsn(typed).shape === "path") {
                setUnfielded(true)
                return
              }
              setBase(typed)
              setEdits({})
            }
            if (next === "whole") setWhole(fromFields)
            setShape(next)
          }}
          options={[
            { value: "fields", label: "Fields" },
            { value: "whole", label: "Whole string" },
          ]}
        />
      )}

      {unfielded && (
        <FormNote role="status">
          The fields cannot take this string apart — it is not written as an address — so it is
          edited whole.
        </FormNote>
      )}
      {unread && (
        <Notice tone="warning" title="The saved string could not be read">
          <span className="break-words">{unread}</span> Type the whole string again to repair the
          connection.
        </Notice>
      )}
      {failed && (
        <Notice tone="danger" title="It refused the connection">
          <span className="break-words whitespace-pre-wrap">{failed}</span>
        </Notice>
      )}

      {shape === "whole" ? (
        <Field
          label="Connection string"
          htmlFor={`${field}-whole`}
          hint="As the application that uses it would write it. It replaces the saved one whole."
        >
          <Input
            id={`${field}-whole`}
            // A connection string is a password with an address round it.
            type="password"
            value={whole}
            onChange={(event) => {
              setWhole(event.target.value)
              setRefusal(undefined)
              setUnfielded(false)
            }}
            placeholder={engine.dsnExample}
            className="font-mono"
            autoComplete="off"
            spellCheck={false}
          />
        </Field>
      ) : !parts ? (
        <div className="space-y-3" role="status" aria-label="Reading the saved address">
          <Skeleton className="h-9 w-full" />
          <Skeleton className="h-9 w-full" />
          <Skeleton className="h-9 w-2/3" />
        </div>
      ) : file ? (
        <Field
          label={engine.databaseField}
          htmlFor={`${field}-database`}
          hint="A path on this server inside the dashboard's file roots."
        >
          <Input
            id={`${field}-database`}
            value={value("database")}
            onChange={(event) => set({ database: event.target.value })}
            className="font-mono"
            autoComplete="off"
            spellCheck={false}
          />
        </Field>
      ) : (
        <>
          {parts.hosts ? (
            <Field
              label="Hosts"
              hint="This string names several servers. Edit them in the whole string."
            >
              <p className="rounded-md bg-surface-sunken px-3 py-2 font-mono text-xs wrap-anywhere">
                {parts.hosts}
              </p>
            </Field>
          ) : (
            <FieldRow columns={3}>
              <Field label="Host" htmlFor={`${field}-host`} className="sm:col-span-2">
                <Input
                  id={`${field}-host`}
                  value={value("host")}
                  onChange={(event) => set({ host: event.target.value })}
                  className="font-mono"
                  autoComplete="off"
                  spellCheck={false}
                />
              </Field>
              <Field label="Port" htmlFor={`${field}-port`}>
                <Input
                  id={`${field}-port`}
                  value={value("port")}
                  onChange={(event) => set({ port: event.target.value.replace(/[^0-9]/g, "") })}
                  placeholder={engine.defaultPort}
                  className="font-mono"
                  inputMode="numeric"
                  autoComplete="off"
                />
              </Field>
            </FieldRow>
          )}
          <FieldRow>
            <Field label="Account" htmlFor={`${field}-user`}>
              <Input
                id={`${field}-user`}
                value={value("user")}
                onChange={(event) => set({ user: event.target.value })}
                className="font-mono"
                autoComplete="off"
                spellCheck={false}
              />
            </Field>
            <Field
              label="Password"
              htmlFor={`${field}-password`}
              hint={parts.password ? "Empty keeps the saved one." : "None is saved."}
            >
              <Input
                id={`${field}-password`}
                type="password"
                value={edits.password ?? ""}
                onChange={(event) => set({ password: event.target.value })}
                placeholder={parts.password ? "unchanged" : ""}
                className="font-mono"
                autoComplete="new-password"
              />
            </Field>
          </FieldRow>
          <Field label={engine.databaseField} htmlFor={`${field}-database`}>
            <Input
              id={`${field}-database`}
              value={value("database")}
              onChange={(event) => set({ database: event.target.value })}
              className="font-mono"
              autoComplete="off"
              spellCheck={false}
            />
          </Field>
          <div className="space-y-1.5" data-slot="kept-options">
            <p className="text-body font-medium">
              {base === stored ? "Kept as saved" : "Kept as written"}
            </p>
            {parts.options.length > 0 ? (
              <p className="flex flex-wrap gap-1.5">
                {parts.options.map((option) => (
                  <Tag key={option} mono className="max-w-full truncate">
                    {option}
                  </Tag>
                ))}
              </p>
            ) : (
              <FormNote>The saved string carries no option besides these fields.</FormNote>
            )}
          </div>
        </>
      )}

      {shape === "fields" && dsn && !file && (
        <FormNote className="truncate font-mono" data-slot="connection-preview">
          {maskedDsn(dsn)}
        </FormNote>
      )}

      <OptionList>
        <OptionRow
          title="Save it without testing, for a server that is not answering now"
          checked={untested}
          onCheckedChange={setUntested}
        />
      </OptionList>
    </TaskDialog>
  )
}
