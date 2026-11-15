"use client"

import { get } from "@/lib/api"
import { usePoll } from "@/hooks/use-poll"
import type { DeploymentFleet, DeploymentSummary } from "@/lib/types"
import { CloudUpload, Rocket } from "@/components/icons"
import { ProductLogo } from "@/components/product-logo"
import { ProjectMark } from "@/components/deploy/project-mark"
import { WirePlaceholder } from "@/components/deploy/wire"
import type { WireFlowNode, WireLine } from "@/components/wire-flow"

/**
 * What the section pages beside Notifications share in their pictures: the
 * projects of this server as nodes, each drawn as itself, wired to the server
 * in the middle. Log drains and Notifications put them on the left, where
 * things come from; Secret stores and Shared variables on the right, where a
 * value ends up.
 */

/**
 * The fleet, for the projects in a picture: asked once a minute, as
 * Notifications asks. `waiting` is true only until its first answer, error
 * included, so a picture is never held back by a fleet that cannot be read.
 */
export function useFleet(): { projects: DeploymentSummary[]; waiting: boolean } {
  const fleet = usePoll(
    (signal) => get<DeploymentFleet>("/deploy/", { view: "fleet" }, signal),
    60000,
    [],
  )
  return { projects: fleet.data?.deployments ?? EMPTY, waiting: fleet.loading && !fleet.data }
}

const EMPTY: DeploymentSummary[] = []

export const projectNodeId = (id: number) => `project:${id}`

/**
 * One project as a node. `deployment` is its summary where the fleet has it;
 * a project the fleet has not answered for yet, or no longer lists, keeps the
 * tile with the rocket in it, so the column does not change shape when the
 * fleet lands. Its environment stands over its name, so the node is the three
 * lines a waiting picture holds room for (`WireFlow`'s `loading`).
 */
export function projectNode({
  id,
  name,
  deployment,
  hint,
  line,
}: {
  id: number
  name: string
  deployment?: DeploymentSummary
  hint?: React.ReactNode
  line?: WireLine
}): WireFlowNode {
  return {
    id: projectNodeId(id),
    mark: (size) =>
      deployment ? (
        <ProjectMark deployment={deployment} size={size === "sm" ? "sm" : "md"} />
      ) : (
        <ProductLogo size={size === "sm" ? "sm" : "md"} fallback={Rocket} />
      ),
    eyebrow: deployment?.environmentName ?? "Project",
    title: name,
    name,
    hint,
    href: `/deploy/${id}`,
    label: `Open ${name}`,
    line: line ?? { state: "up" },
  }
}

/** The dashed ring where the first project will stand, before there is one. */
export function noProjectNode(hint: string): WireFlowNode {
  return {
    id: "project:none",
    placeholder: true,
    mark: (size) => (
      <WirePlaceholder size={size}>
        <CloudUpload />
      </WirePlaceholder>
    ),
    title: <span className="font-normal text-muted-foreground">No projects yet</span>,
    name: "No projects yet",
    hint,
    line: { state: "idle" },
  }
}
