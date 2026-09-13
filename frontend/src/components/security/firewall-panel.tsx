"use client"

import { useMemo, useState } from "react"
import {
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
import { SearchInput } from "@/components/page"
import { Panel, PanelBody, PanelFooter, PanelHeader, PanelToolbar } from "@/components/panel"
import { EmptyState, ErrorState, LoadingPanel, Notice } from "@/components/state"
import { AreaFindings } from "@/components/security/posture-panel"
import { AddRuleDialog, EditRuleDialog } from "@/components/security/rule-form"
import { Status } from "@/components/status-dot"
import { IconAction, RowActions } from "@/components/icon-action"
import { Tag } from "@/components/tag"
import { Label } from "@/components/ui/label"
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
 * The firewall, with the two controls that decide what it actually does.
 *
 * A rule list on its own is only half the picture: the default policy is what
 * happens to everything the list did not mention, and a list of allows in
 * front of a default of allow is decoration. The other half is logging — a
 * firewall that drops silently leaves an incident with no record of what was
 * refused, and "off" should be a choice somebody made rather than one they
 * inherited.
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
  const [editing, setEditing] = useState<FirewallRule | null>(null)
  const [query, setQuery] = useState("")
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

  if (loading) return <LoadingPanel />
  if (error) return <ErrorState error={error} />
  if (!status?.available) {
    return (
      <EmptyState
        icon={Shield}
        title="No firewall on this host"
        description={
          status?.error ??
          "Neither ufw, firewalld nor iptables answered. Install one — without it, every port anything on this machine opens is reachable from wherever the machine is."
        }
      />
    )
  }

  // Controls are keyed off what the backend says it can do rather than off its
  // name, so adding a fourth firewall does not mean revisiting this file.
  const caps = status.capabilities ?? {
    editable: false,
    toggle: false,
    defaultPolicy: false,
    logging: false,
    reset: false,
    profiles: false,
  }
  const writable = admin && caps.editable
  const hidden = status.rules.length - rules.length

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
      <div className="flex min-w-0 flex-col gap-4">
        <AreaFindings posture={posture} area="firewall" onFix={onFix} />

        {caps.readOnlyReason && (
          <Notice icon={LockClosed} title={`${status.backend} can be read here, not changed`}>
            {caps.readOnlyReason}
          </Notice>
        )}

        <Panel>
          <PanelHeader
            eyebrow={status.backend}
            title="Firewall"
            actions={
              <>
                <Status
                  verdict={status.enabled ? "ok" : "warning"}
                  label={status.enabled ? "Active" : "Inactive"}
                />
                {writable && (
                  <>
                    <AddRuleDialog onDone={refresh} hasProfiles={caps.profiles} />
                    {caps.toggle && (
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
                                ufw applies its default-deny policy immediately. If the port this
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
                  </>
                )}
              </>
            }
          />
          {/* One strip whether or not this firewall can be changed from here.
              Read-only backends (iptables) used to show none of this — just a
              "·"-joined string in the description — so the same facts now read
              the same way: the default policy and the logging level, and where
              the backend can take an instruction, the word becomes the control
              that sets it. Every field is one column of the same height, so a
              writable host and a read-only one have the same strip rather than
              a row of mismatched boxes. */}
          <PanelToolbar className="gap-x-5 gap-y-3">
            <PolicyField
              label="Inbound default"
              hint="A connection no rule matched"
              value={policyLabel(status.policy?.incoming, status.defaultPolicy)}
              options={POLICIES}
              fallback="allow"
              editable={caps.defaultPolicy && writable}
              onChange={(v) => setPolicy("incoming", v)}
            />
            {status.backend === "ufw" && (
              <PolicyField
                label="Outbound default"
                hint="What this host may reach"
                value={policyLabel(status.policy?.outgoing)}
                options={POLICIES}
                fallback="allow"
                editable={caps.defaultPolicy && writable}
                onChange={(v) => setPolicy("outgoing", v)}
              />
            )}
            <PolicyField
              label="Logging"
              hint={
                !status.logging || status.logging.startsWith("off")
                  ? "A silent drop leaves no record"
                  : "Refused connections are recorded"
              }
              value={loggingLevel(status.logging)}
              options={LOG_LEVELS}
              fallback="off"
              editable={caps.logging && writable}
              onChange={(level) =>
                post("/firewall/logging", { level })
                  .then(() => {
                    notify.success(`Logging set to ${level}`)
                    refresh()
                  })
                  .catch((err) => notify.error("Not applied", err))
              }
            />
            <span className="flex-1" />
            <SearchInput
              dense
              aria-label="Filter rules"
              placeholder="Filter rules"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              containerClassName="self-end sm:w-56"
            />
            {writable && caps.reset && (
              <Button
                size="sm"
                variant="ghost"
                className="self-end text-destructive"
                onClick={() =>
                  confirm({
                    title: "Reset the firewall",
                    phrase: "reset firewall",
                    confirmLabel: "Reset",
                    description: (
                      <p className="text-destructive">
                        Every rule is removed and ufw is disabled. There is no undo, and the host is
                        left unfiltered until you configure it again.
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
            <Table containerClassName="max-h-[calc(100svh-28rem)]">
              <TableHeader className={stickyTableHeader}>
                <TableRow>
                  <TableHead className="w-10">#</TableHead>
                  <TableHead>Action</TableHead>
                  <TableHead>To</TableHead>
                  <TableHead>From</TableHead>
                  <TableHead className="w-full">Comment</TableHead>
                  <TableHead className="w-px" />
                </TableRow>
              </TableHeader>
              <TableBody>
                {shown.map((rule, i) => (
                  <TableRow
                    key={`${rule.number}-${i}`}
                    className={cn("group", rule.danger && "bg-wash-danger")}
                  >
                    <TableCell className="numeric font-mono text-muted-foreground">
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
                          <span className="text-hint text-muted-foreground">{rule.direction}</span>
                        )}
                      </span>
                    </TableCell>
                    <TableCell>
                      <span className="font-mono">{rule.to || "—"}</span>
                      {rule.service && (
                        <span className="ml-1.5 text-muted-foreground">{rule.service}</span>
                      )}
                    </TableCell>
                    <TableCell className="font-mono">{rule.from || "anywhere"}</TableCell>
                    <TableCell className="text-muted-foreground">
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
                {shown.length === 0 && (
                  <TableRow>
                    <TableCell colSpan={6} className="p-0">
                      <EmptyState
                        icon={Shield}
                        title={rules.length === 0 ? "No rules configured" : "No rule matches"}
                        description={
                          rules.length === 0
                            ? undefined
                            : `Nothing in these ${rules.length} rules contains “${query.trim()}”.`
                        }
                        className="border-0"
                      />
                    </TableCell>
                  </TableRow>
                )}
              </TableBody>
            </Table>
          </PanelBody>
          {/* The guard, stated once and where it applies, instead of a
              permanent three-line banner above the page. It never changes and
              it is never the reason somebody opened this page, so it reads as
              a footnote to the rule list it protects. */}
          <PanelFooter className="text-hint leading-relaxed text-muted-foreground">
            <span className="min-w-0">
              A rule that would block the address you are connected from is refused before it is
              applied, and so is an inbound default of deny on a host with no allow rule at all.
              {hidden > 0 &&
                ` ${hidden} IPv6 ${hidden === 1 ? "counterpart is" : "counterparts are"} folded away — ufw writes each rule into both tables and prints them separately.`}
            </span>
          </PanelFooter>
        </Panel>
      </div>
      {dialog}
      {editing && (
        <EditRuleDialog
          rule={editing}
          open
          onOpenChange={(o) => !o && setEditing(null)}
          onDone={refresh}
          hasProfiles={caps.profiles}
        />
      )}
    </>
  )
}

const POLICIES = ["deny", "reject", "allow"]
const LOG_LEVELS = ["off", "low", "medium", "high", "full"]

/**
 * One decision in the firewall's strip: its name, its value and — where this
 * backend can be told to change it — the control that does.
 *
 * Read-only and writable render at the same height and on the same baseline,
 * which is what stopped the strip reading as three dropdowns and a stray
 * figure that had wandered in from somewhere else.
 */
function PolicyField({
  label,
  hint,
  value,
  options,
  fallback,
  editable,
  onChange,
}: {
  label: string
  hint: string
  value: string
  options: string[]
  /** ufw's verbose line is not one of the words the control offers. */
  fallback: string
  editable: boolean
  onChange: (value: string) => void
}) {
  return (
    <div className="min-w-0 space-y-1" title={hint}>
      <Label className="eyebrow">{label}</Label>
      {editable ? (
        <Select value={options.includes(value) ? value : fallback} onValueChange={onChange}>
          <SelectTrigger size="sm" className="w-32">
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
      ) : (
        <p className="numeric flex h-8 items-center text-body font-medium">{value}</p>
      )}
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

/** ufw prints "on (low)" or "off"; the control offers the level. */
function loggingLevel(logging: string | undefined) {
  if (!logging || logging.startsWith("off")) return "off"
  const match = logging.match(/\(([^)]+)\)/)
  return match ? match[1] : "low"
}
