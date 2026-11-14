"use client"

import { useRouter } from "next/navigation"
import {
  Box,
  ChartActivity,
  Clipboard,
  Copy,
  GridMasonry,
  ListOrdered,
  RefreshClockwise,
  Servers,
  Shield,
  Slash,
  Stop,
  StopCircle,
} from "@/components/icons"
import { post } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import type { PortsFirewall } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import type { useConfirm } from "@/components/confirm-dialog"
import type { Verb } from "@/components/verbs"
import {
  ownerActions,
  ownerLinks,
  type OwnerAction,
  type OwnerLink,
  type Socket,
} from "@/components/proxy/ports"
import { firewallHandoffs } from "@/components/proxy/ports-firewall"
import { formatEndpoint } from "@/components/proxy/ports-list"
import { proxyVerbs } from "@/components/proxy/ports-proxy"
import { processHref } from "@/components/procs/process-facts"

const OWNER_ACTION_ICON: Record<OwnerAction["key"], Verb["icon"]> = {
  restart: RefreshClockwise,
  reload: RefreshClockwise,
  stop: StopCircle,
  disable: Slash,
  terminate: StopCircle,
  kill: Stop,
}

const OWNER_ICON: Record<OwnerLink["key"], Verb["icon"]> = {
  container: Box,
  deployment: GridMasonry,
  unit: Servers,
  app: ChartActivity,
}

/**
 * What can be done with a socket, declared once for the list's rows and the
 * port's own page: the owner's own page first (the container, the deployment
 * it runs for, the PM2 app or the unit, where its remedy usually is), the
 * process, copying, the firewall and its hand-offs, putting it behind the
 * proxy, and the owner's own controls under their route's capability, each
 * confirmed.
 */
export function useSocketVerbs({
  firewall,
  confirm,
  onDone,
}: {
  firewall: PortsFirewall | undefined
  confirm: ReturnType<typeof useConfirm>["confirm"]
  onDone: () => void
}) {
  const router = useRouter()
  const { can } = useAuth()
  const admin = can("system.admin")
  return (l: Socket): Verb[] => {
    const endpoint = formatEndpoint(l.address, l.port)
    const verbs: Verb[] = ownerLinks(l).map((link, i) => ({
      key: link.key,
      label: link.label,
      icon: OWNER_ICON[link.key],
      inline: i === 0,
      run: () => router.push(link.href),
    }))
    // Init holding a socket unit's socket is systemd listening, whose page
    // is the unit's; docker-proxy is a container's plumbing, kept in the menu.
    if (l.pid > 1 || (l.pid === 1 && !l.socketUnit)) {
      verbs.push({
        key: "process",
        label: "Process",
        icon: ListOrdered,
        inline: !l.container,
        run: () => router.push(processHref(l.pid)),
      })
    }
    verbs.push({
      key: "endpoint",
      label: "Copy endpoint",
      icon: Copy,
      run: () => void copyText(endpoint, `Copied ${endpoint}`),
    })
    if (l.cmdline) {
      const cmdline = l.cmdline
      verbs.push({
        key: "command",
        label: "Copy command",
        icon: Clipboard,
        run: () => void copyText(cmdline, "Copied the command"),
      })
    }
    if (l.exposed) {
      verbs.push({
        key: "firewall",
        label: "Firewall",
        icon: Shield,
        run: () => router.push("/network/firewall"),
      })
    }
    if (admin) verbs.push(...firewallHandoffs(l, firewall, (href) => router.push(href)))
    if (admin) verbs.push(...proxyVerbs(l, (href) => router.push(href)))
    for (const action of ownerActions(l, can)) {
      verbs.push({
        key: `owner-${action.key}`,
        label: action.label,
        icon: OWNER_ACTION_ICON[action.key],
        danger: action.danger,
        run: () =>
          confirm({
            title: action.title,
            confirmLabel: action.label,
            description: (
              <>
                {action.description.map((line) => (
                  <p key={line}>{line}</p>
                ))}
              </>
            ),
            action: async (phrase) => {
              await post(action.path, action.body, { confirm: phrase })
            },
            onDone,
          }),
      })
    }
    return verbs
  }
}

/** A socket's menu is named by the endpoint it acts on; two programs can share a port on two addresses. */
export function socketMenuLabel(l: Socket) {
  return `Actions for ${l.protocol} ${formatEndpoint(l.address, l.port)}`
}
