"use client"

import { useCallback, useEffect, useMemo, useRef, useState } from "react"

export type Paged<S> = {
  /** What has been read so far; `undefined` before the first page. */
  state: S | undefined
  error: Error | undefined
  /** The first page has not landed. */
  loading: boolean
  /** A fresh read is in flight over what is shown. */
  reloading: boolean
  loadingMore: boolean
  /** Nothing is left to read. */
  done: boolean
  /** Read the next page, from where the last one stopped. */
  more: () => void
  /** Read again from the start; what is shown stays until the answer lands. */
  reload: () => void
  /**
   * Change what is held without reading it again: a write the server
   * accepted is laid over the rows already here, so a list the reader has
   * paged a long way into does not go back to its first page for one edit.
   */
  patch: (change: (state: S) => S) => void
}

/** A paged read as something that only shows it: how far it has come, and the presses that take it further. */
export type PagedStatus = Omit<Paged<unknown>, "patch">

type Held<S> = {
  resource: object
  stamp: string
  state?: S
  /** Where the next page starts; `null` once there is none. */
  cursor: string | null
  error?: Error
}

/**
 * A read that arrives in pages — a scan of keys, the fields of a hash, the
 * entries of a stream — held as one growing answer.
 *
 * Redis hands nothing over whole: every listing is a cursor to follow, and
 * the cursor is an opaque string that goes back exactly as it came. `merge`
 * folds each page into what is held and `next` says where the following one
 * starts.
 *
 * `key` is what the read is *of*: when it changes, what was held belonged
 * to something else and is dropped. `epoch` is the same thing read again —
 * after a write, or on Refresh — and what is shown stays on screen until the
 * fresh page replaces it, so nothing blinks and a failed re-read leaves the
 * rows where they were, with the error beside them.
 */
export function usePaged<P, S>(
  options: {
    fetch: (cursor: string | undefined, signal: AbortSignal) => Promise<P>
    merge: (held: S | undefined, page: P) => S
    /** The cursor after this page, or `null` when it was the last. */
    next: (page: P) => string | null
  },
  /** What the read is of, as one string: a different key is a different read. */
  key: string,
  { epoch = 0, enabled = true }: { epoch?: number; enabled?: boolean } = {},
): Paged<S> {
  // An object per key, so a state that was held for another key is told
  // apart by identity and never drawn for this one.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  const resource = useMemo(() => ({}), [key])
  const [held, setHeld] = useState<Held<S>>()
  const [extending, setExtending] = useState<object | null>(null)
  const [tick, setTick] = useState(0)
  const stamp = `${epoch}:${tick}`

  const latest = useRef(options)
  useEffect(() => {
    latest.current = options
  })
  const inFlight = useRef<AbortController | null>(null)

  const read = useCallback(
    (cursor: string | undefined, stampOfRead: string, fresh: boolean) => {
      inFlight.current?.abort()
      const controller = new AbortController()
      inFlight.current = controller
      latest.current.fetch(cursor, controller.signal).then(
        (page) => {
          if (controller.signal.aborted) return
          setHeld((before) => ({
            resource,
            stamp: stampOfRead,
            state: latest.current.merge(
              !fresh && before?.resource === resource ? before.state : undefined,
              page,
            ),
            cursor: latest.current.next(page),
          }))
          setExtending(null)
        },
        (err: unknown) => {
          if (controller.signal.aborted) return
          setHeld((before) => ({
            resource,
            stamp: stampOfRead,
            state: before?.resource === resource ? before.state : undefined,
            // A page that failed is asked for again from the same place.
            cursor: before?.resource === resource ? before.cursor : null,
            error: err instanceof Error ? err : new Error(String(err)),
          }))
          setExtending(null)
        },
      )
    },
    [resource],
  )

  useEffect(() => {
    if (!enabled) return
    read(undefined, stamp, true)
    return () => inFlight.current?.abort()
  }, [read, enabled, stamp])

  const current = held?.resource === resource ? held : undefined
  const loadingMore = extending === resource
  return {
    state: current?.state,
    error: current?.error,
    loading: enabled && !current,
    reloading: Boolean(current) && current?.stamp !== stamp,
    loadingMore,
    done: Boolean(current) && current?.cursor === null,
    more: () => {
      if (!current || current.cursor === null || loadingMore) return
      setExtending(resource)
      read(current.cursor, stamp, false)
    },
    reload: () => setTick((n) => n + 1),
    patch: (change) =>
      setHeld((before) =>
        before?.resource === resource && before.state !== undefined
          ? { ...before, state: change(before.state) }
          : before,
      ),
  }
}
