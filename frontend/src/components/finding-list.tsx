"use client"

import { useId, useState } from "react"
import Link from "next/link"
import { ArrowRight, CheckCircle, ChevronDown, Cross } from "@/components/icons"
import { Row, RowList } from "@/components/row-list"
import { Button } from "@/components/ui/button"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import { StatusDot, type DotTone } from "@/components/status-dot"
import { useArrivals } from "@/hooks/use-arrivals"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"

export type FindingAction = {
  label: string
  pending?: boolean
  disabled?: boolean
} & (
  | { href: string; onClick?: never; reveal?: never }
  | { onClick: () => void | Promise<unknown>; href?: never; reveal?: never }
  | { reveal: true; href?: never; onClick?: never }
)

export type Finding = {
  id: string
  level: "critical" | "warning" | "notice"
  title: string
  detail: string
  /** The consequence to read before opening the measured evidence. */
  summary?: string
  advice?: string
  meta?: React.ReactNode
  action?: FindingAction
  extra?: React.ReactNode
}

const LEVEL_TONE: Record<Finding["level"], DotTone> = {
  critical: "danger",
  warning: "warning",
  notice: "notice",
}

const LEVEL_WORD: Record<Finding["level"], string> = {
  critical: "Critical issue",
  warning: "Warning",
  notice: "Observation",
}

/**
 * A list of findings: each a `Row` holding the problem, its consequence and
 * the next action, with the evidence in a popover off its title.
 *
 * It was an accordion, and every finding opened its evidence under itself —
 * on Traffic's errors a log sample a dozen lines long pushed the list under
 * it down the page. List rows never grow (D11): one height in every state,
 * details in a popover or a page. The row keeps what a reader decides by; the
 * measured detail, the advice and whatever the caller adds (`extra`: a log
 * sample, a link to copy) are a press away, at the row they belong to.
 *
 * On a narrow column the action and the dismiss move to a line of their own
 * under the words, the same in every state.
 */
export function FindingList({
  findings,
  emptyLabel = "No problems found in the completed checks",
  onDismiss,
  anchor,
  defaultOpen,
  emphasize = false,
}: {
  findings: Finding[]
  emptyLabel?: string
  onDismiss?: (id: string) => void
  /** The id a finding's row takes, for a link to it (`#…`). */
  anchor?: (id: string) => string
  /** The finding a link pointed at: its row is marked current and its evidence opens. */
  defaultOpen?: string[]
  /** An attention list gives its first remedy the command face; diagnostic reports stay secondary. */
  emphasize?: boolean
}) {
  const arrivals = useArrivals(findings.map((finding) => finding.id))
  const primaryId = emphasize ? findings.find((finding) => finding.action)?.id : undefined
  const linked = defaultOpen?.[0]
  if (findings.length === 0) {
    if (!emptyLabel) return null
    return (
      <div className="flex items-center gap-2.5 text-body text-muted-foreground">
        <CheckCircle aria-hidden className="size-4 shrink-0 text-success" />
        <span className="min-w-0">{emptyLabel}</span>
      </div>
    )
  }

  return (
    <RowList data-slot="finding-list" className="@container/finding">
      {findings.map((finding) => (
        <FindingRow
          key={finding.id}
          finding={finding}
          id={anchor?.(finding.id)}
          linked={finding.id === linked}
          arrived={arrivals.has(finding.id)}
          primary={finding.id === primaryId}
          onDismiss={onDismiss}
        />
      ))}
    </RowList>
  )
}

function FindingRow({
  finding,
  id,
  linked,
  arrived,
  primary,
  onDismiss,
}: {
  finding: Finding
  id?: string
  linked: boolean
  arrived: boolean
  primary: boolean
  onDismiss?: (id: string) => void
}) {
  // The reader's own press wins; until then a link's finding stands open,
  // read as it arrives (the hash is read after the list first draws).
  const [held, setOpen] = useState<boolean>()
  const open = held ?? linked
  const words = useId()
  const summary = finding.summary ?? finding.detail
  const showSummary = Boolean(summary) && summary !== finding.title
  const showDetail = Boolean(finding.detail) && finding.detail !== finding.title
  const evidence = showDetail || finding.advice || finding.extra
  const actions = (finding.meta || finding.action || onDismiss) && (
    // Under the words on a narrow column, set in past the dot so they start
    // where the title does; beside them once the column holds both.
    <span className="flex min-w-0 shrink-0 basis-full flex-wrap items-center gap-2 pl-4.5 @xl/finding:basis-auto @xl/finding:pl-0">
      {finding.meta && (
        <span className="flex min-w-0 items-center gap-1 text-hint text-muted-foreground">
          {finding.meta}
        </span>
      )}
      {finding.action && (
        <FindingActionButton
          action={finding.action}
          primary={primary}
          onReveal={() => setOpen(true)}
        />
      )}
      {onDismiss && (
        <button
          type="button"
          aria-label={`Dismiss ${finding.title}`}
          title="Dismiss until the next check"
          onClick={() => onDismiss(finding.id)}
          className="shrink-0 rounded-sm p-1.5 text-muted-foreground focus-ring transition-colors hover:text-foreground"
        >
          <Cross aria-hidden className="size-3.5" />
        </button>
      )}
    </span>
  )
  const heading = (
    <>
      <span id={`${words}-level`} className="sr-only">
        {LEVEL_WORD[finding.level]}
      </span>
      {evidence ? (
        <PopoverTrigger asChild>
          <button
            type="button"
            aria-label={finding.title}
            aria-describedby={`${words}-level${showSummary ? ` ${words}-summary` : ""}`}
            className="inline-flex max-w-full min-w-0 items-center gap-1 rounded-sm text-left focus-ring"
          >
            <span className="min-w-0 truncate">{finding.title}</span>
            <ChevronDown
              aria-hidden
              className={cn(
                "size-3.5 shrink-0 text-muted-foreground transition-transform",
                open && "rotate-180",
              )}
            />
          </button>
        </PopoverTrigger>
      ) : (
        <span aria-describedby={`${words}-level`}>{finding.title}</span>
      )}
    </>
  )
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <Row
        id={id}
        data-finding={finding.id}
        data-level={finding.level}
        current={linked}
        // The anchor's row lands under the page's sticky head, not behind it.
        style={id ? { scrollMarginTop: "5rem" } : undefined}
        className={cn(
          // The list's own gutter rather than a panel row's, wherever it is
          // put: a plain panel, a section, a sheet's column.
          "-mx-3 flex-wrap items-start px-3 @xl/finding:flex-nowrap @xl/finding:items-center",
          arrived && "animate-rise",
        )}
        leading={
          <span className="flex h-5 items-center">
            <StatusDot tone={LEVEL_TONE[finding.level]} />
          </span>
        }
        title={heading}
        subtitle={
          showSummary ? (
            <span id={`${words}-summary`} className="font-normal">
              {summary}
            </span>
          ) : undefined
        }
      >
        {actions}
      </Row>
      {evidence && (
        <PopoverContent
          align="start"
          aria-label={finding.title}
          className="w-md space-y-2 text-body leading-relaxed text-muted-foreground"
        >
          <p className="font-medium wrap-anywhere text-foreground">{finding.title}</p>
          {showDetail && <p className="wrap-anywhere">{finding.detail}</p>}
          {finding.advice && <p className="text-foreground">{finding.advice}</p>}
          {finding.extra}
        </PopoverContent>
      )}
    </Popover>
  )
}

function FindingActionButton({
  action,
  onReveal,
  primary,
}: {
  action: FindingAction
  onReveal: () => void
  primary: boolean
}) {
  const [working, setWorking] = useState(false)
  if (action.href) {
    return (
      <Button size="sm" variant={primary ? "default" : "outline"} asChild>
        <Link href={action.href}>
          {action.label}
          <ArrowRight aria-hidden className="size-3" />
        </Link>
      </Button>
    )
  }
  const run = async () => {
    if (working || action.disabled || action.pending) return
    setWorking(true)
    try {
      if (action.reveal) onReveal()
      else await action.onClick?.()
    } catch (error) {
      notify.error("Could not complete the action", error)
    } finally {
      setWorking(false)
    }
  }
  return (
    <Button
      type="button"
      size="sm"
      variant={primary ? "default" : "outline"}
      pending={working || action.pending}
      disabled={working || action.disabled || action.pending}
      onClick={run}
    >
      {action.label}
      <ArrowRight aria-hidden className="size-3" />
    </Button>
  )
}
