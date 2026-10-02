"use client"

import { useEffect, useRef, useState } from "react"
import { CornerDownLeft } from "@/components/icons"
import { errorMessage } from "@/lib/api"
import { useMemoryState } from "@/lib/view-state"
import { cn } from "@/lib/utils"
import type { ConfirmRequest } from "@/components/confirm-dialog"
import { FormFact } from "@/components/form"
import { Well } from "@/components/panel"
import { Spinner } from "@/components/state"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { EngineMark } from "@/components/database/kit"
import { redisClassify, redisCommand } from "@/components/database/redis/api"
import {
  accept,
  commandOf,
  completions,
  remember,
} from "@/components/database/redis/console/complete"
import { Reply } from "@/components/database/redis/console/reply"
import type {
  RedisClassifyResponse,
  RedisCommandClass,
  RedisCommandRef,
  RedisReply,
} from "@/components/database/redis/types"
import type { Redis } from "@/components/database/redis/use-redis"

/** One line of the transcript: what was typed, and what came of it. */
export type ConsoleEntry = {
  id: number
  command: string
  db: number | undefined
  /** How the server classed it, once it had. */
  class?: RedisCommandClass
  /** The round trip to the server, in milliseconds. */
  ms?: number
  reply?: RedisReply
  /** The server cut the reply short. */
  truncated?: boolean
  /** Why it was not run, or why the run failed: a sentence, in place of a reply. */
  refused?: string
  /** The view of this page that does what the line asked for, where there is one. */
  elsewhere?: ConsoleElsewhere
}

/** The two things a console line cannot do, and the view each has. */
export type ConsoleElsewhere = "pubsub" | "monitor"

/**
 * Commands the console refuses because a view of this page is built for
 * them. The server's sentence for MONITOR names a page this product does not
 * have ("the Profiler"), so that one is said here, with the view it means.
 */
const ELSEWHERE: Record<string, { view: ConsoleElsewhere; why?: string }> = {
  MONITOR: {
    view: "monitor",
    why: "streams every command the server runs, which one console line cannot hold",
  },
  SUBSCRIBE: { view: "pubsub" },
  PSUBSCRIBE: { view: "pubsub" },
  SSUBSCRIBE: { view: "pubsub" },
}

const KEEP = 60

/** The tag a command's class takes: a read is the ordinary case and says nothing. */
export function ClassTag({ value, admin }: { value: RedisCommandClass; admin?: boolean }) {
  return (
    <>
      {value !== "read" && (
        <Tag tone={value === "write" ? "default" : value === "dangerous" ? "warning" : "danger"}>
          {value === "write" ? "writes" : value === "dangerous" ? "removes data" : "not run here"}
        </Tag>
      )}
      {admin && <Tag>admin</Tag>}
    </>
  )
}

const CAPABILITY: Record<string, string> = {
  "service.control": "service control",
  destructive: "the destructive capability",
  "system.admin": "administrator rights",
}

/**
 * The console: a line typed, the server's answer under it.
 *
 * Every line is put to the server twice. First it is asked what the command
 * is (`classify`): a command that cannot run over one request is refused
 * with the reason, one the reader's role may not run says which capability
 * it wants, and one that removes data — or that may stall every other
 * client — is confirmed with the command and the reasons on screen. Only
 * then is it sent. The server decides again when it runs; this is so the
 * reader knows before pressing Enter twice.
 *
 * The transcript and the history live in memory for the tab: a console line
 * may carry a value, and nothing typed here is written to the browser's
 * storage.
 */
export function ConsoleView({
  redis,
  commands,
  line,
  onLine,
  confirm,
  views,
  onView,
}: {
  redis: Redis
  /** The views this page offers this reader beside the console. */
  views: readonly ConsoleElsewhere[]
  onView: (view: ConsoleElsewhere) => void
  /** The server's command reference, once it has arrived. */
  commands: readonly RedisCommandRef[]
  /** The prompt's text, held by the page so the reference pane can write into it. */
  line: string
  onLine: (line: string) => void
  confirm: (request: ConfirmRequest) => void
}) {
  const { id, target, db, conn, engine, readOnly } = redis
  const [entries, setEntries] = useMemoryState<ConsoleEntry[]>(
    `databases.${id}.redis.console.log`,
    [],
  )
  const [history, setHistory] = useMemoryState<string[]>(
    `databases.${id}.redis.console.history`,
    [],
  )
  const [busy, setBusy] = useState(false)
  /** How far back in the history the prompt is; 0 is the line being typed. */
  const [back, setBack] = useState(0)
  const [typing, setTyping] = useState(false)
  const [active, setActive] = useState(0)
  const input = useRef<HTMLInputElement>(null)
  const scroller = useRef<HTMLDivElement>(null)

  const options = typing ? completions(line, commands) : []
  const named = commandOf(line, commands)
  const prompt = `db${db ?? ""}>`

  // The newest line is the one being read: keep it in view as lines arrive.
  useEffect(() => {
    const el = scroller.current
    if (el) el.scrollTop = el.scrollHeight
  }, [entries.length, busy])

  const add = (entry: Omit<ConsoleEntry, "id" | "db">) =>
    setEntries((held) => [
      ...held.slice(-(KEEP - 1)),
      { ...entry, db, id: (held[held.length - 1]?.id ?? 0) + 1 },
    ])

  const execute = async (command: string) => {
    try {
      const result = await redisCommand(target, command)
      add({
        command,
        class: result.class,
        ms: result.ms,
        reply: result.reply,
        truncated: result.truncated,
      })
    } catch (err) {
      add({ command, refused: errorMessage(err) })
    }
  }

  const refusal = (verdict: RedisClassifyResponse): string | null => {
    const there = Object.hasOwn(ELSEWHERE, verdict.name) ? ELSEWHERE[verdict.name] : undefined
    const why = there?.why ?? verdict.reasons.join("; ")
    if (verdict.class === "blocked") {
      return there?.why && views.includes(there.view)
        ? `Not run: ${verdict.name} ${why}. The Monitor view is built for it.`
        : `Not run: ${verdict.name} ${why}.`
    }
    if (!verdict.allowed) {
      const wants = verdict.requires.map((need) => CAPABILITY[need] ?? need)
      return `Not run: ${verdict.name} ${why ? `${why}, and ` : ""}needs ${wants.join(" and ")}, which your role does not have.`
    }
    if (readOnly && verdict.class !== "read") {
      return `Not run: this connection is protected, and ${verdict.name} ${why || "changes data"}.`
    }
    return null
  }

  const run = async () => {
    const command = line.trim()
    if (!command || busy) return
    setHistory((held) => remember(held, command))
    setBack(0)
    setTyping(false)
    onLine("")
    setBusy(true)
    try {
      const verdict = await redisClassify(target, command)
      const refused = refusal(verdict)
      if (refused) {
        const there =
          verdict.class === "blocked" && Object.hasOwn(ELSEWHERE, verdict.name)
            ? ELSEWHERE[verdict.name]
            : undefined
        add({
          command,
          class: verdict.class,
          refused,
          ...(there && views.includes(there.view) ? { elsewhere: there.view } : {}),
        })
      } else if (verdict.class === "dangerous" || verdict.slow) {
        confirm({
          title:
            verdict.class !== "dangerous"
              ? "Run a slow command"
              : verdict.known
                ? "Run a command that removes data"
                : // Classed as dangerous because nobody could say what it does.
                  "Run a command nobody recognises",
          subject: {
            mark: <EngineMark engine={engine} size="sm" />,
            name: conn.name,
            facts: <FormFact label="Database">db{db}</FormFact>,
          },
          description: (
            <>
              <Well className="max-h-40 break-all whitespace-pre-wrap">{command}</Well>
              <p>
                {verdict.name}{" "}
                {verdict.reasons.join("; ") || "changes data in a way that cannot be undone"}.
              </p>
            </>
          ),
          confirmLabel: "Run command",
          action: async () => {
            await execute(command)
            return "reported"
          },
          onDone: () => input.current?.focus(),
        })
      } else {
        await execute(command)
      }
    } catch (err) {
      add({ command, refused: errorMessage(err) })
    } finally {
      setBusy(false)
      input.current?.focus()
    }
  }

  const recall = (step: number) => {
    const next = Math.min(Math.max(back + step, 0), history.length)
    if (next === back) return
    setBack(next)
    setTyping(false)
    onLine(next === 0 ? "" : history[history.length - next])
  }

  const take = (command: RedisCommandRef) => {
    onLine(accept(command))
    setTyping(true)
    setActive(0)
  }

  const onKeyDown = (event: React.KeyboardEvent<HTMLInputElement>) => {
    const open = options.length > 0
    if (event.key === "Enter") {
      event.preventDefault()
      void run()
    } else if (event.key === "Tab" && open) {
      event.preventDefault()
      take(options[Math.min(active, options.length - 1)])
    } else if (event.key === "ArrowUp") {
      event.preventDefault()
      if (open) setActive((n) => (n + options.length - 1) % options.length)
      else recall(1)
    } else if (event.key === "ArrowDown") {
      event.preventDefault()
      if (open) setActive((n) => (n + 1) % options.length)
      else recall(-1)
    } else if (event.key === "Escape" && open) {
      event.preventDefault()
      setTyping(false)
    } else if (event.key.toLowerCase() === "l" && event.ctrlKey) {
      event.preventDefault()
      setEntries([])
    }
  }

  const listId = `redis-console-options-${id}`

  return (
    <div className="flex min-h-0 min-w-0 flex-1 flex-col">
      <div
        ref={scroller}
        data-slot="redis-transcript"
        role="log"
        aria-label="Console transcript"
        tabIndex={0}
        className="min-h-0 flex-1 space-y-3 overflow-auto bg-surface-sunken p-3 font-mono text-xs leading-relaxed focus-ring-inset"
      >
        {entries.length === 0 && !busy ? (
          <div className="flex h-full flex-col items-center justify-center gap-3 font-sans">
            <EngineMark engine={engine} />
            <div className="space-y-1 text-center">
              <p className="text-body font-medium">Nothing has been run yet</p>
              <p className="mx-auto max-w-md text-xs leading-relaxed text-muted-foreground">
                {readOnly
                  ? "This connection is protected: commands that read are run, and the rest are refused. Tab completes a name, Up and Down walk the history."
                  : "Type a command below. Tab completes its name, Up and Down walk the history, and a command that removes data asks first."}
              </p>
            </div>
            <div className="flex flex-wrap justify-center gap-1.5">
              {["PING", "DBSIZE", "INFO keyspace", "SCAN 0 COUNT 20"].map((example) => (
                <Button
                  key={example}
                  size="xs"
                  variant="outline"
                  className="font-mono"
                  onClick={() => {
                    onLine(example)
                    input.current?.focus()
                  }}
                >
                  {example}
                </Button>
              ))}
            </div>
          </div>
        ) : (
          entries.map((entry) => <Entry key={entry.id} entry={entry} onView={onView} />)
        )}
        {busy && (
          <div className="flex items-center gap-2 text-muted-foreground">
            <Spinner className="size-3" />
            Waiting for the server…
          </div>
        )}
      </div>

      <div className="relative shrink-0 border-t border-hairline">
        {options.length > 0 && (
          <ul
            id={listId}
            role="listbox"
            aria-label="Commands"
            className="absolute bottom-full left-2 z-20 mb-1 max-h-64 w-[min(34rem,calc(100%-1rem))] overflow-auto rounded-md border bg-popover p-1 shadow-md"
          >
            {options.map((option, at) => (
              <li
                key={option.name}
                id={`${listId}-${at}`}
                role="option"
                aria-selected={at === active}
                // The pointer picks without taking the keyboard off the prompt.
                onMouseDown={(event) => {
                  event.preventDefault()
                  take(option)
                }}
                onMouseEnter={() => setActive(at)}
                className={cn(
                  "flex min-w-0 cursor-pointer items-baseline gap-3 rounded-sm px-2 py-1",
                  at === active && "bg-menu-hover",
                )}
              >
                <span className="shrink-0 font-mono text-xs font-medium">{option.name}</span>
                <span className="min-w-0 flex-1 truncate text-hint text-muted-foreground">
                  {option.summary ?? option.group}
                </span>
              </li>
            ))}
          </ul>
        )}
        <div
          data-slot="redis-syntax"
          aria-live="polite"
          className="flex min-h-7 min-w-0 items-center gap-2 border-b border-hairline px-3 text-hint text-muted-foreground"
        >
          {named ? (
            <>
              <span className="min-w-0 truncate font-mono">
                <span className="text-foreground">{named.name}</span>
                {named.syntax ? ` ${named.syntax}` : ""}
              </span>
              <span className="flex shrink-0 items-center gap-2">
                <ClassTag value={named.class} admin={named.admin} />
              </span>
              {named.summary && (
                <span className="min-w-0 flex-1 truncate text-right max-md:hidden">
                  {named.summary}
                </span>
              )}
            </>
          ) : (
            <span className="truncate">
              Enter runs the line · Tab completes a command · Ctrl+L clears the transcript
            </span>
          )}
        </div>
        <div className="flex items-center gap-2 px-3 py-2">
          <label
            htmlFor={`redis-console-${id}`}
            className="shrink-0 font-mono text-xs text-muted-foreground"
          >
            {prompt}
          </label>
          <input
            ref={input}
            id={`redis-console-${id}`}
            role="combobox"
            aria-expanded={options.length > 0}
            aria-controls={listId}
            aria-autocomplete="list"
            aria-activedescendant={options.length > 0 ? `${listId}-${active}` : undefined}
            aria-label="Command"
            autoFocus
            value={line}
            disabled={busy}
            spellCheck={false}
            autoComplete="off"
            autoCapitalize="off"
            placeholder="GET session:00001"
            onChange={(event) => {
              onLine(event.target.value)
              setTyping(true)
              setActive(0)
              setBack(0)
            }}
            onKeyDown={onKeyDown}
            onBlur={() => setTyping(false)}
            className="h-7 min-w-0 flex-1 bg-transparent font-mono text-xs outline-none placeholder:text-muted-foreground/60 max-sm:text-base"
          />
          <Button size="xs" pending={busy} disabled={!line.trim()} onClick={() => void run()}>
            <CornerDownLeft />
            Run
          </Button>
        </div>
      </div>
    </div>
  )
}

function Entry({
  entry,
  onView,
}: {
  entry: ConsoleEntry
  onView: (view: ConsoleElsewhere) => void
}) {
  return (
    <div data-slot="redis-console-entry" className="min-w-0 animate-rise">
      <div className="flex min-w-0 items-baseline gap-2">
        <span className="shrink-0 text-muted-foreground select-none">db{entry.db ?? ""}&gt;</span>
        <span className="min-w-0 flex-1 font-medium break-all whitespace-pre-wrap">
          {entry.command}
        </span>
        <span className="flex shrink-0 items-center gap-2 font-sans text-hint text-muted-foreground">
          {entry.class && <ClassTag value={entry.class} />}
          {entry.ms !== undefined && (
            <span className="numeric">
              {entry.ms < 10 ? entry.ms.toFixed(2) : Math.round(entry.ms)} ms
            </span>
          )}
        </span>
      </div>
      <div className="mt-0.5 min-w-0">
        {entry.refused ? (
          <span className="flex flex-wrap items-center gap-x-3 gap-y-1 font-sans">
            <span className="text-warning">{entry.refused}</span>
            {entry.elsewhere && (
              <Button
                size="xs"
                variant="outline"
                onClick={() => onView(entry.elsewhere as ConsoleElsewhere)}
              >
                {entry.elsewhere === "monitor" ? "Open Monitor" : "Open Pub/Sub"}
              </Button>
            )}
          </span>
        ) : (
          entry.reply && <Reply reply={entry.reply} />
        )}
        {entry.truncated && (
          <p className="font-sans text-hint text-muted-foreground">
            The reply was larger than what is shown.
          </p>
        )}
      </div>
    </div>
  )
}
