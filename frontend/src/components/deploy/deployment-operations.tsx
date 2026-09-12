"use client"

import Link from "next/link"
import { ArrowRight, Database, Globe, Shield, Warning } from "@/components/icons"
import { Group, Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyNote, EmptyState, Notice } from "@/components/state"
import { Tag } from "@/components/tag"
import type { Tone } from "@/components/tone"
import { Button } from "@/components/ui/button"
import type {
  DeploymentBackupJob,
  DeploymentDiagnosis,
  DeploymentDiagnosisFinding,
  DeploymentDomainRoute,
  DeploymentOperations,
  DeploymentStorageMount,
} from "@/lib/types"

const SEVERITY_TONE: Record<
  DeploymentDiagnosisFinding["severity"],
  "danger" | "warning" | "default"
> = { critical: "danger", warning: "warning", notice: "default" }

/**
 * The workspace's one place for "what is wrong". Every card states what was
 * measured, what it means and the action that follows, and every owner that
 * could not be read is listed as an unanswered question rather than silently
 * counted as healthy.
 */
export function DeploymentFindings({
  diagnosis,
  loading,
}: {
  diagnosis?: DeploymentDiagnosis
  loading?: boolean
}) {
  return (
    <Panel>
      <PanelHeader
        icon={Warning}
        title="Current findings"
      />
      <PanelBody className="space-y-3">
        {!diagnosis ? (
          <EmptyNote>{loading ? "Reading dependency owners…" : "Diagnosis unavailable."}</EmptyNote>
        ) : (
          <>
            {diagnosis.findings.length === 0 && (
              <EmptyState
                icon={Warning}
                title="Nothing to report"
                description={
                  diagnosis.status === "assessed"
                    ? "Runtime, domains, storage, backups and dependencies were all read and none reported a problem."
                    : "No owner that could be read reported a problem. The questions below were not answered."
                }
                className="border-0 py-6"
              />
            )}
            {diagnosis.findings.map((finding) => (
              <Notice
                key={finding.code + finding.title}
                title={finding.title}
                tone={SEVERITY_TONE[finding.severity]}
                icon={Warning}
              >
                <p>{finding.measured}</p>
                <p>{finding.means}</p>
                <p className="mt-1.5 flex min-w-0 flex-wrap items-center gap-2">
                  <span>{finding.action}</span>
                  {finding.deepLink ? (
                    <Link
                      href={finding.deepLink}
                      className="inline-flex min-h-9 items-center underline underline-offset-4 focus-ring"
                    >
                      Open {finding.owner}
                    </Link>
                  ) : (
                    <Tag>outside this dashboard</Tag>
                  )}
                </p>
              </Notice>
            ))}
            {diagnosis.silences.length > 0 && (
              <Group className="space-y-1.5">
                <p className="text-xs font-medium">Not assessed</p>
                <ul className="space-y-1">
                  {diagnosis.silences.map((silence, index) => (
                    <li key={silence.subject + index} className="text-xs text-muted-foreground">
                      <span className="font-medium capitalize">{silence.subject}</span>:{" "}
                      {silence.reason}
                    </li>
                  ))}
                </ul>
              </Group>
            )}
          </>
        )}
      </PanelBody>
    </Panel>
  )
}

const ROUTE_LABEL: Record<DeploymentDomainRoute["route"], { text: string; tone: Tone }> = {
  served: { text: "Routed here", tone: "success" },
  missing: { text: "No route", tone: "warning" },
  foreign: { text: "Another site", tone: "danger" },
  conflict: { text: "Conflict", tone: "danger" },
  unavailable: { text: "Not observed", tone: "default" },
}

const CERTIFICATE_LABEL: Record<
  DeploymentDomainRoute["certificate"],
  { text: string; tone: Tone }
> = {
  valid: { text: "Certificate valid", tone: "success" },
  expiring: { text: "Certificate expiring", tone: "warning" },
  expired: { text: "Certificate expired", tone: "danger" },
  missing: { text: "No certificate", tone: "warning" },
  "not requested": { text: "HTTP only", tone: "default" },
  unavailable: { text: "Certificate not observed", tone: "default" },
}

export function DeploymentDomains({ operations }: { operations?: DeploymentOperations }) {
  const domains = operations?.domains
  return (
    <Panel>
      <PanelHeader
        icon={Globe}
        title="Domains & certificates"
        actions={
          <Button variant="ghost" size="xs" asChild>
            <Link href="/proxy/sites">
              Open Proxy <ArrowRight className="size-3" />
            </Link>
          </Button>
        }
      />
      <PanelBody className="space-y-3">
        {!domains || domains.status !== "available" ? (
          <Notice title="Domain evidence unavailable" icon={Globe}>
            {domains?.reason ??
              "Proxy evidence for this deployment could not be read. Open Proxy to check the connection."}
          </Notice>
        ) : domains.domains.length === 0 ? (
          <EmptyState
            icon={Globe}
            title="No public domain"
            description={domains.reason ?? "This release serves no public domain."}
            className="border-0 py-6"
          />
        ) : (
          <ul aria-label="Deployment domains" className="divide-y divide-hairline">
            {domains.domains.map((domain) => {
              const route = ROUTE_LABEL[domain.route] ?? ROUTE_LABEL.unavailable
              const certificate =
                CERTIFICATE_LABEL[domain.certificate] ?? CERTIFICATE_LABEL.unavailable
              return (
                <li key={domain.hostname} className="min-w-0 space-y-2 py-3 first:pt-0 last:pb-0">
                  <div className="flex min-w-0 flex-wrap items-center gap-2">
                    <Link
                      href={`${domain.https ? "https" : "http"}://${domain.hostname}/`}
                      target="_blank"
                      rel="noreferrer noopener"
                      className="inline-flex min-h-9 min-w-0 items-center text-sm font-medium break-all underline-offset-4 focus-ring hover:underline"
                    >
                      {domain.hostname}
                    </Link>
                    <Tag tone={route.tone}>{route.text}</Tag>
                    <Tag tone={certificate.tone}>{certificate.text}</Tag>
                    <Tag>{domain.ownership}</Tag>
                  </div>
                  {domain.servedBy && (
                    <p className="text-xs break-all text-muted-foreground">
                      Served by {domain.servedBy}
                    </p>
                  )}
                  {domain.certificateName && (
                    <p className="text-xs text-muted-foreground">
                      {domain.certificateName}
                      {typeof domain.certificateDaysLeft === "number" &&
                        domain.certificateDaysLeft > 0 && (
                          <> · {domain.certificateDaysLeft} days left</>
                        )}
                    </p>
                  )}
                  <div className="flex min-w-0 flex-wrap gap-3">
                    {domain.deepLink && (
                      <Link
                        href={domain.deepLink}
                        className="inline-flex min-h-9 items-center text-xs underline underline-offset-4 focus-ring"
                      >
                        Open the serving site
                      </Link>
                    )}
                    {domain.certificateLink && (
                      <Link
                        href={domain.certificateLink}
                        className="inline-flex min-h-9 items-center text-xs underline underline-offset-4 focus-ring"
                      >
                        Open the certificate
                      </Link>
                    )}
                  </div>
                </li>
              )
            })}
          </ul>
        )}
      </PanelBody>
    </Panel>
  )
}

const MOUNT_TONE: Record<DeploymentStorageMount["status"], Tone> = {
  present: "success",
  missing: "danger",
  unavailable: "default",
}

const BACKUP_TONE: Record<DeploymentBackupJob["status"], Tone> = {
  present: "success",
  missing: "danger",
  unavailable: "default",
}

export function DeploymentStorage({ operations }: { operations?: DeploymentOperations }) {
  const storage = operations?.storage
  const backups = operations?.backups
  return (
    <Panel>
      <PanelHeader
        icon={Database}
        title="Storage & backups"
        actions={
          <Button variant="ghost" size="xs" asChild>
            <Link href="/backups">
              Open Backups <ArrowRight className="size-3" />
            </Link>
          </Button>
        }
      />
      <PanelBody className="space-y-4">
        <section className="min-w-0 space-y-2" aria-label="Persistent storage">
          <h3 className="text-xs font-medium text-muted-foreground">Persistent storage</h3>
          {!storage || storage.status !== "available" ? (
            <Notice title="Storage evidence unavailable" icon={Database}>
              {storage?.reason ?? "The storage owner could not be read."}
            </Notice>
          ) : storage.mounts.length === 0 ? (
            <EmptyNote>This release declares no persistent storage.</EmptyNote>
          ) : (
            <ul className="divide-y divide-hairline">
              {storage.mounts.map((mount) => (
                <li key={mount.target} className="min-w-0 space-y-1.5 py-2.5 first:pt-0 last:pb-0">
                  <div className="flex min-w-0 flex-wrap items-center gap-2">
                    <span className="min-w-0 font-mono text-xs break-all">{mount.target}</span>
                    <Tag tone={MOUNT_TONE[mount.status]}>{mount.status}</Tag>
                    <Tag>{mount.kind}</Tag>
                    {mount.readOnly && <Tag>read-only</Tag>}
                    <Tag>{mount.ownership}</Tag>
                  </div>
                  <p className="text-xs break-all text-muted-foreground">{mount.source}</p>
                  {mount.detail && <p className="text-xs text-muted-foreground">{mount.detail}</p>}
                  {mount.deepLink && (
                    <Link
                      href={mount.deepLink}
                      className="inline-flex min-h-9 items-center text-xs underline underline-offset-4 focus-ring"
                    >
                      Open {mount.kind === "volume" ? "the volume" : "the path"}
                    </Link>
                  )}
                </li>
              ))}
            </ul>
          )}
        </section>

        <section className="min-w-0 space-y-2 border-t border-hairline pt-3" aria-label="Backups">
          <h3 className="text-xs font-medium text-muted-foreground">Backups</h3>
          {!backups || backups.status !== "available" ? (
            <Notice title="Backup evidence unavailable" icon={Database}>
              {backups?.reason ?? "The Backups module could not be read."}
            </Notice>
          ) : backups.jobs.length === 0 ? (
            <EmptyNote>{backups.reason ?? "This release declares no backup policy."}</EmptyNote>
          ) : (
            <ul className="divide-y divide-hairline">
              {backups.jobs.map((job) => (
                <li
                  key={job.resourceId}
                  className="min-w-0 space-y-1.5 py-2.5 first:pt-0 last:pb-0"
                >
                  <div className="flex min-w-0 flex-wrap items-center gap-2">
                    <span className="text-xs font-medium">Backup job {job.resourceId}</span>
                    <Tag tone={BACKUP_TONE[job.status]}>{job.status}</Tag>
                    {job.required && <Tag tone={job.fresh ? "success" : "warning"}>required</Tag>}
                  </div>
                  <p className="text-xs text-muted-foreground">
                    {job.status === "present"
                      ? `Last run ${job.lastStatus ?? "unknown"}${job.required ? (job.fresh ? " · within maximum age" : " · outside maximum age") : ""}`
                      : (job.detail ?? "No observation was returned.")}
                  </p>
                  {job.deepLink && (
                    <Link
                      href={job.deepLink}
                      className="inline-flex min-h-9 items-center text-xs underline underline-offset-4 focus-ring"
                    >
                      Open the backup job
                    </Link>
                  )}
                </li>
              ))}
            </ul>
          )}
        </section>
      </PanelBody>
    </Panel>
  )
}

export function DeploymentDependencies({ operations }: { operations?: DeploymentOperations }) {
  const dependencies = operations?.dependencies
  if (dependencies?.status === "available" && dependencies.items.length === 0) return null
  return (
    <Panel>
      <PanelHeader icon={Shield} title="Other dependencies" />
      <PanelBody>
        {!dependencies || dependencies.status !== "available" ? (
          <Notice title="Dependency evidence unavailable" icon={Shield}>
            {dependencies?.reason ?? "The owning modules could not be read."}
          </Notice>
        ) : (
          <ul className="divide-y divide-hairline">
            {dependencies.items.map((item) => (
              <li
                key={`${item.resourceKind}:${item.resourceId}`}
                className="min-w-0 space-y-1.5 py-2.5 first:pt-0 last:pb-0"
              >
                <div className="flex min-w-0 flex-wrap items-center gap-2">
                  <span className="min-w-0 text-xs font-medium break-all">
                    {item.resourceKind} {item.resourceId}
                  </span>
                  <Tag tone={item.available ? "success" : "warning"}>
                    {item.available ? (item.status ?? "available") : "unavailable"}
                  </Tag>
                </div>
                {item.detail && <p className="text-xs text-muted-foreground">{item.detail}</p>}
                {item.deepLink && (
                  <Link
                    href={item.deepLink}
                    className="inline-flex min-h-9 items-center text-xs underline underline-offset-4 focus-ring"
                  >
                    Open the owning section
                  </Link>
                )}
              </li>
            ))}
          </ul>
        )}
      </PanelBody>
    </Panel>
  )
}
