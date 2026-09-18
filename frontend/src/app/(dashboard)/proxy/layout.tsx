"use client"

import { get } from "@/lib/api"
import { usePoll } from "@/hooks/use-poll"
import { ProxyProvider, type ProxyStatus } from "@/components/proxy/proxy-context"

/**
 * The proxy is six pages — the sites, the certificates, what a visitor
 * actually gets over TLS, the non-HTTP streams, and every listening port. The
 * rail lists them; this layout exists for the one thing they share.
 *
 * It polls which proxy this host runs, because the site builder needs nginx
 * and both it and the Overview read that. It does not gate the section:
 * certificates and ports are real questions with or without a proxy installed.
 */
export default function ProxyLayout({ children }: { children: React.ReactNode }) {
  const status = usePoll((signal) => get<ProxyStatus>("/proxy/status", undefined, signal), 60_000)

  return (
    <ProxyProvider
      value={{
        status: status.data,
        loading: status.loading,
        hasNginx: status.data?.nginx ?? false,
        refresh: status.refresh,
      }}
    >
      {children}
    </ProxyProvider>
  )
}
