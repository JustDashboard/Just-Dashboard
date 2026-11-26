"use client"

import { Trash } from "@/components/icons"
import { del, get } from "@/lib/api"
import { plural } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { CertificateEvidence, EvidencePrune } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useConfirm } from "@/components/confirm-dialog"
import { Notice } from "@/components/state"
import { Hold } from "@/components/loading"
import { Button } from "@/components/ui/button"

/**
 * The copies of Caddy's certificates that deployments left: kept when one was
 * activated, for a domain no route serves and no release names any more.
 * Nothing on this host resolves to one, so removing them loses only the
 * record of a certificate Caddy has since let go. Offered only when there is
 * one to remove.
 *
 * It was "Caddy release copies" with a button to "Prune evidence for routes
 * that no longer exist": the code's words for them, which told the operator
 * neither what they are nor whether it was safe.
 */
export function CaddyEvidencePrune({ onPruned }: { onPruned: () => void }) {
  const { confirm, dialog } = useConfirm()
  const orphans = usePoll<CertificateEvidence[]>(
    (signal) => get("/certificates/evidence", undefined, signal),
    0,
  )
  const list = orphans.data ?? []
  // Whether there is anything to prune decides whether this is on the page:
  // what is under it waits to find out.
  const hold = <Hold until={!orphans.loading} />
  if (list.length === 0)
    return (
      <>
        {dialog}
        {hold}
      </>
    )

  const prune = () =>
    confirm({
      title: "Remove leftover certificate copies",
      confirmLabel: `Remove ${plural(list.length, "copy", "copies")}`,
      description: (
        <div className="space-y-3">
          <p>
            A copy of Caddy&apos;s certificate is kept each time a deployment goes live. No route
            serves these domains and no release names them, so nothing uses them. Any a deployment
            claims before this runs are kept.
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
            `Removed ${result.removed.length}; kept ${result.kept.length} a route or release names again`,
          )
        } else {
          notify.success(`Removed ${plural(result.removed.length, "copy", "copies")}`)
        }
        orphans.refresh()
        onPruned()
      },
    })

  return (
    <>
      <Notice
        data-slot="leftover-copies"
        title={`${list.length} leftover certificate ${list.length === 1 ? "copy" : "copies"}`}
      >
        <Button size="xs" variant="outline" onClick={prune}>
          <Trash className="size-3.5" />
          Remove
        </Button>
      </Notice>
      {dialog}
      {hold}
    </>
  )
}
