"use client"

import { useState } from "react"
import Link from "next/link"
import { useSearchParams } from "next/navigation"
import { ArrowRight, Box, Terminal } from "@/components/icons"
import { useAuth } from "@/hooks/use-auth"
import { Pane, PaneFooter, PaneHeader, Panel, PanelBody, PanelHeader } from "@/components/panel"
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
import { useProject } from "@/components/deploy/project-context"
import { GameConsole } from "@/components/deploy/game/console"

/**
 * A shell inside the running release — or, for a game server, the game's own
 * console — never a second way into a container the Docker owner does not
 * already audit.
 */
export function ProjectConsole() {
  const project = useProject()
  if (project.detail.deployment.profile === "game") {
    return <GameConsole projectId={project.projectId} />
  }
  return <DockerConsole />
}

function DockerConsole() {
  const { can } = useAuth()
  const project = useProject()
  const search = useSearchParams()
  const { runtime } = project.detail
  const services = runtime?.status === "available" ? runtime.services : []
  const running = services.filter((service) => service.state === "running")
  const preferred = running.find((service) => service.liveRelease) ?? running[0]
  const [selected, setSelected] = useState<string | undefined>(
    () => search.get("service") ?? undefined,
  )
  const service = running.find((item) => item.containerId === selected) ?? preferred

  if (!can("terminal")) {
    return (
      <Panel plain>
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
      <Panel plain>
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
      <Panel plain>
        <PanelHeader title="Console" />
        <PanelBody>
          <EmptyState
            icon={Terminal}
            title="No running container"
            description="A shell opens inside the live release once a deployment is running. Deploy first, or open the Runtime tab to see what Docker reports."
            action={
              <Button size="sm" variant="outline" asChild>
                <Link href={`/deploy/${project.projectId}/runtime`}>
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
    <Pane className="min-h-[32rem]">
      <PaneHeader>
        <span className="min-w-0 flex-1" />
        {running.length > 1 ? (
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
        )}
      </PaneHeader>
      <XtermPane
        key={service.containerId}
        flush
        path={`/docker/containers/${service.containerId}/exec`}
        query={{ rows: 30, cols: 100 }}
        className="min-h-96 flex-1"
        subtitle={`${service.name} · deployment shell`}
      />
      <PaneFooter>
        <p className="text-hint text-muted-foreground">
          Commands run inside <span className="font-mono">{service.name}</span>, not on the host.
          Changes made here disappear when the next release starts from its image.
        </p>
      </PaneFooter>
    </Pane>
  )
}
