"use client"

import { plural } from "@/lib/format"
import { useBelow } from "@/lib/breakpoints"
import { ViewTabList } from "@/components/tabs"
import { ProjectMark } from "@/components/project-mark"
import { WorkbenchHeader } from "@/components/workbench-header"
import { useProject } from "@/components/deploy/project-context"
import { EnvironmentSwitch } from "@/components/deploy/environment-switch"
import { ProjectStatus, deploymentURL, hostOf } from "@/components/deploy/vocabulary"
import type { LogsView } from "@/components/deploy/logs-model"

export const VIEW_LABEL: Record<LogsView, string> = {
  requests: "Requests",
  insights: "Insights",
  output: "Output",
  events: "Events",
}

/**
 * The one strip over a deployment's logs.
 *
 * The project's header stood here: the tile, the address, the state, two
 * commands, a line of six facts — and under it four reading tiles and an
 * alerts line, before the first request. Every visit paid for that in rows of
 * logs pushed under the fold, to read again what the Overview says. The page
 * is a workbench now (§ workbench), and this is its strip: the way back to
 * the project, what the project is and how it is, the views, and the
 * environment where there are two. Everything the header said is one press
 * away, on the Overview the arrow leads to.
 *
 * The views are the strip's tools at a desk. On a phone they are the stops of
 * the bar along the pane's foot, so the strip keeps only the way back, the
 * name and its state.
 */
export function LogsStrip({
  view,
  disruptions,
}: {
  view: LogsView
  /** The container's exits and restarts in the last hour, counted on Events. */
  disruptions: number
}) {
  const project = useProject()
  const { deployment, project: record, runtime } = project.detail
  const url = deploymentURL(deployment.endpoint)
  // Chosen once by width, so the views are in the document once: here at a
  // desk, on the bar at the pane's foot on a phone.
  const phone = useBelow("md")
  return (
    <WorkbenchHeader
      back={{ href: `/deploy/${project.projectId}`, label: `Back to ${record.name}` }}
      // The project's name, as every page of the project says it: the rail
      // and the tab add "Logs" after it.
      heading={record.name}
      mark={<ProjectMark deployment={deployment} product={project.product} size="sm" />}
      name={
        <span className="flex min-w-0 items-baseline gap-2">
          <span className="truncate">{record.name}</span>
          {url && (
            <span className="truncate font-mono text-hint font-normal text-muted-foreground max-md:hidden">
              {hostOf(url)}
            </span>
          )}
        </span>
      }
      state={
        <ProjectStatus
          summary={deployment}
          runtime={runtime}
          archived={project.archived}
          live={Boolean(deployment.activeRun)}
          className="text-hint"
        />
      }
      tools={
        phone ? undefined : (
          // Inside the `Tabs` the page holds, whose one panel is the view on
          // screen. The container's disruptions are a count beside Events
          // (§4); none on the others, whose window is the view's own, and a
          // count that disagrees with the rows under it is worse than none.
          <ViewTabList
            label="Log view"
            size="sm"
            value={view}
            className="border-b-0"
            views={(Object.keys(VIEW_LABEL) as LogsView[]).map((id) => ({
              key: id,
              // The count is the tab's title rather than its name, so the
              // tab is "Events" to a screen reader and a test alike, and the
              // figure is said where the pointer asks for it.
              label:
                id === "events" && disruptions > 0 ? (
                  <>
                    {VIEW_LABEL[id]}
                    <span
                      aria-hidden
                      title={`${plural(disruptions, "exit or restart", "exits or restarts")} in the last hour`}
                      className="numeric text-hint text-warning"
                    >
                      {disruptions}
                    </span>
                  </>
                ) : (
                  VIEW_LABEL[id]
                ),
            }))}
          />
        )
      }
      command={project.staging ? <EnvironmentSwitch /> : undefined}
    />
  )
}
