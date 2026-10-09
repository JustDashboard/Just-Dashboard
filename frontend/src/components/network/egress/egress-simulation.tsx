"use client"

import { cn } from "@/lib/utils"
import {
  egressNames,
  egressPhaseSummary,
  egressSimulationReading,
  type EgressGroup,
  type EgressView,
} from "@/lib/network-egress"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Status } from "@/components/status-dot"
import { Disclosure } from "@/components/form"
import { CheckCircle, CrossCircle } from "@/components/icons"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"

/**
 * What the monitor decided when this configuration's members were shown
 * flapping, loss, latency and an outage in disposable namespaces: the
 * expectations it was judged against, phase by phase, and the topology and
 * limits of the model. Automation waits on a passed run of exactly this
 * configuration.
 */
export function EgressSimulationPanel({
  group,
  running,
}: {
  group: EgressGroup
  running?: EgressView["running"]
}) {
  const sim = group.lastSimulation
  const reading = egressSimulationReading(sim, group.fingerprint)
  const tone =
    reading.tone === "success" ? "running" : reading.tone === "default" ? "stopped" : reading.tone
  return (
    <Panel plain>
      <PanelHeader
        title="Simulation"
        actions={
          running ? (
            <Status
              tone="warning"
              label={`Running · sample ${running.step + 1} of ${running.steps} · ${running.phase}`}
            />
          ) : (
            <Status tone={tone} label={reading.label} />
          )
        }
      />
      <PanelBody className="space-y-4">
        {!sim ? (
          <p className="text-hint text-muted-foreground">
            No simulation of this group yet. A run builds disposable network namespaces standing for
            this host, each member and the internet, injects latency, failure, flapping and an
            outage with netem, and records what these rules decide. Automation stays off until a run
            passes.
          </p>
        ) : (
          <>
            <p className="text-hint text-muted-foreground">
              Simulation {sim.id}, started{" "}
              <time dateTime={sim.startedAt}>{new Date(sim.startedAt).toLocaleString()}</time>
              {sim.actor ? ` by ${sim.actor}` : ""}
              {sim.result
                ? ` · one sample every ${sim.result.intervalSeconds} s of simulated time`
                : ""}
              {!reading.current && sim.status !== "running"
                ? " · this configuration has changed since"
                : ""}
            </p>
            {sim.error && <p className="text-body text-destructive">{sim.error}</p>}
            {sim.result && (
              <>
                <ul className="grid gap-2 sm:grid-cols-2" aria-label="Expectations">
                  {sim.result.expectations.map((e) => (
                    <li key={e.name} className="flex min-w-0 items-start gap-2">
                      {e.passed ? (
                        <CheckCircle aria-hidden className="mt-0.5 size-4 shrink-0 text-success" />
                      ) : (
                        <CrossCircle
                          aria-hidden
                          className="mt-0.5 size-4 shrink-0 text-destructive"
                        />
                      )}
                      <span className="min-w-0">
                        <span
                          className={cn(
                            "block text-body font-medium",
                            !e.passed && "text-destructive",
                          )}
                        >
                          {e.name}
                          <span className="sr-only">
                            {e.passed ? " — held" : " — did not hold"}
                          </span>
                        </span>
                        <span className="block text-hint text-muted-foreground">{e.detail}</span>
                      </span>
                    </li>
                  ))}
                </ul>
                <Phases group={group} />
                <Disclosure quiet summary="Topology, limits and cleanup">
                  <ul className="list-disc space-y-1 pl-4 text-hint text-muted-foreground">
                    {sim.result.topology.map((line) => (
                      <li key={line}>{line}</li>
                    ))}
                    {sim.result.limits.map((line) => (
                      <li key={line}>{line}</li>
                    ))}
                    <li>{sim.result.cleanup}</li>
                  </ul>
                </Disclosure>
              </>
            )}
          </>
        )}
      </PanelBody>
    </Panel>
  )
}

function Phases({ group }: { group: EgressGroup }) {
  const phases = egressPhaseSummary(group.lastSimulation!)
  return (
    <div className="overflow-x-auto">
      <Table aria-label="Simulated phases">
        <TableHeader>
          <TableRow>
            <TableHead>Phase</TableHead>
            <TableHead className="text-right">Samples</TableHead>
            <TableHead>What the rules did</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {phases.map((p) => (
            <TableRow key={p.name}>
              <TableCell>
                <span className="font-medium">{p.name}</span>
                <div className="text-hint text-muted-foreground">{p.description}</div>
              </TableCell>
              <TableCell className="numeric text-right">{p.samples}</TableCell>
              <TableCell className="text-body">
                {p.switches.length === 0 ? (
                  <span className="text-muted-foreground">
                    No switch{p.holds ? ` · held ${p.holds} samples` : ""}
                  </span>
                ) : (
                  p.switches.map((s) => (
                    <div key={s.sample}>
                      Sample {s.sample}: {s.action} to {egressNames(group, s.active)}
                      <div className="text-hint text-muted-foreground">{s.reason}</div>
                    </div>
                  ))
                )}
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  )
}
