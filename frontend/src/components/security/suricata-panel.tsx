"use client"

import { useState } from "react"
import { ArrowRight, Bug, TerminalWindow } from "@/components/icons"
import { get } from "@/lib/api"
import { plural, relativeTime, timestamp } from "@/lib/format"
import type { SuricataView } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { BarList, type BarListItem } from "@/components/bar-list"
import { FactDot, HostIdentity } from "@/components/metrics/host-identity"
import { InstallFollowUp, InstallHandoff } from "@/components/network/install"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Address } from "@/components/security/marks"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { EmptyState, ErrorState, LoadingPanel, Notice } from "@/components/state"
import { Status, type DotTone } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import type { Tone } from "@/components/tone"
import { Button } from "@/components/ui/button"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"

type Alert = SuricataView["alerts"][number]

const ALERTS_SHOWN = 25

/** Suricata's own scale: 1 is the one to read first. */
const SEVERITY: Record<number, { label: string; tone: Tone; dot: DotTone }> = {
  1: { label: "High", tone: "danger", dot: "danger" },
  2: { label: "Medium", tone: "warning", dot: "warning" },
  3: { label: "Low", tone: "default", dot: "notice" },
}

/**
 * Suricata: what the packets themselves say, where fail2ban and CrowdSec read
 * what services wrote down.
 *
 * It matches traffic against thousands of signatures, so it sees a scan or an
 * exploit attempt against a port whose service logs nothing at all. Whether it
 * can do anything about one depends on how it runs, which the head states in
 * the first words: IDS only reads and raises the alert, IPS sits in the packet
 * path (NFQUEUE) and may drop. Switching between them is Suricata's own
 * configuration and a service restart, not something to flip from a dashboard
 * whose own traffic passes through the same queue, so this page reads the mode
 * and never sets it.
 *
 * Everything comes from the tail of eve.json: the readings count the alerts
 * the server scanned there, the signatures are ranked, and the latest alerts
 * are the one framed table. The file is root's, so the read is an
 * administrator's, and where it cannot be read the page names the path and why
 * rather than drawing a quiet night.
 */
export function SuricataPanel() {
  const { can } = useAuth()
  const admin = can("system.admin")
  const { data, error, loading, refresh } = usePoll<SuricataView>(
    (signal) => get("/security/suricata/", undefined, signal),
    20_000,
    [],
    { enabled: admin },
  )

  if (!admin) {
    return (
      <EmptyState
        icon={Bug}
        title="Suricata needs the admin capability"
        description="Its alerts name the addresses that probed this machine and what they sent, which is read from a log only root can open."
      />
    )
  }
  if (loading && !data) return <LoadingPanel />
  if (error && !data) return <ErrorState error={error} onRetry={refresh} />
  if (!data) return null

  if (!data.installed) {
    return (
      <InstallHandoff
        pkg="suricata"
        products={[]}
        icon={Bug}
        title="Suricata is not installed"
        description="An intrusion detection system that inspects packets against signatures, so it sees scans and exploit attempts that no service logs. In IDS mode it only reads and raises alerts; in IPS mode (NFQUEUE) it can also drop. Which one runs is set in Suricata's own configuration, not here."
        onInstalled={refresh}
      />
    )
  }

  const ips = data.mode === "ips"
  const severity = (level: number) => data.bySeverity.find((s) => s.severity === level)?.count ?? 0
  const readable = !data.logRefused && !data.logError
  const strongest = data.topSignatures.reduce((most, s) => Math.max(most, s.count), 1)

  const signatures: BarListItem[] = data.topSignatures.map((signature) => ({
    key: String(signature.signatureId),
    label: signature.signature,
    mono: false,
    hint: signature.category,
    value: signature.count,
    share: signature.count / strongest,
    signal: signature.severity <= 2 ? 1 : 0,
    tone: signature.severity === 1 ? "danger" : "warning",
    title: `${signature.signature} (sid ${signature.signatureId})`,
  }))

  return (
    <>
      <InstallFollowUp pkg="suricata" />
      <HostIdentity
        fallback={Bug}
        title={
          <>
            Suricata{" "}
            {data.version && (
              <span className="numeric font-mono text-body font-normal text-muted-foreground">
                {data.version}
              </span>
            )}
          </>
        }
        facts={
          <>
            <span title={ips ? "Sits in the packet path and may drop" : "Reads traffic and alerts"}>
              <span className="font-medium text-foreground">{ips ? "IPS" : "IDS"}</span>{" "}
              {ips ? "can drop what it matches" : "only reads"}
              {data.modeSource && <> · from {data.modeSource}</>}
            </span>
            {data.rulesLoaded !== undefined && (
              <>
                <FactDot />
                <span className="numeric">{plural(data.rulesLoaded, "rule")} loaded</span>
              </>
            )}
          </>
        }
        aside={
          <Status
            tone={data.active ? "running" : "stopped"}
            label={data.active ? "active" : "not running"}
            className="text-body"
          />
        }
      />

      {!data.active && (
        <Notice tone="warning" title="Suricata is installed and not running">
          Nothing is being inspected. What is listed below is what it wrote before it stopped.
        </Notice>
      )}

      {!readable && (
        <Notice tone="warning" icon={TerminalWindow} title="Suricata's alert log could not be read">
          <span className="font-mono">{data.logPath}</span>{" "}
          {data.logRefused ? `was refused: ${data.logRefused}` : `failed: ${data.logError}`}
        </Notice>
      )}

      {readable && (
        <>
          {/* The three severities each have a tile because they are what is
              acted on: a high alert is read today, a low one in aggregate. A
              zero is never coloured, so a tile that glows means there is
              something under it. */}
          <StatGrid columns={4} dense>
            <StatTile
              label="Alerts scanned"
              value={data.scanned}
              hint={`from the tail of ${data.logPath.split("/").pop()}`}
            />
            {[1, 2, 3].map((level) => (
              <StatTile
                key={level}
                label={`${SEVERITY[level].label} · severity ${level}`}
                value={severity(level)}
                tone={severity(level) > 0 ? SEVERITY[level].tone : "default"}
              />
            ))}
          </StatGrid>

          <Panel plain>
            <PanelHeader title="Top signatures" />
            <PanelBody flush>
              <BarList
                items={signatures}
                emptyLabel="No signature has matched in the alerts scanned."
              />
            </PanelBody>
          </Panel>

          <AlertsTable alerts={data.alerts} ips={ips} />
        </>
      )}
    </>
  )
}

/** The latest alerts: one framed table, because it scrolls sideways on a phone. */
function AlertsTable({ alerts, ips }: { alerts: Alert[]; ips: boolean }) {
  const [all, setAll] = useState(false)
  const rows = all ? alerts : alerts.slice(0, ALERTS_SHOWN)
  return (
    <Panel>
      <PanelHeader
        title="Latest alerts"
        actions={
          alerts.length > 0 && (
            <span className="numeric text-hint text-muted-foreground">newest first</span>
          )
        }
      />
      <PanelBody flush>
        {alerts.length === 0 ? (
          <p className="px-4 py-4 text-body text-muted-foreground">
            No alert in the log.{" "}
            {ips ? "Nothing it matched either." : "Nothing matched a signature."}
          </p>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>When</TableHead>
                <TableHead>Traffic</TableHead>
                <TableHead>Signature</TableHead>
                <TableHead>Severity</TableHead>
                <TableHead>Action</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((alert, index) => {
                const level = SEVERITY[alert.severity]
                return (
                  <TableRow key={`${alert.time}-${alert.signatureId}-${index}`}>
                    <TableCell
                      className="numeric py-3 whitespace-nowrap text-muted-foreground"
                      title={timestamp(alert.time)}
                    >
                      {relativeTime(alert.time)}
                    </TableCell>
                    <TableCell className="py-3">
                      <span className="flex flex-col gap-1 lg:flex-row lg:items-center lg:gap-1.5">
                        <Endpoint ip={alert.srcIp} port={alert.srcPort} />
                        <span className="inline-flex items-center gap-1.5">
                          <ArrowRight aria-label="to" className="size-3 text-muted-foreground" />
                          <Endpoint ip={alert.destIp} port={alert.destPort} />
                        </span>
                      </span>
                      <span className="mt-1 flex items-center gap-2 text-hint text-muted-foreground">
                        {alert.proto && <Tag mono>{alert.proto.toLowerCase()}</Tag>}
                        {alert.appProto && alert.appProto !== "failed" && (
                          <span>{alert.appProto}</span>
                        )}
                      </span>
                    </TableCell>
                    <TableCell className="py-3">
                      <span className="block max-w-96 min-w-48 text-body">{alert.signature}</span>
                      {alert.category && (
                        <span className="mt-1 block text-hint text-muted-foreground">
                          {alert.category}
                        </span>
                      )}
                    </TableCell>
                    <TableCell>
                      <Status
                        tone={level?.dot ?? "unknown"}
                        label={level?.label ?? alert.severity}
                      />
                    </TableCell>
                    <TableCell>
                      {alert.action ? (
                        <Status
                          tone={alert.action === "blocked" ? "running" : "notice"}
                          label={alert.action === "blocked" ? "blocked" : "allowed"}
                        />
                      ) : (
                        <span className="text-muted-foreground">—</span>
                      )}
                    </TableCell>
                  </TableRow>
                )
              })}
            </TableBody>
          </Table>
        )}
        {alerts.length > rows.length && (
          <div className="flex items-center justify-between gap-3 border-t border-hairline px-4 py-2.5">
            <span className="numeric text-hint text-muted-foreground">
              {rows.length} of {alerts.length}
            </span>
            <Button size="xs" variant="ghost" onClick={() => setAll(true)}>
              Show all {alerts.length}
            </Button>
          </div>
        )}
      </PanelBody>
    </Panel>
  )
}

/** An address with its port after it, in the port hue every other page draws ports in. */
function Endpoint({ ip, port }: { ip: string; port?: number }) {
  return (
    <span className="inline-flex min-w-0 items-baseline">
      <Address ip={ip} />
      {port !== undefined && port > 0 && (
        <span className="numeric font-mono">
          <span className="text-muted-foreground">:</span>
          <span className="text-[var(--tag-pink)]">{port}</span>
        </span>
      )}
    </span>
  )
}
