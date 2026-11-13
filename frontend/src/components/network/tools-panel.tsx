"use client"

import { useMemo, useState } from "react"
import Link from "next/link"
import { useSearchParams } from "next/navigation"
import { ListUnordered, Wrench } from "@/components/icons"
import { cn } from "@/lib/utils"
import { PageContext, SearchInput } from "@/components/page"
import { Pane, PaneHeader } from "@/components/panel"
import { PaneBar, paneShown, type PaneStop } from "@/components/phone-panes"
import { ChoiceCard } from "@/components/choice-card"
import { WireHost } from "@/components/deploy/wire"
import { Button } from "@/components/ui/button"
import { useAuth } from "@/hooks/use-auth"
import { PATH_HINT, PathTool } from "./tools/path-tool"
import { SUBNET_HINT, SubnetTool } from "./tools/subnet-panel"
import { TOOL_GROUPS } from "./tools/tool-defs"
import { PATH_TOOL } from "./tools/tool-links"
import { GROUP_HUE, ToolMark } from "./tools/tool-marks"
import { ToolPanel } from "./tools/tool-panel"
import type { ToolPrefill } from "./tools/use-tool-run"

const probes = TOOL_GROUPS.flatMap((group) => group.tools)

/**
 * The rail: every probe in its group, with Connection path among the
 * Reachability tools, after the route lookup it extends. It is not a probe
 * (`POST /network/investigate` rather than `/network/probe`), so it has a
 * pane of its own rather than a definition in `TOOL_GROUPS`.
 */
type RailEntry = { key: string; label: string; hint: string }
const RAIL = TOOL_GROUPS.map((group, index) => ({
  title: group.title,
  hue: GROUP_HUE[index],
  tools: group.tools.flatMap((tool): RailEntry[] =>
    tool.key === "route"
      ? [tool, { key: PATH_TOOL, label: "Connection path", hint: PATH_HINT }]
      : [tool],
  ),
}))
const DIAGNOSTICS = RAIL.reduce((sum, group) => sum + group.tools.length, 0)
const KNOWN = new Set([...RAIL.flatMap((group) => group.tools.map((tool) => tool.key)), "subnet"])

/** A phone's two panes: the tool in use, and the catalogue to pick another. */
type ToolsPane = "tool" | "catalogue"
const TOOLS_STOPS: PaneStop<ToolsPane>[] = [
  { key: "tool", label: "Tool", icon: <Wrench /> },
  { key: "catalogue", label: "Every tool", icon: <ListUnordered /> },
]

/**
 * The diagnostics workbench, drawn as the Git workspace is: one frame held to
 * the window, a strip across its top saying where every probe is sent from
 * with Saved runs as a quiet link at its end, the rail of tools and the
 * chosen tool's pane beside it. Each tool is scanned by its mark, in its
 * group's hue. Hidden tools stay mounted so their drafts, results and
 * requests in flight survive a switch.
 */
export function ToolsPanel() {
  const { can } = useAuth()
  const admin = can("system.admin")
  const params = useSearchParams()
  const arrival = useMemo(() => {
    const tool = params.get("tool")
    if (!tool || !KNOWN.has(tool)) return null
    return {
      tool,
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
  const needle = query.trim().toLowerCase()
  const groups = RAIL.map((group) => ({
    ...group,
    tools: (admin ? group.tools : []).filter((tool) =>
      `${tool.label} ${tool.hint}`.toLowerCase().includes(needle),
    ),
  }))
  const subnetShown = `subnet calculator ${SUBNET_HINT}`.toLowerCase().includes(needle)
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
      <PageContext eyebrow="Network" title="Tools" />
      {/* One frame around the whole workbench: the strip, then the rail and
          the pane with a hairline between them. Below `md` it runs to the
          window's edges, one pane at a time with a bar at the foot. */}
      <div
        data-slot="security-tools"
        className="flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden bg-card md:rounded-xl md:border"
      >
        <PaneHeader className="h-auto min-h-12 gap-3 px-3 py-1.5">
          <WireHost size="sm" />
          <div className="min-w-0 flex-1">
            <p className="truncate text-body font-semibold">Tools</p>
            {/* Where every probe is sent from is what its answer can and
                cannot say: an open port seen from here is not one the
                internet can reach. */}
            <p className="truncate text-micro text-muted-foreground">
              {active === "subnet"
                ? "calculated in this browser"
                : `sent from this server · ${DIAGNOSTICS} diagnostics`}
            </p>
          </div>
          {admin && (
            <Button asChild size="xs" variant="ghost" className="text-muted-foreground">
              <Link href="/network/runs">Saved runs</Link>
            </Button>
          )}
          {/* A phone has this as a stop on the bar at the foot, and a reader
              has one tool. */}
          {admin && (
            <Button
              size="xs"
              variant="ghost"
              className="text-muted-foreground max-md:hidden"
              onClick={() => {
                setQuery("")
                document.querySelector<HTMLInputElement>('[aria-label="Filter tools"]')?.focus()
              }}
            >
              Every tool
            </Button>
          )}
        </PaneHeader>
        {/* Why a reader's rail holds one tool, inside the frame so the
            workbench still has the whole window. */}
        {!admin && (
          <p className="shrink-0 border-b border-hairline px-3 py-2 text-hint text-muted-foreground">
            <span className="font-medium text-foreground">
              Server diagnostics need the admin capability
            </span>{" "}
            <span>The subnet calculator runs in your browser and needs only read access.</span>
          </p>
        )}
        <div className="flex min-h-0 min-w-0 flex-1 flex-col lg:grid lg:grid-cols-[17rem_minmax(0,1fr)]">
          <Pane
            flush
            className={cn(
              "border-hairline max-md:flex-1 md:max-h-64 md:shrink-0 md:border-b lg:max-h-none lg:border-r lg:border-b-0",
              paneShown(pane === "catalogue", "md"),
            )}
          >
            <div className={cn("shrink-0 p-3 pb-1", !admin && "hidden")}>
              <SearchInput
                dense
                aria-label="Filter tools"
                placeholder="Find a diagnostic"
                value={query}
                onChange={(event) => setQuery(event.target.value)}
                containerClassName="sm:w-full"
              />
            </div>
            <nav aria-label="Diagnostics" className="min-h-0 space-y-4 overflow-y-auto p-3 pt-2">
              {groups.map(
                (group) =>
                  group.tools.length > 0 && (
                    <div key={group.title} className="space-y-1.5">
                      <div className="flex items-center justify-between px-1">
                        <h2 className="eyebrow">{group.title}</h2>
                        <span className="numeric text-micro text-muted-foreground">
                          {group.tools.length}
                        </span>
                      </div>
                      <div className="space-y-1">
                        {group.tools.map((tool) => (
                          <RailRow
                            key={tool.key}
                            tool={tool.key}
                            label={tool.label}
                            hue={group.hue}
                            selected={active === tool.key}
                            onChoose={() => choose(tool.key)}
                          />
                        ))}
                      </div>
                    </div>
                  ),
              )}
              {subnetShown && (
                <div className="space-y-1.5">
                  <h2 className="eyebrow px-1">Address planning</h2>
                  <RailRow
                    tool="subnet"
                    label="Subnet calculator"
                    selected={active === "subnet"}
                    onChoose={() => choose("subnet")}
                  />
                </div>
              )}
              {groups.every((group) => group.tools.length === 0) && !subnetShown && (
                <p className="p-2 text-body text-muted-foreground">No matching diagnostics.</p>
              )}
            </nav>
          </Pane>
          <Pane
            flush
            className={cn("min-h-0 flex-1 max-md:flex-1", paneShown(pane === "tool", "md"))}
          >
            <div className="min-h-0 flex-1 overflow-y-auto p-5 sm:p-6">
              {admin &&
                probes.map((tool) => (
                  <div key={tool.key} data-tool={tool.key} hidden={active !== tool.key}>
                    <ToolPanel
                      def={tool}
                      hue={RAIL.find((g) => g.tools.some((t) => t.key === tool.key))?.hue}
                      prefill={arrival?.tool === tool.key ? arrival.prefill : undefined}
                      active={active === tool.key}
                    />
                  </div>
                ))}
              {admin && (
                <div data-tool={PATH_TOOL} hidden={active !== PATH_TOOL}>
                  <PathTool
                    arrival={arrival?.tool === PATH_TOOL ? params : undefined}
                    active={active === PATH_TOOL}
                    hue={RAIL[0].hue}
                  />
                </div>
              )}
              <div data-tool="subnet" hidden={active !== "subnet"}>
                <SubnetTool />
              </div>
            </div>
          </Pane>
        </div>
        <PaneBar label="Tools" until="md" stops={TOOLS_STOPS} active={pane} onPick={setPane} />
      </div>
    </>
  )
}

/** One tool in the rail: its mark in its group's hue, and its name, which is the button's. */
function RailRow({
  tool,
  label,
  hue,
  selected,
  onChoose,
}: {
  tool: string
  label: string
  hue?: string
  selected: boolean
  onChoose: () => void
}) {
  return (
    <ChoiceCard
      selected={selected}
      onClick={onChoose}
      className="min-h-0 flex-row items-center gap-2.5 px-2 py-1.5"
    >
      <ToolMark tool={tool} hue={hue} />
      <span className="truncate text-body font-medium">{label}</span>
    </ChoiceCard>
  )
}
