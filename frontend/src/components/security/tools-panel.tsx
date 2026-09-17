"use client"

import { useMemo, useState } from "react"
import Link from "next/link"
import { useSearchParams } from "next/navigation"
import { Crosshair, Information } from "@/components/icons"
import { PageHeader, SearchInput, Section, Toolbar } from "@/components/page"
import { EmptyState, Notice } from "@/components/state"
import { FilterChip } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import { useAuth } from "@/hooks/use-auth"
import { SubnetTool } from "./tools/subnet-panel"
import { TOOL_GROUPS } from "./tools/tool-defs"
import { ToolPanel } from "./tools/tool-panel"
import type { ToolPrefill } from "./tools/use-tool-run"

/**
 * The tools an operator opens a terminal for, on the page where the question
 * arose.
 *
 * One independent block per tool: each keeps its own target, port and answer,
 * and several can run at once. Twenty blocks is more than fits on a screen, so
 * the page opens with a filter rather than with scrolling: the group chips and
 * the search box narrow the grid to the tool that answers the question you
 * arrived with. And the warning that three of the blocks each printed in full
 * — that an outward probe proves nothing about what can reach *in* — is stated
 * once, at the top, where it applies to all of them.
 *
 * Another page can arrive here with the question already asked: an address in
 * the ban log or the connection table links to `?tool=asn&target=…`, which
 * narrows the page to that one tool with the address filled in. The run is
 * still a press.
 */
export function ToolsPanel() {
  const { can } = useAuth()
  const params = useSearchParams()
  const arrival = useMemo<{ tool: string; prefill: ToolPrefill } | null>(() => {
    const tool = params.get("tool")
    const target = params.get("target")
    if (!tool || !TOOL_GROUPS.some((g) => g.tools.some((t) => t.key === tool))) return null
    return { tool, prefill: { target: target ?? undefined, record: params.get("record") ?? undefined } }
  }, [params])
  const [query, setQuery] = useState("")
  const [group, setGroup] = useState<string | null>(null)
  // The arrival the reader has widened away from. Kept as the object rather
  // than a flag so a fresh arrival — a new query string, hence a new object —
  // narrows the page again without an effect to reset anything.
  const [widened, setWidened] = useState<typeof arrival>(null)
  const focused = arrival && widened !== arrival ? arrival.tool : null
  const setFocused = (tool: string | null) => setWidened(tool ? null : arrival)

  const groups = useMemo(() => {
    const q = query.trim().toLowerCase()
    return TOOL_GROUPS.map((g) => ({
      ...g,
      tools: g.tools.filter(
        (t) =>
          (!focused || t.key === focused) &&
          (!q || `${t.label} ${t.hint} ${t.key}`.toLowerCase().includes(q)),
      ),
    })).filter((g) => (!group || g.title === group) && g.tools.length > 0)
  }, [query, group, focused])

  const header = <PageHeader eyebrow="Security" title="Tools" />

  if (!can("system.admin")) {
    return (
      <>
        {header}
        <EmptyState
          icon={Crosshair}
          title="Diagnostics need the admin capability"
          description="A probe makes the server send traffic to an address the caller chose, which is a scanner if it is handed to everybody."
        />
      </>
    )
  }

  const showSubnet = !focused && groups.some((g) => g.title === "This host")

  return (
    <>
      {header}

      <Notice icon={Information} title="A probe answers outward, not inward">
        Everything marked <span className="font-medium text-warning">outward</span> proves what{" "}
        <b>this server</b> can reach, not what can reach it. Pointed at your own public address the
        traffic can hairpin or be admitted by rules that never apply to an outside visitor, so an
        open port here is not proof of exposure — the{" "}
        <Link href="/security/connections" className="underline underline-offset-4">
          Connections
        </Link>{" "}
        page and the exposure grade are.
      </Notice>

      <Toolbar>
        <FilterChip
          selected={group === null && !focused}
          onClick={() => {
            setGroup(null)
            setFocused(null)
          }}
        >
          All
        </FilterChip>
        {TOOL_GROUPS.map((g) => (
          <FilterChip
            key={g.title}
            selected={group === g.title}
            onClick={() => {
              setFocused(null)
              setGroup(group === g.title ? null : g.title)
            }}
          >
            {g.title}
          </FilterChip>
        ))}
        <span className="flex-1" />
        {focused && (
          <Button size="sm" variant="ghost" onClick={() => setFocused(null)}>
            Every tool
          </Button>
        )}
        <SearchInput
          dense
          aria-label="Filter tools"
          placeholder="Filter tools"
          value={query}
          onChange={(e) => {
            setFocused(null)
            setQuery(e.target.value)
          }}
          containerClassName="sm:w-64"
        />
      </Toolbar>

      {groups.length === 0 && (
        <EmptyState
          icon={Crosshair}
          title="No tool matches"
          description={`Nothing in these twenty probes is called “${query.trim()}”.`}
        />
      )}

      {groups.map((g) => (
        <Section key={g.title} title={g.title}>
          {/* items-start so a block holding two hundred lines of traceroute
              output does not stretch the empty block beside it to match. */}
          <div className="grid min-w-0 items-start gap-x-8 gap-y-6 xl:grid-cols-2 2xl:grid-cols-3">
            {g.tools.map((def) => (
              <ToolPanel
                key={def.key}
                def={def}
                prefill={arrival && arrival.tool === def.key ? arrival.prefill : undefined}
              />
            ))}
            {g.title === "This host" && showSubnet && !query.trim() && <SubnetTool />}
          </div>
        </Section>
      ))}
    </>
  )
}
