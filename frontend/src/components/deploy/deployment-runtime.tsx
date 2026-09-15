"use client"

import Link from "next/link"
import { Box, Layers, Logs } from "@/components/icons"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyState, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { relativeTime } from "@/lib/format"
import type { DeploymentRuntimeServices } from "@/lib/types"

export function DeploymentRuntime({ runtime }: { runtime?: DeploymentRuntimeServices }) {
  return (
    <Panel>
      <PanelHeader
        title="Runtime services"
        actions={
          <Button variant="ghost" size="sm" asChild>
            <Link href="/docker/containers">Open Docker</Link>
          </Button>
        }
      />
      <PanelBody>
        {runtime?.status !== "available" ? (
          <Notice title="Runtime unavailable" icon={Box}>
            {runtime?.reason ??
              "Docker runtime evidence could not be loaded. Open Docker to check the connection."}
          </Notice>
        ) : runtime.services.length === 0 ? (
          <EmptyState
            icon={Box}
            title="No managed runtime services"
            description="Docker returned no managed containers for this environment. Observed imports remain under Docker until managed deployment creates a runtime."
            className="border-0 py-6"
          />
        ) : (
          <ul aria-label="Runtime services" className="divide-y divide-hairline">
            {runtime.services.map((service) => (
              <li key={service.containerId} className="min-w-0 space-y-2 py-3 first:pt-0 last:pb-0">
                <div className="flex min-w-0 flex-wrap items-center gap-2">
                  <Link
                    href={`/docker/containers?${new URLSearchParams({ container: service.containerId })}`}
                    className="inline-flex min-h-9 min-w-0 items-center text-sm font-medium break-all underline-offset-4 focus-ring hover:underline"
                  >
                    {service.name || service.containerId}
                  </Link>
                  <Tag>{service.liveRelease ? "Live release" : "Other release"}</Tag>
                  <Status state={service.state} />
                </div>
                <p className="text-xs text-muted-foreground">
                  Health: {service.health === "unavailable" ? "Not observed" : service.health}
                  {service.startedAt && <> · Started {relativeTime(service.startedAt)}</>}
                </p>
                <div className="flex flex-wrap items-center gap-2">
                  <Button variant="outline" size="sm" asChild>
                    <Link
                      href={`?${new URLSearchParams({ tab: "logs", service: service.containerId })}`}
                      aria-label={`Open runtime logs for ${service.name || service.containerId}`}
                    >
                      <Logs className="size-3.5" /> Runtime logs
                    </Link>
                  </Button>
                  {service.stack && (
                    <Button variant="ghost" size="sm" asChild>
                      <Link
                        href={`/docker/stacks?${new URLSearchParams({ stack: service.stack })}`}
                        aria-label={`Open stack ${service.stack}${service.service ? ` · ${service.service}` : ""}`}
                      >
                        <Layers className="size-3.5" /> Open stack
                      </Link>
                    </Button>
                  )}
                </div>
              </li>
            ))}
          </ul>
        )}
      </PanelBody>
    </Panel>
  )
}
