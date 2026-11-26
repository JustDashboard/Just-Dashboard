"use client"

import { useCallback, useEffect, useRef, useState } from "react"

/**
 * The echo guard, as a pure step: what the box does when the applied value
 * it is told about changes.
 *
 * `unechoed` is what the box sent that has not come back yet, oldest first.
 * A value that comes back from that list is the box's own echo: the address
 * catching up with what was typed. It never replaces the draft, because the
 * reader may be several letters further on by then. Anything else is a
 * change from outside (a namespace narrowed to, Back), and the box then says
 * what the address says.
 */
export function receiveApplied(
  unechoed: readonly string[],
  applied: string,
): { echo: boolean; unechoed: string[] } {
  const at = unechoed.indexOf(applied)
  if (at === -1) return { echo: false, unechoed: [] }
  return { echo: true, unechoed: unechoed.slice(at + 1) }
}

/**
 * A search applied after a pause rather than on every key: each value is a
 * scan on the server (a keyspace, a key's members), and one per keystroke is
 * a scan per letter.
 *
 * `applied` is the value in force, which the caller owns (an address, its own
 * state); `onApply` is how a typed value becomes it. The hook keeps the
 * draft, applies it `ms` after the last key, at once on `apply()` (Enter), and
 * empties it on `clear()`. An `applied` that changes from outside replaces the
 * draft; the echo of what the box itself sent does not.
 *
 * `held` stops the pause from applying anything: an input method composing a
 * word holds text the reader has not chosen yet, and a pause while picking a
 * candidate scanned for it. Let go, the pause starts again from then.
 */
export function useDeferredSearch(
  applied: string,
  onApply: (value: string) => void,
  ms = 400,
  held = false,
) {
  const [draft, setDraft] = useState(applied)
  const [seen, setSeen] = useState(applied)
  // What was sent and has not come back as `applied` yet.
  const [unechoed, setUnechoed] = useState<string[]>([])
  if (seen !== applied) {
    const next = receiveApplied(unechoed, applied)
    setSeen(applied)
    setUnechoed(next.unechoed)
    if (!next.echo && draft.trim() !== applied) setDraft(applied)
  }
  // The last value sent, or the one in force when nothing is on its way.
  const latest = unechoed.length > 0 ? unechoed[unechoed.length - 1] : applied

  const handler = useRef(onApply)
  useEffect(() => {
    handler.current = onApply
  })
  const send = useCallback((value: string) => {
    setUnechoed((list) => [...list, value].slice(-8))
    handler.current(value)
  }, [])

  useEffect(() => {
    const typed = draft.trim()
    if (held || typed === latest) return
    const timer = setTimeout(() => send(typed), ms)
    return () => clearTimeout(timer)
  }, [draft, latest, ms, send, held])

  return {
    draft,
    setDraft,
    /** Applies the draft now: Enter. */
    apply: () => {
      const typed = draft.trim()
      if (typed !== latest) send(typed)
    },
    /** Empties the box and applies the empty value now. */
    clear: () => {
      setDraft("")
      if (latest !== "") send("")
    },
  }
}
