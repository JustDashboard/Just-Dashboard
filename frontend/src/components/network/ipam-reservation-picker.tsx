"use client"

import { useEffect, useRef } from "react"
import Link from "next/link"
import { Field } from "@/components/form"
import { Button } from "@/components/ui/button"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { get } from "@/lib/api"
import { readIPAMView, selectedReservation } from "@/lib/network-ipam"
import type { IPAMOwner, IPAMReservation, IPAMView } from "@/lib/network-ipam"

export function IPAMReservationPicker({
  open,
  owner,
  selected,
  onSelect,
  onUnavailable,
  initialId = "",
  disabled = false,
  refreshKey = 0,
}: {
  open: boolean
  owner: IPAMOwner
  selected: IPAMReservation[]
  onSelect: (row: IPAMReservation | undefined, family: "inet" | "inet6") => void
  onUnavailable: (value: boolean) => void
  initialId?: string
  disabled?: boolean
  refreshKey?: number
}) {
  const { can } = useAuth()
  const admin = can("system.admin")
  const inventory = usePoll<IPAMView>(
    async (signal) => readIPAMView(await get("/network/ipam/", undefined, signal)),
    30000,
    [owner, admin, open, refreshKey],
    { enabled: open && admin },
  )
  const loadedInitial = useRef("")
  const rows =
    inventory.data?.reservations.filter((row) => row.owner === owner && row.state === "reserved") ??
    []
  const invalid = selected.some((row) => !selectedReservation(rows, row.id, owner))
  const missingInitial = Boolean(
    initialId &&
    inventory.data &&
    !selectedReservation(inventory.data.reservations, initialId, owner),
  )
  const unavailable =
    Boolean(selected.length && (!inventory.data || inventory.error || invalid)) ||
    Boolean(initialId && (!inventory.data || inventory.error)) ||
    missingInitial
  useEffect(() => {
    onUnavailable(unavailable)
  }, [unavailable, onUnavailable])
  useEffect(() => {
    if (
      !open ||
      !admin ||
      !initialId ||
      !inventory.data ||
      inventory.error ||
      loadedInitial.current === initialId
    )
      return
    const row = selectedReservation(inventory.data.reservations, initialId, owner)
    if (!row) return
    loadedInitial.current = initialId
    onSelect(row, row.family)
  }, [open, admin, initialId, inventory.data, inventory.error, owner, onSelect])
  if (!admin) return null
  return (
    <div className="space-y-3">
      <div className="grid gap-3 sm:grid-cols-2">
        {(["inet", "inet6"] as const).map((family) => (
          <Field
            key={family}
            label={`Shared ${family === "inet" ? "IPv4" : "IPv6"} reservation`}
            htmlFor={`ipam-pick-${owner}-${family}`}
            hint="Optional planning identity; native creation rechecks current owners."
          >
            <Select
              value={selected.find((row) => row.family === family)?.id ?? "none"}
              disabled={disabled || !inventory.data || Boolean(inventory.error)}
              onValueChange={(id) => {
                const row = selectedReservation(rows, id, owner)
                if (id === "none" || row) onSelect(row, family)
              }}
            >
              <SelectTrigger id={`ipam-pick-${owner}-${family}`}>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="none">No selected plan</SelectItem>
                {rows
                  .filter((row) => row.family === family)
                  .map((row) => (
                    <SelectItem key={row.id} value={row.id}>
                      {row.resource} · {row.prefix}
                    </SelectItem>
                  ))}
              </SelectContent>
            </Select>
          </Field>
        ))}
      </div>
      {(inventory.error || invalid || missingInitial) && (
        <div role="alert" className="space-y-2 text-hint text-destructive">
          <p>
            {inventory.error
              ? "Shared reservation inventory is unavailable. Your draft is retained; refresh before using a selected plan."
              : "The selected reservation is absent, held by another handoff or no longer reserved. Return to IPAM to review it."}
          </p>
          <Button type="button" size="xs" variant="outline" onClick={inventory.refresh}>
            Refresh planning inventory
          </Button>
        </div>
      )}
      <p className="text-hint text-muted-foreground">
        Selected plans fill an exact name and prefix. Editing either clears that selection.
        Reservations do not reserve native network state.{" "}
        <Link href="/network/ipam" className="underline underline-offset-4">
          Review shared address pools
        </Link>
      </p>
    </div>
  )
}
