"use client"

import { Trash } from "@/components/icons"
import { del, get } from "@/lib/api"
import { notify } from "@/lib/toast"
import type { CertificateEvidence, EvidencePrune } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useConfirm } from "@/components/confirm-dialog"
import { Notice } from "@/components/state"
import { Button } from "@/components/ui/button"

/**
 * Caddy's release copies whose domain no route serves and no release names,
 * and the one way to clear them. Nothing on this host resolves to one any
 * more, so removing them loses only the record of a certificate Caddy has
 * since let go. Offered only when there is one to remove.
 */
export function CaddyEvidencePrune({ onPruned }: { onPruned: () => void }) {
  const { confirm, dialog } = useConfirm()
  const orphans = usePoll<CertificateEvidence[]>(
    (signal) => get("/certificates/evidence", undefined, signal),
    0,
  )
  const list = orphans.data ?? []
  if (list.length === 0) return dialog

  const prune = () =>
    confirm({
      title: "Prune evidence for routes that no longer exist",
      confirmLabel: `Delete ${list.length} ${list.length === 1 ? "copy" : "copies"}`,
      description: (
        <div className="space-y-3">
          <p>
            These are the copies of Caddy&apos;s certificates kept when a deployment was activated.
            No Caddy route serves their domains and no release names them, so nothing here can
            activate with one again. Any a deployment claims before this runs are kept.
          </p>
          <ul className="space-y-1">
            {list.map((copy) => (
              <li key={copy.name} className="min-w-0">
                <span className="break-all">{copy.domains.join(", ") || "No names reported"}</span>
                <span className="block font-mono text-hint break-all text-muted-foreground">
                  {copy.path}
                </span>
              </li>
            ))}
          </ul>
        </div>
      ),
      action: async () => {
        const result = await del<EvidencePrune>("/certificates/evidence", {
          body: { names: list.map((copy) => copy.name) },
        })
        if (result.kept.length > 0) {
          notify.warning(
            `Deleted ${result.removed.length}; kept ${result.kept.length} a route or release names again`,
          )
        } else {
          notify.success(
            `Deleted ${result.removed.length} release ${result.removed.length === 1 ? "copy" : "copies"}`,
          )
        }
        orphans.refresh()
        onPruned()
      },
    })

  return (
    <>
      <Notice
        title={`${list.length} Caddy release ${list.length === 1 ? "copy is" : "copies are"} left from routes that no longer exist`}
      >
        <div className="flex flex-wrap items-center gap-2">
          <span>No route serves their domains and no release names them.</span>
          <Button size="xs" variant="outline" onClick={prune}>
            <Trash className="size-3.5" />
            Prune evidence for routes that no longer exist
          </Button>
        </div>
      </Notice>
      {dialog}
    </>
  )
}
