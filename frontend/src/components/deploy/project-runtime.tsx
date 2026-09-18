"use client"

import { useCallback, useState } from "react"
import { useRouter } from "next/navigation"
import Link from "next/link"
import { Box, External, Layers, Logs, Terminal } from "@/components/icons"
import { ContainerUsage } from "@/components/docker/container-usage"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyNote, EmptyState, Notice } from "@/components/state"
import { Status, type DotTone } from "@/components/status-dot"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { Tag } from "@/components/tag"
import { VerbActions, type Verb } from "@/components/verbs"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { useSocket, type Envelope } from "@/hooks/use-socket"
import { bytes, percent, relativeTime } from "@/lib/format"
import type {
  ContainerStats,
  DeploymentBackupJob,
  DeploymentDomainRoute,
  DeploymentRuntimeService,
  DeploymentStorageMount,
} from "@/lib/types"
import { useProject } from "@/components/deploy/project-context"

/** Present/missing/unavailable, the shared vocabulary of a mount and a backup job. */
export const PRESENCE_TONE: Record<"present" | "missing" | "unavailable", DotTone> = {
  present: "running",
  missing: "danger",
  unavailable: "unknown",
}

export const ROUTE_TONE: Record<DeploymentDomainRoute["route"], DotTone> = {
  served: "running",
  missing: "warning",
  foreign: "danger",
  conflict: "danger",
  unavailable: "unknown",
}

export const ROUTE_LABEL: Record<DeploymentDomainRoute["route"], string> = {
  served: "Routed here",
  missing: "No route",
  foreign: "Another site",
  conflict: "Conflict",
  unavailable: "Not observed",
}

export const CERTIFICATE_TONE: Record<DeploymentDomainRoute["certificate"], DotTone> = {
  valid: "running",
  expiring: "warning",
  expired: "danger",
  missing: "warning",
  "not requested": "stopped",
  unavailable: "unknown",
}

export const CERTIFICATE_LABEL: Record<DeploymentDomainRoute["certificate"], string> = {
  valid: "Certificate valid",
  expiring: "Certificate expiring",
  expired: "Certificate expired",
  missing: "No certificate",
  "not requested": "HTTP only",
  unavailable: "Not observed",
}

/** One domain's reading, shared with the overview's facts column. */
export function CertificateStatus({ domain }: { domain: DeploymentDomainRoute }) {
  return (
    <Status
      tone={CERTIFICATE_TONE[domain.certificate]}
      label={CERTIFICATE_LABEL[domain.certificate]}
    />
  )
}

function SilenceRow({
  subjects,
  silences,
}: {
  subjects: string[]
  silences: { subject: string; reason: string }[]
}) {
  const matches = silences.filter((silence) => subjects.includes(silence.subject))
  if (matches.length === 0) return null
  return (
    <p className="py-2.5 text-hint text-muted-foreground first:pt-0">
      <span className="font-medium text-foreground">Not assessed.</span>{" "}
      {matches.map((silence) => silence.reason).join(" ")}
    </p>
  )
}

/**
 * Everything Docker and the operational owners say about the live release:
 * its containers, their live usage, and the routes, storage, backups and
 * dependencies it declares. The old Diagnostics tab minus the findings, which
 * moved to the overview — this page is evidence, not verdicts.
 */
export function ProjectRuntime() {
  const project = useProject()
  const router = useRouter()
  const { runtime } = project.detail
  const services = runtime?.status === "available" ? runtime.services : []
  const running = services.filter((service) => service.state === "running")
  const [picked, setPicked] = useState<string>()
  const selected =
    (picked && services.find((service) => service.containerId === picked)) ||
    running.find((service) => service.liveRelease) ||
    running[0]

  const [stats, setStats] = useState<ContainerStats | null>(null)
  const onMessage = useCallback((message: Envelope) => {
    if (message.type === "stats") setStats(message.data as ContainerStats)
  }, [])
  const socket = useSocket(`/docker/containers/${selected?.containerId}/stats/stream`, {
    onMessage,
    enabled: Boolean(selected),
  })
  const live = socket.state === "open"

  const diagnosis = project.operations?.diagnosis
  const silences = diagnosis?.silences ?? []
  const domains = project.operations?.domains
  const storage = project.operations?.storage
  const backups = project.operations?.backups
  const dependencies = project.operations?.dependencies

  return (
    <div className="space-y-6">
      <Panel plain>
        <PanelHeader title="Services" />
        <PanelBody flush>
          {runtime?.status !== "available" ? (
            <Notice title="Runtime unavailable" icon={Box} className="m-4">
              {runtime?.reason ??
                "Docker runtime evidence could not be loaded. Open Docker to check the connection."}
            </Notice>
          ) : services.length === 0 ? (
            <EmptyState
              icon={Box}
              title="No managed runtime services"
              description="Docker returned no managed containers for this environment. Observed imports remain under Docker until managed deployment creates a runtime."
              className="m-4 border-0 py-6"
            />
          ) : (
            <>
              <ul aria-label="Runtime services" className="divide-y divide-hairline">
                {services.map((service) => (
                  <ServiceRow
                    key={service.containerId}
                    service={service}
                    projectId={project.projectId}
                    live={live && selected?.containerId === service.containerId}
                    onOpenConsole={() =>
                      router.push(
                        `/deploy/${project.projectId}/console?service=${service.containerId}`,
                      )
                    }
                  />
                ))}
              </ul>
              {silences.length > 0 && (
                <div className="px-5">
                  <SilenceRow subjects={["runtime"]} silences={silences} />
                </div>
              )}
            </>
          )}
        </PanelBody>
      </Panel>

      {selected && (
        <>
          <Panel plain>
            <PanelHeader
              title="Resource usage"
              actions={
                services.length > 1 && (
                  <Select value={selected.containerId} onValueChange={setPicked}>
                    <SelectTrigger size="sm" className="w-56" aria-label="Runtime usage service">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      {services.map((service) => (
                        <SelectItem key={service.containerId} value={service.containerId}>
                          {service.name || service.containerId}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                )
              }
            />
            <PanelBody>
              <StatGrid columns={4}>
                <StatTile
                  label="CPU · 100% = 1 core"
                  value={stats ? percent(stats.cpuPercent) : "—"}
                />
                <StatTile
                  label="Memory"
                  value={stats ? bytes(stats.memUsage) : "—"}
                  hint={stats?.memLimited ? `of ${bytes(stats.memLimit)}` : "No container limit"}
                />
                <StatTile label="Processes" value={stats ? String(stats.pids) : "—"} />
                <StatTile
                  label="Last sample"
                  value={stats ? new Date(stats.ts).toLocaleTimeString() : "—"}
                />
              </StatGrid>
            </PanelBody>
          </Panel>
          <ContainerUsage
            key={selected.containerId}
            containerId={selected.containerId}
            name={selected.name || selected.containerId}
          />
        </>
      )}

      <Panel plain>
        <PanelHeader title="Domains" />
        <PanelBody flush>
          {!domains || domains.status !== "available" ? (
            <Notice title="Domain evidence unavailable" icon={External} className="m-4">
              {domains?.reason ?? "Proxy evidence for this deployment could not be read."}
            </Notice>
          ) : domains.domains.length === 0 ? (
            <EmptyNote>This release serves no public domain.</EmptyNote>
          ) : (
            <ul aria-label="Deployment domains" className="divide-y divide-hairline px-5">
              {domains.domains.map((domain) => (
                <li key={domain.hostname} className="min-w-0 space-y-1.5 py-3 first:pt-0 last:pb-0">
                  <div className="flex min-w-0 flex-wrap items-center gap-2">
                    <a
                      href={`${domain.https ? "https" : "http"}://${domain.hostname}/`}
                      target="_blank"
                      rel="noreferrer noopener"
                      className="min-w-0 truncate text-body font-medium underline-offset-4 focus-ring hover:underline"
                    >
                      {domain.hostname}
                    </a>
                    <Status tone={ROUTE_TONE[domain.route]} label={ROUTE_LABEL[domain.route]} />
                    <CertificateStatus domain={domain} />
                    <Tag>{domain.ownership}</Tag>
                    {typeof domain.certificateDaysLeft === "number" &&
                      domain.certificateDaysLeft > 0 && (
                        <span className="numeric text-hint text-muted-foreground">
                          {domain.certificateDaysLeft}d left
                        </span>
                      )}
                  </div>
                  <div className="flex min-w-0 flex-wrap gap-3">
                    {domain.deepLink && (
                      <Link
                        href={domain.deepLink}
                        className="text-hint underline underline-offset-4 focus-ring"
                      >
                        Open the serving site
                      </Link>
                    )}
                    {domain.certificateLink && (
                      <Link
                        href={domain.certificateLink}
                        className="text-hint underline underline-offset-4 focus-ring"
                      >
                        Open the certificate
                      </Link>
                    )}
                  </div>
                </li>
              ))}
              {silences.length > 0 && (
                <li>
                  <SilenceRow subjects={["domains", "certificates"]} silences={silences} />
                </li>
              )}
            </ul>
          )}
        </PanelBody>
      </Panel>

      <Panel plain>
        <PanelHeader title="Storage & backups" />
        <PanelBody className="space-y-4">
          <section aria-label="Persistent storage" className="min-w-0 space-y-1">
            <p className="mb-1 text-xs font-medium text-muted-foreground">Persistent storage</p>
            {!storage || storage.status !== "available" ? (
              <Notice title="Storage evidence unavailable" icon={Box}>
                {storage?.reason ?? "The storage owner could not be read."}
              </Notice>
            ) : storage.mounts.length === 0 ? (
              <EmptyNote>This release declares no persistent storage.</EmptyNote>
            ) : (
              <ul className="divide-y divide-hairline">
                {storage.mounts.map((mount) => (
                  <MountRow key={mount.target} mount={mount} />
                ))}
              </ul>
            )}
            <SilenceRow subjects={["storage"]} silences={silences} />
          </section>
          <section aria-label="Backups" className="min-w-0 space-y-1 border-t border-hairline pt-3">
            <p className="mb-1 text-xs font-medium text-muted-foreground">Backups</p>
            {!backups || backups.status !== "available" ? (
              <Notice title="Backup evidence unavailable" icon={Box}>
                {backups?.reason ?? "The Backups module could not be read."}
              </Notice>
            ) : backups.jobs.length === 0 ? (
              <EmptyNote>This release declares no backup policy.</EmptyNote>
            ) : (
              <ul className="divide-y divide-hairline">
                {backups.jobs.map((job) => (
                  <BackupRow key={job.resourceId} job={job} />
                ))}
              </ul>
            )}
            <SilenceRow subjects={["backups"]} silences={silences} />
          </section>
        </PanelBody>
      </Panel>

      {(!dependencies || dependencies.status !== "available" || dependencies.items.length > 0) && (
        <Panel plain>
          <PanelHeader title="Other dependencies" />
          <PanelBody flush>
            {!dependencies || dependencies.status !== "available" ? (
              <Notice title="Dependency evidence unavailable" icon={Box} className="m-4">
                {dependencies?.reason ?? "The owning modules could not be read."}
              </Notice>
            ) : (
              <ul className="divide-y divide-hairline px-5">
                {dependencies.items.map((item) => (
                  <li
                    key={`${item.resourceKind}:${item.resourceId}`}
                    className="min-w-0 space-y-1 py-3 first:pt-0 last:pb-0"
                  >
                    <div className="flex min-w-0 flex-wrap items-center gap-2">
                      <span className="min-w-0 truncate text-body font-medium">
                        {item.resourceKind} {item.resourceId}
                      </span>
                      <Status
                        tone={item.available ? "running" : "warning"}
                        label={item.available ? (item.status ?? "Available") : "Unavailable"}
                      />
                    </div>
                    {item.detail && (
                      <p className="text-hint text-muted-foreground">{item.detail}</p>
                    )}
                    {item.deepLink && (
                      <Link
                        href={item.deepLink}
                        className="text-hint underline underline-offset-4 focus-ring"
                      >
                        Open the owning section
                      </Link>
                    )}
                  </li>
                ))}
                {silences.length > 0 && (
                  <li>
                    <SilenceRow subjects={["dependencies"]} silences={silences} />
                  </li>
                )}
              </ul>
            )}
          </PanelBody>
        </Panel>
      )}
    </div>
  )
}

function ServiceRow({
  service,
  projectId,
  live,
  onOpenConsole,
}: {
  service: DeploymentRuntimeService
  projectId: number
  live: boolean
  onOpenConsole: () => void
}) {
  const router = useRouter()
  const verbs: Verb[] = [
    {
      key: "logs",
      label: "Logs",
      detail: "This service's runtime logs.",
      icon: Logs,
      run: () => router.push(`/deploy/${projectId}/logs?service=${service.containerId}`),
    },
    {
      key: "console",
      label: "Console",
      detail: "Open a shell inside this container.",
      icon: Terminal,
      run: onOpenConsole,
    },
  ]
  if (service.stack) {
    verbs.push({
      key: "stack",
      label: "Open stack",
      detail: "The Compose stack this service belongs to.",
      icon: Layers,
      run: () => router.push(`/docker/stacks?stack=${service.stack}`),
    })
  }
  verbs.push({
    key: "docker",
    label: "Open in Docker",
    detail: "This container, as Docker sees it.",
    icon: External,
    run: () => router.push(`/docker/containers?container=${service.containerId}`),
  })
  return (
    <li className="min-w-0 space-y-1.5 px-5 py-3">
      <div className="flex min-w-0 flex-wrap items-center gap-2">
        <Link
          href={`/docker/containers?container=${service.containerId}`}
          className="min-w-0 truncate text-body font-medium underline-offset-4 focus-ring hover:underline"
        >
          {service.name || service.containerId}
        </Link>
        <Tag tone={service.liveRelease ? "success" : "default"}>
          {service.liveRelease ? "Live release" : "Other release"}
        </Tag>
        <Status state={service.state} live={live} />
        <VerbActions
          reveal
          verbs={verbs}
          menuLabel={`Actions for ${service.name || service.containerId}`}
          className="ml-auto"
        />
      </div>
      <p className="truncate text-hint text-muted-foreground">
        {service.imageId} · Health:{" "}
        {service.health === "unavailable" ? "Not observed" : service.health}
        {service.startedAt && <> · Started {relativeTime(service.startedAt)}</>}
      </p>
    </li>
  )
}

function MountRow({ mount }: { mount: DeploymentStorageMount }) {
  return (
    <li className="min-w-0 space-y-1.5 py-2.5 first:pt-0 last:pb-0">
      <div className="flex min-w-0 flex-wrap items-center gap-2">
        <span className="min-w-0 truncate font-mono text-xs">{mount.target}</span>
        <Status tone={PRESENCE_TONE[mount.status]} label={mount.status} />
        <Tag>{mount.kind}</Tag>
        {mount.readOnly && <Tag>read-only</Tag>}
        <Tag>{mount.ownership}</Tag>
      </div>
      <p className="truncate text-hint text-muted-foreground">{mount.source}</p>
      {mount.detail && <p className="text-hint text-muted-foreground">{mount.detail}</p>}
      {mount.deepLink && (
        <Link href={mount.deepLink} className="text-hint underline underline-offset-4 focus-ring">
          Open {mount.kind === "volume" ? "the volume" : "the path"}
        </Link>
      )}
    </li>
  )
}

function BackupRow({ job }: { job: DeploymentBackupJob }) {
  return (
    <li className="min-w-0 space-y-1.5 py-2.5 first:pt-0 last:pb-0">
      <div className="flex min-w-0 flex-wrap items-center gap-2">
        <span className="text-body font-medium">Backup job {job.resourceId}</span>
        <Status tone={PRESENCE_TONE[job.status]} label={job.status} />
        {job.required && <Tag tone={job.fresh ? "success" : "warning"}>required</Tag>}
      </div>
      <p className="text-hint text-muted-foreground">
        {job.status === "present"
          ? `Last run ${job.lastStatus ?? "unknown"}${job.required ? (job.fresh ? " · within maximum age" : " · outside maximum age") : ""}`
          : (job.detail ?? "No observation was returned.")}
      </p>
      {job.deepLink && (
        <Link href={job.deepLink} className="text-hint underline underline-offset-4 focus-ring">
          Open the backup job
        </Link>
      )}
    </li>
  )
}
