"use client"

import { useEffect, useId, useRef, useState } from "react"
import Link from "next/link"
import { Database, Pencil, Router } from "@/components/icons"
import { errorMessage, get, post, put } from "@/lib/api"
import { calendarDate, timestamp } from "@/lib/format"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import type { DbConnection } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import { Segments } from "@/components/deploy/settings/segments"
import {
  SettingForm,
  SettingSection,
  settingStatus,
} from "@/components/deploy/settings/setting-card"
import { SettingPicture } from "@/components/deploy/settings/setting-picture"
import { WireHost, WireMark, WireNode } from "@/components/deploy/wire"
import { Field, FieldRow, FormFact, FormNote, OptionList, OptionRow } from "@/components/form"
import { Detail, DetailList } from "@/components/page"
import { Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { AnimatedBeam } from "@/components/ui/animated-beam"
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
import { EngineGlyph, EnvironmentTag, ProtectedTag } from "@/components/database/kit"
import { TaskDialog, databaseSubject } from "@/components/database/ops/settings-dialog"
import { maskedDsn, patchDsn, readDsn } from "@/components/database/ops/settings-dsn"
import {
  connectionChanges,
  draftOf,
  notesProblem,
  type ConnectionDraft,
} from "@/components/database/ops/settings-model"
import { useDatabase } from "@/components/database/shell/database-context"
import { useDatabases } from "@/components/database/shell/databases-context"
import type { DbConnectionSummary } from "@/components/database/shell/types"
import { supportFact } from "@/components/database/support"

/** The fields of the labels form, as `connectionChanges` names them. */
const LABELS = ["name", "environment", "notes"] as const

/**
 * What the dashboard keeps about the connection: what it calls it, what it
 * is for, notes, and whether it may change what is in it.
 *
 * One form and one write over two sections, the way a project's Runtime is
 * one form over five: the labels and the protection are the same `PUT`, and
 * it sends only what changed. The form draws no Save of its own — the page's
 * bar carries it, with its count, from the first keystroke.
 */
export function ConnectionForm() {
  const { id, conn, engine, summary, status, href } = useDatabase()
  const { connections, refresh } = useDatabases()
  const { can } = useAuth()
  const admin = can("system.admin")
  const field = useId()
  const saved = draftOf(conn)
  const [edits, setEdits] = useState<Partial<ConnectionDraft>>({})
  const [saving, setSaving] = useState(false)
  const [refusal, setRefusal] = useState<string>()

  const draft: ConnectionDraft = { ...saved, ...edits }
  const changes = connectionChanges(saved, draft)
  const count = Object.keys(changes).length
  const labelsEdited = LABELS.some((key) => key in changes)
  const protectionEdited = "readOnly" in changes
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
    if (count === 0) return true
    if (invalid) return false
    setSaving(true)
    setRefusal(undefined)
    try {
      await put<DbConnection>(`/databases/${id}`, changes)
      setEdits({})
      refresh()
      status.refresh()
      return true
    } catch (err) {
      setRefusal(errorMessage(err))
      return false
    } finally {
      setSaving(false)
    }
  }

  // What the connection is, under its name: the product that answered, until
  // when its release is maintained, and when the dashboard was given it.
  const what = (
    <span className="flex min-w-0 flex-wrap items-center gap-x-1.5">
      <EngineGlyph engine={engine} className="size-3.5" />
      <span>
        {engine.label}
        {summary?.versionNumber ? ` ${summary.versionNumber}` : ""}
      </span>
      {summary?.endOfLife && (
        <>
          <span aria-hidden>·</span>
          <span>{supportFact(summary.endOfLife)}</span>
        </>
      )}
      <span aria-hidden>·</span>
      <span title={timestamp(conn.createdAt)}>added {calendarDate(conn.createdAt)}</span>
    </span>
  )
  const protection = conn.readOnly
    ? "On — every change to its data or schema made here is refused"
    : "Off — its data and schema can be changed from here"
  const lock = (
    <FormNote>
      It is the dashboard reading each request, not a lock in the engine: a statement that calls a
      function which writes still passes.{" "}
      {engine.has("access") ? (
        <>
          For a lock the server keeps, connect with an account that cannot write — one is made under{" "}
          <Link href={href("access")} className="rounded-sm underline focus-ring">
            Access
          </Link>
          .
        </>
      ) : (
        "For a lock the server keeps, connect with an account that cannot write."
      )}
    </FormNote>
  )

  if (!admin) {
    return (
      <>
        <SettingSection id="connection" title="Name and labels" state={what}>
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
            {conn.notes && (
              <Detail label="Notes" className="whitespace-pre-wrap">
                {conn.notes}
              </Detail>
            )}
          </DetailList>
        </SettingSection>
        <SettingSection id="protection" title="Protection" state={protection}>
          {conn.readOnly && (
            <span className="flex">
              <ProtectedTag />
            </span>
          )}
          {lock}
        </SettingSection>
      </>
    )
  }

  return (
    <SettingForm
      name="Connection labels"
      onSave={save}
      dirty={count > 0}
      changes={count}
      saving={saving}
      invalid={invalid}
      canEdit={admin}
      applies="immediately"
      onDiscard={() => {
        setEdits({})
        setRefusal(undefined)
      }}
      error={refusal && `It was not saved. ${refusal}`}
    >
      <SettingSection
        id="connection"
        title="Name and labels"
        state={what}
        status={settingStatus({ dirty: labelsEdited, refused: labelsEdited && Boolean(refusal) })}
      >
        <Field
          label="Name"
          htmlFor={`${field}-name`}
          hint="How it is listed in the rail, the control center and the map."
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
          hint="Kept by the dashboard only; the server never sees them."
          error={notesProblem(draft.notes)}
        >
          <Textarea
            id={`${field}-notes`}
            value={draft.notes}
            onChange={(event) => set({ notes: event.target.value })}
            placeholder="Who owns it, what reads it, when it can be restarted"
            rows={3}
          />
        </Field>
      </SettingSection>
      <SettingSection
        id="protection"
        title="Protection"
        state={protection}
        status={settingStatus({
          dirty: protectionEdited,
          refused: protectionEdited && Boolean(refusal),
        })}
      >
        <OptionList>
          <OptionRow
            title="Refuse every change to its data or schema made from this dashboard"
            hint="For every role, on every page. Dumps are still taken."
            checked={draft.readOnly}
            onCheckedChange={(readOnly) => set({ readOnly })}
          />
        </OptionList>
        {lock}
      </SettingSection>
    </SettingForm>
  )
}

/**
 * The address the dashboard dials, drawn as the way it goes — this server,
 * the account it signs in as, the server that answers, the database it opens
 * — with Test and Edit at the head's end.
 *
 * The string itself is sealed on the server and read back only when an
 * administrator asks to change it, in its own dialog.
 */
export function AddressSection() {
  const { id, conn, status } = useDatabase()
  const { refresh } = useDatabases()
  const { can } = useAuth()
  const admin = can("system.admin")
  const [editing, setEditing] = useState(false)
  const [testing, setTesting] = useState(false)
  const [tested, setTested] = useState<{ ok: boolean; words: string }>()

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
    <SettingSection
      id="address"
      title="Address"
      state={
        <span className="font-mono wrap-anywhere">
          {conn.port ? `${conn.host}:${conn.port}` : conn.database}
        </span>
      }
      actions={
        <>
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
        </>
      }
    >
      <AddressPicture />
      <div aria-live="polite" className="empty:hidden">
        {tested &&
          (tested.ok ? (
            <span className="flex animate-rise" data-slot="test-result">
              <Status tone="running" label={tested.words} />
            </span>
          ) : (
            <Notice tone="danger" title="It did not answer">
              <span className="break-words whitespace-pre-wrap">{tested.words}</span>
            </Notice>
          ))}
      </div>
      {conn.broken && (
        <Notice tone="danger" title="It cannot be opened">
          <span className="break-words">
            {conn.brokenReason ?? "The saved string cannot be read."} Edit the address to repair it.
          </span>
        </Notice>
      )}
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
    </SettingSection>
  )
}

/**
 * How the dashboard reaches the database, in the wiring every settings
 * picture speaks: this server, signing in as the connection's account, to
 * the server that answers — drawn as its product — and the database it
 * opens. A file is two marks: this server and the file.
 *
 * The line is the state. It carries a pulse while the server answers, is
 * still while it is stopped or paused, red while it refuses or cannot be
 * opened, and dashed until it has been checked at all.
 */
function AddressPicture() {
  const { conn, engine, summary, status } = useDatabase()
  const container = useRef<HTMLDivElement>(null)
  const hostMark = useRef<HTMLDivElement>(null)
  const serverMark = useRef<HTMLDivElement>(null)
  const databaseMark = useRef<HTMLDivElement>(null)
  const file = engine.can("fileBased")
  const state = status.state
  const broken = state === "unreachable" || state === "broken"
  const line = {
    still: state !== "running",
    dashed: state === "checking" || state === "unknown",
    tone: broken ? ("danger" as const) : ("default" as const),
  }
  const answer =
    state === "running"
      ? summary?.latencyMs
        ? `answers in ${summary.latencyMs} ms`
        : "answers"
      : state === "stopped"
        ? "stopped"
        : state === "paused"
          ? "paused"
          : broken
            ? "does not answer"
            : "not checked yet"
  const answerNode = <span className={cn(broken && "text-destructive")}>{answer}</span>
  const numbered = engine.can("logicalDatabases")

  const host = (
    <WireNode
      nodeRef={hostMark}
      align="end"
      mark={<WireHost size="md" />}
      eyebrow="This server"
      title="Just Dashboard"
      hint={
        conn.user ? (
          <>
            signs in as <span className="font-mono text-foreground">{conn.user}</span>
          </>
        ) : file ? (
          "opens the file"
        ) : engine.kind === "keyvalue" ? (
          // A key–value server asked with no user name signs its client in as its default user.
          <>
            signs in as <span className="font-mono text-foreground">default</span>
          </>
        ) : (
          "signs in with no account"
        )
      }
    />
  )
  const server = (
    <WireNode
      nodeRef={serverMark}
      align={file ? "start" : "center"}
      mark={
        <WireMark size="md" tone="logo">
          <EngineGlyph engine={engine} />
        </WireMark>
      }
      eyebrow={`${engine.label}${summary?.versionNumber ? ` ${summary.versionNumber}` : ""}`}
      title={
        <span className="font-mono text-xs wrap-anywhere">
          {file ? conn.database : `${conn.host}:${conn.port || engine.defaultPort}`}
        </span>
      }
      hint={file ? "a file on this server" : answerNode}
    />
  )
  const database = (
    <WireNode
      nodeRef={databaseMark}
      align="start"
      mark={
        <WireMark size="md" tone="logo">
          <Database />
        </WireMark>
      }
      eyebrow={engine.databaseField}
      title={
        <span className="font-mono text-xs wrap-anywhere">
          {conn.database ? (numbered ? `db ${conn.database}` : conn.database) : "none named"}
        </span>
      }
      hint={
        conn.environment || conn.readOnly ? (
          <span className="flex flex-wrap items-center gap-x-2">
            {conn.environment && <EnvironmentTag environment={conn.environment} />}
            {conn.readOnly && <ProtectedTag />}
          </span>
        ) : (
          "what it opens"
        )
      }
    />
  )

  return (
    <SettingPicture
      label="How the dashboard reaches it"
      containerRef={container}
      className="py-4"
      lines={
        <>
          <AnimatedBeam containerRef={container} fromRef={hostMark} toRef={serverMark} {...line} />
          {!file && (
            <AnimatedBeam
              containerRef={container}
              fromRef={serverMark}
              toRef={databaseMark}
              delay={0.6}
              {...line}
            />
          )}
        </>
      }
      start={[host]}
      middle={file ? undefined : server}
      end={file ? server : database}
    />
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
