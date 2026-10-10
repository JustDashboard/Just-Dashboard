"use client"

import { useCallback, useRef } from "react"
import { notify } from "@/lib/toast"
import { useConfirm } from "@/components/confirm-dialog"
import { Statement } from "@/components/form"
import { EngineMark } from "@/components/database/kit"
import { useDatabase } from "@/components/database/shell/database-context"
import type { Subject } from "@/components/database/schema/change-dialog"
import type { DdlRequest } from "@/components/database/schema/changes"
import { useFocusReturn } from "@/components/database/schema/focus"
import { previewChange, runChange } from "@/components/database/schema/use-ddl"

export type Destruction = {
  request: DdlRequest
  /** The act and its confirm button: "Drop column". */
  title: string
  subject: Subject
  /** What is lost, in a sentence. */
  sentence: React.ReactNode
  /** What the toast says once it is done: "Dropped note from orders". */
  done: string
  onDone: () => void
}

/** How long a statement may take to be read before the wait is said. */
const SAY_AFTER_MS = 350

/**
 * The changes that destroy something — a dropped column, index, key, table,
 * view or schema, an emptied table — each asked about by name before it runs.
 *
 * The confirmation shows the thing with the engine's mark and the statement
 * the server says it will run, read from the same route with `?preview=1`.
 * That read is made once, when the dialog is asked for: a preview of a drop
 * spends the same budget as a drop, so it is never repeated while the dialog
 * is open — and a second press while the first is still being read is not a
 * second ask. A read that takes more than a moment says it is being made, so
 * the press is not a dead one.
 *
 * When the confirmation closes, the keyboard goes back to the control that
 * asked for it — or, where that control went with what was dropped, to the
 * nearest thing still on the page.
 */
export function useDestroy() {
  const { id, engine } = useDatabase()
  const { confirm, dialog } = useConfirm()
  useFocusReturn((dialog.props as { request: unknown }).request !== null)
  const reading = useRef(false)

  const destroy = useCallback(
    async (spec: Destruction) => {
      if (reading.current) return
      reading.current = true
      let said: string | number | undefined
      const slow = window.setTimeout(() => {
        said = notify.loading(`${spec.title}: reading the statement…`)
      }, SAY_AFTER_MS)
      let statement: string
      try {
        statement = (await previewChange(id, spec.request)).statement
      } catch (err) {
        notify.error(`${spec.title}: the statement could not be read`, err)
        return
      } finally {
        window.clearTimeout(slow)
        if (said !== undefined) notify.dismiss(said)
        reading.current = false
      }
      confirm({
        title: spec.title,
        confirmLabel: spec.title,
        subject: { mark: <EngineMark engine={engine} size="sm" />, ...spec.subject },
        description: (
          <>
            <p>{spec.sentence}</p>
            <Statement sql={statement} placeholder="" />
          </>
        ),
        action: async () => {
          const answer = await runChange(id, spec.request)
          notify.success(spec.done, { description: answer.statement })
          spec.onDone()
          return "reported"
        },
      })
    },
    [id, engine, confirm],
  )

  return { destroy, dialog }
}
