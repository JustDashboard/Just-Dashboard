"use client"

import { useId, useMemo, useRef, useState } from "react"
import { useAuth } from "@/hooks/use-auth"
import { post } from "@/lib/api"
import { sentenceCase } from "@/lib/format"
import type { ForwardingFamily, NetworkForwarding } from "@/lib/types"
import { Bridge } from "@/components/icons"
import { InfoTip } from "@/components/form"
import { ProductGlyph } from "@/components/product-logo"
import { Status } from "@/components/status-dot"
import { Switch } from "@/components/ui/switch"
import { ConfirmDialog } from "@/components/confirm-dialog"
import { WireHost, WireMark } from "@/components/deploy/wire"
import { WireFlow, type WireFlowNode, type WireLine } from "@/components/wire-flow"
import {
  SettingForm,
  SettingSection,
  settingStatus,
} from "@/components/deploy/settings/setting-card"
import { forwardingNeeds, forwardingReading, needProduct } from "./route-reading"

type Family = "ipv4" | "ipv6"
const FAMILIES: { key: Family; name: string; short: string }[] = [
  { key: "ipv4", name: "IPv4", short: "v4" },
  { key: "ipv6", name: "IPv6", short: "v6" },
]

/**
 * Whether this server routes packets that are not its own, per family: a
 * setting, so it is drawn as one — a picture of what it carries and for
 * whom, then a switch row on a hairline for each family, changed as a draft
 * and written by the page's save bar — where it stood mid-page as two
 * half-width blocks with a sentence each and three footnotes.
 *
 * What depends on forwarding is in the picture as the products it is,
 * because a switch that silently breaks Docker's networks is the one an
 * operator flips once; turning one off is refused while something needs it,
 * and the refusal is drawn before it is asked: the switch stays, disabled,
 * with the guard's sentence behind the ⓘ beside its name. Turning one off is
 * still asked about, at Save. An account that cannot change it reads the
 * state in the switch's place.
 */
export function ForwardingSettings({
  forwarding,
  onChanged,
}: {
  forwarding: NetworkForwarding
  onChanged: () => void
}) {
  const { can } = useAuth()
  const admin = can("system.admin")
  const [draft, setDraft] = useState<Partial<Record<Family, boolean>>>({})
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string>()
  const [asking, setAsking] = useState<{ families: Family[]; settle: (ok: boolean) => void }>()
  const turnedOff = useRef(false)

  // A draft is an edit only while the server still says otherwise, so a
  // saved switch stops being one the moment its new state is read back.
  const wanted = (family: Family) => draft[family] ?? forwarding[family].enabled
  const changed = FAMILIES.filter(({ key }) => wanted(key) !== forwarding[key].enabled)
  const named = (families: Family[]) =>
    FAMILIES.filter(({ key }) => families.includes(key))
      .map(({ name }) => name)
      .join(" and ")

  const save = async () => {
    if (saving) return false
    setError(undefined)
    setSaving(true)
    try {
      for (const { key } of changed.filter(({ key }) => wanted(key)))
        await post(`/network/forwarding/${key}/on`)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
      setSaving(false)
      onChanged()
      return false
    }
    const off = changed.filter(({ key }) => !wanted(key)).map(({ key }) => key)
    if (off.length === 0) {
      setSaving(false)
      onChanged()
      return true
    }
    // Stopping is asked about once, here: the bar's Save waits on the answer.
    turnedOff.current = false
    return new Promise<boolean>((settle) => setAsking({ families: off, settle }))
  }

  return (
    <SettingForm
      name="Forwarding"
      onSave={save}
      dirty={changed.length > 0}
      changes={changed.length}
      saving={saving}
      canEdit={admin}
      onDiscard={() => setDraft({})}
      applies="immediately"
      error={error}
    >
      <SettingSection title="Forwarding" status={settingStatus({ dirty: changed.length > 0 })}>
        <ForwardingPicture forwarding={forwarding} />
        <div className="min-w-0 divide-y divide-hairline">
          {FAMILIES.map(({ key, name }) => (
            <FamilyRow
              key={key}
              name={name}
              state={forwarding[key]}
              checked={wanted(key)}
              admin={admin}
              destructive={can("destructive")}
              busy={saving}
              onChange={(next) => setDraft((held) => ({ ...held, [key]: next }))}
            />
          ))}
        </div>
      </SettingSection>
      <ConfirmDialog
        request={
          asking
            ? {
                title: `Stop forwarding ${named(asking.families)}`,
                confirmLabel: "Turn off",
                description: (
                  <p>
                    This server stops passing {named(asking.families)} traffic between its networks:
                    every tunnel, NAT entry and container network that relies on it stops reaching
                    anything beyond this machine.
                  </p>
                ),
                action: async () => {
                  for (const family of asking.families)
                    await post(`/network/forwarding/${family}/off`)
                  turnedOff.current = true
                  // The save bar says Saved; a second "completed" would say it twice.
                  return "reported"
                },
              }
            : null
        }
        onOpenChange={(open) => {
          if (open || !asking) return
          asking.settle(turnedOff.current)
          setAsking(undefined)
          setSaving(false)
          onChanged()
        }}
      />
    </SettingForm>
  )
}

/**
 * What the setting is, drawn: each family arriving, this server passing it
 * on or not, and what stops working without it. A family's line moves while
 * the kernel's own counter says datagrams are being forwarded, is still when
 * it is on and idle, amber where a device's own switch keeps its traffic
 * out, and dashed when the family is off.
 */
function ForwardingPicture({ forwarding }: { forwarding: NetworkForwarding }) {
  const start = useMemo<WireFlowNode[]>(
    () =>
      FAMILIES.map(({ key, name, short }) => {
        const state = forwarding[key]
        const reading = forwardingReading(state)
        const stuck = state.health?.disabled ?? []
        return {
          id: key,
          name: `${name}: ${reading.label}`,
          mark: (size) => (
            <WireMark tone="logo" size={size}>
              <span className="numeric font-mono text-micro">{short}</span>
            </WireMark>
          ),
          eyebrow: name,
          title: (
            <span className={reading.tone === "warning" ? "text-warning" : undefined}>
              {reading.word}
            </span>
          ),
          hint:
            [
              stuck.length > 0 && `not from ${stuck.join(", ")}`,
              reading.rate,
              state.enabled && state.persisted && "set at boot",
            ]
              .filter(Boolean)
              .join(" · ") || undefined,
          line: lineOf(state),
        }
      }),
    [forwarding],
  )
  const end = useMemo<WireFlowNode[]>(
    () =>
      forwardingNeeds(forwarding).map(({ need, ipv4, ipv6 }) => {
        const product = needProduct(need)
        const families = FAMILIES.filter(({ key }) => (key === "ipv4" ? ipv4 : ipv6))
        const states = families.map(({ key }) => forwarding[key])
        return {
          id: `need:${need}`,
          name: need,
          mark: (size) => (
            <WireMark tone="logo" size={size}>
              {product ? <ProductGlyph id={product} /> : <Bridge aria-hidden />}
            </WireMark>
          ),
          title: sentenceCase(need),
          hint: families.map(({ name }) => name).join(" and "),
          line: states.every((state) => state.enabled)
            ? {
                state: "up",
                moving: states.some((state) => state.health?.status === "forwarding"),
              }
            : { state: "down" },
        }
      }),
    [forwarding],
  )
  const on = forwarding.ipv4.enabled || forwarding.ipv6.enabled
  return (
    <WireFlow
      className="pb-2"
      start={{ title: "Arrives as", nodes: start, heading: "always" }}
      hub={{
        mark: <WireHost />,
        eyebrow: "This server",
        title: on ? "Passes it on" : "Keeps to its own",
      }}
      end={{
        title: "Needed by",
        nodes: end,
        heading: "always",
        empty: <span className="text-body text-muted-foreground">Nothing here needs it</span>,
      }}
    />
  )
}

function lineOf(state: ForwardingFamily): WireLine {
  if (!state.available || !state.enabled) return { state: "down" }
  if (state.health?.status === "partial") return { state: "warning" }
  return { state: "up", moving: state.health?.status === "forwarding" }
}

function FamilyRow({
  name,
  state,
  checked,
  admin,
  destructive,
  busy,
  onChange,
}: {
  name: string
  state: ForwardingFamily
  checked: boolean
  admin: boolean
  destructive: boolean
  busy: boolean
  onChange: (next: boolean) => void
}) {
  const titleId = useId()
  const locked = state.enabled && !!state.guard
  const health = state.health
  const notes = [
    locked && state.guard,
    state.enabled &&
      !locked &&
      admin &&
      !destructive &&
      "Turning it off needs the destructive capability.",
    state.dockerBasis,
    health?.reason,
    health?.forwarded !== undefined &&
      `${health.forwarded.toLocaleString("en-US")} forwarded since the kernel started counting.`,
  ].filter((note): note is string => Boolean(note))
  const reading = forwardingReading(state)
  return (
    <div
      data-slot="forwarding-row"
      className="flex min-h-12 min-w-0 items-center justify-between gap-6 py-2"
    >
      <p className="flex min-w-0 items-center gap-1.5 text-sm leading-snug font-medium">
        <span id={titleId}>{name} forwarding</span>
        {notes.length > 0 && (
          <InfoTip label={`About ${name} forwarding`}>
            <span className="block space-y-1.5 text-left">
              {notes.map((note) => (
                <span key={note} className="block">
                  {note}
                </span>
              ))}
            </span>
          </InfoTip>
        )}
      </p>
      {admin ? (
        <Switch
          className="shrink-0"
          aria-labelledby={titleId}
          checked={checked}
          disabled={!state.available || busy || locked || (state.enabled && !destructive)}
          onCheckedChange={onChange}
        />
      ) : (
        // No switch for an account that cannot press it: the state, in its place.
        <Status
          tone={reading.tone}
          label={!state.available ? reading.word : state.enabled ? "On" : "Off"}
        />
      )}
    </div>
  )
}
