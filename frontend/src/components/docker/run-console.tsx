"use client"

import { useCallback, useMemo, useRef, useState } from "react"
import { useSocket, type Envelope } from "@/hooks/use-socket"
import { ConsoleView } from "@/components/console-view"
import { streamKind } from "@/components/transcript-line"

/**
 * Watching a long command run, instead of waiting for one.
 *
 * `docker compose up` on a stack that has to pull four images and build one
 * takes minutes. Every other panel in this class runs it as a request that
 * hangs for those minutes and then produces a wall of text — which is
 * indistinguishable from a broken dashboard, and is the single most common
 * reason an operator who has been burned once goes back to ssh. Dockge got
 * this right and it is most of why people like it.
 *
 * So: a socket, the same output a terminal would have shown, arriving as it
 * happens. The exit code comes as its own frame rather than being inferred
 * from the text, because compose prints its failures as ordinary lines and a
 * reader watching only the words cannot tell a build that failed from one that
 * was noisy.
 */

export type RunState = "idle" | "running" | "ok" | "failed"

export type RunLine = { stream: string; text: string; service?: string }

export function useRunConsole() {
  const [lines, setLines] = useState<RunLine[]>([])
  const [state, setState] = useState<RunState>("idle")
  const [path, setPath] = useState<string>()
  const [query, setQuery] = useState<Record<string, string | number | undefined>>({})
  const [exitCode, setExitCode] = useState<number>()
  const finish = useRef<((code: number) => void) | undefined>(undefined)
  const output = useRef<((line: string) => void) | undefined>(undefined)
  // The state as the socket callbacks see it. They are created once and would
  // otherwise close over the first render's value.
  const live = useRef<RunState>("idle")
  const settle = (next: RunState, code?: number) => {
    live.current = next
    setState(next)
    if (code !== undefined) setExitCode(code)
    setPath(undefined)
    finish.current?.(code ?? -1)
    finish.current = undefined
    output.current = undefined
  }

  const onMessage = useCallback((envelope: Envelope) => {
    if (envelope.type === "output") {
      const batch = envelope.data as RunLine[]
      const latest = batch.at(-1)?.text
      if (latest) output.current?.(latest)
      // Bounded, for the same reason the log panel is: a build with a verbose
      // dependency install can emit tens of thousands of lines, and keeping
      // all of them in React state makes the tab unusable long before it
      // runs out of memory.
      setLines((prev) => {
        const next = [...prev, ...batch]
        return next.length > 4000 ? next.slice(next.length - 4000) : next
      })
    } else if (envelope.type === "done") {
      const code = (envelope.data as { exitCode: number }).exitCode
      settle(code === 0 ? "ok" : "failed", code)
    } else if (envelope.type === "error") {
      setLines((prev) => [...prev, { stream: "stderr", text: envelope.error ?? "failed" }])
      settle("failed")
    }
    // The socket keeps this handler in a ref, so rebuilding it per render
    // would be pure churn.
  }, [])

  /**
   * A socket that drops mid-run ends the run.
   *
   * useSocket reconnects with backoff, which is right for a metrics feed and
   * badly wrong here: reconnecting re-issues the GET, and re-issuing the GET
   * runs the command a second time. `docker compose down` twice is survivable;
   * a redeploy fired twice because a VPN blinked is not. The server has
   * already killed the command when the socket closed — its context is the
   * request's — so the honest report is that the run stopped and its outcome
   * is unknown.
   */
  const onClose = useCallback(() => {
    if (live.current !== "running") return
    setLines((prev) => [
      ...prev,
      {
        stream: "stderr",
        text: "— connection lost, so this run was stopped. Nothing was retried.",
      },
    ])
    settle("failed")
  }, [])

  useSocket(path ?? "", { onMessage, onClose, enabled: Boolean(path), query })

  /** Starts a run and resolves with its exit code when it finishes. */
  const run = useCallback(
    (
      socketPath: string,
      params: Record<string, string | number | undefined> = {},
      onOutput?: (line: string) => void,
    ) => {
      output.current = onOutput
      setLines([])
      setExitCode(undefined)
      live.current = "running"
      setState("running")
      setQuery(params)
      setPath(socketPath)
      return new Promise<number>((resolve) => {
        finish.current = resolve
      })
    },
    [],
  )

  const reset = useCallback(() => {
    setLines([])
    live.current = "idle"
    setState("idle")
    setExitCode(undefined)
    setPath(undefined)
  }, [])

  return { run, reset, lines, state, exitCode, running: state === "running" }
}

/**
 * A compose run's console (`ConsoleView`): each line numbered and drawn by
 * its stream, the service that wrote it before it, and the status it exited
 * with when it failed. It rises into place, since it mounts when a command
 * starts or its output arrives.
 */
export function RunConsole({
  lines,
  state,
  exitCode,
  title,
  onDismiss,
  className,
}: {
  lines: RunLine[]
  state: RunState
  exitCode?: number
  title?: string
  onDismiss?: () => void
  className?: string
}) {
  const shown = useMemo(
    () =>
      lines.map((line, i) => ({
        key: i,
        line: { number: i + 1, text: line.text, kind: streamKind(line.stream) },
        prefix: line.service,
      })),
    [lines],
  )
  if (state === "idle") return null
  return (
    <ConsoleView
      title={title ?? "Output"}
      state={state}
      detail={
        state === "failed" && exitCode !== undefined ? `exited with status ${exitCode}` : undefined
      }
      lines={shown}
      empty="Starting…"
      onDismiss={onDismiss}
      rise
      className={className}
    />
  )
}
