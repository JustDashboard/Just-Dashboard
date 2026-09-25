"use client"

import { useMemo } from "react"
import { forgetSessionState, useSessionState } from "@/lib/view-state"
import {
  FirewallCheck,
  LockClosed,
  Pencil,
  RotateCounterClockwise,
  Shield,
  Trash,
  Warning,
} from "@/components/icons"
import { notify } from "@/lib/toast"
import { del, post } from "@/lib/api"
import { cn } from "@/lib/utils"
import type { FirewallRule, FirewallStatus, Posture, SecurityFinding } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import { useConfirm } from "@/components/confirm-dialog"
import { PageContext, SearchInput } from "@/components/page"
import { FactDot, HostIdentity } from "@/components/metrics/host-identity"
import { Panel, PanelBody, PanelFooter, PanelHeader, PanelToolbar } from "@/components/panel"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { EmptyNote, EmptyState, ErrorState, LoadingPanel, Notice } from "@/components/state"
import { AreaFindings } from "@/components/security/posture-panel"
import { AddRuleDialog, EditRuleDialog } from "@/components/security/rule-form"
import { Status } from "@/components/status-dot"
import { IconAction, RowActions } from "@/components/icon-action"
import { Tag } from "@/components/tag"
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
 * The firewall, with the two settings that decide what its rules mean.
 *
 * A rule list on its own is only half the picture: the default policy is what
 * happens to everything the list did not mention, and a list of allows in
 * front of a default of allow is decoration. The other half is logging — a
 * firewall that drops silently leaves an incident with no record of what was
 * refused, and "off" should be a choice somebody made rather than inherited.
 *
 * So the page opens on the firewall itself — its backend named in one
 * identity line, the way the host Overview names the machine, with whether it
 * is enforcing and the switch that decides that at the line's right end —
 * then on those two settings as readings, four tiles on the page's own
 * ground, and offers the controls that set them, where this backend can be
 * told to, in one plain block under the tiles. The rules follow, as a table
 * that bleeds to the page edge, with the command that adds one in its header.
 */
export function FirewallPanel({
  status,
  posture,
  loading,
  error,
  refresh,
  onFix,
}: {
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
  const [query, setQuery] = useSessionState("security.firewall.query", "")
  const admin = can("system.admin")

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

  const header = <PageContext eyebrow="Security" title="Firewall" />
  // Whether the firewall is enforcing, and the switch that decides it, at the
  // right end of the identity line: the control beside the fact it changes.
  const enforcing = status?.available && (
    <span className="flex flex-wrap items-center gap-3">
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
              phrase: enabled ? "enable firewall" : "disable firewall",
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
      phrase: "deny incoming",
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
    <>
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
              {allows} allow · {rules.length - allows} deny
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

      {caps.readOnlyReason && (
        <Notice icon={LockClosed} title={`${status.backend} can be read here, not changed`}>
          {caps.readOnlyReason}
        </Notice>
      )}

      {/* The four readings the rule list is an explanation of. Read-only
          backends (iptables) used to show none of this — a "·"-joined string
          in a description — so the same facts now read the same way on every
          host. Amber only where the reading is the finding: an inbound default
          of allow makes the deny rules a blocklist rather than a fence. */}
      <StatGrid columns={4}>
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
        <StatTile
          label="Logging"
          value={logging}
          hint={
            logging === "off"
              ? "a silent drop leaves no record"
              : "refused connections are recorded"
          }
        />
      </StatGrid>

      <AreaFindings posture={posture} area="firewall" onFix={onFix} />

      {/* The controls for the two settings above, only where this backend can
          take an instruction. Read-only hosts have the readings and no
          duplicate row of dead controls under them. */}
      {writable && (caps.defaultPolicy || caps.logging) && (
        <Panel plain>
          <PanelHeader title="Defaults" />
          <PanelBody className="flex flex-wrap gap-x-8 gap-y-3">
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

      <Panel>
        <PanelHeader
          title="Rules"
          actions={writable && <AddRuleDialog onDone={refresh} hasProfiles={caps.profiles} />}
        />
        <PanelToolbar>
          <SearchInput
            dense
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
                  phrase: "reset firewall",
                  confirmLabel: "Reset",
                  description: (
                    <p className="text-destructive">
                      Every rule is removed and {status.backend} is disabled. There is no undo, and
                      the host is left unfiltered until you configure it again.
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
              <Table containerClassName="max-h-[calc(100svh-26rem)]">
                <TableHeader className={stickyTableHeader}>
                  <TableRow>
                    <TableHead className="hidden w-10 sm:table-cell">#</TableHead>
                    <TableHead>Action</TableHead>
                    <TableHead>To</TableHead>
                    <TableHead>From</TableHead>
                    <TableHead className="hidden w-full md:table-cell">Comment</TableHead>
                    <TableHead className="w-px" />
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {shown.map((rule, i) => (
                    <TableRow
                      key={`${rule.number}-${i}`}
                      className={cn("group", rule.danger && "bg-wash-danger")}
                    >
                      <TableCell className="numeric hidden font-mono text-muted-foreground sm:table-cell">
                        {rule.number}
                      </TableCell>
                      <TableCell>
                        <span className="flex items-center gap-1.5">
                          <Tag
                            mono
                            tone={rule.danger ? "danger" : "default"}
                            className={cn(
                              // ALLOW / DENY / LIMIT is a property of the rule,
                              // not the app's running/stopped status language, so
                              // it stays a plain tag. Deny-family rules read
                              // quieter — an allow to the world is the line worth
                              // catching, and rule.danger already tints that row.
                              !rule.danger && rule.action === "ALLOW" && "text-foreground",
                            )}
                          >
                            {rule.action}
                          </Tag>
                          {rule.direction && (
                            <span className="text-hint text-muted-foreground">
                              {rule.direction}
                            </span>
                          )}
                        </span>
                      </TableCell>
                      <TableCell>
                        <span className="font-mono">{rule.to || "—"}</span>
                        {rule.service && (
                          <span className="ml-1.5 text-muted-foreground">{rule.service}</span>
                        )}
                        {rule.danger && (
                          <span className="mt-0.5 flex items-start gap-1 text-hint whitespace-normal text-destructive md:hidden">
                            <Warning className="mt-px size-3 shrink-0" />
                            <span>{rule.danger}</span>
                          </span>
                        )}
                      </TableCell>
                      <TableCell className="font-mono">{rule.from || "anywhere"}</TableCell>
                      <TableCell className="hidden text-muted-foreground md:table-cell">
                        {rule.danger ? (
                          <span className="flex items-start gap-1.5 whitespace-normal text-destructive">
                            <Warning className="mt-px size-3 shrink-0" />
                            <span>{rule.danger}</span>
                          </span>
                        ) : (
                          rule.comment
                        )}
                      </TableCell>
                      <TableCell className="whitespace-nowrap">
                        <RowActions className="justify-end">
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
                                  // No typed phrase. A rule is one line, visible
                                  // on the row being deleted and re-addable from
                                  // the form beside it — and a phrase in front of
                                  // something done a dozen times a day is a
                                  // phrase that gets typed without being read,
                                  // which is what makes the phrase worthless on
                                  // the routes that keep it.
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
                        </RowActions>
                      </TableCell>
                    </TableRow>
                  ))}
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
              A rule that would block the address you are connected from is refused before it is
              applied, and so is an inbound default of deny on a host with no allow rule at all. A
              deny written for one address goes in front of the rules it would otherwise sit behind.
            </span>
          </PanelFooter>
        )}
      </Panel>

      {dialog}
      {editing && (
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
    </>
  )
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
  label,
  hint,
  value,
  options,
  fallback,
  onChange,
}: {
  label: string
  hint: string
  value: string
  options: string[]
  /** ufw's verbose line is not always one of the words the control offers. */
  fallback: string
  onChange: (value: string) => void
}) {
  return (
    <div className="min-w-0 space-y-1" title={hint}>
      <p className="eyebrow">{label}</p>
      <Select value={options.includes(value) ? value : fallback} onValueChange={onChange}>
        <SelectTrigger size="sm" className="w-32" aria-label={`${label} default`}>
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
    </div>
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
