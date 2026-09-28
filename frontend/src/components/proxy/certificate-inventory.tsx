"use client"

import { useState } from "react"
import Link from "next/link"
import { Copy, Inspect, ShieldCheck } from "@/components/icons"
import { copyText } from "@/lib/clipboard"
import { calendarDate } from "@/lib/format"
import type { Certificate } from "@/lib/types"
import { ChoiceRow } from "@/components/flow"
import { Detail, DetailList, SearchInput, Toolbar } from "@/components/page"
import { ProductLogo } from "@/components/product-logo"
import { SidePanel } from "@/components/side-panel"
import { EmptyState, Notice } from "@/components/state"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { ProxyGrid } from "@/components/proxy/route-path"
import { CertLife, ExpiryStatus } from "@/components/proxy/expiry-status"
import { certificateProduct } from "@/components/proxy/marks"

/** One inventory with a detail surface, so paths and every SAN remain readable at any width. */
export function CertificateInventory({
  certs,
  canScan,
}: {
  certs: Certificate[]
  canScan: boolean
}) {
  const [query, setQuery] = useState("")
  const [attention, setAttention] = useState(false)
  const [selected, setSelected] = useState<string | null>(null)
  const needsAttention = (cert: Certificate) => Boolean(cert.error || cert.expired || cert.expiring)
  const needle = query.trim().toLowerCase()
  const ordered = certs
    .filter(
      (cert) =>
        (!attention || needsAttention(cert)) &&
        [
          cert.name,
          cert.issuer,
          cert.path,
          ...cert.domains,
          ...cert.usedBy,
          ...(cert.usedByStreams ?? []),
        ].some((value) => value.toLowerCase().includes(needle)),
    )
    .sort((a, b) => {
      const rank = (c: Certificate) => (c.error ? 0 : c.expired ? 1 : c.expiring ? 2 : 3)
      return rank(a) - rank(b) || a.daysLeft - b.daysLeft
    })
  const selectedCert = certs.find((cert) => cert.path === selected)
  const scanDomain = selectedCert?.domains.find((domain) => !domain.startsWith("*"))

  return (
    <div className="min-w-0 space-y-4">
      <Toolbar className="justify-between">
        <SearchInput
          value={query}
          onChange={(event) => setQuery(event.target.value)}
          placeholder="Certificate, domain or issuer"
        />
        <ChipStrip>
          <FilterChip selected={!attention} onClick={() => setAttention(false)}>
            All <ChipCount>{certs.length}</ChipCount>
          </FilterChip>
          <FilterChip selected={attention} onClick={() => setAttention(true)}>
            Needs attention <ChipCount>{certs.filter(needsAttention).length}</ChipCount>
          </FilterChip>
        </ChipStrip>
      </Toolbar>
      {ordered.length === 0 ? (
        <EmptyState icon={ShieldCheck} title="No certificates match" />
      ) : (
        <ProxyGrid
          aria-label="Installed certificates"
          data-slot="cert-list"
          className="xl:grid-cols-1 2xl:grid-cols-2"
        >
          {ordered.map((cert) => (
            <ChoiceRow
              key={cert.path}
              verb={`Inspect ${cert.name}`}
              onSelect={() => setSelected(cert.path)}
              className="h-full gap-4 p-4"
              leading={
                <ProductLogo id={certificateProduct(cert)} size="md" fallback={ShieldCheck} />
              }
              title={<span className="text-title">{cert.name}</span>}
              description={cert.issuer || "Unknown issuer"}
              trailing={<ExpiryStatus cert={cert} />}
            >
              <div className="space-y-3 border-y border-hairline py-3">
                <p className="font-mono text-body break-all">
                  {cert.domains.join(", ") || "No names reported"}
                </p>
                {!cert.error && <CertLife cert={cert} className="w-full" />}
                <div className="flex flex-wrap justify-between gap-2 text-hint text-muted-foreground">
                  <span>
                    {cert.error
                      ? "Certificate could not be read"
                      : `Expires ${calendarDate(cert.notAfter)}`}
                  </span>
                  <Tag>
                    {cert.source.startsWith("nginx:")
                      ? "site file"
                      : cert.source.startsWith("stream:")
                        ? "stream file"
                        : cert.source}
                  </Tag>
                </div>
              </div>
              <div className="flex flex-wrap items-center justify-between gap-2 text-hint text-muted-foreground">
                <span className="min-w-0 break-all">
                  Used by{" "}
                  {[
                    ...cert.usedBy,
                    ...(cert.usedByStreams ?? []).map((name) => `stream ${name}`),
                  ].join(", ") || "nothing"}
                </span>
                {cert.selfSigned && <Tag tone="warning">self-signed</Tag>}
              </div>
            </ChoiceRow>
          ))}
        </ProxyGrid>
      )}
      <SidePanel
        open={Boolean(selectedCert)}
        onOpenChange={(open) => !open && setSelected(null)}
        title={selectedCert?.name ?? "Certificate"}
        description="Certificate details and the sites using it"
        width="md"
        footer={
          selectedCert && (
            <>
              <Button
                size="sm"
                variant="outline"
                onClick={() => void copyText(selectedCert.path, "Path copied")}
              >
                <Copy className="size-3.5" />
                Copy path
              </Button>
              {canScan && scanDomain && (
                <Button size="sm" asChild>
                  <Link href={`/proxy/tls?domain=${encodeURIComponent(scanDomain)}`}>
                    <Inspect className="size-3.5" />
                    TLS report
                  </Link>
                </Button>
              )}
            </>
          )
        }
      >
        {selectedCert && (
          <div className="space-y-6">
            <div className="flex items-center gap-3">
              <ProductLogo id={certificateProduct(selectedCert)} size="md" fallback={ShieldCheck} />
              <div className="min-w-0 flex-1">
                <p className="text-title font-medium break-words">
                  {selectedCert.issuer || "Unknown issuer"}
                </p>
                <ExpiryStatus cert={selectedCert} />
              </div>
            </div>
            {selectedCert.error && (
              <Notice tone="danger" title="Unreadable certificate" className="break-all">
                {selectedCert.error}
              </Notice>
            )}
            {!selectedCert.error && <CertLife cert={selectedCert} className="w-full" />}
            <DetailList>
              <Detail label="Names">
                <span className="break-all">{selectedCert.domains.join(", ") || "—"}</span>
              </Detail>
              <Detail label="Issued">
                {!selectedCert.error && selectedCert.notBefore
                  ? calendarDate(selectedCert.notBefore)
                  : "—"}
              </Detail>
              <Detail label="Expires">
                {!selectedCert.error && selectedCert.notAfter
                  ? calendarDate(selectedCert.notAfter)
                  : "—"}
              </Detail>
              <Detail label="Source">{selectedCert.source}</Detail>
              <Detail label="Used by">
                {selectedCert.usedBy.length || selectedCert.usedByStreams?.length ? (
                  <div className="flex flex-col gap-2">
                    {selectedCert.usedBy.map((site) => (
                      <Link
                        key={site}
                        href={`/proxy/sites?site=${encodeURIComponent(site)}`}
                        className="break-all text-brand hover:underline"
                      >
                        {site}
                      </Link>
                    ))}
                    {selectedCert.usedByStreams?.map((stream) => (
                      <Link
                        key={`stream:${stream}`}
                        href={`/proxy/streams?stream=${encodeURIComponent(stream)}`}
                        className="break-all text-brand hover:underline"
                      >
                        stream {stream}
                      </Link>
                    ))}
                  </div>
                ) : (
                  "nothing"
                )}
              </Detail>
              <Detail label="File">
                <span className="font-mono text-hint break-all">{selectedCert.path}</span>
              </Detail>
              <Detail label="Self-signed">
                {selectedCert.error ? "—" : selectedCert.selfSigned ? "yes" : "no"}
              </Detail>
            </DetailList>
          </div>
        )}
      </SidePanel>
    </div>
  )
}
