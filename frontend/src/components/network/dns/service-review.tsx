"use client"

import { useEffect, useLayoutEffect, useRef, useState } from "react"
import { useAuth } from "@/hooks/use-auth"
import { del, errorMessage, post } from "@/lib/api"
import {
  DNS_SERVICE_BASE,
  dnsChangeName,
  dnsEngineName,
  dnsReviewProblem,
  dnsSameProvisionResources,
  dnsSameReviewedIntent,
  readDNSChange,
  readDNSProvision,
  type DNSConnection,
  type DNSServiceChange,
  type DNSServiceProvision,
  type DNSServiceSnapshot,
} from "@/lib/network-dns-services"
import { useConfirm } from "@/components/confirm-dialog"
import { Detail, DetailList, Section } from "@/components/page"
import { Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"

export function DNSReviewStatus({ state }: { state: string }) {
  return (
    <Status
      tone={
        state === "verified"
          ? "running"
          : state === "planned"
            ? "notice"
            : state === "removed"
              ? "stopped"
              : "warning"
      }
      label={state.replaceAll("_", " ")}
    />
  )
}

export function DNSServiceReview({
  review,
  connection,
  stale,
  attempted,
  onAttempt,
  onUpdated,
  onRefresh,
}: {
  review: DNSServiceChange | DNSServiceProvision
  connection?: DNSConnection
  stale: boolean
  attempted: ReadonlySet<string>
  onAttempt: (kind: "change" | "provision", id: string) => boolean
  onUpdated: (value: DNSServiceChange | DNSServiceProvision) => void
  onRefresh: () => void
}) {
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  const kind = "resources" in review ? "provision" : "change"
  const [now, setNow] = useState(Date.now)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()
  const submitted = useRef(false)
  const problem =
    !can("system.admin") || !can("destructive")
      ? "An administrator with destructive permission must review this action."
      : stale
        ? "Refresh the retained review and its native owner before applying."
        : dnsReviewProblem(review, kind, attempted, now, connection)
  const mayRemove =
    kind === "provision" &&
    !stale &&
    can("system.admin") &&
    can("destructive") &&
    !["applying", "removing", "removed"].includes(review.state)
  const live = useRef({ problem, review, mayRemove, connection, attempted })
  useLayoutEffect(() => {
    live.current = { problem, review, mayRemove, connection, attempted }
  })
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(timer)
  }, [])

  const apply = () => {
    if (problem || busy || submitted.current) return
    confirm({
      title: kind === "provision" ? "Create owned DNS service" : "Apply reviewed DNS change",
      description:
        kind === "provision"
          ? "Create only the reviewed loopback service and its two persistent data volumes with the saved limits and native credentials."
          : "Apply this exact native policy once, then inspect native readback. A lost response remains a retained result to review.",
      confirmLabel: kind === "provision" ? "Create owned service" : "Apply native change",
      action: async (): Promise<"reported"> => {
        const current = live.current
        const latestProblem =
          current.problem ??
          dnsReviewProblem(current.review, kind, current.attempted, Date.now(), current.connection)
        if (latestProblem || !dnsSameReviewedIntent(review, current.review) || submitted.current) {
          setError(latestProblem ?? "This review changed or already has an attempted apply.")
          return "reported"
        }
        submitted.current = true
        if (!onAttempt(kind, review.id)) {
          setError(
            "The browser could not retain this attempt. Read the retained result before continuing.",
          )
          return "reported"
        }
        setBusy(true)
        setError(undefined)
        try {
          const path =
            kind === "provision"
              ? `${DNS_SERVICE_BASE}/provisions/${review.id}/apply`
              : `${DNS_SERVICE_BASE}/changes/${review.id}/apply`
          const value = await post(path, undefined, {
            signal: AbortSignal.timeout(kind === "provision" ? 135000 : 75000),
          })
          const result =
            kind === "provision"
              ? readDNSProvision(value, review.id)
              : readDNSChange(value, review.id)
          if (!dnsSameReviewedIntent(review, result))
            throw new Error("The returned native review changed its owner or intent.")
          onUpdated(result)
          if (result.state !== "verified")
            setError(
              result.error ?? "Native verification did not complete. Review the retained result.",
            )
          onRefresh()
        } catch (err) {
          setError(
            `${errorMessage(err)} The outcome may be incomplete. Read the retained result; this review will not be applied again.`,
          )
          onRefresh()
        } finally {
          setBusy(false)
        }
        return "reported"
      },
    })
  }

  const remove = () => {
    if (!mayRemove || busy) return
    const setup = review as DNSServiceProvision
    confirm({
      title:
        setup.state === "planned"
          ? "Discard DNS setup review"
          : "Remove owned DNS service and data",
      description: (
        <div className="space-y-2">
          <p>
            Remove only the resources still owned by this reviewed setup. Both saved data volumes
            are deleted.
          </p>
          <p className="font-mono break-all">
            {setup.resources.containerName}
            <br />
            {setup.resources.networkName}
            <br />
            {setup.resources.volumes.join(" · ")}
          </p>
        </div>
      ),
      confirmLabel: setup.state === "planned" ? "Discard review" : "Remove service and data",
      action: async (): Promise<"reported"> => {
        const current = live.current
        if (
          !current.mayRemove ||
          !("resources" in current.review) ||
          !dnsSameProvisionResources(setup, current.review)
        ) {
          setError(
            "The retained setup changed. Refresh and review its current resources before removal.",
          )
          return "reported"
        }
        setBusy(true)
        setError(undefined)
        try {
          const result = readDNSProvision(
            await del(`${DNS_SERVICE_BASE}/provisions/${setup.id}`, {
              signal: AbortSignal.timeout(75000),
            }),
            setup.id,
          )
          if (!dnsSameReviewedIntent(setup, result))
            throw new Error("The returned owned setup changed its owner or reviewed resources.")
          onUpdated(result)
          onRefresh()
        } catch (err) {
          setError(
            `${errorMessage(err)} Read the retained setup and its cleanup state before reviewing removal again.`,
          )
          onRefresh()
        } finally {
          setBusy(false)
        }
        return "reported"
      },
    })
  }

  const setup = kind === "provision" ? (review as DNSServiceProvision) : undefined
  const change = kind === "change" ? (review as DNSServiceChange) : undefined
  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-center gap-3" role="status" aria-atomic="true">
        <DNSReviewStatus state={review.state} />
        <span className="text-hint text-muted-foreground">
          {busy ? "Request in progress" : `Reviewed ${new Date(review.createdAt).toLocaleString()}`}
        </span>
      </div>
      {review.error && (
        <Notice tone="warning" title="Native owner needs review">
          {review.error}
        </Notice>
      )}
      {error && (
        <div role="alert">
          <Notice tone="warning" title="Retained outcome needs review">
            {error}
          </Notice>
        </div>
      )}
      {review.state === "verified" && (
        <Notice tone="success" title="Native readback verified">
          This is the engine&apos;s configured policy and runtime evidence. Client reachability
          remains a separate measurement.
        </Notice>
      )}
      {setup && (
        <Section title="Reviewed owned setup">
          <DetailList>
            <ReviewDetail label="Engine" value={dnsEngineName(setup.request.engine)} />
            <ReviewDetail label="Service" value={setup.request.name} />
            <ReviewDetail
              label="Management"
              value={`127.0.0.1:${setup.request.managementPort}`}
              mono
            />
            <ReviewDetail label="DNS UDP/TCP" value={`127.0.0.1:${setup.request.dnsPort}`} mono />
            <ReviewDetail
              label="Limits"
              value={`${setup.request.memoryMiB} MiB · ${setup.request.cpus} CPU`}
            />
            <ReviewDetail
              label="Native upstreams"
              value={setup.request.upstreams.join(" · ")}
              mono
            />
            <ReviewDetail
              label="Dashboard changes"
              value={setup.request.management ? "Reviewed changes enabled" : "Read only"}
            />
            <ReviewDetail label="Immutable image" value={setup.image} mono />
            <ReviewDetail label="Image identity" value={setup.imageId} mono />
            <ReviewDetail label="Container" value={setup.resources.containerName} mono />
            <ReviewDetail label="Dedicated bridge" value={setup.resources.networkName} mono />
            <ReviewDetail
              label="Persistent volumes"
              value={setup.resources.volumes.join(" · ")}
              mono
            />
            <ReviewDetail
              label="Resource progress"
              value={setup.resources.phase.replaceAll("_", " ")}
            />
          </DetailList>
          <ul className="space-y-2 text-hint text-muted-foreground">
            {setup.limitations.map((item, i) => (
              <li key={i}>{item}</li>
            ))}
          </ul>
        </Section>
      )}
      {change && (
        <>
          <Section title="Reviewed native change">
            <DetailList>
              <ReviewDetail label="Action" value={dnsChangeName(change.request)} />
              <ReviewDetail label="Connection generation" value={change.generation} />
              <ReviewDetail
                label="Review expires"
                value={new Date(change.expiresAt).toLocaleString()}
              />
              {change.request.action === "upstreams" && (
                <ReviewDetail
                  label="New upstreams"
                  value={change.request.upstreams.join(" · ")}
                  mono
                />
              )}
              {change.request.action === "access" && (
                <>
                  <ReviewDetail
                    label="Allowed clients"
                    value={change.request.allowedClients.join(" · ")}
                    mono
                  />
                  <ReviewDetail
                    label="Denied clients"
                    value={change.request.deniedClients.join(" · ") || "None"}
                    mono
                  />
                </>
              )}
              {"record" in change.request && (
                <>
                  <ReviewDetail label="Record name" value={change.request.record.name} mono />
                  <ReviewDetail label="Record type" value={change.request.record.type} />
                  <ReviewDetail label="Record value" value={change.request.record.value} mono />
                  {"zone" in change.request && (
                    <ReviewDetail label="Authoritative zone" value={change.request.zone} mono />
                  )}
                  {"ttl" in change.request.record && (
                    <ReviewDetail
                      label="Record TTL"
                      value={`${change.request.record.ttl} seconds`}
                    />
                  )}
                </>
              )}
              {change.request.action === "client_groups" && (
                <>
                  <ReviewDetail label="Client address" value={change.request.client.address} mono />
                  <ReviewDetail
                    label="New group IDs"
                    value={
                      change.request.client.groups.join(" · ") || "None · remove all memberships"
                    }
                  />
                </>
              )}
            </DetailList>
          </Section>
          {change.before && <ReviewedSnapshot title="Before the change" value={change.before} />}
          {change.after && <ReviewedSnapshot title="Native readback" value={change.after} />}
        </>
      )}
      {problem && review.state === "planned" && (
        <Notice tone="warning" title="Apply is held">
          {problem}
        </Notice>
      )}
      <div className="flex flex-wrap gap-2">
        {review.state === "planned" && (
          <Button onClick={apply} disabled={Boolean(problem) || busy} pending={busy}>
            {kind === "provision" ? "Create reviewed DNS service" : "Apply reviewed native change"}
          </Button>
        )}
        <Button variant="outline" onClick={onRefresh} disabled={busy}>
          Read retained result
        </Button>
        {setup && !["applying", "removing", "removed"].includes(setup.state) && (
          <Button variant="outline" onClick={remove} disabled={busy || !mayRemove}>
            {setup.state === "planned" ? "Discard setup review" : "Remove owned service and data"}
          </Button>
        )}
      </div>
      {dialog}
    </div>
  )
}

function ReviewedSnapshot({ title, value }: { title: string; value: DNSServiceSnapshot }) {
  return (
    <Section title={title}>
      <DetailList>
        <ReviewDetail
          label="Engine version"
          value={`${dnsEngineName(value.engine)} ${value.version}`}
        />
        <ReviewDetail label="Observed" value={new Date(value.observedAt).toLocaleString()} />
        <ReviewDetail label="Protection" value={value.protection ? "Enabled" : "Disabled"} />
        <ReviewDetail
          label="Upstreams"
          value={value.upstreams.join(" · ") || "None reported"}
          mono
        />
        <ReviewDetail
          label="Allowed clients"
          value={value.allowedClients.join(" · ") || "No explicit allowed scope"}
          mono
        />
        <ReviewDetail
          label="Denied clients"
          value={value.deniedClients.join(" · ") || "None reported"}
          mono
        />
        <ReviewDetail
          label="Native zones"
          value={
            value.zones
              .map((zone) => `${zone.name} · ${zone.type}${zone.disabled ? " · disabled" : ""}`)
              .join("; ") || value.zoneEvidence.summary
          }
        />
        <ReviewDetail
          label="Local overrides"
          value={
            value.localOverrides
              .map(
                (record) =>
                  `${record.name} · ${record.type} → ${record.value} · ${record.enabled === undefined ? "Enabled state unspecified" : record.enabled ? "Enabled" : "Disabled"}`,
              )
              .join("; ") || "None reported"
          }
          mono
        />
        {value.records && (
          <>
            <ReviewDetail label="Selected zone" value={value.records.zone} mono />
            <ReviewDetail
              label="Zone policy"
              value={`${value.records.type} · ${value.records.disabled ? "Disabled" : "Enabled"} · ${value.records.dnssec} · ${value.records.internal === null ? "Internal category unreported" : value.records.internal ? "Internal zone" : "External zone"} · native ${value.records.nativeVersion}`}
            />
            <ReviewDetail
              label="Native record inventory"
              value={
                value.records.records
                  .map(
                    (record) =>
                      `${record.name} · ${record.type} → ${record.value ?? "Native value outside this control"} · TTL ${record.ttl}s · ${record.disabled ? "Disabled" : "Enabled"}${record.comments ? ` · ${record.comments}` : ""}`,
                  )
                  .join("; ") || "No records reported"
              }
              mono
            />
          </>
        )}
        {value.selectedClient && (
          <>
            <ReviewDetail
              label="Selected client address"
              value={value.selectedClient.address}
              mono
            />
            <ReviewDetail
              label="Selected client group IDs"
              value={value.selectedClient.groups.join(" · ") || "None"}
            />
            <ReviewDetail
              label="Client comment"
              value={
                value.selectedClient.comment === null
                  ? "None (native null)"
                  : value.selectedClient.comment || "Empty native comment"
              }
            />
          </>
        )}
        <ReviewDetail
          label="Runtime evidence"
          value={`${value.runtime.state} · ${value.runtime.basis} · ${value.runtime.summary}`}
        />
      </DetailList>
    </Section>
  )
}

function ReviewDetail({
  label,
  value,
  mono,
}: {
  label: React.ReactNode
  value: React.ReactNode
  mono?: boolean
}) {
  return (
    <Detail label={label} className={mono ? "font-mono break-all" : "break-words"}>
      {value}
    </Detail>
  )
}
