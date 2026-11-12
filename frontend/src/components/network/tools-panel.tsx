"use client"

import { useMemo, useState } from "react"
import Link from "next/link"
import { useSearchParams } from "next/navigation"
import {
  Crosshair,
  Globe,
  ListUnordered,
  NetworkDevice,
  SecureConnection,
  Servers,
  Wrench,
} from "@/components/icons"
import { cn } from "@/lib/utils"
import { PageContext, SearchInput } from "@/components/page"
import { Pane, PaneHeader } from "@/components/panel"
import { PaneBar, paneShown, type PaneStop } from "@/components/phone-panes"
import { ChoiceCard } from "@/components/choice-card"
import { Notice } from "@/components/state"
import { Button } from "@/components/ui/button"
import { useAuth } from "@/hooks/use-auth"
import { SubnetTool } from "./tools/subnet-panel"
import { TOOL_GROUPS } from "./tools/tool-defs"
import { ToolPanel } from "./tools/tool-panel"
import type { ToolPrefill } from "./tools/use-tool-run"

const tools = TOOL_GROUPS.flatMap((group) => group.tools)
const GROUP_MARK = [NetworkDevice, Crosshair, SecureConnection, Globe, Servers]

/** A phone's two panes: the tool in use, and the catalogue to pick another. */
type ToolsPane = "tool" | "catalogue"
const TOOLS_STOPS: PaneStop<ToolsPane>[] = [
  { key: "tool", label: "Tool", icon: <Wrench /> },
  { key: "catalogue", label: "Every tool", icon: <ListUnordered /> },
]

/** One diagnostic in focus. Hidden tools stay mounted so their work and drafts survive a switch. */
export function ToolsPanel() {
  const { can } = useAuth()
  const admin = can("system.admin")
  const availableTools = admin ? tools : []
  const params = useSearchParams()
  const arrival = useMemo(() => {
    const tool = params.get("tool")
    if (tool !== "subnet" && !tools.some((entry) => entry.key === tool)) return null
    return {
      tool: tool!,
      prefill: {
        target: params.get("target") ?? undefined,
        record: params.get("record") ?? undefined,
      } satisfies ToolPrefill,
    }
  }, [params])
  const [choice, setChoice] = useState<{ arrival: typeof arrival; key: string } | null>(null)
  const active = admin
    ? choice?.arrival === arrival
      ? choice.key
      : (arrival?.tool ?? "dns")
    : "subnet"
  const [query, setQuery] = useState("")
  const selectedGroup = TOOL_GROUPS.find((group) => group.tools.some((tool) => tool.key === active))
  const groups = TOOL_GROUPS.map((group, index) => ({
    ...group,
    mark: GROUP_MARK[index],
    tools: (admin ? group.tools : []).filter((tool) =>
      `${tool.label} ${tool.hint}`.toLowerCase().includes(query.trim().toLowerCase()),
    ),
  }))
  // On a phone the catalogue and the tool are one pane at a time, and the
  // page opens on the tool: the one a link asked for, or the first.
  const [pane, setPane] = useState<ToolsPane>("tool")
  const choose = (key: string) => {
    setChoice({ arrival, key })
    // Choosing a tool is asking to use it.
    setPane("tool")
  }

  return (
    <>
      <PageContext
        eyebrow="Network"
        title="Tools"
        actions={
          admin && (
            <Button asChild variant="outline" size="sm">
              <Link href="/network/runs">Saved runs</Link>
            </Button>
          )
        }
      />
      {!admin && (
        <Notice title="Server diagnostics need the admin capability">
          The subnet calculator runs locally in your browser and is available with read access.
        </Notice>
      )}
      {/* The chooser and result own their scrolling; this is one framed
          workbench. Below `md` it is held to the window and runs to its
          edges, one pane at a time with a bar at the foot: stacked, a phone
          had the catalogue in a 278px box over the tool. */}
      <div
        data-slot="security-tools"
        className="grid min-w-0 overflow-hidden rounded-xl border bg-card max-md:-mx-5 max-md:-mb-6 max-md:flex max-md:min-h-0 max-md:flex-1 max-md:flex-col max-md:rounded-none max-md:border-x-0 max-md:border-b-0 lg:h-[min(46rem,calc(100svh-16rem))] lg:min-h-[30rem] lg:grid-cols-[17rem_minmax(0,1fr)]"
      >
        <Pane
          flush
          className={cn(
            "border-hairline max-md:flex-1 md:max-h-80 md:border-b lg:max-h-none lg:border-r lg:border-b-0",
            paneShown(pane === "catalogue", "md"),
          )}
        >
          <PaneHeader>
            <SearchInput
              dense
              aria-label="Filter tools"
              placeholder="Find a diagnostic"
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              containerClassName="w-full"
            />
          </PaneHeader>
          <nav aria-label="Diagnostics" className="min-h-0 space-y-5 overflow-y-auto p-3">
            {groups.map(
              (group) =>
                group.tools.length > 0 && (
                  <div key={group.title} className="space-y-2">
                    <div className="flex items-center justify-between px-1">
                      <h2 className="eyebrow">{group.title}</h2>
                      <span className="numeric text-micro text-muted-foreground">
                        {group.tools.length}
                      </span>
                    </div>
                    <div className="space-y-1.5">
                      {group.tools.map((tool) => (
                        <ChoiceCard
                          key={tool.key}
                          selected={active === tool.key}
                          onClick={() => choose(tool.key)}
                          className="min-h-0 flex-row items-center gap-2.5 px-3 py-2"
                        >
                          <group.mark
                            aria-hidden
                            className="size-3.5 shrink-0 text-muted-foreground"
                          />
                          <span className="text-body font-medium">{tool.label}</span>
                        </ChoiceCard>
                      ))}
                    </div>
                  </div>
                ),
            )}
            {"subnet calculator".includes(query.trim().toLowerCase()) && (
              <ChoiceCard
                selected={active === "subnet"}
                onClick={() => choose("subnet")}
                className="min-h-0 flex-row items-center gap-2.5 px-3 py-2"
              >
                <NetworkDevice aria-hidden className="size-3.5 text-muted-foreground" />
                <span className="text-body font-medium">Subnet calculator</span>
              </ChoiceCard>
            )}
            {groups.every((group) => group.tools.length === 0) &&
              !"subnet calculator".includes(query.trim().toLowerCase()) && (
                <p className="p-2 text-body text-muted-foreground">No matching diagnostics.</p>
              )}
          </nav>
        </Pane>
        <Pane flush className={cn("max-md:flex-1 md:min-h-80", paneShown(pane === "tool", "md"))}>
          <PaneHeader className="justify-between">
            <span className="flex min-w-0 items-baseline gap-2">
              <span className="text-body font-medium">
                {selectedGroup?.title ?? "Address planning"}
              </span>
              {/* Where every probe is sent from, which is what its answer can
                  and cannot say: an open port seen from here is not one the
                  internet can reach. It was a tile; a fact read with the
                  result belongs beside it. */}
              <span className="hidden truncate text-hint text-muted-foreground sm:inline">
                {active === "subnet"
                  ? "calculated in this browser"
                  : `sent from this server · ${tools.length} diagnostics`}
              </span>
            </span>
            {/* A phone has this as a stop on the bar at the foot. */}
            <Button
              size="xs"
              variant="ghost"
              className="max-md:hidden"
              onClick={() => {
                setQuery("")
                document.querySelector<HTMLInputElement>('[aria-label="Filter tools"]')?.focus()
              }}
            >
              Every tool
            </Button>
          </PaneHeader>
          <div className="min-h-0 overflow-y-auto p-5 sm:p-6">
            {availableTools.map((tool) => (
              <div key={tool.key} hidden={active !== tool.key}>
                <ToolPanel
                  def={tool}
                  prefill={arrival?.tool === tool.key ? arrival.prefill : undefined}
                  active={active === tool.key}
                />
              </div>
            ))}
            <div hidden={active !== "subnet"}>
              <SubnetTool />
            </div>
          </div>
        </Pane>
        <PaneBar label="Tools" until="md" stops={TOOLS_STOPS} active={pane} onPick={setPane} />
      </div>
    </>
  )
}
