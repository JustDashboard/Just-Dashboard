"use client"

import { cn } from "@/lib/utils"
import {
  egressDuration,
  egressHop,
  egressMemberReading,
  type EgressGroup,
  type EgressMemberView,
  type EgressPoint,
} from "@/lib/network-egress"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Status } from "@/components/status-dot"
import { Notice } from "@/components/state"
import { Tag } from "@/components/tag"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { DOT } from "@/components/network/egress/marks"
import { egressAdvice } from "@/components/network/egress/reading"

/** The strip holds at most this many samples, however long the loss window. */
const STRIP = 30

/**
 * The Members view: every member measured through its own mark, source and
 * device, read down its columns — the state, the round trip, the loss and
 * the last samples — one line a member. What the rules would do now,
 * readiness, what each member is bound to and how the group decides follow.
 *
 * The table had a sixth column, the mark, table and probe source, under a
 * second line of path and tier in the first: two lines a row, and cut off at
 * Round trip on a phone. Those are a member's fixed identity, not a reading
 * compared down the page, so they are the list under the table.
 */
export function EgressMembers({ group }: { group: EgressGroup }) {
  const advice = egressAdvice(group, group.advice)
  const samples = Math.min(STRIP, group.thresholds.window * 6)
  return (
    <div className="flex min-w-0 flex-col gap-6">
      {advice && (
        <Notice tone={group.advice?.action === "hold" ? "default" : "warning"} title="Advice">
          {advice}
        </Notice>
      )}
      <Panel>
        <PanelHeader
          title="Members"
          actions={
            <span className="numeric text-hint text-muted-foreground">
              every {egressDuration(group.thresholds.intervalSeconds)} · group table {group.table}
            </span>
          }
        />
        <PanelBody flush>
          <div className="max-md:hidden">
            <Table aria-label={`Members of ${group.name}`}>
              <TableHeader>
                <TableRow>
                  <TableHead>Member</TableHead>
                  <TableHead>State</TableHead>
                  <TableHead className="text-right">Round trip</TableHead>
                  <TableHead className="text-right">Loss</TableHead>
                  <TableHead>Last {samples} samples</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {group.members.map((m) => (
                  <MemberRow key={m.id} group={group} member={m} samples={samples} />
                ))}
              </TableBody>
            </Table>
          </div>
          <ul
            aria-label={`Members of ${group.name}`}
            className="divide-y divide-hairline md:hidden"
          >
            {group.members.map((m) => (
              <MemberItem key={m.id} group={group} member={m} samples={samples} />
            ))}
          </ul>
        </PanelBody>
      </Panel>
      <Readiness group={group} />
      <Bindings group={group} />
      <HowItDecides group={group} />
    </div>
  )
}

/** Why a member is in its state: the monitor's own reason, else the probes that failed. */
function why(member: EgressMemberView): string | undefined {
  const last = member.last
  if (last?.why) return last.why
  const failing = last?.probes.filter((p) => !p.ok) ?? []
  return failing.length > 0
    ? failing.map((p) => `${p.kind} ${p.target}: ${p.error}`).join("; ")
    : undefined
}

function MemberRow({
  group,
  member,
  samples,
}: {
  group: EgressGroup
  member: EgressMemberView
  samples: number
}) {
  const state = egressMemberReading(member)
  const reason = why(member)
  const last = member.last
  return (
    <TableRow aria-label={`Member ${member.name}`} className="h-12">
      <TableCell>
        <span className="flex min-w-0 items-center gap-2">
          <span className="truncate font-medium">{member.name}</span>
          <CarryingTag group={group} member={member} />
        </span>
      </TableCell>
      {/* Takes the width the readings leave, and the reason gives way first. */}
      <TableCell className="w-full max-w-0">
        <span className="flex min-w-0 items-center gap-2" title={reason}>
          <Status tone={DOT[state.tone]} label={state.label} />
          {reason && <span className="truncate text-hint text-muted-foreground">{reason}</span>}
        </span>
      </TableCell>
      <TableCell className="numeric text-right whitespace-nowrap">
        {last?.latencyMillis !== undefined ? `${last.latencyMillis.toFixed(1)} ms` : "—"}
      </TableCell>
      <TableCell
        className={cn(
          "numeric text-right",
          last && last.loss > group.thresholds.lossPercent && "text-destructive",
        )}
      >
        {last ? `${Math.round(last.loss)}%` : "—"}
      </TableCell>
      <TableCell>
        <SampleStrip points={member.history.slice(-samples)} />
      </TableCell>
    </TableRow>
  )
}

/** A phone's member: two lines at one height, the same readings as the table's row. */
function MemberItem({
  group,
  member,
  samples,
}: {
  group: EgressGroup
  member: EgressMemberView
  samples: number
}) {
  const state = egressMemberReading(member)
  const last = member.last
  const reason = why(member)
  return (
    <li
      aria-label={`Member ${member.name}`}
      className="flex h-16 min-w-0 flex-col justify-center gap-1 px-4"
    >
      <span className="flex min-w-0 items-center gap-2">
        <span className="truncate text-body font-medium">{member.name}</span>
        <CarryingTag group={group} member={member} />
        <Status tone={DOT[state.tone]} label={state.label} />
        <span className="ml-auto shrink-0">
          <SampleStrip points={member.history.slice(-Math.min(samples, 16))} />
        </span>
      </span>
      <span className="flex min-w-0 items-center gap-2 text-hint text-muted-foreground">
        <span className="numeric shrink-0">
          {last?.latencyMillis !== undefined ? `${last.latencyMillis.toFixed(1)} ms` : "—"}
        </span>
        <span
          className={cn(
            "numeric shrink-0",
            last && last.loss > group.thresholds.lossPercent && "text-destructive",
          )}
        >
          {last ? `${Math.round(last.loss)}% loss` : "no sample"}
        </span>
        {reason && <span className="truncate">{reason}</span>}
      </span>
    </li>
  )
}

/** The member the group routes through; only "decided" while the group is off. */
function CarryingTag({ group, member }: { group: EgressGroup; member: EgressMemberView }) {
  if (!member.active) return null
  return group.enabled ? <Tag tone="success">carrying</Tag> : <Tag>decided</Tag>
}

/**
 * What each member is bound to: its path and tier, the mark its probes and
 * pinned connections carry, its own table and the address its probes leave
 * from. One line a member, in mono, because they are the kernel's literals.
 */
function Bindings({ group }: { group: EgressGroup }) {
  return (
    <Panel plain>
      <PanelHeader title="Bound to" />
      <PanelBody>
        <ul aria-label="What each member is bound to" className="divide-y divide-hairline">
          {group.members.map((m) => (
            <li
              key={m.id}
              className="flex min-w-0 flex-wrap items-baseline gap-x-4 gap-y-0.5 py-2 first:pt-0 last:pb-0"
            >
              <span className="w-32 shrink-0 truncate text-body font-medium">{m.name}</span>
              <span className="min-w-0 font-mono text-hint break-words text-muted-foreground">
                {egressHop(m)} · tier {m.priority}
                {m.weight > 1 ? ` · weight ${m.weight}` : ""} · mark {m.mark} · table {m.table}
                {m.probeSource ? ` · from ${m.probeSource}` : ""}
              </span>
            </li>
          ))}
        </ul>
      </PanelBody>
    </Panel>
  )
}

/** One cell per sample: filled when good, washed red when not. */
function SampleStrip({ points }: { points: EgressPoint[] }) {
  if (points.length === 0) return <span className="text-hint text-muted-foreground">none yet</span>
  const good = points.filter((p) => p.good).length
  return (
    <span
      role="img"
      aria-label={`${good} of ${points.length} recent samples good`}
      className="flex h-4 items-end gap-px"
    >
      {points.map((p) => (
        <span
          key={p.at}
          title={`${new Date(p.at).toLocaleTimeString()} · ${p.good ? "good" : "bad"}${p.latencyMillis !== undefined ? ` · ${p.latencyMillis.toFixed(1)} ms` : ""} · loss ${Math.round(p.loss)}%`}
          className={cn("h-full w-1 rounded-[1px]", p.good ? "bg-success/70" : "bg-destructive/80")}
        />
      ))}
    </span>
  )
}

function Readiness({ group }: { group: EgressGroup }) {
  const problems = group.readiness.filter((f) => f.level !== "ok")
  if (problems.length === 0) {
    return (
      <p className="text-hint text-muted-foreground">
        {group.readiness[0]?.text ?? "Readiness has not been read."}
      </p>
    )
  }
  return (
    <Notice
      tone={problems.some((f) => f.level === "error") ? "danger" : "warning"}
      title="Before relying on these paths"
    >
      <ul className="list-disc space-y-1 pl-4">
        {problems.map((f) => (
          <li key={f.text}>{f.text}</li>
        ))}
      </ul>
    </Notice>
  )
}

/**
 * The rules the monitor decides by, read back from the group: what it probes,
 * when a member is down or up again, how long it holds, what happens to open
 * connections and which addresses never move. Only the form showed these.
 */
function HowItDecides({ group }: { group: EgressGroup }) {
  const t = group.thresholds
  const rows: [string, React.ReactNode][] = [
    [
      "Probes",
      <span key="p" className="font-mono">
        {group.probes
          .map(
            (p) =>
              `${p.kind} ${p.target}${p.port ? `:${p.port}` : ""}${p.name ? ` ${p.name}` : ""}`,
          )
          .join(", ") || "none"}
      </span>,
    ],
    [
      "Down",
      `after ${t.failAfter} bad samples; bad is no answer in ${t.timeoutMillis} ms, a median over ${t.latencyMillis} ms, or over ${t.lossPercent}% loss in ${t.window}`,
    ],
    [
      "Up again",
      `after ${t.recoverAfter} good samples; fails back once up for ${egressDuration(t.stableSeconds)}${group.failback === "manual" ? ", by hand" : ""}`,
    ],
    ["Holds", `${egressDuration(t.holdSeconds)} between switches, except a failover`],
    [
      "Connections",
      `${group.sticky ? "pinned to their member; " : ""}${group.connections === "keep" ? "kept" : "flushed"} when they move`,
    ],
  ]
  if (group.protected?.length)
    rows.push([
      "Never moved",
      <span key="x" className="font-mono">
        {group.protected.join(", ")}
      </span>,
    ])
  return (
    <Panel plain>
      <PanelHeader title="How it decides" />
      <PanelBody>
        <dl className="grid gap-x-6 gap-y-2 text-body sm:grid-cols-[max-content_minmax(0,1fr)]">
          {rows.map(([term, value]) => (
            <div key={term} className="contents">
              <dt className="text-muted-foreground">{term}</dt>
              <dd className="min-w-0 max-sm:mb-1">{value}</dd>
            </div>
          ))}
        </dl>
      </PanelBody>
    </Panel>
  )
}
