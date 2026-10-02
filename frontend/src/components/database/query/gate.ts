import type { Risk } from "@/components/database/query/types"

/**
 * Whether a statement may be sent, decided from the server's own reading of
 * it and from who is asking — before anything is sent.
 *
 * The server refuses the same things; this is what lets the page say why in
 * the reader's words, and ask before a statement that destroys. It is always
 * given a classification of the exact text about to run: a missing or failed
 * one is never read as "safe".
 */
export type Gate =
  /** It may run; `confirm` when the reader is asked first. */
  | { run: true; confirm: boolean }
  /** It may not, and this is why. */
  | { run: false; why: string }

export interface Asker {
  /** The role may run statements at all (`service.control`). */
  canRun: boolean
  /** The role may run what destroys (`destructive`). */
  canDestroy: boolean
  /** The connection is protected: the dashboard changes nothing through it. */
  readOnly: boolean
}

/** "drops a table and deletes rows", from the server's sentences. */
export function reasonsText(risk: Risk): string {
  const reasons = risk.reasons.filter(Boolean)
  if (reasons.length === 0) return ""
  if (reasons.length === 1) return reasons[0]
  return `${reasons.slice(0, -1).join(", ")} and ${reasons[reasons.length - 1]}`
}

export function gate(risk: Risk, asker: Asker): Gate {
  if (!asker.canRun) {
    return { run: false, why: "Your role reads this database and may not run statements." }
  }
  const what = reasonsText(risk)
  if (asker.readOnly && risk.level !== "read") {
    return {
      run: false,
      why: `This connection is protected, so only statements that read are run${what ? `. This one ${what}` : ""}.`,
    }
  }
  if (risk.destructive && !asker.canDestroy) {
    return {
      run: false,
      why: `Your role may not run a statement that destroys${what ? `. This one ${what}` : ""}.`,
    }
  }
  return { run: true, confirm: risk.destructive }
}

/** One word for what a statement would do, for the strip beside Run. */
export function riskWord(risk: Risk): string {
  if (risk.level === "read") return "reads"
  if (risk.level === "medium") return "writes"
  return "destroys"
}
