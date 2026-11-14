"use client"

import { useCallback, useState } from "react"
import Link from "next/link"
import { useParams, useRouter, useSearchParams } from "next/navigation"
import { Tabs as TabsPrimitive } from "radix-ui"
import {
  ArrowLeft,
  ChevronDown,
  ChevronUp,
  ShieldCheck,
  ShieldOff,
  Warning,
} from "@/components/icons"
import { expiredAgo } from "@/lib/certificates"
import { calendarDate } from "@/lib/format"
import type { Certificate } from "@/lib/types"
import { useViewState } from "@/lib/view-state"
import { IconAction } from "@/components/icon-action"
import { FactDot, HostIdentity } from "@/components/metrics/host-identity"
import { Detail, DetailList, Page, PageContext, Section } from "@/components/page"
import { ProductLogo } from "@/components/product-logo"
import { EmptyNote, EmptyState, ErrorState, LoadingRows, Notice } from "@/components/state"
import { Status, StatusDot } from "@/components/status-dot"
import { tabClasses } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { Tabs, TabsContent } from "@/components/ui/tabs"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { VerbMenu, type Verb } from "@/components/verbs"
import { Workspace, WorkspaceHelp } from "@/components/workspace/workspace"
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
  certificateHref,
  certificatesHref,
  certificateTab,
  findCertificate,
  type CertificateTab,
} from "@/components/proxy/certificate-paths"
import { CertificatePicture } from "@/components/proxy/certificate-picture"
import { CertificateRail } from "@/components/proxy/certificate-rail"
import { CertificateRun, useCertificateWork } from "@/components/proxy/certificate-work"
import { CertLife, ExpiryStatus, expiryTone } from "@/components/proxy/expiry-status"
import { PrivateTrustNote } from "@/components/proxy/local-ca-panel"
import { certificateProduct } from "@/components/proxy/marks"
import { sitePath } from "@/components/proxy/site-verbs"
import { streamHref } from "@/components/proxy/stream-facts"

const BACK = (
  <Link
    href="/proxy/certificates"
    className="inline-flex items-center gap-1 rounded-sm focus-ring hover:underline"
  >
    <ArrowLeft className="size-3" /> Certificates
  </Link>
)

/**
 * One certificate, as a place of its own.
 *
 * It was a `SidePanel` over the Certificates list until 0.8.7: identity, key,
 * chain, files, timeline, the sites that use it and its export in a sheet's
 * width over a list nobody could read behind it, and a certificate nothing
 * could link to by name — a deployment's "Open the certificate" landed on the
 * list. It is a destination now, in the shape of a process's page
 * (`procs/process-page.tsx`): the way back and its verbs, the identity line,
 * then its views as the underlined strip, with `?tab=` in the address.
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
  const name = decodeURIComponent(params.name ?? "")
  const path = search.get("path")
  const work = useCertificateWork()
  const certs = work.certs
  const cert = findCertificate(certs.data, name, path)

  const requested = search.get("tab")
  const [remembered, remember] = useViewState<string>("proxy.certificate.tab", "overview")
  const [chosen, setChosen] = useState<string>(requested ?? remembered)
  const [seen, setSeen] = useState(requested)
  if (requested !== seen) {
    setSeen(requested)
    if (requested) setChosen(requested)
  }
  const tab = certificateTab(chosen)
  const setTab = useCallback(
    (next: string) => {
      setChosen(next)
      remember(next)
      // Replaced rather than pushed: a view is not a place Back steps through.
      const url = new URL(window.location.href)
      if (next === "overview") url.searchParams.delete("tab")
      else url.searchParams.set("tab", next)
      window.history.replaceState(null, "", `${url.pathname}${url.search}`)
    },
    [remember],
  )

  const filters = useCertificateFilters()
  const rows = certs.data ? filters.apply(certs.data) : []
  const hrefFor = (target: Certificate) => certificateHref(target, certs.data, tab)
  const at = cert ? rows.findIndex((row) => row.path === cert.path) : -1
  const step = (direction: number) => {
    const next = at >= 0 ? rows[at + direction] : rows[direction > 0 ? 0 : rows.length - 1]
    if (next) router.push(hrefFor(next))
  }
  const previous = { disabled: at <= 0, run: () => step(-1) }
  const next = { disabled: rows.length === 0 || at >= rows.length - 1, run: () => step(1) }

  return (
    <Workspace
      name="Certificate"
      stateKey="proxy.certificate.page"
      refresh={certs.refresh}
      commands={[
        {
          id: "previous",
          label: "Previous certificate",
          keys: "Alt+↑",
          chord: "Alt+ArrowUp",
          disabled: previous.disabled,
          run: previous.run,
        },
        {
          id: "next",
          label: "Next certificate",
          keys: "Alt+↓",
          chord: "Alt+ArrowDown",
          disabled: next.disabled,
          run: next.run,
        },
      ]}
    >
      {/* Held to the window where the rail stands beside it, so the list and
          the identity line stay put while a view scrolls; narrower, the page
          is the one pane and scrolls as a whole. */}
      <Page fill="xl" className="gap-0 md:gap-0">
        <div className="flex min-w-0 flex-1 gap-6 xl:min-h-0">
          <CertificateRail
            className="hidden w-72 shrink-0 xl:flex"
            certs={certs.data}
            rows={rows}
            current={cert}
            loading={certs.loading}
            filters={filters}
            hrefFor={hrefFor}
          />
          <CertificateMain
            key={cert?.path ?? name}
            name={name}
            cert={cert}
            tab={tab}
            setTab={setTab}
            previous={previous}
            next={next}
          />
        </div>
      </Page>
    </Workspace>
  )
}

type Step = { disabled: boolean; run: () => void }

function CertificateMain({
  name,
  cert,
  tab,
  setTab,
  previous,
  next,
}: {
  name: string
  cert: Certificate | undefined
  tab: CertificateTab
  setTab: (tab: string) => void
  previous: Step
  next: Step
}) {
  const work = useCertificateWork()
  const router = useRouter()
  const { admin, certs } = work
  const detail = useCertificateDetail(cert)
  const vhosts = useProxyRead("vhosts")

  const steps = (
    <span className="flex items-center">
      <IconAction label="Previous certificate" disabled={previous.disabled} onClick={previous.run}>
        <ChevronUp />
      </IconAction>
      <IconAction label="Next certificate" disabled={next.disabled} onClick={next.run}>
        <ChevronDown />
      </IconAction>
      <span className="max-sm:hidden">
        <WorkspaceHelp compact />
      </span>
    </span>
  )

  if (!cert && certs.data) {
    return (
      <div className="flex min-w-0 flex-1 flex-col gap-5">
        <PageContext eyebrow={BACK} title={name} />
        <EmptyState
          icon={ShieldOff}
          title={`No certificate called ${name}`}
          description="It may have been deleted or renewed under another name. The list has every certificate on this server."
          action={
            <Button size="sm" variant="outline" asChild>
              <Link href={certificatesHref()}>All certificates</Link>
            </Button>
          }
        />
      </div>
    )
  }
  if (!cert && certs.error) {
    return (
      <div className="flex min-w-0 flex-1 flex-col gap-5">
        <PageContext eyebrow={BACK} title={name} />
        <ErrorState error={certs.error} onRetry={certs.refresh} />
      </div>
    )
  }

  const readable = Boolean(cert) && cert?.source !== "caddy" && !cert?.error
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
  const verbs = cert
    ? work.verbsFor(cert, { onDeleted: () => router.push(certificatesHref()) })
    : []
  const busy = Boolean(cert && work.certbotBusy && cert.source === "certbot" && admin)

  return (
    <div className="flex min-h-0 min-w-0 flex-1 flex-col gap-5">
      <div className="flex min-w-0 shrink-0 flex-col gap-5">
        <PageContext
          eyebrow={BACK}
          title={cert?.name ?? name}
          actions={
            <>
              {cert && <HeaderVerbs name={cert.name} verbs={verbs} />}
              {steps}
            </>
          }
        />
        {cert ? (
          <CertificateIdentity cert={cert} replacing={work.replacing(cert)} />
        ) : (
          <HostIdentity
            className="border-b-0 pb-0"
            fallback={ShieldCheck}
            title={name}
            facts={<TextShimmer>Reading the certificate…</TextShimmer>}
          />
        )}
        {busy && <CertbotBusy onOpen={work.openCertbotJob} />}
        <CertificateRun />
      </div>

      {!cert ? (
        <>
          <Skeleton className="h-10 w-full" />
          <LoadingRows />
        </>
      ) : (
        <Tabs value={shown} onValueChange={setTab} className="min-h-0 flex-1 gap-0">
          <TabsPrimitive.List
            aria-label={`Views of ${cert.name}`}
            className="flex shrink-0 [scrollbar-width:none] gap-1 overflow-x-auto border-b border-hairline [&::-webkit-scrollbar]:hidden"
          >
            {views.map((view) => (
              <TabsPrimitive.Trigger
                key={view.key}
                value={view.key}
                className={tabClasses(shown === view.key, "h-10")}
              >
                {view.label}
              </TabsPrimitive.Trigger>
            ))}
          </TabsPrimitive.List>

          <TabsContent
            value="overview"
            className="min-h-0 flex-1 space-y-8 pt-6 xl:overflow-y-auto"
          >
            <CertificateOverview
              cert={cert}
              detail={detail}
              vhosts={vhosts.data}
              replace={verbs.find((verb) => verb.key === "replace")}
            />
          </TabsContent>
          <TabsContent value="key" className="min-h-0 flex-1 pt-6 xl:overflow-y-auto">
            {shown === "key" && (
              <DetailView detail={detail}>
                {(data) =>
                  data.key ? (
                    <KeyFacts keyInfo={data.key} />
                  ) : (
                    <EmptyNote>
                      No site names a key for this file and none is kept beside it.
                    </EmptyNote>
                  )
                }
              </DetailView>
            )}
          </TabsContent>
          <TabsContent value="chain" className="min-h-0 flex-1 pt-6 xl:overflow-y-auto">
            {shown === "chain" && (
              <DetailView detail={detail}>{(data) => <ChainView chain={data.chain} />}</DetailView>
            )}
          </TabsContent>
          <TabsContent value="files" className="min-h-0 flex-1 pt-6 xl:overflow-y-auto">
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
          <TabsContent value="timeline" className="min-h-0 flex-1 pt-6 xl:overflow-y-auto">
            {shown === "timeline" && admin && <CertificateTimeline name={cert.name} />}
          </TabsContent>
        </Tabs>
      )}
    </div>
  )
}

/**
 * The certificate's verbs: its one command on the brand's face — the real
 * issuance for a test certificate, a renewal for certbot's, a new pair for an
 * import — and everything else behind the menu.
 */
function HeaderVerbs({ name, verbs }: { name: string; verbs: Verb[] }) {
  const primary = verbs.find((verb) => verb.inline)
  const rest = verbs.filter((verb) => verb !== primary)
  return (
    <div className="flex items-center gap-2">
      {primary && (
        // certbot's word for a renewal names the day it falls due, which is
        // the button's title here rather than a sentence on its face.
        <Button
          size="sm"
          disabled={primary.disabled}
          onClick={primary.run}
          title={primary.key === "renew" ? primary.label : undefined}
        >
          <primary.icon className="size-3.5" />
          {primary.key === "renew" ? "Renew" : primary.label}
        </Button>
      )}
      {rest.length > 0 && <VerbMenu verbs={rest} label={`More actions for ${name}`} />}
    </div>
  )
}

/**
 * The line the page opens on: the issuer's mark with the certificate's state
 * in its corner, its name, and the facts a certificate is opened to check —
 * who signed it, where it came from, when it expires and who serves it — with
 * how long it has left at the line's end.
 */
function CertificateIdentity({ cert, replacing }: { cert: Certificate; replacing: boolean }) {
  const tone = expiryTone(cert)
  return (
    <HostIdentity
      className="animate-rise border-b-0 pb-0"
      logo={
        <span className="relative flex shrink-0">
          <ProductLogo
            id={certificateProduct(cert)}
            fallback={ShieldCheck}
            className="size-12 rounded-xl [&_img]:size-7"
          />
          <span
            aria-hidden
            className="absolute -right-1 -bottom-1 flex size-4 items-center justify-center rounded-full bg-background"
          >
            <StatusDot
              tone={tone === "danger" ? "danger" : tone === "warning" ? "warning" : "running"}
            />
          </span>
        </span>
      }
      title={cert.name}
      facts={
        <>
          <span className="truncate" title={cert.issuer}>
            {cert.selfSigned ? "self-signed" : cert.issuer || "unknown issuer"}
          </span>
          <FactDot />
          <span>{cert.source === "caddy" ? "renewed by Caddy" : originWord(cert)}</span>
          {!cert.error && cert.notAfter && (
            <>
              <FactDot />
              <span className="numeric">
                {cert.expired ? "expired" : "expires"} {calendarDate(cert.notAfter)}
              </span>
            </>
          )}
          <FactDot />
          <span>{usedByWord(cert)}</span>
        </>
      }
      aside={
        replacing ? (
          <Status state="activating" label="Replacing…" className="text-body" />
        ) : (
          <span className="text-body">
            <ExpiryStatus cert={cert} />
          </span>
        )
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
        <DetailList>
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
