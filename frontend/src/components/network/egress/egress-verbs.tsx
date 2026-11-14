"use client"

import { useState } from "react"
import { useAuth } from "@/hooks/use-auth"
import { del, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import {
  egressManualActions,
  egressNames,
  egressPolicySummary,
  type EgressGroup,
  type EgressView,
} from "@/lib/network-egress"
import { useConfirm } from "@/components/confirm-dialog"
import type { Verb } from "@/components/verbs"
import {
  ArrowLeftRight,
  CornerUpLeft,
  Lightning,
  Pause,
  Pencil,
  Play,
  StopCircle,
  Trash,
} from "@/components/icons"

/**
 * Everything a group can be told to do, with the confirmations the server's
 * own destructive check stands behind: the list's card menus and a group's
 * page draw the same verbs, so a switch confirmed in one is confirmed the
 * same way in the other.
 *
 * A verb the account can never use is left out rather than drawn disabled; a
 * verb the group's state forbids for now stays, disabled, so the menu says it
 * exists.
 */
export function useEgressVerbs({
  group,
  running,
  onChanged,
  onEdit,
  onRemoved,
}: {
  group: EgressGroup
  running?: EgressView["running"]
  onChanged: () => void
  onEdit: (group: EgressGroup) => void
  onRemoved?: () => void
}) {
  const { can } = useAuth()
  const admin = can("system.admin")
  const destructive = admin && can("destructive")
  const { confirm, dialog } = useConfirm()
  const [busy, setBusy] = useState(false)
  const manual = egressManualActions(group)

  const switchTo = (action: "failover" | "failback") =>
    confirm({
      title: action === "failover" ? `Fail ${group.name} over now` : `Fail ${group.name} back now`,
      confirmLabel: action === "failover" ? "Fail over" : "Fail back",
      description: (
        <div className="space-y-2">
          <p>
            The group&rsquo;s traffic moves off {egressNames(group, group.active)}. The members it
            moves to must answer their probes first, and the switch is put back if your own
            connection or this server&rsquo;s way out would move anywhere unproven.
          </p>
          <p>
            Connections already open through {egressNames(group, group.active)} are counted;
            {group.sticky
              ? " pinned ones stay on their member while it is up."
              : " one translated to its address will not survive the move."}
          </p>
        </div>
      ),
      action: async () => {
        const result = await post<{
          after: number[]
          connections?: { moved: number; flushed: number }
        }>(`/network/egress/${group.id}/switch`, { action })
        notify.success(`${group.name} now routes through ${egressNames(group, result.after)}`, {
          description: result.connections
            ? `${result.connections.moved} tracked connections moved, ${result.connections.flushed} flushed.`
            : undefined,
        })
        onChanged()
        return "reported"
      },
    })

  const setEnabled = (on: boolean) =>
    confirm({
      title: on ? `Enable ${group.name}` : `Disable ${group.name}`,
      confirmLabel: on ? "Enable" : "Disable",
      description: on ? (
        <p>
          {egressPolicySummary(group.policy)} leaves the main table for{" "}
          {egressNames(group, group.active)}. The decided members are probed first; if the group
          would carry your own connection, your address is kept on the main table.
        </p>
      ) : (
        <p>
          The group&rsquo;s traffic returns to whatever the main table says, and automation is
          turned off. If the main table&rsquo;s path is the one that failed, that traffic fails with
          it.
        </p>
      ),
      action: async () => {
        await post(`/network/egress/${group.id}/${on ? "enable" : "disable"}`)
        notify.success(
          on ? `${group.name} carries traffic` : `${group.name} no longer carries traffic`,
        )
        onChanged()
        return "reported"
      },
    })

  const automationOn = () =>
    confirm({
      title: `Automate ${group.name}`,
      confirmLabel: "Turn on",
      description: (
        <p>
          The monitor will fail over as soon as {egressNames(group, group.active)} is declared down
          and{" "}
          {group.failback === "automatic"
            ? "fail back after a stable recovery"
            : "leave failback to you"}
          , through the same proof and guards a manual switch takes. Simulation{" "}
          {group.lastSimulation?.id} showed these rules deciding.
        </p>
      ),
      action: async () => {
        await post(`/network/egress/${group.id}/automation/on`)
        notify.success(`${group.name} is automated`)
        onChanged()
        return "reported"
      },
    })

  // Turning automation off only takes a switch away from the monitor, so it
  // is not confirmed; the server does not count it as destructive either.
  const automationOff = async () => {
    setBusy(true)
    try {
      await post(`/network/egress/${group.id}/automation/off`)
      notify.success(`${group.name} switches only by hand now`)
      onChanged()
    } catch (err) {
      notify.error("Automation not changed", err)
    } finally {
      setBusy(false)
    }
  }

  const remove = () =>
    confirm({
      title: `Remove ${group.name}`,
      confirmLabel: "Remove",
      description: (
        <p>
          Its tables, rules and connection pinning are removed
          {group.enabled ? ", and its traffic returns to the main table" : ""}. The decision record
          stays.
        </p>
      ),
      action: async () => {
        await del(`/network/egress/${group.id}`)
        notify.success(`${group.name} removed`)
        onChanged()
        onRemoved?.()
        return "reported"
      },
    })

  const simulate = async () => {
    setBusy(true)
    try {
      await post(`/network/egress/${group.id}/simulate`)
      notify.success("Simulation started", {
        description: "It runs in disposable namespaces; this host's own network is not touched.",
      })
      onChanged()
    } catch (err) {
      notify.error("Simulation not started", err)
    } finally {
      setBusy(false)
    }
  }

  const simulating = running?.groupId === group.id
  const simulation: Verb | undefined = admin
    ? {
        key: "simulate",
        label: "Run simulation",
        progressive: "Simulating…",
        icon: Play,
        disabled: busy || Boolean(running),
        run: () => void simulate(),
      }
    : undefined

  const verbs: Verb[] = []
  if (destructive && group.enabled) {
    verbs.push(
      {
        key: "failover",
        label: "Fail over now",
        icon: ArrowLeftRight,
        group: "Traffic",
        disabled: !manual.failover,
        run: () => switchTo("failover"),
      },
      {
        key: "failback",
        label: "Fail back now",
        icon: CornerUpLeft,
        group: "Traffic",
        disabled: !manual.failback,
        run: () => switchTo("failback"),
      },
    )
  }
  if (destructive)
    verbs.push({
      key: "enable",
      label: group.enabled ? "Disable" : "Enable",
      icon: group.enabled ? Pause : Lightning,
      group: "Traffic",
      run: () => setEnabled(!group.enabled),
    })
  if (group.automation ? admin : destructive)
    verbs.push({
      key: "automation",
      label: group.automation ? "Stop automating" : "Automate",
      icon: group.automation ? StopCircle : Lightning,
      group: "Automation",
      disabled: busy || (!group.automation && Boolean(group.automationBlock)),
      run: () => (group.automation ? void automationOff() : automationOn()),
    })
  if (admin)
    verbs.push({
      key: "edit",
      // The server refuses to rewrite a group that carries traffic; the menu
      // says why instead of a tooltip a menu item cannot show.
      label: group.enabled ? "Edit (disable it first)" : "Edit",
      icon: Pencil,
      group: "Group",
      disabled: group.enabled,
      run: () => onEdit(group),
    })
  if (destructive)
    verbs.push({
      key: "remove",
      label: "Remove",
      icon: Trash,
      group: "Group",
      danger: true,
      run: remove,
    })

  return { simulation, simulating, verbs, dialog, busy }
}
