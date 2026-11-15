"use client"

import { InfoTip } from "@/components/form"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { cn } from "@/lib/utils"
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
  actions,
}: {
  tool: string
  hue?: string
  label: string
  hint: string
  outward?: boolean
  /**
   * A verb that comes and goes with a result (Clear), at the far end of the
   * title line: in the fields' row its arrival moved Run.
   */
  actions?: React.ReactNode
}) {
  return (
    <div className="flex min-h-8 min-w-0 items-center gap-3">
      <ToolMark tool={tool} hue={hue} size="md" />
      <h2 className="truncate text-title font-medium">{label}</h2>
      <InfoTip label={`What ${label} does`}>{hint}.</InfoTip>
      {outward && (
        <Tag tone="warning" title="Proves what this server can reach, never what can reach it.">
          outward
        </Tag>
      )}
      {actions && <div className="ml-auto flex shrink-0 items-center gap-2">{actions}</div>}
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
      {/* The sentence keeps its lines while the run is out and the word
          stands over them, so the pane is one height either way. */}
      <p
        className="relative max-w-md text-body text-balance text-muted-foreground"
        aria-live="polite"
      >
        <span className={cn(busy && "invisible")} aria-hidden={busy || undefined}>
          {sentence}
        </span>
        {busy && (
          <span className="absolute inset-0 flex items-center justify-center">
            Running diagnostic…
          </span>
        )}
      </p>
      {example && onExample && (
        <Button size="sm" variant="outline" disabled={busy} onClick={onExample}>
          Try <span className="font-mono">{example}</span>
        </Button>
      )}
    </div>
  )
}
