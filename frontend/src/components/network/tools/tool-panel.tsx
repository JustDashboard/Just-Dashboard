"use client"

import { useEffect, useRef } from "react"
import { Play } from "@/components/icons"
import { cn } from "@/lib/utils"
import { Field } from "@/components/form"
import { ErrorState } from "@/components/state"
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
import { useToolRun, type ToolPrefill } from "./use-tool-run"
import { ToolResult } from "./tool-result"
import { SaveDiagnosticRun, SaveHeldResult } from "./save-run"
import { SSHTrustActions } from "./ssh-trust"
import { WakeDevices } from "./wake-devices"
import { WatchProbeButton } from "@/components/network/watched-probes"
import { ToolEmpty, ToolHead } from "./tool-frame"
import { exampleTarget } from "./tool-links"

/** Each tool retains its inputs, result and request while another tool is selected. */
export function ToolPanel({
  def,
  hue,
  prefill,
  active = true,
}: {
  def: ToolDef
  /** The hue of the tool's group in the rail, which its marks wear. */
  hue?: string
  prefill?: ToolPrefill
  /** Hidden tools stay mounted; only the one in view reads its saved references. */
  active?: boolean
}) {
  const t = useToolRun(def, prefill)
  const base = `tool-${def.key}`
  const targetRef = useRef<HTMLInputElement>(null)

  // A block that was arrived at from another page takes the focus, so the
  // press that runs it is the next thing that happens.
  useEffect(() => {
    if (prefill?.target) targetRef.current?.focus()
  }, [prefill?.target])

  return (
    <div className={cn("min-w-0 space-y-5", prefill?.target && "animate-rise")}>
      <ToolHead
        tool={def.key}
        hue={hue}
        label={def.label}
        hint={def.hint}
        outward={def.outward}
        actions={
          (t.result || t.error || t.past.length > 0) && (
            <Button size="sm" variant="ghost" onClick={t.clear} disabled={t.busy}>
              Clear
            </Button>
          )
        }
      />
      <div className="min-w-0 space-y-2.5">
        {/* Only the tool in view has its fields in the page: its inputs live in
            `useToolRun`, so nothing is lost, and twenty-six hidden forms no
            longer answer to "Port" beside the one on screen. */}
        {active && (
          <div className="flex min-w-0 flex-wrap items-end gap-3">
            {def.needsTarget && (
              <Field
                label={def.targetLabel ?? "Target"}
                htmlFor={`${base}-target`}
                className="min-w-48 flex-1"
              >
                <Input
                  ref={targetRef}
                  id={`${base}-target`}
                  aria-label={def.targetLabel ?? "Target"}
                  value={t.target}
                  onChange={(e) => t.setTarget(e.target.value)}
                  onKeyDown={(e) => e.key === "Enter" && t.canRun && t.run()}
                  placeholder={def.targetPlaceholder ?? "example.com or 203.0.113.9"}
                  className="font-mono"
                />
              </Field>
            )}
            {def.recordOptions && (
              <Field label="Record type">
                <Select value={t.record} onValueChange={t.setRecord}>
                  <SelectTrigger className="w-24" aria-label="Record type">
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
              </Field>
            )}
            {def.optionOptions && (
              <Field label={def.optionLabel ?? "Option"}>
                <Select value={t.option} onValueChange={t.setOption}>
                  <SelectTrigger className="w-28" aria-label={def.optionLabel ?? "Option"}>
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
              </Field>
            )}
            {def.optionPlaceholder && (
              <Field label={def.optionLabel ?? "Option"} htmlFor={`${base}-option`}>
                <Input
                  id={`${base}-option`}
                  value={t.option}
                  onChange={(event) => t.setOption(event.target.value)}
                  onKeyDown={(event) => event.key === "Enter" && t.canRun && t.run()}
                  placeholder={def.optionPlaceholder}
                  className="w-36 font-mono"
                />
              </Field>
            )}
            {def.needsPort && (
              <Field label={def.portLabel ?? "Port"} htmlFor={`${base}-port`} error={t.portError}>
                <Input
                  id={`${base}-port`}
                  aria-label="Port"
                  aria-invalid={Boolean(t.portError)}
                  value={t.port}
                  inputMode="numeric"
                  onChange={(e) => t.setPort(e.target.value)}
                  onKeyDown={(e) => e.key === "Enter" && t.canRun && t.run()}
                  placeholder={def.portOptional ? "optional" : "port"}
                  className="w-24 font-mono"
                />
              </Field>
            )}
            {def.verify && (
              <>
                <Field label="Verify address" htmlFor={`${base}-verify`} error={t.verifyError}>
                  <Input
                    id={`${base}-verify`}
                    value={t.verify}
                    onChange={(event) => t.setVerify(event.target.value)}
                    onKeyDown={(event) => event.key === "Enter" && t.canRun && t.run()}
                    placeholder="optional, 192.168.1.50"
                    aria-invalid={Boolean(t.verifyError)}
                    className="w-44 font-mono"
                  />
                </Field>
                <Field label="Verify TCP port" htmlFor={`${base}-verify-port`}>
                  <Input
                    id={`${base}-verify-port`}
                    value={t.verifyPort}
                    inputMode="numeric"
                    onChange={(event) => t.setVerifyPort(event.target.value)}
                    onKeyDown={(event) => event.key === "Enter" && t.canRun && t.run()}
                    placeholder="ICMP"
                    className="w-24 font-mono"
                  />
                </Field>
              </>
            )}
            <Button onClick={t.run} disabled={t.busy || !t.canRun} pending={t.busy}>
              {/* A glyph for the spinner to stand in for: the button keeps its
                  width while the probe is out, and the row does not move. */}
              <Play />
              Run
            </Button>
            <SaveDiagnosticRun
              request={t.request}
              label={def.label}
              disabled={t.busy || !t.canRun}
            />
          </div>
        )}

        {t.error && (
          <ErrorState error={t.error} onRetry={t.canRun && !t.busy ? t.run : undefined} />
        )}

        {!t.result && (
          <div className="pt-3">
            <ToolEmpty
              tool={def.key}
              hue={hue}
              sentence={`${def.hint}.${def.outward ? " It tests access from this server, not what an outside visitor can reach." : ""}`}
              busy={t.busy}
              example={def.needsTarget ? exampleTarget(def.targetPlaceholder) : undefined}
              onExample={() => {
                t.setTarget(exampleTarget(def.targetPlaceholder) ?? "")
                targetRef.current?.focus()
              }}
            />
          </div>
        )}
        {t.result && (
          <div className="space-y-3 pt-5">
            <ToolResult
              result={t.result}
              successLabel={def.key === "wol" ? "packet sent" : undefined}
            />
            {t.result.resultId && (
              <SaveHeldResult
                key={t.result.resultId}
                resultId={t.result.resultId}
                label={def.label}
                target={t.result.target}
              />
            )}
          </div>
        )}

        {def.key === "ssh" && (
          <SSHTrustActions target={t.target} port={Number(t.port) || 22} scan={t.result} />
        )}
        {def.key === "port" && t.result && Number(t.port) > 0 && (
          <WatchProbeButton target={t.target.trim()} port={Number(t.port)} />
        )}
        {def.key === "wol" && active && <WakeDevices run={t} />}

        {t.past.length > 0 && (
          <div className="flex flex-wrap items-center gap-1.5">
            <span className="eyebrow">earlier</span>
            {/* By position: two runs of one target can take the same time. */}
            {t.past.map((p, index) => (
              <button
                key={index}
                type="button"
                onClick={() => t.restore(p)}
                className="rounded-sm border border-hairline px-1.5 py-px font-mono text-micro text-muted-foreground focus-ring transition-colors hover:border-rule-primary hover:text-foreground"
              >
                <span className={cn("mr-1", p.ok ? "text-success" : "text-destructive")}>
                  {p.ok ? "✓" : "✗"}
                </span>
                {p.target} · {p.duration}
              </button>
            ))}
          </div>
        )}
      </div>
    </div>
  )
}
