"use client"

import { useEffect, useRef, useState } from "react"
import { wsUrl, type Query } from "@/lib/api"

export type Envelope<T = unknown> = {
  type: string
  data?: T
  error?: string
  ts: number
}

type SocketOptions = {
  /** Called for every decoded frame. Kept in a ref so a changing handler does not reconnect. */
  onMessage?: (envelope: Envelope) => void
  onOpen?: () => void
  onClose?: () => void
  enabled?: boolean
  /**
   * Query parameters appended to the endpoint. A factory is evaluated for
   * each reconnect, which lets resumable streams send their latest sequence
   * without reconnecting merely because that sequence advanced.
   */
  query?: Query | (() => Query)
}

export type SocketState = "connecting" | "open" | "closed" | "error"

/**
 * How long a socket must stay up before its open counts as recovery.
 *
 * Resetting the backoff on open alone meant an endpoint that accepts, reports
 * an error and closes was redialled every second for as long as the page was
 * open. Only a connection that held for a while proves the endpoint is back.
 */
const STABLE_OPEN_MS = 10_000
const MAX_RETRY_MS = 15_000

/**
 * Exponential backoff with "equal jitter": half the step fixed, half random.
 * Without the random half, every tab and every socket that lost the same
 * tunnel redialled in lockstep.
 */
export function retryDelay(attempt: number, random = Math.random()) {
  const step = Math.min(1000 * 2 ** (attempt - 1), MAX_RETRY_MS)
  return step / 2 + (step / 2) * random
}

/**
 * Subscribes to one of the dashboard's push endpoints. Reconnects with
 * backoff, because these sockets ride a VPN tunnel that drops routinely and a
 * dead metrics graph is worse than a brief gap.
 */
export function useSocket(path: string, options: SocketOptions = {}) {
  const { enabled = true } = options
  const [liveState, setState] = useState<SocketState>("closed")
  // A disabled socket is closed by definition; reporting that from the
  // parameter rather than from state avoids a render just to say so.
  const state: SocketState = enabled ? liveState : "closed"
  // Handlers live in a ref so a caller passing a fresh closure each render
  // does not tear down and rebuild the socket. Syncing it in an effect keeps
  // render itself free of side effects.
  const handlers = useRef(options)
  useEffect(() => {
    handlers.current = options
  })
  const socketRef = useRef<WebSocket | null>(null)

  const queryKey =
    typeof options.query === "function" ? "query-factory" : JSON.stringify(options.query ?? {})

  useEffect(() => {
    if (!enabled) return
    let attempt = 0
    let openedAt = 0
    let closedByUs = false
    let retryTimer: ReturnType<typeof setTimeout> | undefined

    const connect = () => {
      setState("connecting")
      const query = handlers.current.query
      const ws = new WebSocket(wsUrl(path, typeof query === "function" ? query() : query))
      ws.binaryType = "arraybuffer"
      socketRef.current = ws

      ws.onopen = () => {
        openedAt = Date.now()
        setState("open")
        handlers.current.onOpen?.()
      }
      ws.onmessage = (event) => {
        if (typeof event.data !== "string") return
        try {
          handlers.current.onMessage?.(JSON.parse(event.data) as Envelope)
        } catch {
          // A frame we cannot parse is not worth tearing the socket down for.
        }
      }
      ws.onerror = () => setState("error")
      ws.onclose = () => {
        socketRef.current = null
        handlers.current.onClose?.()
        if (closedByUs) {
          setState("closed")
          return
        }
        setState("closed")
        if (openedAt > 0 && Date.now() - openedAt >= STABLE_OPEN_MS) attempt = 0
        openedAt = 0
        attempt += 1
        retryTimer = setTimeout(connect, retryDelay(attempt))
      }
    }

    connect()
    return () => {
      closedByUs = true
      if (retryTimer) clearTimeout(retryTimer)
      socketRef.current?.close()
      socketRef.current = null
    }
  }, [path, enabled, queryKey])

  const send = (payload: unknown) => {
    const ws = socketRef.current
    if (ws?.readyState !== WebSocket.OPEN) return false
    ws.send(typeof payload === "string" ? payload : JSON.stringify(payload))
    return true
  }

  return { state, send }
}
