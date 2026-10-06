"use client"

import { Workspace, WorkspaceHelp } from "@/components/workspace/workspace"
import { useFilterHistory } from "@/components/workspace/history"
import { useMemo, useState } from "react"
import { forgetSessionState, useSessionState } from "@/lib/view-state"
import {
  FirewallCheck,
  NetworkDevice,
  SecureConnection,
  LockClosed,
  Logs,
  Pencil,
  RotateCounterClockwise,
  Shield,
  Trash,
  Warning,
} from "@/components/icons"
import { notify } from "@/lib/toast"
import { del, post } from "@/lib/api"
import { cn } from "@/lib/utils"
import { lensFor } from "@/lib/log-lenses"
import type { FirewallRule, FirewallStatus, LogLine, Posture, SecurityFinding } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import { useConfirm } from "@/components/confirm-dialog"
import { Field } from "@/components/form"
import { Address } from "@/components/security/marks"
import { ProductLogo, portProduct } from "@/components/product-logo"
import { PageContext, SearchInput } from "@/components/page"
import { FactDot, HostIdentity } from "@/components/metrics/host-identity"
import { Panel, PanelBody, PanelFooter, PanelHeader, PanelToolbar } from "@/components/panel"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { EmptyNote, EmptyState, ErrorState, LoadingPanel, Notice } from "@/components/state"
import { AreaFindings } from "@/components/security/posture-panel"
import { AddRuleDialog, EditRuleDialog, type RuleHandoff } from "@/components/security/rule-form"
import { FirewallPicture } from "@/components/security/firewall-picture"
import { FIREWALL_LOG } from "@/components/security/host-logs"
import {
  HostLogSection,
  useAddressLineVerbs,
  useHostLog,
  useReadingPress,
} from "@/components/security/log-section"
import { ReadingTile, useLensReadings } from "@/components/logs/lens-readings"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { IconAction, DimActions } from "@/components/icon-action"
import { Button } from "@/components/ui/button"
import { Switch } from "@/components/ui/switch"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import {
  stickyTableHeader,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"

/**
 * Rules occupy the working column; the policy those rules fall through to stays beside them.
 *
 * Then what the rules did: the firewall's log, read through its lens — each drop and rate limit as
 * the address, the port and the protocol it was — with the day's counts as a second row of the
 * readings. A firewall that is not logging has no such log, and the section says so and points at
 * the control that changes it rather than drawing an empty pane.
 */
export function FirewallPanel({
  status,
  posture,
  loading,
  error,
  refresh,
  onFix,
  handoff,
}: {
  /** A rule a link into the page asked for, from the ports page's hand-offs. */
  handoff?: RuleHandoff
  status: FirewallStatus | undefined
  posture: Posture | undefined
  loading: boolean
  error: Error | undefined
  refresh: () => void
  onFix?: (finding: SecurityFinding) => void
}) {
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  const [editing, setEditing] = useSessionState<FirewallRule | null>(
    "security.firewall.editing",
    null,
  )
  const [filters, setFilters] = useFilterHistory("security.firewall.filters", { q: "" })
  const query = filters.q
  const setQuery = (q: string) => setFilters({ q })
  const admin = can("system.admin")
  const [handoffEdit, setHandoffEdit] = useState(handoff?.edit)

  // ufw and firewalld both say "off" in words; iptables says nothing, and its
  // LOG rules are the operator's own, so only a firewall that says so is
  // treated as not logging.
  const silent = Boolean(status?.logging) && loggingLevel(status?.logging) === "off"
  const firewallLog = useHostLog(FIREWALL_LOG, Boolean(status?.available) && !silent)
  const readings = useLensReadings(firewallLog.data?.id ?? "", FIREWALL_LENS, {
    forcedLens: "firewall",
    enabled: Boolean(firewallLog.data) && Boolean(status?.available) && !silent,
  })
  const [ask, press] = useReadingPress()
  // A drop is already the firewall's answer to the address on it, and an
  // allowed or audited connection may be the operator's own; a rate limit
  // is the one line where a deny is the next step. An outbound packet's
  // source is this host, so it gets no verbs at all.
  const lineVerbs = useAddressLineVerbs({
    comment: "blocked from the firewall log",
    onBlocked: refresh,
    blockOn: RATE_LIMITED,
    remote: arrived,
  })

  // ufw prints every rule twice on a dual-stack host and distinguishes the
  // pair only by a "(v6)" suffix. Folding the duplicate away is what keeps
  // eight rules from reading as sixteen.
  const rules = useMemo(() => (status?.rules ?? []).filter((r) => !r.ipv6), [status?.rules])
  const shown = useMemo(() => {
    const q = query.trim().toLowerCase()
    if (!q) return rules
    return rules.filter((r) =>
      [r.action, r.to, r.from, r.service, r.comment, r.direction, r.raw]
        .filter(Boolean)
        .join(" ")
        .toLowerCase()
        .includes(q),
    )
  }, [rules, query])

  // Controls are keyed off what the backend says it can do rather than off its
  // name, so adding a fourth firewall does not mean revisiting this file.
  const caps = status?.capabilities ?? {
    editable: false,
    toggle: false,
    defaultPolicy: false,
    logging: false,
    reset: false,
    profiles: false,
  }
  const writable = admin && Boolean(status?.available) && caps.editable
  // The rule is looked up by number and opened only while it still names the
  // port the link was about: ufw renumbers on every delete, and the form
  // would otherwise replace whichever rule took the number since.
  const handoffRule =
    handoffEdit && writable
      ? rules.find((r) => r.number === handoffEdit.number && r.port === handoffEdit.port)
      : undefined

  const header = <PageContext eyebrow="Security" title="Firewall" />
  // Whether the firewall is enforcing, and the switch that decides it, at the
  // right end of the identity line: the control beside the fact it changes.
  // The page's shortcuts sit there too, rather than on a line of their own
  // above the page with nothing else on it.
  const enforcing = status?.available && (
    <span className="flex flex-wrap items-center gap-3">
      <WorkspaceHelp compact />
      <Status
        verdict={status.enabled ? "ok" : "warning"}
        label={status.enabled ? "Active" : "Inactive"}
        className="text-body"
      />
      {writable && caps.toggle && (
        <Switch
          aria-label="Firewall enabled"
          checked={status.enabled}
          onCheckedChange={(enabled) =>
            confirm({
              title: enabled ? "Enable firewall" : "Disable firewall",
              confirmLabel: enabled ? "Enable" : "Disable",
              description: enabled ? (
                <p className="text-destructive">
                  {status.backend} applies its default-deny policy immediately. If the port this
                  dashboard listens on is not already allowed, you will lose access.
                </p>
              ) : (
                <p className="text-destructive">
                  Every rule stops being enforced and the host is left unfiltered.
                </p>
              ),
              action: async (c) => {
                await post("/firewall/enabled", { enabled }, { confirm: c })
                refresh()
              },
            })
          }
        />
      )}
    </span>
  )

  if (loading && !status) {
    return (
      <>
        {header}
        <LoadingPanel />
      </>
    )
  }
  if (error && !status) {
    return (
      <>
        {header}
        <ErrorState error={error} />
      </>
    )
  }
  if (!status?.available) {
    return (
      <>
        {header}
        <EmptyState
          icon={Shield}
          title="No firewall on this host"
          description={
            status?.error ??
            "Neither ufw, firewalld nor iptables answered. Install one — without it, every port anything on this machine opens is reachable from wherever the machine is."
          }
        />
      </>
    )
  }

  const hidden = status.rules.length - rules.length
  const allows = rules.filter((r) => r.action === "ALLOW" || r.action === "LIMIT").length
  const dangerous = rules.filter((r) => r.danger).length
  const inbound = policyLabel(status.policy?.incoming, status.defaultPolicy)
  const logging = loggingLevel(status.logging)

  const setPolicy = (direction: string, policy: string) => {
    const risky = direction === "incoming" && policy !== "allow"
    const send = (c?: string) =>
      post("/firewall/policy", { direction, policy }, c ? { confirm: c } : undefined)
    if (!risky) {
      send()
        .then(() => {
          notify.success(`Default ${direction} set to ${policy}`)
          refresh()
        })
        .catch((err) => notify.error("Not applied", err))
      return
    }
    confirm({
      title: "Deny inbound by default",
      confirmLabel: "Apply",
      description: (
        <p className="text-destructive">
          Everything not covered by an allow rule stops being reachable, including this dashboard if
          no rule admits the port you are reading it on. The server refuses the change outright when
          there is no inbound allow rule at all.
        </p>
      ),
      action: async (c) => {
        await send(c)
        refresh()
      },
    })
  }

  return (
    <Workspace
      name="Firewall"
      openItems={false}
      refresh={refresh}
      escape={() => {
        if (!query) return false
        setQuery("")
        return true
      }}
    >
      {header}

      {/* What the page is about, as its own row (§15 pass 8): the backend by
          its own name, on the tile a product's mark takes — none of the three
          has one, so it keeps the section's glyph — with whether it can be
          told anything from here and, on firewalld, the zone its rules are
          held in. */}
      <HostIdentity
        fallback={FirewallCheck}
        title={status.backend}
        facts={
          <>
            <span>{caps.editable ? "managed from here" : "read only from here"}</span>
            {status.zone && (
              <>
                <FactDot />
                <span>
                  zone <span className="font-mono text-foreground">{status.zone}</span>
                </span>
              </>
            )}
            <FactDot />
            <span className="numeric">
              <span style={{ color: ACTION_HUE.ALLOW }}>{allows} allow</span> ·{" "}
              <span style={{ color: ACTION_HUE.DENY }}>{rules.length - allows} deny</span>
            </span>
            {hidden > 0 && (
              <>
                <FactDot />
                <span className="numeric">
                  {hidden} IPv6 twin{hidden === 1 ? "" : "s"} folded away
                </span>
              </>
            )}
          </>
        }
        aside={enforcing}
      />

      {/* The rules folded by where they lead, before the rules in the order
          the firewall reads them: what can reach this machine is the
          question the page is opened with. */}
      <Panel plain>
        <PanelHeader title="Inbound" />
        <PanelBody>
          <FirewallPicture status={status} />
        </PanelBody>
      </Panel>

      {caps.readOnlyReason && (
        <Notice icon={LockClosed} title={`${status.backend} can be read here, not changed`}>
          {caps.readOnlyReason}
        </Notice>
      )}

      {/* The four readings the rule list is an explanation of. Read-only
          backends (iptables) used to show none of this — a "·"-joined string
          in a description — so the same facts now read the same way on every
          host. Amber only where the reading is the finding: an inbound default
          of allow makes the deny rules a blocklist rather than a fence. Under
          them, what the log says the rules did over the last day, in the same
          grid — and not while logging is off, when every one of them would be
          a zero that means "not recorded" rather than "nothing happened".
          Two-up on a phone: seven short figures one-up are a screen and a
          half before the controls they describe. Seven across four columns
          let the last reading take the row's end rather than leave a hole
          beside it, as the phone's odd one does. */}
      <StatGrid columns={4} dense className="xl:[&>*:last-child:nth-child(4n+3)]:col-span-2">
        <StatTile
          label="Rules"
          value={rules.length}
          tone={dangerous > 0 ? "danger" : "default"}
          hint={
            dangerous > 0
              ? `${dangerous} open${dangerous === 1 ? "s" : ""} a sensitive port to everyone`
              : rules.length === 0
                ? "everything is answered by the default"
                : "one line each, read in order"
          }
        />
        <StatTile
          label="Inbound default"
          value={inbound}
          tone={inbound === "allow" ? "warning" : "default"}
          hint={
            inbound === "allow"
              ? "the deny rules are a blocklist, not a fence"
              : "a connection no rule matched"
          }
        />
        {status.backend === "ufw" ? (
          <StatTile
            label="Outbound default"
            value={policyLabel(status.policy?.outgoing)}
            hint="what this host may reach"
          />
        ) : status.zone ? (
          <StatTile label="Zone" value={status.zone} hint="where the rules are held" />
        ) : (
          <StatTile
            label="Backend"
            value={status.backend}
            hint={caps.editable ? "managed from here" : "read only from here"}
          />
        )}
        {/* iptables names no level: what it logs is whatever its LOG rules
            write, which "off" would misstate beside the drops counted next
            to it. */}
        <StatTile
          label="Logging"
          value={status.logging ? logging : "—"}
          hint={
            !status.logging
              ? "set by its own LOG rules"
              : logging === "off"
                ? "a silent drop leaves no record"
                : "every drop is recorded"
          }
        />
        {!silent &&
          readings.tiles.map((tile) => (
            <ReadingTile
              key={tile.reading.id}
              tile={tile}
              window={readings.window}
              onPick={() => press(tile.reading)}
            />
          ))}
      </StatGrid>

      <AreaFindings posture={posture} area="firewall" onFix={onFix} />

      <div
        className={cn(
          "grid min-w-0 items-start gap-6",
          writable && (caps.defaultPolicy || caps.logging) && "xl:grid-cols-[minmax(0,1fr)_15rem]",
        )}
      >
        <Panel>
          <PanelHeader
            title="Rules"
            actions={
              writable && (
                <AddRuleDialog
                  onDone={refresh}
                  hasProfiles={caps.profiles}
                  arrival={handoff?.add}
                />
              )
            }
          />
          <PanelToolbar>
            <SearchInput
              dense
              data-workspace-search
              aria-label="Filter rules"
              placeholder="Filter rules"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              containerClassName="sm:w-64"
            />
            <span className="flex-1" />
            {writable && caps.reset && (
              <Button
                size="sm"
                variant="ghost"
                className="text-destructive"
                onClick={() =>
                  confirm({
                    title: "Reset the firewall",
                    confirmLabel: "Reset",
                    description: (
                      <p className="text-destructive">
                        Every rule is removed and {status.backend} is disabled. There is no undo,
                        and the host is left unfiltered until you configure it again.
                      </p>
                    ),
                    action: async (c) => {
                      await post("/firewall/reset", {}, { confirm: c })
                      refresh()
                    },
                  })
                }
              >
                <RotateCounterClockwise className="size-3.5" />
                Reset
              </Button>
            )}
          </PanelToolbar>
          <PanelBody flush>
            {rules.length === 0 ? (
              <EmptyState
                icon={Shield}
                title="No rules configured"
                description={
                  status.enabled
                    ? `Everything is answered by the inbound default of ${inbound}.`
                    : undefined
                }
                className="mt-4"
              />
            ) : shown.length === 0 ? (
              <EmptyNote>
                Nothing in these {rules.length} rules contains &ldquo;{query.trim()}&rdquo;.
              </EmptyNote>
            ) : (
              <div className="min-w-0 group-data-[plain]/panel:-mx-4">
                <Table containerClassName="max-h-[36rem]">
                  <TableHeader className={stickyTableHeader}>
                    <TableRow>
                      <TableHead>Rule</TableHead>
                      <TableHead className="w-full">Destination</TableHead>
                      <TableHead>Source</TableHead>
                      <TableHead className="w-px">
                        <span className="sr-only">Actions</span>
                      </TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {shown.map((rule, i) => (
                      <TableRow
                        key={`${rule.number}-${i}`}
                        data-workspace-item={String(rule.number)}
                        data-workspace-name={`${rule.to} ${rule.comment}`}
                        tabIndex={0}
                        className="group focus-ring-inset"
                      >
                        <TableCell className="relative py-4">
                          {/* The action's hue down the row's edge, so a
                              column of thirty rules reads as where the
                              fence opens and where it shuts before a word
                              of it is. */}
                          <span
                            aria-hidden
                            className="absolute inset-y-3 left-0 w-0.5 rounded-full"
                            style={{ background: actionHue(rule.action) }}
                          />
                          <div className="space-y-1.5">
                            <Tag style={{ color: actionHue(rule.action) }}>{rule.action}</Tag>
                            <span className="block font-mono text-hint text-muted-foreground">
                              #{rule.number ?? i + 1} · {rule.direction || "IN"}
                            </span>
                          </div>
                        </TableCell>
                        <TableCell className="min-w-40 whitespace-normal">
                          <div className="flex items-center gap-3">
                            <ProductLogo
                              id={portProduct(Number(rule.port))}
                              fallback={rule.port === "22" ? SecureConnection : NetworkDevice}
                              size="sm"
                              className="hidden sm:flex"
                            />
                            <div className="min-w-0 space-y-1">
                              <span className="block text-body font-medium">
                                {rule.service || <Destination to={rule.to} />}
                              </span>
                              {(rule.service || rule.comment) && (
                                <span className="block font-mono text-hint break-words text-muted-foreground">
                                  {rule.service ? <Destination to={rule.to} /> : rule.comment}
                                </span>
                              )}
                              {rule.service && rule.comment && (
                                <span className="block text-hint text-muted-foreground">
                                  {rule.comment}
                                </span>
                              )}
                              {rule.danger && (
                                <span className="flex items-start gap-1 text-hint text-destructive">
                                  <Warning aria-hidden className="mt-px size-3 shrink-0" />
                                  {rule.danger}
                                </span>
                              )}
                            </div>
                          </div>
                        </TableCell>
                        <TableCell>
                          <Address ip={rule.from || "Anywhere"} />
                        </TableCell>
                        <TableCell className="whitespace-nowrap">
                          <DimActions className="justify-end">
                            {/* A forwarding rule (ufw route, which is what
                              ufw-docker writes) has no representation in this
                              form: it is neither inbound nor outbound, and
                              reopening it here would offer to save it back as an
                              inbound rule. The server refuses that too; not
                              offering the button is the half the reader can
                              see. */}
                            {writable && rule.number !== undefined && rule.direction !== "FWD" && (
                              <IconAction label="Edit rule" onClick={() => setEditing(rule)}>
                                <Pencil />
                              </IconAction>
                            )}
                            {writable && rule.number !== undefined && (
                              <IconAction
                                label="Delete rule"
                                className="text-destructive"
                                onClick={() =>
                                  confirm({
                                    title: "Delete firewall rule",
                                    confirmLabel: "Delete",
                                    description: <p className="font-mono text-xs">{rule.raw}</p>,
                                    action: async (c) => {
                                      await del(`/firewall/rules/${rule.number}`, { confirm: c })
                                      refresh()
                                    },
                                  })
                                }
                              >
                                <Trash />
                              </IconAction>
                            )}
                          </DimActions>
                        </TableCell>
                      </TableRow>
                    ))}
                    {/* Where a connection goes that no rule above matched:
                        the end of the list the firewall reads, so it is the
                        list's last row rather than a tile a screen away. */}
                    {!query.trim() && (
                      <TableRow className="hover:bg-transparent">
                        <TableCell className="relative py-4">
                          <span
                            aria-hidden
                            className="absolute inset-y-3 left-0 w-0.5 rounded-full border-l border-dashed border-border-strong"
                          />
                          <div className="space-y-1.5">
                            <Tag style={{ color: actionHue(inbound) }}>{inbound}</Tag>
                            <span className="block font-mono text-hint text-muted-foreground">
                              default · IN
                            </span>
                          </div>
                        </TableCell>
                        <TableCell className="whitespace-normal">
                          <div className="flex items-center gap-3">
                            <ProductLogo fallback={Shield} size="sm" className="hidden sm:flex" />
                            <div className="min-w-0 space-y-1">
                              <span className="block text-body font-medium">Everything else</span>
                              <span
                                className={cn(
                                  "block text-hint",
                                  inbound === "allow" ? "text-warning" : "text-muted-foreground",
                                )}
                              >
                                {inbound === "allow"
                                  ? "let in: the deny rules above are a blocklist, not a fence"
                                  : "a connection no rule above matched"}
                              </span>
                            </div>
                          </div>
                        </TableCell>
                        <TableCell>
                          <span className="text-muted-foreground">Anyone</span>
                        </TableCell>
                        <TableCell />
                      </TableRow>
                    )}
                  </TableBody>
                </Table>
              </div>
            )}
          </PanelBody>
          {/* The guard, stated once and where it applies, instead of a
            permanent banner above the page. It never changes and it is never
            the reason somebody opened this page, so it reads as a footnote to
            the rule list it protects. */}
          {writable && (
            <PanelFooter className="text-hint leading-relaxed text-muted-foreground">
              <span className="min-w-0">
                Your current address is protected from lockout. Address-specific deny rules take
                priority over allow rules.
              </span>
            </PanelFooter>
          )}
        </Panel>

        {writable && (caps.defaultPolicy || caps.logging) && (
          <Panel plain className="xl:sticky xl:top-6">
            <PanelHeader title="Defaults" />
            <PanelBody className="grid gap-5 sm:grid-cols-3 xl:grid-cols-1">
              {caps.defaultPolicy && (
                <PolicyField
                  label="Inbound"
                  hint="A connection no rule matched"
                  value={inbound}
                  options={POLICIES}
                  fallback="allow"
                  onChange={(v) => setPolicy("incoming", v)}
                />
              )}
              {caps.defaultPolicy && status.backend === "ufw" && (
                <PolicyField
                  label="Outbound"
                  hint="What this host may reach"
                  value={policyLabel(status.policy?.outgoing)}
                  options={POLICIES}
                  fallback="allow"
                  onChange={(v) => setPolicy("outgoing", v)}
                />
              )}
              {caps.logging && (
                <PolicyField
                  id={LOGGING_CONTROL}
                  label="Logging"
                  hint="How much is written about what was dropped"
                  value={logging}
                  options={status.backend === "firewalld" ? FIREWALLD_LOG_LEVELS : LOG_LEVELS}
                  fallback="off"
                  onChange={(level) =>
                    post("/firewall/logging", { level })
                      .then(() => {
                        notify.success(`Logging set to ${level}`)
                        refresh()
                      })
                      .catch((err) => notify.error("Not applied", err))
                  }
                />
              )}
            </PanelBody>
          </Panel>
        )}
      </div>

      <HostLogSection
        title="Firewall log"
        log={firewallLog}
        storageKey="security.firewall.log"
        ask={ask}
        readings={readings}
        instead={
          silent && (
            <EmptyState
              icon={Logs}
              title={`${status.backend} is not logging`}
              description={`A refused connection leaves no record while logging is off, so there is nothing here to read. ${
                writable && caps.logging
                  ? "Set a level under Defaults — low records every blocked packet and every rate limit."
                  : caps.editable && caps.logging
                    ? "An administrator can set a level under Defaults on this page."
                    : `It cannot be set from here; on the host, ${LOGGING_COMMAND[status.backend] ?? `${status.backend}'s own logging setting`} turns it on.`
              }`}
              action={
                writable &&
                caps.logging && (
                  <Button size="sm" variant="outline" onClick={pointAtLogging}>
                    Choose a logging level
                  </Button>
                )
              }
            />
          )
        }
        lineVerbs={lineVerbs}
      />

      {dialog}
      {handoffEdit && writable && !handoffRule && (
        <Notice
          tone="warning"
          icon={Warning}
          title={`Rule ${handoffEdit.number} no longer names port ${handoffEdit.port}`}
        >
          <p>The rules have changed since the link was made, so nothing was opened.</p>
          <Button
            variant="outline"
            size="xs"
            className="mt-2"
            onClick={() => setHandoffEdit(undefined)}
          >
            Dismiss
          </Button>
        </Notice>
      )}
      {handoffRule && handoffEdit && (
        <EditRuleDialog
          rule={handoffRule}
          open
          arrival={handoffEdit.fields}
          onOpenChange={(o) => {
            if (o) return
            setHandoffEdit(undefined)
            forgetSessionState("security.firewall.rule.")
          }}
          onDone={refresh}
          hasProfiles={caps.profiles}
        />
      )}
      {editing && !handoffRule && (
        <EditRuleDialog
          rule={editing}
          open
          onOpenChange={(o) => {
            if (o) return
            setEditing(null)
            forgetSessionState("security.firewall.rule.")
          }}
          onDone={refresh}
          hasProfiles={caps.profiles}
        />
      )}
    </Workspace>
  )
}

const FIREWALL_LENS = lensFor("firewall")

/**
 * An action's hue: a kind of rule, named by colour the way a Redis key's type
 * is (§15) — admitted in green, rate-limited in amber, turned away in red,
 * refused with an answer in pink. The three backends spell the same four
 * actions three ways.
 */
const ACTION_HUE = {
  ALLOW: "var(--tag-green)",
  LIMIT: "var(--tag-amber)",
  DENY: "var(--tag-red)",
  REJECT: "var(--tag-pink)",
} as const

function actionHue(action: string) {
  const a = action.toUpperCase()
  if (a === "ALLOW" || a === "ACCEPT") return ACTION_HUE.ALLOW
  if (a === "LIMIT") return ACTION_HUE.LIMIT
  if (a === "DENY" || a === "DROP") return ACTION_HUE.DENY
  if (a === "REJECT") return ACTION_HUE.REJECT
  return "var(--tag-slate)"
}

/** A rule's destination, its port in the port hue: `22/tcp`, `Anywhere`, `10.0.0.5 443`. */
function Destination({ to }: { to: string }) {
  const match = /^(\d+(?::\d+)?)(\/\w+)?$/.exec(to)
  if (!match) return <>{to || "Any destination"}</>
  return (
    <span className="numeric font-mono">
      <span className="text-[var(--tag-pink)]">{match[1]}</span>
      {match[2] && <span className="text-muted-foreground">{match[2]}</span>}
    </span>
  )
}

/** The lines a deny answers: a connection a limit rule turned away. */
const RATE_LIMITED = ["limit"]

/** A packet that arrived: ufw, firewalld and iptables all write `IN=` empty for one this host sent. */
function arrived(line: LogLine) {
  return /\bIN=\S/.test(line.text)
}

/** How each backend is told to log, for a reader this page cannot set it for. */
const LOGGING_COMMAND: Record<string, string> = {
  ufw: "ufw logging low",
  firewalld: "firewall-cmd --set-log-denied=all",
}

/** The logging control's id, which the log section's empty state brings into view. */
const LOGGING_CONTROL = "firewall-logging-level"

function pointAtLogging() {
  const control = document.getElementById(LOGGING_CONTROL)
  control?.scrollIntoView({ block: "center" })
  control?.focus({ preventScroll: true })
}

const POLICIES = ["deny", "reject", "allow"]
const LOG_LEVELS = ["off", "low", "medium", "high", "full"]
// firewalld distinguishes three levels, not five; offering ufw's ladder would
// let somebody pick "medium" and read back "full".
const FIREWALLD_LOG_LEVELS = ["off", "low", "full"]

/**
 * One default in the block under the tiles: its name, and the control that
 * sets it. Only drawn where the backend can take the instruction, so a
 * read-only host has the tiles and nothing that looks like a dead control.
 */
function PolicyField({
  id,
  label,
  hint,
  value,
  options,
  fallback,
  onChange,
}: {
  id?: string
  label: string
  hint: string
  value: string
  options: string[]
  /** ufw's verbose line is not always one of the words the control offers. */
  fallback: string
  onChange: (value: string) => void
}) {
  return (
    <Field label={label} hint={hint}>
      <Select value={options.includes(value) ? value : fallback} onValueChange={onChange}>
        <SelectTrigger id={id} size="sm" className="w-full" aria-label={`${label} default`}>
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {options.map((option) => (
            <SelectItem key={option} value={option}>
              {option}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </Field>
  )
}

/**
 * The default policy as a word. Prefer the structured field the write controls
 * also read, so a read-only backend and a writable one describe the same thing
 * the same way; fall back to the raw string ufw's verbose output carries.
 */
function policyLabel(structured: string | undefined, raw?: string) {
  return structured || raw || "—"
}

/**
 * The logging level as one of the words the control offers. ufw prints "on
 * (low)" or "off"; firewalld prints its own three — "unicast" is what its
 * "low" writes and "all" what anything higher does.
 */
function loggingLevel(logging: string | undefined) {
  if (!logging || logging.startsWith("off")) return "off"
  if (logging === "unicast") return "low"
  if (logging === "all" || logging === "broadcast" || logging === "multicast") return "full"
  const match = logging.match(/\(([^)]+)\)/)
  return match ? match[1] : "low"
}
