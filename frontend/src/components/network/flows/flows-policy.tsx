"use client"

import { useState } from "react"
import { Field, FieldRow, FormNote } from "@/components/form"
import { SidePanel } from "@/components/side-panel"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { flowPolicyProblem, type FlowSettings } from "@/lib/network-flows"

/**
 * How often the recorder samples and how long it keeps what it sampled. The
 * change is reviewed before it is saved, because a shorter retention erases
 * at once and a new interval resets the byte baseline.
 */
export function FlowPolicySheet({
  open,
  onOpenChange,
  settings,
  disabled,
  onReview,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  settings: FlowSettings
  /** No fresh reading to act on. */
  disabled: boolean
  onReview: (intervalSeconds: number, retentionDays: number) => void
}) {
  const [interval, setInterval] = useState(String(settings.intervalSeconds))
  const [days, setDays] = useState(String(settings.retentionDays))
  const problem = flowPolicyProblem(interval, days)
  const changed =
    Number(interval) !== settings.intervalSeconds || Number(days) !== settings.retentionDays
  return (
    <SidePanel
      open={open}
      onOpenChange={onOpenChange}
      title="Sample interval and retention"
      description="How often sockets are sampled and how long the record is kept."
      width="md"
      initialFocus="body"
      footer={
        <>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button
            disabled={disabled || !changed || Boolean(problem)}
            onClick={() => onReview(Number(interval), Number(days))}
          >
            Review recording policy
          </Button>
        </>
      }
    >
      <div className="space-y-6">
        <FieldRow>
          <Field label="Sample interval (seconds)" htmlFor="flow-interval" hint="10 to 300.">
            <Input
              id="flow-interval"
              inputMode="numeric"
              className="font-mono"
              value={interval}
              onChange={(event) => setInterval(event.target.value)}
            />
          </Field>
          <Field label="Retention (days)" htmlFor="flow-days" hint="1 to 31.">
            <Input
              id="flow-days"
              inputMode="numeric"
              className="font-mono"
              value={days}
              onChange={(event) => setDays(event.target.value)}
            />
          </Field>
        </FieldRow>
        {problem && (
          <FormNote tone="danger" role="alert">
            {problem}
          </FormNote>
        )}
      </div>
    </SidePanel>
  )
}
