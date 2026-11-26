"use client"

import { forwardRef } from "react"
import Link from "next/link"
import { ShieldCheck } from "@/components/icons"
import {
  CERT_SORTS,
  certInState,
  certMatches,
  certSource,
  certUnused,
  certificateUsers,
  type CertSort,
  type CertSource,
  type CertStateFilter,
} from "@/lib/certificates"
import { calendarDate } from "@/lib/format"
import type { Certificate } from "@/lib/types"
import { cn } from "@/lib/utils"
import { useSessionState } from "@/lib/view-state"
import { ChoiceList, ChoiceRow } from "@/components/choice-row"
import { SearchInput } from "@/components/search-input"
import { ListToolbar } from "@/components/toolbar"
import { ProductLogo } from "@/components/product-logo"
import { EmptyState } from "@/components/state"
import { Status } from "@/components/status-dot"
import { ChipCount, ChipStrip, FilterChip } from "@/components/chips"
import { VerbActions, type Verb } from "@/components/verbs"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
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

/**
 * Who serves it, in words: the project a deployment's route belongs to, a
 * site's or a stream's name, with how many more after the first, and "Unused"
 * for one nothing serves. The first is a link to it.
 *
 * It said the route's file name ("just-dashboard-env-12.conf"), which names
 * nothing the operator knows, and "used by nothing" in the same grey as a
 * site's name; and the list hid it below `xl`, where most laptops are.
 */
export function UsedBy({ cert, className }: { cert: Certificate; className?: string }) {
  const users = certificateUsers(cert)
  if (cert.error) return <span className={className}>—</span>
  if (certUnused(cert)) {
    return (
      <span className={className} data-slot="cert-unused">
        <Status tone="stopped" label="Unused" />
      </span>
    )
  }
  const [first, ...rest] = users
  return (
    <span
      className={cn("flex min-w-0 items-center gap-1", className)}
      title={users.map((user) => user.label).join(", ")}
      data-slot="cert-used-by"
    >
      <Link
        href={first.href}
        className="min-w-0 truncate rounded-sm focus-ring hover:text-foreground hover:underline"
      >
        {first.label}
      </Link>
      {rest.length > 0 && <span className="numeric shrink-0">+{rest.length}</span>}
    </span>
  )
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
  const [state, setState] = useSessionState<CertStateFilter>("proxy.certificates.state", "all")
  const [sort, setSort] = useSessionState<CertSort>("proxy.certificates.sort", "urgency")
  const apply = (certs: Certificate[]) =>
    certs
      .filter(
        (cert) =>
          (source === "all" || certSource(cert) === source) &&
          certInState(cert, state) &&
          certMatches(cert, query),
      )
      .sort(CERT_SORTS[sort])
  return { query, setQuery, source, setSource, state, setState, sort, setSort, apply }
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
/** The orders the list takes, as the sort menu words them. */
export const CERT_SORT_CHOICES: { value: CertSort; label: string }[] = [
  { value: "urgency", label: "Most urgent first" },
  { value: "expiry", label: "Soonest to expire" },
  { value: "name", label: "By name" },
]

const STATE_LABELS: Record<Exclude<CertStateFilter, "all">, string> = {
  unused: "Unused",
  expired: "Expired",
}

/**
 * The chips over the list: where a certificate came from, and the two states
 * a list is cleaned up by — what nothing serves, and what has run out. A
 * state's chip is drawn only where a certificate is in it.
 */
export function SourceChips({
  certs,
  source,
  onSource,
  state,
  onState,
}: {
  certs: Certificate[]
  source: "all" | CertSource
  onSource: (source: "all" | CertSource) => void
  state: CertStateFilter
  onState: (state: CertStateFilter) => void
}) {
  const sources = (Object.keys(SOURCE_LABELS) as CertSource[])
    .map((key) => ({ key, count: certs.filter((cert) => certSource(cert) === key).length }))
    .filter((s) => s.count > 0)
  const states = (Object.keys(STATE_LABELS) as Exclude<CertStateFilter, "all">[])
    .map((key) => ({ key, count: certs.filter((cert) => certInState(cert, key)).length }))
    // The chosen one stays while it is chosen, so a list emptied by a
    // cleanup still shows what it is narrowed to and how to undo it.
    .filter((s) => s.count > 0 || s.key === state)
  return (
    <ChipStrip aria-label="Source">
      <FilterChip
        selected={source === "all" && state === "all"}
        onClick={() => {
          onSource("all")
          onState("all")
        }}
      >
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
      {states.map((s) => (
        <FilterChip
          key={s.key}
          selected={state === s.key}
          onClick={() => onState(state === s.key ? "all" : s.key)}
        >
          {STATE_LABELS[s.key]} <ChipCount>{s.count}</ChipCount>
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
  const { query, setQuery, source, setSource, state, setState, sort, setSort, apply } = filters
  const ordered = apply(certs)

  return (
    <div className="min-w-0 space-y-4">
      {/* The list's one toolbar: the search, the order at the end, the
          sources under them, as every list in the area has it. */}
      <ListToolbar
        search={<CertificateSearch ref={searchRef} value={query} onChange={setQuery} />}
        sort={
          <Select value={sort} onValueChange={(v) => setSort(v as CertSort)}>
            <SelectTrigger size="sm" className="w-44" aria-label="Sort">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {CERT_SORT_CHOICES.map((choice) => (
                <SelectItem key={choice.value} value={choice.value}>
                  {choice.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        }
        chips={
          <SourceChips
            certs={certs}
            source={source}
            onSource={setSource}
            state={state}
            onState={setState}
          />
        }
      />
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
      // The row every other page of the section draws: its tile and its
      // name were a size up, and the list stood out from the lists beside it.
      leading={<ProductLogo id={certificateProduct(cert)} size="sm" fallback={ShieldCheck} />}
      title={cert.name}
      description={
        <span className="font-mono" title={cert.error ?? names}>
          {cert.error ? "could not be read" : names || "no names reported"}
        </span>
      }
      trailing={
        // Fixed widths, so each reading starts on the same edge in every row.
        <span className="flex shrink-0 items-center gap-4 text-hint text-muted-foreground">
          <span className="w-20 truncate max-lg:hidden" data-slot="cert-origin">
            {originWord(cert)}
          </span>
          {/* Who serves it stays from a tablet up, and the date is what
              gives way on a laptop: the status beside it says how long. */}
          <UsedBy cert={cert} className="w-40 max-md:hidden" />
          <span className="numeric w-24 max-xl:hidden">
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
