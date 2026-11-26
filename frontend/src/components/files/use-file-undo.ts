"use client"

import { useCallback, useEffect, useMemo, useRef } from "react"
import { ApiError, del, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import { sentenceCase } from "@/lib/format"
import { filesOwnKeyboard, isTypingTarget } from "@/components/files/keyboard"
import {
  carryIdentity,
  pushOperation,
  runUndo,
  type FileOperation,
  type UndoRunner,
  type UndoStep,
} from "@/components/files/undo"

const RUNNER: UndoRunner = {
  restore: (id) => post("/files/trash/restore", { id }),
  move: (from, to, identity) => post("/files/move", { from, to, expect: identity }),
  trash: (path, identity) =>
    del("/files/delete", { query: { path, recursive: true, expect: identity } }),
}

/** Why an undo step failed, in words: a taken name is the usual reason. */
function failure(step: UndoStep, error: unknown): string | undefined {
  if (error instanceof ApiError && error.status === 409 && error.code === "already_exists") {
    return step.kind === "restore"
      ? `Something now has the name ${step.name} where it was.`
      : "Something else now has that name."
  }
  if (error instanceof ApiError && error.code === "not_in_trash") {
    return "It is no longer in the Trash."
  }
  if (error instanceof ApiError && error.code === "entry_changed") {
    return "Something else has taken its name since, and was left alone."
  }
  return error instanceof Error ? error.message : undefined
}

/**
 * The page's undo: `record` announces an operation with an Undo on its notice
 * (or says it cannot be undone), and keeps it on this visit's stack;
 * `undoLast` takes the newest off the stack, so a second press reaches the
 * one before. Nothing is kept past the visit: undo is for the slip just made.
 *
 * Undos run one after another, in the order they were asked for. A press
 * while the one before is still talking to the server used to be dropped —
 * Ctrl+Z twice in quick succession undid one operation — so it waits its
 * turn instead. An operation is taken off the stack when it is asked for,
 * not when it finishes, which is what lets the second press reach the one
 * before; and one asked for twice (the notice's Undo pressed twice) runs once.
 */
export function useFileUndo(onChange: () => void) {
  const stack = useRef<FileOperation[]>([])
  const taken = useRef(new WeakSet<FileOperation>())
  // Asked for and waiting its turn, or running: a save can still carry it.
  const queued = useRef(new Set<FileOperation>())
  const queue = useRef<Promise<void>>(Promise.resolve())
  const changed = useRef(onChange)
  useEffect(() => {
    changed.current = onChange
  }, [onChange])

  const undo = useCallback((op: FileOperation) => {
    if (taken.current.has(op)) return queue.current
    taken.current.add(op)
    stack.current = stack.current.filter((held) => held !== op)
    if (op.steps.length === 0) {
      notify.info(`${sentenceCase(op.label)} cannot be undone`, { description: op.why })
      return queue.current
    }
    queued.current.add(op)
    const run = queue.current.then(async () => {
      try {
        const result = await runUndo(op, RUNNER)
        if (result.failed) {
          notify.error(`Could not undo ${op.label}`, undefined, {
            description: failure(result.failed.step, result.failed.error),
          })
        } else {
          notify.success(`Undid ${op.label}`)
        }
      } finally {
        queued.current.delete(op)
        changed.current()
      }
    })
    // The next undo waits for this one whether or not it failed.
    queue.current = run.catch(() => undefined)
    return run
  }, [])

  const undoLast = useCallback(() => {
    const op = stack.current.at(-1)
    if (!op) {
      notify.info("Nothing to undo")
      return
    }
    void undo(op)
  }, [undo])

  const record = useCallback(
    (op: FileOperation, title: string, description?: string) => {
      stack.current = pushOperation(stack.current, op)
      if (op.steps.length > 0) {
        notify.success(title, {
          description: description ?? op.why,
          action: { label: "Undo", onClick: () => void undo(op) },
        })
      } else {
        notify.success(title, { description: description ?? op.why ?? "It cannot be undone." })
      }
    },
    [undo],
  )

  /**
   * The editor saved `path`, replacing the file that had identity `was` with
   * one that has `now`: an undo still to run that names that file follows it.
   */
  const saved = useCallback((path: string, was?: string, now?: string) => {
    if (!was || !now) return
    for (const op of [...stack.current, ...queued.current]) carryIdentity(op, path, was, now)
  }, [])

  return useMemo(() => ({ record, undo, undoLast, saved }), [record, undo, undoLast, saved])
}

/**
 * Ctrl+Z (Cmd+Z) undoes the last file operation while the Files workspace
 * has the keyboard. A text field, an open dialog or menu keeps its own undo.
 */
export function useUndoShortcut(undoLast: () => void) {
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (!(event.ctrlKey || event.metaKey) || event.shiftKey || event.altKey) return
      if (event.key.toLowerCase() !== "z") return
      if (!filesOwnKeyboard(event) || isTypingTarget(event.target)) return
      event.preventDefault()
      undoLast()
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [undoLast])
}

/**
 * Undo for a surface without the page's stack (the editor's tree): restores
 * one trashed item and says how it went.
 */
export async function restoreTrashItem(id: string, name: string, after: () => void) {
  try {
    await RUNNER.restore(id)
    notify.success(`Restored ${name}`)
  } catch (error) {
    notify.error(`Could not restore ${name}`, undefined, {
      description: failure({ kind: "restore", id, name }, error),
    })
  } finally {
    after()
  }
}
