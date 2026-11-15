"use client"

import { useState } from "react"
import { useAuth } from "@/hooks/use-auth"
import { LockClosed, Plus, Trash } from "@/components/icons"
import { del, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import type { NetworkLink } from "@/lib/types"
import { useConfirm } from "@/components/confirm-dialog"
import { FormNote, InfoTip } from "@/components/form"
import { IconAction } from "@/components/icon-action"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Cidr } from "@/components/network/address"
import { addressProvenance } from "./device-reading"

/**
 * Where a device answers: each address with its family, its scope, how it
 * came to be and how long it lives. The ones the dashboard added can be
 * taken away; one the guard holds says why in place of the control. A new
 * address is static and kept by the dashboard, and a refused one stays in
 * the field to be corrected.
 */
export function DeviceAddresses({ link, onChanged }: { link: NetworkLink; onChanged: () => void }) {
  const { can } = useAuth()
  const admin = can("system.admin")
  const destructive = admin && can("destructive")
  const canAdd = admin && !link.guard
  const { confirm, dialog } = useConfirm()
  const [address, setAddress] = useState("")
  const [busy, setBusy] = useState(false)
  const name = encodeURIComponent(link.name)
  const acquired =
    !link.managed &&
    link.addresses.some((a) => a.origin && a.origin !== "static" && a.origin !== "link-local")

  const add = async () => {
    const cidr = address.trim()
    if (!cidr || busy) return
    setBusy(true)
    try {
      await post(`/network/links/${name}/addresses`, { cidr })
      notify.success(`${cidr} added to ${link.name}`)
      setAddress("")
      onChanged()
    } catch (err) {
      // The draft stays: a refused address is corrected, not typed again.
      notify.error(`${link.name}: not changed`, err)
    } finally {
      setBusy(false)
    }
  }
  const remove = (cidr: string) =>
    confirm({
      title: `Remove ${cidr}`,
      confirmLabel: "Remove",
      description: (
        <p>
          <span className="font-mono">{link.name}</span> stops answering at{" "}
          <span className="font-mono">{cidr}</span>, and the routes the kernel made for its network
          go with it.
        </p>
      ),
      action: async () => {
        await del(`/network/links/${name}/addresses`, { query: { cidr } })
        notify.success(`${cidr} removed`)
        onChanged()
        return "reported"
      },
    })

  return (
    <Panel plain className="pb-6">
      <PanelHeader
        title={
          <span className="inline-flex items-center gap-2">
            Addresses
            {acquired && (
              <InfoTip label="Where its addresses come from">
                Addresses added here are static and kept by the dashboard. How {link.name} acquires
                its own (DHCP, router advertisements or a fixed address) is its network
                manager&rsquo;s setting
                {admin ? ", edited in its native profile under Settings." : "."}
              </InfoTip>
            )}
          </span>
        }
        actions={
          <span className="numeric text-hint text-muted-foreground">{link.addresses.length}</span>
        }
      />
      <PanelBody>
        {link.addresses.length === 0 ? (
          <p className="py-2 text-body text-muted-foreground">
            <span className="font-mono">{link.name}</span> has no address.
          </p>
        ) : (
          <ul aria-label="Addresses" className="flex flex-col divide-y divide-hairline">
            {link.addresses.map((a) => (
              <li
                key={a.cidr}
                data-slot="address-row"
                className="flex h-14 min-w-0 items-center gap-3"
              >
                <span className="min-w-0 flex-1">
                  <Cidr cidr={a.cidr} className="block truncate text-body" />
                  <span className="block truncate text-hint text-muted-foreground">
                    {a.family === "inet" ? "IPv4" : "IPv6"} · {a.scope} · {addressProvenance(a)}
                    {a.public ? " · public" : ""}
                  </span>
                </span>
                {a.guard ? (
                  <span className="inline-flex shrink-0 items-center gap-1 text-hint text-muted-foreground">
                    <LockClosed aria-hidden className="size-3" />
                    guarded
                    <InfoTip label={`Why ${a.cidr} is guarded`}>{a.guard}</InfoTip>
                  </span>
                ) : destructive && a.managed ? (
                  <IconAction label={`Remove ${a.cidr}`} onClick={() => remove(a.cidr)}>
                    <Trash />
                  </IconAction>
                ) : null}
              </li>
            ))}
          </ul>
        )}
        {canAdd && (
          <form
            className="mt-4 flex max-w-xl items-center gap-2"
            onSubmit={(event) => {
              event.preventDefault()
              void add()
            }}
          >
            <Input
              value={address}
              placeholder="10.20.0.1/24 or 2001:db8::1/64"
              onChange={(event) => setAddress(event.target.value)}
              aria-label="Address in CIDR form"
              className="min-w-0 flex-1 font-mono"
            />
            <Button
              type="submit"
              size="sm"
              variant="outline"
              className="h-9 shrink-0"
              disabled={!address.trim() || busy}
              pending={busy}
            >
              <Plus aria-hidden />
              Add
            </Button>
          </form>
        )}
        {admin && link.guard && (
          <FormNote className="mt-4 flex items-start gap-1.5">
            <LockClosed aria-hidden className="mt-0.5 size-3 shrink-0" />
            <span>{link.guard}</span>
          </FormNote>
        )}
      </PanelBody>
      {dialog}
    </Panel>
  )
}
