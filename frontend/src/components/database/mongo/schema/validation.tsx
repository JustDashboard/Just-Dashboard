"use client"

import { useMemo, useRef, useState } from "react"
import Link from "next/link"
import { errorMessage } from "@/lib/api"
import { notify } from "@/lib/toast"
import { useSessionState } from "@/lib/view-state"
import { usePoll } from "@/hooks/use-poll"
import { CodeEditor } from "@/components/code-editor"
import { Segments } from "@/components/deploy/settings/segments"
import { Field, FieldCheck, FormFact, FormFacts } from "@/components/form"
import { EmptyState, LoadingRows, Notice } from "@/components/state"
import { Button } from "@/components/ui/button"
import { useUnloadGuard } from "@/components/database/data/guard"
import { EngineMark } from "@/components/database/kit"
import { checkValidation, mongoValidation, setValidation } from "@/components/database/mongo/api"
import {
  idLabel,
  JsonSyntaxError,
  parseDocument,
  parseJson,
  printJson,
} from "@/components/database/mongo/bson"
import { BsonTree } from "@/components/database/mongo/bson-tree"
import { collectionKey } from "@/components/database/mongo/query"
import { scan } from "@/components/database/mongo/shell"
import type {
  MongoValidation,
  MongoValidationAction,
  MongoValidationCheck,
  MongoValidationLevel,
} from "@/components/database/mongo/types"
import type { Workbench } from "@/components/database/mongo/workbench"
import { ReadError } from "@/components/database/redis/read-error"

const LEVEL_HINT: Record<MongoValidationLevel, string> = {
  strict: "Every insert and every update is checked.",
  moderate:
    "Inserts are checked, and updates to documents that already pass. One that fails today can still be changed.",
  off: "Nothing is checked. The rule is kept and can be turned back on.",
}
const ACTION_HINT: Record<MongoValidationAction, string> = {
  error: "A write that breaks the rule is refused.",
  warn: "A write that breaks the rule goes through, and the server logs it.",
  errorAndLog: "A write that breaks the rule is refused, and the server logs it.",
}

const TEMPLATE = `{
  "$jsonSchema": {
    "bsonType": "object",
    "required": ["name"],
    "properties": {
      "name": { "bsonType": "string" }
    }
  }
}`

type Draft = { validator: string; level: MongoValidationLevel; action: MongoValidationAction }

/** A rule laid out for editing: its own text, indented. */
function pretty(validator: string): string {
  if (!validator.trim()) return ""
  try {
    return printJson(parseJson(validator), true)
  } catch {
    return validator
  }
}

/** What is wrong with the rule as typed, or `null`. An empty rule is "no rule", which is not wrong. */
function ruleProblem(text: string): string | null {
  if (!text.trim()) return null
  try {
    return parseJson(text).k === "object" ? null : "A rule is one document: { … }."
  } catch (err) {
    if (!(err instanceof JsonSyntaxError)) throw err
    const shell = scan(text)
    if (shell.ok) return shell.value.kind === "document" ? null : "A rule is one document: { … }."
    return `Line ${shell.error.line}, column ${shell.error.column}: ${shell.error.message}.`
  }
}

/** The grammar the editor colours the rule with: JSON's own for strict JSON, one that marks nothing for the shell's spelling. */
function ruleGrammar(text: string): string {
  if (!text.trim()) return "json"
  try {
    parseJson(text)
    return "json"
  } catch {
    return scan(text).ok ? "dart" : "json"
  }
}

/**
 * The validation rule of a collection: what every written document must
 * match, how strictly, and what happens to one that does not.
 *
 * A rule judges writes, never what is already stored — so before one is
 * saved, Check counts the existing documents it would reject and shows a few
 * of them. The check reads the collection; it changes nothing.
 */
export function ValidationView({ mongo, catalog, collection: info, confirm }: Workbench) {
  const { id, target, database, collection, engine, canWrite, href } = mongo
  const view = info?.type === "view"
  // What the collection is comes from the list. Until the list has answered
  // (or failed), the rule is not asked for: a view has none, and the server
  // refuses the question.
  const known = info !== undefined || Boolean(catalog.collections.error)
  const poll = usePoll((signal) => mongoValidation(target, signal), 0, [id, database, collection], {
    enabled: known && !view,
  })
  // What is being typed is kept for the tab, per collection: a look at the
  // indexes and back does not lose a half-written rule.
  const [drafts, setDrafts] = useSessionState<Record<string, Draft>>(
    `databases.${id}.mongo.validation`,
    {},
  )
  const scope = collectionKey(database, collection)
  const [checked, setChecked] = useState<{
    of: string
    data?: MongoValidationCheck
    error?: string
  }>()
  const [busy, setBusy] = useState<"checking" | "saving" | null>(null)
  const [refused, setRefused] = useState("")
  const formatRef = useRef<(() => void) | null>(null)
  const resultRef = useRef<HTMLElement>(null)

  const stored = poll.data
  const saved = useMemo<Draft | undefined>(
    () =>
      stored && {
        validator: pretty(stored.validator),
        level: stored.level,
        action: stored.action,
      },
    [stored],
  )
  const draft = (drafts[scope] as Draft | undefined) ?? saved
  const dirty =
    saved !== undefined &&
    draft !== undefined &&
    (draft.validator !== saved.validator ||
      draft.level !== saved.level ||
      draft.action !== saved.action)
  useUnloadGuard(dirty)

  if (view) {
    return (
      <EmptyState
        mark={<EngineMark engine={engine} />}
        className="m-4 min-h-0 flex-1 border-0"
        title="A view has no rule of its own"
        description={`${collection} reads ${info?.viewOn ?? "another collection"}: a validation rule belongs to the collection the documents are written to.`}
        action={
          info?.viewOn && (
            <Button size="sm" variant="outline" asChild>
              <Link href={href("schema", { collection: info.viewOn, view: "validation" })}>
                Open the rule of {info.viewOn}
              </Link>
            </Button>
          )
        }
      />
    )
  }
  if (poll.error && !stored) {
    return <ReadError error={poll.error} onRetry={poll.refresh} className="m-4" />
  }
  if (!draft || !saved) return <LoadingRows rows={8} className="p-4" />

  const set = (patch: Partial<Draft>) => {
    setDrafts((held) => ({ ...held, [scope]: { ...draft, ...patch } }))
    setRefused("")
  }
  const discard = () => {
    setDrafts((held) => Object.fromEntries(Object.entries(held).filter(([key]) => key !== scope)))
    setRefused("")
    setChecked(undefined)
  }

  const problem = ruleProblem(draft.validator)
  const empty = !draft.validator.trim()
  const check = checked?.of === draft.validator ? checked : undefined
  const actions: MongoValidationAction[] = [
    "error",
    "warn",
    // A newer server's third answer is offered only where the server already gave it.
    ...(saved.action === "errorAndLog" || draft.action === "errorAndLog"
      ? (["errorAndLog"] as const)
      : []),
  ]

  const runCheck = async () => {
    setBusy("checking")
    try {
      // The stored rule is checked as it is; a changed one is sent as proposed.
      const proposed = draft.validator !== saved.validator ? draft.validator : undefined
      const data = await checkValidation(target, proposed)
      setChecked({ of: draft.validator, data })
    } catch (err) {
      setChecked({ of: draft.validator, error: errorMessage(err) })
    } finally {
      setBusy(null)
      // The answer is drawn under the form: it is brought into sight.
      requestAnimationFrame(() =>
        resultRef.current?.scrollIntoView({ block: "nearest", behavior: "smooth" }),
      )
    }
  }

  const save = async (next: Draft) => {
    setBusy("saving")
    setRefused("")
    try {
      const change: Partial<MongoValidation> & { validator?: string } = {}
      if (next.validator !== saved.validator) change.validator = next.validator.trim()
      if (next.level !== saved.level) change.level = next.level
      if (next.action !== saved.action) change.action = next.action
      await setValidation(target, change)
      notify.success(
        next.validator.trim() ? "Validation rule saved" : "The validation rule was removed",
      )
      setDrafts((held) => Object.fromEntries(Object.entries(held).filter(([key]) => key !== scope)))
      setChecked(undefined)
      poll.refresh()
    } catch (err) {
      setRefused(errorMessage(err))
      throw err
    } finally {
      setBusy(null)
    }
  }

  const pressSave = () => {
    // Taking a rule away is the one save here that loosens what the collection accepts.
    if (!empty || !saved.validator) {
      void save(draft).catch(() => {})
      return
    }
    confirm({
      title: "Remove the validation rule",
      description:
        "Nothing checks a write to this collection any more. Documents already stored are not touched.",
      subject: {
        mark: <EngineMark engine={engine} size="sm" />,
        name: <span className="font-mono">{collection}</span>,
        facts: (
          <FormFacts>
            <FormFact label="Database" mono>
              {database}
            </FormFact>
          </FormFacts>
        ),
      },
      confirmLabel: "Remove the rule",
      action: async () => {
        await save(draft)
        return "reported"
      },
    })
  }

  return (
    <div className="min-h-0 flex-1 overflow-auto">
      <div className="@container animate-rise p-3">
        <div className="grid items-start gap-x-6 gap-y-4 @4xl:grid-cols-[minmax(0,1fr)_20rem]">
          <div className="min-w-0 space-y-2">
            <div className="flex min-h-7 flex-wrap items-center gap-2">
              <h3 className="text-body font-medium">Rule</h3>
              <span className="min-w-0 flex-1 text-hint text-muted-foreground">
                {saved.validator
                  ? dirty
                    ? "changed, not saved"
                    : "as the collection has it"
                  : "the collection has none"}
              </span>
              {canWrite && empty && (
                <Button size="xs" variant="outline" onClick={() => set({ validator: TEMPLATE })}>
                  Start from a $jsonSchema
                </Button>
              )}
              {!empty && canWrite && ruleGrammar(draft.validator) === "json" && !problem && (
                <Button size="xs" variant="ghost" onClick={() => formatRef.current?.()}>
                  Format
                </Button>
              )}
            </div>
            {/* A frame because the editor is a working region with its own scroll. */}
            <div className="h-[min(22rem,38svh)] overflow-hidden rounded-lg border border-hairline bg-surface-sunken @4xl:h-[min(30rem,55svh)]">
              <CodeEditor
                value={draft.validator}
                language={ruleGrammar(draft.validator)}
                readOnly={!canWrite}
                className="h-full"
                onChange={(validator) => set({ validator })}
                onFormat={(format) => {
                  formatRef.current = format
                }}
              />
            </div>
            <div aria-live="polite" className="min-h-5">
              {problem ? (
                <p className="text-hint text-destructive">{problem}</p>
              ) : (
                <FieldCheck met={!empty}>
                  {empty ? "No rule: every document is accepted" : "Reads as one document"}
                </FieldCheck>
              )}
            </div>
          </div>

          {/* Its first label sits on the line the rule's heading is on. */}
          <div className="min-w-0 space-y-4 @4xl:pt-1">
            <Field label="How strictly" hint={LEVEL_HINT[draft.level]}>
              <Segments
                label="Validation level"
                value={draft.level}
                disabled={!canWrite}
                options={[
                  { value: "strict", label: "Strict" },
                  { value: "moderate", label: "Moderate" },
                  { value: "off", label: "Off" },
                ]}
                onChange={(level) => set({ level })}
              />
            </Field>
            <Field label="A document that fails" hint={ACTION_HINT[draft.action]}>
              <Segments
                label="Validation action"
                value={draft.action}
                disabled={!canWrite}
                options={actions.map((action) => ({
                  value: action,
                  label:
                    action === "error"
                      ? "Is refused"
                      : action === "warn"
                        ? "Is let in"
                        : "Refused and logged",
                }))}
                onChange={(action) => set({ action })}
              />
            </Field>

            <div className="flex flex-wrap items-center gap-2">
              <Button
                size="sm"
                variant="outline"
                pending={busy === "checking"}
                disabled={Boolean(problem) || empty || busy !== null}
                onClick={() => void runCheck()}
              >
                Check
              </Button>
              {canWrite && (
                <>
                  <Button
                    size="sm"
                    pending={busy === "saving"}
                    disabled={!dirty || Boolean(problem) || busy !== null}
                    onClick={pressSave}
                  >
                    {empty && saved.validator ? "Remove the rule" : "Save rule"}
                  </Button>
                  {dirty && (
                    <Button size="sm" variant="ghost" disabled={busy !== null} onClick={discard}>
                      Discard
                    </Button>
                  )}
                </>
              )}
            </div>
            <p className="text-hint leading-relaxed text-muted-foreground">
              A rule judges writes, not what is already stored. Check counts the documents in the
              collection that this rule would reject.
            </p>
            {refused && (
              <Notice tone="danger" title="The rule was not saved">
                {refused}
              </Notice>
            )}
          </div>
        </div>

        {check && (
          <section
            ref={resultRef}
            aria-label="Result of the check"
            className="animate-rise space-y-3 pt-5"
          >
            {check.error !== undefined ? (
              <Notice tone="danger" title="The check could not be run">
                {check.error}
              </Notice>
            ) : check.data ? (
              <CheckResult
                data={check.data}
                href={href}
                database={database}
                collection={collection}
              />
            ) : null}
          </section>
        )}
      </div>
    </div>
  )
}

function CheckResult({
  data,
  href,
  database,
  collection,
}: {
  data: MongoValidationCheck
  href: Workbench["mongo"]["href"]
  database: string
  collection: string
}) {
  const total = data.total >= 0 ? ` of about ${data.total.toLocaleString("en-US")}` : ""
  const count = `${data.exact ? "" : "at least "}${data.failing.toLocaleString("en-US")}`
  const samples = useMemo(
    () =>
      data.samples.map((doc) => {
        try {
          return { doc, root: parseDocument(doc.canonical) }
        } catch {
          return { doc, root: null }
        }
      }),
    [data.samples],
  )
  if (data.failing === 0) {
    return (
      <Notice tone="success" title="Every existing document passes">
        None{total} would be rejected by this rule
        {data.exact ? "." : ", as far as the check got before it ran out of time."}
      </Notice>
    )
  }
  return (
    <>
      <Notice
        tone="warning"
        title={`${count} existing ${data.failing === 1 ? "document fails" : "documents fail"} this rule`}
      >
        Counted{total} in {data.durationMs.toLocaleString("en-US")} ms
        {data.exact ? "" : "; the count ran out of time, so there may be more"}. They stay as they
        are: a rule is only applied when a document is written.
      </Notice>
      <div className="space-y-2">
        <h4 className="eyebrow">
          {samples.length === 1 ? "One of them" : `${samples.length} of them`}
        </h4>
        <ul className="grid gap-2 @3xl:grid-cols-2">
          {samples.map(({ doc, root }) => (
            // A well: output to read.
            <li
              key={doc.id || doc.digest}
              className="max-h-64 min-w-0 overflow-auto rounded-lg border border-hairline bg-surface-sunken p-2"
            >
              {root ? (
                <BsonTree root={root} expand={0} />
              ) : (
                <pre className="font-mono text-hint break-all whitespace-pre-wrap">
                  {doc.canonical}
                </pre>
              )}
              {doc.id && (
                <Link
                  href={href("data", {
                    db: database,
                    collection,
                    filter: `{ "_id": ${doc.id} }`,
                  })}
                  className="mt-1 inline-block rounded-sm text-hint text-muted-foreground underline underline-offset-2 focus-ring hover:text-foreground"
                >
                  Open {idLabel(doc.id)} in Documents
                </Link>
              )}
            </li>
          ))}
        </ul>
      </div>
    </>
  )
}
