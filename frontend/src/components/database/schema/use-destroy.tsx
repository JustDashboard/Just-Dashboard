"use client"

import { useCallback } from "react"
import { notify } from "@/lib/toast"
import { useConfirm } from "@/components/confirm-dialog"
import { Statement } from "@/components/form"
import { EngineMark } from "@/components/database/kit"
import { useDatabase } from "@/components/database/shell/database-context"
import type { Subject } from "@/components/database/schema/change-dialog"
import type { DdlRequest } from "@/components/database/schema/changes"
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

/**
 * The changes that destroy something — a dropped column, index, key, table,
 * view or schema, an emptied table — each asked about by name before it runs.
 *
 * The confirmation shows the thing with the engine's mark and the statement
 * the server says it will run, read from the same route with `?preview=1`.
 * That read is made once, when the dialog is asked for: a preview of a drop
 * spends the same budget as a drop, so it is never repeated while the dialog
 * is open.
 */
export function useDestroy() {
  const { id, engine } = useDatabase()
  const { confirm, dialog } = useConfirm()

  const destroy = useCallback(
    async (spec: Destruction) => {
      let statement: string
      try {
        statement = (await previewChange(id, spec.request)).statement
      } catch (err) {
        notify.error(`${spec.title}: the statement could not be read`, err)
        return
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
