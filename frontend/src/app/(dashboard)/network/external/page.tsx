"use client"

import { useEffect, useState } from "react"
import { Page, PageContext } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { Disclosure, Field, FieldRow } from "@/components/form"
import { ConfirmDialog } from "@/components/confirm-dialog"
import type { ConfirmRequest } from "@/components/confirm-dialog"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Checkbox } from "@/components/ui/checkbox"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { NetworkReadWarning } from "@/components/network/read-warning"
import { del, errorMessage, get, post } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import {
  activeCheck,
  checkCounts,
  enrollmentRequest,
  newEnrollmentDraft,
  stageReading,
} from "@/lib/network-external"
import type {
  EnrollmentDraft,
  ExternalCheck,
  ExternalEnrollment,
  ExternalVantage,
} from "@/lib/network-external"

const time = (value?: string) => (value ? new Date(value).toLocaleString() : "Not recorded")
const familyLabel = (value: string) => (value === "inet" ? "IPv4" : "IPv6")

// The reader compares retained measurements. Enrollment changes its inputs,
// while this page stays in the report register with the host Overview's ladder.
export default function ExternalChecksPage() {
  const { can } = useAuth()
  const admin = can("system.admin")
  const vantages = usePoll<ExternalVantage[]>(
    (signal) => get("/network/external/vantages", undefined, signal),
    10000,
    [admin],
    { enabled: admin },
  )
  const checks = usePoll<ExternalCheck[]>(
    (signal) => get("/network/external/checks", undefined, signal),
    5000,
    [admin],
    { enabled: admin },
  )
  const [draft, setDraft] = useState(newEnrollmentDraft)
  const [enrollment, setEnrollment] = useState<ExternalEnrollment>()
  const [vantageId, setVantageId] = useState("")
  const [scopeId, setScopeId] = useState("")
  const [family, setFamily] = useState<"inet" | "inet6">("inet")
  const [port, setPort] = useState("")
  const [tls, setTLS] = useState(true)
  const [selectedId, setSelectedId] = useState("")
  const [before, setBefore] = useState("")
  const [after, setAfter] = useState("")
  const [comparison, setComparison] = useState<string>()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()
  const [confirm, setConfirm] = useState<ConfirmRequest | null>(null)
  const rows = checks.data ?? []
  const allVantages = vantages.data ?? []
  const enrolled = allVantages.filter((v) => v.enrolledAt && !v.revokedAt)
  const vantage = enrolled.find((v) => v.id === vantageId)
  const scope = vantage?.scopes.find((s) => s.id === scopeId)
  const counts = checkCounts(allVantages, rows)
  const selected = rows.find((row) => row.id === selectedId)
  const input = enrollmentRequest(draft)
  const retained = rows.filter((row) => row.result)
  useEffect(() => {
    if (!enrollment) return
    const timer = setTimeout(
      () => setEnrollment(undefined),
      Math.max(0, new Date(enrollment.expiresAt).getTime() - Date.now()),
    )
    return () => clearTimeout(timer)
  }, [enrollment])
  const changeDraft = <K extends keyof EnrollmentDraft>(key: K, value: EnrollmentDraft[K]) =>
    setDraft((current) => ({ ...current, [key]: value }))
  const refresh = () => {
    vantages.refresh()
    checks.refresh()
  }
  const perform = async (action: () => Promise<void>) => {
    if (busy || !admin) return
    setBusy(true)
    setError(undefined)
    try {
      await action()
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }
  const chooseVantage = (id: string) => {
    const item = enrolled.find((v) => v.id === id)
    const first = item?.scopes[0]
    setVantageId(id)
    setScopeId(first?.id ?? "")
    setFamily(first?.families[0] ?? "inet")
    setPort(first ? String(first.ports[0]) : "")
  }
  return (
    <Page className="animate-rise">
      <PageContext eyebrow="Network" title="External checks" />
      <StatGrid columns={4}>
        <StatTile
          label="Enrolled sources"
          value={counts.enrolled}
          hint="Operator declared placement"
        />
        <StatTile
          label="Pending checks"
          value={counts.pending}
          hint="Queue acceptance is not connectivity"
        />
        <StatTile
          label="Retained measurements"
          value={counts.retained}
          hint="Signed agent reports · seven days"
        />
        <StatTile
          label="Without evidence"
          value={counts.unknown}
          hint="Expired or cancelled checks stay unknown"
        />
      </StatGrid>
      <Panel plain>
        <PanelHeader title="Measure from a controlled source" />
        <PanelBody className="space-y-5">
          <p className="max-w-3xl text-body text-muted-foreground">
            Compare DNS, one TCP connection and optional verified TLS from an explicitly enrolled
            machine. Region names are operator declarations. These results describe that source,
            address family and collection time; they do not prove global availability or identify a
            provider failure.
          </p>
          {!admin && (
            <p role="status" className="text-body text-muted-foreground">
              External measurements and agent management require system administrator access.
            </p>
          )}
          {error && (
            <p role="alert" className="text-body text-destructive">
              {error}
            </p>
          )}
          <NetworkReadWarning
            error={vantages.error ?? checks.error}
            refresh={refresh}
            lastSuccess={checks.lastSuccess}
          />
          <form
            className="max-w-3xl space-y-4"
            onSubmit={(event) => {
              event.preventDefault()
              if (
                !vantage ||
                !scope ||
                !scope.families.includes(family) ||
                !scope.ports.includes(Number(port))
              )
                return
              void perform(async () => {
                const check = await post<ExternalCheck>("/network/external/checks", {
                  vantageId,
                  scopeId,
                  family,
                  port: Number(port),
                  tls,
                })
                setSelectedId(check.id)
                checks.refresh()
              })
            }}
          >
            <FieldRow>
              <Field label="Controlled source" htmlFor="external-source">
                <Picker
                  id="external-source"
                  value={vantageId}
                  onChange={chooseVantage}
                  disabled={!admin || busy}
                  options={enrolled.map((v) => ({
                    value: v.id,
                    label: `${v.name} · ${v.location || "Location undeclared"}`,
                  }))}
                />
              </Field>
              <Field label="Approved service" htmlFor="external-scope">
                <Picker
                  id="external-scope"
                  value={scopeId}
                  onChange={(id) => {
                    const item = vantage?.scopes.find((s) => s.id === id)
                    setScopeId(id)
                    setPort(item ? String(item.ports[0]) : "")
                    setFamily(item?.families[0] ?? "inet")
                  }}
                  disabled={!vantage || busy}
                  options={(vantage?.scopes ?? []).map((s) => ({ value: s.id, label: s.target }))}
                />
              </Field>
            </FieldRow>
            <FieldRow>
              <Field label="Address family" htmlFor="external-family">
                <Picker
                  id="external-family"
                  value={family}
                  onChange={(value) => setFamily(value as "inet" | "inet6")}
                  disabled={!scope || busy}
                  options={(scope?.families ?? []).map((f) => ({
                    value: f,
                    label: familyLabel(f),
                  }))}
                />
              </Field>
              <Field label="Approved TCP port" htmlFor="external-port">
                <Picker
                  id="external-port"
                  value={port}
                  onChange={setPort}
                  disabled={!scope || busy}
                  options={(scope?.ports ?? []).map((p) => ({
                    value: String(p),
                    label: String(p),
                  }))}
                />
              </Field>
            </FieldRow>
            {scope && (
              <p className="font-mono text-hint break-all text-muted-foreground">
                Approved destinations: {scope.addresses.join(", ")}. A DNS answer outside this set
                stops before TCP.
              </p>
            )}
            <label className="flex items-center gap-2 text-body">
              <Checkbox
                checked={tls}
                onCheckedChange={(value) => setTLS(value === true)}
                disabled={!admin || busy}
              />{" "}
              Verify TLS certificate and hostname on this connection
            </label>
            <Button
              type="submit"
              disabled={
                !admin ||
                busy ||
                !scope ||
                !scope.ports.includes(Number(port)) ||
                rows.some((row) => row.vantageId === vantageId && activeCheck(row))
              }
            >
              {busy ? "Working…" : "Queue bounded check"}
            </Button>
            <p className="text-hint text-muted-foreground">
              The agent polls outbound through Caddy. A missing agent expires without a measurement.
              One outstanding check per source.
            </p>
          </form>
        </PanelBody>
      </Panel>
      <Panel plain>
        <PanelHeader title="Retained evidence" />
        <PanelBody className="space-y-5">
          {/* A table scrolls independently to retain the source and three stages at narrow widths. */}
          <div className="overflow-x-auto rounded-md border border-hairline">
            <table className="w-full text-left text-body">
              <thead className="bg-surface-header text-hint text-muted-foreground">
                <tr>
                  {["Source and time", "Path", "State", "DNS", "TCP", "TLS", ""].map((title, i) => (
                    <th key={i} className="px-4 py-3 font-medium">
                      {title}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody className="divide-y divide-hairline">
                {rows.map((row) => {
                  const owner = allVantages.find((v) => v.id === row.vantageId)
                  return (
                    <tr key={row.id} className="hover:bg-row-hover">
                      <td className="px-4 py-3">
                        <button
                          className="text-left hover:text-primary"
                          onClick={() => setSelectedId(row.id)}
                        >
                          {owner?.name ?? row.vantageId}
                        </button>
                        <p className="text-hint text-muted-foreground">
                          {owner?.location || "Location undeclared"} ·{" "}
                          {time(row.result?.endedAt ?? row.createdAt)}
                        </p>
                      </td>
                      <td className="px-4 py-3 font-mono text-hint">
                        {row.result?.target ??
                          owner?.scopes.find((s) => s.id === row.request.scopeId)?.target ??
                          "Unknown target"}
                        <br />
                        {familyLabel(row.request.family)} · TCP {row.request.port}
                      </td>
                      <td className="px-4 py-3 capitalize">
                        {row.status}
                        <p className="text-hint text-muted-foreground">
                          {row.result ? "Agent reported" : "No accepted measurement"}
                        </p>
                      </td>
                      {(["dns", "tcp", "tls"] as const).map((name) => {
                        const reading = stageReading(row, name)
                        return (
                          <td key={name} className="px-4 py-3" title={reading.detail}>
                            <span
                              className={reading.good ? "text-success" : "text-muted-foreground"}
                            >
                              {reading.label}
                            </span>
                          </td>
                        )
                      })}
                      <td className="px-4 py-3">
                        {activeCheck(row) && (
                          <Button
                            size="sm"
                            variant="outline"
                            disabled={busy}
                            onClick={() =>
                              void perform(async () => {
                                await post(`/network/external/checks/${row.id}/cancel`)
                                checks.refresh()
                              })
                            }
                          >
                            Cancel acceptance
                          </Button>
                        )}
                      </td>
                    </tr>
                  )
                })}
                {!rows.length && (
                  <tr>
                    <td colSpan={7} className="px-4 py-6 text-muted-foreground">
                      No retained checks. Enroll a controlled source and select its approved
                      service.
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>
          {selected && (
            <Disclosure
              summary="Selected check"
              facts={`${familyLabel(selected.request.family)} · ${selected.status}`}
              open
            >
              <div className="space-y-3 text-body">
                <p>
                  Deadline: {time(selected.expiresAt)} · Received: {time(selected.completedAt)} ·
                  Requested by {selected.startedBy}
                </p>
                {selected.result ? (
                  <>
                    <p className="font-mono break-all">
                      {selected.result.sourceAddress || "Source unknown"} →{" "}
                      {selected.result.address || "No connected destination"}:
                      {selected.request.port}
                    </p>
                    {selected.result.stages.map((stage) => (
                      <div key={stage.name}>
                        <p className="font-medium">
                          {stage.name.toUpperCase()}: {stageReading(selected, stage.name).label} ·{" "}
                          {stage.basis} · {stage.durationMs} ms
                        </p>
                        <p className="text-muted-foreground">{stage.detail}</p>
                      </div>
                    ))}
                    {selected.result.certificate && (
                      <p className="font-mono text-hint break-all">
                        TLS certificate SHA256: {selected.result.certificate.sha256}
                      </p>
                    )}
                    {selected.result.limitations.map((limit, i) => (
                      <p key={i} className="text-hint text-muted-foreground">
                        {limit}
                      </p>
                    ))}
                  </>
                ) : (
                  <p className="text-muted-foreground">
                    No accepted measurement. Queue, expiry or cancellation states do not establish
                    service reachability.
                  </p>
                )}
              </div>
            </Disclosure>
          )}
          <FieldRow>
            <Field label="First measurement" htmlFor="external-before">
              <Picker
                id="external-before"
                value={before}
                onChange={setBefore}
                options={retained.map((row) => ({
                  value: row.id,
                  label: `${allVantages.find((v) => v.id === row.vantageId)?.name ?? row.vantageId} · ${familyLabel(row.request.family)} · ${time(row.result?.endedAt)}`,
                }))}
              />
            </Field>
            <Field label="Second measurement" htmlFor="external-after">
              <Picker
                id="external-after"
                value={after}
                onChange={setAfter}
                options={retained.map((row) => ({
                  value: row.id,
                  label: `${allVantages.find((v) => v.id === row.vantageId)?.name ?? row.vantageId} · ${familyLabel(row.request.family)} · ${time(row.result?.endedAt)}`,
                }))}
              />
            </Field>
          </FieldRow>
          <Button
            variant="outline"
            disabled={!before || !after || before === after || busy}
            onClick={() =>
              void perform(async () => {
                const result = await get<{
                  before: ExternalCheck
                  after: ExternalCheck
                  comparison: string
                }>("/network/external/compare", { before, after })
                setComparison(result.comparison)
              })
            }
          >
            Compare retained measurements
          </Button>
          {comparison && (
            <p role="status" className="text-body text-muted-foreground">
              {comparison}
            </p>
          )}
        </PanelBody>
      </Panel>
      <Panel plain>
        <PanelHeader title="Controlled sources" />
        <PanelBody className="space-y-5">
          {allVantages.map((v) => (
            <div
              key={v.id}
              className="flex flex-wrap items-start justify-between gap-3 border-b border-hairline pb-3 text-body"
            >
              <div>
                <p className="font-medium">
                  {v.name} ·{" "}
                  {v.revokedAt ? "Revoked" : v.enrolledAt ? "Enrolled" : "Awaiting enrollment"}
                </p>
                <p className="text-muted-foreground">
                  {v.location || "Location undeclared"} ·{" "}
                  {v.placement === "controlled_fixture"
                    ? "Controlled fixture"
                    : "External host (operator declared)"}
                </p>
                <p className="text-hint text-muted-foreground">
                  Last poll: {time(v.lastSeen)}. Poll source IP: {v.lastIp || "Not recorded"}; this
                  is not a probe source or geographic attestation.
                </p>
              </div>
              {!v.revokedAt && (
                <Button
                  variant="outline"
                  size="sm"
                  disabled={busy || !admin}
                  onClick={() =>
                    setConfirm({
                      title: "Revoke controlled source",
                      description: `Revoke ${v.name}'s machine identity and reject all pending results. Its retained evidence remains for seven days.`,
                      confirmLabel: "Revoke source",
                      action: async () => {
                        await del(`/network/external/vantages/${v.id}`)
                        refresh()
                      },
                    })
                  }
                >
                  Revoke
                </Button>
              )}
            </div>
          ))}
          <Disclosure
            summary="Enroll a controlled source"
            facts="One service scope · one-use credential"
          >
            <form
              className="max-w-3xl space-y-4"
              onSubmit={(event) => {
                event.preventDefault()
                if (input)
                  void perform(async () => {
                    setEnrollment(
                      await post<ExternalEnrollment>("/network/external/enrollments", input),
                    )
                    vantages.refresh()
                  })
              }}
            >
              <p className="text-body text-muted-foreground">
                Create a ten-minute enrollment for a machine you control. The exact addresses and
                ports cannot be expanded by the agent. Keep its outbound source in the dashboard
                allowlist through the existing access controls.
              </p>
              <FieldRow>
                <Field label="Source name" htmlFor="enroll-name">
                  <Input
                    id="enroll-name"
                    value={draft.name}
                    onChange={(event) => changeDraft("name", event.target.value)}
                    maxLength={80}
                    disabled={!admin || busy}
                  />
                </Field>
                <Field
                  label="Declared location"
                  htmlFor="enroll-location"
                  hint="A label supplied by you; not independently verified."
                >
                  <Input
                    id="enroll-location"
                    value={draft.location}
                    onChange={(event) => changeDraft("location", event.target.value)}
                    maxLength={120}
                    disabled={!admin || busy}
                  />
                </Field>
              </FieldRow>
              <FieldRow>
                <Field label="Placement" htmlFor="enroll-placement">
                  <Picker
                    id="enroll-placement"
                    value={draft.placement}
                    onChange={(value) =>
                      changeDraft("placement", value as EnrollmentDraft["placement"])
                    }
                    disabled={!admin || busy}
                    options={[
                      { value: "external_host", label: "External controlled host" },
                      { value: "controlled_fixture", label: "Controlled fixture" },
                    ]}
                  />
                </Field>
                <Field label="Service name or address" htmlFor="enroll-target">
                  <Input
                    id="enroll-target"
                    value={draft.target}
                    onChange={(event) => changeDraft("target", event.target.value)}
                    spellCheck={false}
                    autoComplete="off"
                    disabled={!admin || busy}
                  />
                </Field>
              </FieldRow>
              <Field
                label="Approved literal addresses"
                htmlFor="enroll-addresses"
                hint="Comma-separated exact IPv4/IPv6 addresses. No ranges or wildcard targets."
              >
                <Input
                  id="enroll-addresses"
                  value={draft.addresses}
                  onChange={(event) => changeDraft("addresses", event.target.value)}
                  className="font-mono"
                  spellCheck={false}
                  disabled={!admin || busy}
                />
              </Field>
              <FieldRow>
                <Field label="Approved TCP ports" htmlFor="enroll-ports">
                  <Input
                    id="enroll-ports"
                    value={draft.ports}
                    onChange={(event) => changeDraft("ports", event.target.value)}
                    disabled={!admin || busy}
                  />
                </Field>
                <Field label="Approved families" htmlFor="enroll-families">
                  <Picker
                    id="enroll-families"
                    value={draft.family}
                    onChange={(value) => changeDraft("family", value as EnrollmentDraft["family"])}
                    disabled={!admin || busy}
                    options={[
                      { value: "inet", label: "IPv4" },
                      { value: "inet6", label: "IPv6" },
                      { value: "both", label: "IPv4 and IPv6" },
                    ]}
                  />
                </Field>
              </FieldRow>
              <Button type="submit" disabled={!admin || busy || !input}>
                Create one-use enrollment
              </Button>
            </form>
          </Disclosure>
          {enrollment && (
            <div className="space-y-3 text-body" data-testid="external-enrollment">
              <p className="font-medium">
                Enrollment credential shown once · expires {time(enrollment.expiresAt)}
              </p>
              <p className="text-muted-foreground">
                Send the token to the agent through standard input. It is kept only in this page
                until dismissed or expired. The agent also needs the dashboard HTTPS origin, its
                trusted TLS public-key pin and this server signing key.
              </p>
              <Field label="One-use enrollment token" htmlFor="enrollment-token">
                <Input
                  id="enrollment-token"
                  value={enrollment.token}
                  readOnly
                  autoComplete="off"
                  className="font-mono"
                />
              </Field>
              <p className="font-mono text-hint break-all">
                Vantage ID: {enrollment.vantage.id}
                <br />
                Server signing key: {enrollment.serverKey}
              </p>
              <div className="flex flex-wrap gap-2">
                <Button
                  variant="outline"
                  onClick={() => void copyText(enrollment.token, "Enrollment token copied")}
                >
                  Copy enrollment token
                </Button>
                <Button variant="outline" onClick={() => setEnrollment(undefined)}>
                  Dismiss credential
                </Button>
              </div>
            </div>
          )}
        </PanelBody>
      </Panel>
      <ConfirmDialog
        request={confirm}
        onOpenChange={(open) => {
          if (!open) setConfirm(null)
        }}
      />
    </Page>
  )
}

function Picker({
  id,
  value,
  onChange,
  options,
  disabled,
}: {
  id: string
  value: string
  onChange: (value: string) => void
  options: { value: string; label: string }[]
  disabled?: boolean
}) {
  return (
    <Select value={value || undefined} onValueChange={onChange} disabled={disabled}>
      <SelectTrigger id={id}>
        <SelectValue placeholder="Select…" />
      </SelectTrigger>
      <SelectContent>
        {options.map((option) => (
          <SelectItem key={option.value} value={option.value}>
            {option.label}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  )
}
