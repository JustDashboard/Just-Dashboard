"use client"

import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { cn } from "@/lib/utils"

import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import type { ToolDef } from "./tool-defs"
import { useToolRun } from "./use-tool-run"
import { ToolResult } from "./tool-result"

/**
 * One network tool: its own inputs, its own run, its own answer.
 *
 * Panels share nothing — each mounts its own `useToolRun`, so a target typed
 * into DNS stays in DNS when the port check runs, and a slow traceroute never
 * disables another panel's Run button.
 *
 * The form is one line. It was a labelled grid two rows tall, which with
 * twenty tools on the page meant a screen and a half of empty form fields
 * before the first answer — and three of the panels carried an identical
 * three-line warning paragraph, so the same sentence appeared on the page
 * three times. The label is the placeholder, the warning is a mark in the
 * header, and the page says the sentence once.
 */
export function ToolPanel({ def }: { def: ToolDef }) {
  const t = useToolRun(def)
  const base = `tool-${def.key}`

  return (
    <Panel>
      <PanelHeader
        title={def.label}
        actions={
          def.outward ? (
            <Tag tone="warning" title="Proves what this server can reach, never what can reach it.">
              outward
            </Tag>
          ) : undefined
        }
      />
      <PanelBody className="space-y-2.5">
        <div className="flex min-w-0 flex-wrap items-center gap-2">
          {def.needsTarget && (
            <Input
              id={`${base}-target`}
              aria-label="Target"
              value={t.target}
              onChange={(e) => t.setTarget(e.target.value)}
              onKeyDown={(e) => e.key === "Enter" && t.canRun && t.run()}
              placeholder={def.targetPlaceholder ?? "example.com or 203.0.113.9"}
              className="h-8 min-w-0 flex-1 font-mono text-xs"
            />
          )}
          {def.recordOptions && (
            <Select value={t.record} onValueChange={t.setRecord}>
              <SelectTrigger size="sm" className="w-24" aria-label="Record type">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {def.recordOptions.map((r) => (
                  <SelectItem key={r} value={r}>
                    {r}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          )}
          {def.optionOptions && (
            <Select value={t.option} onValueChange={t.setOption}>
              <SelectTrigger size="sm" className="w-28" aria-label={def.optionLabel ?? "Option"}>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {def.optionOptions.map((o) => (
                  <SelectItem key={o.value} value={o.value}>
                    {o.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          )}
          {def.needsPort && (
            <Input
              id={`${base}-port`}
              aria-label="Port"
              value={t.port}
              inputMode="numeric"
              onChange={(e) => t.setPort(e.target.value)}
              onKeyDown={(e) => e.key === "Enter" && t.canRun && t.run()}
              placeholder="port"
              className="h-8 w-20 font-mono text-xs"
            />
          )}
          <Button size="sm" onClick={t.run} disabled={t.busy || !t.canRun} pending={t.busy}>
            Run
          </Button>
          {(t.result || t.past.length > 0) && (
            <Button size="sm" variant="ghost" onClick={t.clear} disabled={t.busy}>
              Clear
            </Button>
          )}
        </div>

        {t.result && <ToolResult result={t.result} />}

        {t.past.length > 0 && (
          <div className="flex flex-wrap items-center gap-1.5">
            <span className="eyebrow">earlier</span>
            {t.past.map((p) => (
              <button
                key={`${p.target}-${p.duration}`}
                type="button"
                onClick={() => t.restore(p)}
                className="rounded-sm border border-hairline px-1.5 py-px font-mono text-micro text-muted-foreground transition-colors hover:border-rule-primary hover:text-foreground"
              >
                <span className={cn("mr-1", p.ok ? "text-success" : "text-destructive")}>
                  {p.ok ? "✓" : "✗"}
                </span>
                {p.target} · {p.duration}
              </button>
            ))}
          </div>
        )}
      </PanelBody>
    </Panel>
  )
}
