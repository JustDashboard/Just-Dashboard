"use client"

import { useState } from "react"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { del, get, post } from "@/lib/api"
import { dnsEvidenceLabel, type SavedDNSEvidence } from "@/lib/network-dns-evidence"
import { useConfirm } from "@/components/confirm-dialog"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { Field } from "@/components/form"
import { NetworkReadWarning } from "@/components/network/read-warning"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyNote, Notice } from "@/components/state"
import { Status, type DotTone } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { GhostRows } from "@/components/network/dns/ghost"
import { Question } from "@/components/network/dns/lookup-question"

const STATUS_TONE: Record<SavedDNSEvidence["status"], DotTone> = {
  running: "notice",
  completed: "running",
  failed: "danger",
  interrupted: "warning",
}

/**
 * A reading report: the question obtains evidence from the identified native
 * resolver under its split-DNS, TLS and DNSSEC policy, then flat rows separate
 * native measurements from configuration and unknown application scope. The
 * report asked for, or opened from those kept, stands under the question; the
 * kept reports are one-line cards under it.
 *
 * The routes are an administrator's, so the page offers this lookup to no one
 * else (`lookups.tsx`).
 */
export function DNSPolicyEvidence({
  name,
  type,
  onName,
  onType,
  active,
}: {
  name: string
  type: string
  onName: (name: string) => void
  onType: (type: string) => void
  /** The lookup is the one on screen: the kept reports are read only then. */
  active: boolean
}) {
  const { can } = useAuth()
  const allowed = can("system.admin")
  const records = usePoll<SavedDNSEvidence[]>(
    (signal) => get("/network/dns/evidence", undefined, signal),
    15000,
    [allowed],
    { enabled: allowed && active },
  )
  const [expectedInterface, setExpectedInterface] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()
  const [selected, setSelected] = useState<SavedDNSEvidence>()
  const { confirm, dialog } = useConfirm()
  const launch = async () => {
    setBusy(true)
    setError(undefined)
    try {
      setSelected(
        await post<SavedDNSEvidence>("/network/dns/evidence", {
          name: name.trim(),
          type,
          ...(expectedInterface.trim() ? { expectedInterface: expectedInterface.trim() } : {}),
        }),
      )
      records.refresh()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
      records.refresh()
    } finally {
      setBusy(false)
    }
  }
  const open = async (id: string) => {
    setBusy(true)
    setError(undefined)
    try {
      setSelected(await get<SavedDNSEvidence>(`/network/dns/evidence/${id}`))
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }
  // A kept report is evidence somebody may still want: it is deleted on a
  // second word, not on one press.
  const remove = (report: SavedDNSEvidence) =>
    confirm({
      title: "Delete the kept report",
      confirmLabel: "Delete",
      description: (
        <p>
          The report for <span className="font-mono">{report.request.name}</span> and its saved
          answers are removed. Export it first to keep a copy.
        </p>
      ),
      action: async () => {
        await del(`/network/dns/evidence/${report.id}`)
        setSelected(undefined)
        records.refresh()
      },
    })
  const report = selected?.result
  if (!allowed) return null
  return (
    <div className="flex min-w-0 flex-col gap-6">
      <Question
        id="dns-evidence-name"
        name={name}
        onName={onName}
        type={type}
        onType={onType}
        verb="Investigate"
        ready={Boolean(name.trim())}
        busy={busy}
        info="Asks the identified native resolver for a fresh DNS record under its split-DNS, TLS and DNSSEC policy. Private names stay within that policy; failure does not try another resolver. Reports keep this host's scope and timestamps for seven days, up to 128 finished reports."
        onSubmit={() => void launch()}
      >
        <Field
          label="Expected link"
          htmlFor="dns-evidence-link"
          info="Optional. An expected link must already be a best-match native policy scope. A/AAAA selects the answer family; it does not force the resolver upstream family or source address."
          className="max-lg:col-span-full"
        >
          <Input
            id="dns-evidence-link"
            value={expectedInterface}
            onChange={(event) => setExpectedInterface(event.target.value)}
            placeholder="Native automatic selection"
            spellCheck={false}
            autoComplete="off"
          />
        </Field>
      </Question>
      {error && (
        <div role="alert">
          <Notice tone="warning" title="Investigation unavailable">
            <p>{error}</p>
            <p>
              {selected && "The last selected report remains below. "}No automatic retry was
              requested.
            </p>
          </Notice>
        </div>
      )}
      <NetworkReadWarning
        error={records.error}
        refresh={records.refresh}
        lastSuccess={records.lastSuccess}
        reading="DNS investigation history"
      />
      {selected && (
        <div className="flex min-w-0 flex-col gap-4" aria-label="DNS policy evidence report">
          <div className="flex flex-wrap items-center justify-between gap-3 border-b border-hairline pb-3">
            <div className="min-w-0">
              <p className="font-mono text-body break-all">
                {selected.request.name} · {selected.request.type}
              </p>
              <p className="text-hint text-muted-foreground">
                {selected.status} ·{" "}
                <time dateTime={selected.startedAt}>
                  {new Date(selected.startedAt).toLocaleString()}
                </time>
                {selected.endedAt && (
                  <>
                    {" "}
                    →{" "}
                    <time dateTime={selected.endedAt}>
                      {new Date(selected.endedAt).toLocaleString()}
                    </time>
                  </>
                )}
              </p>
            </div>
            <div className="flex flex-wrap gap-2">
              <Button size="sm" variant="outline" asChild>
                <a href={`/api/v1/network/dns/evidence/${selected.id}/export`} download>
                  Export evidence
                </a>
              </Button>
              {can("destructive") && (
                <Button
                  size="sm"
                  variant="ghost"
                  disabled={busy || selected.status === "running"}
                  onClick={() => remove(selected)}
                >
                  Delete saved report
                </Button>
              )}
            </div>
          </div>
          {report ? (
            <>
              {report.error && (
                <div role="alert">
                  <Notice tone="warning" title="Native query unavailable">
                    {report.error}
                  </Notice>
                </div>
              )}
              <p className="text-body">
                {report.owner} · {report.ownerVersion ?? "Version unknown"} · {report.vantage} ·
                answer family {report.answerFamily} · upstream family {report.upstreamFamily}
              </p>
              <dl className="divide-y divide-hairline">
                {(["route", "transport", "trust", "dnssec", "nss"] as const).map((key) => (
                  <div
                    key={key}
                    className="grid min-w-0 gap-1 py-3 sm:grid-cols-[10rem_minmax(0,1fr)]"
                  >
                    <dt className="text-body font-medium">
                      {
                        {
                          route: "DNS route",
                          transport: "Transport",
                          trust: "TLS trust",
                          dnssec: "DNSSEC",
                          nss: "Wire and NSS",
                        }[key]
                      }
                    </dt>
                    <dd className="min-w-0 text-body">
                      <p>
                        {dnsEvidenceLabel(report[key])}{" "}
                        <span className="text-hint text-muted-foreground">
                          · {report[key].basis}
                        </span>
                      </p>
                      <p className="text-muted-foreground">{report[key].summary}</p>
                    </dd>
                  </div>
                ))}
              </dl>
              <div className="min-w-0 text-body">
                <p className="font-medium">Answers</p>
                <p className="font-mono break-all">
                  {report.answers.join(" · ") || "No accepted answer"}
                </p>
                <p className="text-muted-foreground">
                  Answering link indexes: {report.answerInterfaces.join(", ") || "Unmeasured"}
                </p>
              </div>
              <div className="min-w-0 text-body">
                <p className="font-medium">Policy snapshot · {report.policyMatch}</p>
                {report.policy.map((scope) => (
                  <div key={scope.index} className="min-w-0 border-b border-hairline py-3">
                    <p>
                      {scope.interface} · index {scope.index} · configured DoT{" "}
                      {scope.dnsOverTLS || "inherited/unknown"} · configured DNSSEC{" "}
                      {scope.dnssec || "inherited/unknown"}
                    </p>
                    <p className="text-muted-foreground">
                      Native DNS scope: {scope.activeDNS ? "active" : "unavailable"}
                    </p>
                    <p className="font-mono break-all">
                      {scope.domains.join(", ") || "Default route"} → {scope.servers.join(", ")}
                    </p>
                    <p className="break-all text-muted-foreground">
                      Current server observation: {scope.currentServer || "Unavailable"}; not a
                      per-query endpoint.
                    </p>
                    {scope.negativeTrustAnchors.length > 0 && (
                      <p className="break-all text-muted-foreground">
                        Configured negative trust anchors: {scope.negativeTrustAnchors.join(", ")}
                      </p>
                    )}
                  </div>
                ))}
              </div>
              {report.hops && report.hops.length > 0 && (
                <section className="min-w-0 text-body" aria-label="Native DNS question chain">
                  <h3 className="font-medium">Question chain</h3>
                  <p className="text-muted-foreground">
                    Each target receives a fresh policy check. Accepted-record trust includes every
                    alias; failed discovery questions have no transport measurement.
                  </p>
                  <ol className="divide-y divide-hairline">
                    {report.hops.map((hop, index) => (
                      <li
                        key={`${index}-${hop.name}-${hop.type}`}
                        className="min-w-0 space-y-2 py-3"
                      >
                        <p className="font-mono break-all">
                          {index + 1}. {hop.name} · {hop.type}
                        </p>
                        <p className="text-hint break-all text-muted-foreground">
                          Native owner {hop.ownerIdentity} ·{" "}
                          {hop.policyMatch || "Policy unavailable"}
                        </p>
                        {hop.policy.map((scope) => (
                          <p key={scope.index} className="break-all text-muted-foreground">
                            {scope.interface} · {scope.domains.join(", ") || "Default route"} · DNS{" "}
                            {scope.activeDNS && scope.servers.length ? "active" : "unavailable"} ·
                            configured DoT {scope.dnsOverTLS || "unknown"} · configured DNSSEC{" "}
                            {scope.dnssec || "unknown"}
                          </p>
                        ))}
                        {hop.aliasTarget && (
                          <p className="font-mono break-all">CNAME target → {hop.aliasTarget}</p>
                        )}
                        <p className="font-mono break-all">
                          Accepted records: {hop.answers.join(" · ") || "None"}
                        </p>
                        <dl className="grid min-w-0 gap-2 sm:grid-cols-2">
                          {(["transport", "trust", "dnssec"] as const).map((key) => (
                            <div key={key} className="min-w-0">
                              <dt className="text-hint text-muted-foreground">
                                {
                                  { transport: "Transport", trust: "TLS trust", dnssec: "DNSSEC" }[
                                    key
                                  ]
                                }
                              </dt>
                              <dd>
                                {dnsEvidenceLabel(hop[key])}
                                <span className="text-hint text-muted-foreground">
                                  {" "}
                                  · {hop[key].basis}
                                </span>
                              </dd>
                            </div>
                          ))}
                        </dl>
                        <p className="text-hint text-muted-foreground">
                          Answering link indexes: {hop.answerInterfaces.join(", ") || "Unmeasured"}
                          {" · "}
                          {hop.policyStable
                            ? "Stable before/after native policy"
                            : "Policy changed or completion snapshot unavailable"}
                        </p>
                        {hop.error && <p className="break-all text-warning">{hop.error}</p>}
                      </li>
                    ))}
                  </ol>
                </section>
              )}
              <ul className="list-disc space-y-1 pl-5 text-body text-muted-foreground">
                {report.limitations.map((limit) => (
                  <li key={limit}>{limit}</li>
                ))}
              </ul>
            </>
          ) : (
            <p className="text-body text-muted-foreground">
              No retained native result. An interrupted query is not rerun and cannot establish
              DNSSEC, encryption or connectivity.
            </p>
          )}
        </div>
      )}
      <Panel plain>
        <PanelHeader
          title="Kept reports"
          actions={
            records.data && (
              <span className="numeric text-hint text-muted-foreground">{records.data.length}</span>
            )
          }
        />
        <PanelBody aria-label="Saved DNS investigations">
          {!records.data ? (
            // Only while it is in front: behind another lookup nothing is read, and a
            // grey row that never lands is what a spec waiting on the page would wait for.
            !records.error && active && <GhostRows rows={1} />
          ) : records.data.length === 0 ? (
            <EmptyNote>No retained policy investigations.</EmptyNote>
          ) : (
            <ChoiceList>
              {records.data.map((record) => (
                <ChoiceRow
                  key={record.id}
                  title={<span className="align-top font-mono">{record.request.name}</span>}
                  verb={`${record.request.name} · ${record.request.type} · ${record.status}`}
                  description={
                    <>
                      {record.request.type} ·{" "}
                      <time dateTime={record.startedAt}>
                        {new Date(record.startedAt).toLocaleString()}
                      </time>
                    </>
                  }
                  trailing={<Status tone={STATUS_TONE[record.status]} label={record.status} />}
                  onSelect={() => void open(record.id)}
                />
              ))}
            </ChoiceList>
          )}
        </PanelBody>
      </Panel>
      {dialog}
    </div>
  )
}
