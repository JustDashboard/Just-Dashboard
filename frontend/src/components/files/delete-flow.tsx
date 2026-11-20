"use client"

import { useCallback } from "react"
import { del, get } from "@/lib/api"
import { notify } from "@/lib/toast"
import { plural } from "@/lib/format"
import type { FileEntry } from "@/lib/types"
import type { ConfirmRequest } from "@/components/confirm-dialog"
import { FileIcon } from "@/components/file-icon"
import {
  keepSentence,
  type DeleteResult,
  type TrashItem,
  type TrashPlan,
} from "@/components/files/trash"
import { deleteOperation, type FileOperation } from "@/components/files/undo"

/** The most entries asked about before the confirmation opens; the rest are assumed trashable. */
const CHECK_LIMIT = 100

type Refusal = { entry: FileEntry; reason: string }

/**
 * Which of these entries would go to the Trash, asked before the confirmation
 * opens so it can say what will really happen. A check that fails is not a
 * refusal: the delete itself will say if the Trash cannot take an entry.
 */
async function checkTrash(
  entries: FileEntry[],
): Promise<{ refused: Refusal[]; keepDays?: number }> {
  const plans = await Promise.all(
    entries
      .slice(0, CHECK_LIMIT)
      .map((entry) => get<TrashPlan>("/files/trash/check", { path: entry.path }).catch(() => null)),
  )
  const refused: Refusal[] = []
  let keepDays: number | undefined
  plans.forEach((plan, i) => {
    if (!plan || typeof plan.available !== "boolean") return
    if (plan.available) keepDays ??= plan.keepDays
    else refused.push({ entry: entries[i], reason: plan.reason ?? "it cannot go to the Trash" })
  })
  return { refused, keepDays }
}

const inside = (entries: FileEntry[]) =>
  entries.some((e) => e.isDir && !e.isSymlink) ? ", along with everything inside" : ""

/**
 * Delete on the Files page: to the Trash unless asked for good (Shift+Delete,
 * "Delete permanently") or unless the Trash cannot take it, which the
 * confirmation says with the reason before anything happens.
 *
 * The confirmation is the one that already guarded a delete; its sentence now
 * says where the thing goes rather than warning that it is lost. The notice
 * afterwards carries Undo, which restores from the Trash.
 */
export function useDeleteFlow({
  confirm,
  record,
  onDeleted,
}: {
  confirm: (request: ConfirmRequest) => void
  record: (op: FileOperation, title: string, description?: string) => void
  /** After the delete, with the paths that went, to clear the selection and reload. */
  onDeleted: (paths: string[]) => void
}) {
  return useCallback(
    async (entries: FileEntry[], permanent = false) => {
      if (entries.length === 0) return
      const { refused, keepDays } = permanent ? { refused: [] } : await checkTrash(entries)
      const forGood = new Set(
        permanent ? entries.map((e) => e.path) : refused.map((r) => r.entry.path),
      )
      const single = entries.length === 1 ? entries[0] : null
      const everythingForGood = forGood.size === entries.length

      let title: string
      let confirmLabel: string
      let description: React.ReactNode
      if (permanent) {
        title = single
          ? "Delete permanently"
          : `Delete ${plural(entries.length, "item")} permanently`
        confirmLabel = "Delete permanently"
        description = (
          <p>
            {single ? "It is" : "They are"} deleted permanently{inside(entries)}, without going to
            the Trash. This cannot be undone.
          </p>
        )
      } else if (everythingForGood) {
        title = single
          ? "Delete permanently"
          : `Delete ${plural(entries.length, "item")} permanently`
        confirmLabel = "Delete permanently"
        description = (
          <p>
            {single
              ? `It cannot go to the Trash: ${refused[0].reason}. It is deleted permanently${inside(entries)}, and this cannot be undone.`
              : `None of them can go to the Trash: ${refused[0].reason}. They are deleted permanently${inside(entries)}, and this cannot be undone.`}
          </p>
        )
      } else if (forGood.size > 0) {
        title = `Delete ${plural(entries.length, "item")}`
        confirmLabel = "Delete"
        description = (
          <>
            <p>
              {entries.length - forGood.size} go to the Trash, {keepSentence(keepDays)}.{" "}
              {plural(forGood.size, "item")} cannot, and {forGood.size === 1 ? "is" : "are"} deleted
              permanently:
            </p>
            <ul className="mt-2 space-y-0.5">
              {refused.slice(0, 4).map((r) => (
                <li key={r.entry.path} className="truncate">
                  <span className="font-mono">{r.entry.name}</span>: {r.reason}
                </li>
              ))}
              {refused.length > 4 && <li>and {refused.length - 4} more</li>}
            </ul>
          </>
        )
      } else {
        title = single ? "Move to Trash" : `Move ${plural(entries.length, "item")} to Trash`
        confirmLabel = "Move to Trash"
        description = (
          <p>
            {single ? "It goes" : "They go"} to the Trash{inside(entries)}, {keepSentence(keepDays)}
            , and can be restored from there.
          </p>
        )
      }

      confirm({
        title,
        confirmLabel,
        workingLabel: single ? undefined : "Deleting selected files…",
        subject: single
          ? {
              mark: <FileIcon entry={single} className="size-6" />,
              name: single.name,
              facts: <span className="font-mono break-all">{single.path}</span>,
            }
          : undefined,
        description,
        action: async (_, report) => {
          const trashed: TrashItem[] = []
          const gone: string[] = []
          let deletedForGood = 0
          let failed = 0
          let firstError: unknown
          report({ completed: 0, total: entries.length })
          for (const [index, entry] of entries.entries()) {
            report({ detail: entry.name, completed: index })
            try {
              const result = await del<DeleteResult>("/files/delete", {
                query: {
                  path: entry.path,
                  recursive: entry.isDir,
                  ...(forGood.has(entry.path) ? { permanent: true } : {}),
                },
              })
              gone.push(entry.path)
              if (result?.trashed && result.item) trashed.push(result.item)
              else deletedForGood++
            } catch (err) {
              failed++
              firstError ??= err
            }
            report({ completed: index + 1 })
          }
          if (gone.length > 0) onDeleted(gone)
          if (failed && gone.length === 0 && entries.length === 1) throw firstError
          if (gone.length > 0) {
            const what =
              gone.length === 1
                ? (entries.find((e) => e.path === gone[0])?.name ?? "1 item")
                : plural(gone.length, "item")
            const op = deleteOperation(`deleting ${what}`, trashed, deletedForGood)
            const said =
              trashed.length === 0
                ? `Deleted ${what} permanently`
                : deletedForGood === 0
                  ? `Moved ${what} to the Trash`
                  : `Deleted ${what}, ${trashed.length} to the Trash`
            record(op, said)
          }
          if (failed) notify.error(`${plural(failed, "item")} could not be deleted`, firstError)
          return "reported" as const
        },
      })
    },
    [confirm, record, onDeleted],
  )
}
