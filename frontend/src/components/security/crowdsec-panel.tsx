"use client"

import { useState } from "react"
import { Bell, Check, Slash, Warning } from "@/components/icons"
import { del } from "@/lib/api"
import { plural, relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import type { CrowdSecView } from "@/lib/types"
import type { PollState } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { useConfirm } from "@/components/confirm-dialog"
import { WireMark } from "@/components/deploy/wire"
import { FactDot } from "@/components/metrics/host-identity"
import { InstallFollowUp, InstallHandoff } from "@/components/network/install"
import { Panel, PanelBody, PanelHeader, PanelToolbar } from "@/components/panel"
import { NoReading, ReadingLine, ReadingLines } from "@/components/reading-line"
import { Row, RowList } from "@/components/row-list"
import { EngineHead, EngineWaiting } from "@/components/security/engine-head"
import { ENFORCEMENT, enforcementOf, goDuration, pullAge } from "@/components/security/enforcement"
import {
  AddressLine,
  LineCell,
  LineList,
  LineRow,
  WIDE_GONE,
} from "@/components/security/line-list"
import { EmptyNote, ErrorState, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"

type Decision = CrowdSecView["decisions"][number]
type Alert = CrowdSecView["alerts"][number]

/** Past this many a list of decisions is a wall; the rest is one press away. */
const DECISIONS_SHOWN = 50
const ALERTS_SHOWN = 10

/** What is decided about, why, what kind, until when, and the release. */
const DECISION_COLUMNS = "minmax(0,1.5fr) minmax(0,1.3fr) 3rem 7.5rem 4.5rem"
/** The scenario, who it fired on, how much it saw, when. */
const ALERT_COLUMNS = "minmax(0,1.1fr) minmax(0,1fr) 5rem 5.5rem"

/**
 * CrowdSec, beside fail2ban and doing the half of the job fail2ban cannot.
 *
 * fail2ban reads this host's logs and bans what it saw misbehave here.
 * CrowdSec reads them too, but it also takes in what other servers saw (the
 * community list is thousands of addresses already attacking somebody else)
 * and it hands every decision to a bouncer, a small program in the firewall or
 * the proxy that actually enforces it. That second part is the thing to check
 * first: a decision nothing pulls is a sentence in a database. The server
 * judges enforcement from evidence (a bouncer that pulled within the last
 * few minutes and, for the firewall bouncer, a kernel set holding the
 * addresses with a hooked rule dropping on it) and the view claims
 * protection only where that verdict does.
 *
 * The view opens on the engine's own line, then says the two things its
 * tiles used to (whether decisions are dropped, what fired in the last day)
 * as lines. The decisions are one line each: the community list is most of
 * them on any host that has joined, so a chip narrows to what was decided
 * here, and the list stops at fifty rows. Under them, the alerts that made
 * those decisions and the bouncers that enforce them.
 */
export function CrowdSecPanel({
  view,
  onChanged,
}: {
  view: PollState<CrowdSecView>
  /** A decision was released: every engine's merged list is stale too. */
  onChanged: () => void
}) {
  const { can } = useAuth()
  const { data, error, loading } = view

  if (loading && !data) return <EngineWaiting id="crowdsec" name="CrowdSec" lines={2} />
  if (error && !data) return <ErrorState error={error} onRetry={view.refresh} />
  if (!data) return null

  if (!data.installed) {
    return (
      <InstallHandoff
        pkg="crowdsec"
        products={["crowdsec"]}
        title="CrowdSec is not installed"
        description="It reads the same logs as fail2ban, adds the addresses other servers already saw attacking, and enforces every decision through a bouncer in the firewall or the proxy."
        onInstalled={onChanged}
      />
    )
  }

  const alertsToday = data.alerts.filter((a) => inLastDay(a.createdAt)).length
  const state = enforcementOf(data)
  const words = ENFORCEMENT[state]
  const enforcement = data.enforcement
  const freshBouncers = enforcement?.bouncers.filter((b) => b.fresh).length ?? 0
  const community = data.decisions.filter(isCommunity).length

  return (
    <>
      <InstallFollowUp pkg="crowdsec" />
      <EngineHead
        id="crowdsec"
        name="CrowdSec"
        state={<Status verdict={words.verdict} label={words.label} />}
        facts={
          <>
            <span className="numeric">{plural(data.decisions.length, "decision")} in force</span>
            <FactDot />
            <span className="numeric">{community} from the community list</span>
            <FactDot />
            <span className="numeric">{plural(data.bouncers.length, "bouncer")}</span>
            {words.protects && enforcement && enforcement.enforcedBy.length > 0 && (
              <>
                <FactDot />
                <span>
                  enforced by{" "}
                  <span className="font-mono text-foreground">
                    {enforcement.enforcedBy.join(", ")}
                  </span>
                </span>
              </>
            )}
          </>
        }
      />

      {/* What stops being true when the engine is down: nothing is deciding
          and nothing new is pulled. The error is the engine's own words. */}
      {!data.active && (
        <Notice tone="warning" title="CrowdSec is installed and not running">
          {data.error ?? "Nothing is being detected, and the decisions below are the last it made."}
        </Notice>
      )}
      {data.active && data.error && (
        <Notice tone="warning" title="CrowdSec answered with an error">
          {data.error}
        </Notice>
      )}

      <ReadingLines>
        <ReadingLine
          mark={
            <WireMark
              size="md"
              tone={
                words.verdict === "ok"
                  ? "success"
                  : words.verdict === "critical"
                    ? "danger"
                    : "warning"
              }
            >
              {words.verdict === "ok" ? <Check aria-hidden /> : <Slash aria-hidden />}
            </WireMark>
          }
          name="Enforcement"
          hint={
            enforcement ? (
              <span className="numeric">
                {freshBouncers} of {data.bouncers.length} pulled within{" "}
                {goDuration(enforcement.freshness)}
              </span>
            ) : (
              "No verdict from the server"
            )
          }
        >
          {words.title}
        </ReadingLine>
        <ReadingLine
          mark={
            <WireMark size="md" hue="var(--tag-amber)">
              <Bell aria-hidden />
            </WireMark>
          }
          name="Alerts"
          hint={<span className="numeric">{data.alerts.length} on record</span>}
        >
          {alertsToday === 0 ? (
            <NoReading>No scenario fired in the last day</NoReading>
          ) : (
            <span className="numeric">{plural(alertsToday, "scenario")} fired in the last day</span>
          )}
        </ReadingLine>
      </ReadingLines>

      {/* The engine running is not the claim; the verdict is. Anything short
          of a verified drop says what is missing, in the server's words. */}
      {data.active && state !== "enforcing" && (
        <Notice
          tone={words.verdict === "critical" ? "danger" : words.protects ? "default" : "warning"}
          icon={words.protects ? undefined : Warning}
          title={words.title}
        >
          <p>
            {enforcement?.summary ??
              "This server did not say whether a bouncer enforces the decisions, so nothing here claims they are."}
          </p>
          {enforcement?.missing && enforcement.missing.length > 0 && (
            <ul className="mt-1 list-disc pl-5">
              {enforcement.missing.map((cause) => (
                <li key={`${cause.bouncer}-${cause.reason}`}>
                  {cause.bouncer && <span className="font-mono">{cause.bouncer}</span>}
                  {cause.bouncer ? ": " : ""}
                  {cause.reason}
                </li>
              ))}
            </ul>
          )}
        </Notice>
      )}

      <DecisionsPanel
        decisions={data.decisions}
        // Releasing lowers a defence: the route is inside `s.destructive`.
        canRelease={can("system.admin") && can("destructive")}
        onChanged={onChanged}
      />

      <div className="grid min-w-0 gap-8 xl:grid-cols-[minmax(0,3fr)_minmax(0,2fr)] xl:gap-12">
        <Panel plain>
          <PanelHeader
            title="Alerts"
            actions={
              <span className="numeric text-hint text-muted-foreground">
                newest {Math.min(data.alerts.length, ALERTS_SHOWN)} of {data.alerts.length}
              </span>
            }
          />
          <PanelBody flush>
            {data.alerts.length === 0 ? (
              <EmptyNote>No scenario has fired.</EmptyNote>
            ) : (
              <LineList columns={ALERT_COLUMNS} label="Alerts">
                {data.alerts.slice(0, ALERTS_SHOWN).map((alert) => (
                  <AlertRow key={alert.id} alert={alert} />
                ))}
              </LineList>
            )}
          </PanelBody>
        </Panel>

        <Panel plain>
          <PanelHeader title="Bouncers" />
          <PanelBody flush>
            {enforcement && enforcement.bouncers.length > 0 ? (
              <EnforcementEvidence enforcement={enforcement} />
            ) : data.bouncers.length === 0 ? (
              <EmptyNote>
                None registered. Install one, such as{" "}
                <span className="font-mono">crowdsec-firewall-bouncer-nftables</span>, so the
                decisions are enforced.
              </EmptyNote>
            ) : (
              <RowList className="animate-rise">
                {data.bouncers.map((bouncer) => (
                  <Row
                    key={bouncer.name}
                    title={bouncer.name}
                    subtitle={
                      [bouncer.type, bouncer.version, bouncer.ipAddress]
                        .filter(Boolean)
                        .join(" · ") || "no details"
                    }
                    mono
                    className="py-2.5"
                    trailing={
                      <>
                        <span className="numeric text-hint text-muted-foreground">
                          {bouncer.lastPull
                            ? `pulled ${relativeTime(bouncer.lastPull)}`
                            : "never pulled"}
                        </span>
                        <Status
                          verdict={bouncer.valid ? "ok" : "critical"}
                          label={bouncer.valid ? "valid" : "not valid"}
                        />
                      </>
                    }
                  />
                ))}
              </RowList>
            )}
          </PanelBody>
        </Panel>
      </div>
    </>
  )
}

/**
 * Each bouncer with what was read about it (what it enforces, when it last
 * pulled, whether its unit runs) and, under them, the firewall bouncer's
 * footprint in the kernel: the sets that hold the addresses and whether a
 * hooked rule drops on each.
 */
function EnforcementEvidence({
  enforcement,
}: {
  enforcement: NonNullable<CrowdSecView["enforcement"]>
}) {
  const kernel = enforcement.kernel
  return (
    <div className="flex flex-col gap-4">
      <RowList className="animate-rise">
        {enforcement.bouncers.map((bouncer) => (
          <Row
            key={bouncer.name}
            title={bouncer.name}
            subtitle={
              <span className="flex min-w-0 items-center gap-x-2">
                <Tag>{bouncer.kind}</Tag>
                {bouncer.unit && (
                  <span className="truncate font-mono">
                    {bouncer.unit}{" "}
                    {bouncer.unitActive === undefined
                      ? ""
                      : bouncer.unitActive
                        ? "running"
                        : "not running"}
                  </span>
                )}
              </span>
            }
            mono
            className="py-2.5"
            trailing={
              <>
                <span className="numeric text-hint text-muted-foreground">
                  {bouncer.pullAgeSeconds < 0
                    ? "never pulled"
                    : `pulled ${pullAge(bouncer.pullAgeSeconds)} ago`}
                </span>
                <Status
                  verdict={!bouncer.valid ? "critical" : bouncer.fresh ? "ok" : "warning"}
                  label={!bouncer.valid ? "not valid" : bouncer.fresh ? "pulling" : "stale"}
                />
              </>
            }
          />
        ))}
      </RowList>
      {kernel && (
        <div className="space-y-1.5" aria-label="Kernel sets">
          <p className="eyebrow">In the kernel{kernel.backend ? ` · ${kernel.backend}` : ""}</p>
          {kernel.error ? (
            <p className="text-body text-muted-foreground">{kernel.error}</p>
          ) : kernel.sets.length === 0 ? (
            <p className="text-body text-muted-foreground">
              No CrowdSec set exists in nftables or ipset.
            </p>
          ) : (
            <RowList>
              {kernel.sets.map((set) => (
                <Row
                  key={`${set.family}-${set.table}-${set.name}`}
                  title={set.name}
                  subtitle={
                    set.dropped
                      ? `dropped on the ${(set.hooks ?? ["input"]).join(", ")} hook`
                      : "no hooked rule drops on it"
                  }
                  mono
                  className="py-2"
                  trailing={
                    <>
                      <span className="numeric text-hint text-muted-foreground">
                        {plural(set.entries, "address", "addresses")}
                      </span>
                      <Status
                        verdict={set.dropped ? "ok" : "critical"}
                        label={set.dropped ? "dropping" : "inert"}
                      />
                    </>
                  }
                />
              ))}
            </RowList>
          )}
        </div>
      )}
    </div>
  )
}

/** Whether a moment is within the last twenty-four hours, as the reading counts alerts. */
function inLastDay(iso: string) {
  return Date.now() - new Date(iso).getTime() < 24 * 3_600_000
}

/** CrowdSec's own words for who decided, as a reader would say it. */
function originWords(origin: string) {
  switch (origin) {
    case "crowdsec":
      return "detected on this server"
    case "cscli":
      return "added by hand"
    case "CAPI":
      return "the community list"
    case "lists":
      return "a list you subscribed to"
    default:
      return origin
  }
}

/** Decided somewhere else: the community list or a subscribed blocklist. */
const isCommunity = (decision: Decision) =>
  decision.origin === "CAPI" || decision.origin === "lists"

/**
 * What a decision is about, drawn by its scope: an address or a range as the
 * network it is on with where it is from after it, and a whole country or
 * autonomous system as the words they are.
 */
function DecisionValue({ decision }: { decision: Decision }) {
  const scope = decision.scope.toLowerCase()
  if (scope === "ip" || scope === "range")
    return <AddressLine value={decision.value} country={decision.country} owner={decision.as} />
  return (
    <>
      <Tag>{decision.scope}</Tag>
      <span className="truncate font-mono font-medium">{decision.value}</span>
    </>
  )
}

function DecisionsPanel({
  decisions,
  canRelease,
  onChanged,
}: {
  decisions: Decision[]
  canRelease: boolean
  onChanged: () => void
}) {
  const { confirm, dialog } = useConfirm()
  const [which, setWhich] = useState<"all" | "here" | "community">("all")
  const [all, setAll] = useState(false)

  const here = decisions.filter((d) => !isCommunity(d))
  const community = decisions.length - here.length
  const shown =
    which === "here" ? here : which === "community" ? decisions.filter(isCommunity) : decisions
  const rows = all ? shown : shown.slice(0, DECISIONS_SHOWN)

  const release = (decision: Decision) =>
    confirm({
      title: `Release ${decision.value}`,
      confirmLabel: "Release",
      description: (
        <p>
          The decision is deleted and the address may connect again at the next pull. If it is still
          misbehaving, CrowdSec will decide about it again.
        </p>
      ),
      action: async (c) => {
        await del(`/security/crowdsec/decisions/${decision.id}`, { confirm: c })
        notify.success(`${decision.value} released`)
        onChanged()
      },
    })

  return (
    <>
      <Panel plain>
        <PanelHeader
          title="Decisions"
          actions={
            <span className="numeric text-hint text-muted-foreground">
              {shown.length > rows.length ? `${rows.length} of ${shown.length}` : shown.length}
            </span>
          }
        />
        <PanelToolbar>
          <ChipStrip aria-label="Which decisions to show">
            <FilterChip selected={which === "all"} onClick={() => setWhich("all")}>
              All <ChipCount>{decisions.length}</ChipCount>
            </FilterChip>
            <FilterChip selected={which === "here"} onClick={() => setWhich("here")}>
              Decided here <ChipCount>{here.length}</ChipCount>
            </FilterChip>
            <FilterChip selected={which === "community"} onClick={() => setWhich("community")}>
              Community list <ChipCount>{community}</ChipCount>
            </FilterChip>
          </ChipStrip>
        </PanelToolbar>
        <PanelBody flush>
          {shown.length === 0 ? (
            <EmptyNote>
              {decisions.length === 0
                ? "Nothing is blocked by CrowdSec right now."
                : "Nothing under this filter."}
            </EmptyNote>
          ) : (
            <LineList columns={DECISION_COLUMNS} label="Decisions">
              {rows.map((decision) => (
                <LineRow key={decision.id}>
                  <LineCell
                    main
                    under={`${decision.scenario} · ${originWords(decision.origin)} · ${decision.type}`}
                  >
                    <DecisionValue decision={decision} />
                  </LineCell>
                  <LineCell wide>
                    <span className="truncate font-mono text-hint" title={decision.scenario}>
                      {decision.scenario}
                    </span>
                    <span className="shrink-0 text-hint text-muted-foreground max-lg:hidden">
                      {originWords(decision.origin)}
                    </span>
                  </LineCell>
                  <LineCell wide>
                    <Tag tone={decision.type === "ban" ? "danger" : "warning"}>{decision.type}</Tag>
                  </LineCell>
                  <LineCell
                    wide
                    className="numeric text-hint whitespace-nowrap text-muted-foreground"
                  >
                    {decision.until ? relativeTime(decision.until) : decision.duration}
                  </LineCell>
                  <LineCell end>
                    <span
                      className={cn(
                        "numeric text-hint whitespace-nowrap text-muted-foreground",
                        WIDE_GONE,
                      )}
                    >
                      {decision.until ? relativeTime(decision.until) : decision.duration}
                    </span>
                    {canRelease && (
                      <Button
                        size="xs"
                        variant="outline"
                        aria-label={`Release ${decision.value}`}
                        onClick={() => release(decision)}
                      >
                        Release
                      </Button>
                    )}
                  </LineCell>
                </LineRow>
              ))}
            </LineList>
          )}
          {shown.length > rows.length && (
            <div className="flex items-center justify-end border-t border-hairline py-2.5">
              <Button size="xs" variant="ghost" onClick={() => setAll(true)}>
                Show all {shown.length}
              </Button>
            </div>
          )}
        </PanelBody>
      </Panel>
      {dialog}
    </>
  )
}

/** An alert on one line: the scenario that fired, who it fired on, and how much it saw. */
function AlertRow({ alert }: { alert: Alert }) {
  const source = alert.source.ip ?? alert.source.value
  return (
    <LineRow>
      <LineCell
        main
        under={[source, alert.source.country?.toUpperCase(), alert.source.asName]
          .filter(Boolean)
          .join(" · ")}
      >
        <span className="truncate font-mono text-hint text-foreground" title={alert.scenario}>
          {alert.scenario}
        </span>
      </LineCell>
      <LineCell wide title={alert.source.asName}>
        {source ? (
          <AddressLine value={source} country={alert.source.country} />
        ) : (
          <span className="text-muted-foreground">—</span>
        )}
      </LineCell>
      <LineCell wide end className="numeric text-hint whitespace-nowrap">
        {plural(alert.eventsCount, "event")}
      </LineCell>
      <LineCell end className="numeric text-hint whitespace-nowrap text-muted-foreground">
        {relativeTime(alert.createdAt)}
      </LineCell>
    </LineRow>
  )
}
