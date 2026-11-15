"use client"

import { useState } from "react"
import { ArrowRight, Bug, TerminalWindow } from "@/components/icons"
import { plural, relativeTime, timestamp } from "@/lib/format"
import type { SuricataView } from "@/lib/types"
import type { PollState } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { BarReading } from "@/components/bar-reading"
import { RankList, type RankItem } from "@/components/rank-list"
import { FactDot } from "@/components/metrics/host-identity"
import { InstallFollowUp, InstallHandoff } from "@/components/network/install"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EngineHead, EngineWaiting } from "@/components/security/engine-head"
import { LineCell, LineList, LineRow } from "@/components/security/line-list"
import { SuricataSetup } from "@/components/security/suricata-setup"
import { EmptyNote, EmptyState, ErrorState, Notice } from "@/components/state"
import { Status, StatusDot, type DotTone } from "@/components/status-dot"
import { Button } from "@/components/ui/button"

type Alert = SuricataView["alerts"][number]

const ALERTS_SHOWN = 25

/** Suricata's own scale: 1 is the one to read first. */
const SEVERITY: Record<number, { label: string; dot: DotTone; color: string }> = {
  1: { label: "High", dot: "danger", color: "var(--destructive)" },
  2: { label: "Medium", dot: "warning", color: "var(--warning)" },
  3: { label: "Low", dot: "notice", color: "var(--chart-2)" },
}

/** When, the two ends of the traffic, what matched, how bad, what was done. */
const ALERT_COLUMNS = "4.75rem 19.5rem minmax(0,1fr) 4.5rem 4.25rem"

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
 * whose own traffic passes through the same queue, so this view reads the mode
 * and the queue rules and never sets them. The setup it does carry (the
 * capture interface, the rules, starting the service) is `suricata-setup`.
 *
 * Everything comes from the tail of eve.json: the severities are one band,
 * the signatures are ranked, and the latest alerts are one line each. The
 * file is root's, so the read is an administrator's, and where it cannot be
 * read the view names the path and why rather than drawing a quiet night.
 */
export function SuricataPanel({ view }: { view: PollState<SuricataView> }) {
  const { can } = useAuth()
  const admin = can("system.admin")
  const { data, error, loading, refresh } = view

  if (!admin) {
    return (
      <EmptyState
        icon={Bug}
        title="Suricata needs the admin capability"
        description="Its alerts name the addresses that probed this machine and what they sent, which is read from a log only root can open."
      />
    )
  }
  if (loading && !data) return <EngineWaiting id="suricata" name="Suricata" lines={0} />
  if (error && !data) return <ErrorState error={error} onRetry={refresh} retryable />
  if (!data) return null

  if (!data.installed) {
    return (
      <InstallHandoff
        pkg="suricata"
        products={[]}
        icon={Bug}
        title="Suricata is not installed"
        description="It inspects packets against signatures, so it sees scans and exploit attempts that no service logs. As an IDS it only reads; in IPS mode it can also drop, which is set in Suricata's own configuration."
        onInstalled={refresh}
      />
    )
  }

  const ips = data.mode === "ips"
  const severity = (level: number) => data.bySeverity.find((s) => s.severity === level)?.count ?? 0
  const readable = !data.logRefused && !data.logError
  // Figures in columns: how often each signature matched and its share of
  // the alerts scanned, with its severity as the dot before the name.
  const signatures: RankItem[] = data.topSignatures.map((signature) => ({
    key: String(signature.signatureId),
    label: signature.signature,
    mono: false,
    mark: <StatusDot tone={SEVERITY[signature.severity]?.dot ?? "unknown"} />,
    hint: signature.category,
    value: signature.count.toLocaleString(),
    share: data.scanned > 0 ? signature.count / data.scanned : undefined,
    title: `${signature.signature} (sid ${signature.signatureId})`,
  }))

  return (
    <>
      <InstallFollowUp pkg="suricata" />
      <EngineHead
        id="suricata"
        name="Suricata"
        title={
          <span className="flex items-baseline gap-2">
            Suricata
            {data.version && (
              <span className="numeric font-mono text-body font-normal text-muted-foreground">
                {data.version}
              </span>
            )}
          </span>
        }
        state={
          <Status
            tone={data.active ? "running" : "stopped"}
            label={data.active ? "active" : "not running"}
          />
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
      />

      {!data.active && (
        <Notice tone="warning" title="Suricata is installed and not running">
          Nothing is being inspected. What is listed below is what it wrote before it stopped.
        </Notice>
      )}

      <SuricataSetup data={data} onChanged={refresh} />

      {!readable && (
        <Notice tone="warning" icon={TerminalWindow} title="Suricata's alert log could not be read">
          <span className="font-mono">{data.logPath}</span>{" "}
          {data.logRefused ? `was refused: ${data.logRefused}` : `failed: ${data.logError}`}
        </Notice>
      )}

      {readable && (
        <>
          {/* The three severities as one band: a high alert is read today, a
              low one in aggregate, and their proportion is the reading. They
              were four tiles, the first of them the sum of the other three. */}
          <BarReading
            title="Alerts by severity"
            label="Alerts by severity"
            list="Severities"
            fixed
            figure={
              <span className="numeric flex h-7 items-center gap-1 text-hint text-muted-foreground">
                <span className="font-medium text-foreground">{data.scanned.toLocaleString()}</span>
                scanned from the tail of {data.logPath.split("/").pop()}
              </span>
            }
            message={
              data.scanned === 0 ? "No alert in the part of the log that was scanned." : undefined
            }
            parts={[1, 2, 3]
              .filter((level) => severity(level) > 0)
              .map((level) => ({
                key: String(level),
                value: severity(level),
                color: SEVERITY[level].color,
                mark: <StatusDot tone={SEVERITY[level].dot} />,
                name: SEVERITY[level].label,
                detail: `severity ${level}`,
                figure: plural(severity(level), "alert"),
                label: `${SEVERITY[level].label} ${severity(level)}`,
              }))}
          />

          <Panel plain>
            <PanelHeader title="Top signatures" />
            <PanelBody flush>
              <RankList
                items={signatures}
                emptyLabel="No signature has matched in the alerts scanned."
              />
            </PanelBody>
          </Panel>

          <AlertsList alerts={data.alerts} ips={ips} />
        </>
      )}
    </>
  )
}

/** The latest alerts, one line each, newest first. */
function AlertsList({ alerts, ips }: { alerts: Alert[]; ips: boolean }) {
  const [all, setAll] = useState(false)
  const rows = all ? alerts : alerts.slice(0, ALERTS_SHOWN)
  return (
    <Panel plain>
      <PanelHeader
        title="Latest alerts"
        actions={
          alerts.length > 0 && (
            <span className="numeric text-hint text-muted-foreground">
              {alerts.length > rows.length
                ? `newest ${rows.length} of ${alerts.length}`
                : "newest first"}
            </span>
          )
        }
      />
      <PanelBody flush>
        {alerts.length === 0 ? (
          <EmptyNote>
            No alert in the log.{" "}
            {ips ? "Nothing it matched either." : "Nothing matched a signature."}
          </EmptyNote>
        ) : (
          <LineList columns={ALERT_COLUMNS} label="Latest alerts" roomy>
            {rows.map((alert, index) => {
              const level = SEVERITY[alert.severity]
              const protocol = [
                alert.proto?.toLowerCase(),
                alert.appProto && alert.appProto !== "failed" ? alert.appProto : undefined,
              ]
                .filter(Boolean)
                .join(" · ")
              return (
                <LineRow key={`${alert.time}-${alert.signatureId}-${index}`}>
                  <LineCell
                    wide
                    className="numeric text-hint whitespace-nowrap text-muted-foreground"
                  >
                    <span title={timestamp(alert.time)}>{relativeTime(alert.time)}</span>
                  </LineCell>
                  <LineCell wide className="gap-1.5 text-hint" title={protocol}>
                    <Endpoint ip={alert.srcIp} port={alert.srcPort} />
                    <ArrowRight aria-label="to" className="size-3 shrink-0 text-muted-foreground" />
                    <Endpoint ip={alert.destIp} port={alert.destPort} />
                  </LineCell>
                  <LineCell
                    main
                    under={`${alert.srcIp} to ${alert.destIp}${alert.destPort ? `:${alert.destPort}` : ""} · ${protocol} · ${relativeTime(alert.time)}`}
                  >
                    <span
                      className="truncate"
                      title={[alert.signature, alert.category].filter(Boolean).join(" · ")}
                    >
                      {alert.signature}
                    </span>
                  </LineCell>
                  <LineCell end>
                    <Status tone={level?.dot ?? "unknown"} label={level?.label ?? alert.severity} />
                  </LineCell>
                  <LineCell wide>
                    {alert.action ? (
                      <Status
                        tone={alert.action === "blocked" ? "running" : "notice"}
                        label={alert.action === "blocked" ? "blocked" : "allowed"}
                      />
                    ) : (
                      <span className="text-muted-foreground">—</span>
                    )}
                  </LineCell>
                </LineRow>
              )
            })}
          </LineList>
        )}
        {alerts.length > rows.length && (
          <div className="flex items-center justify-end border-t border-hairline py-2.5">
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
    <span className="numeric min-w-0 truncate font-mono">
      {ip}
      {port !== undefined && port > 0 && (
        <>
          <span className="text-muted-foreground">:</span>
          <span className="text-[var(--tag-pink)]">{port}</span>
        </>
      )}
    </span>
  )
}
