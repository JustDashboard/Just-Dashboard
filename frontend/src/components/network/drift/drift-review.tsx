import type { DriftReport, OwnedRepair } from "@/lib/network-drift"
import { Modal } from "@/components/modal"
import { Notice } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Digest } from "@/components/network/drift/digest"

/**
 * The exact change each selected repair would make, and what must still hold
 * when it is applied. Apply is offered only to an administrator with
 * destructive permission and only for an executable selection; the page
 * rereads the report first and refuses if the evidence moved.
 */
export function DriftReview({
  open,
  busy,
  blocked,
  canRepair,
  executable,
  items,
  plan,
  actionError,
  onClose,
  onApply,
}: {
  open: boolean
  busy: boolean
  blocked: boolean
  canRepair: boolean
  /** Whether the selection can be sent at all. */
  executable: boolean
  items: OwnedRepair[]
  plan: DriftReport["repairPlan"]
  actionError?: string
  onClose: () => void
  onApply: () => void
}) {
  return (
    <Modal
      open={open}
      onOpenChange={(next) => {
        if (!busy && !next) onClose()
      }}
      title="Review selected owned repairs"
      description="Inspect the selected exact changes and their preconditions before applying."
      size="lg"
      footer={
        <>
          <Button variant="outline" disabled={busy} onClick={onClose}>
            Close review
          </Button>
          {canRepair && executable && (
            <Button
              variant="destructive"
              disabled={blocked || Boolean(actionError) || busy}
              pending={busy}
              onClick={onApply}
            >
              Apply selected repairs
            </Button>
          )}
        </>
      }
    >
      <div className="space-y-4">
        {!executable || !canRepair ? (
          <Notice title="No changes will be applied">
            {canRepair
              ? "The selections include resources available for review only."
              : "An administrator with destructive permission can apply executable selected repairs."}
          </Notice>
        ) : (
          <Notice title="Apply only these reviewed resources">
            File selections repair saved boot inputs. Admission selections repair owned rules in the
            current kernel. The server rereads ownership and evidence before applying.
          </Notice>
        )}
        {actionError && (
          <Notice tone="warning" title="Repair needs review">
            {actionError}
          </Notice>
        )}
        {blocked || !items.length ? (
          <Notice tone="warning" title="The reviewed evidence changed">
            Close this review and inspect the network again before selecting repairs.
          </Notice>
        ) : (
          <div className="space-y-6">
            {items.map((item) => (
              <section key={item.id} className="space-y-3">
                <h3 className="font-mono text-body font-medium break-all">{item.resource}</h3>
                <p className="text-body text-muted-foreground">{item.reason}</p>
                {item.blocker && (
                  <p className="text-body text-warning">Review only: {item.blocker}</p>
                )}
                {item.effect && (
                  <p className="text-body">
                    {item.effect === "boot_files"
                      ? "Repairs this saved boot input. A future restore will read it."
                      : "Repairs this owned admission rule in the current kernel."}
                  </p>
                )}
                {item.after && (
                  <>
                    <p className="text-hint text-muted-foreground">Before</p>
                    <pre className="font-mono text-hint break-all whitespace-pre-wrap">
                      {item.before || "File absent"}
                    </pre>
                    <p className="text-hint text-muted-foreground">After</p>
                    <pre className="font-mono text-hint break-all whitespace-pre-wrap">
                      {item.after}
                    </pre>
                  </>
                )}
                <ul className="list-disc space-y-1 pl-5 text-body text-muted-foreground">
                  {item.preconditions.map((text) => (
                    <li key={text}>{text}</li>
                  ))}
                </ul>
              </section>
            ))}
          </div>
        )}
        <Digest label="Reviewed generation" value={plan.generation} />
        <ul className="list-disc space-y-1 pl-5 text-body text-muted-foreground">
          {plan.preconditions.map((text) => (
            <li key={text}>{text}</li>
          ))}
        </ul>
      </div>
    </Modal>
  )
}
