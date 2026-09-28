"use client"

import { useState } from "react"
import Link from "next/link"
import { Copy, FileText, Inspect, ShieldCheck, Trash, Warning } from "@/components/icons"
import { certbotRunning, replacingTestCertificate } from "@/lib/certificates"
import { copyText } from "@/lib/clipboard"
import { calendarDate } from "@/lib/format"
import type { Certificate, Job } from "@/lib/types"
import { ChoiceRow } from "@/components/flow"
import { Detail, DetailList, SearchInput, Toolbar } from "@/components/page"
import { ProductLogo } from "@/components/product-logo"
import { SidePanel } from "@/components/side-panel"
import { EmptyState, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { Tag } from "@/components/tag"
import { VerbBar, type Verb } from "@/components/verbs"
import { Button } from "@/components/ui/button"
import { CertificateDetails, PemDecoder } from "@/components/proxy/cert-detail"
import { ProxyGrid } from "@/components/proxy/route-path"
import { CertLife, ExpiryStatus } from "@/components/proxy/expiry-status"
import { certificateProduct } from "@/components/proxy/marks"

/**
 * A test certificate, and what a real issuance does about it: certbot's own
 * is replaced in place, where every site naming it finds the real one; any
 * other file stays as it is, and the real one lands in certbot's directory.
 */
function stagingNote(cert: Certificate, canReplace: boolean): string {
  const refused = "A staging authority signed it, so browsers refuse it."
  if (!canReplace) return refused
  return cert.source === "certbot"
    ? `${refused} A real issuance for the same names replaces it.`
    : `${refused} certbot saves the real one in its own directory: point the site at it.`
}

/** Where a certificate comes from, in the words the list uses. */
function sourceLabel(cert: Certificate): string {
  if (cert.source.startsWith("nginx:")) return "site file"
  return cert.source === "caddy" ? "Caddy" : cert.source
}

/** One inventory with a detail surface, so paths and every SAN remain readable at any width. */
export function CertificateInventory({
  certs,
  canScan,
  canReadHistory,
  job = null,
  onReplace,
  onDelete,
}: {
  certs: Certificate[]
  canScan: boolean
  /** A certificate's timeline reads the audit trail, which only administrators may. */
  canReadHistory: boolean
  /** The job on screen: a certificate it is replacing says so, and no other certbot run starts. */
  job?: Job | null
  /** Opens the real issuance for a test certificate's names; absent where nobody here can issue. */
  onReplace?: (domains: string) => void
  /** Deletes a certbot lineage or an import; absent for anyone who may not. */
  onDelete?: (cert: Certificate) => void
}) {
  const [query, setQuery] = useState("")
  const [filter, setFilter] = useState<"all" | "attention" | "caddy">("all")
  const [selected, setSelected] = useState<string | null>(null)
  const [decoding, setDecoding] = useState(false)
  const needsAttention = (cert: Certificate) =>
    Boolean(cert.error || cert.expired || cert.expiring || cert.staging)
  // Only certbot's own file is replaced in place; a copy a site names
  // elsewhere keeps its test certificate whatever certbot does.
  const replacing = (cert: Certificate) =>
    cert.source === "certbot" && replacingTestCertificate(job, cert.domains)
  // Caddy obtains its own from the directory its routes name; certbot
  // issuing one for the same names would change nothing Caddy serves.
  const replaceVerb = (cert: Certificate): Verb | undefined =>
    onReplace && cert.staging && cert.source !== "caddy" && cert.domains.length > 0
      ? {
          key: "replace",
          label:
            cert.source === "certbot"
              ? "Replace with a real certificate"
              : "Issue a real certificate",
          icon: ShieldCheck,
          inline: true,
          // certbot holds one lock for all of its work.
          disabled: certbotRunning(job),
          run: () => onReplace(cert.domains.join(" ")),
        }
      : undefined
  const needle = query.trim().toLowerCase()
  const caddyCount = certs.filter((cert) => cert.source === "caddy").length
  const ordered = certs
    .filter(
      (cert) =>
        (filter !== "attention" || needsAttention(cert)) &&
        (filter !== "caddy" || cert.source === "caddy") &&
        [cert.name, cert.issuer, cert.path, ...cert.domains, ...cert.usedBy].some((value) =>
          value.toLowerCase().includes(needle),
        ),
    )
    .sort((a, b) => {
      const rank = (c: Certificate) =>
        c.error ? 0 : c.expired || c.staging ? 1 : c.expiring ? 2 : 3
      return rank(a) - rank(b) || a.daysLeft - b.daysLeft
    })
  const selectedCert = certs.find((cert) => cert.path === selected)
  const scanDomain = selectedCert?.domains.find((domain) => !domain.startsWith("*"))
  const selectedReplace = selectedCert && replaceVerb(selectedCert)
  // Only certbot's lineages and the imports are this host's to delete: a
  // file a site names elsewhere is the site's, and Caddy renews its own.
  const deletable =
    onDelete && (selectedCert?.source === "certbot" || selectedCert?.source === "imported")

  return (
    <div className="min-w-0 space-y-4">
      <Toolbar className="justify-between">
        <SearchInput
          value={query}
          onChange={(event) => setQuery(event.target.value)}
          placeholder="Certificate, domain or issuer"
        />
        <div className="flex flex-wrap items-center gap-2">
          <ChipStrip>
            <FilterChip selected={filter === "all"} onClick={() => setFilter("all")}>
              All <ChipCount>{certs.length}</ChipCount>
            </FilterChip>
            <FilterChip selected={filter === "attention"} onClick={() => setFilter("attention")}>
              Needs attention <ChipCount>{certs.filter(needsAttention).length}</ChipCount>
            </FilterChip>
            {caddyCount > 0 && (
              <FilterChip selected={filter === "caddy"} onClick={() => setFilter("caddy")}>
                Caddy <ChipCount>{caddyCount}</ChipCount>
              </FilterChip>
            )}
          </ChipStrip>
          <Button size="sm" variant="outline" onClick={() => setDecoding(true)}>
            <FileText className="size-3.5" />
            Paste a certificate
          </Button>
        </div>
      </Toolbar>
      <PemDecoder open={decoding} onOpenChange={setDecoding} />
      {ordered.length === 0 ? (
        <EmptyState icon={ShieldCheck} title="No certificates match" />
      ) : (
        <ProxyGrid
          aria-label="Installed certificates"
          data-slot="cert-list"
          className="xl:grid-cols-1 2xl:grid-cols-2"
        >
          {ordered.map((cert) => {
            const verb = replaceVerb(cert)
            const busy = replacing(cert)
            return (
              <ChoiceRow
                key={cert.path}
                verb={`Inspect ${cert.name}`}
                onSelect={() => setSelected(cert.path)}
                busy={busy}
                className="h-full gap-4 p-4"
                leading={
                  <ProductLogo id={certificateProduct(cert)} size="md" fallback={ShieldCheck} />
                }
                title={<span className="text-title">{cert.name}</span>}
                description={cert.issuer || "Unknown issuer"}
                trailing={
                  busy ? (
                    <Status state="activating" label="Replacing…" />
                  ) : (
                    <ExpiryStatus cert={cert} />
                  )
                }
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
                        : `Expires ${calendarDate(cert.notAfter)}${cert.source === "caddy" ? " · renewed by Caddy" : ""}`}
                    </span>
                    <Tag>{sourceLabel(cert)}</Tag>
                  </div>
                </div>
                <div className="flex flex-wrap items-center justify-between gap-2 text-hint text-muted-foreground">
                  <span className="min-w-0 break-all">
                    Used by {cert.usedBy.join(", ") || "no site"}
                  </span>
                  {cert.selfSigned && <Tag tone="warning">self-signed</Tag>}
                </div>
                {cert.staging && (
                  <div className="flex min-w-0 flex-wrap items-center justify-between gap-3">
                    <p className="min-w-0 flex-1 basis-48 text-hint text-muted-foreground">
                      {stagingNote(cert, Boolean(verb))}
                    </p>
                    {verb && <VerbBar verbs={[verb]} />}
                  </div>
                )}
              </ChoiceRow>
            )
          })}
        </ProxyGrid>
      )}
      <SidePanel
        open={Boolean(selectedCert)}
        onOpenChange={(open) => !open && setSelected(null)}
        title={selectedCert?.name ?? "Certificate"}
        description="Certificate details and the sites using it"
        width="lg"
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
                <Button size="sm" variant={selectedReplace ? "outline" : undefined} asChild>
                  <Link href={`/proxy/tls?domain=${encodeURIComponent(scanDomain)}`}>
                    <Inspect className="size-3.5" />
                    TLS report
                  </Link>
                </Button>
              )}
              {deletable && (
                <Button
                  size="sm"
                  variant="outline"
                  disabled={selectedCert.source === "certbot" && certbotRunning(job)}
                  onClick={() => {
                    setSelected(null)
                    onDelete(selectedCert)
                  }}
                >
                  <Trash className="size-3.5" />
                  Delete
                </Button>
              )}
              {selectedReplace && (
                <Button
                  size="sm"
                  disabled={selectedReplace.disabled}
                  onClick={() => {
                    setSelected(null)
                    selectedReplace.run()
                  }}
                >
                  <ShieldCheck className="size-3.5" />
                  {selectedReplace.label}
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
            {selectedCert.staging && (
              <Notice tone="danger" icon={Warning} title="A test certificate">
                {stagingNote(selectedCert, Boolean(selectedReplace))}
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
              <Detail label="Source">
                {selectedCert.source === "caddy"
                  ? "Caddy, which renews it itself"
                  : selectedCert.source}
              </Detail>
              <Detail label="Used by">
                {selectedCert.usedBy.length ? (
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
                  </div>
                ) : (
                  "no site"
                )}
              </Detail>
              <Detail
                label={selectedCert.source === "caddy" ? "File in the Caddy container" : "File"}
              >
                <span className="font-mono text-hint break-all">{selectedCert.path}</span>
              </Detail>
              {selectedCert.evidence?.length ? (
                <Detail label="Release copies">
                  <div className="flex flex-col gap-2">
                    {selectedCert.evidence.map((copy) => (
                      <span key={copy.name} className="min-w-0">
                        <span className="font-mono text-hint break-all">{copy.path}</span>
                        <span className="block text-hint text-muted-foreground">
                          Kept when a deployment was activated · valid until{" "}
                          {calendarDate(copy.notAfter)}
                        </span>
                      </span>
                    ))}
                  </div>
                </Detail>
              ) : null}
              <Detail label="Self-signed">
                {selectedCert.error ? "—" : selectedCert.selfSigned ? "yes" : "no"}
              </Detail>
            </DetailList>
            <CertificateDetails cert={selectedCert} canReadHistory={canReadHistory} />
          </div>
        )}
      </SidePanel>
    </div>
  )
}
