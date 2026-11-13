"use client"

import { useAuth } from "@/hooks/use-auth"
import { useMemo, useRef, useState } from "react"
import { del, post } from "@/lib/api"
import { plural } from "@/lib/format"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import type { InterfaceReading, KernelProfile, ProtectionSetting } from "@/lib/types"
import { ConfirmDialog, useConfirm, type ConfirmRequest } from "@/components/confirm-dialog"
import { InfoTip } from "@/components/form"
import { EmptyNote } from "@/components/state"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { Input } from "@/components/ui/input"
import { Skeleton } from "@/components/ui/skeleton"
import { Switch } from "@/components/ui/switch"
import { SaveBarProvider } from "@/components/deploy/settings/save-bar"
import { Segments } from "@/components/deploy/settings/segments"
import {
  SettingForm,
  SettingSection,
  settingStatus,
} from "@/components/deploy/settings/setting-card"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { InterfaceValues } from "@/components/network/protection/interfaces"
import {
  choiceWord,
  inRange,
  profileChanges,
  SETTING_GROUPS,
  settingControl,
  settingGroup,
  settingWord,
  stagedChanges,
} from "@/components/network/protection/reading"

type Only = "all" | "edited"

/** What the reader has staged and not applied: held by the page, so it outlives a look at another view. */
export type KernelDraft = { pending: Record<string, string>; profile?: string }

/** The settings column every settings page in the product is drawn in. */
const COLUMN = "mx-auto w-full max-w-3xl min-w-0"

/**
 * The kernel's own defences against floods and spoofed packets, as the closed
 * list the server offers, in the one centred column a project's settings are
 * read in: a section for each thing defended, a row for each setting with
 * what it protects against behind its ⓘ, its sysctl key, and its control — a
 * switch for a setting the kernel holds as off or on, segments for another
 * small choice, a field for a number.
 *
 * It was two columns of rows side by side, each with a "Running" line under
 * its key and a bar of its own design under them. One column reads top to
 * bottom, and the bar is the product's one save bar (`save-bar.tsx`).
 *
 * Changes are staged, not made: every control writes into a draft, an edited
 * row is marked down its edge in git's modified hue and says what the kernel
 * is still running under its control, and the bar that floats at the foot of
 * the window counts what is staged and applies it together after one
 * confirmation that lists each old and new value. What is applied is restored
 * at boot and shown as "kept for boot"; a kept setting can be let go, which
 * stops it being restored and leaves the running kernel as it is.
 *
 * A row is one height in every state: the line under its control is always
 * there, and says what is kept, what is running under a staged value, or the
 * range a number has left — one at a time, in place.
 */
export function KernelSettings({
  settings,
  resetNote,
  profiles = [],
  interfaces,
  draft,
  onDraft,
  onChanged,
}: {
  settings: ProtectionSetting[]
  resetNote: string
  /** Sets of values for a kind of host, staged like hand-edited ones. */
  profiles?: KernelProfile[]
  /** The per-interface values the five per-device settings come to. */
  interfaces?: InterfaceReading
  draft: KernelDraft
  onDraft: (next: KernelDraft) => void
  onChanged: () => void
}) {
  const { can } = useAuth()
  const admin = can("system.admin")
  const { confirm, dialog } = useConfirm()
  const { pending, profile } = draft
  const [only, setOnly] = useState<Only>("all")
  const [busy, setBusy] = useState(false)
  // The bar's Save waits on the confirmation: it resolves when the change was
  // applied, or when the reader closed the dialog without applying.
  const [review, setReview] = useState<ConfirmRequest | null>(null)
  const settle = useRef<((applied: boolean) => void) | undefined>(undefined)
  const answer = (applied: boolean) => {
    settle.current?.(applied)
    settle.current = undefined
  }

  const stageProfile = (id: string) => {
    const p = profiles.find((x) => x.id === id)
    if (!p) return
    onDraft({ profile: id, pending: { ...pending, ...profileChanges(settings, p) } })
  }
  const changes = useMemo(() => stagedChanges(settings, pending), [settings, pending])
  const keys = Object.keys(changes)
  const dirty = keys.length > 0
  const invalid = settings.some((s) => s.key in pending && !inRange(s, pending[s.key]))
  const valueOf = (s: ProtectionSetting) => pending[s.key] ?? s.current
  const edited = (s: ProtectionSetting) => s.key in changes
  const byKey = (key: string) => settings.find((s) => s.key === key)

  const shown = only === "edited" && dirty ? settings.filter(edited) : settings
  const groups = [
    ...SETTING_GROUPS.map((g) => g.title),
    ...(settings.some((s) => settingGroup(s.key) === "Other settings") ? ["Other settings"] : []),
  ]

  const apply = () =>
    new Promise<boolean>((resolve) => {
      settle.current = resolve
      setReview({
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
            onDraft({ pending: {} })
            notify.success(`${plural(keys.length, "kernel setting")} applied`)
            onChanged()
            answer(true)
            return "reported"
          } finally {
            setBusy(false)
          }
        },
      })
    })

  const letGo = (s: ProtectionSetting) =>
    confirm({
      title: `Stop keeping ${s.label}`,
      confirmLabel: "Stop keeping",
      description: <p>{resetNote}</p>,
      action: async () => {
        await del(`/network/protection/settings/${encodeURIComponent(s.key)}`)
        onDraft({
          profile,
          pending: Object.fromEntries(Object.entries(pending).filter(([k]) => k !== s.key)),
        })
        notify.success(`${s.label} is no longer kept for boot`)
        onChanged()
        return "reported"
      },
    })

  const chosen = profiles.find((p) => p.id === profile)
  return (
    <SaveBarProvider>
      {/* The room at the foot is the save bar's, so it never lies over the last row. */}
      <div className={cn(COLUMN, "space-y-8 pb-20")}>
        <div className="flex min-w-0 flex-wrap items-center justify-between gap-x-4 gap-y-3">
          <ChipStrip aria-label="Which settings to show">
            <FilterChip selected={only === "all" || !dirty} onClick={() => setOnly("all")}>
              All <ChipCount>{settings.length}</ChipCount>
            </FilterChip>
            {dirty && (
              <FilterChip selected={only === "edited"} onClick={() => setOnly("edited")}>
                <span style={{ color: "var(--git-modified)" }}>Edited</span>
                <ChipCount>{keys.length}</ChipCount>
              </FilterChip>
            )}
          </ChipStrip>
          {profiles.length > 0 && admin && (
            <div className="flex min-w-0 items-center gap-2 max-sm:w-full">
              <Select value={profile ?? ""} onValueChange={stageProfile}>
                <SelectTrigger
                  size="sm"
                  aria-label="Stage a workload profile"
                  className="w-full sm:w-64"
                >
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
              <InfoTip label="About workload profiles">
                {chosen
                  ? `${chosen.why} Staged below; nothing changes until you save.`
                  : "A profile stages the values for a kind of host; review them and apply them together."}
              </InfoTip>
            </div>
          )}
        </div>

        <SettingForm
          name="Kernel protections"
          canEdit={admin}
          dirty={dirty}
          changes={keys.length}
          saving={busy}
          invalid={invalid}
          applies="immediately"
          onSave={apply}
          onDiscard={() => onDraft({ pending: {} })}
        >
          {groups.map((title) => {
            const rows = shown.filter((s) => settingGroup(s.key) === title)
            if (rows.length === 0) return null
            return (
              <SettingSection
                key={title}
                title={title}
                state={<span className="numeric">{plural(rows.length, "setting")}</span>}
                status={settingStatus({ dirty: rows.some(edited) })}
              >
                <div className="divide-y divide-hairline">
                  {rows.map((s) => (
                    <SettingRow
                      key={s.key}
                      setting={s}
                      readOnly={!admin}
                      mayLetGo={admin && can("destructive")}
                      value={valueOf(s)}
                      edited={edited(s)}
                      onChange={(value) =>
                        onDraft({ profile, pending: { ...pending, [s.key]: value } })
                      }
                      onLetGo={() => letGo(s)}
                    />
                  ))}
                </div>
              </SettingSection>
            )
          })}
        </SettingForm>
        {shown.length === 0 && <EmptyNote>No settings match this filter.</EmptyNote>}
        {interfaces && <InterfaceValues reading={interfaces} settings={settings} />}
      </div>
      <ConfirmDialog
        request={review}
        onOpenChange={(open) => {
          if (open) return
          setReview(null)
          answer(false)
        }}
      />
      {dialog}
    </SaveBarProvider>
  )
}

/** The width a row's control and the line under it share. */
const CONTROL = "w-40 sm:w-48"
/** A row's height in every state: its name over its key, or its control over its line. */
const ROW = "min-h-[4.25rem]"

/**
 * One setting as a full-width row on a hairline: its name with why behind
 * the ⓘ, its key, and at the far end its control over one line that says —
 * one at a time — the range a number has left, what the kernel still runs
 * under a staged value, or that the value is kept for boot.
 */
function SettingRow({
  setting: s,
  readOnly,
  mayLetGo,
  value,
  edited,
  onChange,
  onLetGo,
}: {
  setting: ProtectionSetting
  readOnly: boolean
  mayLetGo: boolean
  value: string
  edited: boolean
  onChange: (value: string) => void
  onLetGo: () => void
}) {
  const valid = inRange(s, value)
  const id = `kernel-${s.key}`
  const control = settingControl(s, value)
  return (
    <div
      data-slot="kernel-setting"
      data-edited={edited || undefined}
      className={cn("relative flex min-w-0 items-center justify-between gap-4 py-2.5", ROW)}
    >
      {/* A staged change is marked down the row's edge in git's modified hue,
          the colour §3 gives a change that is not applied yet. */}
      {edited && (
        <span
          aria-hidden
          className="absolute inset-y-2.5 -left-3 w-0.5 rounded-full bg-[var(--git-modified)]"
        />
      )}
      <div className="min-w-0 space-y-0.5">
        <div className="flex min-w-0 items-center gap-2">
          <label htmlFor={id} className="min-w-0 text-sm leading-snug font-medium">
            {s.label}
          </label>
          <InfoTip label={`About ${s.label}`}>{s.why}</InfoTip>
        </div>
        <code className="block font-mono text-hint break-all text-muted-foreground">{s.key}</code>
      </div>

      <div className={cn("flex shrink-0 flex-col items-end gap-1", CONTROL)}>
        <div className="flex h-8 w-full items-center justify-end">
          {readOnly ? (
            <span className="numeric text-body font-medium">
              {s.available ? settingWord(s, s.current) : "—"}
            </span>
          ) : control === "switch" ? (
            <Switch
              id={id}
              checked={value === "1"}
              disabled={!s.available}
              onCheckedChange={(on) => onChange(on ? "1" : "0")}
            />
          ) : control === "segments" ? (
            <Segments
              id={id}
              label={s.label}
              value={value}
              disabled={!s.available}
              options={s.allowed.map((a) => ({ value: a, label: choiceWord(s.key, a) }))}
              onChange={onChange}
              fill
            />
          ) : (
            <Input
              id={id}
              value={value}
              inputMode="numeric"
              disabled={!s.available}
              aria-invalid={!valid || undefined}
              aria-describedby={valid ? undefined : `${id}-range`}
              onChange={(event) => onChange(event.target.value)}
              className="h-8 font-mono"
            />
          )}
        </div>
        {/* Always there, so a row is one height whatever it has to say. */}
        <p className="numeric h-4 max-w-full truncate text-hint leading-4 text-muted-foreground">
          {!s.available ? (
            "not in this kernel"
          ) : !valid ? (
            <span id={`${id}-range`} role="alert" className="text-destructive">
              <span className="sr-only">A whole number from </span>
              {s.min.toLocaleString()} to {s.max.toLocaleString()}
              <span className="sr-only">.</span>
            </span>
          ) : edited ? (
            <span style={{ color: "var(--git-modified)" }}>
              running <span className="font-mono">{settingWord(s, s.current)}</span>
            </span>
          ) : s.setHere ? (
            <>
              <span title="Restored at every boot">kept for boot</span>
              {mayLetGo && (
                <>
                  {" · "}
                  <button
                    type="button"
                    onClick={onLetGo}
                    aria-label={`Stop keeping ${s.label}`}
                    className="rounded-sm underline underline-offset-2 focus-ring hover:text-foreground"
                  >
                    stop keeping
                  </button>
                </>
              )}
            </>
          ) : null}
        </p>
      </div>
    </div>
  )
}

/**
 * The settings column while its first read is out: the chips' line, then the
 * three sections the server always sends, five rows each, at their heights.
 */
export function KernelSkeleton() {
  return (
    <div className={cn(COLUMN, "space-y-8 pb-20")} aria-hidden>
      <Skeleton className="h-7 w-24 rounded-md" />
      <div className="divide-y divide-hairline">
        {SETTING_GROUPS.map((group) => (
          <div key={group.title} className="space-y-4 py-8 first:pt-0 last:pb-0">
            <div className="space-y-1">
              <p className="text-base font-semibold tracking-tight">{group.title}</p>
              <Skeleton className="h-4 w-20" />
            </div>
            <div className="divide-y divide-hairline">
              {group.keys.map((key) => (
                <div
                  key={key}
                  className={cn("flex items-center justify-between gap-4 py-2.5", ROW)}
                >
                  <div className="space-y-2">
                    <Skeleton className="h-4 w-44" />
                    <Skeleton className="h-3 w-56 max-w-full" />
                  </div>
                  <Skeleton className="h-8 w-24 rounded-md" />
                </div>
              ))}
            </div>
          </div>
        ))}
      </div>
    </div>
  )
}
