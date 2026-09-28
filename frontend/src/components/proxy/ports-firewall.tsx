"use client"

import { FirewallCheck, Gauge, Globe, Trash, Warning } from "@/components/icons"
import { del, get } from "@/lib/api"
import type { FirewallStatus, ListenerFirewall, PortsFirewall } from "@/lib/types"
import { useConfirm } from "@/components/confirm-dialog"
import { IconAction } from "@/components/icon-action"
import { Panel, PanelBody, PanelFooter, PanelHeader } from "@/components/panel"
import { Status, type Verdict } from "@/components/status-dot"
import type { Verb } from "@/components/verbs"
import type { Socket } from "@/components/proxy/ports"

/**
 * What the firewall does with one socket, and the rules nothing answers.
 *
 * The verdict is the server's (netsec.JudgeFirewall), read in the order the
 * firewall reads its rules; this file only puts it into words and turns it
 * into hand-offs to the firewall page, whose dialog opens filled in and
 * waits for the operator to add the rule.
 */

const BACKEND: Record<string, string> = { ufw: "ufw", firewalld: "firewalld", iptables: "iptables" }

function backendName(fw: ListenerFirewall) {
  return (fw.backend && BACKEND[fw.backend]) || "the firewall"
}

function refuses(policy?: string) {
  return /^(deny|reject|drop)/i.test(policy ?? "")
}

/** The verdict in a few words, and the conclusion it leads to. */
export function firewallWords(fw: ListenerFirewall): {
  verdict: Verdict | undefined
  label: string
  conclusion: string
} {
  const rule = fw.rule ? `rule ${fw.rule}` : undefined
  switch (fw.verdict) {
    case "allowed":
      return {
        verdict: "notice",
        label: rule
          ? `${fw.action === "LIMIT" ? "Rate-limited" : "Allowed"} by ${rule}`
          : "Allowed: default allow",
        conclusion: "Anyone who can reach the address can connect.",
      }
    case "restricted":
      return {
        verdict: "ok",
        label: `Allowed from ${fw.from} by ${rule}`,
        conclusion: `Everyone else meets the inbound default, ${fw.default}.`,
      }
    case "blocked":
      return {
        verdict: "ok",
        label: rule ? `Blocked by ${rule}` : `Blocked: default ${fw.default}`,
        conclusion: "Connections from off this machine are refused.",
      }
    case "off":
      return {
        verdict: "warning",
        label: "Firewall off",
        conclusion: `${backendName(fw)} is installed and not enforcing, so nothing is filtered.`,
      }
    case "docker":
      return {
        verdict: "warning",
        label: `Docker bypasses ${backendName(fw)}`,
        conclusion: `Docker's NAT rules forward the port before ${backendName(fw)}'s rules are read, so no rule there applies. Publish it on 127.0.0.1 or filter it in the DOCKER-USER chain.`,
      }
  }
  return {
    verdict: undefined,
    label: "Firewall unknown",
    conclusion: "No firewall this dashboard can read is enforcing here.",
  }
}

export function FirewallVerdict({ socket }: { socket: Socket }) {
  const fw = socket.firewall
  if (!fw) return <span className="text-xs text-muted-foreground">Loopback only</span>
  const words = firewallWords(fw)
  return (
    <div className="min-w-0">
      <Status
        verdict={words.verdict}
        state={words.verdict ? undefined : "unknown"}
        label={words.label}
        icon={FirewallCheck}
        className="max-w-full whitespace-normal"
      />
      <p className="mt-0.5 pl-5 text-hint whitespace-normal text-muted-foreground">
        {words.conclusion}
      </p>
    </div>
  )
}

const TAILNET = "tailnet"

function firewallHref(params: Record<string, string | number | undefined>) {
  const search = new URLSearchParams()
  for (const [key, value] of Object.entries(params)) {
    if (value !== undefined && value !== "") search.set(key, String(value))
  }
  return `/security/firewall?${search}`
}

/**
 * The rules a socket's verdict suggests, as links into the firewall page's
 * dialog. Offered only for ufw, whose rule order and numbers the dialog
 * writes against, and never for a port Docker publishes: no ufw rule reaches
 * it, and the verdict cell says so instead.
 */
export function firewallHandoffs(
  socket: Socket,
  firewall: PortsFirewall | undefined,
  navigate: (href: string) => void,
): Verb[] {
  const fw = socket.firewall
  if (
    !fw ||
    !firewall?.editable ||
    !firewall.enabled ||
    firewall.backend !== "ufw" ||
    (socket.reach !== "all" && socket.reach !== "public")
  ) {
    return []
  }
  const port = socket.port
  const proto = socket.protocol
  const comment = socket.displayName || socket.process || undefined
  const byRule = fw.rule && (fw.action === "ALLOW" || fw.action === "LIMIT")
  const verbs: Verb[] = []
  const group = "Firewall"

  if (refuses(fw.default)) {
    if (fw.verdict === "blocked" && !fw.rule) {
      verbs.push({
        key: "fw-tailnet",
        label: "Allow only from tailnet",
        icon: Globe,
        group,
        run: () =>
          navigate(
            firewallHref({ add: 1, port, proto, action: "allow", source: TAILNET, comment }),
          ),
      })
    }
    // Narrowing the rule that admits everyone, in its own place: a second
    // rule for the tailnet would sit behind it and change nothing.
    if (fw.verdict === "allowed" && byRule) {
      verbs.push({
        key: "fw-tailnet",
        label: "Allow only from tailnet",
        icon: Globe,
        group,
        run: () => navigate(firewallHref({ edit: fw.rule, port, source: TAILNET })),
      })
    }
  }
  if (fw.verdict === "allowed") {
    // Ahead of the rule that admits it, since the first match wins.
    verbs.push({
      key: "fw-deny",
      label: "Deny from everyone",
      icon: Warning,
      group,
      run: () =>
        navigate(
          firewallHref({
            add: 1,
            port,
            proto,
            action: "deny",
            source: "anywhere",
            position: fw.rule || undefined,
            comment,
          }),
        ),
    })
    if (port === 22 && proto === "tcp" && fw.action !== "LIMIT") {
      verbs.push({
        key: "fw-limit",
        label: "Rate-limit",
        icon: Gauge,
        group,
        run: () =>
          navigate(
            byRule
              ? firewallHref({ edit: fw.rule, port, action: "limit" })
              : firewallHref({ add: 1, port, proto, action: "limit", source: "anywhere", comment }),
          ),
      })
    }
  }
  return verbs
}

/**
 * Rules with nothing behind them: an allow for a port no socket listens on
 * and Docker does not publish, left from a service that moved or was
 * removed. The hole stays open for whatever binds the port next.
 */
export function OrphanRules({
  firewall,
  admin,
  onChange,
}: {
  firewall: PortsFirewall | undefined
  admin: boolean
  onChange: () => void
}) {
  const { confirm, dialog } = useConfirm()
  const rules = firewall?.orphanRules ?? []
  if (rules.length === 0) return null
  const writable = admin && firewall?.editable
  return (
    <Panel>
      <PanelHeader title="Rules with nothing behind them" />
      <PanelBody>
        <ul className="divide-y divide-hairline">
          {rules.map((rule) => (
            <li key={rule.number ?? rule.raw} className="flex min-w-0 items-center gap-3 py-2.5">
              <span className="numeric w-10 shrink-0 font-mono text-hint text-muted-foreground">
                {rule.number}
              </span>
              <div className="min-w-0 flex-1">
                <p className="font-mono text-body wrap-anywhere">
                  {rule.port}
                  {rule.protocol ? `/${rule.protocol}` : ""}
                  <span className="ml-2 text-hint text-muted-foreground">
                    {rule.action} from {rule.from || "Anywhere"}
                  </span>
                </p>
                {(rule.service || rule.comment) && (
                  <p className="truncate text-hint text-muted-foreground">
                    {[rule.service, rule.comment].filter(Boolean).join(" · ")}
                  </p>
                )}
              </div>
              {writable && rule.number !== undefined && (
                <IconAction
                  label={`Delete rule ${rule.number}`}
                  className="text-destructive"
                  onClick={() =>
                    confirm({
                      title: "Delete firewall rule",
                      confirmLabel: "Delete",
                      description: <p className="font-mono text-xs">{rule.raw}</p>,
                      action: async (c) => {
                        // ufw deletes by number and renumbers after every
                        // delete, so the number is checked against the rule
                        // it names now, not the one listed a poll ago.
                        const now = await get<FirewallStatus>("/firewall/")
                        const same = now.rules.find((r) => r.number === rule.number)
                        if (same?.raw !== rule.raw) {
                          onChange()
                          throw new Error(
                            `Rule ${rule.number} has changed since this list was read. The list has been refreshed.`,
                          )
                        }
                        await del(`/firewall/rules/${rule.number}`, { confirm: c })
                        onChange()
                      },
                    })
                  }
                >
                  <Trash />
                </IconAction>
              )}
            </li>
          ))}
        </ul>
      </PanelBody>
      <PanelFooter className="text-hint text-muted-foreground">
        No socket listens on these ports and Docker publishes none of them, so each rule holds a
        port open for whatever binds it next.
      </PanelFooter>
      {dialog}
    </Panel>
  )
}
