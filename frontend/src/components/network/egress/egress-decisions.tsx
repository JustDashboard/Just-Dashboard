"use client"

import { cn } from "@/lib/utils"
import { egressEventReading, type EgressEvent, type EgressGroup } from "@/lib/network-egress"
import { Disclosure } from "@/components/form"
import { TONE_TEXT } from "@/components/network/egress/marks"

/**
 * The Decisions view: the group's record, newest first, each decision in the
 * words the monitor wrote it and the evidence it was made on behind a quiet
 * disclosure — the routes before and after, every member's measurement, the
 * connections moved, pinned and flushed, and the journal entry.
 */
export function EgressDecisions({ group }: { group: EgressGroup }) {
  if (group.events.length === 0)
    return <p className="text-hint text-muted-foreground">Nothing has been decided yet.</p>
  return (
    <ol
      className="flex flex-col divide-y divide-hairline"
      aria-label={`Decisions of ${group.name}`}
    >
      {group.events.map((event) => (
        <EventRow key={event.id} group={group} event={event} />
      ))}
    </ol>
  )
}

function EventRow({ group, event }: { group: EgressGroup; event: EgressEvent }) {
  const reading = egressEventReading(event)
  const ev = event.evidence
  const hasEvidence = Boolean(
    ev && (ev.members?.length || ev.routeBefore || ev.routeAfter || ev.connections),
  )
  return (
    <li className="flex min-w-0 flex-col gap-1 py-2.5">
      <div className="flex min-w-0 flex-wrap items-baseline gap-x-3 gap-y-1">
        <time className="numeric text-hint text-muted-foreground" dateTime={event.at}>
          {new Date(event.at).toLocaleString()}
        </time>
        <span className={cn("text-body font-medium", TONE_TEXT[reading.tone])}>
          {reading.label}
        </span>
        {event.actor && <span className="text-hint text-muted-foreground">{event.actor}</span>}
      </div>
      <p className="text-body">{event.reason}</p>
      {hasEvidence && (
        <Disclosure quiet summary="Evidence">
          <dl className="grid gap-x-4 gap-y-1 text-hint sm:grid-cols-[max-content_1fr]">
            {ev?.routeBefore && (
              <>
                <dt className="text-muted-foreground">Before</dt>
                <dd className="font-mono">{ev.routeBefore}</dd>
              </>
            )}
            {ev?.routeAfter && (
              <>
                <dt className="text-muted-foreground">After</dt>
                <dd className="font-mono">{ev.routeAfter}</dd>
              </>
            )}
            {ev?.members?.map((m) => (
              <MemberEvidence key={m.id} group={group} evidence={m} />
            ))}
            {ev?.connections && (
              <>
                <dt className="text-muted-foreground">Connections</dt>
                <dd>
                  {ev.connections.error
                    ? `Not read: ${ev.connections.error}`
                    : `${ev.connections.read} tracked; ${ev.connections.moved} moved, ${ev.connections.pinned} kept pinned, ${ev.connections.flushed} flushed${ev.connections.spared ? `, ${ev.connections.spared} spared as operator connections` : ""}.`}{" "}
                  <span className="text-muted-foreground">{ev.connections.basis}</span>
                </dd>
              </>
            )}
            {ev?.change && (
              <>
                <dt className="text-muted-foreground">Journal</dt>
                <dd className="font-mono">{ev.change}</dd>
              </>
            )}
          </dl>
        </Disclosure>
      )}
    </li>
  )
}

function MemberEvidence({
  group,
  evidence,
}: {
  group: EgressGroup
  evidence: NonNullable<NonNullable<EgressEvent["evidence"]>["members"]>[number]
}) {
  const name = group.members.find((m) => m.id === evidence.id)?.name ?? `member ${evidence.id}`
  return (
    <>
      <dt className="text-muted-foreground">{name}</dt>
      <dd>
        {evidence.state}
        {evidence.total > 0 && ` · ${evidence.ok} of ${evidence.total} probes answered`}
        {evidence.latencyMillis ? ` · ${evidence.latencyMillis.toFixed(1)} ms` : ""}
        {` · loss ${Math.round(evidence.loss)}%`}
        {evidence.why ? ` · ${evidence.why}` : ""}
      </dd>
    </>
  )
}
