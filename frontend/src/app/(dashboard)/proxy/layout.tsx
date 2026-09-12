"use client"

import { get } from "@/lib/api"
import { usePoll } from "@/hooks/use-poll"
import { SectionNav } from "@/components/tabs"
import { ProxyProvider, type ProxyStatus } from "@/components/proxy/proxy-context"

/**
 * The proxy page is five areas — the sites, the certificates, what a visitor
 * actually gets over TLS, the non-HTTP streams, and every listening port. The
 * sidebar entry expands to them; this strip is the switcher when it is
 * collapsed.
 *
 * The layout polls one thing — which proxy this host runs — because the site
 * builder needs nginx and both it and the Overview read that. It does not gate
 * the section: certificates and ports are real questions with or without a
 * proxy installed.
 */
const TABS = [
  { title: "Overview", href: "/proxy" },
  { title: "Sites", href: "/proxy/sites" },
  { title: "Certificates", href: "/proxy/certificates" },
  { title: "TLS report", href: "/proxy/tls" },
  { title: "Streams", href: "/proxy/streams" },
  { title: "Ports", href: "/proxy/ports" },
]

export default function ProxyLayout({ children }: { children: React.ReactNode }) {
  const status = usePoll((signal) => get<ProxyStatus>("/proxy/status", undefined, signal), 60_000)

  return (
    <ProxyProvider
      value={{
        status: status.data,
        loading: status.loading,
        hasNginx: status.data?.nginx ?? false,
      }}
    >
      <SectionNav tabs={TABS} />
      {children}
    </ProxyProvider>
  )
}
