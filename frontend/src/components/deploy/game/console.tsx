"use client"

import { useCallback, useRef, useState } from "react"
import Link from "next/link"
import { ArrowRight, Terminal } from "@/components/icons"
import { Group, Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyNote, ErrorState, LoadingRows, Notice } from "@/components/state"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { get, post } from "@/lib/api"
import { relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { GameConsoleResult, GameOverview } from "@/lib/types"

type ConsoleLine = { id: number; command: string; output: string; failed: boolean; at: string }

/**
 * The game console. It sends one command at a time through the container's
 * own console client and shows what the game said back — deliberately not a
 * shell, and deliberately not a persistent session: the server refuses
 * anything that is not a plain game command, and this field can only produce
 * those. Ported from the pre-rebuild `GameConsoleTab`.
 */
export function GameConsole({ projectId }: { projectId: number }) {
  const { can } = useAuth()
  const [command, setCommand] = useState("")
  const [busy, setBusy] = useState(false)
  const [lines, setLines] = useState<ConsoleLine[]>([])
  const [history, setHistory] = useState<string[]>([])
  const [historyIndex, setHistoryIndex] = useState(-1)
  const nextID = useRef(0)
  const overview = usePoll(
    (signal) => get<GameOverview>(`/deploy/${projectId}/game`, undefined, signal),
    15000,
    [projectId],
  )

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
            failed: result.exitCode !== 0,
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

  const unavailable = overview.data && overview.data.status !== "available"
  return (
    <Panel plain>
      <PanelHeader
        title="Console"
        actions={
          overview.data?.containerId && (
            <Button variant="ghost" size="sm" asChild>
              <Link href={`/deploy/${projectId}/logs?service=${overview.data.containerId}`}>
                Open logs <ArrowRight className="size-3" />
              </Link>
            </Button>
          )
        }
      />
      <PanelBody className="space-y-3">
        {overview.error ? (
          <ErrorState error={overview.error} />
        ) : !overview.data ? (
          <LoadingRows rows={3} />
        ) : unavailable ? (
          <Notice title="Console unavailable" icon={Terminal}>
            {overview.data.reason ??
              "This game server has no running container to send commands to."}
          </Notice>
        ) : (
          <>
            <Group
              role="log"
              aria-label="Console transcript"
              aria-live="polite"
              className="max-h-[26rem] min-w-0 space-y-3 overflow-y-auto"
              tinted
            >
              {lines.length === 0 ? (
                <EmptyNote>
                  Nothing sent yet. Try <span className="font-mono">list</span> to see who is
                  online.
                </EmptyNote>
              ) : (
                lines.map((line) => (
                  <div key={line.id} className="min-w-0 space-y-1">
                    <p className="flex min-w-0 flex-wrap items-center gap-2 font-mono text-xs">
                      <span className="text-muted-foreground">&gt;</span>
                      <span className="min-w-0 font-medium break-all">{line.command}</span>
                      {line.failed && <Tag tone="warning">non-zero exit</Tag>}
                      <span className="text-hint text-muted-foreground">
                        {relativeTime(line.at)}
                      </span>
                    </p>
                    <ol className="font-mono text-xs leading-6">
                      {line.output
                        .replace(/\r\n?/g, "\n")
                        .split("\n")
                        .map((text, index) => (
                          <li
                            key={index}
                            className="flex min-w-0 gap-3 rounded-sm px-2 hover:bg-row-hover"
                          >
                            <span
                              aria-hidden="true"
                              className="w-7 shrink-0 text-right text-muted-foreground/60 select-none"
                            >
                              {index + 1}
                            </span>
                            <span className="min-w-0 break-words whitespace-pre-wrap text-muted-foreground">
                              {text || " "}
                            </span>
                          </li>
                        ))}
                    </ol>
                  </div>
                ))
              )}
            </Group>
            {can("service.control") ? (
              <form
                className="flex min-w-0 flex-wrap items-end gap-2"
                onSubmit={(event) => {
                  event.preventDefault()
                  void send()
                }}
              >
                <div className="min-w-0 flex-1 space-y-1.5">
                  <Label htmlFor="game-console-command">Command</Label>
                  <Input
                    id="game-console-command"
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
                </div>
                <Button type="submit" size="sm" disabled={busy || !command.trim()} pending={busy}>
                  Send
                </Button>
              </form>
            ) : (
              <EmptyNote>Sending commands needs the service control capability.</EmptyNote>
            )}
            <p className="text-hint text-muted-foreground">
              Only plain game commands are accepted. Anything carrying a shell character is refused
              before it reaches the container.
            </p>
          </>
        )}
      </PanelBody>
    </Panel>
  )
}
