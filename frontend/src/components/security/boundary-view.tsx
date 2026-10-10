"use client"

import { useEffect, useState } from "react"
import { get } from "@/lib/api"
import type {
  AccessBoundary,
  BoundaryAfterVerify,
  BoundaryImpact,
  BoundaryProposal,
} from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Row, RowList } from "@/components/row-list"
import { BOUNDARY_STATE, boundaryQuery, judgeable } from "@/components/security/boundary"
import { ErrorState, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Skeleton } from "@/components/ui/skeleton"

/**
 * What a proposed change does to the way the dashboard is reached, asked of
 * the server while the form is still being filled in. The answer arrives a
 * moment after typing stops, so a half-typed address is not judged.
 */
export function useBoundaryCheck(proposal: BoundaryProposal | undefined) {
  const key = proposal ? JSON.stringify(boundaryQuery(proposal)) : ""
  const [settled, setSettled] = useState(key)
  useEffect(() => {
    const timer = setTimeout(() => setSettled(key), 300)
    return () => clearTimeout(timer)
  }, [key])
  const complete = judgeable(proposal)
  const ready = complete && settled === key
  const check = usePoll<{ impacts: BoundaryImpact[] }>(
    (signal) =>
      get("/security/boundary/check", JSON.parse(settled) as Record<string, string>, signal),
    0,
    [settled],
    { enabled: ready && settled !== "" },
  )
  return {
    impacts: ready ? check.data?.impacts : undefined,
    // Until the answer for exactly this proposal is in, the form waits; a
    // check that failed leaves the decision to the server's own gate.
    checking: complete && !check.error && (!ready || check.loading || check.data === undefined),
  }
}

/**
 * The impacts, worst first: a cut is the server's refusal before it is made,
 * an effect is what confirming acknowledges.
 */
export function BoundaryImpacts({ impacts }: { impacts: BoundaryImpact[] | undefined }) {
  if (!impacts || impacts.length === 0) return null
  const cuts = impacts.filter((impact) => impact.level === "cuts")
  const affects = impacts.filter((impact) => impact.level === "affects")
  return (
    <div className="flex flex-col gap-2" aria-label="Access boundary">
      {cuts.length > 0 && (
        <Notice tone="danger" title="This would cut your way in">
          <ul className="list-disc pl-5">
            {cuts.map((impact) => (
              <li key={impact.text}>{impact.text}</li>
            ))}
          </ul>
        </Notice>
      )}
      {affects.length > 0 && (
        <Notice tone="warning" title="This crosses the dashboard's access boundary">
          <ul className="list-disc pl-5">
            {affects.map((impact) => (
              <li key={impact.text}>{impact.text}</li>
            ))}
          </ul>
          <p className="mt-1">Going ahead acknowledges it.</p>
        </Notice>
      )}
    </div>
  )
}

/**
 * The access boundary, each part with whether it holds: Caddy as the only
 * routable listener, the allowlist that runs before sign-in, the tailnet,
 * SSH for a tunnel, and previews that stay on the tailnet.
 */
export function BoundaryPanel() {
  const { data, error, loading, refresh } = usePoll<AccessBoundary>(
    (signal) => get("/security/boundary", undefined, signal),
    60_000,
  )
  return (
    <Panel plain>
      <PanelHeader
        title="Access boundary"
        actions={
          data ? (
            <span className="numeric text-hint text-muted-foreground">
              {data.checks.filter((c) => c.state === "held").length} of {data.checks.length} held
            </span>
          ) : undefined
        }
      />
      <PanelBody flush>
        {loading && !data ? (
          <Skeleton className="h-32 w-full" />
        ) : error && !data ? (
          <ErrorState error={error} onRetry={refresh} />
        ) : data ? (
          <RowList className="animate-rise">
            {data.checks.map((check) => (
              <Row
                key={check.id}
                title={check.title}
                subtitle={check.detail}
                className="py-2.5"
                trailing={
                  <Status
                    verdict={BOUNDARY_STATE[check.state].verdict}
                    label={BOUNDARY_STATE[check.state].label}
                  />
                }
              />
            ))}
          </RowList>
        ) : null}
      </PanelBody>
    </Panel>
  )
}

/**
 * The boundary after a pending change, beside how it stood before. A
 * boundary that held and no longer does is the one thing to read before
 * keeping the change.
 */
export function BoundaryChanges({ boundary }: { boundary: BoundaryAfterVerify }) {
  return (
    <div className="mt-2 space-y-1" aria-label="Access boundary after this change">
      {!boundary.before && (
        <p>
          The dashboard has no picture of the boundary from before this change, so only how it
          stands now is shown.
        </p>
      )}
      <ul className="space-y-1">
        {(boundary.before ? boundary.changes : boundary.checks.map(asChange)).map((change) => (
          <li key={change.id} className="flex flex-wrap items-baseline gap-x-2">
            <Status
              verdict={change.lost ? "critical" : BOUNDARY_STATE[change.after].verdict}
              label={change.lost ? "lost" : BOUNDARY_STATE[change.after].label}
            />
            <span className="font-medium">{change.title}</span>
            {(change.lost || change.after !== "held") && (
              <span className="text-muted-foreground">{change.detail}</span>
            )}
          </li>
        ))}
      </ul>
    </div>
  )
}

function asChange(check: BoundaryAfterVerify["checks"][number]) {
  return {
    id: check.id,
    title: check.title,
    before: check.state,
    after: check.state,
    detail: check.detail,
    lost: false,
  }
}
