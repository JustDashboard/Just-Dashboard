import type { TrashItem } from "@/components/files/trash"

/**
 * Undo for the Files page, as data.
 *
 * Every file operation the page makes — a delete, a move, a rename, a copy —
 * is recorded with what undoing it takes, in the order it has to happen, so
 * the notice's Undo and Ctrl+Z run the same steps. Undoing never needs the
 * server to remember anything: a delete is undone by restoring from the
 * Trash, a move by moving back, a copy by putting the copy in the Trash, and
 * whatever Replace put in the Trash comes back after the thing that took its
 * name has gone.
 */
export type UndoStep =
  /** Put a Trash item back where it was. */
  | { kind: "restore"; id: string; name: string }
  /**
   * Move an entry back. `identity` is the entry the move answered with: an
   * undo that waited its turn leaves whatever took the name meanwhile alone.
   */
  | { kind: "move"; from: string; to: string; identity?: string }
  /** Put an entry in the Trash: the copy an undone copy made, by its identity too. */
  | { kind: "trash"; path: string; identity?: string }

export type FileOperation = {
  /** What was done, worded to follow "Undid" and "Could not undo": "the move of 3 items". */
  label: string
  /** What undoing it does, in order. Empty when it cannot be undone. */
  steps: UndoStep[]
  /** Why it cannot be undone, said in place of an Undo. */
  why?: string
}

/** A delete: each trashed item comes back. Permanent deletes are not undoable. */
export function deleteOperation(
  label: string,
  trashed: TrashItem[],
  forGood: number,
): FileOperation {
  if (trashed.length === 0) {
    return { label, steps: [], why: forGood > 0 ? "It cannot be undone." : undefined }
  }
  return {
    label,
    steps: [...trashed]
      .reverse()
      .map((item) => ({ kind: "restore", id: item.id, name: item.name })),
    why: forGood > 0 ? "What was deleted permanently cannot come back." : undefined,
  }
}

export type Moved = { from: string; to: string; identity?: string; replaced?: TrashItem }

/** A move or a rename: the last moved goes back first, then what it replaced returns. */
export function moveOperation(label: string, moved: Moved[]): FileOperation {
  const steps: UndoStep[] = []
  for (const m of [...moved].reverse()) {
    steps.push({ kind: "move", from: m.to, to: m.from, identity: m.identity })
    if (m.replaced) steps.push({ kind: "restore", id: m.replaced.id, name: m.replaced.name })
  }
  return { label, steps }
}

export type Copied = { to: string; identity?: string; replaced?: TrashItem }

/**
 * A copy or a duplicate: the copy goes to the Trash, then what it replaced
 * returns. Putting the copy in the Trash is a delete, so an account that may
 * not delete cannot undo a copy, and is told so rather than offered it.
 */
export function copyOperation(label: string, copied: Copied[], canTrash: boolean): FileOperation {
  if (!canTrash) {
    return { label, steps: [], why: "Undoing a copy deletes it, which your account cannot do." }
  }
  const steps: UndoStep[] = []
  for (const c of [...copied].reverse()) {
    steps.push({ kind: "trash", path: c.to, identity: c.identity })
    if (c.replaced) steps.push({ kind: "restore", id: c.replaced.id, name: c.replaced.name })
  }
  return { label, steps }
}

export type UndoRunner = {
  restore: (id: string) => Promise<unknown>
  move: (from: string, to: string, identity?: string) => Promise<unknown>
  trash: (path: string, identity?: string) => Promise<unknown>
}

/**
 * Runs an operation's steps in order and stops at the first that fails, since
 * a later step may rely on it (what a move replaced cannot come back while
 * the moved entry still holds its name). Returns the failure, if any.
 */
export async function runUndo(
  op: FileOperation,
  run: UndoRunner,
): Promise<{ done: number; failed?: { step: UndoStep; error: unknown } }> {
  let done = 0
  for (const step of op.steps) {
    try {
      if (step.kind === "restore") await run.restore(step.id)
      else if (step.kind === "move") await run.move(step.from, step.to, step.identity)
      else await run.trash(step.path, step.identity)
      done++
    } catch (error) {
      return { done, failed: { step, error } }
    }
  }
  return { done }
}

/**
 * A save in the dashboard's editor writes a new file over the name, which
 * gives the file another identity (`PUT /files/write` answers the one it
 * replaced and the one it left): a step waiting on the file it saved is
 * carried from the one to the other. A step that expects anything else at
 * that name, or another path, is left as it is, so what took the name is
 * still refused.
 */
export function carryIdentity(op: FileOperation, path: string, was: string, now: string) {
  if (!was || !now || was === now) return
  op.steps = op.steps.map((step) =>
    (step.kind === "move" && step.from === path && step.identity === was) ||
    (step.kind === "trash" && step.path === path && step.identity === was)
      ? { ...step, identity: now }
      : step,
  )
}

/** The stack of this visit's operations, newest last, kept to a bound. */
export const UNDO_DEPTH = 50

export function pushOperation(stack: FileOperation[], op: FileOperation): FileOperation[] {
  const next = [...stack, op]
  return next.length > UNDO_DEPTH ? next.slice(next.length - UNDO_DEPTH) : next
}
