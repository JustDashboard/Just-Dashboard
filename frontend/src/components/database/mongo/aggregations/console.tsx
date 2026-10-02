"use client"

import { useEffect, useMemo, useRef, useState } from "react"
import { CornerDownLeft, LockClosed, Trash } from "@/components/icons"
import { errorMessage } from "@/lib/api"
import { cn } from "@/lib/utils"
import { useMemoryState } from "@/lib/view-state"
import { usePoll } from "@/hooks/use-poll"
import type { ConfirmRequest } from "@/components/confirm-dialog"
import { FormFact, FormFacts } from "@/components/form"
import { IconAction } from "@/components/icon-action"
import { SearchInput } from "@/components/page"
import { Well } from "@/components/panel"
import { EmptyNote, EmptyState, LoadingRows } from "@/components/state"
import { Button } from "@/components/ui/button"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { EngineMark } from "@/components/database/kit"
import { classifyCommand, mongoCommands, runCommand } from "@/components/database/mongo/api"
import { parseDocument } from "@/components/database/mongo/bson"
import { BsonTree } from "@/components/database/mongo/bson-tree"
import { CodeField } from "@/components/database/mongo/code-field"
import { effectColor } from "@/components/database/mongo/kinds"
import { DatabaseMark } from "@/components/database/mongo/rail"
import { shapeProblem } from "@/components/database/mongo/shell"
import type {
  MongoCapability,
  MongoClassification,
  MongoCommandClass,
  MongoVerdict,
} from "@/components/database/mongo/types"
import type { Workbench } from "@/components/database/mongo/workbench"
import { ReadError } from "@/components/database/redis/read-error"

const capitalised = (text: string) => text.charAt(0).toUpperCase() + text.slice(1)

/** How many commands the transcript keeps. */
const KEPT = 40

/**
 * What a command does, as the server classes it, in the hue the section
 * gives a statement of that effect wherever statements are listed. A command
 * that is never run has no effect, and no hue.
 */
const CLASS: Record<MongoCommandClass, { word: string; color: string | undefined }> = {
  read: { word: "reads", color: effectColor("read") },
  write: { word: "writes", color: effectColor("change") },
  destructive: { word: "removes or stops", color: effectColor("remove") },
  blocked: { word: "never run", color: undefined },
}

const NEEDS: Record<MongoCapability, string> = {
  "service.control": "the permission to control services",
  "system.admin": "an administrator",
  destructive: "the permission to remove data",
}

type Entry = {
  at: number
  database: string
  command: string
  verdict?: MongoVerdict
  /** The reply as canonical Extended JSON. */
  reply?: string
  more?: boolean
  durationMs?: number
  error?: string
}

/**
 * The command console: one command document, run with `runCommand`.
 *
 * The server decides what a command is before it is dialled — a read, a
 * write, a removal, or one it never runs — and says so as the command is
 * typed, with what it would take to run it. A removal is confirmed with the
 * server's own reason on screen; a command the role or a protected
 * connection cannot run is not offered at all, and the line under the prompt
 * says why. Every command runs on a connection of its own: there is no
 * session to carry a transaction or a cursor between two of them.
 *
 * Enter runs the command. Pressed before the server has said what the
 * command is, the press is kept: the command is classed at once and runs as
 * soon as the answer allows it, rather than the key doing nothing. A role
 * that cannot run commands is not handed a prompt at all — only the
 * reference of what each command does.
 */
export function ConsoleView({
  mongo,
  catalog,
  confirm,
}: Pick<Workbench, "mongo" | "catalog" | "confirm">) {
  const { id, engine, readOnly, canRun, conn } = mongo
  const [database, setDatabase] = useState(mongo.database || conn.database || "admin")
  const [text, setText] = useState("")
  const [classified, setClassified] = useState<{
    key: string
    data?: MongoClassification
    error?: string
  }>()
  const [busy, setBusy] = useState(false)
  // The command Enter was pressed on while its class was still unknown (`key`).
  const [pressed, setPressed] = useState<string | null>(null)
  // Replies can hold anything the database holds: kept for the page's life, never written down.
  const [log, setLog] = useMemoryState<Entry[]>(`databases.${id}.mongo.console.log`, [])
  const [filter, setFilter] = useState("")
  const end = useRef<HTMLDivElement>(null)
  const commands = usePoll((signal) => mongoCommands(id, signal), 0, [id])

  const shape = text.trim() ? shapeProblem(text, "document") : null
  const key = `${database}\u0000${text}`
  // A press that is waiting for the answer does not wait for the typing pause as well.
  const eager = pressed === key
  useEffect(() => {
    if (!text.trim() || shape) return
    const controller = new AbortController()
    const timer = setTimeout(
      () => {
        classifyCommand(id, database, text, controller.signal)
          .then((data) => {
            setClassified({ key, data })
            if (!eager) return
            // The press that was kept for this answer is acted on now.
            setPressed(null)
            act.current(data)
          })
          .catch((err: unknown) => {
            if (controller.signal.aborted) return
            setClassified({ key, error: errorMessage(err) })
          })
      },
      eager ? 0 : 300,
    )
    return () => {
      clearTimeout(timer)
      controller.abort()
    }
  }, [id, database, text, shape, key, eager])

  const current = classified?.key === key ? classified : undefined
  const verdict = current?.data?.verdict
  const protectedRefusal = readOnly && verdict !== undefined && verdict.class !== "read"
  const allowed = Boolean(current?.data?.allowed) && !protectedRefusal && canRun
  const why = !text.trim()
    ? null
    : shape
      ? shape
      : current?.error
        ? current.error
        : !current?.data
          ? null
          : verdict?.class === "blocked"
            ? verdict.reason
              ? `${capitalised(verdict.reason)}.`
              : "This command is never run from here."
            : protectedRefusal
              ? "The connection is protected: only a command that reads is run."
              : !current.data.allowed || !canRun
                ? `Running it needs ${current.data.requires.map((need) => NEEDS[need]).join(" and ")}, which your role does not have.`
                : null

  const send = async (verdict: MongoVerdict) => {
    const sent = text
    const asked = { at: Date.now(), database, command: sent.trim() }
    setBusy(true)
    try {
      const answer = await runCommand(id, database, sent)
      setLog((held) =>
        [
          ...held,
          {
            ...asked,
            database: answer.database,
            verdict: answer.verdict,
            reply: answer.reply.canonical,
            more: answer.reply.more,
            durationMs: answer.reply.durationMs,
          },
        ].slice(-KEPT),
      )
      // The prompt is cleared of the command that ran, not of one typed while it ran.
      setText((now) => (now === sent ? "" : now))
    } catch (err) {
      setLog((held) => [...held, { ...asked, verdict, error: errorMessage(err) }].slice(-KEPT))
    } finally {
      setBusy(false)
      requestAnimationFrame(() => end.current?.scrollIntoView({ block: "nearest" }))
    }
  }

  /** Runs the command on screen as the server classed it: at once, after asking, or not at all. */
  const run = (data: MongoClassification) => {
    const { verdict } = data
    const may = data.allowed && canRun && !(readOnly && verdict.class !== "read")
    if (!may || busy) return
    if (verdict.class !== "destructive") {
      void send(verdict)
      return
    }
    const request: ConfirmRequest = {
      title: `Run ${verdict.command}`,
      description: (
        <>
          <p>
            The server classes this command as one that removes or stops something
            {verdict.reason ? `: ${verdict.reason}.` : "."}
            {!verdict.known && " It is not a command the dashboard knows, so it is treated as one."}
          </p>
          <Well className="mt-2 max-h-40 overflow-auto text-hint whitespace-pre-wrap">
            {text.trim()}
          </Well>
        </>
      ),
      subject: {
        mark: <EngineMark engine={engine} size="sm" />,
        name: <span className="font-mono">{verdict.target ?? verdict.command}</span>,
        facts: (
          <FormFacts>
            <FormFact label="Database" mono>
              {database}
            </FormFact>
            <FormFact label="Command" mono>
              {verdict.command}
            </FormFact>
          </FormFacts>
        ),
      },
      confirmLabel: `Run ${verdict.command}`,
      action: async () => {
        await send(verdict)
        return "reported"
      },
    }
    confirm(request)
  }
  // What the classification's answer calls when a press was waiting for it:
  // the run of the render that is on screen when it lands.
  const act = useRef(run)
  useEffect(() => {
    act.current = run
  })

  const press = () => {
    if (busy || !canRun || !text.trim() || shape) return
    if (current?.data) run(current.data)
    // Not classed yet: the press is kept, and acted on when the answer lands.
    else if (!current) setPressed(key)
  }

  // A kept press is for the command it was made on: text typed since lets it go.
  if (pressed !== null && pressed !== key) setPressed(null)
  const waiting = pressed === key && !current

  const needle = filter.trim().toLowerCase()
  const reference = useMemo(() => {
    // The server lists a command under each spelling it accepts (buildInfo,
    // buildinfo): one row for each command, in the spelling its manual uses.
    const spelled = new Map<string, MongoVerdict>()
    for (const entry of commands.data ?? []) {
      if (entry.class === "blocked") continue
      const name = entry.command.toLowerCase()
      const held = spelled.get(name)
      if (!held || (held.command === name && entry.command !== name)) spelled.set(name, entry)
    }
    return [...spelled.values()].filter(
      (entry) => !needle || entry.command.toLowerCase().includes(needle),
    )
  }, [commands.data, needle])
  const databases = catalog.databases.data?.map((entry) => entry.name) ?? []
  const listedDatabases = databases.includes(database) ? databases : [database, ...databases]

  return (
    <div data-slot="mongo-console" className="flex min-h-0 min-w-0 flex-1">
      <div className="flex min-h-0 min-w-0 flex-1 flex-col">
        <div
          role="log"
          aria-label="Commands run and their replies"
          className="min-h-0 flex-1 space-y-3 overflow-auto p-3"
        >
          {!canRun ? (
            <EmptyState
              icon={LockClosed}
              className="border-0"
              title="Your role cannot run commands"
              description="Running a command takes the permission to control services. What each command would do is in the list beside this."
            />
          ) : log.length === 0 ? (
            <EmptyNote className="py-10 text-pretty">
              Type a command document below — <span className="font-mono">{"{ ping: 1 }"}</span>,{" "}
              <span className="font-mono">{'{ collStats: "orders" }'}</span> — and its reply is
              drawn here. What a command does is said before it runs.
            </EmptyNote>
          ) : (
            log.map((entry) => (
              <TranscriptEntry
                key={entry.at}
                entry={entry}
                onAgain={() => {
                  setText(entry.command)
                  setDatabase(entry.database)
                }}
              />
            ))
          )}
          <div ref={end} />
        </div>

        {/* No prompt for a role that cannot run what is typed into it. */}
        {canRun && (
          <div className="shrink-0 space-y-1.5 border-t border-hairline bg-surface-header p-2.5">
            <div className="flex min-w-0 items-start gap-2">
              <Select value={database} onValueChange={setDatabase}>
                <SelectTrigger
                  size="sm"
                  aria-label="Database the command runs in"
                  className="h-8 max-w-44 gap-1.5 px-2 font-mono text-xs data-[size=sm]:h-8 sm:data-[size=sm]:h-8"
                >
                  <SelectValue />
                </SelectTrigger>
                <SelectContent position="popper" align="start" className="max-h-72">
                  {listedDatabases.map((name) => (
                    <SelectItem key={name} value={name} className="font-mono text-xs">
                      <DatabaseMark name={name} />
                      {name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              <CodeField
                dense
                value={text}
                invalid={Boolean(shape)}
                aria-label="Command"
                aria-describedby="mongo-console-verdict"
                placeholder="{ ping: 1 }"
                className="min-w-0 flex-1"
                onChange={setText}
                onSubmit={press}
              />
              <Button
                size="sm"
                className="h-8"
                pending={busy || waiting}
                // Before the answer the button is the kept press; after it, only what may run.
                disabled={!text.trim() || Boolean(shape) || (current !== undefined && !allowed)}
                onClick={press}
              >
                <CornerDownLeft />
                Run
              </Button>
              <IconAction
                label="Clear the transcript"
                className="size-8"
                disabled={log.length === 0}
                onClick={() => setLog([])}
              >
                <Trash />
              </IconAction>
            </div>
            <p
              id="mongo-console-verdict"
              aria-live="polite"
              className="flex min-h-5 min-w-0 flex-wrap items-center gap-x-2 text-hint text-muted-foreground"
            >
              {verdict && !shape && (
                <span className="flex shrink-0 items-center gap-1.5">
                  <span className="font-mono text-xs text-foreground">{verdict.command}</span>{" "}
                  <span style={{ color: CLASS[verdict.class].color }}>
                    {CLASS[verdict.class].word}
                  </span>{" "}
                  {verdict.target && <span className="font-mono">{verdict.target} </span>}
                  {verdict.admin && <span>(an administrator&rsquo;s command) </span>}
                </span>
              )}
              {why ? (
                <span className={cn("min-w-0", (shape || current?.error) && "text-destructive")}>
                  {why}
                </span>
              ) : verdict?.class === "destructive" ? (
                <span className="min-w-0">
                  {verdict.reason ? `${capitalised(verdict.reason)}. ` : ""}It is asked for once
                  more before it runs.
                </span>
              ) : null}
            </p>
          </div>
        )}
      </div>

      <aside
        aria-label="Commands the console knows"
        className="flex w-64 shrink-0 flex-col border-l border-hairline max-lg:hidden"
      >
        <div className="shrink-0 border-b border-hairline p-1.5">
          <SearchInput
            dense
            value={filter}
            placeholder="Find a command"
            aria-label="Find a command"
            containerClassName="sm:w-full"
            onChange={(event) => setFilter(event.target.value)}
          />
        </div>
        <div className="min-h-0 flex-1 overflow-y-auto p-1.5">
          {commands.error && !commands.data ? (
            <ReadError error={commands.error} onRetry={commands.refresh} />
          ) : !commands.data ? (
            <LoadingRows rows={8} className="p-1.5" />
          ) : reference.length === 0 ? (
            <EmptyNote className="py-6">No command is called that.</EmptyNote>
          ) : (
            <ul>
              {reference.map((entry) => (
                <li key={entry.command}>
                  <button
                    type="button"
                    title={canRun ? `Write { ${entry.command}: 1 } into the prompt` : undefined}
                    // Without a prompt there is nothing to write it into: the row is then a reading.
                    disabled={!canRun}
                    onClick={() => setText(`{ ${entry.command}: 1 }`)}
                    className="flex h-7 w-full items-center gap-2 rounded-md px-2 text-left focus-ring-inset enabled:hover:bg-row-hover"
                  >
                    <span className="min-w-0 flex-1 truncate font-mono text-xs">
                      {entry.command}
                    </span>
                    <span
                      className="shrink-0 text-hint"
                      style={{ color: CLASS[entry.class].color }}
                    >
                      {CLASS[entry.class].word}
                      {entry.admin ? " · admin" : ""}
                    </span>
                  </button>
                </li>
              ))}
            </ul>
          )}
        </div>
      </aside>
    </div>
  )
}

function TranscriptEntry({ entry, onAgain }: { entry: Entry; onAgain: () => void }) {
  const tree = useMemo(() => {
    if (!entry.reply) return null
    try {
      return parseDocument(entry.reply)
    } catch {
      return null
    }
  }, [entry.reply])
  const word = /^\s*\{\s*["']?([A-Za-z_$][\w$]*)/.exec(entry.command)?.[1]
  return (
    <article className="group min-w-0 space-y-1.5">
      <header className="flex min-w-0 items-baseline gap-2">
        <span aria-hidden className="shrink-0 font-mono text-xs text-muted-foreground">
          {entry.database}&gt;
        </span>
        <code
          className="min-w-0 flex-1 font-mono text-xs break-all whitespace-pre-wrap"
          title={entry.command}
        >
          {entry.verdict && word ? (
            <>
              {entry.command.slice(0, entry.command.indexOf(word))}
              <span style={{ color: CLASS[entry.verdict.class].color }}>{word}</span>
              {entry.command.slice(entry.command.indexOf(word) + word.length)}
            </>
          ) : (
            entry.command
          )}
        </code>
        {entry.durationMs !== undefined && (
          <span className="numeric shrink-0 text-hint text-muted-foreground">
            {entry.durationMs.toLocaleString("en-US")} ms
          </span>
        )}
        <Button size="xs" variant="ghost" className="shrink-0" onClick={onAgain}>
          Again
        </Button>
      </header>
      {entry.error !== undefined ? (
        <Well className="border-rule-danger text-hint break-words whitespace-pre-wrap text-destructive">
          {entry.error}
        </Well>
      ) : (
        <Well plain className="max-h-96 overflow-auto p-2">
          {tree ? (
            <BsonTree root={tree} expand={1} />
          ) : (
            <pre className="font-mono text-hint break-all whitespace-pre-wrap">{entry.reply}</pre>
          )}
          {entry.more && (
            <p className="pt-1.5 font-sans text-hint text-warning">
              The command opened a cursor with more than this first batch, and it was closed. Add a
              limit, or a larger batchSize, to read further.
            </p>
          )}
        </Well>
      )}
    </article>
  )
}
