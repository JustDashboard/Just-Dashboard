"use client"

import { useCallback, useEffect, useRef, useState } from "react"
import { CheckCircle, CrossCircle } from "@/components/icons"
import { cn } from "@/lib/utils"
import { useSocket, type Envelope } from "@/hooks/use-socket"
import { Button } from "@/components/ui/button"
import { Spinner } from "@/components/state"
import { Pane, PaneHeader } from "@/components/panel"

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
  }

  const onMessage = useCallback((envelope: Envelope) => {
    if (envelope.type === "output") {
      const batch = envelope.data as RunLine[]
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
    (socketPath: string, params: Record<string, string | number | undefined> = {}) => {
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
  const scrollRef = useRef<HTMLDivElement>(null)
  const [following, setFollowing] = useState(true)

  useEffect(() => {
    if (!following) return
    const el = scrollRef.current
    if (el) el.scrollTop = el.scrollHeight
  }, [lines, following])

  if (state === "idle") return null

  return (
    <Pane className={cn("bg-surface-sunken", className)}>
      <PaneHeader className="gap-2 px-3">
        {state === "running" && <Spinner className="size-3.5 text-muted-foreground" />}
        {state === "ok" && <CheckCircle className="size-3.5 text-success" />}
        {state === "failed" && <CrossCircle className="size-3.5 text-destructive" />}
        <span className="min-w-0 flex-1 truncate text-xs font-medium">
          {title ?? "Output"}
          {state === "failed" && exitCode !== undefined && (
            <span className="ml-2 font-normal text-muted-foreground">
              exited with status {exitCode}
            </span>
          )}
        </span>
        {onDismiss && state !== "running" && (
          <Button size="xs" variant="ghost" onClick={onDismiss}>
            Dismiss
          </Button>
        )}
      </PaneHeader>
      <div
        ref={scrollRef}
        onScroll={() => {
          const el = scrollRef.current
          if (!el) return
          setFollowing(el.scrollHeight - el.scrollTop - el.clientHeight < 40)
        }}
        className="max-h-72 min-h-24 overflow-auto p-2.5 font-mono text-hint leading-relaxed"
      >
        {lines.length === 0 ? (
          <p className="text-muted-foreground">Starting…</p>
        ) : (
          lines.map((line, i) => (
            <div
              key={i}
              className={cn(
                "break-all whitespace-pre-wrap",
                // A step marker is the runner's own voice rather than the
                // command's, so it reads as a heading in the stream.
                line.stream === "status" && "mt-1.5 font-medium text-primary",
                line.stream === "stderr" && "text-warning",
              )}
            >
              {line.service && <span className="mr-2 text-muted-foreground">{line.service} |</span>}
              {line.text}
            </div>
          ))
        )}
      </div>
    </Pane>
  )
}
