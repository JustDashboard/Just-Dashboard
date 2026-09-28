"use client"

import { useEffect, useRef } from "react"
import { useRouter } from "next/navigation"
import { get } from "@/lib/api"
import type { Certificate, Listener, ProxyFindingSnooze, StreamStatus, VHost } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { useNavMarks } from "@/components/nav-scope"
import { useNow } from "@/components/deploy/vocabulary"
import { ProxyProvider, type ProxyStatus } from "@/components/proxy/proxy-context"
import { reading } from "@/components/proxy/freshness"
import { useConfigTest } from "@/components/proxy/test-result"
import { certificateFindings } from "@/components/proxy/findings/certificates"
import { siteFindings } from "@/components/proxy/findings/sites"
import { streamFindings } from "@/components/proxy/findings/streams"
import { portFindings } from "@/components/proxy/findings/ports"
import type { ProxyFinding } from "@/components/proxy/findings/shared"
import { frontDoorFindings } from "@/components/proxy/front-door"
import { splitSnoozed } from "@/components/proxy/finding-snooze"

/**
 * The proxy is eight pages — the overview, the sites, the certificates, what
 * a visitor actually gets over TLS, the non-HTTP streams, every listening
 * port, the traffic, and nginx's configuration as files. The rail lists
 * them; this layout holds what they share.
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

  useProxyMarks({
    vhosts: vhosts.error ? undefined : vhosts.data?.value,
    certs: certs.error ? undefined : certs.data?.value,
    streams: streams.error ? undefined : streams.data?.value,
    ports: ports.error ? undefined : ports.data?.value,
  })

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
 * A mark on each page in the rail that holds a warning or worse the reader
 * has not put aside. A notice is not marked: a site left disabled on purpose
 * would light the rail for good. A list that could not be read marks
 * nothing, since its page says so itself.
 */
function useProxyMarks({
  vhosts,
  certs,
  streams,
  ports,
}: {
  vhosts?: VHost[]
  certs?: Certificate[]
  streams?: StreamStatus
  ports?: Listener[]
}) {
  const now = useNow(60_000)
  const snoozes = usePoll(
    (signal) => get<ProxyFindingSnooze[]>("/proxy/findings/snoozes", undefined, signal),
    60_000,
  )
  const held = snoozes.error ? [] : (snoozes.data ?? [])
  const pressing = (findings: ProxyFinding[]) =>
    splitSnoozed(findings, held, now).shown.some((f) => f.level !== "notice")
  useNavMarks({
    "/proxy/sites": pressing(siteFindings({ vhosts })),
    "/proxy/certificates": pressing(certificateFindings({ certs })),
    "/proxy/streams": pressing(streamFindings({ streams })),
    "/proxy/ports": pressing([...portFindings({ ports }), ...frontDoorFindings({ ports, vhosts })]),
  })
}

/** Where `g` then a letter goes. */
const GO: Record<string, string> = {
  s: "/proxy/sites",
  c: "/proxy/certificates",
  p: "/proxy/ports",
}

/**
 * The section's keys: `t` tests the engine's config, Shift+R reads the
 * shared lists again, `/` puts the cursor in the page's filter, and `g`
 * then s, c or p opens Sites, Certificates or Ports. None fires while the
 * reader is typing, or while a dialog or menu owns the keyboard — a key
 * acting behind one acts on a page they cannot see.
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
  const handlers = useRef({ onTest, onRefresh })
  useEffect(() => {
    handlers.current = { onTest, onRefresh }
  })
  useEffect(() => {
    // When `g` was pressed, waiting for the letter that says where to.
    let goAt = 0
    const onKey = (event: KeyboardEvent) => {
      if (event.defaultPrevented || event.metaKey || event.ctrlKey || event.altKey) return
      const target = event.target as HTMLElement | null
      if (
        target?.tagName === "INPUT" ||
        target?.tagName === "TEXTAREA" ||
        target?.tagName === "SELECT" ||
        target?.isContentEditable
      ) {
        return
      }
      if (
        document.querySelector(
          "[role='dialog']:not([data-state='closed']), [role='menu']:not([data-state='closed'])",
        )
      ) {
        return
      }

      const leading = goAt > 0 && Date.now() - goAt < 1500
      goAt = 0
      if (leading) {
        const href = GO[event.key]
        if (href) {
          event.preventDefault()
          router.push(href)
        }
        return
      }
      if (event.key === "g" && !event.shiftKey) {
        goAt = Date.now()
      } else if (event.key === "t" && !event.shiftKey && handlers.current.onTest) {
        event.preventDefault()
        handlers.current.onTest()
      } else if (event.key === "R" && event.shiftKey) {
        event.preventDefault()
        handlers.current.onRefresh()
      } else if (event.key === "/") {
        const search = document.querySelector<HTMLInputElement>("[data-page-search]")
        if (search) {
          event.preventDefault()
          search.focus()
        }
      }
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [router])
}
