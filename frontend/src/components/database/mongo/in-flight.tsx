"use client"

import { useCallback, useEffect, useRef, useState } from "react"
import { StopCircle } from "@/components/icons"
import { Button } from "@/components/ui/button"

/**
 * A read the reader stopped. It is its own kind of failure — nothing went
 * wrong, and the page says "stopped" rather than showing the browser's word
 * for an aborted request.
 */
export class Stopped extends Error {
  constructor() {
    super("Stopped before the server answered.")
    this.name = "Stopped"
  }
}

/** How long before a wait says how long it has been: a read that answers at once never shows a clock. */
const SHOWN_AFTER = 1

/**
 * Whole seconds since `running` became true, counted while it stays so. `0`
 * while nothing is in flight.
 */
export function useElapsed(running: boolean): number {
  const [seconds, setSeconds] = useState(0)
  useEffect(() => {
    if (!running) return
    const began = performance.now()
    const timer = setInterval(() => setSeconds(Math.floor((performance.now() - began) / 1000)), 250)
    return () => {
      clearInterval(timer)
      setSeconds(0)
    }
  }, [running])
  return running ? seconds : 0
}

/**
 * A request the reader can stop.
 *
 * `run` hands the work a signal of its own; `stop` aborts it, and the work
 * then fails with `Stopped` instead of the abort it was. Dropping the request
 * is also what stops the server: the dashboard's own request is cancelled,
 * and with it the operation it was waiting on.
 */
export function useStoppable() {
  const held = useRef<AbortController | null>(null)
  const run = useCallback(<T,>(work: (signal: AbortSignal) => Promise<T>, outer?: AbortSignal) => {
    const own = new AbortController()
    held.current = own
    const pass = () => own.abort()
    outer?.addEventListener("abort", pass)
    return work(own.signal)
      .catch((err: unknown) => {
        // Aborted by the reader, not by the page moving on: said as a stop.
        if (own.signal.aborted && !outer?.aborted) throw new Stopped()
        throw err
      })
      .finally(() => {
        outer?.removeEventListener("abort", pass)
        if (held.current === own) held.current = null
      })
  }, [])
  const stop = useCallback(() => held.current?.abort(), [])
  return { run, stop }
}

/** Whether a wait has gone on long enough to say so. */
export const worthShowing = (seconds: number) => seconds >= SHOWN_AFTER

/**
 * The command while its work is in flight and has been for a second: how
 * long it has run, counted as it runs, and the way to stop it. It takes the
 * command's own place, so the reader's eye is already there.
 */
export function StopButton({
  seconds,
  onStop,
  size = "sm",
  className,
}: {
  seconds: number
  onStop: () => void
  size?: "xs" | "sm"
  className?: string
}) {
  return (
    <Button type="button" size={size} variant="outline" className={className} onClick={onStop}>
      <StopCircle />
      Stop
      <span className="numeric text-muted-foreground" aria-live="off">
        {seconds} s
      </span>
    </Button>
  )
}
