"use client"

import { useCallback, useEffect, useMemo, useRef } from "react"
import { ApiError, del, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import { sentenceCase } from "@/lib/format"
import { filesOwnKeyboard, isTypingTarget } from "@/components/files/keyboard"
import {
  pushOperation,
  runUndo,
  type FileOperation,
  type UndoRunner,
  type UndoStep,
} from "@/components/files/undo"

const RUNNER: UndoRunner = {
  restore: (id) => post("/files/trash/restore", { id }),
  move: (from, to) => post("/files/move", { from, to }),
  trash: (path) => del("/files/delete", { query: { path, recursive: true } }),
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
  return error instanceof Error ? error.message : undefined
}

/**
 * The page's undo: `record` announces an operation with an Undo on its notice
 * (or says it cannot be undone), and keeps it on this visit's stack;
 * `undoLast` takes the newest off the stack, so a second press reaches the
 * one before. Nothing is kept past the visit: undo is for the slip just made.
 */
export function useFileUndo(onChange: () => void) {
  const stack = useRef<FileOperation[]>([])
  const busy = useRef(false)
  const changed = useRef(onChange)
  useEffect(() => {
    changed.current = onChange
  }, [onChange])

  const undo = useCallback(async (op: FileOperation) => {
    if (busy.current) return
    stack.current = stack.current.filter((held) => held !== op)
    if (op.steps.length === 0) {
      notify.info(`${sentenceCase(op.label)} cannot be undone`, { description: op.why })
      return
    }
    busy.current = true
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
      busy.current = false
      changed.current()
    }
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

  return useMemo(() => ({ record, undo, undoLast }), [record, undo, undoLast])
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
