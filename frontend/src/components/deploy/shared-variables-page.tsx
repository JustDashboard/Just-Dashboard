"use client"

import { useCallback } from "react"
import Link from "next/link"
import { plural } from "@/lib/format"
import type { DeploymentSharedVariable, DeploymentSummary } from "@/lib/types"
import { Code, LockClosed, Plus } from "@/components/icons"
import { Page, PageContext } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ProductGlyph, hasProductLogo, variableProduct } from "@/components/product-logo"
import { WireFlow, type WireFlowNode } from "@/components/wire-flow"
import { WireHost, WireMark, WirePlaceholder } from "@/components/deploy/wire"
import { projectNode, projectNodeId, useFleet } from "@/components/deploy/section-picture"
import { SharedVariables } from "@/components/deploy/shared-variables"
import {
  notLiveYet,
  sharedCount,
  sharedReaders,
} from "@/components/deploy/shared-variables-reading"

/**
 * The server's shared variables, under Deployments. The list is the one a
 * project's Variables settings draws (`SharedVariables`); this page puts a
 * picture over it, as Notifications beside it has, and the count on its head.
 */
export function SharedVariablesPage() {
  const fleet = useFleet()
  return (
    <Page>
      <PageContext
        eyebrow={
          <Link href="/deploy" className="rounded-sm focus-ring hover:underline">
            Deployments
          </Link>
        }
        title="Shared variables"
      />
      <SharedVariables
        scope={{ kind: "server" }}
        frame={({ count, variables, failed, actions, body }) => (
          <>
            {/* Before the first variable there is nothing to wire, and the
                list's own empty state says how to add one: no picture then. */}
            {count !== 0 && !failed && (
              <div className="mx-auto w-full max-w-5xl lg:py-2">
                <ReachPicture
                  variables={variables}
                  projects={fleet.projects}
                  loading={!variables || fleet.waiting}
                />
              </div>
            )}
            <Panel plain>
              <PanelHeader
                title="Server variables"
                actions={
                  <span className="flex flex-wrap items-center gap-3">
                    {variables && variables.length > 0 && (
                      <span className="numeric text-hint text-muted-foreground">
                        {sharedCount(variables)}
                      </span>
                    )}
                    {actions}
                  </span>
                }
              />
              <PanelBody>{body}</PanelBody>
            </Panel>
          </>
        )}
      />
    </Page>
  )
}

// The variables the picture has room for; past it the rest are one "N more"
// node and the list under it names them all.
const PICTURE_VARIABLES = 4

/**
 * What a server variable reaches: each variable on one side, drawn as the
 * service its name says holds it or as a lock or brackets, this server in the
 * middle (it resolves a reference when a run starts), and on the other side
 * the projects whose variables read one, each with how many of them do.
 *
 * A variable's line is solid once something reads it and dashed while
 * nothing does. A project's line is dashed while one of its readers still
 * runs an earlier value: the new one is saved and has not arrived, which the
 * list says as "not live yet". Nothing pulses: a value travels only when a
 * run starts. Pointing at a variable steps back the projects that do not
 * read it.
 *
 * On a phone the list under it names every variable, so the picture is the
 * marks alone: the variables over this server over the projects.
 */
function ReachPicture({
  variables,
  projects,
  loading,
}: {
  variables: DeploymentSharedVariable[] | undefined
  projects: DeploymentSummary[]
  loading: boolean
}) {
  const list = variables ?? NO_VARIABLES
  const readers = sharedReaders(list)
  const related = useCallback(
    (focus: string, id: string) => {
      const reads = (variableId: string, projectId: string) =>
        list.some(
          (variable) =>
            variableNodeId(variable.name) === variableId &&
            variable.usedBy.some((use) => projectNodeId(use.projectId) === projectId),
        )
      return reads(focus, id) || reads(id, focus)
    },
    [list],
  )
  const sources: WireFlowNode[] = list.map((variable) => {
    const product = variableProduct(variable.name)
    const Glyph = variable.sensitivity === "secret" ? LockClosed : Code
    const read = variable.usedBy.length
    return {
      id: variableNodeId(variable.name),
      mark: (size) => (
        <WireMark tone="logo" size={size}>
          {hasProductLogo(product) ? <ProductGlyph id={product} /> : <Glyph />}
        </WireMark>
      ),
      title: <span className="font-mono break-all">{variable.name}</span>,
      name: variable.name,
      hint: `${variable.sensitivity === "secret" ? "secret" : "config"} · ${
        read === 0 ? "not read" : `read by ${read}`
      }${notLiveYet(variable) ? " · not live yet" : ""}`,
      line: read === 0 ? { state: "idle" } : { state: "up" },
    }
  })
  const targets: WireFlowNode[] =
    readers.length === 0
      ? [
          {
            id: "reader:none",
            placeholder: true,
            mark: (size) => (
              <WirePlaceholder size={size}>
                <Plus />
              </WirePlaceholder>
            ),
            title: <span className="font-normal text-muted-foreground">No reader yet</span>,
            name: "No reader yet",
            hint: "a variable reads one by reference",
            line: { state: "idle" },
          },
        ]
      : readers.map((reader) =>
          projectNode({
            id: reader.projectId,
            name: reader.projectName,
            deployment: projects.find((project) => project.id === reader.projectId),
            hint: `${plural(reader.variables, "variable")}${
              reader.waiting > 0 ? ` · ${reader.waiting} not live yet` : ""
            }`,
            line: { state: "up", dashed: reader.waiting > 0 },
          }),
        )
  return (
    <WireFlow
      className="animate-rise"
      phone="marks"
      loading={loading}
      related={related}
      start={{
        title: "Server variables",
        nodes: sources,
        max: PICTURE_VARIABLES,
        more: { words: (hidden) => plural(hidden, "more variable") },
        loadingRows: 3,
      }}
      hub={{
        mark: (size) => <WireHost size={size} />,
        eyebrow: "This server",
        title: "Every run",
        hint: `reads ${plural(list.length, "variable")} by reference`,
      }}
      end={{
        title: "Projects reading them",
        nodes: targets,
        max: 3,
        more: { href: "/deploy", words: (hidden) => plural(hidden, "more project") },
        loadingRows: 2,
      }}
    />
  )
}

const NO_VARIABLES: DeploymentSharedVariable[] = []
const variableNodeId = (name: string) => `variable:${name}`
