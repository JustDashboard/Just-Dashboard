"use client"

import { useEffect, useRef, useState } from "react"
import { ApiError, errorMessage } from "@/lib/api"
import { bytes } from "@/lib/format"
import { notify } from "@/lib/toast"
import { CodeEditor } from "@/components/code-editor"
import { FieldCheck, FormFact, FormFacts, OptionList, OptionRow } from "@/components/form"
import { Modal } from "@/components/modal"
import { LoadingRows, Notice } from "@/components/state"
import { Button } from "@/components/ui/button"
import { EngineMark } from "@/components/database/kit"
import {
  getDocument,
  insertDocuments,
  replaceDocument,
  type MongoTarget,
} from "@/components/database/mongo/api"
import {
  idLabel,
  JsonSyntaxError,
  parseDocument,
  parseJson,
  printReadable,
} from "@/components/database/mongo/bson"
import { scan } from "@/components/database/mongo/shell"
import type { MongoDoc, MongoWriteError } from "@/components/database/mongo/types"
import type { Mongo } from "@/components/database/mongo/use-mongo"
import { ReadError } from "@/components/database/redis/read-error"

/**
 * A document laid out for editing: its Extended JSON, indented, every type
 * and the field order kept. A date is its ISO moment — still a date to the
 * server, and one a reader can change without counting milliseconds.
 */
export function editorText(canonical: string): string {
  try {
    return printReadable(parseDocument(canonical), true)
  } catch {
    return canonical
  }
}

/** What Insert opens on when nothing is handed to it: a document to fill in. */
const BLANK = "{\n  \n}"

type Reading =
  | { state: "empty" }
  | { state: "json" }
  /** Not JSON, but one whole value in the shell's spelling: the server reads it. */
  | { state: "shell" }
  | { state: "bad"; message: string }

/** What the text is, as far as can be told before the server reads it. */
function readingOf(text: string, shape: "document" | "document or list"): Reading {
  if (!text.trim()) return { state: "empty" }
  const lists = shape === "document or list"
  const misshapen: Reading = {
    state: "bad",
    message: lists
      ? "Write a document { … } or a list of them [ … ]."
      : "Write one document: { … }.",
  }
  try {
    const value = parseJson(text)
    return value.k === "object" || (lists && value.k === "array") ? { state: "json" } : misshapen
  } catch (err) {
    if (!(err instanceof JsonSyntaxError)) throw err
    // Not JSON. The server also reads the shell's spelling, so the text is
    // only wrong when it is not one whole value in that either.
    const shell = scan(text)
    if (!shell.ok) {
      const { line, column, message } = shell.error
      return { state: "bad", message: `Line ${line}, column ${column}: ${message}.` }
    }
    const kind = shell.value.kind
    return kind === "document" || (lists && kind === "array") ? { state: "shell" } : misshapen
  }
}

/**
 * The grammar the editor colours the text with. Strict JSON gets the JSON
 * grammar, which also marks a mistake where it is made. Text in the shell's
 * spelling is not JSON and the server reads it all the same, so it is
 * coloured by a grammar that marks nothing: the JSON one would underline
 * every unquoted key of a document that is perfectly good.
 */
function grammarOf(reading: Reading): string {
  return reading.state === "shell" ? "dart" : "json"
}

function ReadingLine({ reading }: { reading: Reading }) {
  return (
    <div aria-live="polite" className="min-h-5">
      {reading.state === "bad" ? (
        <p className="text-hint text-destructive">{reading.message}</p>
      ) : reading.state === "shell" ? (
        <FieldCheck met>
          Shell syntax: the server reads ObjectId(…), ISODate(…) and unquoted keys
        </FieldCheck>
      ) : (
        <FieldCheck met={reading.state === "json"}>Extended JSON</FieldCheck>
      )}
    </div>
  )
}

/**
 * Asks before typed work is thrown away. The dialog's close — the corner,
 * Escape, a press outside — is the one way out of it, and with changes in
 * the editor that way leads here first.
 */
function useDiscardGuard(dirty: boolean, busy: boolean, onOpenChange: (open: boolean) => void) {
  const [asking, setAsking] = useState(false)
  const request = (open: boolean) => {
    if (open || busy) return
    if (dirty) setAsking(true)
    else onOpenChange(false)
  }
  return {
    asking,
    request,
    stay: () => setAsking(false),
    leave: () => {
      setAsking(false)
      onOpenChange(false)
    },
  }
}

function DiscardFooter({ onStay, onLeave }: { onStay: () => void; onLeave: () => void }) {
  return (
    <>
      <p role="alert" className="mr-auto text-body">
        Close without saving what you typed?
      </p>
      <Button variant="outline" autoFocus onClick={onStay}>
        Keep editing
      </Button>
      <Button variant="destructive" onClick={onLeave}>
        Discard
      </Button>
    </>
  )
}

/**
 * One document in an editor: its Extended JSON, as stored.
 *
 * The text is the document's own — every type spelled out, the fields in
 * their stored order — so saving it unchanged changes nothing. It is read
 * afresh when the editor opens, and saved only if the stored document is
 * still that one (the server compares the two): a change somebody else made
 * in the meantime is not written over, it is said, and the reader chooses.
 */
export function EditDocumentDialog({
  mongo,
  target,
  document: listed,
  onOpenChange,
  onSaved,
}: {
  mongo: Mongo
  target: MongoTarget
  /** The document as the page listed it; `null` closes the editor. */
  document: MongoDoc | null
  onOpenChange: (open: boolean) => void
  onSaved: () => void
}) {
  if (!listed) return null
  return (
    <EditDocument
      key={listed.id}
      mongo={mongo}
      target={target}
      listed={listed}
      onOpenChange={onOpenChange}
      onSaved={onSaved}
    />
  )
}

function EditDocument({
  mongo,
  target,
  listed,
  onOpenChange,
  onSaved,
}: {
  mongo: Mongo
  target: MongoTarget
  listed: MongoDoc
  onOpenChange: (open: boolean) => void
  onSaved: () => void
}) {
  const { engine, canWrite } = mongo
  const [read, setRead] = useState<MongoDoc>()
  const [readError, setReadError] = useState<Error>()
  const [attempt, setAttempt] = useState(0)
  const [text, setText] = useState("")
  const [busy, setBusy] = useState(false)
  const [conflict, setConflict] = useState<"changed" | "gone" | null>(null)
  const [refused, setRefused] = useState("")
  const formatRef = useRef<(() => void) | null>(null)

  // The whole document, as it is stored now: the page may have listed it
  // under a projection, and its digest then names only the part that was shown.
  useEffect(() => {
    const controller = new AbortController()
    getDocument(target, listed.id, controller.signal)
      .then((document) => {
        setRead(document)
        setReadError(undefined)
        setText(editorText(document.canonical))
        setConflict(null)
      })
      .catch((err: unknown) => {
        if (controller.signal.aborted) return
        setReadError(err instanceof Error ? err : new Error(String(err)))
      })
    return () => controller.abort()
  }, [target, listed.id, attempt])

  const original = read ? editorText(read.canonical) : ""
  const dirty = read !== undefined && text !== original
  const reading = readingOf(text, "document")
  const guard = useDiscardGuard(dirty && canWrite, busy, onOpenChange)

  const save = async (guarded: boolean) => {
    if (!read) return
    setBusy(true)
    setRefused("")
    try {
      const result = await replaceDocument(target, read.id, text, guarded ? read.digest : undefined)
      if (result.modified === 0) notify.info("No changes: the document already read that way")
      else notify.success("Document saved")
      onOpenChange(false)
      onSaved()
    } catch (err) {
      if (err instanceof ApiError && err.code === "document_changed") setConflict("changed")
      else if (err instanceof ApiError && err.code === "document_not_found") setConflict("gone")
      else setRefused(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  const reload = () => {
    setRead(undefined)
    setAttempt((n) => n + 1)
  }

  return (
    <Modal
      open
      onOpenChange={guard.request}
      size="xl"
      title={canWrite ? "Edit document" : "Document"}
      description={`The document ${idLabel(listed.id)} of ${target.collection} as Extended JSON`}
      bodyClassName="flex min-h-0 flex-col gap-3 p-4"
      className="h-[min(48rem,calc(100svh-4rem))]"
      actions={
        // Format rewrites the text: there is nothing to rewrite in a document that is only read.
        canWrite &&
        read &&
        reading.state === "json" && (
          <Button size="xs" variant="ghost" onClick={() => formatRef.current?.()}>
            Format
          </Button>
        )
      }
      footer={
        guard.asking ? (
          <DiscardFooter onStay={guard.stay} onLeave={guard.leave} />
        ) : !canWrite ? (
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            Close
          </Button>
        ) : (
          <>
            <p className="mr-auto min-w-0 text-hint text-muted-foreground">
              Replaces the stored document, if it is still the one read here.
            </p>
            <Button variant="outline" disabled={busy} onClick={() => guard.request(false)}>
              Cancel
            </Button>
            <Button
              pending={busy}
              disabled={!read || !dirty || reading.state === "bad" || reading.state === "empty"}
              onClick={() => void save(true)}
            >
              Save document
            </Button>
          </>
        )
      }
    >
      <div className="flex min-w-0 shrink-0 items-center gap-3">
        <EngineMark engine={engine} size="sm" />
        <div className="min-w-0 space-y-0.5">
          <p className="truncate font-mono text-body font-medium">{idLabel(listed.id)}</p>
          <FormFacts>
            <FormFact label="Collection" mono>
              {target.database}.{target.collection}
            </FormFact>
            {read && <FormFact label="Size">{bytes(read.size)}</FormFact>}
          </FormFacts>
        </div>
      </div>

      {conflict === "changed" && (
        <Notice
          tone="warning"
          title="This document changed after it was opened"
          className="shrink-0"
        >
          <p>
            Somebody else wrote to it, so nothing was saved. Load the current document to start from
            it (what you typed is replaced), or save yours over it.
          </p>
          <div className="mt-2 flex flex-wrap gap-2">
            <Button size="xs" variant="outline" disabled={busy} onClick={reload}>
              Load the current document
            </Button>
            <Button size="xs" variant="outline" pending={busy} onClick={() => void save(false)}>
              Save mine over it
            </Button>
          </div>
        </Notice>
      )}
      {conflict === "gone" && (
        <Notice tone="warning" title="This document no longer exists" className="shrink-0">
          It was deleted after it was opened, so nothing was saved. Copy what you typed and insert
          it as a new document if it is still wanted.
        </Notice>
      )}
      {refused && (
        <Notice tone="danger" title="The document was not saved" className="shrink-0">
          {refused}
        </Notice>
      )}

      {readError && !read ? (
        <ReadError error={readError} onRetry={reload} />
      ) : !read ? (
        <LoadingRows rows={8} />
      ) : (
        <>
          {/* A frame because the editor is a working region with its own scroll. */}
          <div className="min-h-0 flex-1 overflow-hidden rounded-lg border border-hairline bg-surface-sunken">
            <CodeEditor
              value={text}
              language={grammarOf(reading)}
              readOnly={!canWrite}
              className="h-full"
              onChange={(next) => {
                setText(next)
                setRefused("")
              }}
              onSave={() => {
                if (canWrite && dirty && reading.state !== "bad") void save(true)
              }}
              onFormat={(format) => {
                formatRef.current = format
              }}
            />
          </div>
          <ReadingLine reading={reading} />
        </>
      )}
    </Modal>
  )
}

/**
 * Insert documents: one, or a list of them, as Extended JSON.
 *
 * A list can land in part — there is no transaction on a standalone server —
 * so the answer is read out: how many are in, and which were refused and why.
 * A refused list keeps the editor open with what was typed.
 */
export function InsertDocumentDialog({
  mongo,
  target,
  open,
  initial,
  onOpenChange,
  onInserted,
}: {
  mongo: Mongo
  target: MongoTarget
  open: boolean
  /** The text the editor opens on: a copy of another document, or nothing. */
  initial?: string
  onOpenChange: (open: boolean) => void
  onInserted: () => void
}) {
  if (!open) return null
  return (
    <InsertDocument
      mongo={mongo}
      target={target}
      initial={initial ?? BLANK}
      onOpenChange={onOpenChange}
      onInserted={onInserted}
    />
  )
}

function InsertDocument({
  mongo,
  target,
  initial,
  onOpenChange,
  onInserted,
}: {
  mongo: Mongo
  target: MongoTarget
  initial: string
  onOpenChange: (open: boolean) => void
  onInserted: () => void
}) {
  const { engine } = mongo
  const [text, setText] = useState(initial)
  const [ordered, setOrdered] = useState(true)
  const [busy, setBusy] = useState(false)
  const [refused, setRefused] = useState("")
  const [partial, setPartial] = useState<{ inserted: number; errors: MongoWriteError[] } | null>(
    null,
  )
  const formatRef = useRef<(() => void) | null>(null)

  const reading = readingOf(text, "document or list")
  const dirty = text !== initial && text.trim() !== ""
  const guard = useDiscardGuard(dirty && !partial, busy, onOpenChange)
  const list = text.trimStart().startsWith("[")

  const insert = async () => {
    setBusy(true)
    setRefused("")
    setPartial(null)
    try {
      const result = await insertDocuments(target, text, ordered)
      onInserted()
      if (result.errors.length > 0) {
        setPartial({ inserted: result.inserted, errors: result.errors })
        return
      }
      notify.success(
        result.inserted === 1 ? "Document inserted" : `${result.inserted} documents inserted`,
      )
      onOpenChange(false)
    } catch (err) {
      setRefused(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal
      open
      onOpenChange={guard.request}
      size="xl"
      title="Insert documents"
      description={`Insert one document or a list of documents into ${target.collection}`}
      bodyClassName="flex min-h-0 flex-col gap-3 p-4"
      className="h-[min(44rem,calc(100svh-4rem))]"
      actions={
        reading.state === "json" && (
          <Button size="xs" variant="ghost" onClick={() => formatRef.current?.()}>
            Format
          </Button>
        )
      }
      footer={
        guard.asking ? (
          <DiscardFooter onStay={guard.stay} onLeave={guard.leave} />
        ) : (
          <>
            <p className="mr-auto min-w-0 text-hint text-muted-foreground">
              A document without an _id is given an ObjectId.
            </p>
            <Button variant="outline" disabled={busy} onClick={() => guard.request(false)}>
              {partial ? "Close" : "Cancel"}
            </Button>
            <Button
              pending={busy}
              // The starting text is an empty document: inserting it is not what a press here means.
              disabled={reading.state === "bad" || reading.state === "empty" || text === BLANK}
              onClick={() => void insert()}
            >
              Insert
            </Button>
          </>
        )
      }
    >
      <div className="flex min-w-0 shrink-0 items-center gap-3">
        <EngineMark engine={engine} size="sm" />
        <FormFacts>
          <FormFact label="Into" mono>
            {target.database}.{target.collection}
          </FormFact>
        </FormFacts>
      </div>

      {partial && (
        <Notice
          tone="warning"
          title={`${partial.inserted.toLocaleString("en-US")} inserted, ${partial.errors.length.toLocaleString("en-US")} refused`}
          className="shrink-0"
        >
          <ul className="mt-1 max-h-28 space-y-0.5 overflow-auto font-mono text-hint">
            {partial.errors.map((error) => (
              <li key={error.index}>
                <span className="text-muted-foreground">Document {error.index + 1}: </span>
                {error.message}
              </li>
            ))}
          </ul>
        </Notice>
      )}
      {refused && (
        <Notice tone="danger" title="Nothing was inserted" className="shrink-0">
          {refused}
        </Notice>
      )}

      <div className="min-h-0 flex-1 overflow-hidden rounded-lg border border-hairline bg-surface-sunken">
        <CodeEditor
          value={text}
          language={grammarOf(reading)}
          className="h-full"
          onChange={(next) => {
            setText(next)
            setRefused("")
          }}
          onSave={() => {
            if (reading.state !== "bad" && reading.state !== "empty") void insert()
          }}
          onFormat={(format) => {
            formatRef.current = format
          }}
        />
      </div>
      <ReadingLine reading={reading} />
      {list && (
        <OptionList className="shrink-0">
          <OptionRow
            title="Stop at the first document the collection refuses"
            checked={ordered}
            onCheckedChange={setOrdered}
          />
        </OptionList>
      )}
    </Modal>
  )
}
