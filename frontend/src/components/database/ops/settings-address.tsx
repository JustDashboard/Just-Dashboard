"use client"

import { useId, useRef, useState } from "react"
import { Database, Router } from "@/components/icons"
import { errorMessage, get, post, put } from "@/lib/api"
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
import { Field, FieldRow, FormNote } from "@/components/form"
import { Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { AnimatedBeam } from "@/components/ui/animated-beam"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import type { DbTestResponse } from "@/components/database/fleet/types"
import { EngineGlyph, EnvironmentTag, ProtectedTag } from "@/components/database/kit"
import {
  addressDraft,
  type AddressEdits,
  type AddressShape,
} from "@/components/database/ops/settings-address-model"
import { maskedDsn, readDsn } from "@/components/database/ops/settings-dsn"
import { usePendingReport } from "@/components/database/ops/settings-pending"
import { useDatabase } from "@/components/database/shell/database-context"
import { useDatabases } from "@/components/database/shell/databases-context"
import type { DbConnectionSummary } from "@/components/database/shell/types"

type Answer = { ok: boolean; words: string }

/**
 * The address the dashboard dials, drawn as the way it goes (this server,
 * the account it signs in as, the server that answers, the database it
 * opens) with its fields under the picture and Test at the head's end.
 *
 * Any role tests the connection as it is saved. An administrator changes it,
 * in place, and the page's one Save writes it.
 */
export function AddressSection() {
  const { id } = useDatabase()
  const { can } = useAuth()
  // The draft is one connection's: another database's page starts clean.
  return can("system.admin") ? <AddressForm key={id} /> : <AddressReading />
}

/** Dials the connection as it is saved, and says what answered. */
function useSavedTest() {
  const { id, status } = useDatabase()
  const [testing, setTesting] = useState(false)
  const [answer, setAnswer] = useState<Answer>()
  const test = async () => {
    setTesting(true)
    try {
      const said = await get<DbConnectionSummary>(`/databases/${id}`)
      setAnswer(
        said.ok
          ? {
              ok: true,
              words: `Answered${said.latencyMs ? ` in ${said.latencyMs} ms` : ""}${said.version ? `: ${said.version}` : ""}`,
            }
          : { ok: false, words: said.error ?? `The server is ${said.state}; it was not dialled.` },
      )
    } catch (err) {
      setAnswer({ ok: false, words: errorMessage(err) })
    } finally {
      setTesting(false)
      status.refresh()
    }
  }
  return { testing, answer, test, forget: () => setAnswer(undefined) }
}

/** What the saved connection answered, where the reader pressed Test. */
function SavedAnswer({ answer }: { answer: Answer | undefined }) {
  if (!answer) return null
  return answer.ok ? (
    <span className="flex animate-rise" data-slot="test-result">
      <Status tone="running" label={answer.words} />
    </span>
  ) : (
    <Notice tone="danger" title="It did not answer">
      <span className="break-words whitespace-pre-wrap">{answer.words}</span>
    </Notice>
  )
}

const addressState = (conn: DbConnection) => (
  <span className="font-mono wrap-anywhere">
    {conn.port ? `${conn.host}:${conn.port}` : conn.database}
  </span>
)

/** The address for a role that reads it: the picture, and Test. */
function AddressReading() {
  const { conn } = useDatabase()
  const saved = useSavedTest()
  return (
    <SettingSection
      id="address"
      title="Address"
      state={addressState(conn)}
      actions={
        <Button
          size="sm"
          variant="outline"
          pending={saved.testing}
          onClick={() => void saved.test()}
        >
          <Router />
          Test
        </Button>
      }
    >
      <AddressPicture />
      <div aria-live="polite" className="empty:hidden">
        <SavedAnswer answer={saved.answer} />
      </div>
      {conn.broken && (
        <Notice tone="danger" title="It cannot be opened">
          <span className="break-words">
            {conn.brokenReason ?? "The saved string cannot be read."}
          </span>
        </Notice>
      )}
    </SettingSection>
  )
}

type Tested = { dsn: string; answer: DbTestResponse }

/**
 * Change the address the dashboard dials, or the password it signs in with,
 * in the page's own column and under its one Save.
 *
 * It was a dialog with a Save of its own, opened from an Edit beside Test, on
 * a page whose other fields are saved by the bar. The fields are on the page
 * now, as a project's repository is, showing what the connection's row
 * already says: host, port, account and database.
 *
 * The string itself is sealed on the server and its read is recorded, so it
 * is not asked for to draw the page. It is asked for when the reader first
 * changes a field, and the change is then made in place: the fields are the
 * characters of the saved string that say them, and every option it carries
 * (an `sslmode`, a `replicaSet`, a timeout) stays exactly as saved and is
 * listed, so nothing is lost by changing a password. A string with something
 * the fields do not cover is edited whole, and the two editors are one string
 * (`settings-address-model.ts`).
 *
 * Save dials the new string before it writes it, unless Test already has: a
 * string that does not answer is not saved, and the refusal offers the one
 * way round it, for a server that is not answering now. A save that is
 * refused leaves the fields as they were typed.
 */
function AddressForm() {
  const { id, conn, engine, status } = useDatabase()
  const { refresh } = useDatabases()
  const field = useId()
  const file = engine.can("fileBased")
  const saved = useSavedTest()
  const [stored, setStored] = useState<string>()
  // The string the fields are laid over: the saved one, until the whole string is retyped.
  const [base, setBase] = useState<string>()
  const [unread, setUnread] = useState<string>()
  // A row that cannot be opened has no string to read: a new one is typed whole.
  const [shape, setShape] = useState<AddressShape>(conn.broken ? "whole" : "fields")
  const [edits, setEdits] = useState<AddressEdits>({})
  const [whole, setWhole] = useState("")
  const [unfielded, setUnfielded] = useState(false)
  const [tested, setTested] = useState<Tested>()
  const [testing, setTesting] = useState(false)
  const [switching, setSwitching] = useState(false)
  const [saving, setSaving] = useState(false)
  const [refusal, setRefusal] = useState<string>()
  const reading = useRef<Promise<string | undefined>>(undefined)

  /** The saved string, read once and only when a change needs it. */
  const ask = () => {
    if (reading.current) return reading.current
    if (conn.broken) return Promise.resolve(undefined)
    reading.current = get<{ url: string }>(`/databases/${id}/url`, { target: "host" })
      .then((answer) => {
        setStored(answer.url)
        setBase(answer.url)
        return answer.url
      })
      .catch((err) => {
        // A string that cannot be read is replaced whole.
        setUnread(errorMessage(err))
        setShape("whole")
        return undefined
      })
    return reading.current
  }

  const row = { host: conn.host, port: conn.port, user: conn.user, database: conn.database }
  const draft = addressDraft({ stored, base, shape, edits, whole, row })
  const { parts, dsn, dirty, changes } = draft
  const passed = tested?.dsn === dsn && tested.answer.ok ? tested.answer : undefined
  const failed = tested?.dsn === dsn && !tested.answer.ok ? tested.answer.error : undefined

  const set = (patch: AddressEdits) => {
    setEdits((held) => ({ ...held, ...patch }))
    setRefusal(undefined)
    void ask()
  }
  const value = (key: "host" | "port" | "user" | "database") =>
    edits[key] ?? (parts ? parts[key] : (row[key] ?? ""))

  /** The string a press acts on, read if it has not been: a press may come before the read lands. */
  const target = async () => {
    if (shape === "whole") return whole.trim()
    const from = base ?? (await ask())
    return from === undefined
      ? ""
      : addressDraft({ stored: from, base: from, shape, edits, whole, row }).dsn
  }

  const dial = async (string: string) => {
    try {
      return await post<DbTestResponse>("/databases/test", { driver: conn.driver, dsn: string })
    } catch (err) {
      return { ok: false, error: errorMessage(err) } as DbTestResponse
    }
  }

  const test = async () => {
    if (!dirty) return saved.test()
    setTesting(true)
    setRefusal(undefined)
    try {
      const string = await target()
      if (string) setTested({ dsn: string, answer: await dial(string) })
    } finally {
      setTesting(false)
    }
  }

  const save = async (untested = false) => {
    if (!dirty) return true
    setSaving(true)
    setRefusal(undefined)
    try {
      const string = await target()
      if (!string) return false
      if (!untested && !(tested?.dsn === string && tested.answer.ok)) {
        const answer = await dial(string)
        setTested({ dsn: string, answer })
        if (!answer.ok) return false
      }
      await put<DbConnection>(`/databases/${id}`, { dsn: string })
      reading.current = Promise.resolve(string)
      setStored(string)
      setBase(string)
      setEdits({})
      setWhole("")
      setUnread(undefined)
      setUnfielded(false)
      setTested(undefined)
      saved.forget()
      setShape(!file && readDsn(string).shape === "path" ? "whole" : "fields")
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

  const discard = () => {
    setEdits({})
    setWhole("")
    setBase(stored)
    setUnfielded(false)
    setTested(undefined)
    setRefusal(undefined)
    setShape(conn.broken || (unread && stored === undefined) ? "whole" : "fields")
  }

  const reshape = async (next: AddressShape) => {
    if (next === shape) return
    setRefusal(undefined)
    setUnfielded(false)
    if (next === "whole") {
      setSwitching(true)
      // The whole string is what the fields have made of it, every time.
      const string = await target()
      setSwitching(false)
      setWhole(string)
      setShape("whole")
      return
    }
    const typed = whole.trim()
    if (typed && typed !== draft.fromFields) {
      // A string the fields cannot take apart stays where it can be read whole.
      if (readDsn(typed).shape === "path") {
        setUnfielded(true)
        return
      }
      setBase(typed)
      setEdits({})
    } else if (base === undefined) {
      // Nothing was read and nothing typed: there is no string to lay fields over.
      return
    }
    setShape("fields")
  }

  usePendingReport("address", {
    edits: dirty ? [{ name: "Address", state: "changed" }] : [],
  })

  return (
    <SettingForm
      name="Address"
      onSave={() => save()}
      dirty={dirty}
      changes={changes}
      saving={saving}
      invalid={draft.empty}
      canEdit
      applies="immediately"
      onDiscard={discard}
      error={refusal && `It was not saved. ${refusal}`}
    >
      <SettingSection
        id="address"
        title="Address"
        state={addressState(conn)}
        status={settingStatus({ dirty, refused: dirty && Boolean(refusal || failed) })}
        actions={
          <Button
            size="sm"
            variant="outline"
            pending={testing || saved.testing}
            disabled={saving}
            onClick={() => void test()}
          >
            <Router />
            Test
          </Button>
        }
      >
        <AddressPicture />
        <div aria-live="polite" className="empty:hidden">
          {dirty ? (
            passed ? (
              <span className="flex animate-rise" data-slot="test-result">
                <Status tone="running" label={`Answered: ${passed.version}`} />
              </span>
            ) : (
              failed && (
                <Notice tone="danger" title="It refused the connection">
                  <span className="break-words whitespace-pre-wrap">{failed}</span>
                  <Button
                    type="button"
                    size="xs"
                    variant="outline"
                    className="mt-2 block"
                    pending={saving}
                    onClick={() =>
                      void save(true).then(
                        (done) => done && notify.success(`Saved the address of ${conn.name}`),
                      )
                    }
                  >
                    Save it untested
                  </Button>
                </Notice>
              )
            )
          ) : (
            <SavedAnswer answer={saved.answer} />
          )}
        </div>
        {conn.broken && (
          <Notice tone="danger" title="It cannot be opened">
            <span className="break-words">
              {conn.brokenReason ?? "The saved string cannot be read."} Type the whole string again
              to repair it.
            </span>
          </Notice>
        )}
        {unread && !conn.broken && (
          <Notice tone="warning" title="The saved string could not be read">
            <span className="break-words">{unread}</span> Type the whole string again to replace it.
          </Notice>
        )}

        {!file && (
          <Segments
            label="How the address is edited"
            value={shape}
            disabled={switching || saving}
            onChange={(next) => void reshape(next)}
            options={[
              { value: "fields", label: "Fields" },
              { value: "whole", label: "Whole string" },
            ]}
          />
        )}
        {unfielded && (
          <FormNote role="status">
            The fields cannot take this string apart (it is not written as an address), so it is
            edited whole.
          </FormNote>
        )}

        {shape === "whole" ? (
          <Field
            label="Connection string"
            htmlFor={`${field}-whole`}
            info="As the application that uses it would write it. It replaces the saved one whole."
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
        ) : file ? (
          <Field
            label={engine.databaseField}
            htmlFor={`${field}-database`}
            info="A path on this server inside the dashboard's file roots."
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
            {parts?.hosts ? (
              <Field
                label="Hosts"
                info="This string names several servers. Edit them in the whole string."
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
                info="Empty keeps the saved one. It is never put back into the page."
              >
                <Input
                  id={`${field}-password`}
                  type="password"
                  value={edits.password ?? ""}
                  onChange={(event) => set({ password: event.target.value })}
                  placeholder={parts && !parts.password ? "none saved" : "unchanged"}
                  className="font-mono"
                  autoComplete="new-password"
                />
              </Field>
            </FieldRow>
            <FieldRow>
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
              <Field
                label={base !== undefined && base !== stored ? "Kept as written" : "Kept as saved"}
                info="Everything else the string carries (an sslmode, a replicaSet, a timeout) stays exactly as it is. Change it in the whole string."
              >
                <p
                  data-slot="kept-options"
                  title={parts?.options.join(" ")}
                  className={cn(
                    "flex h-11 items-center truncate rounded-md bg-surface-sunken px-3 text-xs sm:h-9",
                    parts?.options.length ? "font-mono" : "text-muted-foreground",
                  )}
                >
                  <span className="truncate">
                    {!parts
                      ? "its other options"
                      : parts.options.length > 0
                        ? parts.options.join(" · ")
                        : "no option besides these fields"}
                  </span>
                </p>
              </Field>
            </FieldRow>
          </>
        )}

        {/* One line in every state: what will be saved once it is known, and until then why it is not shown. */}
        {shape === "fields" && !file && base !== undefined && dsn ? (
          <FormNote className="truncate font-mono" data-slot="connection-preview">
            {maskedDsn(dsn)}
          </FormNote>
        ) : (
          <FormNote className="truncate">
            The string is sealed on the server, and read back only to change it.
          </FormNote>
        )}
      </SettingSection>
    </SettingForm>
  )
}

/**
 * How the dashboard reaches the database, in the wiring every settings
 * picture speaks: this server, signing in as the connection's account, to
 * the server that answers, drawn as its product, and the database it opens.
 * A file is two marks: this server and the file.
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
