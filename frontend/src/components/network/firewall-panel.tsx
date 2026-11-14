"use client"

import { Workspace, WorkspaceHelp } from "@/components/workspace/workspace"
import { useFilterHistory } from "@/components/workspace/history"
import { useEffect, useMemo, useRef, useState } from "react"
import { forgetSessionState, useSessionState } from "@/lib/view-state"
import {
  FirewallCheck,
  ListOrdered,
  NetworkDevice,
  SecureConnection,
  LockClosed,
  Logs,
  Pencil,
  RefreshClockwise,
  RotateCounterClockwise,
  Shield,
  Trash,
  Warning,
} from "@/components/icons"
import { notify } from "@/lib/toast"
import { del, post } from "@/lib/api"
import { cn } from "@/lib/utils"
import { lensFor, type LensReading } from "@/lib/log-lenses"
import type { FirewallRule, FirewallStatus, LogLine } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import { ConfirmDialog, useConfirm, type ConfirmRequest } from "@/components/confirm-dialog"
import { InfoTip } from "@/components/form"
import { Address } from "@/components/security/marks"
import { ProductLogo, ProductLogos, portProduct } from "@/components/product-logo"
import { PageContext, SearchInput } from "@/components/page"
import { FactDot, HostIdentity } from "@/components/metrics/host-identity"
import { Panel, PanelBody, PanelHeader, PanelToolbar } from "@/components/panel"
import { EmptyNote, EmptyState, ErrorState, Notice } from "@/components/state"
import { AddRuleDialog, EditRuleDialog, type RuleHandoff } from "@/components/security/rule-form"
import { NetworkReadWarning } from "@/components/network/read-warning"
import {
  FirewallPicture,
  FirewallPictureLoading,
  firewallProduct,
  type FirewallCounts,
} from "@/components/network/firewall-picture"
import { InstallHandoff, usePackageKnown } from "@/components/network/install"
import {
  FirewallAccessPanel,
  FirewallEnforcement,
  FirewallPreflightCheck,
} from "@/components/network/firewall-access"
import {
  FirewallHistoryPanel,
  FirewallPlanTray,
  RuleHistoryPopover,
} from "@/components/network/firewall-changes"
import { findingsByRule, rulePath, type StagedChange } from "@/components/network/firewall-reading"
import { FIREWALL_LOG } from "@/components/security/host-logs"
import {
  HostLogSection,
  useAddressLineVerbs,
  useHostLog,
  useReadingPress,
} from "@/components/security/log-section"
import { useLensReadings, type LensReadingsState } from "@/components/logs/lens-readings"
import { SaveBarProvider } from "@/components/deploy/settings/save-bar"
import { Segments } from "@/components/deploy/settings/segments"
import {
  SettingForm,
  SettingSection,
  settingStatus,
} from "@/components/deploy/settings/setting-card"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { IconAction, DimActions } from "@/components/icon-action"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { Switch } from "@/components/ui/switch"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { VerbMenu, type Verb } from "@/components/verbs"
import {
  stickyTableHeader,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"

type PanelProps = {
  /** A rule a link into the page asked for, from the ports page's hand-offs. */
  handoff?: RuleHandoff
  status: FirewallStatus | undefined
  loading: boolean
  error: Error | undefined
  lastSuccess?: number
  refresh: () => void
}

/**
 * What may reach this machine, and the controls that change it.
 *
 * The page opens on the firewall as a project's page opens on the project:
 * its own mark and name, whether it is enforcing beside it, the switch that
 * decides that and Add rule at the line's end, Reset and Refresh behind the
 * menu. Then the rules folded by where they lead (`firewall-picture.tsx`),
 * with what the log says the rules did on the nodes that did it; the ways in
 * a change may never take away; the rules in the order the firewall reads
 * them; the defaults they fall through to, as one settings section whose
 * edits are staged and saved together by the page's one save bar; how the
 * host is filtered; what was changed from here; and the firewall's own log.
 *
 * Seven tiles stood under the picture. The rule count is the identity's
 * allow and deny; the three defaults are the Defaults section and the
 * picture's last node; and the three counts from the log are on the picture.
 * The defaults were four selects in a column beside the rules, each sending
 * its change the moment it was picked.
 *
 * A firewall that is not logging has no log, and the section says so and
 * points at the control that changes it rather than drawing an empty pane.
 */
export function FirewallPanel(props: PanelProps) {
  return (
    <SaveBarProvider>
      <FirewallWorkspace {...props} />
    </SaveBarProvider>
  )
}

function FirewallWorkspace({ status, loading, error, lastSuccess, refresh, handoff }: PanelProps) {
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
  // Switching the firewall off, a default, a reset, a removal and a plan are
  // the server's destructive routes: an administrator's account without that
  // capability is refused them, so it is not offered them.
  const mayDestroy = can("destructive")
  const [handoffEdit, setHandoffEdit] = useState(handoff?.edit)
  const [staged, setStaged] = useState<StagedChange[]>([])
  const findings = useMemo(() => findingsByRule(status), [status])

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
  useEffect(() => {
    if (!status?.available || window.location.hash !== "#firewall-enabled") return
    const control = document.getElementById("firewall-enabled")
    control?.scrollIntoView({ block: "center" })
    control?.focus({ preventScroll: true })
  }, [status?.available])
  // The rule is looked up by number and opened only while it still names the
  // port the link was about: ufw renumbers on every delete, and the form
  // would otherwise replace whichever rule took the number since.
  const handoffRule =
    handoffEdit && writable
      ? rules.find((r) => r.number === handoffEdit.number && r.port === handoffEdit.port)
      : undefined

  const header = <PageContext eyebrow="Network" title="Firewall" />
  const refreshVerb: Verb = {
    key: "refresh",
    label: "Refresh",
    icon: RefreshClockwise,
    run: refresh,
  }

  if (!status) {
    const failed = Boolean(error) && !loading
    return (
      <>
        {header}
        <HostIdentity
          label="About the firewall"
          fallback={FirewallCheck}
          title="Firewall"
          state={
            failed ? (
              <Status tone="danger" label="Not read" />
            ) : (
              <span className="text-xs font-medium">
                <TextShimmer>Reading the firewall…</TextShimmer>
              </span>
            )
          }
          facts={
            failed ? (
              <span>The firewall could not be read.</span>
            ) : (
              <Skeleton className="my-0.5 h-3 w-56 max-w-full" />
            )
          }
          menu={<VerbMenu verbs={[refreshVerb]} label="Firewall actions" />}
        />
        {failed && error ? (
          <ErrorState error={error} onRetry={refresh} />
        ) : (
          <Panel plain>
            <PanelHeader title="Inbound" />
            <PanelBody>
              <FirewallPictureLoading />
            </PanelBody>
          </Panel>
        )}
      </>
    )
  }
  if (!status.available) {
    return (
      <>
        {header}
        <NetworkReadWarning
          error={error}
          refresh={refresh}
          lastSuccess={lastSuccess}
          reading="firewall state"
        />
        <FirewallMissing reason={status.error} onInstalled={refresh} />
      </>
    )
  }

  const hidden = status.rules.length - rules.length
  const allows = rules.filter((r) => r.action === "ALLOW" || r.action === "LIMIT").length
  const dangerous = rules.filter((r) => r.danger).length
  const inbound = policyLabel(status.policy?.incoming, status.defaultPolicy)

  const reset = () =>
    confirm({
      title:
        status.backend === "firewalld"
          ? `Reload zone ${status.zone ?? ""}'s shipped settings`
          : "Reset the firewall",
      confirmLabel: "Reset",
      description: (
        <div className="space-y-3">
          <p className="text-destructive">
            {status.backend === "firewalld"
              ? `Every change made to zone ${status.zone ?? ""} is replaced by the definition firewalld ships for it. Zones firewalld does not ship have nothing to reload.`
              : `Every rule is removed and ${status.backend} is disabled. The host is left unfiltered until you configure it again.`}
          </p>
          <FirewallPreflightCheck change={{ op: "reset" }} />
        </div>
      ),
      action: async (c) => {
        await post("/firewall/reset", {}, { confirm: c })
        refresh()
      },
    })
  const verbs: Verb[] = [
    refreshVerb,
    ...(writable && caps.reset && mayDestroy
      ? [{ key: "reset", label: "Reset", icon: RotateCounterClockwise, run: reset, danger: true }]
      : []),
  ]
  const toggle = (enabled: boolean) =>
    confirm({
      title: enabled ? "Enable firewall" : "Disable firewall",
      confirmLabel: enabled ? "Enable" : "Disable",
      description: (
        <div className="space-y-3">
          {enabled ? (
            <p className="text-destructive">
              {status.backend} applies its default policy immediately. The server checks first that
              your connection, SSH and public ingress stay admitted, and puts the firewall back if
              the enforced rules say otherwise.
            </p>
          ) : (
            <p className="text-destructive">
              Every rule stops being enforced and the host is left unfiltered.
            </p>
          )}
          <FirewallPreflightCheck change={{ op: enabled ? "enable" : "disable" }} />
        </div>
      ),
      action: async (c) => {
        await post("/firewall/enabled", { enabled }, { confirm: c })
        refresh()
      },
    })
  // What the log says the rules did, for the nodes that did it. Not while
  // logging is off, when every figure would be a zero that means "not
  // recorded" rather than "nothing happened".
  const counts = silent ? undefined : firewallCounts(readings, firewallLog.error, press)

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
      <NetworkReadWarning
        error={error}
        refresh={refresh}
        lastSuccess={lastSuccess}
        reading="firewall state"
      />

      {/* What the page is about, laid out as a project's header: the backend
          by its own name on its own mark, whether it is enforcing beside the
          name, whether it can be told anything from here and, on firewalld,
          the zone its rules are held in. The switch that decides whether it
          enforces sits before the page's command, beside the fact it
          changes. */}
      <HostIdentity
        label="About the firewall"
        mark={firewallProduct(status.backend)}
        fallback={FirewallCheck}
        title={status.backend}
        state={
          <Status
            verdict={status.enabled ? "ok" : "warning"}
            label={status.enabled ? "Active" : "Inactive"}
          />
        }
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
            {dangerous > 0 && (
              <>
                <FactDot />
                <span className="numeric text-destructive">
                  {dangerous} open{dangerous === 1 ? "s" : ""} a sensitive port to everyone
                </span>
              </>
            )}
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
        command={
          <>
            <span className="max-sm:hidden">
              <WorkspaceHelp compact />
            </span>
            {writable && caps.toggle && mayDestroy && (
              <Switch
                id="firewall-enabled"
                aria-label="Firewall enabled"
                checked={status.enabled}
                onCheckedChange={toggle}
              />
            )}
            {writable && (
              <AddRuleDialog
                onDone={refresh}
                hasProfiles={caps.profiles}
                arrival={handoff?.add}
                onStage={
                  mayDestroy
                    ? (rule, label) => setStaged([...staged, { op: "add", rule, label }])
                    : undefined
                }
              />
            )}
          </>
        }
        menu={<VerbMenu verbs={verbs} label="Firewall actions" />}
      />

      {/* The rules folded by where they lead, before the rules in the order
          the firewall reads them: what can reach this machine is the
          question the page is opened with. */}
      <Panel plain>
        <PanelHeader title="Inbound" />
        <PanelBody>
          <FirewallPicture status={status} counts={counts} />
        </PanelBody>
      </Panel>

      {caps.readOnlyReason && (
        <Notice icon={LockClosed} title={`${status.backend} can be read here, not changed`}>
          {caps.readOnlyReason}
        </Notice>
      )}
      {status.rulesFrom === "configured" && (
        <Notice title={`${status.backend} is not enforcing these rules`}>
          They are the rules it loads the moment it is switched on, read from its configuration.
          Rules are numbered, and can be edited or removed one by one, once it enforces them.
        </Notice>
      )}
      {status.backend === "nftables" && (
        <Notice title="The dashboard's own nftables table">
          Only table inet jd_firewall is managed here: it filters inbound traffic for both families,
          admits established connections, loopback, ICMP and your trusted addresses before its
          rules, and is restored at boot. Rules in any other table are never adopted or changed.
        </Notice>
      )}

      <FirewallAccessPanel />

      {writable && mayDestroy && (
        <FirewallPlanTray
          staged={staged}
          onUnstage={(index) => setStaged(staged.filter((_, i) => i !== index))}
          onClear={() => setStaged([])}
          onApplied={refresh}
        />
      )}

      <Panel id="rules" className="scroll-mt-6">
        <PanelHeader
          title={
            <span className="inline-flex items-center gap-1.5">
              Rules
              {/* The guard, stated once and where it applies. It never
                  changes and it is never the reason somebody opened this
                  page, so it is the note on the list it protects. */}
              {writable && (
                <InfoTip label="About the lock-out guard">
                  Your current address is protected from lockout. Address-specific deny rules take
                  priority over allow rules.
                </InfoTip>
              )}
            </span>
          }
          actions={
            <span className="numeric text-hint text-muted-foreground">
              {query.trim() ? `${shown.length} of ${rules.length}` : rules.length}
            </span>
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
                            {rule.number !== undefined ? `#${rule.number}` : "configured"} ·{" "}
                            {rule.direction || "IN"}
                            {rule.interface ? ` · on ${rule.interface}` : ""}
                          </span>
                          {rule.id && findings.get(rule.id) && (
                            <Tag
                              tone="warning"
                              title={findings.get(rule.id)!.reason}
                              data-testid="rule-finding"
                            >
                              {findings.get(rule.id)!.kind} by #{findings.get(rule.id)!.byNumber}
                            </Tag>
                          )}
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
                          {admin && rule.id && <RuleHistoryPopover rule={rule} />}
                          {writable &&
                            rule.number !== undefined &&
                            rule.direction !== "FWD" &&
                            !rule.interface && (
                              <IconAction label="Edit rule" onClick={() => setEditing(rule)}>
                                <Pencil />
                              </IconAction>
                            )}
                          {writable &&
                            mayDestroy &&
                            rule.id &&
                            rule.number !== undefined &&
                            !staged.some((c) => c.op === "delete" && c.ruleId === rule.id) && (
                              <IconAction
                                label="Remove in a plan"
                                onClick={() =>
                                  setStaged([
                                    ...staged,
                                    {
                                      op: "delete",
                                      ruleId: rule.id!,
                                      label: `#${rule.number} ${rule.raw}`,
                                    },
                                  ])
                                }
                              >
                                <ListOrdered />
                              </IconAction>
                            )}
                          {writable && mayDestroy && rule.number !== undefined && (
                            <IconAction
                              label="Delete rule"
                              className="text-destructive"
                              onClick={() =>
                                confirm({
                                  title: "Delete firewall rule",
                                  confirmLabel: "Delete",
                                  description: (
                                    <div className="space-y-3">
                                      <p className="font-mono text-xs">{rule.raw}</p>
                                      {rule.id && (
                                        <FirewallPreflightCheck
                                          change={{ op: "delete", ruleId: rule.id }}
                                        />
                                      )}
                                    </div>
                                  ),
                                  action: async (c) => {
                                    await del(rulePath(rule), { confirm: c })
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
      </Panel>

      <FirewallDefaults
        status={status}
        mayPolicy={writable && caps.defaultPolicy && mayDestroy}
        mayLog={writable && caps.logging}
        refresh={refresh}
      />

      <FirewallEnforcement status={status} />
      {admin && <FirewallHistoryPanel />}

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
  const group = document.getElementById(LOGGING_CONTROL)
  group?.scrollIntoView({ block: "center" })
  // The group is not a stop of its own: the level in force is.
  const level =
    group?.querySelector<HTMLElement>('[data-state="on"]') ?? group?.querySelector("button")
  level?.focus({ preventScroll: true })
}

const POLICIES = ["deny", "allow", "reject"]
const LOG_LEVELS = ["off", "low", "medium", "high", "full"]
// firewalld distinguishes three levels, not five; offering ufw's ladder would
// let somebody pick "medium" and read back "full".
const FIREWALLD_LOG_LEVELS = ["off", "low", "full"]

/**
 * The firewall log's three counts over its window, as the picture's nodes say
 * them: what was refused and from how many addresses, and what a rate limit
 * turned away. A press on either narrows the log to the lines it counts. A
 * log that could not be read has no figures, and says so in their place.
 */
function firewallCounts(
  readings: LensReadingsState,
  unread: Error | undefined,
  press: (reading: LensReading) => void,
): FirewallCounts | undefined {
  const tile = (id: string) => readings.tiles.find((t) => t.reading.id === id)
  const blocked = tile("blocked")
  const limited = tile("limited")
  if (unread || !blocked || !limited) return undefined
  return {
    blocked: blocked.figure?.value,
    sources: tile("sources")?.figure?.value,
    limited: limited.figure?.value,
    window: readings.window.short,
    onBlocked: () => press(blocked.reading),
    onLimited: () => press(limited.reading),
  }
}

type DefaultKey = "incoming" | "outgoing" | "routed" | "logging"

type DefaultRow = {
  key: DefaultKey
  label: string
  /** The control's accessible name. */
  name: string
  why: string
  options: string[]
  /** What is in force, as one of the options: ufw's line is not always one of the words offered. */
  current: string
  /** What a reader who cannot change it is told, where it is not one of the options. */
  word?: string
  editable: boolean
}

/**
 * What a connection meets when no rule matched it, and how much the firewall
 * writes down: one settings section under the rules it finishes. Each default
 * is a row, a name with its note behind the ⓘ and its few choices as
 * segments; an edit is staged, marked down the row's edge, and saved with the
 * rest by the page's one save bar.
 *
 * Saving sends what each row sent when it was a select that applied on
 * change: `POST /firewall/policy` a direction at a time and `POST
 * /firewall/logging`. An inbound default that turns traffic away is still
 * asked about first, with the server's own check of the ways in it must keep;
 * the bar's Save waits on that answer, and closing the dialog sends nothing.
 *
 * A reader who cannot change a default is shown it in words in the same
 * rows. The backend decides which rows there are: ufw has all four,
 * firewalld its zone's target and its log, and iptables' log is whatever its
 * own LOG rules write.
 */
function FirewallDefaults({
  status,
  mayPolicy,
  mayLog,
  refresh,
}: {
  status: FirewallStatus
  mayPolicy: boolean
  mayLog: boolean
  refresh: () => void
}) {
  const [draft, setDraft] = useState<Partial<Record<DefaultKey, string>>>({})
  const [busy, setBusy] = useState(false)
  // The bar's Save waits on the confirmation: it resolves when the default was
  // applied, or when the reader closed the dialog without applying.
  const [review, setReview] = useState<ConfirmRequest | null>(null)
  const settle = useRef<((applied: boolean) => void) | undefined>(undefined)
  const answer = (applied: boolean) => {
    settle.current?.(applied)
    settle.current = undefined
  }

  const ufw = status.backend === "ufw"
  const among = (options: string[], value: string, fallback: string) =>
    options.includes(value) ? value : fallback
  const levels = status.backend === "firewalld" ? FIREWALLD_LOG_LEVELS : LOG_LEVELS
  const rows: DefaultRow[] = [
    {
      key: "incoming",
      label: "Inbound",
      name: "Inbound default",
      why: "A connection no rule matched.",
      options: POLICIES,
      current: among(POLICIES, policyLabel(status.policy?.incoming, status.defaultPolicy), "allow"),
      word: policyLabel(status.policy?.incoming, status.defaultPolicy),
      editable: mayPolicy,
    },
    ...(ufw || status.policy?.outgoing
      ? [
          {
            key: "outgoing" as const,
            label: "Outbound",
            name: "Outbound default",
            why: "What this host may reach.",
            options: POLICIES,
            current: among(POLICIES, policyLabel(status.policy?.outgoing), "allow"),
            word: policyLabel(status.policy?.outgoing),
            editable: mayPolicy && ufw,
          },
        ]
      : []),
    ...(ufw || status.policy?.routed
      ? [
          {
            key: "routed" as const,
            label: "Routed",
            name: "Routed default",
            why: "Traffic this host forwards between networks.",
            options: POLICIES,
            current: among(POLICIES, policyLabel(status.policy?.routed), "deny"),
            word: policyLabel(status.policy?.routed),
            editable: mayPolicy && ufw,
          },
        ]
      : []),
    {
      key: "logging",
      label: "Logging",
      name: "Logging level",
      why: "How much is written about what was dropped.",
      options: levels,
      current: among(levels, loggingLevel(status.logging), "off"),
      // iptables names no level: what it logs is whatever its LOG rules write.
      word: status.logging ? loggingLevel(status.logging) : "set by its own LOG rules",
      editable: mayLog,
    },
  ]
  const staged = rows.filter(
    (row) => row.editable && draft[row.key] !== undefined && draft[row.key] !== row.current,
  )
  const editable = rows.some((row) => row.editable)

  const forget = (key: DefaultKey) =>
    setDraft((held) => Object.fromEntries(Object.entries(held).filter(([k]) => k !== key)))
  const sendPolicy = (direction: string, policy: string, c?: string) =>
    post("/firewall/policy", { direction, policy }, c ? { confirm: c } : undefined)
  /** The inbound default that turns traffic away, asked about first as it always was. */
  const askInbound = (policy: string) =>
    new Promise<boolean>((resolve) => {
      settle.current = resolve
      setReview({
        title: "Deny inbound by default",
        confirmLabel: "Apply",
        description: (
          <div className="space-y-3">
            <p className="text-destructive">
              Everything not covered by an allow rule stops being reachable. The server refuses a
              default that would refuse your connection, SSH or public ingress where they are
              admitted now.
            </p>
            <FirewallPreflightCheck change={{ op: "policy", direction: "incoming", policy }} />
          </div>
        ),
        action: async (c) => {
          await sendPolicy("incoming", policy, c)
          forget("incoming")
          refresh()
          answer(true)
        },
      })
    })
  const save = async () => {
    setBusy(true)
    let everything = true
    try {
      // The one that can cost access goes first, behind its confirmation: a
      // reader who backs out of it has changed nothing.
      const inbound = staged.find((row) => row.key === "incoming")
      const risky = inbound && draft.incoming !== "allow"
      if (risky && !(await askInbound(draft.incoming!))) return false
      for (const row of staged) {
        const value = draft[row.key]!
        if (row.key === "incoming" && risky) continue
        try {
          if (row.key === "logging") {
            await post("/firewall/logging", { level: value })
            notify.success(`Logging set to ${value}`)
          } else {
            await sendPolicy(row.key, value)
            notify.success(`Default ${row.key} set to ${value}`)
          }
          forget(row.key)
        } catch (err) {
          everything = false
          notify.error("Not applied", err)
        }
      }
      refresh()
      return everything
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="min-w-0">
      <SettingForm
        name="Defaults"
        canEdit={editable}
        dirty={staged.length > 0}
        changes={staged.length}
        saving={busy}
        applies="immediately"
        onSave={save}
        onDiscard={() => setDraft({})}
      >
        <SettingSection title="Defaults" status={settingStatus({ dirty: staged.length > 0 })}>
          <div className="divide-y divide-hairline">
            {rows.map((row) => {
              const value = draft[row.key] ?? row.current
              const edited = row.editable && value !== row.current
              return (
                <div
                  key={row.key}
                  data-slot="firewall-default"
                  data-edited={edited || undefined}
                  className="relative flex min-h-12 min-w-0 flex-wrap items-center justify-between gap-x-4 gap-y-2 py-2"
                >
                  {/* A staged change is marked down the row's edge in git's
                      modified hue, the colour of a change not applied yet. */}
                  {edited && (
                    <span
                      aria-hidden
                      className="absolute inset-y-2.5 -left-3 w-0.5 rounded-full bg-[var(--git-modified)]"
                    />
                  )}
                  <span className="flex min-w-0 items-center gap-2 text-sm font-medium">
                    {row.label}
                    <InfoTip label={`About the ${row.label.toLowerCase()} default`}>
                      {row.why}
                    </InfoTip>
                  </span>
                  {row.editable ? (
                    <Segments
                      id={row.key === "logging" ? LOGGING_CONTROL : undefined}
                      label={row.name}
                      value={value}
                      options={row.options.map((option) => ({ value: option, label: option }))}
                      onChange={(next) => setDraft((held) => ({ ...held, [row.key]: next }))}
                      disabled={busy}
                    />
                  ) : (
                    <span className="text-body font-medium" data-testid={`default-${row.key}`}>
                      {row.word}
                    </span>
                  )}
                </div>
              )
            })}
          </div>
        </SettingSection>
      </SettingForm>
      <ConfirmDialog
        request={review}
        onOpenChange={(open) => {
          if (open) return
          setReview(null)
          answer(false)
        }}
      />
    </div>
  )
}

/**
 * No firewall answered. Where this server's package manager carries ufw it is
 * installed from here, as WireGuard and Tailscale are from the VPN page: it
 * goes in switched off, so nothing is filtered until the reader turns it on
 * behind the page's own check of the ways in. Elsewhere the page says what is
 * missing and why it matters.
 */
function FirewallMissing({ reason, onInstalled }: { reason?: string; onInstalled: () => void }) {
  const known = usePackageKnown("ufw")
  const description =
    reason ??
    "Neither ufw, firewalld nor iptables answered. Without one, every port anything on this machine opens is reachable from wherever the machine is."
  if (known === "known")
    return (
      <InstallHandoff
        pkg="ufw"
        products={["netfilter"]}
        icon={Shield}
        title="No firewall on this host"
        description={`${description} ufw goes in switched off: nothing is filtered until you turn it on here.`}
        onInstalled={onInstalled}
        phases={false}
      />
    )
  return (
    <EmptyState
      mark={<ProductLogos ids={["netfilter", "firewalld"]} />}
      title="No firewall on this host"
      description={description}
    />
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
