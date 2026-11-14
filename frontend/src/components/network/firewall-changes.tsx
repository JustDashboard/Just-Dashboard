"use client"

import { useRef, useState } from "react"
import { get, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import { relativeTime, timestamp } from "@/lib/format"
import type {
  FirewallPlanReview,
  FirewallRule,
  FirewallRuleEvent,
  FirewallRuleHistory,
} from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { InfoTip } from "@/components/form"
import { IconAction } from "@/components/icon-action"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyNote, ErrorState, LoadingRows, Notice } from "@/components/state"
import { Status, type DotTone } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Popover, PopoverAnchor, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import { ClockRewind, Trash, Warning } from "@/components/icons"
import { accessLosses, isAnywhere, planOperations, type StagedChange } from "./firewall-reading"

const OUTCOME_TONE: Record<FirewallRuleEvent["outcome"], DotTone> = {
  applied: "running",
  refused: "warning",
  failed: "danger",
  pending: "notice",
  compensated: "warning",
  skipped: "stopped",
}

/** A recorded rule as the few words a line has room for: "allow 51820/udp from 10.0.0.0/8". */
export function ruleWords(rule: Record<string, unknown> | undefined) {
  if (!rule) return ""
  const word = (key: string) => (typeof rule[key] === "string" ? (rule[key] as string) : "")
  const port = word("port")
  const from = word("from")
  return [
    word("action"),
    port ? `${port}${word("protocol") ? `/${word("protocol")}` : ""}` : word("to"),
    from && !isAnywhere(from) ? `from ${from}` : "",
  ]
    .filter(Boolean)
    .join(" ")
}

/** What a change asked for, field by field, as the server recorded it. */
function Request({ label, rule }: { label: string; rule: Record<string, unknown> }) {
  return (
    <section aria-label={label} className="space-y-1.5">
      <p className="text-xs font-medium">{label}</p>
      <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-1 text-hint">
        {Object.entries(rule).map(([key, value]) => (
          <div key={key} className="contents">
            <dt className="text-muted-foreground">{key}</dt>
            <dd className="font-mono break-all">
              {typeof value === "string" ? value : JSON.stringify(value)}
            </dd>
          </div>
        ))}
      </dl>
    </section>
  )
}

/** Who made a change and when, as one run of words. */
function ByWhom({ event }: { event: FirewallRuleEvent }) {
  return (
    <>
      by {event.actor || "an API caller"} ·{" "}
      <time dateTime={event.at} title={timestamp(event.at)}>
        {relativeTime(event.at)}
      </time>
    </>
  )
}

/** A change's request and what came of it: what the line's popover and a rule's history both hold. */
function ChangeDetail({ event }: { event: FirewallRuleEvent }) {
  return (
    <>
      {event.detail && (
        <p className="text-hint break-words text-muted-foreground">{event.detail}</p>
      )}
      {event.rule && <Request label="Asked for" rule={event.rule} />}
      {event.previous && <Request label="In place of" rule={event.previous} />}
      {!event.detail && !event.rule && !event.previous && (
        <p className="text-hint text-muted-foreground">Nothing more was recorded.</p>
      )}
    </>
  )
}

/**
 * One recorded change as one line, the same height whatever it carries: what
 * came of it as a dot and a word, the operation and the rule in a few words,
 * who and when. What it asked for and what the server answered is a glance,
 * so it opens from the line's end as a popover rather than hanging under it
 * as the raw JSON it was.
 */
function ChangeLine({ event }: { event: FirewallRuleEvent }) {
  const words = ruleWords(event.rule)
  return (
    <li data-slot="firewall-change" className="flex h-11 min-w-0 items-center gap-3">
      <Status
        tone={OUTCOME_TONE[event.outcome] ?? "unknown"}
        label={event.outcome}
        className="w-24 shrink-0"
      />
      <p className="min-w-0 flex-1 truncate text-body">
        <span className="font-medium">{event.operation}</span>
        {words && <span className="ml-2 font-mono text-hint text-muted-foreground">{words}</span>}
      </p>
      {event.changeId && (
        <Tag mono className="max-sm:hidden">
          pending {event.changeId.slice(0, 8)}
        </Tag>
      )}
      <p className="shrink-0 text-hint text-muted-foreground max-sm:hidden">
        <ByWhom event={event} />
      </p>
      <Popover>
        <PopoverTrigger asChild>
          <Button
            size="xs"
            variant="ghost"
            className="shrink-0 text-muted-foreground"
            aria-label={`What ${event.operation} asked for, ${relativeTime(event.at)}`}
          >
            Request
          </Button>
        </PopoverTrigger>
        <PopoverContent align="end" className="w-80 space-y-3">
          <p className="flex flex-wrap items-center gap-x-2 gap-y-1 text-xs">
            <Status tone={OUTCOME_TONE[event.outcome] ?? "unknown"} label={event.outcome} />
            <span className="font-medium">{event.operation}</span>
            <span className="text-muted-foreground">
              <ByWhom event={event} />
            </span>
          </p>
          {event.changeId && <Tag mono>pending {event.changeId.slice(0, 8)}</Tag>}
          <ChangeDetail event={event} />
        </PopoverContent>
      </Popover>
    </li>
  )
}

/**
 * One rule's recorded changes, followed back through its edits, opened from
 * the rule's own row. It was a sheet over the page; a rule's history is a
 * handful of changes read at a glance, so it is a popover on the button that
 * asks for it, and the rules stay in view behind it.
 */
export function RuleHistoryPopover({ rule }: { rule: FirewallRule }) {
  const [open, setOpen] = useState(false)
  const anchor = useRef<HTMLSpanElement>(null)
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverAnchor asChild>
        <span ref={anchor} className="inline-flex">
          <IconAction label="Rule history" onClick={() => setOpen((held) => !held)}>
            <ClockRewind />
          </IconAction>
        </span>
      </PopoverAnchor>
      <PopoverContent
        align="end"
        className="w-96 space-y-3"
        aria-label={`History of rule ${rule.number ?? ""}`.trim()}
        // The button is outside the popover: its own press closes it, and
        // must not be read as a press outside that closes it first.
        onInteractOutside={(event) => {
          if (anchor.current?.contains(event.target as Node)) event.preventDefault()
        }}
      >
        {open && <RuleHistory rule={rule} />}
      </PopoverContent>
    </Popover>
  )
}

function RuleHistory({ rule }: { rule: FirewallRule }) {
  const history = usePoll<FirewallRuleHistory>(
    (signal) => get("/firewall/history", { rule: rule.id }, signal),
    0,
    [rule.id],
  )
  return (
    <>
      <div className="space-y-1">
        <p className="text-sm font-medium">{`History of rule ${rule.number ?? ""}`.trim()}</p>
        <p className="font-mono text-hint break-all text-muted-foreground">{rule.raw}</p>
      </div>
      {history.error ? (
        <ErrorState error={history.error} onRetry={history.refresh} />
      ) : !history.data ? (
        <LoadingRows rows={3} />
      ) : history.data.events.length === 0 ? (
        <EmptyNote>No change to this from the dashboard is recorded.</EmptyNote>
      ) : (
        <ol className="max-h-80 space-y-3 overflow-y-auto">
          {history.data.events.map((event) => (
            <li key={event.id} className="space-y-1.5">
              <p className="flex flex-wrap items-center gap-x-2 gap-y-1 text-xs">
                <Status tone={OUTCOME_TONE[event.outcome] ?? "unknown"} label={event.outcome} />
                <span className="font-medium">{event.operation}</span>
                <span className="text-muted-foreground">
                  <ByWhom event={event} />
                </span>
                {event.changeId && <Tag mono>pending {event.changeId.slice(0, 8)}</Tag>}
              </p>
              <ChangeDetail event={event} />
            </li>
          ))}
        </ol>
      )}
      {history.data && history.data.limits.length > 0 && (
        <ul className="space-y-1 border-t border-hairline pt-3 text-hint text-muted-foreground">
          {history.data.limits.map((limit) => (
            <li key={limit}>{limit}</li>
          ))}
        </ul>
      )}
    </>
  )
}

/**
 * The firewall's recent changes from the dashboard, refused ones included,
 * a line each. What the record cannot see was a sentence under the list; it
 * is the ⓘ on the title now.
 */
export function FirewallHistoryPanel() {
  const history = usePoll<FirewallRuleHistory>(
    (signal) => get("/firewall/history", { limit: 20 }, signal),
    30_000,
  )
  const limits = history.data?.limits ?? []
  return (
    <Panel plain>
      <PanelHeader
        title={
          <span className="inline-flex items-center gap-1.5">
            Recent firewall changes
            {limits.length > 0 && (
              <InfoTip label="What the record cannot see">{limits.join(" ")}</InfoTip>
            )}
          </span>
        }
        actions={
          history.data && (
            <span className="numeric text-hint text-muted-foreground">
              {history.data.events.length}
            </span>
          )
        }
      />
      <PanelBody flush={Boolean(history.data?.events.length)}>
        {history.error && !history.data ? (
          <ErrorState error={history.error} onRetry={history.refresh} />
        ) : !history.data ? (
          <LoadingRows rows={3} />
        ) : history.data.events.length === 0 ? (
          <EmptyNote>No change from the dashboard is recorded.</EmptyNote>
        ) : (
          <ol className="divide-y divide-hairline">
            {history.data.events.map((event) => (
              <ChangeLine key={event.id} event={event} />
            ))}
          </ol>
        )}
      </PanelBody>
    </Panel>
  )
}

/**
 * Several rule changes reviewed and applied as one. The server orders them
 * — additions, then replacements, then removals — names every rule by its
 * identity, judges the result against the required ways in, and takes back
 * what it already did if a later step fails.
 */
export function FirewallPlanTray({
  staged,
  onUnstage,
  onClear,
  onApplied,
}: {
  staged: StagedChange[]
  onUnstage: (index: number) => void
  onClear: () => void
  onApplied: () => void
}) {
  const [review, setReview] = useState<{ key: string; result: FirewallPlanReview }>()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()
  const body = { operations: planOperations(staged) }
  const key = JSON.stringify(body)
  const current = review?.key === key ? review.result : undefined
  if (staged.length === 0) return null
  const preview = async () => {
    setBusy(true)
    setError(undefined)
    try {
      setReview({ key, result: await post<FirewallPlanReview>("/firewall/plans/preview", body) })
    } catch (err) {
      setReview(undefined)
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }
  const apply = async () => {
    setBusy(true)
    setError(undefined)
    try {
      await post<FirewallPlanReview>("/firewall/plans", body)
      notify.success("Plan applied")
      setReview(undefined)
      onClear()
      onApplied()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
      onApplied()
    } finally {
      setBusy(false)
    }
  }
  const losses = current ? accessLosses(current.checks) : []
  return (
    <Panel plain>
      <PanelHeader
        title="Plan"
        actions={
          <span className="flex items-center gap-2">
            <Button size="sm" variant="ghost" onClick={onClear} disabled={busy}>
              Discard
            </Button>
            <Button
              size="sm"
              variant="outline"
              onClick={preview}
              disabled={busy}
              pending={busy && !current}
            >
              Review plan
            </Button>
            <Button
              size="sm"
              onClick={apply}
              disabled={busy || !current || Boolean(current.refusal)}
              pending={busy && Boolean(current)}
            >
              Apply plan
            </Button>
          </span>
        }
      />
      <PanelBody className="space-y-3">
        <ol
          className="divide-y divide-hairline rounded-lg border border-hairline"
          aria-label="Staged changes"
        >
          {staged.map((change, index) => (
            <li key={`${change.op}:${index}`} className="flex min-w-0 items-center gap-3 px-3 py-2">
              <Tag tone={change.op === "delete" ? "danger" : "success"}>
                {change.op === "delete" ? "remove" : "add"}
              </Tag>
              <span className="min-w-0 flex-1 font-mono text-xs break-all">{change.label}</span>
              <Button
                size="icon-xs"
                variant="ghost"
                aria-label={`Unstage ${change.label}`}
                onClick={() => onUnstage(index)}
              >
                <Trash aria-hidden />
              </Button>
            </li>
          ))}
        </ol>
        {!current && (
          <p className="text-hint text-muted-foreground">
            Review the plan to see the order the server runs it in and which ways in it keeps.
          </p>
        )}
        {current && (
          <div className="space-y-2" data-testid="firewall-plan-review">
            <ol className="list-inside list-decimal space-y-1 text-xs">
              {current.steps.map((step, index) => (
                <li key={`${step.op}:${index}`}>{step.description}</li>
              ))}
            </ol>
            {current.refusal ? (
              <Notice tone="danger" icon={Warning} title="The server will refuse this plan">
                {current.refusal}
              </Notice>
            ) : (
              <p className="text-hint text-muted-foreground">
                Every required way in stays admitted
                {losses.length === 0 && current.findings.length > 0
                  ? `; the result has ${current.findings.length} ordering finding${current.findings.length === 1 ? "" : "s"}.`
                  : "."}
              </p>
            )}
            {current.findings.map((finding) => (
              <p key={finding.ruleId} className="text-hint text-warning">
                {finding.reason}
              </p>
            ))}
          </div>
        )}
        {error && (
          <p role="alert" className="text-body text-destructive">
            {error}
          </p>
        )}
      </PanelBody>
    </Panel>
  )
}
