"use client"

import { useEffect, useId, useLayoutEffect, useMemo, useRef, useState } from "react"
import {
  Copy,
  Cross,
  LockClosed,
  PaperAirplane,
  Plus,
  SettingsGear,
  SidebarRightClose,
  Sparkles,
  StopCircle,
} from "@/components/icons"
import { copyText } from "@/lib/clipboard"
import type { PollState } from "@/hooks/use-poll"
import { Markdown } from "@/components/git/markdown"
import { FormNote } from "@/components/form"
import { IconAction } from "@/components/icon-action"
import { Well } from "@/components/panel"
import { ProductGlyph } from "@/components/product-logo"
import { EmptyState, Notice } from "@/components/state"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Skeleton } from "@/components/ui/skeleton"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { Textarea } from "@/components/ui/textarea"
import { Toggle } from "@/components/ui/toggle"
import { ReadError } from "@/components/database/redis/read-error"
import { useDatabase } from "@/components/database/shell/database-context"
import type { SchemaTable } from "@/components/database/query/completion"
import {
  isStatementFence,
  pendingCalls,
  statementTitle,
  type AssistantTask,
  type AttachedResult,
  type Part,
  type ToolCall,
  type Turn,
} from "@/components/database/query/assistant/transcript"
import type {
  AssistantSettings,
  AssistantState,
} from "@/components/database/query/assistant/use-assistant"

/** What the editor's tab holds that a question can carry, each only when the person attaches it. */
export interface AssistantContext {
  /** The statement in the editor. */
  statement: string
  /** The tab's last result, ready to attach. */
  result?: AttachedResult
  /** The statement the tab's last run failed on, and the engine's words. */
  failed?: { sql: string; error: string }
  /** The tab's last plan. */
  plan?: { sql: string; text: string }
}

/**
 * The editor's assistant: a conversation about this connection beside the
 * statement it is about.
 *
 * It is told the structure of the schema in focus — names, types, keys and
 * references — and nothing in a table, unless a result is attached to a
 * question; the composer says so where the question is written, with who it
 * goes to. What it writes is text: a statement it answers with is opened in
 * a tab or written at the cursor by the person's own press, and run by their
 * own Run. The one thing it may ask for itself, reading more of the
 * structure, waits on the person's answer in the conversation.
 */
export function AssistantPane({
  assistant,
  settings,
  schema,
  tables,
  context,
  canSetUp,
  onSetUp,
  onOpen,
  onInsert,
  onClose,
}: {
  assistant: AssistantState
  settings: PollState<AssistantSettings>
  /** The schema in focus, which a new conversation is told about. */
  schema: string
  /** Its tables, for choosing the one a policy is for. */
  tables: SchemaTable[]
  context: AssistantContext
  /** The role may choose the provider (`system.admin`). */
  canSetUp: boolean
  onSetUp: () => void
  /** Opens a statement in a tab of its own. */
  onOpen: (sql: string, title: string) => void
  /** Writes a statement at the cursor. */
  onInsert: (sql: string) => void
  onClose: () => void
}) {
  const { conn, readOnly } = useDatabase()
  const view = settings.data
  const configured = view?.configured === true
  const { transcript } = assistant

  let body: React.ReactNode
  if (readOnly) {
    body = (
      <EmptyState
        className="m-3 border-0 px-3 py-8"
        icon={LockClosed}
        title="Off for a protected connection"
        description={`Nothing about ${conn.name} is sent to a model while it is protected. Protection is taken off in its settings.`}
      />
    )
  } else if (!view) {
    body = settings.error ? (
      <ReadError error={settings.error} onRetry={settings.refresh} className="m-3" />
    ) : (
      <div aria-hidden className="space-y-3 p-3">
        <Skeleton className="h-3 w-40" />
        <Skeleton className="h-3 w-56" />
        <Skeleton className="h-3 w-32" />
      </div>
    )
  } else if (!configured) {
    body = (
      <div className="space-y-3 p-3">
        {view.problem && (
          <Notice tone="warning" title="The saved provider cannot be read">
            {view.problem}
          </Notice>
        )}
        <EmptyState
          className="border-0 px-3 py-8"
          icon={Sparkles}
          title="The assistant is off"
          description={
            canSetUp
              ? "Choose who answers: Claude with your Anthropic key, or an OpenAI-compatible server such as a model on this machine. It drafts SQL from the structure of a database and never runs any."
              : "An administrator turns it on by choosing who answers: Claude with an Anthropic key, or a model on a server of their own."
          }
          action={
            canSetUp && (
              <Button size="sm" onClick={onSetUp}>
                Set up the assistant
              </Button>
            )
          }
        />
      </div>
    )
  } else {
    body = (
      <Conversation
        assistant={assistant}
        settings={view}
        schema={schema}
        tables={tables}
        context={context}
        onOpen={onOpen}
        onInsert={onInsert}
      />
    )
  }

  return (
    <div data-slot="query-assistant" className="flex min-h-0 min-w-0 flex-1 flex-col">
      <div className="flex h-10 shrink-0 items-center gap-2 border-b border-hairline bg-surface-header pr-1.5 pl-3">
        <h2 className="shrink-0 text-body font-medium">Assistant</h2>
        {configured && !readOnly && (
          <span className="flex min-w-0 items-center gap-1.5 text-hint text-muted-foreground">
            <ProductGlyph id={view?.provider === "anthropic" ? "claude" : "openai"} />
            <span className="truncate">{view?.modelLabel}</span>
          </span>
        )}
        <span className="ml-auto flex shrink-0 items-center gap-0.5">
          {configured && !readOnly && transcript.turns.length > 0 && (
            <IconAction
              label="New conversation"
              className="size-7 max-sm:size-8"
              onClick={assistant.reset}
            >
              <Plus />
            </IconAction>
          )}
          {canSetUp && configured && (
            <IconAction
              label="Assistant settings"
              className="size-7 max-sm:size-8"
              onClick={onSetUp}
            >
              <SettingsGear />
            </IconAction>
          )}
          <IconAction label="Hide the assistant" className="size-7 max-sm:size-8" onClick={onClose}>
            <SidebarRightClose />
          </IconAction>
        </span>
      </div>
      {body}
    </div>
  )
}

/** The turns and, under them, the question being written. */
function Conversation({
  assistant,
  settings,
  schema,
  tables,
  context,
  onOpen,
  onInsert,
}: {
  assistant: AssistantState
  settings: AssistantSettings
  schema: string
  tables: SchemaTable[]
  context: AssistantContext
  onOpen: (sql: string, title: string) => void
  onInsert: (sql: string) => void
}) {
  const { engine } = useDatabase()
  const { transcript, busy } = assistant
  const log = useRef<HTMLOListElement>(null)
  // Follows the answer down as it arrives, unless the reader has scrolled up
  // to read an earlier one.
  const following = useRef(true)
  useLayoutEffect(() => {
    const list = log.current
    if (list && following.current) list.scrollTop = list.scrollHeight
  }, [transcript])

  const policies = engine.can("assistantPolicy")
  const [composing, setComposing] = useState<"sql" | "policy">("sql")
  const ask = (task: AssistantTask) => assistant.ask(task, schema)
  const last = transcript.turns.at(-1)
  const waiting = pendingCalls(transcript)

  const starters = (
    <div className="flex flex-wrap justify-center gap-2">
      {context.failed && (
        <Button
          size="sm"
          variant="outline"
          onClick={() =>
            ask({
              kind: "error",
              prompt: "",
              statement: context.failed!.sql,
              error: context.failed!.error,
            })
          }
        >
          Explain the error
        </Button>
      )}
      {context.plan && (
        <Button
          size="sm"
          variant="outline"
          onClick={() =>
            ask({
              kind: "plan",
              prompt: "",
              statement: context.plan!.sql,
              plan: context.plan!.text,
            })
          }
        >
          Explain the plan
        </Button>
      )}
      {policies && (
        <Button size="sm" variant="outline" onClick={() => setComposing("policy")}>
          Draft a row-level security policy
        </Button>
      )}
    </div>
  )

  return (
    <>
      <ol
        ref={log}
        aria-label="Conversation with the assistant"
        aria-busy={busy}
        onScroll={(event) => {
          const list = event.currentTarget
          following.current = list.scrollHeight - list.scrollTop - list.clientHeight < 48
        }}
        className="min-h-0 flex-1 space-y-5 overflow-y-auto p-3"
      >
        {transcript.turns.length === 0 && (
          <li>
            <EmptyState
              className="border-0 px-2 py-8"
              icon={Sparkles}
              title="Ask for a statement"
              description={`It writes ${engine.label} from the structure of ${schema || "this database"}, and explains errors and plans. Nothing it writes runs until you run it.`}
              action={starters}
            />
          </li>
        )}
        {transcript.turns.map((turn) => (
          <TurnView
            key={turn.id}
            turn={turn}
            current={turn === last}
            onOpen={onOpen}
            onInsert={onInsert}
            onAnswer={assistant.answer}
          />
        ))}
      </ol>
      <Composer
        key={composing}
        mode={composing}
        onMode={setComposing}
        settings={settings}
        schema={transcript.turns.length > 0 && transcript.schema ? transcript.schema : schema}
        tables={tables}
        attachable={context}
        busy={busy}
        waiting={waiting}
        onAsk={ask}
        onStop={assistant.stop}
      />
    </>
  )
}

const TASK_WORDS: Record<AssistantTask["kind"], string> = {
  sql: "",
  error: "Explain why this statement failed",
  plan: "Explain this plan",
  policy: "Draft a row-level security policy",
}

function TurnView({
  turn,
  current,
  onOpen,
  onInsert,
  onAnswer,
}: {
  turn: Turn
  current: boolean
  onOpen: (sql: string, title: string) => void
  onInsert: (sql: string) => void
  onAnswer: (allow: boolean) => void
}) {
  const { asked } = turn
  const settled = !["waiting", "thinking", "writing"].includes(turn.phase)
  const question =
    asked.kind === "policy"
      ? `${TASK_WORDS.policy} on ${asked.table}: ${asked.prompt}`
      : asked.prompt || TASK_WORDS[asked.kind]
  const title = statementTitle(asked)
  return (
    <li className="space-y-2.5" data-turn={turn.phase}>
      <div className="ml-6 space-y-1.5 rounded-lg bg-surface-header px-3 py-2">
        <p className="text-body break-words whitespace-pre-wrap">{question}</p>
        {asked.attached.length > 0 && (
          <p className="flex flex-wrap gap-x-2 gap-y-0.5" aria-label="Attached">
            {asked.attached.map((word) => (
              <Tag key={word}>{word}</Tag>
            ))}
          </p>
        )}
      </div>
      <div className="min-w-0 space-y-2.5">
        {turn.parts.map((part, at) => (
          <PartView
            key={at}
            part={part}
            title={title}
            // The statement in an answer still coming in may not be whole.
            settled={settled || at < turn.parts.length - 1}
            active={current && turn.phase === "asking"}
            onOpen={onOpen}
            onInsert={onInsert}
            onAnswer={onAnswer}
          />
        ))}
        {(turn.phase === "waiting" || turn.phase === "thinking") && (
          <p role="status" className="text-body">
            <TextShimmer>{turn.phase === "thinking" ? "Thinking…" : "Asking…"}</TextShimmer>
          </p>
        )}
        {turn.phase === "stopped" && (
          <p className="text-hint text-muted-foreground">
            Stopped. Nothing of this answer is kept.
          </p>
        )}
        {turn.phase === "failed" && (
          <Notice tone="danger" title="No answer">
            {turn.error ?? "The assistant did not answer."}
          </Notice>
        )}
        {turn.phase === "refused" && (
          <Notice tone="warning" title="The model declined to answer this">
            Put the question another way. Nothing of this turn is kept.
          </Notice>
        )}
        {turn.phase === "cut" && (
          <p className="text-hint text-muted-foreground">
            The answer reached its length limit and was cut off.
          </p>
        )}
        {settled && turn.usage && (turn.usage.input > 0 || turn.usage.output > 0) && (
          <p className="numeric text-hint text-muted-foreground">
            {[
              turn.model,
              `${turn.usage.input.toLocaleString("en-US")} tokens in`,
              `${turn.usage.output.toLocaleString("en-US")} out`,
            ]
              .filter(Boolean)
              .join(" · ")}
          </p>
        )}
      </div>
    </li>
  )
}

function PartView({
  part,
  title,
  settled,
  active,
  onOpen,
  onInsert,
  onAnswer,
}: {
  part: Part
  title: string
  settled: boolean
  active: boolean
  onOpen: (sql: string, title: string) => void
  onInsert: (sql: string) => void
  onAnswer: (allow: boolean) => void
}) {
  if (part.kind === "text") {
    return (
      <Markdown
        source={part.text}
        code={(block) =>
          isStatementFence(block.lang) ? (
            <AnswerStatement
              sql={block.v.trim()}
              settled={settled}
              onOpen={() => onOpen(block.v.trim(), title)}
              onInsert={() => onInsert(block.v.trim())}
            />
          ) : (
            <Well className="max-h-96 p-2.5 text-hint whitespace-pre" aria-label={block.lang}>
              {block.v}
            </Well>
          )
        }
      />
    )
  }
  if (part.kind === "read") {
    return (
      <p className="text-hint text-muted-foreground">
        {part.tables.length > 0 ? `Read the structure of ${listOf(part.tables)}.` : "Read nothing."}
        {part.missing.length > 0 && ` No table is called ${listOf(part.missing, "or")}.`}
      </p>
    )
  }
  return <AskCard calls={part.calls} answer={part.answer} active={active} onAnswer={onAnswer} />
}

function listOf(names: string[], last = "and") {
  if (names.length <= 1) return names.join("")
  return `${names.slice(0, -1).join(", ")} ${last} ${names[names.length - 1]}`
}

/** A statement the assistant wrote, with the verbs that take it to the editor. */
function AnswerStatement({
  sql,
  settled,
  onOpen,
  onInsert,
}: {
  sql: string
  settled: boolean
  onOpen: () => void
  onInsert: () => void
}) {
  return (
    <div
      data-slot="assistant-statement"
      className="min-w-0 overflow-hidden rounded-lg border border-hairline bg-surface-sunken"
    >
      <div className="flex min-h-8 flex-wrap items-center gap-x-0.5 border-b border-hairline bg-surface-header py-0.5 pr-1 pl-2.5">
        <span className="eyebrow mr-auto">Statement</span>
        {settled && (
          <>
            <Button size="xs" variant="ghost" onClick={onOpen}>
              Open in a tab
            </Button>
            <Button size="xs" variant="ghost" onClick={onInsert}>
              Insert
            </Button>
            <Button
              size="xs"
              variant="ghost"
              aria-label="Copy the statement"
              onClick={() => void copyText(sql, "Statement copied")}
            >
              <Copy />
              <span className="max-sm:sr-only">Copy</span>
            </Button>
          </>
        )}
      </div>
      <pre className="max-h-72 overflow-auto p-2.5 font-mono text-hint leading-relaxed whitespace-pre">
        {sql}
      </pre>
    </div>
  )
}

/** The assistant asking to read more of the structure, and the person's answer. */
function AskCard({
  calls,
  answer,
  active,
  onAnswer,
}: {
  calls: ToolCall[]
  answer?: "allowed" | "declined"
  active: boolean
  onAnswer: (allow: boolean) => void
}) {
  const tables = [...new Set(calls.flatMap((call) => call.tables))]
  return (
    <div
      data-slot="assistant-ask"
      className="space-y-2 rounded-lg border border-hairline bg-card p-3"
    >
      <p className="text-body font-medium">It asks to read the structure of {listOf(tables)}</p>
      <p className="text-hint leading-relaxed text-muted-foreground">
        Names, types, keys and references are sent; no rows.
      </p>
      {answer ? (
        <p className="text-hint font-medium text-muted-foreground">
          {answer === "allowed" ? "Allowed" : "Not allowed"}
        </p>
      ) : (
        active && (
          <div className="flex flex-wrap gap-2 pt-0.5">
            <Button size="sm" onClick={() => onAnswer(true)}>
              Allow
            </Button>
            <Button size="sm" variant="outline" onClick={() => onAnswer(false)}>
              Don&rsquo;t allow
            </Button>
          </div>
        )
      )}
    </div>
  )
}

/**
 * The question being written: what it asks, what it carries, and where it
 * goes. A question asked while the assistant waits on an answer to its own
 * request declines that request.
 */
function Composer({
  mode,
  onMode,
  settings,
  schema,
  tables,
  attachable,
  busy,
  waiting,
  onAsk,
  onStop,
}: {
  mode: "sql" | "policy"
  onMode: (mode: "sql" | "policy") => void
  settings: AssistantSettings
  schema: string
  tables: SchemaTable[]
  attachable: AssistantContext
  busy: boolean
  waiting: ToolCall[]
  onAsk: (task: AssistantTask) => void
  onStop: () => void
}) {
  const ids = useId()
  const [prompt, setPrompt] = useState("")
  const [withStatement, setWithStatement] = useState(false)
  const [withResult, setWithResult] = useState(false)
  const [table, setTable] = useState("")
  const field = useRef<HTMLTextAreaElement>(null)
  const policyTables = useMemo(
    () => tables.filter((t) => !t.type || /table/i.test(t.type)).map((t) => t.name),
    [tables],
  )
  useEffect(() => {
    if (mode === "policy") field.current?.focus()
  }, [mode])

  const statement = attachable.statement.trim()
  const result = attachable.result
  const ready =
    !busy &&
    prompt.trim() !== "" &&
    (mode === "sql" || (table !== "" && policyTables.includes(table)))
  const send = () => {
    if (!ready) return
    onAsk(
      mode === "policy"
        ? { kind: "policy", prompt: prompt.trim(), table }
        : {
            kind: "sql",
            prompt: prompt.trim(),
            statement: withStatement && statement ? statement : undefined,
            result: withResult ? result : undefined,
          },
    )
    setPrompt("")
    setWithResult(false)
    if (mode === "policy") onMode("sql")
  }

  // The address was checked when it was saved, so it parses.
  const destination =
    settings.provider === "anthropic"
      ? "Anthropic"
      : settings.baseUrl
        ? new URL(settings.baseUrl).host
        : "the server"
  return (
    <form
      data-slot="assistant-composer"
      className="shrink-0 space-y-2 border-t border-hairline bg-card p-2.5"
      onSubmit={(event) => {
        event.preventDefault()
        send()
      }}
    >
      {mode === "policy" && (
        <div className="flex min-w-0 items-center gap-2">
          <label htmlFor={`${ids}-table`} className="shrink-0 text-xs font-medium">
            Policy on
          </label>
          <Select value={table} onValueChange={setTable}>
            <SelectTrigger id={`${ids}-table`} className="min-w-0 flex-1 font-mono text-xs">
              <SelectValue placeholder="Choose a table" />
            </SelectTrigger>
            <SelectContent>
              {policyTables.map((name) => (
                <SelectItem key={name} value={name} className="font-mono text-xs">
                  {name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <IconAction
            label="Ask a question instead"
            className="size-7 shrink-0 max-sm:size-8"
            onClick={() => onMode("sql")}
          >
            <Cross />
          </IconAction>
        </div>
      )}
      {waiting.length > 0 && (
        <FormNote>
          Asking something else declines the assistant&rsquo;s request to read tables.
        </FormNote>
      )}
      <Textarea
        ref={field}
        aria-label={mode === "policy" ? "Who may see or change which rows" : "Ask the assistant"}
        placeholder={
          mode === "policy"
            ? "Each customer sees only their own orders"
            : "Orders from the last 30 days with their customer's email"
        }
        value={prompt}
        maxLength={4000}
        rows={2}
        className="max-h-40 min-h-16 resize-none"
        onChange={(event) => setPrompt(event.target.value)}
        onKeyDown={(event) => {
          if (event.key === "Enter" && !event.shiftKey && !event.nativeEvent.isComposing) {
            event.preventDefault()
            send()
          }
        }}
      />
      <div className="flex min-w-0 flex-wrap items-center gap-1.5">
        {mode === "sql" && statement && (
          <Toggle
            size="sm"
            variant="outline"
            pressed={withStatement}
            onPressedChange={setWithStatement}
            aria-label="Attach the statement in the editor"
            className="h-7 px-2 text-xs data-[state=off]:text-muted-foreground"
          >
            Statement
          </Toggle>
        )}
        {mode === "sql" && result && result.columns.length > 0 && (
          <Toggle
            size="sm"
            variant="outline"
            pressed={withResult}
            onPressedChange={setWithResult}
            aria-label={`Attach the first ${result.rows.length} rows of the result`}
            className="h-7 px-2 text-xs data-[state=off]:text-muted-foreground"
          >
            Result · {result.rows.length.toLocaleString("en-US")}{" "}
            {result.rows.length === 1 ? "row" : "rows"}
          </Toggle>
        )}
        <span className="ml-auto flex items-center gap-1.5">
          {busy ? (
            <Button type="button" size="sm" variant="outline" onClick={onStop}>
              <StopCircle />
              Stop
            </Button>
          ) : (
            <Button type="submit" size="sm" disabled={!ready}>
              <PaperAirplane />
              {mode === "policy" ? "Draft" : "Ask"}
            </Button>
          )}
        </span>
      </div>
      {withResult && (
        <FormNote tone="warning">
          {`Sends ${destination} the question and the attached rows, with the structure of ${schema || "this database"}.`}
        </FormNote>
      )}
    </form>
  )
}
