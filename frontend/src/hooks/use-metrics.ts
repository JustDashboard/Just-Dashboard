"use client"

import { useCallback, useEffect, useSyncExternalStore } from "react"
import { get } from "@/lib/api"
import type { HostInfo, Snapshot } from "@/lib/types"
import {
  flush,
  getServerState,
  getState,
  pushSnapshot,
  seed,
  setConnection,
  setError,
  setHost,
  subscribe,
  type MetricsState,
} from "@/lib/metrics-store"
import { useSocket, type Envelope } from "@/hooks/use-socket"

/** Reads the live metrics store. Re-renders only on a new frame. */
export function useMetrics(): MetricsState {
  return useSyncExternalStore(subscribe, getState, getServerState)
}

/**
 * This server's host name, as the dashboard reads it for its own pages (`/system/host`, the
 * Overview's title): `undefined` until the shell's metrics stream has it, `null` once its first
 * read failed with nothing to show. It re-renders on a new name, not on every frame.
 *
 * It is this server's own word for itself, which is why the shell names the server with it rather
 * than with the name a hub lists it under: that one is the hub's text (hub-link-protocol §8.10).
 */
export function useHostName(): string | null | undefined {
  return useSyncExternalStore(subscribe, hostNameSnapshot, noHostName)
}

function hostNameSnapshot(): string | null | undefined {
  const { host, error } = getState()
  return host?.hostname?.trim() || (error ? null : undefined)
}

function noHostName(): undefined {
  return undefined
}

/**
 * Runs the metrics stream for as long as it is mounted.
 *
 * Mounted once in the dashboard shell rather than on the Overview page, which
 * is the whole point: the history keeps filling while you are reading logs or
 * in a shell, so the chart you come back to covers the time you were away
 * instead of starting from nothing. It renders nothing, and its own state
 * changes only when the socket opens or closes.
 */
export function MetricsStream() {
  useMetricsStream()
  return null
}

function useMetricsStream() {
  const onMessage = useCallback((envelope: Envelope) => {
    if (envelope.type === "host") {
      setHost(envelope.data as HostInfo)
      return
    }
    if (envelope.type === "metrics") pushSnapshot(envelope.data as Snapshot)
  }, [])

  const { state } = useSocket("/system/stream", { onMessage, query: { interval: 2000 } })

  useEffect(() => {
    setConnection(state)
  }, [state])

  // pagehide rather than beforeunload: it is the one that fires for a tab
  // going into the back/forward cache, which beforeunload does not.
  useEffect(() => {
    window.addEventListener("pagehide", flush)
    return () => window.removeEventListener("pagehide", flush)
  }, [])

  // The socket carries everything once connected; this single fetch only
  // avoids an empty screen during the handshake, and is skipped entirely when
  // the store already holds a snapshot from earlier in the session.
  useEffect(() => {
    if (getState().snapshot) return
    let cancelled = false
    Promise.all([get<HostInfo>("/system/host"), get<Snapshot>("/system/metrics")])
      .then(([host, snapshot]) => {
        if (cancelled) return
        seed(host, snapshot)
        setError(undefined)
      })
      .catch((err: unknown) => {
        if (cancelled) return
        setError(err instanceof Error ? err.message : String(err))
      })
    return () => {
      cancelled = true
    }
  }, [])
}
