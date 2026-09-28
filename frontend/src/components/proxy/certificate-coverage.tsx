"use client"

import { useMemo } from "react"
import type { CertificateCoverage, CertificateHygiene, CoveredName } from "@/lib/types"
import { FindingList } from "@/components/finding-list"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Row, RowList } from "@/components/row-list"
import { ErrorState, LoadingRows } from "@/components/state"
import { Status, type DotTone } from "@/components/status-dot"
import { hygieneFindings } from "@/components/proxy/findings/certificates"
import { Button } from "@/components/ui/button"
import type { PollState } from "@/hooks/use-poll"
import { useQuerySelection } from "@/hooks/use-query-selection"

const STATE: Record<CoveredName["state"], { tone: DotTone; label: string }> = {
  served: { tone: "running", label: "Covered" },
  wrong: { tone: "danger", label: "Not covered" },
  unknown: { tone: "unknown", label: "Per request" },
  available: { tone: "notice", label: "HTTP only" },
  uncovered: { tone: "warning", label: "No certificate" },
}

/** Worst first: a name browsers refuse, then one nothing covers. */
const ORDER: CoveredName["state"][] = ["wrong", "uncovered", "available", "unknown", "served"]

/**
 * What the keys and the server blocks say about the certificates, as one
 * list. Each finding opens the certificate it is about; an uncovered name
 * offers the Issue dialog and a lineage pointing elsewhere offers Delete.
 */
export function CertificateHygienePanel({
  hygiene,
  onIssue,
  onDeleteLineage,
}: {
  hygiene: PollState<CertificateHygiene>
  onIssue?: (names: string[]) => void
  onDeleteLineage?: (name: string) => void
}) {
  const [, select] = useQuerySelection("cert")
  const findings = useMemo(
    () =>
      hygieneFindings(hygiene.data, { issue: onIssue, deleteLineage: onDeleteLineage }).map(
        (finding, i) => {
          const path = hygiene.data?.findings[i].certificate
          return finding.action || !path
            ? finding
            : { ...finding, action: { label: "Open certificate", onClick: () => select(path) } }
        },
      ),
    [hygiene.data, onIssue, onDeleteLineage, select],
  )
  return (
    <Panel plain>
      <PanelHeader title="Hygiene" />
      <PanelBody flush>
        {hygiene.loading ? (
          <LoadingRows rows={2} />
        ) : hygiene.error ? (
          <ErrorState error={hygiene.error} onRetry={hygiene.refresh} />
        ) : hygiene.data ? (
          <>
            <FindingList
              findings={findings}
              emptyLabel="No exposed, shared, weak or mismatched keys, and every name is covered"
            />
            {hygiene.data.configNote && (
              <p className="pt-2 text-hint text-muted-foreground">{hygiene.data.configNote}</p>
            )}
          </>
        ) : null}
      </PanelBody>
    </Panel>
  )
}

/**
 * Every server name against the certificate that covers it, and the
 * certificates no server block or stream names.
 */
export function CertificateCoveragePanel({
  coverage,
  onIssue,
}: {
  coverage: PollState<CertificateCoverage>
  onIssue?: (names: string[]) => void
}) {
  const [, select] = useQuerySelection("cert")
  const names = useMemo(
    () =>
      [...(coverage.data?.names ?? [])].sort(
        (a, b) => ORDER.indexOf(a.state) - ORDER.indexOf(b.state),
      ),
    [coverage.data],
  )
  const uncovered = [...new Set(names.filter((n) => n.state === "uncovered").map((n) => n.name))]
  const unused = coverage.data?.unused

  return (
    <Panel plain>
      <PanelHeader
        title="Coverage"
        actions={
          onIssue &&
          uncovered.length > 0 && (
            <Button size="sm" variant="outline" onClick={() => onIssue(uncovered)}>
              Issue for these
            </Button>
          )
        }
      />
      <PanelBody flush>
        {coverage.loading ? (
          <LoadingRows rows={3} />
        ) : coverage.error ? (
          <ErrorState error={coverage.error} onRetry={coverage.refresh} />
        ) : coverage.data ? (
          <>
            {names.length === 0 ? (
              <p className="py-3 text-body text-muted-foreground">
                No server block names a host a certificate can cover.
              </p>
            ) : (
              <RowList aria-label="Server names">
                {names.map((row) => {
                  const state = STATE[row.state]
                  const path = row.certificate
                  return (
                    <li key={`${row.name}:${row.file}:${row.line}`}>
                      <Row
                        title={<span className="font-mono">{row.name}</span>}
                        subtitle={[
                          `${row.site}:${row.line}`,
                          row.certificateName &&
                            (row.state === "available"
                              ? `${row.certificateName} covers it`
                              : row.certificateName),
                        ]
                          .filter(Boolean)
                          .join(" · ")}
                        trailing={<Status tone={state.tone} label={state.label} />}
                        onClick={path ? () => select(path) : undefined}
                      />
                    </li>
                  )
                })}
              </RowList>
            )}
            <p className="eyebrow pt-5 pb-1">Named by nothing</p>
            {unused === null ? (
              <p className="text-hint text-muted-foreground">
                Which certificates nothing uses is known only from nginx -T.
              </p>
            ) : unused && unused.length > 0 ? (
              <RowList aria-label="Certificates no site uses">
                {unused.map((cert) => (
                  <li key={cert.path}>
                    <Row
                      title={cert.name}
                      mono
                      subtitle={
                        cert.disabledSites?.length
                          ? `${cert.domains.join(", ")} · named by disabled ${cert.disabledSites.join(", ")}`
                          : cert.domains.join(", ")
                      }
                      trailing={cert.expired ? <Status tone="danger" label="Expired" /> : undefined}
                      onClick={() => select(cert.path)}
                    />
                  </li>
                ))}
              </RowList>
            ) : (
              <p className="text-hint text-muted-foreground">
                Every listed certificate is named by a server block, a stream or the Caddyfile.
              </p>
            )}
            {coverage.data.configNote && (
              <p className="pt-2 text-hint text-muted-foreground">{coverage.data.configNote}</p>
            )}
          </>
        ) : null}
      </PanelBody>
    </Panel>
  )
}
