"use client"

import { useAuth } from "@/hooks/use-auth"
import { useMemo, useState } from "react"
import { del, post } from "@/lib/api"
import { plural } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { InterfaceReading, KernelProfile, ProtectionSetting } from "@/lib/types"
import { useConfirm } from "@/components/confirm-dialog"
import { FormSection, InfoTip } from "@/components/form"
import { EmptyNote } from "@/components/state"
import { Status, StatusDot } from "@/components/status-dot"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Segments } from "@/components/deploy/settings/segments"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { InterfaceValues } from "@/components/network/protection/interfaces"
import {
  below,
  choiceWord,
  inRange,
  profileChanges,
  SETTING_GROUPS,
  settingGroup,
  settingWord,
  stagedChanges,
} from "@/components/network/protection/reading"

type Only = "all" | "attention" | "edited"

/**
 * The kernel's own defences against floods and spoofed packets, as the closed
 * list the server offers: each with what it protects against, the value the
 * kernel is running, the value to aim for, and the control to change it.
 *
 * Changes are staged, not made: every control writes into a draft, an edited
 * row is marked down its edge in git's modified hue, and a bar that follows
 * the reader lists what is staged and applies it together after one
 * confirmation — the way the SSH page applies its directives. What is applied
 * is restored at boot and shown as "kept for boot"; a kept setting can be let
 * go, which stops it being restored and leaves the running kernel as it is.
 */
export function KernelSettings({
  settings,
  resetNote,
  profiles = [],
  interfaces,
  onChanged,
}: {
  settings: ProtectionSetting[]
  resetNote: string
  /** Sets of values for a kind of host, staged like hand-edited ones. */
  profiles?: KernelProfile[]
  /** The per-interface values the five per-device settings come to. */
  interfaces?: InterfaceReading
  onChanged: () => void
}) {
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  const [pending, setPending] = useState<Record<string, string>>({})
  const [profile, setProfile] = useState<string>()
  const stageProfile = (id: string) => {
    const p = profiles.find((x) => x.id === id)
    if (!p) return
    setProfile(id)
    setPending((current) => ({ ...current, ...profileChanges(settings, p) }))
  }
  const [only, setOnly] = useState<Only>("all")
  const [busy, setBusy] = useState(false)

  const changes = useMemo(() => stagedChanges(settings, pending), [settings, pending])
  const keys = Object.keys(changes)
  const dirty = keys.length > 0
  const invalid = settings.some((s) => s.key in pending && !inRange(s, pending[s.key]))
  const attention = settings.filter(below).length
  const valueOf = (s: ProtectionSetting) => pending[s.key] ?? s.current
  const edited = (s: ProtectionSetting) => s.key in changes
  const byKey = (key: string) => settings.find((s) => s.key === key)

  const shown =
    only === "attention"
      ? settings.filter((s) => below(s) || edited(s))
      : only === "edited"
        ? settings.filter(edited)
        : settings
  const groups = [
    ...SETTING_GROUPS.map((g) => g.title),
    ...(settings.some((s) => settingGroup(s.key) === "Other settings") ? ["Other settings"] : []),
  ]

  const apply = () =>
    confirm({
      title: `Apply ${plural(keys.length, "kernel setting")}`,
      confirmLabel: "Apply",
      description: (
        <div className="space-y-3">
          <ul className="space-y-1">
            {keys.map((key) => {
              const s = byKey(key)
              if (!s) return null
              return (
                <li key={key} className="flex min-w-0 flex-wrap items-baseline gap-x-2">
                  <span className="font-medium">{s.label}</span>
                  <span className="font-mono text-hint text-muted-foreground">
                    {settingWord(s, s.current)} → {settingWord(s, changes[key])}
                  </span>
                  {changes[key] !== s.recommended && (
                    <span className="text-hint text-warning">
                      not the recommended {settingWord(s, s.recommended)}
                    </span>
                  )}
                </li>
              )
            })}
          </ul>
          <p>
            They take effect now and are set again at every boot. Weakening one gives protection
            away until it is put back.
          </p>
        </div>
      ),
      action: async () => {
        setBusy(true)
        try {
          await post("/network/protection/settings", { values: changes })
          setPending({})
          notify.success(`${plural(keys.length, "kernel setting")} applied`)
          onChanged()
        } finally {
          setBusy(false)
        }
      },
    })

  const letGo = (s: ProtectionSetting) =>
    confirm({
      title: `Stop keeping ${s.label}`,
      confirmLabel: "Stop keeping",
      description: <p>{resetNote}</p>,
      action: async () => {
        await del(`/network/protection/settings/${encodeURIComponent(s.key)}`)
        setPending((p) => Object.fromEntries(Object.entries(p).filter(([k]) => k !== s.key)))
        notify.success(`${s.label} is no longer kept for boot`)
        onChanged()
      },
    })

  const chosen = profiles.find((p) => p.id === profile)
  return (
    <div className="flex min-w-0 flex-col gap-6">
      {profiles.length > 0 && can("system.admin") && (
        <div className="flex min-w-0 flex-wrap items-center gap-3">
          <Select value={profile ?? ""} onValueChange={stageProfile}>
            <SelectTrigger aria-label="Stage a workload profile" className="w-full sm:w-72">
              <SelectValue placeholder="Stage a workload profile" />
            </SelectTrigger>
            <SelectContent>
              {profiles.map((p) => (
                <SelectItem key={p.id} value={p.id}>
                  {p.name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <p className="min-w-0 flex-1 text-hint text-muted-foreground">
            {chosen
              ? `${chosen.why} Staged below; nothing changes until you apply.`
              : "A profile stages the values for a kind of host; review them and apply together."}
          </p>
        </div>
      )}
      <ChipStrip aria-label="Which settings to show">
        <FilterChip selected={only === "all"} onClick={() => setOnly("all")}>
          All <ChipCount>{settings.length}</ChipCount>
        </FilterChip>
        <FilterChip selected={only === "attention"} onClick={() => setOnly("attention")}>
          <StatusDot tone={attention ? "warning" : "running"} />
          Below recommendation <ChipCount>{attention}</ChipCount>
        </FilterChip>
        {dirty && (
          <FilterChip selected={only === "edited"} onClick={() => setOnly("edited")}>
            <span style={{ color: "var(--git-modified)" }}>Edited</span>
            <ChipCount>{keys.length}</ChipCount>
          </FilterChip>
        )}
      </ChipStrip>

      {/* Two columns from `xl`, as the SSH page lays its directives out: a
          48rem column of fifteen settings leaves most of a wide screen empty. */}
      <div className="grid min-w-0 gap-x-12 gap-y-12 xl:grid-cols-2">
        {groups.map((title) => {
          const rows = shown.filter((s) => settingGroup(s.key) === title)
          if (rows.length === 0) return null
          const warnings = rows.filter(below).length
          return (
            <FormSection
              aside
              key={title}
              title={title}
              className="max-w-none py-0 first:pt-0 last:pb-0"
              actions={
                rows.some(edited) && <Tag style={{ color: "var(--git-modified)" }}>Edited</Tag>
              }
              hint={
                <span className="flex flex-wrap items-center gap-x-3 gap-y-1">
                  <Status
                    verdict={warnings ? "warning" : "ok"}
                    label={warnings ? `${warnings} below recommendation` : "At recommendation"}
                  />
                  <span className="numeric">{plural(rows.length, "setting")}</span>
                </span>
              }
            >
              <div className="divide-y divide-hairline">
                {rows.map((s) => (
                  <SettingRow
                    key={s.key}
                    setting={s}
                    readOnly={!can("system.admin")}
                    value={valueOf(s)}
                    edited={edited(s)}
                    onChange={(value) => setPending((p) => ({ ...p, [s.key]: value }))}
                    onLetGo={() => letGo(s)}
                  />
                ))}
              </div>
            </FormSection>
          )
        })}
      </div>
      {shown.length === 0 && (
        <EmptyNote>Every setting is at or above its recommendation.</EmptyNote>
      )}
      {interfaces && <InterfaceValues reading={interfaces} settings={settings} />}

      {/* The apply bar follows the reader, as SSH's does: a change may be
          staged at the top of the list and the rest is a screen under it. It
          takes a popover's surface, because it is the one thing that floats. */}
      {dirty && (
        <div className="pointer-events-none sticky bottom-4 z-20 flex justify-center">
          <div
            role="region"
            aria-label="Apply kernel settings"
            className="pointer-events-auto flex w-full max-w-xl animate-rise items-center gap-3 rounded-xl border border-border-strong bg-popover py-2.5 pr-2.5 pl-4 text-popover-foreground shadow-lg"
          >
            <div className="min-w-0 flex-1">
              <p className="flex items-center gap-2 text-body leading-snug font-medium">
                <StatusDot tone="warning" />
                <span className="numeric">{plural(keys.length, "unsaved change")}</span>
              </p>
              <p className="mt-0.5 pl-4 text-xs text-muted-foreground max-sm:line-clamp-2 sm:truncate">
                {invalid
                  ? "A value is out of range — fix it to apply"
                  : keys.map((key) => byKey(key)?.label ?? key).join(" · ")}
              </p>
            </div>
            <div className="flex shrink-0 items-center gap-1.5">
              <Button
                variant="ghost"
                size="sm"
                onClick={() => {
                  setPending({})
                  setProfile(undefined)
                }}
                disabled={busy}
                className="max-sm:h-10"
              >
                Discard
              </Button>
              <Button
                size="sm"
                onClick={apply}
                disabled={!can("system.admin") || busy || invalid}
                pending={busy}
                className="max-sm:h-10"
              >
                Apply
              </Button>
            </div>
          </div>
        </div>
      )}
      {dialog}
    </div>
  )
}

/** One setting: its name and why, what is running against what is recommended, and the control. */
function SettingRow({
  setting: s,
  readOnly,
  value,
  edited,
  onChange,
  onLetGo,
}: {
  setting: ProtectionSetting
  readOnly: boolean
  value: string
  edited: boolean
  onChange: (value: string) => void
  onLetGo: () => void
}) {
  const behind = below(s)
  const valid = inRange(s, value)
  const id = `kernel-${s.key}`
  return (
    <div className="relative grid min-w-0 items-center gap-x-6 gap-y-3 py-5 first:pt-0 sm:grid-cols-[minmax(0,1fr)_12rem]">
      {/* A staged change is marked down the row's edge in git's modified hue,
          the colour §3 gives a change that is not applied yet. */}
      {edited && (
        <span
          aria-hidden
          className="absolute inset-y-5 -left-3 w-0.5 rounded-full bg-[var(--git-modified)]"
        />
      )}
      <div className="min-w-0 space-y-1">
        <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
          <label htmlFor={id} className="text-body font-medium">
            {s.label}
          </label>
          <InfoTip label={`About ${s.label}`}>{s.why}</InfoTip>
          {edited ? (
            <Tag style={{ color: "var(--git-modified)" }}>edited</Tag>
          ) : (
            behind && <Status verdict="warning" label="below recommendation" />
          )}
          {s.setHere && <Tag title="Restored at every boot">kept for boot</Tag>}
        </div>
        <code className="block font-mono text-hint break-all text-muted-foreground">{s.key}</code>
        {s.available ? (
          <p className="text-hint leading-relaxed text-muted-foreground">
            Running <span className="font-mono text-foreground">{settingWord(s, s.current)}</span>
            {" · "}
            recommended{" "}
            <span className={behind ? "font-mono text-warning" : "font-mono text-foreground"}>
              {settingWord(s, s.recommended)}
            </span>
            {s.setHere && !readOnly && (
              <>
                {" · "}
                <button
                  type="button"
                  onClick={onLetGo}
                  className="rounded-sm underline underline-offset-2 focus-ring hover:text-foreground"
                >
                  stop keeping it
                </button>
              </>
            )}
          </p>
        ) : (
          <p className="text-hint text-muted-foreground">This kernel does not have this setting.</p>
        )}
      </div>

      <div className="min-w-0">
        {s.kind === "choice" && s.allowed.length <= 3 ? (
          <Segments
            id={id}
            label={s.label}
            value={value}
            disabled={readOnly || !s.available}
            options={s.allowed.map((a) => ({ value: a, label: choiceWord(s.key, a) }))}
            onChange={onChange}
            fill
          />
        ) : (
          <>
            <Input
              id={id}
              value={value}
              inputMode="numeric"
              disabled={readOnly || !s.available}
              aria-invalid={!valid || undefined}
              aria-describedby={valid ? undefined : `${id}-range`}
              onChange={(event) => onChange(event.target.value)}
              className="font-mono"
            />
            {!valid && (
              <p id={`${id}-range`} role="alert" className="mt-1 text-hint text-destructive">
                A whole number from {s.min.toLocaleString()} to {s.max.toLocaleString()}.
              </p>
            )}
          </>
        )}
      </div>
    </div>
  )
}
