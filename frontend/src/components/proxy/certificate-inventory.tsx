"use client"

import { forwardRef } from "react"
import { ShieldCheck } from "@/components/icons"
import {
  CERT_SORTS,
  certMatches,
  certSource,
  type CertSort,
  type CertSource,
} from "@/lib/certificates"
import { calendarDate, plural } from "@/lib/format"
import type { Certificate } from "@/lib/types"
import { cn } from "@/lib/utils"
import { useSessionState } from "@/lib/view-state"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { SearchInput, Toolbar } from "@/components/page"
import { ProductLogo } from "@/components/product-logo"
import { EmptyState } from "@/components/state"
import { Status } from "@/components/status-dot"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { VerbActions, type Verb } from "@/components/verbs"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { ExpiryStatus } from "@/components/proxy/expiry-status"
import { certificateProduct } from "@/components/proxy/marks"
import { certificateHref } from "@/components/proxy/certificate-paths"

export const SOURCE_LABELS: Record<CertSource, string> = {
  certbot: "certbot",
  imported: "imported",
  caddy: "Caddy",
  site: "site file",
  stream: "stream file",
}

/**
 * Where a certificate came from, as one word: who signed it where that is
 * what sets it apart (the local CA, itself), otherwise who put it here.
 */
export function originWord(cert: Certificate): string {
  if (cert.localCA) return "local CA"
  if (cert.selfSigned) return "self-signed"
  return SOURCE_LABELS[certSource(cert)]
}

/** Who names it: the one site or stream, or how many of each. */
export function usedByWord(cert: Certificate): string {
  const sites = cert.usedBy.length
  const streams = cert.usedByStreams?.length ?? 0
  if (sites + streams === 0) return "used by nothing"
  if (sites === 1 && streams === 0) return cert.usedBy[0]
  if (sites === 0 && streams === 1) return `stream ${cert.usedByStreams?.[0]}`
  return [sites > 0 && plural(sites, "site"), streams > 0 && plural(streams, "stream")]
    .filter(Boolean)
    .join(", ")
}

/**
 * The list's search, source and order, kept for the tab: the certificate
 * page's rail reads the same three, so the next certificate it offers is the
 * next one the list showed.
 */
export function useCertificateFilters() {
  const [query, setQuery] = useSessionState("proxy.certificates.query", "")
  const [source, setSource] = useSessionState<"all" | CertSource>(
    "proxy.certificates.source",
    "all",
  )
  const [sort, setSort] = useSessionState<CertSort>("proxy.certificates.sort", "urgency")
  const apply = (certs: Certificate[]) =>
    certs
      .filter(
        (cert) => (source === "all" || certSource(cert) === source) && certMatches(cert, query),
      )
      .sort(CERT_SORTS[sort])
  return { query, setQuery, source, setSource, sort, setSort, apply }
}

/** The search box over the list, which `/` puts the cursor in (the section's own key). */
export const CertificateSearch = forwardRef<
  HTMLInputElement,
  { value: string; onChange: (value: string) => void; className?: string }
>(function CertificateSearch({ value, onChange, className }, ref) {
  return (
    <SearchInput
      ref={ref}
      data-page-search
      value={value}
      onChange={(event) => onChange(event.target.value)}
      placeholder="Name, a host it covers, or issuer"
      aria-label="Search certificates (press / to focus)"
      containerClassName={className}
    />
  )
})

/** The source chips, drawn only where there is more than one source to tell apart. */
export function SourceChips({
  certs,
  source,
  onSource,
}: {
  certs: Certificate[]
  source: "all" | CertSource
  onSource: (source: "all" | CertSource) => void
}) {
  const sources = (Object.keys(SOURCE_LABELS) as CertSource[])
    .map((key) => ({ key, count: certs.filter((cert) => certSource(cert) === key).length }))
    .filter((s) => s.count > 0)
  return (
    <ChipStrip aria-label="Source">
      <FilterChip selected={source === "all"} onClick={() => onSource("all")}>
        All <ChipCount>{certs.length}</ChipCount>
      </FilterChip>
      {sources.length > 1 &&
        sources.map((s) => (
          <FilterChip
            key={s.key}
            selected={source === s.key}
            onClick={() => onSource(source === s.key ? "all" : s.key)}
          >
            {SOURCE_LABELS[s.key]} <ChipCount>{s.count}</ChipCount>
          </FilterChip>
        ))}
    </ChipStrip>
  )
}

/**
 * Every certificate on this host, one line each, read down its columns: the
 * issuer's mark, the name over the names it covers, where it came from, who
 * names it, when it expires and how long it has left, and its verbs behind
 * the menu. The line opens the certificate's own page.
 *
 * Each was a card of four lines — the names again, a bar, the date and the
 * sites each on their own — about 140px a certificate, with a table view
 * beside it for the reader who wanted to read down columns. One line read
 * down columns is both, so the toggle went; the bar and the dates are on the
 * certificate's page.
 *
 * A test certificate is one line like the rest: its status says so, and its
 * verb, the real issuance, leads its menu. The sentence that hung under its
 * card is on its page.
 */
export function CertificateInventory({
  certs,
  filters,
  verbsFor,
  replacing,
  searchRef,
}: {
  certs: Certificate[]
  filters: ReturnType<typeof useCertificateFilters>
  verbsFor: (cert: Certificate) => Verb[]
  replacing: (cert: Certificate) => boolean
  searchRef?: React.Ref<HTMLInputElement>
}) {
  const { query, setQuery, source, setSource, sort, setSort, apply } = filters
  const ordered = apply(certs)

  return (
    <div className="min-w-0 space-y-4">
      <Toolbar className="justify-between">
        <CertificateSearch ref={searchRef} value={query} onChange={setQuery} />
        <ToggleGroup
          type="single"
          value={sort}
          onValueChange={(v) => v && setSort(v as CertSort)}
          variant="outline"
          size="sm"
          aria-label="Sort"
        >
          <ToggleGroupItem value="urgency" className="text-hint">
            Urgency
          </ToggleGroupItem>
          <ToggleGroupItem value="expiry" className="text-hint">
            Expiry
          </ToggleGroupItem>
          <ToggleGroupItem value="name" className="text-hint">
            Name
          </ToggleGroupItem>
        </ToggleGroup>
      </Toolbar>
      <SourceChips certs={certs} source={source} onSource={setSource} />
      {ordered.length === 0 ? (
        <EmptyState icon={ShieldCheck} title="No certificates match" />
      ) : (
        <ChoiceList aria-label="Installed certificates" data-slot="cert-list">
          {ordered.map((cert) => (
            <CertificateRow
              key={cert.path}
              cert={cert}
              href={certificateHref(cert, certs)}
              verbs={verbsFor(cert).map((verb) => ({ ...verb, inline: false }))}
              replacing={replacing(cert)}
            />
          ))}
        </ChoiceList>
      )}
    </div>
  )
}

function CertificateRow({
  cert,
  href,
  verbs,
  replacing,
}: {
  cert: Certificate
  href: string
  verbs: Verb[]
  replacing: boolean
}) {
  const names = cert.domains.join(", ")
  return (
    <ChoiceRow
      href={href}
      verb={`Open ${cert.name}`}
      busy={replacing}
      workspaceItem={{ id: cert.path, name: cert.name }}
      className="gap-3 px-3.5 py-3"
      leading={<ProductLogo id={certificateProduct(cert)} size="md" fallback={ShieldCheck} />}
      title={<span className="text-title">{cert.name}</span>}
      description={
        <span className="font-mono" title={cert.error ?? names}>
          {cert.error ? "could not be read" : names || "no names reported"}
        </span>
      }
      trailing={
        // Fixed widths, so each reading starts on the same edge in every row.
        <span className="flex shrink-0 items-center gap-4 text-hint text-muted-foreground">
          <span className="w-20 truncate max-md:hidden" data-slot="cert-origin">
            {originWord(cert)}
          </span>
          <span
            className="w-40 truncate max-xl:hidden"
            title={[...cert.usedBy, ...(cert.usedByStreams ?? [])].join(", ") || undefined}
          >
            {usedByWord(cert)}
          </span>
          <span className="numeric w-24 max-lg:hidden">
            {cert.error || !cert.notAfter ? "—" : calendarDate(cert.notAfter)}
          </span>
          <span className="flex w-28 justify-end text-body max-sm:w-auto">
            {replacing ? (
              <Status state="activating" label="Replacing…" />
            ) : (
              <ExpiryStatus cert={cert} />
            )}
          </span>
        </span>
      }
      actions={
        // The slot is held where there is nothing in it, so a row without a
        // menu keeps its readings on the same edges as the rows that have one.
        <span className={cn("flex w-7 justify-center")}>
          {verbs.length > 0 && (
            <VerbActions dim verbs={verbs} menuLabel={`Actions for ${cert.name}`} />
          )}
        </span>
      }
    />
  )
}
