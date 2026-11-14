"use client"

import { InfoTip } from "@/components/form"
import { Modal } from "@/components/modal"
import { Panel, PanelBody, PanelHeader, Well } from "@/components/panel"
import { EmptyNote, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
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
 * The owned repairs the inspection can propose, one line each under one
 * sentence: a checkbox, the product that owns the resource, the resource over
 * why it is proposed, then in columns of their own what the repair does and
 * to what, how much of it changes, and whether it can be applied from here or
 * only reviewed. The reason a repair is review-only is the ⓘ beside that
 * word.
 *
 * It was a framed notice over half-width cards four lines tall, each a
 * different height by what it had to say. A row is one height now whatever
 * it carries, and the resources the plan leaves alone open from the title
 * line rather than folding out under the list.
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
          <>
            {plan.excluded.length > 0 && (
              <Popover>
                <PopoverTrigger asChild>
                  <Button size="sm" variant="ghost" className="text-muted-foreground">
                    {plural(plan.excluded.length, "excluded resource")}
                  </Button>
                </PopoverTrigger>
                <PopoverContent align="end" className="w-96">
                  <ul aria-label="Excluded resources" className="space-y-2.5">
                    {plan.excluded.map((item) => (
                      <li key={item.observationId} className="min-w-0">
                        <span className="block font-mono text-hint break-all">
                          {byObservation.get(item.observationId)?.label ?? item.observationId}
                        </span>
                        <span className="block text-hint text-muted-foreground">{item.reason}</span>
                      </li>
                    ))}
                  </ul>
                </PopoverContent>
              </Popover>
            )}
            <Button
              size="sm"
              variant={picked.length ? "default" : "outline"}
              disabled={busy || blocked || !picked.length}
              onClick={onReview}
            >
              Review selected ({picked.length})
            </Button>
          </>
        }
      />
      <PanelBody className="space-y-3">
        {plan.items.length > 0 && (
          <p className="text-hint text-muted-foreground">
            {plan.executable
              ? "Pick what to review. Boot files and owned admission rules can be applied one by one; the rest are proposals."
              : "Pick what to review. This plan applies nothing: it proposes owned resources and leaves foreign ones alone."}
          </p>
        )}
        {plan.blockers.map((reason) => (
          <p key={reason} className="text-body text-warning">
            {reason}
          </p>
        ))}
        {plan.items.length ? (
          <ul data-slot="repair-list" className="min-w-0 space-y-2">
            {plan.items.map((item) => (
              <RepairRow
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
      </PanelBody>
    </Panel>
  )
}

function RepairRow({
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
  // A route or a rule is saved under a number; it is named by where it goes.
  const named = row && (row.kind === "route" || row.kind === "rule")
  const name = named ? row.label : item.resource
  return (
    <li
      data-slot="repair-row"
      data-checked={checked || undefined}
      className={cn(
        "flex min-w-0 flex-wrap items-center gap-x-4 gap-y-1.5 rounded-xl border bg-choice-surface px-3 py-2.5 transition-colors sm:min-h-14 sm:flex-nowrap",
        checked && "border-rule-brand",
      )}
    >
      <div className="flex min-w-0 flex-1 basis-64 items-center gap-3">
        <Checkbox
          id={id}
          aria-label={`Select repair for ${item.resource}`}
          disabled={disabled}
          checked={checked}
          onCheckedChange={(value) => onChange(value === true)}
        />
        {row && <DriftMark row={row} />}
        <label htmlFor={id} className="min-w-0 flex-1 cursor-pointer">
          <span className="block truncate font-mono text-body font-medium" title={name}>
            {name}
            {named && (
              <span className="ml-2 font-sans text-hint font-normal text-muted-foreground">
                {row.detail}
              </span>
            )}
          </span>
          <span className="block truncate text-hint text-muted-foreground" title={item.reason}>
            {item.reason}
          </span>
        </label>
      </div>
      <div className="min-w-0 max-sm:pl-7 sm:w-48 sm:shrink-0">
        <p className="truncate text-xs font-medium first-letter:uppercase">
          {actionWords(item.action)}
        </p>
        <p className="flex min-w-0 items-center gap-2 text-hint text-muted-foreground">
          <span className="truncate">
            {item.effect === "boot_files"
              ? "saved boot input"
              : item.effect
                ? "rule in the kernel"
                : "a proposal"}
          </span>
          {changes && <DiffCount {...changes} />}
        </p>
      </div>
      <p className="flex shrink-0 items-center gap-1.5 sm:w-28">
        <Status
          tone={item.executable ? "running" : "stopped"}
          label={item.executable ? "Can apply" : "Review only"}
        />
        {item.blocker && (
          <InfoTip label={`Why ${item.resource} is review only`}>{item.blocker}</InfoTip>
        )}
      </p>
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
