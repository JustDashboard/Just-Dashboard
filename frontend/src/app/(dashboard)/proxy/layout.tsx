"use client"

import { useRouter } from "next/navigation"
import { get } from "@/lib/api"
import type { Certificate, Listener, StreamStatus, VHost } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { useShortcuts } from "@/hooks/use-shortcuts"
import { ProxyProvider, type ProxyStatus } from "@/components/proxy/proxy-context"
import { reading } from "@/components/proxy/freshness"
import { useConfigTest } from "@/components/proxy/test-result"

/**
 * The proxy is nine pages — the overview, the sites, the certificates, what
 * a visitor actually gets over TLS, the non-HTTP streams, every listening
 * port, the traffic, the alerts, and nginx's configuration as files. The
 * rail lists them; this layout holds what they share.
 *
 * It polls which proxy this host runs, because the site builder needs nginx
 * and both it and the Overview read that. It does not gate the section:
 * certificates and ports are real questions with or without a proxy installed.
 *
 * It also polls the four lists more than one page shows — sites,
 * certificates, streams and ports — so the overview's figures, each page's
 * table and the rail's marks read one answer instead of each page asking
 * again as it is opened, and it owns the config test the `t` key runs.
 */
export default function ProxyLayout({ children }: { children: React.ReactNode }) {
  const { can } = useAuth()
  const admin = can("system.admin")
  const status = usePoll(
    (signal) => reading(get<ProxyStatus>("/proxy/status", undefined, signal)),
    60_000,
  )
  const vhosts = usePoll(
    (signal) => reading(get<VHost[]>("/proxy/vhosts", undefined, signal)),
    30_000,
  )
  const certs = usePoll(
    (signal) => reading(get<Certificate[]>("/certificates/", undefined, signal)),
    300_000,
  )
  const streams = usePoll(
    (signal) => reading(get<StreamStatus>("/proxy/streams/", undefined, signal)),
    60_000,
  )
  // The Ports page's own interval: it is the page that reads this list closest.
  const ports = usePoll((signal) => reading(get<Listener[]>("/ports", undefined, signal)), 15_000)
  const configTest = useConfigTest({ status: status.data?.value, admin })

  const refreshShared = () => {
    status.refresh()
    vhosts.refresh()
    certs.refresh()
    streams.refresh()
    ports.refresh()
    configTest.refreshLast()
  }
  useProxyShortcuts({
    onTest: admin ? configTest.run : undefined,
    onRefresh: refreshShared,
  })

  return (
    <ProxyProvider
      value={{
        status: status.data?.value,
        updatedAt: status.data?.at,
        error: status.error,
        loading: status.loading,
        hasNginx: status.data?.value.nginx ?? false,
        refresh: status.refresh,
        reads: { vhosts, certs, streams, ports },
        configTest,
      }}
    >
      {children}
      {configTest.panel}
    </ProxyProvider>
  )
}

/**
 * The section's keys: `t` tests the engine's config, Shift+R reads the
 * shared lists again, `/` puts the cursor in the page's filter, and `g`
 * then s, c or p opens Sites, Certificates or Ports. `useShortcuts` holds
 * them while the reader types or a dialog or menu owns the keyboard (a key
 * acting behind one acts on a page they cannot see), and holds the letters
 * when single-key shortcuts are off.
 */
function useProxyShortcuts({
  onTest,
  onRefresh,
}: {
  /** Absent for an account that may not run the test. */
  onTest?: () => void
  onRefresh: () => void
}) {
  const router = useRouter()
  const go = (href: string) => () => router.push(href)
  useShortcuts({
    t: onTest && (() => onTest()),
    "Shift+R": () => onRefresh(),
    "/": () => {
      const search = document.querySelector<HTMLInputElement>("[data-page-search]")
      if (!search) return false
      search.focus()
    },
    "g s": go("/proxy/sites"),
    "g c": go("/proxy/certificates"),
    "g p": go("/proxy/ports"),
  })
}
