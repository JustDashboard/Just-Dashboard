"use client"

import { useState } from "react"
import { useAuth } from "@/hooks/use-auth"
import { del, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import {
  egressDuration,
  egressEventReading,
  egressGroupReading,
  egressHop,
  egressManualActions,
  egressMemberReading,
  egressNames,
  egressPolicySummary,
  type EgressEvent,
  type EgressGroup,
  type EgressMemberView,
  type EgressPoint,
  type EgressView,
} from "@/lib/network-egress"
import type { Tone } from "@/components/tone"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Status, type DotTone } from "@/components/status-dot"
import { Notice } from "@/components/state"
import { Tag } from "@/components/tag"
import { Disclosure } from "@/components/form"
import { useConfirm } from "@/components/confirm-dialog"
import { Button } from "@/components/ui/button"
import { Switch } from "@/components/ui/switch"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { EgressSimulationPanel } from "@/components/network/egress/egress-simulation"

const DOT: Record<Tone, DotTone> = {
  default: "stopped",
  success: "running",
  warning: "warning",
  danger: "danger",
}

const TONE_TEXT: Record<Tone, string> = {
  default: "text-muted-foreground",
  success: "text-success",
  warning: "text-warning",
  danger: "text-destructive",
}

/**
 * One egress group as a reading: what it carries and through which member,
 * every member measured through its own path, what the rules would do now,
 * the decisions it made with the evidence behind them, and the simulation
 * automation waits on. Every command that moves traffic is behind the
 * destructive confirmation the server also enforces.
 */
export function EgressGroupPanel({
  group,
  running,
  onChanged,
  onEdit,
}: {
  group: EgressGroup
  running?: EgressView["running"]
  onChanged: () => void
  onEdit: (group: EgressGroup) => void
}) {
  const { can } = useAuth()
  const admin = can("system.admin")
  const destructive = admin && can("destructive")
  const { confirm, dialog } = useConfirm()
  const [busy, setBusy] = useState(false)
  const reading = egressGroupReading(group)
  const manual = egressManualActions(group)

  const switchTo = (action: "failover" | "failback") =>
    confirm({
      title: action === "failover" ? `Fail ${group.name} over now` : `Fail ${group.name} back now`,
      confirmLabel: action === "failover" ? "Fail over" : "Fail back",
      description: (
        <div className="space-y-2">
          <p>
            The group&rsquo;s traffic moves off {egressNames(group, group.active)}. The members it
            moves to must answer their probes first, and the switch is put back if your own
            connection or this server&rsquo;s way out would move anywhere unproven.
          </p>
          <p>
            Connections already open through {egressNames(group, group.active)} are counted;
            {group.sticky
              ? " pinned ones stay on their member while it is up."
              : " one translated to its address will not survive the move."}
          </p>
        </div>
      ),
      action: async () => {
        const result = await post<{
          after: number[]
          connections?: { moved: number; flushed: number }
        }>(`/network/egress/${group.id}/switch`, { action })
        notify.success(`${group.name} now routes through ${egressNames(group, result.after)}`, {
          description: result.connections
            ? `${result.connections.moved} tracked connections moved, ${result.connections.flushed} flushed.`
            : undefined,
        })
        onChanged()
        return "reported"
      },
    })

  const setEnabled = (on: boolean) =>
    confirm({
      title: on ? `Enable ${group.name}` : `Disable ${group.name}`,
      confirmLabel: on ? "Enable" : "Disable",
      description: on ? (
        <p>
          {egressPolicySummary(group.policy)} leaves the main table for{" "}
          {egressNames(group, group.active)}. The decided members are probed first; if the group
          would carry your own connection, your address is kept on the main table.
        </p>
      ) : (
        <p>
          The group&rsquo;s traffic returns to whatever the main table says, and automation is
          turned off. If the main table&rsquo;s path is the one that failed, that traffic fails with
          it.
        </p>
      ),
      action: async () => {
        await post(`/network/egress/${group.id}/${on ? "enable" : "disable"}`)
        notify.success(
          on ? `${group.name} carries traffic` : `${group.name} no longer carries traffic`,
        )
        onChanged()
        return "reported"
      },
    })

  const setAutomation = async (on: boolean) => {
    if (on) {
      confirm({
        title: `Automate ${group.name}`,
        confirmLabel: "Turn on",
        description: (
          <p>
            The monitor will fail over as soon as {egressNames(group, group.active)} is declared
            down and{" "}
            {group.failback === "automatic"
              ? "fail back after a stable recovery"
              : "leave failback to you"}
            , through the same proof and guards a manual switch takes. Simulation{" "}
            {group.lastSimulation?.id} showed these rules deciding.
          </p>
        ),
        action: async () => {
          await post(`/network/egress/${group.id}/automation/on`)
          notify.success(`${group.name} is automated`)
          onChanged()
          return "reported"
        },
      })
      return
    }
    setBusy(true)
    try {
      await post(`/network/egress/${group.id}/automation/off`)
      notify.success(`${group.name} switches only by hand now`)
      onChanged()
    } catch (err) {
      notify.error("Automation not changed", err)
    } finally {
      setBusy(false)
    }
  }

  const remove = () =>
    confirm({
      title: `Remove ${group.name}`,
      confirmLabel: "Remove",
      description: (
        <p>
          Its tables, rules and connection pinning are removed
          {group.enabled ? ", and its traffic returns to the main table" : ""}. The decision record
          stays.
        </p>
      ),
      action: async () => {
        await del(`/network/egress/${group.id}`)
        notify.success(`${group.name} removed`)
        onChanged()
        return "reported"
      },
    })

  const simulate = async () => {
    setBusy(true)
    try {
      await post(`/network/egress/${group.id}/simulate`)
      notify.success("Simulation started", {
        description: "It runs in disposable namespaces; this host's own network is not touched.",
      })
      onChanged()
    } catch (err) {
      notify.error("Simulation not started", err)
    } finally {
      setBusy(false)
    }
  }

  const runningHere = running?.groupId === group.id
  return (
    <section
      aria-label={`Egress group ${group.name}`}
      className="flex min-w-0 flex-col gap-4 border-t border-hairline pt-6 first:border-t-0 first:pt-0"
    >
      <div className="flex min-w-0 flex-wrap items-start justify-between gap-x-6 gap-y-3">
        <div className="min-w-0 space-y-1">
          <h2 className="flex flex-wrap items-center gap-2 text-base font-semibold tracking-tight">
            {group.name}
            {group.family === "inet6" && <Tag>IPv6</Tag>}
            {group.sticky && <Tag>sticky</Tag>}
          </h2>
          <p className="text-body text-muted-foreground">{egressPolicySummary(group.policy)}</p>
          <Status tone={DOT[reading.tone]} label={reading.label} />
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <label className="flex items-center gap-2 text-body">
            <Switch
              checked={group.automation}
              disabled={
                busy || (group.automation ? !admin : !destructive || Boolean(group.automationBlock))
              }
              onCheckedChange={(next) => void setAutomation(next)}
              aria-label={`Automate ${group.name}`}
            />
            Automation
          </label>
          {group.enabled && (
            <>
              <Button
                size="xs"
                variant="outline"
                disabled={!destructive || !manual.failover}
                onClick={() => switchTo("failover")}
              >
                Fail over now
              </Button>
              <Button
                size="xs"
                variant="outline"
                disabled={!destructive || !manual.failback}
                onClick={() => switchTo("failback")}
              >
                Fail back now
              </Button>
            </>
          )}
          <Button
            size="xs"
            variant="outline"
            disabled={!destructive}
            onClick={() => setEnabled(!group.enabled)}
          >
            {group.enabled ? "Disable" : "Enable"}
          </Button>
          <Button
            size="xs"
            variant="outline"
            disabled={!admin || busy || Boolean(running)}
            onClick={() => void simulate()}
          >
            Run simulation
          </Button>
          <Button
            size="xs"
            variant="outline"
            disabled={!admin || group.enabled}
            title={group.enabled ? "Disable the group to edit it" : undefined}
            onClick={() => onEdit(group)}
          >
            Edit
          </Button>
          <Button size="xs" variant="outline" disabled={!destructive} onClick={remove}>
            Remove
          </Button>
        </div>
      </div>

      {group.automationBlock && !group.automation && (
        <p className="text-hint text-muted-foreground">{group.automationBlock}</p>
      )}
      {group.advice && group.advice.action !== "none" && (
        <Notice
          tone={group.advice.action === "hold" ? "default" : "warning"}
          title={adviceTitle(group)}
        >
          {group.advice.reason}
        </Notice>
      )}
      {group.runtime && group.runtime.status !== "verified" && (
        <Notice tone="danger" title="The kernel does not hold the decided route">
          {group.runtime.status === "drift"
            ? `Missing: ${group.runtime.missing?.join(", ")}.`
            : group.runtime.error}
        </Notice>
      )}

      <MemberTable group={group} />
      <Readiness group={group} />
      <Timeline group={group} />
      <EgressSimulationPanel group={group} running={runningHere ? running : undefined} />
      {dialog}
    </section>
  )
}

function adviceTitle(group: EgressGroup): string {
  const advice = group.advice!
  const target = egressNames(group, advice.target)
  switch (advice.action) {
    case "failover":
    case "rebalance":
      return group.automation
        ? `Failing over to ${target}`
        : `The rules call for failover to ${target}`
    case "failback":
      return group.automation
        ? `Failing back to ${target}`
        : `The rules call for failback to ${target}`
    default:
      return "Holding"
  }
}

/** Every member measured through its own mark, source and device. */
function MemberTable({ group }: { group: EgressGroup }) {
  return (
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
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Member</TableHead>
              <TableHead>State</TableHead>
              <TableHead className="text-right">Round trip</TableHead>
              <TableHead className="text-right">Loss</TableHead>
              <TableHead>Last {Math.min(30, group.thresholds.window * 6)} samples</TableHead>
              <TableHead>Bound to</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {group.members.map((m) => (
              <MemberRow key={m.id} group={group} member={m} />
            ))}
          </TableBody>
        </Table>
      </PanelBody>
    </Panel>
  )
}

function MemberRow({ group, member }: { group: EgressGroup; member: EgressMemberView }) {
  const reading = egressMemberReading(member)
  const last = member.last
  const failing = last?.probes.filter((p) => !p.ok) ?? []
  return (
    <TableRow aria-label={`Member ${member.name}`}>
      <TableCell>
        <div className="flex flex-wrap items-center gap-2">
          <span className="font-medium">{member.name}</span>
          {member.active && <Tag tone="success">carrying</Tag>}
        </div>
        <div className="text-hint text-muted-foreground">
          {egressHop(member)} · tier {member.priority}
          {member.weight > 1 ? ` · weight ${member.weight}` : ""}
        </div>
      </TableCell>
      <TableCell>
        <Status tone={DOT[reading.tone]} label={reading.label} />
        {last?.why && <div className="mt-1 text-hint text-muted-foreground">{last.why}</div>}
        {failing.length > 0 && !last?.why && (
          <div className="mt-1 text-hint text-muted-foreground">
            {failing.map((p) => `${p.kind} ${p.target}: ${p.error}`).join("; ")}
          </div>
        )}
      </TableCell>
      <TableCell className="numeric text-right">
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
        <SampleStrip points={member.history.slice(-Math.min(30, group.thresholds.window * 6))} />
      </TableCell>
      <TableCell className="font-mono text-hint text-muted-foreground">
        mark {member.mark}
        <div>
          table {member.table}
          {member.probeSource ? ` · from ${member.probeSource}` : ""}
        </div>
      </TableCell>
    </TableRow>
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

/** Every decision, newest first, with the evidence it was made on. */
function Timeline({ group }: { group: EgressGroup }) {
  return (
    <Panel plain>
      <PanelHeader
        title="Decisions"
        actions={
          <span className="numeric text-hint text-muted-foreground">
            {group.events.length} recent
          </span>
        }
      />
      <PanelBody>
        {group.events.length === 0 ? (
          <p className="text-hint text-muted-foreground">Nothing has been decided yet.</p>
        ) : (
          <ol
            className="flex flex-col divide-y divide-hairline"
            aria-label={`Decisions of ${group.name}`}
          >
            {group.events.map((event) => (
              <EventRow key={event.id} group={group} event={event} />
            ))}
          </ol>
        )}
      </PanelBody>
    </Panel>
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
