"use client"

import { useMemo } from "react"
import Link from "next/link"
import {
  Archive,
  ChartActivity,
  CodeBracket,
  Database,
  Gauge,
  LockClosed,
  Logs,
  Table,
  Users,
} from "@/components/icons"
import { cn } from "@/lib/utils"
import { EmptyState } from "@/components/state"
import { Button } from "@/components/ui/button"
import type { AgentSuggestion } from "@/components/assistant/agent/chat"
import type { AgentScope } from "@/components/assistant/agent/sessions"
import { SubjectAssistant } from "@/components/assistant/agent/subject-page"
import type { Engine } from "@/components/database/engine"
import { SectionFrame } from "@/components/database/kit/section"
import { useDatabase } from "@/components/database/shell/database-context"

/** The chat in front for one connection, which its page and its SQL editor share. */
export function databaseScope(id: number, name: string): AgentScope {
  return { kind: "database", databaseId: id, name }
}

/**
 * Questions a person asks of any database, in its engine's own words: a Redis
 * server has keys and commands where PostgreSQL has tables and statements.
 */
export function databaseSuggestions(engine: Engine): AgentSuggestion[] {
  const { nouns } = engine
  const sql = engine.kind === "sql"
  return [
    {
      label: "What's in this database?",
      question: `What does this database hold? Name its ${nouns.containers} and its largest ${nouns.objects}, with how many ${nouns.rows} each has.`,
      product: engine.logo,
      icon: Database,
    },
    {
      label: `Which ${nouns.objects} are biggest?`,
      question: `Which ${nouns.objects} take the most space, and is any of them growing fast?`,
      icon: Table,
    },
    {
      label: sql ? "Find slow queries" : `Find slow ${nouns.statements}`,
      question: `Which ${nouns.statements} are slowest or run most often, and what would make them faster?`,
      icon: ChartActivity,
    },
    {
      label: "Is it healthy?",
      question:
        "Is this database healthy right now: its connections, memory and disk, and anything wrong in its log?",
      icon: Gauge,
    },
    {
      label: "Who can write to it?",
      question: "Which accounts can sign in to this database, and which of them can change data?",
      icon: Users,
    },
    {
      label: "Is it backed up?",
      question:
        "Is this database backed up? When did the last dump run, where is it kept, and is there a schedule?",
      icon: Archive,
    },
    {
      label: sql ? "Write a query for…" : `Write a ${nouns.statement} for…`,
      question: `I want a ${sql ? "query" : nouns.statement} against this database. Ask me what it should do, then write it for me.`,
      icon: CodeBracket,
    },
    {
      label: "Check its log for errors",
      question: "Read this database's server log and tell me about any errors or warnings in it.",
      icon: Logs,
    },
  ]
}

/**
 * What a protected connection says where its assistant would be: nothing about it
 * is sent while it is protected, and where that is turned off. The page and the
 * column beside the SQL editor say the same.
 */
export function AssistantProtected({ className }: { className?: string }) {
  const { conn, href } = useDatabase()
  return (
    <EmptyState
      className={cn("border-0 px-3 py-10", className)}
      icon={LockClosed}
      title="Off for a protected connection"
      description={`Nothing about ${conn.name} is sent to the assistant while it is protected. Protection is turned off in its settings.`}
      action={
        <Button asChild size="sm" variant="outline">
          <Link href={href("settings")}>Open settings</Link>
        </Button>
      }
    />
  )
}

/**
 * A connection's assistant, as a page of its own beside its Home: the same
 * conversation as a deployment's (`SubjectAssistant`), about this database, and
 * shared with the column beside its SQL editor — a chat begun in one is the chat
 * in front in the other.
 */
export function DatabaseAssistant() {
  const { id, conn, engine, readOnly } = useDatabase()
  const scope = useMemo(() => databaseScope(id, conn.name), [id, conn.name])
  const suggestions = useMemo(() => databaseSuggestions(engine), [engine])
  const { nouns } = engine
  return (
    // The conversation is the page's ground from the strip's hairline to the foot
    // of the window, as a deployment's is under its header.
    <SectionFrame section="assistant" className="gap-0 py-0 md:gap-0 md:py-0">
      <SubjectAssistant
        slot="database-assistant"
        scope={scope}
        chats={{ database: id }}
        refusal={readOnly && <AssistantProtected className="m-auto" />}
        title={`Ask anything about ${conn.name}`}
        description={`It can read its ${nouns.objects}, ${nouns.statements}, accounts, backups and log, write ${nouns.statements} for you and change what you ask it to, as you and under your role.`}
        placeholder={`Ask anything about ${conn.name}…`}
        sends={`what it reads of ${conn.name} to answer`}
        what="it can read this database, answer anything about it, and change it for you."
        suggestions={suggestions}
      />
    </SectionFrame>
  )
}
