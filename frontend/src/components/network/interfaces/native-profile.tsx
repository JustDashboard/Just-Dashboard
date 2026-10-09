"use client"

import { useLayoutEffect, useRef, useState } from "react"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { get, put } from "@/lib/api"
import {
  readNativeProfile,
  type NativeFamily,
  type NativeProfile,
} from "@/lib/network-native-profile"
import { useConfirm } from "@/components/confirm-dialog"
import { Detail, DetailList } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Field, FieldRow, FormSection, OptionRow } from "@/components/form"
import { Status } from "@/components/status-dot"
import { NetworkReadWarning } from "@/components/network/read-warning"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"

type DraftFamily = Omit<NativeFamily, "addresses" | "dns" | "domains"> & {
  addresses: string
  dns: string
  domains: string
}
type NativeDraft = {
  generation: string
  owner: string
  renderer?: string
  profile?: string
  ipv4: DraftFamily
  ipv6: DraftFamily
}

const split = (value: string) =>
  value
    .trim()
    .split(/[\s,;]+/)
    .filter(Boolean)
const draftFamily = (family: NativeFamily): DraftFamily => ({
  ...structuredClone(family),
  addresses: family.addresses.join("\n"),
  dns: family.dns.join("\n"),
  domains: family.domains.join("\n"),
})
const intentFamily = (family: DraftFamily): NativeFamily => ({
  ...family,
  addresses: split(family.addresses),
  dns: split(family.dns),
  domains: split(family.domains),
})

/** Native ownership is a separate persistent owner, never a second managed profile. */
export function NativeProfileEditor({
  device,
  open,
  onChanged,
}: {
  device: string
  open: boolean
  onChanged: () => void
}) {
  const { can } = useAuth()
  const writable = can("system.admin") && can("destructive")
  const profile = usePoll(
    async (signal) =>
      readNativeProfile(
        await get<NativeProfile>(
          `/network/native/profiles/${encodeURIComponent(device)}`,
          undefined,
          signal,
        ),
        device,
      ),
    30_000,
    [device],
    { enabled: open && can("system.admin") },
  )
  const view = profile.data
  const [draft, setDraft] = useState<NativeDraft>()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()
  const { confirm, dialog } = useConfirm()
  const sameOwner =
    draft &&
    view &&
    draft.owner === view.owner &&
    draft.renderer === view.renderer &&
    draft.profile === view.profile
  const changed = draft && view && draft.generation !== view.generation
  const blocked =
    !open || !writable || !view?.editable || Boolean(profile.error) || changed || !sameOwner
  const latestReview = useRef<{ draft?: NativeDraft; blocked: boolean }>({ blocked: true })
  useLayoutEffect(() => {
    latestReview.current = { draft, blocked }
  }, [draft, blocked])

  const begin = () => {
    if (!view?.intent || !view.generation || profile.error) return
    setDraft({
      generation: view.generation,
      owner: view.owner,
      renderer: view.renderer,
      profile: view.profile,
      ipv4: draftFamily(view.intent.ipv4),
      ipv6: draftFamily(view.intent.ipv6),
    })
    setError(undefined)
  }
  const apply = () => {
    if (!draft || blocked) return
    void confirm({
      title: `Apply native settings to ${device}`,
      description: `The existing ${draft.owner} profile ${draft.profile} will restart this connection. Reconnect and confirm within 90 seconds to keep the settings; otherwise the independent host watchdog restores the prior profile.`,
      confirmLabel: "Apply temporary settings",
      action: async () => {
        setBusy(true)
        setError(undefined)
        try {
          // An open confirmation survives polls; its callback must use the current rendered review.
          if (latestReview.current.blocked || latestReview.current.draft !== draft) {
            throw new Error(
              "Native ownership or the latest read changed. Review the retained draft before applying.",
            )
          }
          await put(`/network/native/profiles/${encodeURIComponent(device)}`, {
            generation: draft.generation,
            intent: { ipv4: intentFamily(draft.ipv4), ipv6: intentFamily(draft.ipv6) },
          })
          setDraft(undefined)
          profile.refresh()
          onChanged()
        } catch (failure) {
          setError(failure instanceof Error ? failure.message : String(failure))
          profile.refresh()
          throw failure
        } finally {
          setBusy(false)
        }
      },
    })
  }
  const update = (family: "ipv4" | "ipv6", next: DraftFamily) => {
    if (draft) setDraft({ ...draft, [family]: next })
  }

  return (
    <Panel plain>
      <PanelHeader
        title="Native persistent profile"
        actions={view && <Status tone={view.editable ? "running" : "warning"} label={view.owner} />}
      />
      <PanelBody className="space-y-4">
        <NetworkReadWarning
          error={profile.error}
          refresh={profile.refresh}
          lastSuccess={profile.lastSuccess}
          reading="native profile"
        />
        {!view && !profile.error && (
          <p className="text-body text-muted-foreground">Reading native ownership…</p>
        )}
        {view && (
          <>
            <DetailList>
              <Detail label="Profile">{view.profile ?? "No supported persistent owner"}</Detail>
              <Detail label="Manager">
                {view.owner}
                {view.renderer && view.renderer !== view.owner && ` · ${view.renderer}`}
                {view.version && ` · ${view.version}`}
              </Detail>
              <Detail label="Configured">
                {view.configured.status} · {view.configured.reason ?? "Not verified"}
              </Detail>
              <Detail label="Runtime">
                {view.runtime.status} · {view.runtime.reason ?? "Not verified"}
              </Detail>
              <Detail label="Boot">
                {view.boot.status} · {view.boot.reason ?? "Not verified"}
              </Detail>
              {view.contract.bondMode && (
                <Detail label="Bond mode">{view.contract.bondMode}</Detail>
              )}
              {view.contract.vrfTable && (
                <Detail label="VRF table">{view.contract.vrfTable}</Detail>
              )}
              {view.contract.members.length > 0 && (
                <Detail label="Members">{view.contract.members.join(", ")}</Detail>
              )}
            </DetailList>
            {view.refusal && <p className="text-body text-warning">{view.refusal}</p>}
            {!draft && writable && (
              <Button
                size="sm"
                variant="outline"
                disabled={!view.editable || Boolean(profile.error)}
                onClick={begin}
              >
                Edit native profile
              </Button>
            )}
          </>
        )}
        {draft && (
          <div className="space-y-6">
            <NativeFamilyFields
              family="ipv4"
              value={draft.ipv4}
              renderer={draft.renderer}
              device={device}
              routeTable={view?.contract.vrfTable ?? 254}
              onChange={(value) => update("ipv4", value)}
            />
            <NativeFamilyFields
              family="ipv6"
              value={draft.ipv6}
              renderer={draft.renderer}
              device={device}
              routeTable={view?.contract.vrfTable ?? 254}
              onChange={(value) => update("ipv6", value)}
            />
            {changed && (
              <div className="space-y-2 text-body text-warning">
                <p>
                  The native baseline changed. The typed draft is retained; review the latest owner
                  and profile before applying.
                </p>
                <Button
                  size="sm"
                  variant="outline"
                  disabled={!sameOwner || !view?.editable || Boolean(profile.error)}
                  onClick={() => {
                    if (view?.generation) setDraft({ ...draft, generation: view.generation })
                  }}
                >
                  Use latest native baseline
                </Button>
              </div>
            )}
            {error && (
              <p role="alert" className="text-body text-destructive">
                {error}
              </p>
            )}
            <div className="flex flex-wrap gap-2">
              <Button size="sm" pending={busy} disabled={busy || blocked} onClick={apply}>
                Apply temporary native settings
              </Button>
              <Button
                size="sm"
                variant="outline"
                disabled={busy}
                onClick={() => setDraft(undefined)}
              >
                Discard native draft
              </Button>
            </div>
            <p className="text-hint text-muted-foreground">
              Native profile edits always require dashboard reconnection confirmation. Existing bond
              membership, bond mode and VRF table remain owned by the native profile.
            </p>
          </div>
        )}
      </PanelBody>
      {dialog}
    </Panel>
  )
}

function NativeFamilyFields({
  family,
  value,
  renderer,
  device,
  routeTable,
  onChange,
}: {
  family: "ipv4" | "ipv6"
  value: DraftFamily
  renderer?: string
  device: string
  routeTable: number
  onChange: (value: DraftFamily) => void
}) {
  const label = family === "ipv4" ? "IPv4" : "IPv6"
  const methods =
    family === "ipv4"
      ? [
          { value: "manual", label: "Static" },
          { value: "auto", label: "DHCP" },
          { value: "disabled", label: "Disabled" },
        ]
      : [
          { value: "manual", label: "Static" },
          { value: "auto", label: "Automatic (SLAAC and DHCP)" },
          { value: "dhcp", label: "DHCPv6" },
          ...(renderer === "NetworkManager" ? [] : [{ value: "slaac", label: "SLAAC" }]),
          { value: "disabled", label: "Disabled" },
        ]
  const id = `${device}-${family}`
  return (
    <FormSection title={label}>
      <Field label={`${label} addressing`} htmlFor={`${id}-method`}>
        <Select
          value={value.method}
          onValueChange={(method) => {
            if (methods.some((option) => option.value === method)) onChange({ ...value, method })
          }}
        >
          <SelectTrigger id={`${id}-method`}>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {methods.map((method) => (
              <SelectItem key={method.value} value={method.value}>
                {method.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </Field>
      <FieldRow>
        <Field
          label={`${label} static addresses`}
          htmlFor={`${id}-addresses`}
          hint="One address/prefix per line. Manual addressing requires at least one."
        >
          <Textarea
            id={`${id}-addresses`}
            className="font-mono"
            value={value.addresses}
            onChange={(event) => onChange({ ...value, addresses: event.target.value })}
          />
        </Field>
        <Field label={`${label} DNS servers`} htmlFor={`${id}-dns`} hint="One IP literal per line.">
          <Textarea
            id={`${id}-dns`}
            className="font-mono"
            value={value.dns}
            onChange={(event) => onChange({ ...value, dns: event.target.value })}
          />
        </Field>
      </FieldRow>
      <Field
        label={`${label} search and route domains`}
        htmlFor={`${id}-domains`}
        hint="One suffix per line. Prefix a routing-only suffix with ~."
      >
        <Textarea
          id={`${id}-domains`}
          value={value.domains}
          onChange={(event) => onChange({ ...value, domains: event.target.value })}
        />
      </Field>
      <OptionRow
        title={`Ignore automatic ${label} DNS`}
        checked={value.ignoreAutoDns}
        onCheckedChange={(checked) => onChange({ ...value, ignoreAutoDns: checked })}
      />
      <OptionRow
        title={`Ignore automatic ${label} routes`}
        checked={value.ignoreAutoRoutes}
        onCheckedChange={(checked) => onChange({ ...value, ignoreAutoRoutes: checked })}
      />
      <div className="space-y-3">
        {value.routes.map((route, index) => (
          <div className="space-y-2" key={index}>
            <FieldRow>
              {(["destination", "gateway", "metric", "table"] as const).map((key) => (
                <Field
                  key={key}
                  label={`${label} route ${index + 1} ${key}`}
                  htmlFor={`${id}-route-${index}-${key}`}
                >
                  <Input
                    id={`${id}-route-${index}-${key}`}
                    className="font-mono"
                    type={key === "metric" || key === "table" ? "number" : "text"}
                    value={route[key] ?? ""}
                    onChange={(event) =>
                      onChange({
                        ...value,
                        routes: value.routes.map((entry, row) =>
                          row === index
                            ? {
                                ...entry,
                                [key]:
                                  key === "metric" || key === "table"
                                    ? Number(event.target.value)
                                    : event.target.value,
                              }
                            : entry,
                        ),
                      })
                    }
                  />
                </Field>
              ))}
            </FieldRow>
            <Button
              size="xs"
              variant="outline"
              onClick={() =>
                onChange({ ...value, routes: value.routes.filter((_, row) => row !== index) })
              }
            >
              Remove {label} route {index + 1}
            </Button>
          </div>
        ))}
        <Button
          size="sm"
          variant="outline"
          disabled={value.routes.length >= 32}
          onClick={() =>
            onChange({
              ...value,
              routes: [
                ...value.routes,
                {
                  destination: family === "ipv4" ? "0.0.0.0/0" : "::/0",
                  gateway: "",
                  metric: renderer === "NetworkManager" ? -1 : 0,
                  table: routeTable,
                },
              ],
            })
          }
        >
          Add {label} route
        </Button>
      </div>
    </FormSection>
  )
}
