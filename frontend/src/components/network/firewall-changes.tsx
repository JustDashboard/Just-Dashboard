"use client"

import { useState } from "react"
import { get, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import { relativeTime, timestamp } from "@/lib/format"
import type { FirewallPlanReview, FirewallRule, FirewallRuleHistory } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { SidePanel } from "@/components/side-panel"
import { EmptyNote, ErrorState, LoadingRows, Notice } from "@/components/state"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Trash, Warning } from "@/components/icons"
import { accessLosses, planOperations, type StagedChange } from "./firewall-reading"

const OUTCOME_TONE = {
  applied: "success",
  refused: "warning",
  failed: "danger",
  pending: "default",
  compensated: "warning",
  skipped: "default",
} as const

function HistoryList({ history }: { history: FirewallRuleHistory }) {
  return (
    <div className="space-y-3">
      {history.events.length === 0 ? (
        <EmptyNote>No change to this from the dashboard is recorded.</EmptyNote>
      ) : (
        <ol className="divide-y divide-hairline rounded-lg border border-hairline">
          {history.events.map((event) => (
            <li key={event.id} className="space-y-1 px-3 py-2.5">
              <span className="flex flex-wrap items-center gap-2 text-xs">
                <Tag tone={OUTCOME_TONE[event.outcome] ?? "default"}>{event.outcome}</Tag>
                <span className="font-medium">{event.operation}</span>
                <span className="text-muted-foreground">
                  by {event.actor || "an API caller"} ·{" "}
                  <time dateTime={event.at} title={timestamp(event.at)}>
                    {relativeTime(event.at)}
                  </time>
                </span>
                {event.changeId && <Tag mono>pending {event.changeId.slice(0, 8)}</Tag>}
              </span>
              {event.detail && (
                <p className="text-hint break-words text-muted-foreground">{event.detail}</p>
              )}
              {event.rule && (
                <code className="block font-mono text-hint break-all">
                  {JSON.stringify(event.rule)}
                </code>
              )}
            </li>
          ))}
        </ol>
      )}
      <ul className="space-y-1 text-hint text-muted-foreground">
        {history.limits.map((limit) => (
          <li key={limit}>{limit}</li>
        ))}
      </ul>
    </div>
  )
}

/** One rule's recorded changes, followed back through its edits. */
export function RuleHistorySheet({
  rule,
  onOpenChange,
}: {
  rule: FirewallRule
  onOpenChange: (open: boolean) => void
}) {
  const history = usePoll<FirewallRuleHistory>(
    (signal) => get("/firewall/history", { rule: rule.id }, signal),
    0,
    [rule.id],
  )
  return (
    <SidePanel
      open
      onOpenChange={onOpenChange}
      title={`History of rule ${rule.number ?? ""}`.trim()}
      description="Changes to this rule made from the dashboard"
      width="md"
      initialFocus="body"
    >
      <p className="mb-3 font-mono text-xs break-all text-muted-foreground">{rule.raw}</p>
      {history.error ? (
        <ErrorState error={history.error} onRetry={history.refresh} />
      ) : !history.data ? (
        <LoadingRows rows={3} />
      ) : (
        <HistoryList history={history.data} />
      )}
    </SidePanel>
  )
}

/** The firewall's recent changes from the dashboard, refused ones included. */
export function FirewallHistoryPanel() {
  const history = usePoll<FirewallRuleHistory>(
    (signal) => get("/firewall/history", { limit: 20 }, signal),
    30_000,
  )
  return (
    <Panel plain>
      <PanelHeader title="Recent firewall changes" />
      <PanelBody>
        {history.error && !history.data ? (
          <ErrorState error={history.error} onRetry={history.refresh} />
        ) : !history.data ? (
          <LoadingRows rows={3} />
        ) : (
          <HistoryList history={history.data} />
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
