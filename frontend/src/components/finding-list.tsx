"use client"

import { useId, useState } from "react"
import Link from "next/link"
import { ArrowRight, CheckCircle, Cross } from "@/components/icons"
import {
  Accordion,
  AccordionContent,
  AccordionItem,
  AccordionTrigger,
} from "@/components/ui/accordion"
import { Button } from "@/components/ui/button"
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

/** Evidence folds away; the problem, its consequence and the next action never do. */
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
  anchor?: (id: string) => string
  defaultOpen?: string[]
  /** An attention list gives its first remedy the command face; diagnostic reports stay secondary. */
  emphasize?: boolean
}) {
  const arrivals = useArrivals(findings.map((finding) => finding.id))
  const descriptions = useId()
  const primaryId = emphasize ? findings.find((finding) => finding.action)?.id : undefined
  const [opened, setOpened] = useState<string[] | undefined>(undefined)
  const openKeys = opened ?? defaultOpen ?? []
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
    <Accordion
      type="multiple"
      value={openKeys}
      onValueChange={setOpened}
      className="@container/finding min-w-0"
    >
      {findings.map((finding, index) => {
        const summary = finding.summary ?? finding.detail
        const showSummary =
          summary !== finding.title &&
          (!openKeys.includes(finding.id) || summary !== finding.detail)
        const showDetail = finding.detail !== finding.title
        const summaryId = `${descriptions}-${index}-summary`
        return (
          <AccordionItem
            key={finding.id}
            value={finding.id}
            id={anchor?.(finding.id)}
            data-finding={finding.id}
            data-level={finding.level}
            className={cn(
              "group/finding -mx-3 scroll-mt-20 border-hairline px-3 transition-colors focus-within:bg-row-hover hover:bg-row-hover [&>h3]:flex-wrap [&>h3]:gap-x-3",
              arrivals.has(finding.id) && "animate-rise",
            )}
          >
            <AccordionTrigger
              aria-label={finding.title}
              aria-describedby={`${descriptions}-${index}-severity${showSummary || (openKeys.includes(finding.id) && showDetail) ? ` ${summaryId}` : ""}`}
              className="min-w-0 basis-full items-start gap-3 py-3 text-body hover:no-underline @xl/finding:basis-0"
              actions={
                (finding.action || onDismiss) && (
                  <span className="mb-3 ml-[1.375rem] flex min-w-0 flex-wrap items-center gap-2 @xl/finding:my-0 @xl/finding:ml-0 @xl/finding:shrink-0">
                    {finding.action && (
                      <FindingActionButton
                        action={finding.action}
                        primary={finding.id === primaryId}
                        onReveal={() =>
                          setOpened((held) => {
                            const keys = held ?? defaultOpen ?? []
                            return keys.includes(finding.id) ? keys : [...keys, finding.id]
                          })
                        }
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
              }
            >
              <span className="flex min-w-0 flex-1 items-start gap-2.5">
                <span className="mt-1.5 inline-flex shrink-0">
                  <StatusDot tone={LEVEL_TONE[finding.level]} />
                </span>
                <span className="min-w-0 space-y-1">
                  <span id={`${descriptions}-${index}-severity`} className="sr-only">
                    {finding.level === "critical"
                      ? "Critical issue"
                      : finding.level === "warning"
                        ? "Needs attention"
                        : "Observation"}
                  </span>
                  <span className="block font-medium wrap-anywhere">{finding.title}</span>
                  {showSummary && (
                    <span
                      id={summaryId}
                      className="line-clamp-2 block text-body leading-relaxed font-normal text-muted-foreground"
                    >
                      {summary}
                    </span>
                  )}
                  {finding.meta && (
                    <span className="flex min-w-0 flex-wrap items-center gap-1 text-hint font-normal text-muted-foreground/80">
                      {finding.meta}
                    </span>
                  )}
                </span>
              </span>
            </AccordionTrigger>
            <AccordionContent className="space-y-2 pl-[1.375rem] text-body leading-relaxed text-muted-foreground">
              {showDetail && (
                <p id={!showSummary ? summaryId : undefined} className="wrap-anywhere">
                  {finding.detail}
                </p>
              )}
              {finding.advice && <p className="text-foreground/80">{finding.advice}</p>}
              {finding.extra}
            </AccordionContent>
          </AccordionItem>
        )
      })}
    </Accordion>
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
