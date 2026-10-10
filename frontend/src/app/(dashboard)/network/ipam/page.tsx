"use client"

import Link from "next/link"
import { useState } from "react"
import { Page, PageContext } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { Disclosure, Field, FieldRow } from "@/components/form"
import { ConfirmDialog } from "@/components/confirm-dialog"
import type { ConfirmRequest } from "@/components/confirm-dialog"
import { NetworkReadWarning } from "@/components/network/read-warning"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Checkbox } from "@/components/ui/checkbox"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { del, errorMessage, get, post } from "@/lib/api"
import {
  ipamCounts,
  readIPAMView,
  ownerLabel,
  previewReading,
  reservationLink,
  reservationState,
} from "@/lib/network-ipam"
import type {
  IPAMOwner,
  IPAMPool,
  IPAMPreview,
  IPAMReservation,
  IPAMView,
} from "@/lib/network-ipam"

// A report of planned and observed address space, with editing controls. The
// planner never presents an unmeasured native range as reserved or verified.
export default function SharedAddressPoolsPage() {
  const { can } = useAuth()
  const admin = can("system.admin")
  const view = usePoll<IPAMView>(
    async (signal) => readIPAMView(await get("/network/ipam/", undefined, signal)),
    30000,
    [admin],
    { enabled: admin },
  )
  const [name, setName] = useState("")
  const [prefix, setPrefix] = useState("")
  const [bits, setBits] = useState("24")
  const [poolId, setPoolId] = useState("")
  const [owner, setOwner] = useState<IPAMOwner>("docker_network")
  const [resource, setResource] = useState("")
  const [exact, setExact] = useState("")
  const [acknowledge, setAcknowledge] = useState(false)
  const [candidate, setCandidate] = useState("")
  const [preview, setPreview] = useState<IPAMPreview>()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()
  const [confirm, setConfirm] = useState<ConfirmRequest | null>(null)
  const counts = ipamCounts(view.data)
  const pools = view.data?.pools.filter((pool) => !pool.retiredAt) ?? []
  const pool = pools.find((row) => row.id === poolId)
  const reservations = view.data?.reservations ?? []
  const unknown = view.data?.inventory.coverage.filter((row) => row.state !== "observed") ?? []
  const reading = preview ? previewReading(preview) : undefined
  const perform = async (action: () => Promise<void>) => {
    if (busy || !admin) return
    setBusy(true)
    setError(undefined)
    try {
      await action()
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }
  const release = (row: IPAMReservation) =>
    setConfirm({
      title: "Release planning reservation",
      description: `Release ${row.prefix} for ${row.resource} from the shared planner. This does not remove or reconfigure its native resource. Check its native owner before reusing the range.`,
      confirmLabel: "Release plan",
      action: async () => {
        await del(`/network/ipam/reservations/${row.id}`)
        view.refresh()
      },
    })
  return (
    <Page className="animate-rise">
      <PageContext eyebrow="Network" title="Shared address pools" />
      <StatGrid columns={4}>
        <StatTile label="Active pools" value={counts.pools} hint="IPv4 and IPv6 planning space" />
        <StatTile
          label="Held reservations"
          value={counts.reservations}
          hint="Plans and unresolved handoffs"
        />
        <StatTile
          label="Native ranges observed"
          value={counts.observed}
          hint="Current supported owner snapshots"
        />
        <StatTile
          label="Coverage gaps"
          value={counts.gaps}
          hint="Unreadable, foreign or provider scope"
          tone={counts.gaps ? "warning" : "default"}
        />
      </StatGrid>
      <Panel plain>
        <PanelHeader title="Plan against known owners" />
        <PanelBody className="space-y-5">
          <p className="max-w-3xl text-body text-muted-foreground">
            Allocate non-overlapping planning prefixes from shared pools. Docker, host interfaces
            and routes, WireGuard, Tailscale and named namespaces contribute readable evidence.
            Reservations hold planning space; their existing native owners still validate and apply
            configuration.
          </p>
          {!admin && (
            <p role="status" className="text-body text-muted-foreground">
              Shared pools and private native-owner inventories require system administrator access.
              The local subnet calculator remains available to read accounts.
            </p>
          )}
          {error && (
            <p role="alert" className="text-body text-destructive">
              {error}
            </p>
          )}
          <NetworkReadWarning
            error={view.error}
            refresh={view.refresh}
            lastSuccess={view.lastSuccess}
            reading="IPAM evidence"
          />
          <form
            className="max-w-3xl space-y-3"
            onSubmit={(event) => {
              event.preventDefault()
              if (candidate.trim())
                void perform(async () =>
                  setPreview(
                    await post<IPAMPreview>("/network/ipam/preview", { prefix: candidate.trim() }),
                  ),
                )
            }}
          >
            <Field
              label="Prefix to preview"
              htmlFor="ipam-preview-prefix"
              hint="A canonical network prefix in either family."
            >
              <Input
                id="ipam-preview-prefix"
                value={candidate}
                onChange={(event) => setCandidate(event.target.value)}
                className="font-mono"
                disabled={!admin || busy}
                placeholder="10.244.0.0/24 or fd48:abcd::/64"
                spellCheck={false}
              />
            </Field>
            <Button variant="outline" type="submit" disabled={!admin || busy || !candidate.trim()}>
              Preview known overlap
            </Button>
          </form>
          {preview && reading && (
            <div className="space-y-3 text-body" data-testid="ipam-preview">
              <p className="font-medium">
                {reading.label}: <span className="font-mono">{preview.prefix}</span>
              </p>
              <p className="text-muted-foreground">{reading.detail}</p>
              {preview.conflicts.map((conflict, i) => (
                <p key={i} className="font-mono text-hint break-all">
                  {conflict.prefix} · {conflict.owner} · {conflict.resource} · {conflict.basis}
                </p>
              ))}
              <p className="text-hint text-muted-foreground">
                Checked {new Date(preview.checkedAt).toLocaleString()}; native creation rechecks
                current evidence.
              </p>
            </div>
          )}
          <Disclosure
            summary="Coverage and ownership limits"
            facts={`${counts.gaps} gaps · native resources remain with their managers`}
          >
            <div className="space-y-4 text-body">
              {(view.data?.inventory.coverage ?? []).map((row, i) => (
                <div key={i}>
                  <p className="font-medium">
                    {row.source} · {row.state}
                  </p>
                  <p className="text-muted-foreground">{row.detail}</p>
                </div>
              ))}
              {(view.data?.limitations ?? []).map((detail, i) => (
                <p key={i} className="text-hint text-muted-foreground">
                  {detail}
                </p>
              ))}
            </div>
          </Disclosure>
        </PanelBody>
      </Panel>
      <Panel plain>
        <PanelHeader title="Pool utilization" />
        <PanelBody className="space-y-5">
          {/* The exact large-family counts need a table that scrolls without rounding to floating point. */}
          <div className="overflow-x-auto rounded-md border border-hairline">
            <table className="w-full text-left text-body">
              <thead className="bg-surface-header text-hint text-muted-foreground">
                <tr>
                  {[
                    "Pool",
                    "Allocation blocks",
                    "Planned",
                    "Observed",
                    "Unavailable union",
                    "Candidate blocks",
                    "",
                  ].map((label, i) => (
                    <th key={i} className="px-4 py-3 font-medium">
                      {label}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody className="divide-y divide-hairline">
                {(view.data?.pools ?? []).map((row) => {
                  const use = view.data?.utilization.find((item) => item.poolId === row.id)
                  return (
                    <tr key={row.id}>
                      <td className="px-4 py-3">
                        <p className="font-medium">
                          {row.name} {row.retiredAt && "· Retired"}
                        </p>
                        <p className="font-mono text-hint text-muted-foreground">
                          {row.prefix} → /{row.allocationBits}
                        </p>
                      </td>
                      {[
                        use?.totalBlocks,
                        use?.reservedBlocks,
                        use?.observedBlocks,
                        use?.unavailableBlocks,
                        use?.candidateBlocks,
                      ].map((value, i) => (
                        <td key={i} className="px-4 py-3 font-mono text-hint">
                          {value ?? "Unknown"}
                        </td>
                      ))}
                      <td className="px-4 py-3">
                        {!row.retiredAt && (
                          <Button
                            variant="outline"
                            size="sm"
                            disabled={busy}
                            onClick={() =>
                              setConfirm({
                                title: "Retire planning pool",
                                description: `Stop new planning allocations from ${row.name}. Active reservations must be released first. Native configuration remains with its existing owner.`,
                                confirmLabel: "Retire pool",
                                action: async () => {
                                  await del(`/network/ipam/pools/${row.id}`)
                                  view.refresh()
                                },
                              })
                            }
                          >
                            Retire
                          </Button>
                        )}
                      </td>
                    </tr>
                  )
                })}
                {!view.data?.pools.length && (
                  <tr>
                    <td colSpan={7} className="px-4 py-6 text-muted-foreground">
                      No shared pools. Create a planning pool to allocate exact prefixes.
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>
          <p className="text-hint text-muted-foreground">
            Counts are exact. Observed and planned overlap is counted once in the unavailable union.
            Candidate blocks are candidates within readable evidence; coverage gaps can hide
            additional allocations.
          </p>
          <Disclosure summary="Create a shared pool" facts="Planning metadata · no native write">
            <form
              className="max-w-3xl space-y-4"
              onSubmit={(event) => {
                event.preventDefault()
                void perform(async () => {
                  const created = await post<IPAMPool>("/network/ipam/pools", {
                    name: name.trim(),
                    prefix: prefix.trim(),
                    allocationBits: Number(bits),
                  })
                  setPoolId(created.id)
                  view.refresh()
                })
              }}
            >
              <FieldRow>
                <Field label="Pool name" htmlFor="ipam-pool-name">
                  <Input
                    id="ipam-pool-name"
                    value={name}
                    onChange={(event) => setName(event.target.value)}
                    disabled={!admin || busy}
                    maxLength={80}
                  />
                </Field>
                <Field label="Pool network" htmlFor="ipam-pool-prefix">
                  <Input
                    id="ipam-pool-prefix"
                    value={prefix}
                    onChange={(event) => setPrefix(event.target.value)}
                    className="font-mono"
                    disabled={!admin || busy}
                    spellCheck={false}
                  />
                </Field>
              </FieldRow>
              <Field
                label="Allocation prefix length"
                htmlFor="ipam-allocation-bits"
                hint="For example, /24 blocks from an IPv4 /16 or /64 blocks from an IPv6 /48."
              >
                <Input
                  id="ipam-allocation-bits"
                  value={bits}
                  onChange={(event) => setBits(event.target.value)}
                  inputMode="numeric"
                  disabled={!admin || busy}
                />
              </Field>
              <Button
                type="submit"
                disabled={
                  !admin || busy || !name.trim() || !prefix.trim() || !/^\d{1,3}$/.test(bits)
                }
              >
                Create planning pool
              </Button>
            </form>
          </Disclosure>
        </PanelBody>
      </Panel>
      <Panel plain>
        <PanelHeader title="Reservations and owner handoffs" />
        <PanelBody className="space-y-5">
          {reservations.map((row) => (
            <div
              key={row.id}
              className="flex flex-wrap items-start justify-between gap-4 border-b border-hairline pb-4 text-body"
            >
              <div>
                <p className="font-mono font-medium">
                  {row.prefix} · {row.resource}
                </p>
                <p>
                  {ownerLabel[row.owner]} · {reservationState[row.state]}
                </p>
                <p className="text-hint text-muted-foreground">
                  {row.detail || "No native configuration has been applied by this reservation."}
                </p>
                {row.nativeId && (
                  <p className="font-mono text-hint break-all text-muted-foreground">
                    Observed owner ID: {row.nativeId}
                  </p>
                )}
              </div>
              <div className="flex flex-wrap gap-2">
                {reservationLink(row) && (
                  <Button asChild variant="outline" size="sm">
                    <Link href={reservationLink(row)!}>
                      Open {row.owner === "docker_network" ? "Docker" : "WireGuard"} creation
                    </Link>
                  </Button>
                )}
                {row.state !== "released" && (
                  <Button
                    variant="outline"
                    size="sm"
                    disabled={busy || row.state === "handing_off"}
                    onClick={() => release(row)}
                  >
                    Release plan
                  </Button>
                )}
              </div>
            </div>
          ))}
          {!reservations.length && (
            <p className="text-body text-muted-foreground">No planning reservations.</p>
          )}
          <Disclosure
            summary="Reserve an allocation"
            facts="First candidate or exact block · intended owner"
          >
            <form
              className="max-w-3xl space-y-4"
              onSubmit={(event) => {
                event.preventDefault()
                if (!pool) return
                void perform(async () => {
                  await post<IPAMReservation>("/network/ipam/reservations", {
                    poolId,
                    prefix: exact.trim() || undefined,
                    owner,
                    resource: resource.trim(),
                    acknowledgeUnknown: acknowledge,
                  })
                  view.refresh()
                })
              }}
            >
              <FieldRow>
                <Field label="Shared pool" htmlFor="ipam-reserve-pool">
                  <Picker
                    id="ipam-reserve-pool"
                    value={poolId}
                    onChange={setPoolId}
                    options={pools.map((row) => ({
                      value: row.id,
                      label: `${row.name} · ${row.prefix}`,
                    }))}
                    disabled={!admin || busy}
                  />
                </Field>
                <Field label="Intended owner" htmlFor="ipam-reserve-owner">
                  <Picker
                    id="ipam-reserve-owner"
                    value={owner}
                    onChange={(value) => setOwner(value as IPAMOwner)}
                    options={Object.entries(ownerLabel).map(([value, label]) => ({ value, label }))}
                    disabled={!admin || busy}
                  />
                </Field>
              </FieldRow>
              <FieldRow>
                <Field
                  label="Exact native resource name"
                  htmlFor="ipam-reserve-resource"
                  hint="The creation form must retain this exact owner and name."
                >
                  <Input
                    id="ipam-reserve-resource"
                    value={resource}
                    onChange={(event) => setResource(event.target.value)}
                    disabled={!admin || busy}
                    spellCheck={false}
                  />
                </Field>
                <Field
                  label="Exact prefix (optional)"
                  htmlFor="ipam-reserve-exact"
                  hint={
                    pool
                      ? `Leave empty for the first candidate /${pool.allocationBits}.`
                      : "Select a pool first."
                  }
                >
                  <Input
                    id="ipam-reserve-exact"
                    value={exact}
                    onChange={(event) => setExact(event.target.value)}
                    className="font-mono"
                    disabled={!admin || busy}
                    spellCheck={false}
                  />
                </Field>
              </FieldRow>
              {!!unknown.length && (
                <label className="flex items-start gap-2 text-body">
                  <Checkbox
                    checked={acknowledge}
                    onCheckedChange={(value) => setAcknowledge(value === true)}
                    disabled={!admin || busy}
                  />
                  I have reviewed the incomplete native/provider coverage. This reserves planning
                  space and does not prove absence of foreign allocations.
                </label>
              )}
              <Button
                type="submit"
                disabled={
                  !admin ||
                  busy ||
                  !pool ||
                  !resource.trim() ||
                  (!!unknown.length && !acknowledge) ||
                  Boolean(view.error)
                }
              >
                Reserve planning prefix
              </Button>
              {owner !== "docker_network" && owner !== "wireguard_server" && (
                <p className="text-hint text-muted-foreground">
                  This owner currently receives an advisory planning prefix. Its native form remains
                  responsible for validation and applying configuration.
                </p>
              )}
            </form>
          </Disclosure>
        </PanelBody>
      </Panel>
      <ConfirmDialog
        request={confirm}
        onOpenChange={(open) => {
          if (!open) setConfirm(null)
        }}
      />
    </Page>
  )
}
function Picker({
  id,
  value,
  onChange,
  options,
  disabled,
}: {
  id: string
  value: string
  onChange: (value: string) => void
  options: { value: string; label: string }[]
  disabled?: boolean
}) {
  return (
    <Select
      value={value}
      onValueChange={(next) => {
        if (options.some((option) => option.value === next)) onChange(next)
      }}
      disabled={disabled}
    >
      <SelectTrigger id={id}>
        <SelectValue placeholder="Select…" />
      </SelectTrigger>
      <SelectContent>
        {options.map((item) => (
          <SelectItem key={item.value} value={item.value}>
            {item.label}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  )
}
