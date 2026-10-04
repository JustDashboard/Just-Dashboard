"use client"

import Link from "next/link"
import { useProject } from "@/components/deploy/project-context"
import { importedWorkloadOf } from "@/components/deploy/imported-workload"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { Button } from "@/components/ui/button"
import { Status, toneFor } from "@/components/status-dot"
import { workloadManagerUrl, workloadPort } from "@/lib/workload-import"

const MANAGERS = {
  stack: "Docker Compose",
  container: "Docker",
  pm2: "PM2",
  systemd: "systemd",
  process: "Host process",
}

export function ImportedProject() {
  const project = useProject()
  const workload = importedWorkloadOf(project.detail.deployment)
  if (!workload)
    return (
      <Panel plain>
        <PanelHeader title="Imported workload" />
        <PanelBody>
          <p className="text-body text-muted-foreground">
            This project observes an existing workload. Its original manager keeps its configuration
            and controls how it runs.
          </p>
        </PanelBody>
      </Panel>
    )

  const available = !["missing", "unavailable"].includes(workload.state)
  const managerUrl = workloadManagerUrl(workload)
  return (
    <>
      <StatGrid>
        <StatTile label="Running" value={available ? workload.running : "—"} />
        <StatTile label="Services" value={workload.total} />
        <StatTile label="Manager" value={MANAGERS[workload.kind]} />
        <StatTile label="Imported" value="In place" />
      </StatGrid>
      <Panel plain>
        <PanelHeader
          title="Original workload"
          actions={
            managerUrl && (
              <Button variant="outline" size="sm" asChild>
                <Link href={managerUrl}>Open {MANAGERS[workload.kind]}</Link>
              </Button>
            )
          }
        />
        <PanelBody className="space-y-4">
          <p className="text-body text-muted-foreground">
            Your existing files, environment, ports, networks and persistent data stay with{" "}
            {MANAGERS[workload.kind]}. Importing added this project without restarting it. Manage
            its configuration and lifecycle through its original manager.
          </p>
          {workload.sourcePath && (
            <p className="font-mono text-body wrap-anywhere">{workload.sourcePath}</p>
          )}
          {workload.warnings.length > 0 && (
            <ul className="space-y-2 text-body text-muted-foreground">
              {workload.warnings.map((warning) => (
                <li key={warning}>{warning}</li>
              ))}
            </ul>
          )}
        </PanelBody>
      </Panel>
      <Panel plain>
        <PanelHeader title="Services" />
        <PanelBody>
          <ul className="divide-y divide-hairline">
            {workload.services.map((service) => (
              <li
                key={service.resourceId || service.name}
                className="flex min-w-0 flex-wrap items-center justify-between gap-3 py-3"
              >
                <div className="min-w-0 space-y-1">
                  <p className="text-body font-medium wrap-anywhere">{service.name}</p>
                  {service.image && (
                    <p className="font-mono text-xs wrap-anywhere text-muted-foreground">
                      {service.image}
                    </p>
                  )}
                  {service.pid && (
                    <p className="numeric text-xs text-muted-foreground">PID {service.pid}</p>
                  )}
                  {service.ports?.map((port) => (
                    <p
                      key={`${port.hostPort}:${port.containerPort}:${port.protocol}`}
                      className="font-mono text-xs text-muted-foreground"
                    >
                      {workloadPort(port)}
                    </p>
                  ))}
                </div>
                <Status
                  tone={
                    available
                      ? service.health === "unhealthy"
                        ? "danger"
                        : toneFor(service.state)
                      : "unknown"
                  }
                  label={
                    available
                      ? service.health
                        ? `${service.state} · ${service.health}`
                        : service.state || "Not observed"
                      : "Not observed"
                  }
                />
              </li>
            ))}
          </ul>
        </PanelBody>
      </Panel>
    </>
  )
}
