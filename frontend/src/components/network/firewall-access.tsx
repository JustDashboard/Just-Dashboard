"use client"

import { useEffect, useState } from "react"
import { post } from "@/lib/api"
import type {
  FirewallAccessCheck,
  FirewallAccessComparison,
  FirewallPreflight,
  FirewallStatus,
} from "@/lib/types"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Notice, Spinner } from "@/components/state"
import { Status, type DotTone } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { Warning } from "@/components/icons"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { accessLosses } from "./firewall-reading"

const VERDICT_TONE: Record<FirewallAccessCheck["verdict"], DotTone> = {
  admitted: "running",
  limited: "warning",
  refused: "danger",
  unfiltered: "notice",
  unknown: "unknown",
}

function where(check: FirewallAccessCheck) {
  const port = `${check.port}/${check.protocol}`
  const family = check.family === "ipv6" ? "IPv6" : "IPv4"
  const from = check.source === "Anywhere" ? "anyone" : check.source
  return `${port} · ${family} · from ${from}${check.interface ? ` on ${check.interface}` : ""}`
}

/**
 * The ways in a firewall change may never take away — the operator's own
 * connection to the dashboard and to SSH, and Caddy's public ingress — each
 * with the verdict the current rules give it and the rule that decides.
 */
export function FirewallAccessPanel({ status }: { status: FirewallStatus }) {
  const checks = status.access ?? []
  if (checks.length === 0) return null
  return (
    <Panel>
      <PanelHeader title="Preserved access" />
      <PanelBody flush>
        <AccessTable checks={checks} />
      </PanelBody>
    </Panel>
  )
}

function AccessTable({ checks }: { checks: (FirewallAccessCheck & { before?: string })[] }) {
  const comparing = checks.some((check) => check.before !== undefined)
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>Way in</TableHead>
          {comparing && <TableHead>Now</TableHead>}
          <TableHead>{comparing ? "After" : "Verdict"}</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {checks.map((check, index) => (
          <TableRow key={`${check.name}:${check.port}:${check.family}:${check.source}:${index}`}>
            <TableCell className="whitespace-normal">
              <span className="block text-body">{check.name}</span>
              <span className="font-mono text-hint text-muted-foreground">{where(check)}</span>
            </TableCell>
            {comparing && (
              <TableCell>
                <Status
                  tone={VERDICT_TONE[check.before as FirewallAccessCheck["verdict"]]}
                  label={check.before ?? ""}
                />
              </TableCell>
            )}
            <TableCell className="whitespace-normal">
              <Status tone={VERDICT_TONE[check.verdict]} label={check.verdict} />
              <span className="mt-1 block text-hint text-muted-foreground">{check.reason}</span>
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}

/**
 * A proposed change judged by the server before it is made: each access
 * check now and after, and the refusal an apply would answer with. The
 * staged verification after the apply and the timed recovery behind a
 * temporary apply are the server's; this is the part the reader sees first.
 */
export function FirewallPreflightCheck({
  change,
  onResult,
}: {
  change: Record<string, unknown>
  onResult?: (preflight: FirewallPreflight | undefined) => void
}) {
  const key = JSON.stringify(change)
  const [state, setState] = useState<{ key: string; result?: FirewallPreflight; error?: string }>()
  useEffect(() => {
    let live = true
    post<FirewallPreflight>("/firewall/preflight", JSON.parse(key))
      .then((preflight) => {
        if (!live) return
        setState({ key, result: preflight })
        onResult?.(preflight)
      })
      .catch((err) => {
        if (!live) return
        setState({ key, error: err instanceof Error ? err.message : String(err) })
        onResult?.(undefined)
      })
    return () => {
      live = false
    }
    // onResult is the caller's setter; the change is what the check is about.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key])
  const current = state?.key === key ? state : undefined
  if (current?.error)
    return (
      <p className="text-hint text-warning">
        The change could not be checked first: {current.error}
      </p>
    )
  const result = current?.result
  if (!result)
    return (
      <p className="flex items-center gap-2 text-hint text-muted-foreground">
        <Spinner /> Checking which ways in it keeps…
      </p>
    )
  const losses = accessLosses(result.checks as FirewallAccessComparison[])
  return (
    <div className="space-y-3" data-testid="firewall-preflight">
      {result.refusal ? (
        <Notice tone="danger" icon={Warning} title="The server will refuse this change">
          {result.refusal}
        </Notice>
      ) : (
        <p className="text-hint text-muted-foreground">
          Every required way in stays admitted. The firewall is read again after the change and put
          back if that is no longer true.
        </p>
      )}
      {losses.length > 0 && (
        <p className="text-hint text-destructive">
          {losses.length} required way{losses.length === 1 ? "" : "s"} in would be refused.
        </p>
      )}
      {result.checks.length > 0 && <AccessTable checks={result.checks} />}
    </div>
  )
}

/**
 * How the host is filtered: the firewall chosen and why the others were
 * not, the policy each family and interface meets, firewalld's zones, and
 * the other nftables tables the owned table leaves alone.
 */
export function FirewallEnforcement({ status }: { status: FirewallStatus }) {
  const detection = status.detection ?? []
  const families = status.effective?.families ?? []
  const interfaces = status.effective?.interfaces ?? []
  const zones = status.zones ?? []
  if (detection.length + families.length + zones.length === 0) return null
  return (
    <Panel>
      <PanelHeader title="Enforcement" />
      <PanelBody className="space-y-5">
        {detection.length > 0 && (
          <section aria-label="Firewalls on this host" className="space-y-2">
            <p className="text-body font-medium">Firewalls on this host</p>
            <ul className="divide-y divide-hairline rounded-lg border border-hairline">
              {detection.map((d) => (
                <li
                  key={d.backend}
                  className="flex min-w-0 flex-wrap items-start gap-x-3 gap-y-1 px-3 py-2"
                >
                  <span className="w-24 shrink-0 font-mono text-xs">{d.backend}</span>
                  <Status
                    tone={
                      d.active === "active"
                        ? "running"
                        : d.active === "unknown"
                          ? "unknown"
                          : "stopped"
                    }
                    label={!d.installed ? "not installed" : d.active.replace("_", " ")}
                  />
                  {d.selected && <Tag className="text-brand">in charge</Tag>}
                  <span className="min-w-0 basis-full text-hint text-muted-foreground sm:basis-auto">
                    {d.reason}
                  </span>
                </li>
              ))}
            </ul>
          </section>
        )}
        {families.length > 0 && (
          <section aria-label="Policy by family" className="space-y-2">
            <p className="text-body font-medium">Policy by family</p>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Family</TableHead>
                  <TableHead>Filtered</TableHead>
                  <TableHead>Inbound</TableHead>
                  <TableHead>Outbound</TableHead>
                  <TableHead>Routed</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {families.map((f) => (
                  <TableRow key={f.family}>
                    <TableCell className="text-xs">
                      {f.family === "ipv6" ? "IPv6" : "IPv4"}
                    </TableCell>
                    <TableCell className="text-xs whitespace-normal">
                      {f.filtered ? "yes" : "no"}
                      {f.reason && (
                        <span className="mt-1 block text-hint text-muted-foreground">
                          {f.reason}
                        </span>
                      )}
                    </TableCell>
                    <TableCell className="text-xs">{f.incoming ?? "—"}</TableCell>
                    <TableCell className="text-xs">{f.outgoing ?? "—"}</TableCell>
                    <TableCell className="text-xs">{f.routed ?? "—"}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </section>
        )}
        {interfaces.length > 0 && (
          <section aria-label="Policy by interface" className="space-y-2">
            <p className="text-body font-medium">Policy by interface</p>
            <ul className="divide-y divide-hairline rounded-lg border border-hairline">
              {interfaces.map((i) => (
                <li
                  key={`${i.interface}:${i.zone}`}
                  className="flex min-w-0 flex-wrap items-baseline gap-x-3 px-3 py-2 text-xs"
                >
                  <span className="font-mono">{i.interface}</span>
                  {i.zone && <span className="text-muted-foreground">zone {i.zone}</span>}
                  <span>inbound {i.incoming || "—"}</span>
                  <span className="numeric text-muted-foreground">
                    {i.rules} rule{i.rules === 1 ? "" : "s"}
                  </span>
                  {i.reason && (
                    <span className="basis-full text-hint text-muted-foreground">{i.reason}</span>
                  )}
                </li>
              ))}
            </ul>
          </section>
        )}
        {zones.length > 0 && (
          <section aria-label="Active zones" className="space-y-2">
            <p className="text-body font-medium">Active zones</p>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Zone</TableHead>
                  <TableHead>Target</TableHead>
                  <TableHead>Bound</TableHead>
                  <TableHead className="text-right">Rules</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {zones.map((z) => (
                  <TableRow key={z.name}>
                    <TableCell className="text-xs">
                      <span className="font-mono">{z.name}</span>
                      {z.default && <Tag className="ml-2">default</Tag>}
                    </TableCell>
                    <TableCell className="font-mono text-xs">{z.target}</TableCell>
                    <TableCell className="font-mono text-xs whitespace-normal">
                      {[...z.interfaces, ...z.sources].join(", ") ||
                        (z.default ? "every unbound interface" : "—")}
                    </TableCell>
                    <TableCell className="numeric text-right text-xs">{z.rules.length}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </section>
        )}
        {(status.foreign?.length ?? 0) > 0 && (
          <p className="text-hint text-muted-foreground">
            Other nftables tables keep enforcing beside the dashboard&rsquo;s own and are not
            changed from here: <span className="font-mono">{status.foreign!.join(", ")}</span>. An
            accept in the dashboard&rsquo;s table cannot override a drop in theirs.
          </p>
        )}
        {status.analysis && <p className="text-hint text-muted-foreground">{status.analysis}</p>}
      </PanelBody>
    </Panel>
  )
}
