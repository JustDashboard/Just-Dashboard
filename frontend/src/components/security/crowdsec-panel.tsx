"use client"

import { useState } from "react"
import { Slash } from "@/components/icons"
import { del, get, post, ApiError } from "@/lib/api"
import { plural, relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { BlocksView, CrowdSecView } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { useConfirm } from "@/components/confirm-dialog"
import { Field } from "@/components/form"
import { FactDot, HostIdentity } from "@/components/metrics/host-identity"
import { Modal } from "@/components/modal"
import { InstallHandoff } from "@/components/network/install"
import { Panel, PanelBody, PanelHeader, PanelToolbar } from "@/components/panel"
import { Row, RowList } from "@/components/row-list"
import { heldSentence } from "@/components/security/blocks"
import { ENFORCEMENT, enforcementOf, goDuration, pullAge } from "@/components/security/enforcement"
import { Address } from "@/components/security/marks"
import { useSecurity } from "@/components/security/security-context"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { ErrorState, LoadingPanel, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"

type Decision = CrowdSecView["decisions"][number]
type Alert = CrowdSecView["alerts"][number]

/** Past this many a table of decisions is a wall; the rest is one press away. */
const DECISIONS_SHOWN = 50
const ALERTS_SHOWN = 10

/**
 * CrowdSec, beside fail2ban and doing the half of the job fail2ban cannot.
 *
 * fail2ban reads this host's logs and bans what it saw misbehave here.
 * CrowdSec reads them too, but it also takes in what other servers saw — the
 * community list is thousands of addresses already attacking somebody else —
 * and it hands every decision to a bouncer, a small program in the firewall or
 * the proxy that actually enforces it. That second part is the thing to check
 * first: a decision nothing pulls is a sentence in a database. The server
 * judges enforcement from evidence — a bouncer that pulled within the last
 * few minutes and, for the firewall bouncer, a kernel set holding the
 * addresses with a hooked rule dropping on it — and the page claims
 * protection only where that verdict does.
 *
 * The decisions are the one table, framed because it scrolls (§2). The
 * community list is most of it on any host that has joined, so a chip narrows
 * to what was decided here — by CrowdSec's own scenarios or by hand — and the
 * table stops at fifty rows. Everything else is plain: the alerts that made
 * those decisions, and the bouncers that enforce them.
 */
export function CrowdSecPanel({
  blocks,
  onBlocked,
}: {
  /** Every engine's refused addresses, so the ban form can say what already holds one. */
  blocks?: BlocksView
  onBlocked?: () => void
}) {
  const { can } = useAuth()
  const {
    data,
    error,
    loading,
    refresh: reload,
  } = usePoll<CrowdSecView>((signal) => get("/security/crowdsec/", undefined, signal), 20_000)
  const refresh = () => {
    reload()
    onBlocked?.()
  }

  if (loading && !data) return <LoadingPanel />
  if (error && !data) return <ErrorState error={error} onRetry={refresh} />
  if (!data) return null

  if (!data.installed) {
    return (
      <InstallHandoff
        pkg="crowdsec"
        products={["crowdsec"]}
        title="CrowdSec is not installed"
        description="It sits beside fail2ban rather than in its place. Its scenarios catch more than a log regex can, it subscribes this server to the community list of addresses already attacking other servers, and its bouncers enforce every decision in the firewall or the proxy instead of only in a log."
        onInstalled={refresh}
      />
    )
  }

  const admin = can("system.admin")
  const alertsToday = data.alerts.filter((a) => inLastDay(a.createdAt)).length
  const state = enforcementOf(data)
  const words = ENFORCEMENT[state]
  const enforcement = data.enforcement
  const freshBouncers = enforcement?.bouncers.filter((b) => b.fresh).length ?? 0

  return (
    <>
      <HostIdentity
        mark="crowdsec"
        title="CrowdSec"
        facts={
          <>
            <span className="numeric">{plural(data.decisions.length, "decision")} in force</span>
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
        aside={<Status verdict={words.verdict} label={words.label} className="text-body" />}
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

      <StatGrid columns={3} dense>
        <StatTile
          label="Decisions in force"
          value={data.decisions.length}
          hint={`${data.decisions.filter(isCommunity).length} from the community list`}
        />
        <StatTile
          label="Alerts, last day"
          value={alertsToday}
          tone={alertsToday > 0 ? "warning" : "default"}
          hint={`${data.alerts.length} on record`}
        />
        <StatTile
          label="Enforcement"
          value={words.label[0].toUpperCase() + words.label.slice(1)}
          tone={
            words.verdict === "critical" ? "danger" : words.verdict === "ok" ? "default" : "warning"
          }
          hint={
            enforcement
              ? `${freshBouncers} of ${data.bouncers.length} pulled within ${goDuration(enforcement.freshness)}`
              : "no verdict from the server"
          }
        />
      </StatGrid>

      {/* The engine running is not the claim; the verdict is. Anything short
          of a verified drop says what is missing, in the server's words. */}
      {data.active && state !== "enforcing" && (
        <Notice
          tone={words.verdict === "critical" ? "danger" : words.protects ? "default" : "warning"}
          icon={words.protects ? undefined : Slash}
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
        canManage={admin}
        blocks={blocks}
        onChanged={refresh}
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
              <p className="py-3 text-body text-muted-foreground">
                No scenario has fired. Quiet is the usual state of a server nobody is attacking.
              </p>
            ) : (
              <RowList className="animate-rise">
                {data.alerts.slice(0, ALERTS_SHOWN).map((alert) => (
                  <AlertRow key={alert.id} alert={alert} />
                ))}
              </RowList>
            )}
          </PanelBody>
        </Panel>

        <Panel plain>
          <PanelHeader title="Bouncers" />
          <PanelBody flush>
            {enforcement && enforcement.bouncers.length > 0 ? (
              <EnforcementEvidence enforcement={enforcement} />
            ) : data.bouncers.length === 0 ? (
              <p className="py-3 text-body text-muted-foreground">
                None registered. Install one, such as{" "}
                <span className="font-mono">crowdsec-firewall-bouncer-nftables</span>, so the
                decisions are enforced.
              </p>
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
 * Each bouncer with what was read about it — what it enforces, when it last
 * pulled, whether its unit runs — and, under them, the firewall bouncer's
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
              <span className="inline-flex flex-wrap items-center gap-x-2">
                <Tag>{BOUNCER_KIND[bouncer.kind]}</Tag>
                {bouncer.unit && (
                  <span className="font-mono">
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

const BOUNCER_KIND = { firewall: "firewall", proxy: "proxy", other: "other" } as const

/** Whether a moment is within the last twenty-four hours, as the tile counts alerts. */
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

/** The flag a two-letter country code spells in regional indicators, where it is one. */
function flagOf(code?: string) {
  if (!code || !/^[A-Za-z]{2}$/.test(code)) return undefined
  return String.fromCodePoint(...[...code.toUpperCase()].map((c) => 0x1f1e6 + c.charCodeAt(0) - 65))
}

/** A country as the flag and its code, the code being what a reader can search for. */
function Country({ code }: { code?: string }) {
  const flag = flagOf(code)
  if (!flag || !code) return null
  return (
    <span className="inline-flex items-center gap-1" title={code.toUpperCase()}>
      <span aria-hidden>{flag}</span>
      <span>{code.toUpperCase()}</span>
    </span>
  )
}

/**
 * What a decision is about, drawn by its scope: an address as the network it
 * is on, a range as its first address with the prefix beside it, and a whole
 * country or autonomous system as the words they are.
 */
function DecisionValue({ decision }: { decision: Decision }) {
  const scope = decision.scope.toLowerCase()
  if (scope === "ip" || scope === "range") {
    const [base, bits] = decision.value.split("/")
    return (
      <span className="inline-flex min-w-0 items-baseline">
        <Address ip={base} className="text-body font-medium" />
        {bits && <span className="font-mono text-body font-medium">/{bits}</span>}
      </span>
    )
  }
  return (
    <span className="inline-flex items-center gap-2">
      <Tag>{decision.scope}</Tag>
      <span className="font-mono text-body font-medium">{decision.value}</span>
    </span>
  )
}

function DecisionsPanel({
  decisions,
  canManage,
  blocks,
  onChanged,
}: {
  decisions: Decision[]
  canManage: boolean
  blocks?: BlocksView
  onChanged: () => void
}) {
  const { confirm, dialog } = useConfirm()
  const [which, setWhich] = useState<"all" | "here" | "community">("all")
  const [all, setAll] = useState(false)
  const [banning, setBanning] = useState(false)

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
      <Panel>
        <PanelHeader
          title="Decisions"
          actions={
            canManage && (
              <Button size="xs" variant="outline" onClick={() => setBanning(true)}>
                <Slash aria-hidden />
                Ban an address
              </Button>
            )
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
            <p className="px-4 py-4 text-body text-muted-foreground">
              {decisions.length === 0
                ? "Nothing is blocked by CrowdSec right now."
                : "Nothing under this filter."}
            </p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Address</TableHead>
                  <TableHead>Scenario</TableHead>
                  <TableHead>Type</TableHead>
                  <TableHead>Until</TableHead>
                  {canManage && (
                    <TableHead className="w-px">
                      <span className="sr-only">Actions</span>
                    </TableHead>
                  )}
                </TableRow>
              </TableHeader>
              <TableBody>
                {rows.map((decision) => (
                  <TableRow key={decision.id}>
                    <TableCell className="py-3">
                      <DecisionValue decision={decision} />
                      {(decision.country || decision.as) && (
                        <span className="mt-1 flex flex-wrap items-center gap-x-2 text-hint text-muted-foreground">
                          <Country code={decision.country} />
                          {decision.as && <span className="truncate">{decision.as}</span>}
                        </span>
                      )}
                    </TableCell>
                    <TableCell className="py-3">
                      <span className="block max-w-72 truncate font-mono" title={decision.scenario}>
                        {decision.scenario}
                      </span>
                      <span className="mt-1 block text-hint text-muted-foreground">
                        {originWords(decision.origin)}
                      </span>
                    </TableCell>
                    <TableCell>
                      <Tag tone={decision.type === "ban" ? "danger" : "warning"}>
                        {decision.type}
                      </Tag>
                    </TableCell>
                    <TableCell className="numeric whitespace-nowrap text-muted-foreground">
                      {decision.until ? relativeTime(decision.until) : decision.duration}
                    </TableCell>
                    {canManage && (
                      <TableCell className="text-right">
                        <Button
                          size="xs"
                          variant="outline"
                          aria-label={`Release ${decision.value}`}
                          onClick={() => release(decision)}
                        >
                          Release
                        </Button>
                      </TableCell>
                    )}
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
          {shown.length > rows.length && (
            <div className="flex items-center justify-between gap-3 border-t border-hairline px-4 py-2.5">
              <span className="numeric text-hint text-muted-foreground">
                {rows.length} of {shown.length}
              </span>
              <Button size="xs" variant="ghost" onClick={() => setAll(true)}>
                Show all {shown.length}
              </Button>
            </div>
          )}
        </PanelBody>
      </Panel>

      <BanDialog open={banning} onOpenChange={setBanning} blocks={blocks} onBanned={onChanged} />
      {dialog}
    </>
  )
}

/** Hours, because that is the one unit the route reads for every length. */
const DURATIONS = [
  { value: "1h", label: "1 hour" },
  { value: "4h", label: "4 hours" },
  { value: "24h", label: "24 hours" },
  { value: "168h", label: "7 days" },
  { value: "720h", label: "30 days" },
]

/**
 * A ban by hand: an address or a range, for how long, and why. The reason is
 * what the decisions table shows as the scenario a month later, so it is worth
 * a few words. The server refuses a ban that covers this browser's own
 * address, and says so beside the field it is about rather than in a toast.
 */
function BanDialog({
  open,
  onOpenChange,
  blocks,
  onBanned,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  blocks?: BlocksView
  onBanned: () => void
}) {
  const { exposure } = useSecurity()
  const [value, setValue] = useState("")
  const [duration, setDuration] = useState("4h")
  const [reason, setReason] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<{ field: boolean; message: string }>()

  if (!open) return null

  const submit = async () => {
    setBusy(true)
    setError(undefined)
    try {
      await post("/security/crowdsec/decisions", {
        value: value.trim(),
        duration,
        reason: reason.trim() || undefined,
      })
      notify.success(
        `${value.trim()} banned for ${DURATIONS.find((d) => d.value === duration)?.label}`,
      )
      onBanned()
      onOpenChange(false)
    } catch (err) {
      const refusal = err instanceof ApiError ? err : undefined
      setError({
        field: refusal?.code === "would_lock_you_out" || refusal?.field === "value",
        message: err instanceof Error ? err.message : String(err),
      })
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal
      open={open}
      onOpenChange={onOpenChange}
      title="Ban an address"
      description="Adds a CrowdSec decision that its bouncers enforce until it expires."
      size="sm"
      footer={
        <>
          <Button variant="ghost" onClick={() => onOpenChange(false)} disabled={busy}>
            Cancel
          </Button>
          <Button type="submit" form="crowdsec-ban" pending={busy} disabled={busy || !value.trim()}>
            Ban
          </Button>
        </>
      }
    >
      <form
        id="crowdsec-ban"
        className="flex flex-col gap-4"
        onSubmit={(event) => {
          event.preventDefault()
          if (value.trim()) void submit()
        }}
      >
        <Field
          label="Address or range"
          htmlFor="crowdsec-value"
          hint={
            exposure?.client
              ? `One address or a range such as 203.0.113.0/24. It cannot cover yours, ${exposure.client}.`
              : "One address or a range such as 203.0.113.0/24."
          }
          error={error?.field ? error.message : undefined}
        >
          <Input
            id="crowdsec-value"
            aria-describedby="crowdsec-value-held"
            value={value}
            onChange={(event) => setValue(event.target.value)}
            placeholder="203.0.113.77"
            className="font-mono"
            autoComplete="off"
            spellCheck={false}
            aria-invalid={error?.field || undefined}
          />
          {heldSentence(value, blocks) && (
            <p id="crowdsec-value-held" className="mt-1.5 text-hint text-warning">
              {heldSentence(value, blocks)}
            </p>
          )}
        </Field>
        <Field label="For how long" htmlFor="crowdsec-duration">
          <Select value={duration} onValueChange={setDuration}>
            <SelectTrigger id="crowdsec-duration" className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {DURATIONS.map((option) => (
                <SelectItem key={option.value} value={option.value}>
                  {option.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>
        <Field
          label="Reason"
          htmlFor="crowdsec-reason"
          hint="Optional. It becomes the scenario the decisions table shows."
        >
          <Input
            id="crowdsec-reason"
            value={reason}
            onChange={(event) => setReason(event.target.value)}
            placeholder="scanning the admin panel"
            autoComplete="off"
          />
        </Field>
        {error && !error.field && (
          <p role="alert" className="animate-rise text-body text-destructive">
            {error.message}
          </p>
        )}
      </form>
    </Modal>
  )
}

/** An alert: the scenario that fired, who it fired on, and how much it saw. */
function AlertRow({ alert }: { alert: Alert }) {
  const source = alert.source.ip ?? alert.source.value
  return (
    <Row
      title={alert.scenario}
      subtitle={
        <span className="inline-flex min-w-0 items-center gap-x-3">
          {source && <Address ip={source} />}
          <Country code={alert.source.country} />
          {alert.source.asName && <span className="truncate">{alert.source.asName}</span>}
        </span>
      }
      className="py-2.5"
      trailing={
        <span className="flex flex-col items-end">
          <span className="numeric text-hint">{plural(alert.eventsCount, "event")}</span>
          <span className="text-hint text-muted-foreground">{relativeTime(alert.createdAt)}</span>
        </span>
      }
    />
  )
}
