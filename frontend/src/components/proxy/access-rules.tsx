"use client"

import { useEffect, useState } from "react"
import { ArrowDown, ArrowUp, Plus, Trash } from "@/components/icons"
import { get } from "@/lib/api"
import { QUICK_RANGES, firstMatch } from "@/lib/cidr"
import type { Exposure, StreamAccess, StreamRule } from "@/lib/types"
import { Field } from "@/components/form"
import { IconAction } from "@/components/icon-action"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"

/**
 * A stream's ordered allow and deny list. A raw stream has no
 * authentication of its own, so this is the only thing deciding who reaches
 * the backend — and nginx takes the first rule a client matches, which a
 * comma-separated allow list could never show. The order is the meaning, so
 * it is drawn numbered and moved by hand.
 */
export function AccessRules({
  access,
  error,
  onChange,
}: {
  access: StreamAccess
  /** Why the list cannot be saved, from accessError. */
  error: string
  onChange: (access: StreamAccess) => void
}) {
  // The address this browser reaches the dashboard from, which every signed-in
  // account may read. It is only the stream's client when the stream is
  // reached the same way, so the reading below says whose view it is.
  const [client, setClient] = useState("")
  useEffect(() => {
    const controller = new AbortController()
    get<Exposure>("/exposure", undefined, controller.signal)
      .then((exposure) => setClient(exposure.client ?? ""))
      .catch(() => setClient(""))
    return () => controller.abort()
  }, [])

  const { rules, defaultAllow } = access
  const setRules = (next: StreamRule[]) => onChange({ rules: next, defaultAllow })
  const update = (i: number, change: Partial<StreamRule>) =>
    setRules(rules.map((rule, j) => (j === i ? { ...rule, ...change } : rule)))
  const move = (i: number, by: -1 | 1) => {
    const next = [...rules]
    ;[next[i], next[i + by]] = [next[i + by], next[i]]
    setRules(next)
  }
  const listed = (source: string) => rules.some((rule) => rule.source.trim() === source)
  const add = (sources: string[]) =>
    setRules([
      ...rules,
      ...sources
        .filter((source) => source === "" || !listed(source))
        .map((source) => ({ action: "allow" as const, source })),
    ])

  const match = client ? firstMatch(rules, client) : -1
  const admitted = match >= 0 ? rules[match].action === "allow" : defaultAllow

  return (
    <Field
      label="Who may connect"
      hint="The first rule a client matches decides; nothing below it is read. A stream has no authentication of its own, so these rules are the only gate."
      error={error}
    >
      <div className="space-y-2">
        {rules.length > 0 && (
          <ol className="space-y-1.5">
            {rules.map((rule, i) => (
              <li key={i} className="flex min-w-0 items-center gap-1.5">
                <span className="numeric w-4 shrink-0 text-right text-hint text-muted-foreground">
                  {i + 1}
                </span>
                <ToggleGroup
                  type="single"
                  value={rule.action}
                  onValueChange={(v) => v && update(i, { action: v as StreamRule["action"] })}
                  variant="outline"
                  size="sm"
                  aria-label={`Rule ${i + 1}`}
                >
                  <ToggleGroupItem value="allow" className="text-hint">
                    Allow
                  </ToggleGroupItem>
                  <ToggleGroupItem value="deny" className="text-hint">
                    Deny
                  </ToggleGroupItem>
                </ToggleGroup>
                <Input
                  value={rule.source}
                  onChange={(e) => update(i, { source: e.target.value })}
                  placeholder="10.0.0.0/8"
                  aria-label={`Rule ${i + 1} source`}
                  className="min-w-0 flex-1 font-mono text-xs"
                />
                <IconAction label="Move up" disabled={i === 0} onClick={() => move(i, -1)}>
                  <ArrowUp />
                </IconAction>
                <IconAction
                  label="Move down"
                  disabled={i === rules.length - 1}
                  onClick={() => move(i, 1)}
                >
                  <ArrowDown />
                </IconAction>
                <IconAction
                  label={`Remove rule ${i + 1}`}
                  className="text-muted-foreground hover:text-destructive"
                  onClick={() => setRules(rules.filter((_, j) => j !== i))}
                >
                  <Trash />
                </IconAction>
              </li>
            ))}
          </ol>
        )}

        <div className="flex flex-wrap gap-1.5">
          <Button size="xs" variant="outline" onClick={() => add([""])}>
            <Plus />
            Rule
          </Button>
          {client && (
            <Button
              size="xs"
              variant="outline"
              disabled={listed(client)}
              onClick={() => add([client])}
            >
              My address
            </Button>
          )}
          {QUICK_RANGES.map((range) => (
            <Button
              key={range.id}
              size="xs"
              variant="outline"
              title={range.sources.join(", ")}
              disabled={range.sources.every(listed)}
              onClick={() => add(range.sources)}
            >
              {range.label}
            </Button>
          ))}
        </div>

        <div className="flex min-w-0 items-center justify-between gap-3">
          <span className="text-xs">{rules.length > 0 ? "Everyone else" : "Everyone"}</span>
          <ToggleGroup
            type="single"
            value={defaultAllow ? "allow" : "deny"}
            onValueChange={(v) => v && onChange({ rules, defaultAllow: v === "allow" })}
            variant="outline"
            size="sm"
            aria-label="Everyone else"
          >
            <ToggleGroupItem value="allow" className="text-hint">
              Let in
            </ToggleGroupItem>
            <ToggleGroupItem value="deny" className="text-hint">
              Turn away
            </ToggleGroupItem>
          </ToggleGroup>
        </div>

        {client && (
          <p className="text-hint text-muted-foreground">
            Your address as this dashboard sees it, <span className="font-mono">{client}</span>, is{" "}
            {admitted ? "let in" : "turned away"}{" "}
            {match >= 0 ? `by rule ${match + 1}` : "with everyone else"}. Reaching the stream
            another way arrives from another address.
          </p>
        )}
      </div>
    </Field>
  )
}
