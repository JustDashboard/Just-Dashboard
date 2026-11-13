"use client"

import { InfoTip } from "@/components/form"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { ToolMark } from "./tool-marks"

/**
 * The head of a tool's pane: its mark, its name, what it does one hover away
 * and, for the probes that reach outward, the word that says what an answer
 * cannot prove. The sentence that opened every pane under its title moved to
 * the tip, and into the empty pane, where it is the one thing to read.
 */
export function ToolHead({
  tool,
  hue,
  label,
  hint,
  outward,
}: {
  tool: string
  hue?: string
  label: string
  hint: string
  outward?: boolean
}) {
  return (
    <div className="flex min-w-0 items-center gap-3">
      <ToolMark tool={tool} hue={hue} size="md" />
      <h2 className="truncate text-title font-medium">{label}</h2>
      <InfoTip label={`What ${label} does`}>{hint}.</InfoTip>
      {outward && (
        <Tag tone="warning" title="Proves what this server can reach, never what can reach it.">
          outward
        </Tag>
      )}
    </div>
  )
}

/**
 * A tool with nothing run yet: its mark large, what it does in one sentence,
 * and an example to start from. The example fills the target and nothing
 * more — sending a probe is a press of Run. It stays drawn, disabled, while a
 * run is in flight, so the pane keeps its height until the answer replaces it.
 */
export function ToolEmpty({
  tool,
  hue,
  sentence,
  busy,
  example,
  onExample,
}: {
  tool: string
  hue?: string
  sentence: string
  busy: boolean
  example?: string
  onExample?: () => void
}) {
  return (
    <div
      data-slot="tool-empty"
      className="flex min-w-0 flex-col items-center gap-3 border-t border-hairline px-2 py-10 text-center"
    >
      <ToolMark tool={tool} hue={hue} size="lg" />
      <p className="max-w-md text-body text-balance text-muted-foreground" aria-live="polite">
        {busy ? "Running diagnostic…" : sentence}
      </p>
      {example && onExample && (
        <Button size="sm" variant="outline" disabled={busy} onClick={onExample}>
          Try <span className="font-mono">{example}</span>
        </Button>
      )}
    </div>
  )
}
