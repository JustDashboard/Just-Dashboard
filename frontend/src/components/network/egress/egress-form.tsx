"use client"

import { useState } from "react"
import { post, put } from "@/lib/api"
import { notify } from "@/lib/toast"
import {
  egressDraftProblems,
  egressRequestFromDraft,
  type EgressDraft,
  type EgressGroup,
  type EgressThresholds,
} from "@/lib/network-egress"
import type { NetworkLink } from "@/lib/types"
import { Modal } from "@/components/modal"
import { Field, FieldRow, FormNote } from "@/components/form"
import { Notice } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Switch } from "@/components/ui/switch"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Plus, Trash } from "@/components/icons"

const THRESHOLDS: { key: keyof EgressThresholds; label: string; hint: string }[] = [
  { key: "intervalSeconds", label: "Probe every (s)", hint: "2 to 300" },
  { key: "timeoutMillis", label: "Probe timeout (ms)", hint: "Shorter than the interval" },
  {
    key: "latencyMillis",
    label: "Latency threshold (ms)",
    hint: "Median round trip; below the timeout",
  },
  { key: "lossPercent", label: "Loss threshold (%)", hint: "Over the window" },
  { key: "window", label: "Loss window (samples)", hint: "1 to 60" },
  { key: "failAfter", label: "Down after (bad samples)", hint: "Consecutive" },
  { key: "recoverAfter", label: "Up after (good samples)", hint: "Consecutive" },
  { key: "holdSeconds", label: "Hold between switches (s)", hint: "Not applied to a failover" },
  { key: "stableSeconds", label: "Stable before failback (s)", hint: "Up this long first" },
]

/**
 * Create or edit an egress group. The draft survives a refused save: the
 * server's sentence is shown above the form and nothing typed is lost.
 */
export function EgressGroupForm({
  open,
  onOpenChange,
  initial,
  editing,
  links,
  onSaved,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  initial: EgressDraft
  editing?: EgressGroup
  links: NetworkLink[]
  onSaved: () => void
}) {
  const [draft, setDraft] = useState<EgressDraft>(initial)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()
  const [tried, setTried] = useState(false)
  const problems = egressDraftProblems(draft)
  const set = (patch: Partial<EgressDraft>) => setDraft((d) => ({ ...d, ...patch }))
  const setMember = (i: number, patch: Partial<EgressDraft["members"][number]>) =>
    setDraft((d) => ({
      ...d,
      members: d.members.map((m, j) => (j === i ? { ...m, ...patch } : m)),
    }))
  const setProbe = (i: number, patch: Partial<EgressDraft["probes"][number]>) =>
    setDraft((d) => ({ ...d, probes: d.probes.map((p, j) => (j === i ? { ...p, ...patch } : p)) }))

  const submit = async () => {
    setTried(true)
    if (problems.length || busy) return
    setBusy(true)
    setError(undefined)
    try {
      const body = egressRequestFromDraft(draft)
      if (editing) await put(`/network/egress/${editing.id}`, body)
      else await post("/network/egress", body)
      notify.success(editing ? `${draft.name} saved` : `${draft.name} created`, {
        description: editing
          ? "A changed configuration needs a new simulation before automation."
          : "Its members are measured from now; it carries no traffic until you enable it.",
      })
      onOpenChange(false)
      onSaved()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }
  const devices = links.filter((l) => l.role !== "container" && l.name !== "lo")
  return (
    <Modal
      open={open}
      onOpenChange={onOpenChange}
      title={editing ? `Edit ${editing.name}` : "New egress group"}
      description="Candidate next hops for selected traffic, measured through their own paths"
      size="xl"
      footer={
        <>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={busy}>
            Cancel
          </Button>
          <Button onClick={() => void submit()} disabled={busy} pending={busy}>
            {editing ? "Save" : "Create group"}
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-5">
        {error && (
          <div role="alert">
            <Notice tone="danger" title="Not saved">
              {error}
            </Notice>
          </div>
        )}
        {tried && problems.length > 0 && (
          <Notice tone="warning" title="Check these first">
            <ul className="list-disc space-y-1 pl-4">
              {problems.map((p) => (
                <li key={p}>{p}</li>
              ))}
            </ul>
          </Notice>
        )}
        <FieldRow>
          <Field label="Name" htmlFor="egress-name">
            <Input
              id="egress-name"
              value={draft.name}
              onChange={(e) => set({ name: e.target.value })}
            />
          </Field>
          <Field label="Family" htmlFor="egress-family">
            <Select
              value={draft.family}
              onValueChange={(v) => set({ family: v as EgressDraft["family"] })}
            >
              <SelectTrigger id="egress-family" className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="inet">IPv4</SelectItem>
                <SelectItem value="inet6">IPv6</SelectItem>
              </SelectContent>
            </Select>
          </Field>
        </FieldRow>

        <FieldRow>
          <Field label="Traffic it carries" htmlFor="egress-policy">
            <Select
              value={draft.policy.kind}
              onValueChange={(v) =>
                set({ policy: { ...draft.policy, kind: v as EgressDraft["policy"]["kind"] } })
              }
            >
              <SelectTrigger id="egress-policy" className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">
                  All of this server&rsquo;s default-route traffic
                </SelectItem>
                <SelectItem value="selector">By source or destination network</SelectItem>
                <SelectItem value="rule">By packet mark or incoming interface</SelectItem>
              </SelectContent>
            </Select>
          </Field>
          {draft.policy.kind === "selector" && (
            <>
              <Field label="From" htmlFor="egress-from">
                <Input
                  id="egress-from"
                  className="font-mono"
                  placeholder="10.8.0.0/24"
                  value={draft.policy.from}
                  onChange={(e) => set({ policy: { ...draft.policy, from: e.target.value } })}
                />
              </Field>
              <Field label="To" htmlFor="egress-to">
                <Input
                  id="egress-to"
                  className="font-mono"
                  value={draft.policy.to}
                  onChange={(e) => set({ policy: { ...draft.policy, to: e.target.value } })}
                />
              </Field>
            </>
          )}
          {draft.policy.kind === "rule" && (
            <>
              <Field label="Packet mark" htmlFor="egress-mark" hint="Outside 0x7f00">
                <Input
                  id="egress-mark"
                  className="font-mono"
                  placeholder="0x10000/0xff0000"
                  value={draft.policy.fwmark}
                  onChange={(e) => set({ policy: { ...draft.policy, fwmark: e.target.value } })}
                />
              </Field>
              <Field label="Arriving on" htmlFor="egress-iif">
                <Input
                  id="egress-iif"
                  className="font-mono"
                  list="egress-devices"
                  value={draft.policy.iif}
                  onChange={(e) => set({ policy: { ...draft.policy, iif: e.target.value } })}
                />
              </Field>
            </>
          )}
        </FieldRow>

        <fieldset className="flex flex-col gap-3">
          <legend className="mb-2 text-body font-medium">Members</legend>
          {draft.members.map((m, i) => (
            <div
              key={i}
              className="grid gap-3 border-t border-hairline pt-3 sm:grid-cols-[1fr_8rem_1fr_1fr_5rem_5rem_auto] sm:items-end"
            >
              <Field label="Name" htmlFor={`egress-m${i}-name`}>
                <Input
                  id={`egress-m${i}-name`}
                  value={m.name}
                  onChange={(e) => setMember(i, { name: e.target.value })}
                />
              </Field>
              <Field label="Kind" htmlFor={`egress-m${i}-kind`}>
                <Select
                  value={m.kind}
                  onValueChange={(v) => setMember(i, { kind: v as "gateway" | "tunnel" })}
                >
                  <SelectTrigger id={`egress-m${i}-kind`} className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="gateway">Gateway</SelectItem>
                    <SelectItem value="tunnel">Owned tunnel</SelectItem>
                  </SelectContent>
                </Select>
              </Field>
              <Field label="Gateway" htmlFor={`egress-m${i}-gw`}>
                <Input
                  id={`egress-m${i}-gw`}
                  className="font-mono"
                  disabled={m.kind === "tunnel"}
                  value={m.kind === "tunnel" ? "" : m.gateway}
                  onChange={(e) => setMember(i, { gateway: e.target.value })}
                />
              </Field>
              <Field label="Device" htmlFor={`egress-m${i}-dev`}>
                <Input
                  id={`egress-m${i}-dev`}
                  className="font-mono"
                  list="egress-devices"
                  value={m.device}
                  onChange={(e) => setMember(i, { device: e.target.value })}
                />
              </Field>
              <Field label="Tier" htmlFor={`egress-m${i}-tier`}>
                <Input
                  id={`egress-m${i}-tier`}
                  inputMode="numeric"
                  value={m.priority}
                  onChange={(e) => setMember(i, { priority: e.target.value })}
                />
              </Field>
              <Field label="Weight" htmlFor={`egress-m${i}-weight`}>
                <Input
                  id={`egress-m${i}-weight`}
                  inputMode="numeric"
                  value={m.weight}
                  onChange={(e) => setMember(i, { weight: e.target.value })}
                />
              </Field>
              <Button
                size="icon-sm"
                variant="ghost"
                aria-label={`Remove member ${m.name || i + 1}`}
                disabled={draft.members.length <= 2}
                onClick={() => set({ members: draft.members.filter((_, j) => j !== i) })}
              >
                <Trash aria-hidden />
              </Button>
              <div className="sm:col-span-7">
                <Field
                  label="Probe source"
                  htmlFor={`egress-m${i}-src`}
                  hint="Optional; the device's own address otherwise"
                >
                  <Input
                    id={`egress-m${i}-src`}
                    className="font-mono"
                    value={m.source}
                    onChange={(e) => setMember(i, { source: e.target.value })}
                  />
                </Field>
              </div>
            </div>
          ))}
          <Button
            size="xs"
            variant="outline"
            className="self-start"
            disabled={draft.members.length >= 8}
            onClick={() =>
              set({
                members: [
                  ...draft.members,
                  {
                    name: "",
                    kind: "gateway",
                    gateway: "",
                    device: "",
                    source: "",
                    priority: String(draft.members.length + 1),
                    weight: "1",
                  },
                ],
              })
            }
          >
            <Plus aria-hidden />
            Add member
          </Button>
          <FormNote>
            The lowest tier with a proven member carries traffic; members of one tier share it by
            weight. A second device is not necessarily an independent upstream.
          </FormNote>
        </fieldset>

        <fieldset className="flex flex-col gap-3">
          <legend className="mb-2 text-body font-medium">Probes</legend>
          {draft.probes.map((p, i) => (
            <div key={i} className="grid gap-3 sm:grid-cols-[8rem_1fr_6rem_1fr_auto] sm:items-end">
              <Field label="Kind" htmlFor={`egress-p${i}-kind`}>
                <Select
                  value={p.kind}
                  onValueChange={(v) => setProbe(i, { kind: v as "icmp" | "tcp" | "dns" })}
                >
                  <SelectTrigger id={`egress-p${i}-kind`} className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="icmp">ICMP echo</SelectItem>
                    <SelectItem value="tcp">TCP connect</SelectItem>
                    <SelectItem value="dns">DNS answer</SelectItem>
                  </SelectContent>
                </Select>
              </Field>
              <Field label="Target" htmlFor={`egress-p${i}-target`}>
                <Input
                  id={`egress-p${i}-target`}
                  className="font-mono"
                  placeholder={draft.family === "inet6" ? "2606:4700:4700::1111" : "1.1.1.1"}
                  value={p.target}
                  onChange={(e) => setProbe(i, { target: e.target.value })}
                />
              </Field>
              <Field label="Port" htmlFor={`egress-p${i}-port`}>
                <Input
                  id={`egress-p${i}-port`}
                  inputMode="numeric"
                  disabled={p.kind === "icmp"}
                  placeholder={p.kind === "dns" ? "53" : p.kind === "tcp" ? "443" : ""}
                  value={p.kind === "icmp" ? "" : p.port}
                  onChange={(e) => setProbe(i, { port: e.target.value })}
                />
              </Field>
              <Field label="Question" htmlFor={`egress-p${i}-name`}>
                <Input
                  id={`egress-p${i}-name`}
                  className="font-mono"
                  disabled={p.kind !== "dns"}
                  placeholder={p.kind === "dns" ? "the root" : ""}
                  value={p.kind === "dns" ? p.name : ""}
                  onChange={(e) => setProbe(i, { name: e.target.value })}
                />
              </Field>
              <Button
                size="icon-sm"
                variant="ghost"
                aria-label={`Remove probe ${i + 1}`}
                disabled={draft.probes.length <= 1}
                onClick={() => set({ probes: draft.probes.filter((_, j) => j !== i) })}
              >
                <Trash aria-hidden />
              </Button>
            </div>
          ))}
          <Button
            size="xs"
            variant="outline"
            className="self-start"
            disabled={draft.probes.length >= 4}
            onClick={() =>
              set({ probes: [...draft.probes, { kind: "icmp", target: "", port: "", name: "" }] })
            }
          >
            <Plus aria-hidden />
            Add probe
          </Button>
          <FormNote>
            Literal addresses only. Every member is measured against every target through its own
            mark, source address and device, whether or not it carries traffic.
          </FormNote>
        </fieldset>

        <fieldset className="grid gap-3 sm:grid-cols-3">
          <legend className="mb-2 text-body font-medium">Measurement and hysteresis</legend>
          {THRESHOLDS.map((t) => (
            <Field key={t.key} label={t.label} htmlFor={`egress-t-${t.key}`} hint={t.hint}>
              <Input
                id={`egress-t-${t.key}`}
                inputMode="numeric"
                value={draft.thresholds[t.key]}
                onChange={(e) =>
                  set({ thresholds: { ...draft.thresholds, [t.key]: e.target.value } })
                }
              />
            </Field>
          ))}
        </fieldset>

        <FieldRow>
          <Field label="Failback" htmlFor="egress-failback">
            <Select
              value={draft.failback}
              onValueChange={(v) => set({ failback: v as EgressDraft["failback"] })}
            >
              <SelectTrigger id="egress-failback" className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="automatic">Automatic, after a stable recovery</SelectItem>
                <SelectItem value="manual">By hand</SelectItem>
              </SelectContent>
            </Select>
          </Field>
          <Field label="Connections of a failed member" htmlFor="egress-connections">
            <Select
              value={draft.connections}
              onValueChange={(v) => set({ connections: v as EgressDraft["connections"] })}
            >
              <SelectTrigger id="egress-connections" className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="flush">Flush, so clients reconnect at once</SelectItem>
                <SelectItem value="keep">Keep until they recover or time out</SelectItem>
              </SelectContent>
            </Select>
          </Field>
        </FieldRow>
        <label className="flex items-center gap-3 text-body">
          <Switch
            checked={draft.sticky}
            onCheckedChange={(sticky) =>
              set({ sticky, connections: sticky ? "flush" : draft.connections })
            }
            aria-label="Sticky connections"
          />
          Keep each connection on the member it started on while that member is up
        </label>
        <Field
          label="Protected operator addresses"
          htmlFor="egress-protected"
          hint="Kept on the main table while the group carries traffic; up to four, comma separated. Enabling adds yours when the group would carry it."
        >
          <Input
            id="egress-protected"
            className="font-mono"
            value={draft.protected}
            onChange={(e) => set({ protected: e.target.value })}
          />
        </Field>
        <datalist id="egress-devices">
          {devices.map((l) => (
            <option key={l.name} value={l.name} />
          ))}
        </datalist>
      </div>
    </Modal>
  )
}
