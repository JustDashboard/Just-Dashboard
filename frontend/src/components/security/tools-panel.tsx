"use client"

import { useMemo, useState } from "react"
import {
  Crosshair,
  Globe,
  Information,
  LockClosed,
  NetworkDevice,
  Servers,
  Shield,
} from "@/components/icons"
import { SearchInput, Section, Toolbar } from "@/components/page"
import { EmptyState, Notice } from "@/components/state"
import { FilterChip } from "@/components/tabs"
import { useAuth } from "@/hooks/use-auth"
import { SubnetTool } from "./tools/subnet-panel"
import { TOOL_GROUPS } from "./tools/tool-defs"
import { ToolPanel } from "./tools/tool-panel"

// One glyph per group, in group order — the page holds twenty cards and the
// icon is what a returning eye lands on first.
const GROUP_ICONS = [Globe, NetworkDevice, LockClosed, Shield, Servers] as const

/**
 * The tools an operator opens a terminal for, on the page where the question
 * arose.
 *
 * One independent card per tool: each keeps its own target, port and answer,
 * and several can run at once. The old single-panel version shared one input
 * across every tool, so switching tools carried the wrong value along and a
 * slow traceroute blocked the whole page.
 *
 * Twenty cards is also more than fits on a screen, so the page opens with a
 * filter rather than with scrolling: the group chips and the search box narrow
 * the grid to the tool that answers the question you arrived with. And the
 * warning that three of the cards each printed in full — that an outward probe
 * proves nothing about what can reach *in* — is stated once, at the top, where
 * it applies to all of them.
 */
export function ToolsPanel() {
  const { can } = useAuth()
  const [query, setQuery] = useState("")
  const [group, setGroup] = useState<string | null>(null)

  const groups = useMemo(() => {
    const q = query.trim().toLowerCase()
    return TOOL_GROUPS.map((g, i) => ({
      ...g,
      icon: GROUP_ICONS[i] ?? Crosshair,
      tools: g.tools.filter((t) => !q || `${t.label} ${t.hint} ${t.key}`.toLowerCase().includes(q)),
    })).filter((g) => (!group || g.title === group) && g.tools.length > 0)
  }, [query, group])

  if (!can("system.admin")) {
    return (
      <EmptyState
        icon={Crosshair}
        title="Diagnostics need the admin capability"
        description="A probe makes the server send traffic to an address the caller chose, which is a scanner if it is handed to everybody."
      />
    )
  }

  const showSubnet = groups.some((g) => g.title === "This host")

  return (
    <div className="flex min-w-0 flex-col gap-5">
      <Notice icon={Information} title="A probe answers outward, not inward">
        Everything marked <span className="font-medium text-warning">outward</span> proves what{" "}
        <b>this server</b> can reach, not what can reach it. Pointed at your own public address the
        traffic can hairpin or be admitted by rules that never apply to an outside visitor, so an
        open port here is not proof of exposure — the Connections page and the exposure grade are.
      </Notice>

      <Toolbar>
        <FilterChip selected={group === null} onClick={() => setGroup(null)}>
          All
        </FilterChip>
        {TOOL_GROUPS.map((g) => (
          <FilterChip
            key={g.title}
            selected={group === g.title}
            onClick={() => setGroup(group === g.title ? null : g.title)}
          >
            {g.title}
          </FilterChip>
        ))}
        <span className="flex-1" />
        <SearchInput
          dense
          aria-label="Filter tools"
          placeholder="Filter tools"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
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
          {/* items-start so a card holding two hundred lines of traceroute
              output does not stretch the empty card beside it to match. */}
          <div className="grid min-w-0 items-start gap-4 xl:grid-cols-2 2xl:grid-cols-3">
            {g.tools.map((def) => (
              <ToolPanel key={def.key} def={def} />
            ))}
            {g.title === "This host" && showSubnet && !query.trim() && <SubnetTool />}
          </div>
        </Section>
      ))}
    </div>
  )
}
