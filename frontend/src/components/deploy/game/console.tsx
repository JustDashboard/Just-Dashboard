"use client"

import { memo, useCallback, useEffect, useMemo, useRef, useState } from "react"
import Link from "next/link"
import { Copy, Download, Logs, Slash, Trash } from "@/components/icons"
import { post } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import { clock, plural } from "@/lib/format"
import { LANES, hueFor } from "@/lib/hue"
import { downloadText } from "@/lib/metrics-export"
import { cn } from "@/lib/utils"
import { notify } from "@/lib/toast"
import type { GameConsoleResult } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import { FormNote } from "@/components/form"
import { IconAction } from "@/components/icon-action"
import { LogText } from "@/components/logs/log-text"
import { Pane, PaneFooter, PaneHeader } from "@/components/panel"
import { ErrorState, LoadingRows, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { ChipStrip, FilterChip } from "@/components/tabs"
import { BorderBeam } from "@/components/ui/border-beam"
import { Button } from "@/components/ui/button"
import {
  InputGroup,
  InputGroupAddon,
  InputGroupButton,
  InputGroupInput,
  InputGroupText,
} from "@/components/ui/input-group"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { GameIdentity, useGameOverview } from "@/components/deploy/game/identity"

type ConsoleLine = {
  id: number
  command: string
  output: string
  exitCode: number
  at: string
}

/**
 * The game console. It sends one command at a time through the container's
 * own console client and shows what the game said back — deliberately not a
 * shell, and deliberately not a persistent session: the server refuses
 * anything that is not a plain game command, and this field can only produce
 * those.
 *
 * It is drawn as the console it is, the way a run's transcript is
 * (`run-transcript.tsx`): one pane sized to the window, a strip that says
 * whether it is ready and holds the tools, the exchanges on the sunken ground
 * with each reply coloured by the log console's rules — and a player's name in
 * the hue that player has on the Players page, so one person is one colour
 * across both — then the field and its Send as one box. While a command is in
 * flight a light runs round the pane and the strip says so; the answer's lines
 * rise into place when it lands (§11).
 */
export function GameConsole({ projectId }: { projectId: number }) {
  const { can } = useAuth()
  const [command, setCommand] = useState("")
  const [busy, setBusy] = useState(false)
  const [lines, setLines] = useState<ConsoleLine[]>([])
  const [history, setHistory] = useState<string[]>([])
  const [historyIndex, setHistoryIndex] = useState(-1)
  const [wrap, setWrap] = useState(true)
  const [follow, setFollow] = useState(true)
  const nextID = useRef(0)
  const body = useRef<HTMLDivElement>(null)
  const field = useRef<HTMLInputElement>(null)
  const overview = useGameOverview(projectId)
  // Every name of a player who is online, as one pattern built once per poll
  // rather than once per line of every reply on every keystroke.
  const pattern = useMemo(() => {
    const names = overview.data?.players?.names ?? []
    return names.length > 0
      ? new RegExp(
          `\\b(${names.map((name) => name.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")).join("|")})\\b`,
        )
      : undefined
  }, [overview.data])

  const send = useCallback(async () => {
    const trimmed = command.trim()
    if (!trimmed || busy) return
    setBusy(true)
    try {
      const result = await post<GameConsoleResult>(`/deploy/${projectId}/game/console`, {
        command: trimmed,
      })
      setLines((previous) =>
        [
          ...previous,
          {
            id: nextID.current++,
            command: result.command,
            output: result.output || "(the server returned no output)",
            exitCode: result.exitCode,
            at: result.executedAt,
          },
        ].slice(-200),
      )
      setHistory((previous) =>
        [...previous.filter((entry) => entry !== trimmed), trimmed].slice(-50),
      )
      setHistoryIndex(-1)
      setCommand("")
    } catch (error) {
      notify.error("The console refused that command", error)
    } finally {
      setBusy(false)
    }
  }, [busy, command, projectId])

  const recallHistory = (direction: -1 | 1) => {
    if (history.length === 0) return
    const next =
      historyIndex === -1
        ? direction === -1
          ? history.length - 1
          : -1
        : Math.min(history.length - 1, Math.max(-1, historyIndex + direction))
    setHistoryIndex(next)
    setCommand(next === -1 ? "" : history[next])
  }

  // Following the newest answer until the reader scrolls up to read an older
  // one, as the run transcript does.
  useEffect(() => {
    if (follow && body.current) body.current.scrollTop = body.current.scrollHeight
  }, [lines, follow, wrap])

  const transcript = () =>
    lines.map((line) => `> ${line.command}\n${line.output.replace(/\r\n?/g, "\n")}`).join("\n")
  const failed = lines.filter((line) => line.exitCode !== 0).length
  const recent = history.slice(-3).reverse()

  const data = overview.data
  return (
    <div className="space-y-6">
      {data && <GameIdentity overview={data} />}
      {overview.error ? (
        <ErrorState error={overview.error} />
      ) : !data ? (
        <LoadingRows rows={3} />
      ) : data.status !== "available" ? (
        <Notice title="Console unavailable" icon={Slash}>
          {data.reason ?? "This game server has no running container to send commands to."}
        </Notice>
      ) : (
        // Sized to the window below the project's header and this page's
        // identity line, so the composer and the footer under it are on
        // screen on a laptop without scrolling the page.
        <Pane className="relative h-[clamp(20rem,calc(100dvh-19.5rem),40rem)]">
          {busy && <BorderBeam size={96} duration={4} />}
          <PaneHeader className="flex-wrap gap-2 py-2">
            <span className="text-xs font-medium">Game console</span>
            {/* Not `live`: the server is asked on a fifteen-second poll, and §11
                keeps the breathing dot for a socket. */}
            {busy ? (
              <Status tone="warning" label={<TextShimmer>Sending…</TextShimmer>} />
            ) : data.console ? (
              <Status tone="running" label="Ready" />
            ) : (
              <Status tone="stopped" label="No console" />
            )}
            <span className="ml-auto flex shrink-0 items-center gap-1">
              <FilterChip selected={wrap} onClick={() => setWrap(!wrap)}>
                Wrap
              </FilterChip>
              <IconAction
                label="Copy the transcript"
                disabled={lines.length === 0}
                onClick={() => void copyText(transcript(), "Transcript copied")}
              >
                <Copy />
              </IconAction>
              <IconAction
                label="Download the transcript"
                disabled={lines.length === 0}
                onClick={() => downloadText("game-console.log", `${transcript()}\n`, "text/plain")}
              >
                <Download />
              </IconAction>
              <IconAction
                label="Clear the transcript"
                disabled={lines.length === 0}
                onClick={() => setLines([])}
              >
                <Trash />
              </IconAction>
              {data.containerId && (
                <Button size="xs" variant="ghost" asChild>
                  <Link href={`/deploy/${projectId}/logs?service=${data.containerId}`}>
                    <Logs /> Logs
                  </Link>
                </Button>
              )}
            </span>
          </PaneHeader>

          <div
            ref={body}
            role="log"
            aria-label="Console transcript"
            aria-live="polite"
            onScroll={(event) => {
              const el = event.currentTarget
              const atEnd = el.scrollHeight - el.scrollTop - el.clientHeight < 32
              if (atEnd !== follow) setFollow(atEnd)
            }}
            className="min-h-0 flex-1 overflow-auto bg-surface-sunken"
          >
            {lines.length === 0 ? (
              <div className="flex h-full min-h-40 items-center justify-center px-6 text-center text-body text-muted-foreground">
                {data.console ? (
                  <p>
                    Nothing sent yet. Try <span className="font-mono text-foreground">list</span> to
                    see who is online.
                  </p>
                ) : (
                  <p>This game declares no console, so commands cannot be sent from here.</p>
                )}
              </div>
            ) : (
              <ol className={cn("py-3 font-mono text-xs leading-6", !wrap && "min-w-max")}>
                {lines.map((line) => (
                  <Exchange key={line.id} line={line} wrap={wrap} pattern={pattern} />
                ))}
              </ol>
            )}
          </div>

          {can("service.control") && data.console ? (
            <form
              className="shrink-0 space-y-2 border-t border-hairline bg-surface-header p-2"
              onSubmit={(event) => {
                event.preventDefault()
                void send()
              }}
            >
              {recent.length > 0 && (
                // The operator's own last commands, one press from the field —
                // not suggestions the product invents.
                <ChipStrip role="group" aria-label="Recent commands">
                  {recent.map((entry) => (
                    <FilterChip
                      key={entry}
                      className="font-mono"
                      onClick={() => {
                        setCommand(entry)
                        field.current?.focus()
                      }}
                    >
                      {entry}
                    </FilterChip>
                  ))}
                </ChipStrip>
              )}
              <InputGroup>
                <InputGroupAddon>
                  <InputGroupText className="font-mono text-body text-brand">&gt;</InputGroupText>
                </InputGroupAddon>
                <InputGroupInput
                  ref={field}
                  id="game-console-command"
                  aria-label="Command"
                  value={command}
                  autoComplete="off"
                  spellCheck={false}
                  className="font-mono"
                  placeholder="list"
                  onChange={(event) => setCommand(event.target.value)}
                  onKeyDown={(event) => {
                    if (event.key === "ArrowUp") {
                      event.preventDefault()
                      recallHistory(-1)
                    } else if (event.key === "ArrowDown") {
                      event.preventDefault()
                      recallHistory(1)
                    }
                  }}
                />
                <InputGroupAddon align="inline-end" className="max-sm:hidden">
                  <InputGroupText>↑↓ history</InputGroupText>
                </InputGroupAddon>
                <InputGroupAddon align="inline-end" className="gap-0 p-0">
                  <InputGroupButton
                    type="submit"
                    variant="default"
                    pending={busy}
                    disabled={busy || !command.trim()}
                  >
                    Send
                  </InputGroupButton>
                </InputGroupAddon>
              </InputGroup>
            </form>
          ) : (
            data.console && (
              <div className="shrink-0 border-t border-hairline bg-surface-header px-3 py-2.5">
                <FormNote>Sending commands needs the service control capability.</FormNote>
              </div>
            )
          )}

          <PaneFooter className="justify-between gap-x-4 text-hint text-muted-foreground">
            <span className="numeric shrink-0">
              {plural(lines.length, "command")}
              {failed > 0 && <span className="text-warning"> · {failed} failed</span>}
            </span>
            {data.console && (
              <span className="min-w-0">
                Only plain game commands are accepted. Anything carrying a shell character is
                refused before it reaches the container.
              </span>
            )}
          </PaneFooter>
        </Pane>
      )}
    </div>
  )
}

/**
 * One command and what the game said back: the prompt in the brand with the
 * clock time in the gutter, then the reply's lines numbered and coloured. A
 * command the game answered with a failure is washed amber where it sits, and
 * says its exit code as a state at the end of its line.
 *
 * Memoised, because the field's text is state of the console around it: every
 * keystroke would otherwise colour the whole transcript again.
 */
const Exchange = memo(function Exchange({
  line,
  wrap,
  pattern,
}: {
  line: ConsoleLine
  wrap: boolean
  pattern?: RegExp
}) {
  const flow = wrap ? "break-words whitespace-pre-wrap" : "whitespace-pre"
  const failed = line.exitCode !== 0
  const output = line.output.replace(/\r\n?/g, "\n").split("\n")
  return (
    <>
      <li className="mt-1 flex min-w-0 animate-rise gap-3 border-t border-hairline px-3 pt-1 first:mt-0 first:border-t-0 first:pt-0 hover:bg-row-hover sm:px-4">
        <time
          dateTime={line.at}
          aria-hidden
          className="numeric w-10 shrink-0 text-right text-muted-foreground/60 select-none"
        >
          {clock(line.at).slice(0, 5)}
        </time>
        <span className={cn("min-w-0 flex-1 font-medium text-foreground", flow)}>
          <span className="text-brand select-none">&gt; </span>
          {line.command}
        </span>
        {failed && (
          <Status tone="warning" label={`exit ${line.exitCode}`} className="shrink-0 font-sans" />
        )}
      </li>
      {output.map((text, index) => (
        <li
          key={index}
          className={cn(
            "flex min-w-0 animate-rise gap-3 px-3 hover:bg-row-hover sm:px-4",
            failed && "bg-wash-warning hover:bg-wash-warning",
          )}
        >
          <span
            aria-hidden
            className="numeric w-10 shrink-0 text-right text-muted-foreground/60 select-none"
          >
            {index + 1}
          </span>
          <span className={cn("min-w-0 flex-1 text-foreground/85", flow)}>
            <ReplyText text={text} pattern={pattern} />
          </span>
        </li>
      ))}
    </>
  )
})

/**
 * A line of the game's reply in the log console's colours, with every name of
 * a player who is online drawn in that player's own hue.
 */
function ReplyText({ text, pattern }: { text: string; pattern?: RegExp }) {
  if (!text) return " "
  const parts = pattern ? text.split(pattern) : [text]
  return parts.map((part, index) =>
    // `split` with a capture group puts every match at an odd index.
    index % 2 === 1 ? (
      <span key={index} className="font-medium" style={{ color: hueFor(part, LANES) }}>
        {part}
      </span>
    ) : (
      part && <LogText key={index} text={part} />
    ),
  )
}
