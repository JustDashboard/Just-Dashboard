"use client"

import { useMemo, useState } from "react"
import { useSearchParams } from "next/navigation"
import { Crosshair, Globe, NetworkDevice, SecureConnection, Servers } from "@/components/icons"
import { PageContext, SearchInput } from "@/components/page"
import { Pane, PaneHeader } from "@/components/panel"
import { ChoiceCard } from "@/components/choice-card"
import { EmptyState } from "@/components/state"
import { Button } from "@/components/ui/button"
import { useAuth } from "@/hooks/use-auth"
import { SubnetTool } from "./tools/subnet-panel"
import { TOOL_GROUPS } from "./tools/tool-defs"
import { ToolPanel } from "./tools/tool-panel"
import type { ToolPrefill } from "./tools/use-tool-run"

const tools = TOOL_GROUPS.flatMap((group) => group.tools)
const GROUP_MARK = [NetworkDevice, Crosshair, SecureConnection, Globe, Servers]

/** One diagnostic in focus. Hidden tools stay mounted so their work and drafts survive a switch. */
export function ToolsPanel() {
  const { can } = useAuth()
  const params = useSearchParams()
  const arrival = useMemo(() => {
    const tool = params.get("tool")
    if (!tools.some((entry) => entry.key === tool)) return null
    return {
      tool: tool!,
      prefill: {
        target: params.get("target") ?? undefined,
        record: params.get("record") ?? undefined,
      } satisfies ToolPrefill,
    }
  }, [params])
  const [choice, setChoice] = useState<{ arrival: typeof arrival; key: string } | null>(null)
  const active = choice?.arrival === arrival ? choice.key : (arrival?.tool ?? "dns")
  const [query, setQuery] = useState("")
  const selectedGroup = TOOL_GROUPS.find((group) => group.tools.some((tool) => tool.key === active))
  const groups = TOOL_GROUPS.map((group, index) => ({
    ...group,
    mark: GROUP_MARK[index],
    tools: group.tools.filter((tool) =>
      `${tool.label} ${tool.hint}`.toLowerCase().includes(query.trim().toLowerCase()),
    ),
  }))
  const choose = (key: string) => setChoice({ arrival, key })

  if (!can("system.admin"))
    return (
      <>
        <PageContext eyebrow="Network" title="Tools" />
        <EmptyState
          icon={Crosshair}
          title="Diagnostics need the admin capability"
          description="These probes send traffic from this server to the target you choose."
        />
      </>
    )

  return (
    <>
      <PageContext eyebrow="Network" title="Tools" />
      {/* The chooser and result own their scrolling; this is one framed workbench. */}
      <div
        data-slot="security-tools"
        className="grid min-w-0 overflow-hidden rounded-xl border bg-card lg:h-[min(46rem,calc(100svh-16rem))] lg:min-h-[30rem] lg:grid-cols-[17rem_minmax(0,1fr)]"
      >
        <Pane
          flush
          className="max-h-80 border-b border-hairline lg:max-h-none lg:border-r lg:border-b-0"
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
        <Pane flush className="min-h-80">
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
                sent from this server · {tools.length} diagnostics
              </span>
            </span>
            <Button
              size="xs"
              variant="ghost"
              onClick={() => {
                setQuery("")
                document.querySelector<HTMLInputElement>('[aria-label="Filter tools"]')?.focus()
              }}
            >
              Every tool
            </Button>
          </PaneHeader>
          <div className="min-h-0 overflow-y-auto p-5 sm:p-6">
            {tools.map((tool) => (
              <div key={tool.key} hidden={active !== tool.key}>
                <ToolPanel
                  def={tool}
                  prefill={arrival?.tool === tool.key ? arrival.prefill : undefined}
                />
              </div>
            ))}
            <div hidden={active !== "subnet"}>
              <SubnetTool />
            </div>
          </div>
        </Pane>
      </div>
    </>
  )
}
