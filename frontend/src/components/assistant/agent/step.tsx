"use client"

import { useState } from "react"
import Link from "next/link"
import { ArrowRight, Check, ChevronRight, Cross, Warning } from "@/components/icons"
import { cn } from "@/lib/utils"
import { Well } from "@/components/panel"
import { ProductGlyph } from "@/components/product-logo"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { stepMark } from "@/components/assistant/agent/marks"
import { arriveAt } from "@/components/assistant/agent/arrival"
import {
  inputLines,
  type AgentChange,
  type AgentLink,
  type AgentStep,
} from "@/components/assistant/agent/model"

/**
 * One thing the assistant did, or is doing. AI Elements' `tool` and
 * `chain-of-thought`, from its registry (vercel/ai-elements, Apache-2.0), in this
 * product's marks: the product it touched on a tile, and a line of what it came to
 * that lights while it runs. Unfolding one shows what it was asked and what it read.
 */
export function StepRow({ step }: { step: AgentStep }) {
  const [open, setOpen] = useState(false)
  const mark = stepMark(step.call)
  const Glyph = mark.icon
  const details = inputLines(step.call.input).lines.length > 0 || Boolean(step.output)
  const running = step.state === "running"
  return (
    <Collapsible open={open} onOpenChange={setOpen} asChild>
      <li
        data-slot="agent-step"
        data-state={step.state}
        data-kind={step.call.kind}
        className="animate-rise"
      >
        <CollapsibleTrigger
          disabled={!details}
          className={cn(
            "group flex min-h-9 w-full min-w-0 items-center gap-2.5 rounded-lg px-2 py-1.5 text-left focus-ring",
            details && "transition-colors hover:bg-row-hover",
          )}
        >
          <span
            aria-hidden
            className={cn(
              "relative grid size-6 shrink-0 place-items-center rounded-md border border-hairline bg-surface-header",
              running && "border-signal/50",
            )}
          >
            {mark.product ? (
              <ProductGlyph id={mark.product} className="size-3.5" />
            ) : (
              <Glyph className="size-3.5 text-muted-foreground" />
            )}
            <StepBadge state={step.state} />
          </span>
          <span className="min-w-0 flex-1 text-body">
            {running ? (
              <TextShimmer className="max-w-full truncate align-bottom">
                {step.call.label}
              </TextShimmer>
            ) : (
              <span
                className={cn(step.state === "declined" && "text-muted-foreground line-through")}
              >
                {step.call.label}
              </span>
            )}
            {step.said && step.state !== "running" && (
              <span
                className={cn(
                  "ml-2 text-hint",
                  step.state === "failed" ? "text-destructive" : "text-muted-foreground",
                )}
              >
                {step.said}
              </span>
            )}
            {step.state === "declined" && (
              <span className="ml-2 text-hint text-muted-foreground no-underline">Declined</span>
            )}
          </span>
          {details && (
            <ChevronRight
              aria-hidden
              className="size-3.5 shrink-0 text-muted-foreground transition-transform group-data-[state=open]:rotate-90"
            />
          )}
        </CollapsibleTrigger>
        {details && (
          <CollapsibleContent className="space-y-2 px-2 pt-1 pb-2 sm:pl-10.5">
            <StepDetails step={step} />
          </CollapsibleContent>
        )}
        {step.change && <ChangeCard change={step.change} reason={step.call.reason} />}
      </li>
    </Collapsible>
  )
}

function StepBadge({ state }: { state: AgentStep["state"] }) {
  if (state === "done")
    return (
      <span className="absolute -right-1 -bottom-1 grid size-3 place-items-center rounded-full bg-success text-success-foreground">
        <Check className="size-2" />
      </span>
    )
  if (state === "failed")
    return (
      <span className="absolute -right-1 -bottom-1 grid size-3 place-items-center rounded-full bg-destructive text-white">
        <Cross className="size-2" />
      </span>
    )
  if (state === "running" || state === "waiting")
    return (
      <span
        className={cn(
          "absolute -right-0.5 -bottom-0.5 size-2 animate-breathe rounded-full",
          state === "running" ? "bg-signal" : "bg-warning",
        )}
      />
    )
  return null
}

function StepDetails({ step }: { step: AgentStep }) {
  return (
    <>
      {step.call.operation && (
        <p className="truncate font-mono text-hint text-muted-foreground">{step.call.operation}</p>
      )}
      {step.call.reason && !step.change && (
        <p className="text-hint leading-relaxed text-muted-foreground">{step.call.reason}</p>
      )}
      <InputLines input={step.call.input} flush />
      {step.output && (
        <Well className="max-h-56 p-2.5 text-hint whitespace-pre-wrap" aria-label="What it read">
          {step.output}
        </Well>
      )}
    </>
  )
}

/** What a change did, the shortcut straight to it, and what is different now. */
function ChangeCard({ change, reason }: { change: AgentChange; reason?: string }) {
  return (
    <div
      data-slot="agent-change"
      className="mx-1 mt-1 mb-1.5 animate-rise space-y-1.5 rounded-lg border border-rule-success bg-wash-success px-2.5 py-2 sm:mr-0 sm:ml-10.5"
    >
      <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1.5">
        <Check aria-hidden className="size-3.5 shrink-0 text-success" />
        <span className="min-w-0 flex-1 basis-40 text-hint leading-snug">
          <span className="font-medium text-success">Changed</span>
          {reason && <span className="text-muted-foreground"> · {reason}</span>}
        </span>
        <Shortcut label={change.label} href={change.href} />
      </div>
      {change.diff && change.diff.length > 0 && (
        <ul className="space-y-0.5 pl-5.5 font-mono text-hint">
          {change.diff.map((line) => (
            <li key={line.path} className="flex min-w-0 flex-wrap items-baseline gap-x-2">
              <span className="text-muted-foreground">{line.path}</span>
              {line.from && (
                <span className="break-all text-destructive line-through">{line.from}</span>
              )}
              {line.to && <span className="break-all text-success">{line.to}</span>}
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}

/** A press straight to the page, the back way being the dock's. */
export function Shortcut({ label, href }: { label: string; href: string }) {
  return (
    <Button asChild size="xs" variant="outline" data-slot="agent-shortcut">
      <Link href={href} onClick={() => arriveAt(href)}>
        Open {label}
        <ArrowRight />
      </Link>
    </Button>
  )
}

/** A shortcut the assistant offers, for what only the person can do. */
export function LinkCard({ link }: { link: AgentLink }) {
  return (
    <div
      data-slot="agent-link"
      className="flex min-w-0 animate-rise flex-wrap items-center gap-x-3 gap-y-1.5 rounded-lg border border-hairline bg-card p-2.5"
    >
      <p className="min-w-0 flex-1 basis-48 text-body text-muted-foreground">{link.note}</p>
      <Shortcut label={link.label} href={link.href} />
    </div>
  )
}

/** What a call would send, a line a value, so it is read rather than parsed. */
function InputLines({ input, flush }: { input: unknown; flush?: boolean }) {
  const { lines, more } = inputLines(input)
  if (lines.length === 0) return null
  return (
    <ul
      data-slot="agent-input"
      className={cn("space-y-0.5 font-mono text-hint", !flush && "pl-8.5")}
    >
      {lines.map((line) => (
        <li key={line.path} className="flex min-w-0 flex-wrap items-baseline gap-x-2">
          <span className="text-muted-foreground">{line.path}</span>
          <span className="min-w-0 break-all">{line.value}</span>
        </li>
      ))}
      {more > 0 && <li className="text-muted-foreground">and {more} more</li>}
    </ul>
  )
}

const KIND_TAG: Record<
  AgentStep["call"]["kind"],
  { label: string; tone: "default" | "warning" | "danger" }
> = {
  free: { label: "Reads nothing", tone: "default" },
  read: { label: "Reads", tone: "default" },
  change: { label: "Changes", tone: "warning" },
  destructive: { label: "Stops or replaces", tone: "danger" },
}

/**
 * What it wants to do, waiting on the person's answer: AI Elements' `confirmation`.
 * Nothing it names has run. Each has its own Allow, so a person can take the read
 * and leave the restart; the press runs it through the route the project's own
 * button calls, as them.
 */
export function ApprovalCard({
  steps,
  active,
  paused,
  onDecide,
}: {
  steps: AgentStep[]
  active: boolean
  paused?: boolean
  onDecide: (id: string | "all", allow: boolean) => void
}) {
  const open = steps.filter((step) => step.decision === undefined)
  const destructive = steps.some((step) => step.call.kind === "destructive")
  return (
    <section
      data-slot="agent-approval"
      aria-label={
        steps.length === 1 ? "A call waiting for your answer" : "Calls waiting for your answer"
      }
      className={cn(
        "animate-rise space-y-2.5 rounded-xl border p-3",
        destructive ? "border-rule-danger bg-wash-danger" : "border-rule-warning bg-wash-warning",
      )}
    >
      <p className="flex items-center gap-2 text-body font-medium">
        <Warning
          aria-hidden
          className={cn("size-4", destructive ? "text-destructive" : "text-warning")}
        />
        {paused
          ? "It has taken many steps since you last asked. Carry on?"
          : steps.length === 1
            ? "It wants to do this"
            : `It wants to do ${steps.length} things`}
      </p>
      <ul className="divide-y divide-hairline">
        {steps.map((step) => {
          const tag = KIND_TAG[step.call.kind]
          const mark = stepMark(step.call)
          const Glyph = mark.icon
          return (
            <li key={step.call.id} className="space-y-1.5 py-2 first:pt-0 last:pb-0">
              <div className="flex min-w-0 flex-wrap items-center gap-x-2.5 gap-y-1.5">
                <span
                  aria-hidden
                  className="grid size-6 shrink-0 place-items-center rounded-md border border-hairline bg-card"
                >
                  {mark.product ? (
                    <ProductGlyph id={mark.product} className="size-3.5" />
                  ) : (
                    <Glyph className="size-3.5 text-muted-foreground" />
                  )}
                </span>
                <span className="min-w-0 flex-1 text-body font-medium">{step.call.label}</span>
                <Tag tone={tag.tone}>{tag.label}</Tag>
                {step.decision !== undefined ? (
                  <span className="text-hint font-medium text-muted-foreground">
                    {step.decision ? "Allowed" : "Declined"}
                  </span>
                ) : (
                  active && (
                    <span className="flex shrink-0 gap-1.5">
                      <Button
                        size="xs"
                        variant={step.call.kind === "destructive" ? "destructive" : "default"}
                        aria-label={`Allow: ${step.call.label}`}
                        onClick={() => onDecide(step.call.id, true)}
                      >
                        Allow
                      </Button>
                      <Button
                        size="xs"
                        variant="outline"
                        aria-label={`Decline: ${step.call.label}`}
                        onClick={() => onDecide(step.call.id, false)}
                      >
                        Decline
                      </Button>
                    </span>
                  )
                )}
              </div>
              {step.call.reason && (
                <p className="pl-8.5 text-body leading-relaxed text-muted-foreground">
                  {step.call.reason}
                </p>
              )}
              <InputLines input={step.call.input} />
            </li>
          )
        })}
      </ul>
      {active && open.length > 1 && (
        <div className="flex flex-wrap gap-2 border-t border-hairline pt-2.5">
          <Button size="sm" onClick={() => onDecide("all", true)}>
            Allow all
          </Button>
          <Button size="sm" variant="outline" onClick={() => onDecide("all", false)}>
            Decline all
          </Button>
        </div>
      )}
    </section>
  )
}
