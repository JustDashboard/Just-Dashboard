"use client"

import { useMemo, useState } from "react"
import { Box, Warning } from "@/components/icons"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyNote, ErrorState, LoadingRows, Notice } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Switch } from "@/components/ui/switch"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { get, put } from "@/lib/api"
import { notify } from "@/lib/toast"
import type { BlueprintProperty, GameProperties } from "@/lib/types"

/**
 * The server's own settings file. Only the keys the blueprint declares get a
 * control; everything else stays in the raw preview and is never rewritten by
 * this editor. Ported from the pre-rebuild `GameSettingsTab`.
 */
export function GameSettings({ projectId }: { projectId: number }) {
  const { can } = useAuth()
  const [draft, setDraft] = useState<Record<string, string>>({})
  const [saving, setSaving] = useState(false)
  const properties = usePoll(
    (signal) => get<GameProperties>(`/deploy/${projectId}/game/properties`, undefined, signal),
    0,
    [projectId],
  )
  const values = useMemo(() => properties.data?.values ?? {}, [properties.data])
  const changed = useMemo(
    () => Object.entries(draft).filter(([key, value]) => (values[key] ?? "") !== value),
    [draft, values],
  )

  const save = async () => {
    if (changed.length === 0) return
    setSaving(true)
    try {
      const result = await put<{ applied: string[]; restartRequired: boolean }>(
        `/deploy/${projectId}/game/properties`,
        { changes: Object.fromEntries(changed) },
      )
      notify.success(
        result.applied.length === 1
          ? `${result.applied[0]} saved`
          : `${result.applied.length} settings saved`,
        result.restartRequired
          ? { description: "Restart the server for them to take effect." }
          : undefined,
      )
      setDraft({})
      properties.refresh()
    } catch (error) {
      notify.error("Could not save these settings", error)
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="space-y-4">
      <Panel plain>
        <PanelHeader
          title="Server settings"
          actions={
            can("system.admin") && (
              <Button size="sm" disabled={changed.length === 0} pending={saving} onClick={save}>
                {`Save ${changed.length || ""}`.trim()}
              </Button>
            )
          }
        />
        <PanelBody className="space-y-4">
          {properties.error ? (
            <ErrorState error={properties.error} />
          ) : !properties.data ? (
            <LoadingRows rows={5} />
          ) : properties.data.status !== "available" ? (
            <Notice title="Settings unavailable" icon={Box}>
              {properties.data.reason ?? "This file could not be read from the running server."}
            </Notice>
          ) : properties.data.known.length === 0 ? (
            <EmptyNote>This blueprint declares no settings this dashboard edits.</EmptyNote>
          ) : (
            <>
              {properties.data.restartRequired && changed.length > 0 && (
                <Notice title="A restart is needed" tone="warning" icon={Warning}>
                  The server reads this file on start. Saving writes the change; restarting applies
                  it.
                </Notice>
              )}
              <div className="grid min-w-0 gap-4 sm:grid-cols-2">
                {properties.data.known.map((property) => (
                  <PropertyField
                    key={property.key}
                    property={property}
                    value={draft[property.key] ?? values[property.key] ?? property.default ?? ""}
                    disabled={!can("system.admin")}
                    onChange={(value) =>
                      setDraft((previous) => ({ ...previous, [property.key]: value }))
                    }
                  />
                ))}
              </div>
            </>
          )}
        </PanelBody>
      </Panel>

      {properties.data?.raw && (
        <details className="min-w-0 rounded-lg border border-hairline">
          <summary className="cursor-pointer rounded-lg p-4 text-body font-medium focus-ring">
            View the file on disk
          </summary>
          <div className="min-w-0 border-t border-hairline">
            <pre className="max-h-[24rem] overflow-auto p-4 font-mono text-hint leading-relaxed whitespace-pre">
              {properties.data.raw}
            </pre>
          </div>
        </details>
      )}
    </div>
  )
}

function PropertyField({
  property,
  value,
  disabled,
  onChange,
}: {
  property: BlueprintProperty
  value: string
  disabled: boolean
  onChange: (value: string) => void
}) {
  const id = `game-property-${property.key}`
  return (
    <div className="min-w-0 space-y-1.5">
      <Label htmlFor={id}>{property.label}</Label>
      {property.kind === "boolean" ? (
        <div className="flex min-h-9 items-center gap-2">
          <Switch
            id={id}
            checked={value === "true"}
            disabled={disabled}
            onCheckedChange={(checked) => onChange(checked ? "true" : "false")}
          />
          <span className="text-xs text-muted-foreground">{value === "true" ? "On" : "Off"}</span>
        </div>
      ) : property.kind === "choice" ? (
        <Select value={value} disabled={disabled} onValueChange={onChange}>
          <SelectTrigger id={id}>
            <SelectValue placeholder="Choose" />
          </SelectTrigger>
          <SelectContent>
            {(property.choices ?? []).map((choice) => (
              <SelectItem key={choice.value} value={choice.value}>
                {choice.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      ) : (
        <Input
          id={id}
          value={value}
          disabled={disabled}
          inputMode={property.kind === "number" ? "numeric" : undefined}
          min={property.minimum}
          max={property.maximum}
          type={property.kind === "number" ? "number" : "text"}
          onChange={(event) => onChange(event.target.value)}
        />
      )}
      {property.description && (
        <p className="text-hint text-muted-foreground">{property.description}</p>
      )}
      <p className="font-mono text-hint text-muted-foreground">{property.key}</p>
    </div>
  )
}
