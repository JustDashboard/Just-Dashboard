"use client"

import Link from "next/link"
import { useParams, useRouter, useSearchParams } from "next/navigation"
import { ShieldCheck, ShieldOff, Warning } from "@/components/icons"
import { certState, certificateTone, expiredAgo } from "@/lib/certificates"
import { calendarDate } from "@/lib/format"
import type { Certificate } from "@/lib/types"
import { DetailPage, siblingSteps, useDetailTab, type Step } from "@/components/detail-page"
import { Fact } from "@/components/fact-line"
import { IdentityHeader, IdentityVerbs } from "@/components/identity-header"
import { Detail, DetailList } from "@/components/detail-list"
import { Section } from "@/components/section"
import { ProductLogo } from "@/components/product-logo"
import { EmptyNote, EmptyState, ErrorState, Notice } from "@/components/state"
import { LoadingRows } from "@/components/loading"
import { Status, StatusDot } from "@/components/status-dot"
import { VERDICT_TONE } from "@/components/tone"
import { ViewTabList } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { Tabs, TabsContent } from "@/components/ui/tabs"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { VerbMenu, type Verb } from "@/components/verbs"
import { useProxyRead } from "@/components/proxy/proxy-context"
import {
  CertificateFiles,
  CertificateTimeline,
  ChainView,
  IdentityFacts,
  KeyFacts,
  useCertificateDetail,
} from "@/components/proxy/cert-detail"
import { CertbotBusy } from "@/components/proxy/certbot-panel"
import {
  originWord,
  usedByWord,
  useCertificateFilters,
} from "@/components/proxy/certificate-inventory"
import {
  CERTIFICATE_TABS,
  certificateHref,
  certificatesHref,
  findCertificate,
  type CertificateTab,
} from "@/components/proxy/certificate-paths"
import { CertificatePicture } from "@/components/proxy/certificate-picture"
import { CertificateRail } from "@/components/proxy/certificate-rail"
import { CertificateRun, useCertificateWork } from "@/components/proxy/certificate-work"
import { CertLife, ExpiryStatus } from "@/components/proxy/expiry-status"
import { PrivateTrustNote } from "@/components/proxy/local-ca-panel"
import { certificateProduct } from "@/components/proxy/marks"
import { sitePath } from "@/components/proxy/site-verbs"
import { streamHref } from "@/components/proxy/stream-facts"
import { decodeSegment } from "@/lib/route-params"
import { cn } from "@/lib/utils"

const BACK = { href: "/proxy/certificates", label: "Certificates" }

/**
 * One certificate, as a place of its own.
 *
 * It was a `SidePanel` over the Certificates list until 0.8.7: identity, key,
 * chain, files, timeline, the sites that use it and its export in a sheet's
 * width over a list nobody could read behind it, and a certificate nothing
 * could link to by name — a deployment's "Open the certificate" landed on the
 * list. It is a destination now, on the detail frame every item page shares
 * (`DetailPage`): the way back with the arrows, the identity line with the
 * certificate's verbs on it (`IdentityVerbs`), then its views as the
 * underlined strip, with `?tab=` in the address.
 *
 * What the sheet did well — the list beside the thing being read — is the
 * column on the left from `xl` (`CertificateRail`): the list's own search,
 * source and order, the one on screen filled, Alt+↑ and Alt+↓ to walk it, the
 * view on screen carried to the next. On a phone the page is one pane that
 * scrolls as a whole.
 */
export function CertificatePage() {
  const params = useParams<{ name: string }>()
  const search = useSearchParams()
  const router = useRouter()
  const name = decodeSegment(params.name)
  const path = search.get("path")
  const work = useCertificateWork()
  const certs = work.certs
  const cert = findCertificate(certs.data, name, path)

  // The view is kept in the address and remembered for the visit, as a
  // site's, a stream's and a port's are.
  const [tab, setTab] = useDetailTab(CERTIFICATE_TABS, { remember: "proxy.certificate.tab" })

  const filters = useCertificateFilters()
  const rows = certs.data ? filters.apply(certs.data) : []
  const hrefFor = (target: Certificate) => certificateHref(target, certs.data, tab)
  const steps = siblingSteps(
    rows.map((row) => row.path),
    cert?.path,
    (key) => {
      const next = rows.find((row) => row.path === key)
      if (next) router.push(hrefFor(next))
    },
  )

  return (
    <CertificateMain
      key={cert?.path ?? name}
      name={name}
      cert={cert}
      tab={tab}
      setTab={setTab}
      steps={steps}
      rail={
        <CertificateRail
          certs={certs.data}
          rows={rows}
          current={cert}
          loading={certs.loading}
          error={certs.error}
          onRetry={certs.refresh}
          filters={filters}
          hrefFor={hrefFor}
        />
      }
    />
  )
}

function CertificateMain({
  name,
  cert,
  tab,
  setTab,
  steps,
  rail,
}: {
  name: string
  cert: Certificate | undefined
  tab: CertificateTab
  setTab: (tab: string) => void
  steps: { previous: Step; next: Step }
  rail: React.ReactNode
}) {
  const work = useCertificateWork()
  const router = useRouter()
  const { admin, certs } = work
  const detail = useCertificateDetail(cert)
  const vhosts = useProxyRead("vhosts")

  // The frame every detail page has (D9): the way back with the arrows, the
  // rail from `xl`, and the identity line held still over the views.
  const frame = (identity: React.ReactNode, body: React.ReactNode) => (
    <DetailPage
      name="Certificate"
      what="certificate"
      stateKey="proxy.certificate.page"
      refresh={certs.refresh}
      steps={steps}
      rail={rail}
      back={BACK}
      title={cert?.name ?? name}
      identity={identity}
    >
      {body}
    </DetailPage>
  )

  if (!cert && certs.data) {
    return frame(
      undefined,
      <EmptyState
        icon={ShieldOff}
        title={`No certificate called ${name}`}
        description="It may have been deleted or renewed under another name. The list has every certificate on this server."
        action={
          <Button size="sm" variant="outline" asChild>
            <Link href={certificatesHref()}>All certificates</Link>
          </Button>
        }
      />,
    )
  }
  if (!cert && certs.error) {
    return frame(undefined, <ErrorState error={certs.error} onRetry={certs.refresh} />)
  }
  if (!cert) {
    // The line keeps a place for its state and its verbs, as the loaded line
    // has them, so it is as tall on a phone as the line it becomes.
    return frame(
      <IdentityHeader
        loading
        className="border-b-0 pb-0"
        fallback={ShieldCheck}
        title={name}
        facts={<TextShimmer>Reading the certificate…</TextShimmer>}
        state
        command
        menu={false}
      />,
      <>
        <Skeleton className="h-10 w-full" />
        <LoadingRows />
      </>,
    )
  }

  const readable = cert.source !== "caddy" && !cert.error
  const views: { key: CertificateTab; label: string }[] = [
    { key: "overview", label: "Overview" },
    ...(readable
      ? [
          { key: "key" as const, label: "Key" },
          { key: "chain" as const, label: "Chain" },
          { key: "files" as const, label: "Files" },
        ]
      : []),
    ...(admin ? [{ key: "timeline" as const, label: "Timeline" }] : []),
  ]
  const shown = views.some((view) => view.key === tab) ? tab : "overview"
  const verbs = work.verbsFor(cert, { onDeleted: () => router.push(certificatesHref()) })
  const busy = Boolean(work.certbotBusy && cert.source === "certbot" && admin)

  return frame(
    <>
      <CertificateIdentity cert={cert} verbs={verbs} replacing={work.replacing(cert)} />
      {busy && <CertbotBusy onOpen={work.openCertbotJob} />}
      <CertificateRun />
    </>,
    <Tabs value={shown} onValueChange={setTab} className="gap-0">
      <ViewTabList
        label={`Views of ${cert.name}`}
        value={shown}
        views={views}
        sticky
      />

      <TabsContent value="overview" className="space-y-8 pt-6">
        <CertificateOverview
          cert={cert}
          detail={detail}
          vhosts={vhosts.data}
          replace={verbs.find((verb) => verb.key === "replace")}
        />
      </TabsContent>
      <TabsContent value="key" className="pt-6">
        {shown === "key" && (
          <DetailView detail={detail}>
            {(data) =>
              data.key ? (
                <KeyFacts keyInfo={data.key} />
              ) : (
                <EmptyNote>No site names a key for this file and none is kept beside it.</EmptyNote>
              )
            }
          </DetailView>
        )}
      </TabsContent>
      <TabsContent value="chain" className="pt-6">
        {shown === "chain" && (
          <DetailView detail={detail}>{(data) => <ChainView chain={data.chain} />}</DetailView>
        )}
      </TabsContent>
      <TabsContent value="files" className="pt-6">
        {shown === "files" && (
          <DetailView detail={detail}>
            {(data) => (
              <CertificateFiles
                cert={cert}
                hasChain={data.chain.certificates.length > 1}
                canExport={admin && data.key?.matches === true}
              />
            )}
          </DetailView>
        )}
      </TabsContent>
      <TabsContent value="timeline" className="pt-6">
        {shown === "timeline" && admin && <CertificateTimeline name={cert.name} />}
      </TabsContent>
    </Tabs>,
  )
}

/**
 * The certificate's verbs on its identity line, by the one rule
 * (`IdentityVerbs`): its one command on the brand's face — the real issuance
 * for a test certificate, a renewal for certbot's, a new pair for an import —
 * and everything else behind the menu. A certificate with no command (one
 * Caddy renews, or a reader's) keeps only the menu. certbot's word for a
 * renewal names the day it falls due, which the facts already say, so the
 * command reads "Renew".
 */
function lineVerbs(name: string, verbs: Verb[]) {
  const primary = verbs.find((verb) => verb.inline)
  const rest = verbs.filter((verb) => verb !== primary)
  if (!primary)
    return {
      command: undefined,
      menu: rest.length > 0 && <VerbMenu verbs={rest} label={`More actions for ${name}`} />,
    }
  const lead = primary.key === "renew" ? { ...primary, label: "Renew" } : primary
  return IdentityVerbs({ verbs: [lead, ...rest], name })
}

/**
 * The line the page opens on: the issuer's mark with the certificate's state
 * in its corner, its name with how long it has left beside it, its verbs at
 * the far end, and the facts a certificate is opened to check — who signed
 * it, where it came from, when it expires and who serves it. A fact that
 * wants action leads the line (an expiry inside the renewal window or past
 * it), so a phone, which shows one line of facts, still shows it.
 */
function CertificateIdentity({
  cert,
  verbs,
  replacing,
}: {
  cert: Certificate
  verbs: Verb[]
  replacing: boolean
}) {
  const state = certState(cert)
  const expiry = !cert.error && cert.notAfter && (
    <Fact
      key="expiry"
      className={cn(
        "numeric",
        state === "expired" ? "text-destructive" : state === "expiring" && "text-warning",
      )}
    >
      {cert.expired ? "expired" : "expires"} {calendarDate(cert.notAfter)}
    </Fact>
  )
  const urgent = state === "expired" || state === "expiring"
  return (
    <IdentityHeader
      className="border-b-0 pb-0"
      logo={
        <span className="relative flex shrink-0">
          <ProductLogo
            id={certificateProduct(cert)}
            fallback={ShieldCheck}
            className="size-10 rounded-lg [&_img]:size-6 [&>svg]:size-5"
          />
          <StatusDot corner="lg" tone={VERDICT_TONE[certificateTone(cert)]} />
        </span>
      }
      title={<span className="truncate">{cert.name}</span>}
      state={
        replacing ? (
          <Status state="activating" label="Replacing…" />
        ) : (
          <ExpiryStatus cert={cert} />
        )
      }
      {...lineVerbs(cert.name, verbs)}
      facts={
        <>
          {urgent && expiry}
          <Fact title={cert.issuer}>
            {cert.selfSigned ? "self-signed" : cert.issuer || "unknown issuer"}
          </Fact>
          <Fact>{cert.source === "caddy" ? "renewed by Caddy" : originWord(cert)}</Fact>
          {!urgent && expiry}
          <Fact>{usedByWord(cert)}</Fact>
        </>
      }
    />
  )
}

type DetailRead = ReturnType<typeof useCertificateDetail>

/** A view read from the certificate's file: drawn once the file has been read. */
function DetailView({
  detail,
  children,
}: {
  detail: DetailRead
  children: (data: NonNullable<DetailRead["data"]>) => React.ReactNode
}) {
  if (detail.loading && !detail.data) return <LoadingRows rows={4} />
  if (detail.error) return <ErrorState error={detail.error} onRetry={detail.refresh} />
  if (!detail.data || !detail.leaf) return null
  return <>{children(detail.data)}</>
}

/**
 * What the certificate is: what refuses it, if anything does, the picture of
 * who signed it and who serves it, its facts, and its identity read from the
 * file.
 */
function CertificateOverview({
  cert,
  detail,
  vhosts,
  replace,
}: {
  cert: Certificate
  detail: DetailRead
  vhosts?: Parameters<typeof CertificatePicture>[0]["vhosts"]
  replace?: Verb
}) {
  const users = [
    ...cert.usedBy.map((site) => ({ key: `site:${site}`, href: sitePath(site), label: site })),
    ...(cert.usedByStreams ?? []).map((stream) => ({
      key: `stream:${stream}`,
      href: streamHref(stream),
      label: `stream ${stream}`,
    })),
  ]
  return (
    <>
      {cert.error && (
        <Notice tone="danger" title="Unreadable certificate" className="break-all">
          {cert.error}
        </Notice>
      )}
      {cert.staging && (
        <Notice tone="danger" icon={Warning} title="A test certificate">
          <div className="flex flex-wrap items-center gap-2">
            <span>
              A staging authority signed it, so browsers refuse it.
              {replace &&
                (cert.source === "certbot"
                  ? " A real issuance for the same names replaces it."
                  : " certbot saves the real one in its own directory: point the site at it.")}
            </span>
            {replace && (
              <Button size="xs" variant="outline" disabled={replace.disabled} onClick={replace.run}>
                {replace.label}
              </Button>
            )}
          </div>
        </Notice>
      )}
      <PrivateTrustNote cert={cert} />

      <CertificatePicture cert={cert} vhosts={vhosts} />

      <Section title="Facts">
        <DetailList labelWidth="fixed">
          <Detail label="Names">
            <span className="font-mono break-all">{cert.domains.join(", ") || "—"}</span>
          </Detail>
          <Detail label="Issued">
            {!cert.error && cert.notBefore ? calendarDate(cert.notBefore) : "—"}
          </Detail>
          <Detail label="Expires">
            {!cert.error && cert.notAfter ? (
              <span className="flex flex-wrap items-center gap-3">
                <span>
                  {calendarDate(cert.notAfter)}
                  {cert.expired ? ` · expired ${expiredAgo(cert.notAfter)}` : ""}
                </span>
                <CertLife cert={cert} className="w-32" />
              </span>
            ) : (
              "—"
            )}
          </Detail>
          <Detail label="Source">
            {cert.source === "caddy" ? "Caddy, which renews it itself" : cert.source}
          </Detail>
          <Detail label="Used by">
            {users.length > 0 ? (
              <span className="flex flex-col gap-1">
                {users.map((user) => (
                  <Link
                    key={user.key}
                    href={user.href}
                    className="break-all text-brand hover:underline"
                  >
                    {user.label}
                  </Link>
                ))}
              </span>
            ) : (
              "nothing"
            )}
          </Detail>
          <Detail label={cert.source === "caddy" ? "File in the Caddy container" : "File"}>
            <span className="font-mono text-hint break-all">{cert.path}</span>
          </Detail>
          {cert.evidence?.length ? (
            <Detail label="Release copies">
              <span className="flex flex-col gap-2">
                {cert.evidence.map((copy) => (
                  <span key={copy.name} className="min-w-0">
                    <span className="font-mono text-hint break-all">{copy.path}</span>
                    <span className="block text-hint text-muted-foreground">
                      kept when a deployment was activated · valid until{" "}
                      {calendarDate(copy.notAfter)}
                    </span>
                  </span>
                ))}
              </span>
            </Detail>
          ) : null}
          <Detail label="Self-signed">{cert.error ? "—" : cert.selfSigned ? "yes" : "no"}</Detail>
        </DetailList>
      </Section>

      {detail.readable && (
        <Section title="Identity">
          <DetailView detail={detail}>
            {() => (detail.leaf ? <IdentityFacts facts={detail.leaf} /> : null)}
          </DetailView>
        </Section>
      )}
    </>
  )
}
