"use client"

import Link from "next/link"
import { Copy, Fullscreen, SettingsGear, SidebarRightClose, Sparkles } from "@/components/icons"
import { copyText } from "@/lib/clipboard"
import type { PollState } from "@/hooks/use-poll"
import { IconAction } from "@/components/icon-action"
import { Skeleton } from "@/components/ui/skeleton"
import { Button } from "@/components/ui/button"
import { AssistantOff } from "@/components/assistant/off"
import type { AssistantSettings } from "@/components/assistant/settings"
import {
  AgentChat,
  type AgentCodeBlock,
  type AgentSuggestion,
} from "@/components/assistant/agent/chat"
import type { AgentSession } from "@/components/assistant/agent/sessions"
import { AssistantProtected } from "@/components/database/assistant"
import { ReadError } from "@/components/database/redis/read-error"
import { useDatabase } from "@/components/database/shell/database-context"
import {
  ERROR_QUESTION,
  PLAN_QUESTION,
  errorText,
  isStatementFence,
  planAttachmentText,
  queryAttachables,
  statementTitle,
  type QueryContext,
} from "@/components/database/query/attachments"

/**
 * The SQL editor's third column: the connection's own assistant — the chat its
 * Assistant page has in front, on the same session — beside the statement it is
 * about. A question here carries what the person presses of the tab (the
 * statement, the last result, the error, the plan), and a statement in a finished
 * answer gets the editor's verbs in words: open it in a tab, write it at the
 * cursor, copy it. It is run by the person's own Run, or asked for in the
 * conversation like anything else the assistant does.
 */
export function QueryAssistant({
  session,
  settings,
  context,
  canSetUp,
  onSetUp,
  onOpen,
  onInsert,
  onClose,
}: {
  session: AgentSession
  settings: PollState<AssistantSettings>
  context: QueryContext
  /** The role may choose the provider (`system.admin`). */
  canSetUp: boolean
  onSetUp: () => void
  /** Opens a statement in a tab of its own. */
  onOpen: (sql: string, title: string) => void
  /** Writes a statement at the cursor. */
  onInsert: (sql: string) => void
  onClose: () => void
}) {
  const { conn, engine, readOnly, href } = useDatabase()
  const view = settings.data
  const configured = view?.configured === true

  const suggestions: AgentSuggestion[] = []
  if (context.failed) {
    suggestions.push({
      label: "Explain the error",
      question: ERROR_QUESTION,
      attached: [{ label: "error", text: errorText(context.failed) }],
    })
  }
  if (context.plan) {
    suggestions.push({
      label: "Explain the plan",
      question: PLAN_QUESTION,
      attached: [{ label: "plan", text: planAttachmentText(context.plan) }],
    })
  }
  suggestions.push({
    label: "Write a query for…",
    question: "I want a query against this database. Ask me what it should return, then write it.",
  })
  if (engine.can("assistantPolicy")) {
    suggestions.push({
      label: "Draft a row-level security policy",
      question:
        "Help me write a row-level security policy. Ask me which table it is for and who should see or change which rows.",
    })
  }

  const statements: AgentCodeBlock = (block, answer) =>
    isStatementFence(block.lang) ? (
      <AnswerStatement
        sql={block.v.trim()}
        finished={answer.finished}
        onOpen={() => onOpen(block.v.trim(), statementTitle(answer.question))}
        onInsert={() => onInsert(block.v.trim())}
      />
    ) : null

  let body: React.ReactNode
  if (readOnly) {
    body = <AssistantProtected className="m-3" />
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
      <AssistantOff
        className="p-3"
        settings={view}
        onSetUp={onSetUp}
        what={`it reads ${conn.name} as you, writes ${engine.label} for it, and explains errors and plans.`}
      />
    )
  } else {
    body = (
      <AgentChat
        key={session.key}
        compact
        session={session}
        settings={view}
        title={`Ask about ${conn.name}`}
        description={`It writes ${engine.label} for this database and explains errors and plans. A statement it writes opens in a tab or at the cursor.`}
        placeholder="Orders from the last 30 days with their customer's email"
        suggestions={suggestions}
        sends={`what you attach and what it reads of ${conn.name} to answer`}
        attachable={queryAttachables(context)}
        codeBlock={statements}
      />
    )
  }

  return (
    <div data-slot="query-assistant" className="flex min-h-0 min-w-0 flex-1 flex-col">
      <div className="flex h-10 shrink-0 items-center gap-2 border-b border-hairline bg-surface-header pr-1.5 pl-3">
        <Sparkles aria-hidden className="size-3.5 shrink-0 text-muted-foreground" />
        <h2 className="min-w-0 truncate text-body font-medium">Assistant</h2>
        <span className="ml-auto flex shrink-0 items-center gap-0.5">
          {/* The page has this same chat in front, with the rest of them beside it. */}
          {configured && !readOnly && (
            <IconAction
              asChild
              label="Open the assistant's page, with its chats"
              className="size-7 max-sm:size-8"
            >
              <Link href={href("assistant")}>
                <Fullscreen />
              </Link>
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

/**
 * A statement the assistant wrote, with the verbs that take it to the editor. They
 * are drawn once the answer has finished, because a statement still arriving may
 * not be whole.
 */
function AnswerStatement({
  sql,
  finished,
  onOpen,
  onInsert,
}: {
  sql: string
  finished: boolean
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
        {finished && (
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
