"use client"

import { useEffect, useRef, useState } from "react"
import { useAuth } from "@/hooks/use-auth"
import { LockClosed } from "@/components/icons"
import { errorMessage, get, post, put } from "@/lib/api"
import { notify } from "@/lib/toast"
import type { NetworkBridgeView, NetworkLink, NetworkMasterPreview } from "@/lib/types"
import { ConfirmDialog, type ConfirmRequest } from "@/components/confirm-dialog"
import { SaveBarProvider } from "@/components/deploy/settings/save-bar"
import {
  SettingForm,
  SettingSection,
  settingStatus,
} from "@/components/deploy/settings/setting-card"
import { Field, FieldRow, FormNote, FormSections } from "@/components/form"
import { Detail, DetailList } from "@/components/page"
import { Notice } from "@/components/state"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { OWNER_LABEL, kindLabel } from "@/components/network/marks"
import { DeviceDriver, useDeviceDetail } from "./device-detail"
import { useBridge } from "./device-ports"
import { describeVlans, parseRemotes, sameVlans, vlanFields, vlanPolicy } from "./device-reading"
import { NativeProfileEditor } from "./native-profile"

const NONE = "__none"

type Ask = (
  request: Omit<ConfirmRequest, "action"> & { action: () => Promise<void> },
) => Promise<boolean>

/**
 * A confirmation a save can wait on: resolves true once the confirmed action
 * went through, false when the dialog was closed instead. The page's one Save
 * writes each changed section in turn, and a section whose change takes
 * something away asks first; the bar has to know whether it was written.
 */
function useAsk() {
  const [request, setRequest] = useState<ConfirmRequest | null>(null)
  const settle = useRef<((written: boolean) => void) | undefined>(undefined)
  const finish = (written: boolean) => {
    settle.current?.(written)
    settle.current = undefined
  }
  const ask: Ask = (asked) =>
    new Promise<boolean>((resolve) => {
      settle.current = resolve
      setRequest({
        ...asked,
        action: async () => {
          await asked.action()
          finish(true)
          return "reported"
        },
      })
    })
  const dialog = (
    <ConfirmDialog
      request={request}
      onOpenChange={(open) => {
        if (open) return
        setRequest(null)
        finish(false)
      }}
    />
  )
  return { ask, dialog }
}

function isMulticast(address: string) {
  const first = Number(address.split(".")[0])
  return address.includes(":")
    ? address.toLowerCase().startsWith("ff")
    : first >= 224 && first <= 239
}

/**
 * What a device is and what the dashboard may change about it, as one column
 * of sections with one Save: its MTU, the bridge it is a port of, the VLANs
 * its port carries and a VXLAN's further ends. Each is its own write, made
 * in the order they stand; one that takes something away is confirmed first,
 * and a bridge move is read from the server before it is offered, with what
 * it would leave behind. A device the guard holds, and an account that may
 * not change it, read the same facts with no fields. The native profile is a
 * temporary apply the operator has to confirm from the far side, so it keeps
 * its own commands.
 */
export function DeviceSettings({
  link,
  links,
  onChanged,
}: {
  link: NetworkLink
  links: NetworkLink[]
  onChanged: () => void
}) {
  const { can } = useAuth()
  const admin = can("system.admin")
  const canEdit = admin && !link.guard
  const { ask, dialog } = useAsk()
  const detail = useDeviceDetail(link.name)
  const isBridge = link.kind === "bridge"
  const switchName = isBridge ? link.name : link.managed ? link.master : undefined
  const bridge = useBridge(switchName, !!switchName)
  const view = bridge.data
  const refreshBridge = bridge.refresh

  return (
    <SaveBarProvider>
      <div className="mx-auto w-full max-w-3xl min-w-0 pb-24">
        {admin && link.guard && (
          <FormNote className="mb-6 flex items-start gap-1.5">
            <LockClosed aria-hidden className="mt-0.5 size-3 shrink-0" />
            <span>{link.guard}</span>
          </FormNote>
        )}
        <FormSections>
          <DeviceForm link={link} canEdit={canEdit} onChanged={onChanged} />
          {canEdit && !isBridge && link.role !== "loopback" && (
            <BridgeForm link={link} links={links} ask={ask} onChanged={onChanged} />
          )}
          {view?.managed && view.vlanFiltering && (
            <VlanForm
              // A port moved to another bridge starts from that bridge's policy.
              key={view.name}
              device={link.name}
              bridge={view}
              canEdit={admin}
              ask={ask}
              onChanged={() => {
                refreshBridge()
                onChanged()
              }}
            />
          )}
          {link.managed && link.kind === "vxlan" && link.remote && !isMulticast(link.remote) && (
            <FloodForm link={link} canEdit={admin} ask={ask} onChanged={onChanged} />
          )}
          {admin && !link.managed && (
            <NativeProfileEditor key={link.name} device={link.name} onChanged={onChanged} />
          )}
          {link.role !== "loopback" && (
            <div className="py-8 last:pb-0">
              <DeviceDriver detail={detail} />
            </div>
          )}
        </FormSections>
      </div>
      {dialog}
    </SaveBarProvider>
  )
}

/** What the device is, and its MTU as the one thing here that can be typed. */
function DeviceForm({
  link,
  canEdit,
  onChanged,
}: {
  link: NetworkLink
  canEdit: boolean
  onChanged: () => void
}) {
  const [draft, setDraft] = useState<string>()
  const [saving, setSaving] = useState(false)
  const text = draft ?? String(link.mtu)
  const value = Number(text.trim())
  const dirty = draft !== undefined && text.trim() !== String(link.mtu)
  const invalid = dirty && !(Number.isInteger(value) && value > 0)
  const save = async () => {
    setSaving(true)
    try {
      await post(`/network/links/${encodeURIComponent(link.name)}/mtu`, { mtu: value })
      notify.success(`${link.name} MTU is ${value}`)
      setDraft(undefined)
      onChanged()
      return true
    } catch (err) {
      notify.error(`${link.name}: not changed`, err)
      return false
    } finally {
      setSaving(false)
    }
  }
  return (
    <SettingForm
      name="MTU"
      canEdit={canEdit}
      dirty={dirty}
      changes={dirty ? 1 : 0}
      saving={saving}
      invalid={invalid}
      applies="immediately"
      onSave={save}
      onDiscard={() => setDraft(undefined)}
    >
      <SettingSection title="Device" status={settingStatus({ dirty })}>
        <DetailList>
          <Detail label="Kind">{kindLabel(link)}</Detail>
          <Detail label="Kept by">{OWNER_LABEL[link.owner]}</Detail>
          {link.mac && (
            <Detail label="MAC">
              <span className="font-mono">{link.mac}</span>
            </Detail>
          )}
          {!canEdit && (
            <Detail label="MTU">
              <span className="numeric font-mono">{link.mtu}</span>
            </Detail>
          )}
          {link.qdisc && <Detail label="Queue">{link.qdisc}</Detail>}
          {link.speedMbps ? <Detail label="Speed">{link.speedMbps} Mb/s</Detail> : null}
          {link.parent && (
            <Detail label="Rides on">
              <span className="font-mono">{link.parent}</span>
            </Detail>
          )}
          {link.vlanId ? <Detail label="VLAN">{link.vlanId}</Detail> : null}
          {link.vni ? <Detail label="VNI">{link.vni}</Detail> : null}
          {(link.local || link.remote) && (
            <Detail label="Ends">
              <span className="font-mono">
                {link.local ?? "any"} → {link.remote ?? "any"}
                {link.port ? `:${link.port}` : ""}
              </span>
            </Detail>
          )}
          {link.master && (
            <Detail label="Port of">
              <span className="font-mono">{link.master}</span>
            </Detail>
          )}
          {link.container && <Detail label="Container">{link.container}</Detail>}
          {link.dockerNetwork && <Detail label="Docker network">{link.dockerNetwork}</Detail>}
          {link.xdp && (
            <Detail label="XDP program">
              <span className="font-mono">{link.xdp}</span>
            </Detail>
          )}
        </DetailList>
        {canEdit && (
          <Field
            label="MTU"
            htmlFor={`${link.name}-mtu`}
            error={invalid ? "A whole number of bytes." : undefined}
            className="max-w-48"
          >
            <Input
              id={`${link.name}-mtu`}
              inputMode="numeric"
              value={text}
              onChange={(event) => setDraft(event.target.value)}
              aria-invalid={invalid}
              className="numeric font-mono"
            />
          </Field>
        )}
      </SettingSection>
    </SettingForm>
  )
}

/**
 * Which bridge the device is a port of. The move is read from the server
 * the moment another bridge is chosen: what it would leave behind is listed
 * under the choice, and a move the guards refuse holds the Save.
 */
function BridgeForm({
  link,
  links,
  ask,
  onChanged,
}: {
  link: NetworkLink
  links: NetworkLink[]
  ask: Ask
  onChanged: () => void
}) {
  const current = link.master ?? NONE
  const [draft, setDraft] = useState<string>()
  const [saving, setSaving] = useState(false)
  const [preview, setPreview] = useState<{
    key: string
    value?: NetworkMasterPreview
    error?: string
  }>()
  const chosen = draft ?? current
  const dirty = chosen !== current
  const master = chosen === NONE ? "" : chosen
  const previewKey = dirty ? `${link.name}>${chosen}` : undefined
  const linkName = link.name
  useEffect(() => {
    if (!previewKey) return
    const abort = new AbortController()
    get<NetworkMasterPreview>(
      `/network/links/${encodeURIComponent(linkName)}/master/preview`,
      { master },
      abort.signal,
    )
      .then((value) => setPreview({ key: previewKey, value }))
      .catch((err) => {
        if (!abort.signal.aborted) setPreview({ key: previewKey, error: errorMessage(err) })
      })
    return () => abort.abort()
  }, [linkName, master, previewKey])
  const shown = preview?.key === previewKey ? preview : undefined
  const refused = shown?.value?.allowed === false
  const bridges = links.filter(
    (candidate) =>
      candidate.kind === "bridge" && candidate.owner !== "docker" && candidate.name !== link.name,
  )
  const save = async () => {
    setSaving(true)
    const effects = shown?.value?.effects ?? []
    const written = await ask({
      title: master ? `Join ${link.name} to ${master}` : `Remove ${link.name} from its bridge`,
      confirmLabel: "Apply",
      description: (
        <div className="flex flex-col gap-2">
          <p>
            This changes which network receives the device&rsquo;s Ethernet frames. The server
            refuses a change that would disturb its uplink, addresses or your connection.
          </p>
          {effects.length > 0 && (
            <ul className="list-disc pl-5">
              {effects.map((e) => (
                <li key={e.detail}>{e.detail}</li>
              ))}
            </ul>
          )}
        </div>
      ),
      action: async () => {
        await post(`/network/links/${encodeURIComponent(link.name)}/master`, { master })
        notify.success(
          master ? `${link.name} joined ${master}` : `${link.name} is no longer a bridge port`,
        )
        setDraft(undefined)
        onChanged()
      },
    })
    setSaving(false)
    return written
  }
  return (
    <SettingForm
      name="Bridge"
      canEdit
      dirty={dirty}
      changes={dirty ? 1 : 0}
      saving={saving}
      invalid={refused}
      applies="immediately"
      onSave={save}
      onDiscard={() => setDraft(undefined)}
    >
      <SettingSection
        title="Bridge membership"
        state={
          link.master ? (
            <>
              A port of <span className="font-mono">{link.master}</span>
            </>
          ) : (
            "Not a bridge port"
          )
        }
        status={settingStatus({ dirty, refused })}
      >
        <Field label="Bridge" htmlFor={`${link.name}-bridge`} className="max-w-sm">
          <Select value={chosen} onValueChange={setDraft}>
            <SelectTrigger id={`${link.name}-bridge`} aria-label="Bridge membership">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value={NONE}>No bridge</SelectItem>
              {bridges.map((candidate) => (
                <SelectItem key={candidate.name} value={candidate.name}>
                  {candidate.name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>
        {shown && dirty && (
          <div className="flex flex-col gap-2" aria-label="What the move would do">
            {shown.error ? (
              <Notice tone="warning" title="The move could not be previewed">
                {shown.error}
              </Notice>
            ) : refused ? (
              <Notice tone="warning" title="Refused">
                {shown.value?.refusal}
              </Notice>
            ) : null}
            {shown.value && shown.value.effects.length > 0 && (
              <ul className="flex list-disc flex-col gap-1 pl-5 text-hint text-muted-foreground">
                {shown.value.effects.map((e) => (
                  <li key={e.detail}>{e.detail}</li>
                ))}
              </ul>
            )}
          </div>
        )}
      </SettingSection>
    </SettingForm>
  )
}

/**
 * The tagged and untagged policy of a port of a VLAN-filtering bridge the
 * dashboard made, in the words a switch port is configured in: one native
 * VLAN, untagged and the VLAN untagged frames arriving belong to, and the
 * VLANs carried tagged beside it. Leaving both empty returns the port to the
 * kernel's default, VLAN 1 untagged. Taking a VLAN away is confirmed first.
 */
function VlanForm({
  device,
  bridge,
  canEdit,
  ask,
  onChanged,
}: {
  device: string
  bridge: NetworkBridgeView
  canEdit: boolean
  ask: Ask
  onChanged: () => void
}) {
  const port = bridge.ports.find((p) => p.name === device)
  const current = port?.desired ?? [{ vid: bridge.defaultPvid || 1, pvid: true, untagged: true }]
  const initial = vlanFields(current)
  const [draft, setDraft] = useState<{ native: string; tagged: string }>()
  const [saving, setSaving] = useState(false)
  const native = draft?.native ?? initial.native
  const tagged = draft?.tagged ?? initial.tagged
  const policy = vlanPolicy(native, tagged)
  const target = policy.vlans.length ? policy.vlans : [{ vid: 1, pvid: true, untagged: true }]
  const removed = current.filter((c) => !target.some((t) => t.vid === c.vid)).map((c) => c.vid)
  const touched = draft !== undefined
  const dirty = touched && (!!policy.error || !sameVlans(target, current))
  const write = async () => {
    await put(`/network/links/${encodeURIComponent(device)}/vlans`, { vlans: policy.vlans })
    notify.success(`${device} carries ${describeVlans(target)}`)
    setDraft(undefined)
    onChanged()
  }
  const save = async () => {
    setSaving(true)
    try {
      if (removed.length === 0) {
        await write()
        return true
      }
      return await ask({
        title: `Take VLAN ${removed.join(", ")} off ${device}`,
        confirmLabel: "Apply",
        description: (
          <p>
            Frames of VLAN {removed.join(", ")} stop crossing{" "}
            <span className="font-mono">{device}</span>. The server refuses the change if it would
            move your connection, and puts it back if the path moves after it.
          </p>
        ),
        action: write,
      })
    } catch (err) {
      notify.error(`${device}: not changed`, err)
      return false
    } finally {
      setSaving(false)
    }
  }
  const set = (next: Partial<{ native: string; tagged: string }>) =>
    setDraft({ native, tagged, ...next })
  return (
    <SettingForm
      name="VLANs"
      canEdit={canEdit}
      dirty={dirty}
      changes={dirty ? 1 : 0}
      saving={saving}
      invalid={!!policy.error}
      applies="immediately"
      onSave={save}
      onDiscard={() => setDraft(undefined)}
    >
      <SettingSection
        title={port?.self ? `${device}'s own VLANs` : `VLANs on ${bridge.name}`}
        state={<>Now: {port ? describeVlans(port.vlans) : "not read"}</>}
        status={settingStatus({ dirty })}
      >
        {canEdit && (
          <FieldRow>
            <Field label="Native VLAN" htmlFor={`${device}-native`} info="Untagged on this port.">
              <Input
                id={`${device}-native`}
                inputMode="numeric"
                value={native}
                placeholder="1"
                onChange={(event) => set({ native: event.target.value })}
                className="numeric font-mono"
              />
            </Field>
            <Field
              label="Tagged VLANs"
              htmlFor={`${device}-tagged`}
              info="Ids or ranges, separated by commas."
              error={policy.error}
            >
              <Input
                id={`${device}-tagged`}
                value={tagged}
                placeholder="20, 30-32"
                onChange={(event) => set({ tagged: event.target.value })}
                aria-invalid={!!policy.error}
                className="font-mono"
              />
            </Field>
          </FieldRow>
        )}
      </SettingSection>
    </SettingForm>
  )
}

/**
 * A unicast VXLAN's other ends beyond its own remote: every broadcast and
 * unknown frame is copied to each of them (head-end replication), so a
 * segment of three servers needs both others here. Saved in the spec and the
 * boot unit; taking one away is confirmed first.
 */
function FloodForm({
  link,
  canEdit,
  ask,
  onChanged,
}: {
  link: NetworkLink
  canEdit: boolean
  ask: Ask
  onChanged: () => void
}) {
  const remotes = link.remotes ?? []
  const [draft, setDraft] = useState<string>()
  const [saving, setSaving] = useState(false)
  const text = draft ?? remotes.join(", ")
  const parsed = parseRemotes(text, link.remote)
  const removed = remotes.filter((r) => !parsed.remotes.includes(r))
  const dirty =
    draft !== undefined &&
    (!!parsed.error || removed.length > 0 || parsed.remotes.some((r) => !remotes.includes(r)))
  const write = async () => {
    await put(`/network/links/${encodeURIComponent(link.name)}/remotes`, {
      remotes: parsed.remotes,
    })
    notify.success(
      `${link.name} floods to ${parsed.remotes.length + 1} end${parsed.remotes.length ? "s" : ""}`,
    )
    setDraft(undefined)
    onChanged()
  }
  const save = async () => {
    setSaving(true)
    try {
      if (removed.length === 0) {
        await write()
        return true
      }
      return await ask({
        title: `Stop flooding to ${removed.join(", ")}`,
        confirmLabel: "Remove",
        description: (
          <p>
            Broadcast and unknown frames on <span className="font-mono">{link.name}</span> stop
            reaching {removed.join(", ")}; machines behind it lose the segment until it is added
            back.
          </p>
        ),
        action: write,
      })
    } catch (err) {
      notify.error(`${link.name}: not changed`, err)
      return false
    } finally {
      setSaving(false)
    }
  }
  return (
    <SettingForm
      name="Flood ends"
      canEdit={canEdit}
      dirty={dirty}
      changes={dirty ? 1 : 0}
      saving={saving}
      invalid={!!parsed.error}
      applies="immediately"
      onSave={save}
      onDiscard={() => setDraft(undefined)}
    >
      <SettingSection
        title="Flood ends"
        state={
          <>
            Floods to <span className="font-mono">{link.remote}</span>
            {remotes.length > 0 && (
              <>
                {" "}
                and <span className="font-mono">{remotes.join(", ")}</span>
              </>
            )}
          </>
        }
        status={settingStatus({ dirty })}
      >
        {canEdit && (
          <Field
            label="Further ends"
            htmlFor={`${link.name}-remotes`}
            error={parsed.error}
            info={`Besides ${link.remote}: every other server on this segment, separated by commas.`}
          >
            <Input
              id={`${link.name}-remotes`}
              value={text}
              placeholder="198.51.100.8, 198.51.100.9"
              onChange={(event) => setDraft(event.target.value)}
              aria-invalid={Boolean(parsed.error)}
              className="font-mono"
            />
          </Field>
        )}
      </SettingSection>
    </SettingForm>
  )
}
