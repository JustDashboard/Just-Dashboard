"use client"

import { Disclosure } from "@/components/form"
import { Modal } from "@/components/modal"
import { Panel, PanelBody, PanelHeader, Well } from "@/components/panel"
import { EmptyNote, Notice } from "@/components/state"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { SpotlightBorder } from "@/components/ui/spotlight-border"
import { DiffCount } from "@/components/docker/stack-diff"
import { diffCounts, lineDiff } from "@/components/docker/stack-views"
import { plural } from "@/lib/format"
import type { DriftReport, DriftRow, OwnedRepair } from "@/lib/network-drift"
import { cn } from "@/lib/utils"
import { DriftMark, ShortDigest } from "@/components/network/drift/marks"

/** The repair's action as words: `regenerate_owned_file` is "regenerate owned file". */
function actionWords(action: string) {
  return action.replaceAll("_", " ")
}

/**
 * The owned repairs the inspection can propose. Each is a thing you pick, so
 * it is a card whose edge answers the pointer (§16), drawn as the product
 * that owns it with its action in words, what the repair would change and —
 * where it can only be reviewed — the reason, in amber, that it cannot be
 * applied from here.
 */
export function DriftRepairs({
  plan,
  rows,
  selected,
  blocked,
  busy,
  onToggle,
  onReview,
}: {
  plan: DriftReport["repairPlan"]
  rows: DriftRow[]
  selected: string[]
  blocked: boolean
  busy: boolean
  onToggle: (id: string, checked: boolean) => void
  onReview: () => void
}) {
  const byObservation = new Map(rows.map((row) => [row.id, row]))
  const picked = plan.items.filter((item) => selected.includes(item.id))
  return (
    <Panel plain aria-label="Owned repair plan">
      <PanelHeader
        title="Owned repair plan"
        actions={
          <Button
            size="sm"
            variant={picked.length ? "default" : "outline"}
            disabled={busy || blocked || !picked.length}
            onClick={onReview}
          >
            Review selected ({picked.length})
          </Button>
        }
      />
      <PanelBody className="space-y-3">
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
          <ul className="grid gap-2.5 lg:grid-cols-2">
            {plan.items.map((item) => (
              <RepairCard
                key={item.id}
                item={item}
                row={byObservation.get(item.observationId)}
                checked={selected.includes(item.id)}
                disabled={blocked || busy}
                onChange={(checked) => onToggle(item.id, checked)}
              />
            ))}
          </ul>
        ) : (
          <EmptyNote>No known owned repair can be proposed from this evidence.</EmptyNote>
        )}
        {plan.excluded.length > 0 && (
          <Disclosure quiet summary={plural(plan.excluded.length, "excluded resource")}>
            <ul className="space-y-2 text-body">
              {plan.excluded.map((item) => (
                <li key={item.observationId} className="min-w-0">
                  <span className="font-mono text-hint break-all">
                    {byObservation.get(item.observationId)?.label ?? item.observationId}
                  </span>
                  <span className="block text-hint text-muted-foreground">{item.reason}</span>
                </li>
              ))}
            </ul>
          </Disclosure>
        )}
      </PanelBody>
    </Panel>
  )
}

function RepairCard({
  item,
  row,
  checked,
  disabled,
  onChange,
}: {
  item: OwnedRepair
  row?: DriftRow
  checked: boolean
  disabled: boolean
  onChange: (checked: boolean) => void
}) {
  const id = `repair-${item.id}`
  const changes = item.after ? diffCounts(lineDiff(item.before ?? "", item.after)) : undefined
  return (
    <li className="min-w-0">
      <SpotlightBorder resting={checked ? "brand" : "border"} radius={320} className="h-full">
        <label htmlFor={id} className="flex h-full min-w-0 cursor-pointer gap-3 p-3.5">
          <Checkbox
            id={id}
            aria-label={`Select repair for ${item.resource}`}
            disabled={disabled}
            checked={checked}
            onCheckedChange={(value) => onChange(value === true)}
            className="mt-2"
          />
          {row && <DriftMark row={row} />}
          <span className="min-w-0 flex-1 space-y-1">
            {/* A route or a rule is saved under a number; it is named by where it goes. */}
            {row && (row.kind === "route" || row.kind === "rule") ? (
              <span className="block font-mono text-body break-all">
                {row.label}
                <span className="ml-2 font-sans text-hint text-muted-foreground">{row.detail}</span>
              </span>
            ) : (
              <span className="block font-mono text-body break-all">{item.resource}</span>
            )}
            <span className="flex flex-wrap items-center gap-x-3 gap-y-1">
              <Tag tone={item.executable ? "success" : "default"}>{actionWords(item.action)}</Tag>
              {item.effect && (
                <span className="text-hint text-muted-foreground">
                  {item.effect === "boot_files" ? "saved boot input" : "rule in the kernel"}
                </span>
              )}
              {changes && <DiffCount {...changes} />}
            </span>
            <span className="block text-hint text-muted-foreground">{item.reason}</span>
            {item.blocker && (
              <span className="block text-hint text-warning">Review only: {item.blocker}</span>
            )}
          </span>
        </label>
      </SpotlightBorder>
    </li>
  )
}

/**
 * The destructive review of the selected repairs: each one's exact change
 * as a diff of its before and after, its preconditions, and the generation
 * the review is bound to. Apply is drawn only where an administrator with
 * destructive permission holds an executable selection.
 */
export function DriftReview({
  open,
  onOpenChange,
  items,
  plan,
  canApply,
  blocked,
  busy,
  actionError,
  onClose,
  onApply,
  canRepair,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  items: OwnedRepair[]
  plan: DriftReport["repairPlan"]
  canApply: boolean
  canRepair: boolean
  blocked: boolean
  busy: boolean
  actionError?: string
  onClose: () => void
  onApply: () => void
}) {
  return (
    <Modal
      open={open}
      onOpenChange={onOpenChange}
      title="Review selected owned repairs"
      description="Inspect the selected exact changes and their preconditions before applying."
      size="lg"
      footer={
        <>
          <Button variant="outline" disabled={busy} onClick={onClose}>
            Close review
          </Button>
          {canApply && (
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
        {!canApply ? (
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
              <ReviewedRepair key={item.id} item={item} />
            ))}
          </div>
        )}
        <p className="flex flex-wrap items-center gap-x-2 text-body">
          <span className="text-muted-foreground">Reviewed generation</span>
          <ShortDigest value={plan.generation} />
        </p>
        <ul className="list-disc space-y-1 pl-5 text-body text-muted-foreground">
          {plan.preconditions.map((text) => (
            <li key={text}>{text}</li>
          ))}
        </ul>
      </div>
    </Modal>
  )
}

function ReviewedRepair({ item }: { item: OwnedRepair }) {
  const lines = item.after ? lineDiff(item.before ?? "", item.after) : []
  return (
    <section className="min-w-0 space-y-3">
      <header className="flex min-w-0 items-start justify-between gap-3">
        <h3 className="min-w-0 font-mono text-body font-medium break-all">{item.resource}</h3>
        {item.after && <DiffCount {...diffCounts(lines)} />}
      </header>
      <p className="text-body text-muted-foreground">{item.reason}</p>
      {item.blocker && <p className="text-body text-warning">Review only: {item.blocker}</p>}
      {item.effect && (
        <p className="text-body">
          {item.effect === "boot_files"
            ? "Repairs this saved boot input. A future restore will read it."
            : "Repairs this owned admission rule in the current kernel."}
        </p>
      )}
      {item.after && (
        <>
          {!item.before && <p className="text-hint text-muted-foreground">File absent before</p>}
          {/* The exact change, in the well every read-only output sits in. */}
          <Well className="p-0 text-hint">
            <pre className="py-1">
              {lines.map((line, index) => (
                <div
                  key={index}
                  className={cn(
                    "flex px-3 break-all whitespace-pre-wrap",
                    line.kind === "added" && "bg-wash-success text-success",
                    line.kind === "removed" && "bg-wash-danger text-destructive",
                    line.kind !== "added" && line.kind !== "removed" && "text-muted-foreground",
                  )}
                >
                  <span aria-hidden className="w-4 shrink-0 select-none">
                    {line.kind === "added" ? "+" : line.kind === "removed" ? "−" : ""}
                  </span>
                  <span className="sr-only">
                    {line.kind === "added" ? "after: " : line.kind === "removed" ? "before: " : ""}
                  </span>
                  <span className="min-w-0">{line.text}</span>
                </div>
              ))}
            </pre>
          </Well>
        </>
      )}
      <ul className="list-disc space-y-1 pl-5 text-body text-muted-foreground">
        {item.preconditions.map((text) => (
          <li key={text}>{text}</li>
        ))}
      </ul>
    </section>
  )
}
