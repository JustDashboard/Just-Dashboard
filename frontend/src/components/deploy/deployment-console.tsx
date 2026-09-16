"use client"

import { useState } from "react"
import Link from "next/link"
import { ArrowRight, Box, Terminal } from "@/components/icons"
import { useAuth } from "@/hooks/use-auth"
import type { DeploymentRuntimeServices } from "@/lib/types"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyState, Notice } from "@/components/state"
import { XtermPane } from "@/components/xterm-pane"
import { Button } from "@/components/ui/button"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"

/**
 * A shell inside the running release.
 *
 * It is the Docker owner's audited exec session, opened on the container the
 * runtime observation names, so nothing here invents a second way into a
 * container. The live release is preselected; a Compose stack offers each
 * service. What is typed here lives only as long as the container does — the
 * next release starts from its immutable image again, which is the point.
 */
export function DeploymentConsole({ runtime }: { runtime?: DeploymentRuntimeServices }) {
  const { can } = useAuth()
  const services = runtime?.status === "available" ? runtime.services : []
  const running = services.filter((service) => service.state === "running")
  const preferred = running.find((service) => service.liveRelease) ?? running[0]
  const [selected, setSelected] = useState<string>()
  const service = running.find((item) => item.containerId === selected) ?? preferred

  if (!can("terminal")) {
    return (
      <Panel>
        <PanelHeader title="Console" />
        <PanelBody>
          <Notice title="Terminal access is not granted" icon={Terminal}>
            Opening a shell inside a deployment requires the terminal capability.
          </Notice>
        </PanelBody>
      </Panel>
    )
  }
  if (runtime?.status !== "available") {
    return (
      <Panel>
        <PanelHeader title="Console" />
        <PanelBody>
          <Notice title="Runtime unavailable" icon={Box}>
            {runtime?.reason ??
              "Docker runtime evidence could not be loaded, so there is no container to open."}
          </Notice>
        </PanelBody>
      </Panel>
    )
  }
  if (!service) {
    return (
      <Panel>
        <PanelHeader title="Console" />
        <PanelBody>
          <EmptyState
            icon={Terminal}
            title="No running container"
            description="A shell opens inside the live release once a deployment is running. Deploy first, or open the Runtime tab to see what Docker reports."
            className="border-0 py-6"
            action={
              <Button size="sm" variant="outline" asChild>
                <Link href="?tab=runtime">
                  Runtime <ArrowRight className="size-3.5" />
                </Link>
              </Button>
            }
          />
        </PanelBody>
      </Panel>
    )
  }
  return (
    <Panel className="flex min-h-[32rem] flex-col">
      <PanelHeader
        title="Console"
        actions={
          running.length > 1 ? (
            <Select value={service.containerId} onValueChange={setSelected}>
              <SelectTrigger size="sm" className="w-56" aria-label="Console container">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {running.map((item) => (
                  <SelectItem key={item.containerId} value={item.containerId}>
                    {item.service || item.name}
                    {item.liveRelease ? " · live" : ""}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          ) : (
            <span className="font-mono text-xs text-muted-foreground">{service.name}</span>
          )
        }
      />
      <PanelBody className="flex min-h-0 flex-1 flex-col gap-3 p-3">
        <p className="text-xs text-muted-foreground">
          Commands run inside <span className="font-mono">{service.name}</span>, not on the host.
          Changes made here disappear when the next release starts from its image.
        </p>
        <XtermPane
          key={service.containerId}
          path={`/docker/containers/${service.containerId}/exec`}
          query={{ rows: 30, cols: 100 }}
          className="min-h-96 flex-1"
          subtitle={`${service.name} · deployment shell`}
        />
      </PanelBody>
    </Panel>
  )
}
