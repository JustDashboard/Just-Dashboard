import { plural } from "@/lib/format"
import type { DriftReport } from "@/lib/network-drift"
import { Disclosure } from "@/components/form"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Row, RowList } from "@/components/row-list"
import { EmptyNote, Notice } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"

/**
 * The repairs the evidence supports, each a resource this dashboard owns and
 * can put back. Selecting is free; nothing is sent until the review dialog,
 * which shows the exact change. A selection is dropped when the evidence under
 * it changes (`driftReviewKey`), so a ticked box never refers to a report the
 * reader has not seen.
 */
export function DriftRepairs({
  plan,
  selected,
  disabled,
  reviewDisabled,
  onSelect,
  onReview,
}: {
  plan: DriftReport["repairPlan"]
  selected: string[]
  disabled: boolean
  reviewDisabled: boolean
  onSelect: (ids: string[]) => void
  onReview: () => void
}) {
  return (
    <Panel plain>
      <PanelHeader
        title="Owned repair plan"
        actions={
          <Button size="sm" variant="outline" disabled={reviewDisabled} onClick={onReview}>
            Review selected ({selected.length})
          </Button>
        }
      />
      <PanelBody>
        <div className="space-y-3">
          <Notice title={plan.executable ? "Selected repairs available" : "Review only"}>
            {plan.executable
              ? "Reviewed boot files and owned admission rules can be repaired individually. Other proposals remain available for review."
              : "This plan cannot execute repairs. It proposes owned resources for review and preserves foreign resources."}
          </Notice>
          {plan.blockers.map((reason) => (
            <p key={reason} className="text-body text-warning">
              {reason}
            </p>
          ))}
          {plan.items.length ? (
            <RowList>
              {plan.items.map((item) => (
                <Row
                  key={item.id}
                  title={
                    <label htmlFor={`repair-${item.id}`} className="cursor-pointer break-all">
                      {item.resource}
                    </label>
                  }
                  subtitle={
                    <>
                      {item.reason}
                      {item.blocker && <span className="block">Review only: {item.blocker}</span>}
                    </>
                  }
                  leading={
                    <Checkbox
                      id={`repair-${item.id}`}
                      aria-label={`Select repair for ${item.resource}`}
                      disabled={disabled}
                      checked={selected.includes(item.id)}
                      onCheckedChange={(checked) =>
                        onSelect(
                          checked
                            ? [...selected, item.id]
                            : selected.filter((id) => id !== item.id),
                        )
                      }
                    />
                  }
                />
              ))}
            </RowList>
          ) : (
            <EmptyNote>No known owned repair can be proposed from this evidence.</EmptyNote>
          )}
          {plan.excluded.length > 0 && (
            <Disclosure quiet summary={plural(plan.excluded.length, "excluded resource")}>
              <RowList>
                {plan.excluded.map((item) => (
                  <Row key={item.observationId} title={item.observationId} subtitle={item.reason} />
                ))}
              </RowList>
            </Disclosure>
          )}
        </div>
      </PanelBody>
    </Panel>
  )
}
