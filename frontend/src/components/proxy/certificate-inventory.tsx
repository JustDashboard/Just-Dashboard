"use client"

import { useEffect, useRef, useState } from "react"
import Link from "next/link"
import { useRouter } from "next/navigation"
import {
  CloudUpload,
  Code,
  Copy,
  Eye,
  FileText,
  GridSquare,
  Inspect,
  Pencil,
  RefreshClockwise,
  ShieldCheck,
  Table as TableIcon,
  Trash,
  Warning,
} from "@/components/icons"
import {
  CERT_SORTS,
  certbotRunning,
  certMatches,
  certSource,
  certState,
  expiredAgo,
  renewIfDueLabel,
  replacingTestCertificate,
  sslDirectives,
  type CertSort,
  type CertSource,
  type CertState,
} from "@/lib/certificates"
import { copyText } from "@/lib/clipboard"
import { calendarDate } from "@/lib/format"
import type { CertbotCert, Certificate, Job } from "@/lib/types"
import { useSessionState, useViewState } from "@/lib/view-state"
import { useQuerySelection } from "@/hooks/use-query-selection"
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
import {
  stickyTableHeader,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { CertificateDetails, PemDecoder } from "@/components/proxy/cert-detail"
import { CertbotBusy } from "@/components/proxy/certbot-panel"
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

const SOURCE_LABELS: Record<CertSource, string> = {
  certbot: "certbot",
  imported: "imported",
  caddy: "Caddy",
  site: "site file",
}

const STATE_LABELS: Record<Exclude<CertState, "valid">, string> = {
  expiring: "Expiring",
  expired: "Expired",
  test: "Test",
  unreadable: "Unreadable",
}

type StateFilter = "all" | "attention" | Exclude<CertState, "valid">

const needsAttention = (cert: Certificate) => certState(cert) !== "valid"

/** One inventory with a detail surface, so paths and every SAN remain readable at any width. */
export function CertificateInventory({
  certs,
  canScan,
  canReadHistory,
  job = null,
  lineages,
  onOpenJob,
  onReplace,
  onDelete,
  onRenew,
  onRenewNow,
  onChangeNames,
  onRevoke,
  onReplaceImport,
  onWatch,
}: {
  certs: Certificate[]
  canScan: boolean
  /** A certificate's timeline reads the audit trail, which only administrators may. */
  canReadHistory: boolean
  /**
   * The running certbot job, wherever it was started, or else the one on
   * screen: a certificate it is replacing says so, and no other certbot run
   * starts.
   */
  job?: Job | null
  /** certbot's lineages, for the key each one pairs with its certificate. */
  lineages?: CertbotCert[]
  /** Puts the running certbot job in the console. */
  onOpenJob?: () => void
  /** Opens the real issuance for a test certificate's names; absent where nobody here can issue. */
  onReplace?: (domains: string) => void
  /** Deletes a certbot lineage or an import; absent for anyone who may not. */
  onDelete?: (cert: Certificate) => void
  /** certbot's renewal of a lineage: renewed only if due, or rehearsed. */
  onRenew?: (name: string, dryRun: boolean) => void
  /** A forced renewal, which asks first: it spends a duplicate certificate. */
  onRenewNow?: (name: string) => void
  /** Opens the issuance on a lineage with its names, to change them. */
  onChangeNames?: (name: string, domains: string[]) => void
  onRevoke?: (name: string) => void
  /** Opens the import on an imported certificate's name, to replace its pair. */
  onReplaceImport?: (name: string) => void
  /** Adds a name to the watched domains. */
  onWatch?: (domain: string) => void
}) {
  const router = useRouter()
  const [query, setQuery] = useSessionState("proxy.certificates.query", "")
  const [source, setSource] = useSessionState<"all" | CertSource>(
    "proxy.certificates.source",
    "all",
  )
  const [stateFilter, setStateFilter] = useSessionState<StateFilter>(
    "proxy.certificates.state",
    "all",
  )
  const [sort, setSort] = useSessionState<CertSort>("proxy.certificates.sort", "urgency")
  const [view, setView] = useViewState<"cards" | "table">("proxy.certificates.view", "cards")
  const [selected, select] = useQuerySelection("cert")
  const [decoding, setDecoding] = useState(false)
  const search = useRef<HTMLInputElement>(null)

  // "/" is the search key wherever a list is narrowed; without it the
  // operator's hand leaves the keyboard for every search.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const target = e.target as HTMLElement | null
      const typing =
        target?.tagName === "INPUT" || target?.tagName === "TEXTAREA" || target?.isContentEditable
      if (e.key === "/" && !typing && !e.metaKey && !e.ctrlKey && !e.altKey) {
        e.preventDefault()
        search.current?.focus()
        search.current?.select()
      }
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [])

  // certbot holds one lock for all of its work.
  const busy = certbotRunning(job)
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
          disabled: busy,
          run: () => onReplace(cert.domains.join(" ")),
        }
      : undefined

  const count = (keep: (cert: Certificate) => boolean) => certs.filter(keep).length
  const sources = (Object.keys(SOURCE_LABELS) as CertSource[])
    .map((key) => ({ key, count: count((cert) => certSource(cert) === key) }))
    .filter((s) => s.count > 0)
  const states = (Object.keys(STATE_LABELS) as Exclude<CertState, "valid">[])
    .map((key) => ({ key, count: count((cert) => certState(cert) === key) }))
    .filter((s) => s.count > 0)
  const attention = count(needsAttention)
  const ordered = certs
    .filter(
      (cert) =>
        (source === "all" || certSource(cert) === source) &&
        (stateFilter === "all" ||
          (stateFilter === "attention" ? needsAttention(cert) : certState(cert) === stateFilter)) &&
        certMatches(cert, query),
    )
    .sort(CERT_SORTS[sort])
  const selectedCert = certs.find((cert) => cert.path === selected)
  const toggleState = (next: StateFilter) => setStateFilter(stateFilter === next ? "all" : next)

  // A verb that starts a job or opens a dialog closes the sheet, so what it
  // starts is on screen.
  const closing = (run: () => void) => () => {
    select(null)
    run()
  }

  const sheetVerbs = (cert: Certificate): Verb[] => {
    const verbs: Verb[] = []
    const from = certSource(cert)
    const names = cert.domains.filter((domain) => !domain.startsWith("*."))
    const replace = replaceVerb(cert)
    if (replace) verbs.push({ ...replace, run: closing(replace.run) })
    if (from === "certbot") {
      // A test certificate renews from the staging authority its lineage
      // names, which only fetches another one browsers refuse: its verb is
      // the real issuance above.
      if (!cert.error && !cert.staging && onRenew) {
        verbs.push(
          {
            key: "renew",
            label: renewIfDueLabel(cert),
            icon: RefreshClockwise,
            inline: true,
            disabled: busy,
            group: "certbot",
            run: closing(() => onRenew(cert.name, false)),
          },
          {
            key: "dry-run",
            label: "Dry run",
            icon: ShieldCheck,
            disabled: busy,
            group: "certbot",
            run: closing(() => onRenew(cert.name, true)),
          },
        )
        if (onRenewNow) {
          verbs.push({
            key: "renew-now",
            label: "Renew now",
            icon: Warning,
            disabled: busy,
            group: "certbot",
            run: closing(() => onRenewNow(cert.name)),
          })
        }
      }
      if (onChangeNames && !cert.error) {
        verbs.push({
          key: "names",
          label: "Change names",
          icon: Pencil,
          disabled: busy,
          group: "certbot",
          run: closing(() => onChangeNames(cert.name, cert.domains)),
        })
      }
    }
    if (from === "imported" && onReplaceImport) {
      verbs.push({
        key: "replace-import",
        label: "Replace",
        icon: CloudUpload,
        inline: true,
        run: closing(() => onReplaceImport(cert.name)),
      })
    }
    if (canScan) {
      for (const name of names) {
        verbs.push({
          key: `tls:${name}`,
          label: `TLS report for ${name}`,
          icon: Inspect,
          group: "Inspect",
          run: () => router.push(`/proxy/tls?domain=${encodeURIComponent(name)}`),
        })
      }
    }
    if (onWatch) {
      for (const name of names) {
        verbs.push({
          key: `watch:${name}`,
          label: `Watch ${name}`,
          icon: Eye,
          group: "Inspect",
          run: () => onWatch(name),
        })
      }
    }
    const keyPath =
      from === "certbot"
        ? lineages?.find((lineage) => lineage.name === cert.name)?.keyPath
        : from === "imported"
          ? `${cert.path.slice(0, cert.path.lastIndexOf("/"))}/privkey.pem`
          : undefined
    if (keyPath) {
      verbs.push({
        key: "directives",
        label: "Copy nginx directives",
        icon: Code,
        group: "Copy",
        run: () => void copyText(sslDirectives(cert.path, keyPath), "Directives copied"),
      })
    }
    // Caddy's file is inside its container, where no site on this host reads it.
    if (from !== "caddy") {
      verbs.push({
        key: "path",
        label: "Copy path",
        icon: Copy,
        group: "Copy",
        run: () => void copyText(cert.path, "Path copied"),
      })
    }
    // Only certbot's lineages and the imports are this host's to delete: a
    // file a site names elsewhere is the site's, and Caddy renews its own.
    if (onDelete && (from === "certbot" || from === "imported")) {
      verbs.push({
        key: "delete",
        label: "Delete",
        icon: Trash,
        danger: true,
        disabled: from === "certbot" && busy,
        run: closing(() => onDelete(cert)),
      })
    }
    if (from === "certbot" && onRevoke) {
      verbs.push({
        key: "revoke",
        label: "Revoke and delete",
        icon: Trash,
        danger: true,
        disabled: busy,
        run: closing(() => onRevoke(cert.name)),
      })
    }
    return verbs
  }
  const selectedReplace = selectedCert && replaceVerb(selectedCert)

  return (
    <div className="min-w-0 space-y-4">
      <Toolbar className="justify-between">
        <SearchInput
          ref={search}
          value={query}
          onChange={(event) => setQuery(event.target.value)}
          placeholder="Name, a host it covers, or issuer"
          aria-label="Search certificates (press / to focus)"
        />
        <div className="flex flex-wrap items-center gap-2">
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
          <ToggleGroup
            type="single"
            value={view}
            onValueChange={(v) => v && setView(v as "cards" | "table")}
            variant="outline"
            size="sm"
            aria-label="View"
          >
            <ToggleGroupItem value="cards" aria-label="Cards">
              <GridSquare className="size-3.5" />
            </ToggleGroupItem>
            <ToggleGroupItem value="table" aria-label="Table">
              <TableIcon className="size-3.5" />
            </ToggleGroupItem>
          </ToggleGroup>
          <Button size="sm" variant="outline" onClick={() => setDecoding(true)}>
            <FileText className="size-3.5" />
            Paste a certificate
          </Button>
        </div>
      </Toolbar>
      <div className="flex min-w-0 flex-col gap-2 sm:flex-row sm:flex-wrap sm:gap-3">
        <ChipStrip aria-label="Source">
          <FilterChip selected={source === "all"} onClick={() => setSource("all")}>
            All <ChipCount>{certs.length}</ChipCount>
          </FilterChip>
          {sources.length > 1 &&
            sources.map((s) => (
              <FilterChip
                key={s.key}
                selected={source === s.key}
                onClick={() => setSource(source === s.key ? "all" : s.key)}
              >
                {SOURCE_LABELS[s.key]} <ChipCount>{s.count}</ChipCount>
              </FilterChip>
            ))}
        </ChipStrip>
        {attention > 0 && (
          <ChipStrip aria-label="State">
            <FilterChip
              selected={stateFilter === "attention"}
              onClick={() => toggleState("attention")}
            >
              Needs attention <ChipCount>{attention}</ChipCount>
            </FilterChip>
            {states.length > 1 &&
              states.map((s) => (
                <FilterChip
                  key={s.key}
                  selected={stateFilter === s.key}
                  onClick={() => toggleState(s.key)}
                >
                  {STATE_LABELS[s.key]} <ChipCount>{s.count}</ChipCount>
                </FilterChip>
              ))}
          </ChipStrip>
        )}
      </div>
      <PemDecoder open={decoding} onOpenChange={setDecoding} />
      {ordered.length === 0 ? (
        <EmptyState icon={ShieldCheck} title="No certificates match" />
      ) : view === "table" ? (
        <Table
          aria-label="Installed certificates"
          data-slot="cert-list"
          className="table-fixed"
          containerClassName="rounded-xl border bg-card"
        >
          <TableHeader className={stickyTableHeader}>
            <TableRow>
              <TableHead>Certificate</TableHead>
              <TableHead className="w-28 sm:w-36">Expires</TableHead>
              <TableHead className="w-28 max-sm:hidden">Source</TableHead>
              <TableHead className="w-[30%] max-lg:hidden">Used by</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {ordered.map((cert) => (
              <TableRow
                key={cert.path}
                aria-label={`Inspect ${cert.name}`}
                onActivate={() => select(cert.path)}
              >
                <TableCell className="py-3">
                  <p
                    className="max-w-full min-w-0 truncate text-body font-medium"
                    title={cert.name}
                  >
                    {cert.name}
                  </p>
                  <p
                    className="truncate font-mono text-hint text-muted-foreground"
                    title={cert.domains.join(", ")}
                  >
                    {cert.domains.join(", ") || "No names reported"}
                  </p>
                </TableCell>
                <TableCell>
                  {replacing(cert) ? (
                    <Status state="activating" label="Replacing…" />
                  ) : (
                    <ExpiryStatus cert={cert} />
                  )}
                  {!cert.error && (
                    <p className="text-hint text-muted-foreground">{calendarDate(cert.notAfter)}</p>
                  )}
                </TableCell>
                <TableCell className="max-sm:hidden">
                  <Tag>{SOURCE_LABELS[certSource(cert)]}</Tag>
                </TableCell>
                <TableCell className="max-lg:hidden">
                  <p className="truncate text-hint text-muted-foreground">
                    {cert.usedBy.join(", ") || "no site"}
                  </p>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      ) : (
        <ProxyGrid
          aria-label="Installed certificates"
          data-slot="cert-list"
          className="xl:grid-cols-1 2xl:grid-cols-2"
        >
          {ordered.map((cert) => {
            const verb = replaceVerb(cert)
            const inFlight = replacing(cert)
            return (
              <ChoiceRow
                key={cert.path}
                verb={`Inspect ${cert.name}`}
                onSelect={() => select(cert.path)}
                busy={inFlight}
                className="h-full gap-4 p-4"
                leading={
                  <ProductLogo id={certificateProduct(cert)} size="md" fallback={ShieldCheck} />
                }
                title={<span className="text-title">{cert.name}</span>}
                description={cert.issuer || "Unknown issuer"}
                trailing={
                  inFlight ? (
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
                    <Tag>{SOURCE_LABELS[certSource(cert)]}</Tag>
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
        onOpenChange={(open) => !open && select(null)}
        title={selectedCert?.name ?? "Certificate"}
        description="Certificate details and the sites using it"
        width="lg"
        footer={
          selectedCert && (
            <>
              {busy && selectedCert.source === "certbot" && (
                <CertbotBusy onOpen={onOpenJob} className="mr-auto" />
              )}
              <VerbBar
                verbs={sheetVerbs(selectedCert)}
                menuLabel={`More actions for ${selectedCert.name}`}
                className="justify-end"
              />
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
                  ? `${calendarDate(selectedCert.notAfter)}${
                      selectedCert.expired ? ` · expired ${expiredAgo(selectedCert.notAfter)}` : ""
                    }`
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
