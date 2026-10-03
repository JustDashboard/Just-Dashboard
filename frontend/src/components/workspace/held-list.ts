"use client"

import { useState } from "react"

export function heldRows<T>(previous: string[], rows: T[], key: (row: T) => string) {
  const byId = new Map(rows.map((row) => [key(row), row]))
  return previous.flatMap((id) => (byId.has(id) ? [byId.get(id)!] : []))
}

/** Polls update existing readings; newly arrived rows wait for an explicit reveal. */
export function useHeldList<T>(rows: T[] | undefined, key: (row: T) => string, question = "") {
  const [held, setHeld] = useState<{ question: string; ids: string[] } | null>(null)
  if (rows && (!held || held.question !== question)) setHeld({ question, ids: rows.map(key) })
  const baseline = held?.question === question ? held.ids : (rows?.map(key) ?? [])
  const known = new Set(baseline)
  const pending = (rows ?? []).filter((row) => !known.has(key(row))).length
  return {
    rows: heldRows(baseline, rows ?? [], key),
    pending,
    reveal: () => setHeld({ question, ids: (rows ?? []).map(key) }),
  }
}
