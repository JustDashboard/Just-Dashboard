"use client"

import { useState } from "react"
import Link from "next/link"
import { Globe, RefreshClockwise, Warning } from "@/components/icons"
import { ApiError } from "@/lib/api"
import {
  HANDLE_MAX,
  certificateReading,
  handleChecks,
  handleValid,
  normalizeHandle,
  refusalWords,
  zoneWords,
} from "@/lib/app-domain"
import { plural } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { AccountLinkStatus, AppDomainOffer, AppDomainStatus, AppDomainUse } from "@/lib/types"
import type { AppDomainControls } from "@/hooks/use-app-domain"
import { ACTIVITY_ROW, MarkTile } from "@/components/account-link/marks"
import { useConfirm } from "@/components/confirm-dialog"
import { Detail, DetailList } from "@/components/detail-list"
import { Field, FieldCheck, FieldChecks, FormNote, OptionList, OptionRow } from "@/components/form"
import { Row, RowList } from "@/components/row-list"
import { Section } from "@/components/section"
import { EmptyNote, Notice } from "@/components/state"
import { Status, StatusDot } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import {
  InputGroup,
  InputGroupAddon,
  InputGroupInput,
  InputGroupText,
} from "@/components/ui/input-group"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { CopyButton } from "@/components/copy"

/** Where a deployment's names are changed, in the environment that carries this one. */
export function domainsHref(use: AppDomainUse) {
  return `/deploy/${use.projectId}/settings/domains?env=${encodeURIComponent(use.environment)}`
}

/**
 * Settings → Connect's "just-dashboard.com address": the name this server holds under the
 * site's zone, through which every deployed app can answer at `<app>.<handle>.<zone>` with one
 * wildcard record and one wildcard certificate (docs/internal/backend/app-domains.md).
 *
 * One section under a hairline like the page's others, in each state the backend reports: what
 * it is while the server is not linked (the link's own action is the page's, above), the site's
 * reason when it does not offer addresses, the handle to claim, and the address once it is held,
 * with its certificate, the default for new deployments, the deployments using it and Release.
 * A suspended or detached address says why and what to do, and lists the deployments it leaves
 * without a working name.
 *
 * Claiming publishes this server's IP address in public DNS, so it is a person's act: the page
 * says so before the button, and the backend refuses it to an API key and to the assistant.
 */
export function AppDomainSection({
  link,
  site,
  domain,
  className,
}: {
  link: AccountLinkStatus
  /** The site's host, for sentences: "just-dashboard.com". */
  site: string
  domain: AppDomainControls
  className?: string
}) {
  const { status } = domain
  const title = `${status?.zone ?? site} address`

  if (domain.missing) return null
  if (!status) {
    if (!domain.error) return null
    return (
      <Section title={title} className={className}>
        <Notice tone="warning" icon={Warning} title="Could not read this server's address">
          {domain.error.message}
        </Notice>
      </Section>
    )
  }

  if (!status.enabled) {
    return (
      <Section title={title} className={className}>
        <p className="text-body text-muted-foreground">
          Turned off by <code className="font-mono">JD_APP_DOMAINS=false</code> in this
          server&apos;s <code className="font-mono">.env</code>.
        </p>
      </Section>
    )
  }

  const held = status.domain
  const linked = link.state === "linked" && status.linked

  return (
    <Section
      title={title}
      className={className}
      actions={
        held &&
        held.status !== "detached" &&
        linked && <ReleaseButton status={status} held={held} domain={domain} />
      }
    >
      <div id="app-domain" className="flex min-w-0 scroll-mt-24 flex-col gap-5">
        {held?.status === "suspended" && (
          <Notice tone="danger" icon={Warning} title={`${held.hostname} is suspended`}>
            <p>
              {sentence(held.reason)}Its DNS records are removed, so apps under it do not resolve.
              Only a {site} administrator can lift the suspension: write to them from your account
              on {site}.
            </p>
            <InUse uses={status.usedBy} />
          </Notice>
        )}
        {held?.status === "detached" && (
          <Notice
            tone="warning"
            icon={Warning}
            title={`${held.hostname} is no longer this server's`}
          >
            <p>
              {sentence(held.reason)}Apps under it do not resolve.{" "}
              {linked
                ? "Claim it again below, or give those deployments another address."
                : "Link this server again and claim it, or give those deployments another address."}
            </p>
            <InUse uses={status.usedBy} />
          </Notice>
        )}

        {!linked ? (
          <p className="max-w-3xl text-body text-muted-foreground">
            A linked server can hold an address under {zoneWords(status)}, so each app it deploys
            answers at a name like{" "}
            <span className="font-mono text-foreground">shop.acme.{zoneWords(status)}</span> over
            HTTPS, with no DNS record to create.{" "}
            {link.state === "pending"
              ? "Approve the link request above first."
              : "Link this server above to claim one."}
          </p>
        ) : held?.status === "active" ? (
          <Active status={status} held={held} domain={domain} />
        ) : held?.status === "suspended" ? null : status.offer && !held ? (
          <Offered status={status} offer={status.offer} domain={domain} />
        ) : status.available === false ? (
          <Notice icon={Globe} title={`${site} is not offering addresses`}>
            {status.reason ?? "The site has not set up the DNS it needs for them."}
          </Notice>
        ) : status.available === null ? (
          <Notice tone="warning" icon={Warning} title={`Could not ask ${site}`}>
            {status.reason ?? "The site did not answer."} The page asks again when it is opened.
          </Notice>
        ) : (
          <Claim status={status} domain={domain} initial={held?.handle} />
        )}

        {linked && held?.status === "active" && status.available === null && (
          <FormNote tone="warning">
            Could not reach {site}: {status.reason ?? "it did not answer"}. Apps keep working on the
            records and the certificate already in place; claiming, releasing and issuing wait until
            it answers.
          </FormNote>
        )}
      </div>
    </Section>
  )
}

/**
 * The address the account chose for this server when it approved the link, which the server
 * claims by itself: under way, it is one line led by a live dot while the page asks every
 * three seconds; failed, it is the reason and the ordinary claim form, filled in with that handle.
 */
function Offered({
  status,
  offer,
  domain,
}: {
  status: AppDomainStatus
  offer: AppDomainOffer
  domain: AppDomainControls
}) {
  if (!offer.error) {
    return (
      <p role="status" className="flex max-w-3xl min-w-0 items-start gap-2 text-body">
        <StatusDot tone="notice" live className="mt-1.5" />
        <span className="min-w-0 text-muted-foreground">
          Claiming <span className="font-mono break-all text-foreground">{offer.hostname}</span>,
          the address chosen when this server&apos;s link was approved…
        </span>
      </p>
    )
  }
  return (
    <>
      <Notice tone="danger" icon={Warning} title={`Could not claim ${offer.hostname}`}>
        {offer.error}
      </Notice>
      <Claim key={offer.handle} status={status} domain={domain} initial={offer.handle} />
    </>
  )
}

/** A reason the site wrote, as the first sentence of ours. */
function sentence(reason: string | null) {
  return reason ? `${reason.trim().replace(/\.$/, "")}. ` : ""
}

/** The deployments a name stops working for, each a way to its Domains settings. */
function InUse({ uses }: { uses: AppDomainUse[] }) {
  if (uses.length === 0) return null
  return (
    <ul aria-label="Deployments using the address" className="mt-1.5 space-y-0.5">
      {uses.map((use) => (
        <li key={`${use.environmentId}-${use.hostname}`} className="min-w-0 truncate">
          <Link
            href={domainsHref(use)}
            className="font-medium text-foreground underline underline-offset-2"
          >
            {use.project}
          </Link>{" "}
          · {use.environment} · <span className="font-mono">{use.hostname}</span>
        </li>
      ))}
    </ul>
  )
}

function Claim({
  status,
  domain,
  initial,
}: {
  status: AppDomainStatus
  domain: AppDomainControls
  /** A detached handle, to claim again. */
  initial?: string
}) {
  const [value, setValue] = useState(initial ?? "")
  const [busy, setBusy] = useState(false)
  const [refusal, setRefusal] = useState<{ words: string; inUse: boolean }>()
  const handle = normalizeHandle(value)
  const zone = zoneWords(status)
  const valid = handleValid(handle)

  const claim = async () => {
    if (!valid) return
    setBusy(true)
    setRefusal(undefined)
    try {
      await domain.claim(handle)
      notify.success(`${handle}.${zone} is this server's`)
    } catch (err) {
      setRefusal({
        words: refusalWords(err, status),
        inUse: err instanceof ApiError && err.code === "domain_in_use",
      })
    } finally {
      setBusy(false)
    }
  }

  return (
    <form
      className="flex max-w-xl min-w-0 flex-col gap-4"
      onSubmit={(event) => {
        event.preventDefault()
        void claim()
      }}
    >
      <Field
        label="Handle"
        htmlFor="app-domain-handle"
        hint={
          <FieldChecks>
            {handleChecks(handle).map((check) => (
              <FieldCheck key={check.key} met={check.met}>
                {check.label}
              </FieldCheck>
            ))}
          </FieldChecks>
        }
      >
        <InputGroup>
          <InputGroupInput
            id="app-domain-handle"
            value={value}
            maxLength={HANDLE_MAX + 10}
            autoComplete="off"
            spellCheck={false}
            placeholder="acme"
            className="font-mono"
            onChange={(event) => {
              setValue(event.target.value.toLowerCase())
              setRefusal(undefined)
            }}
          />
          <InputGroupAddon align="inline-end">
            <InputGroupText className="font-mono">.{zone}</InputGroupText>
          </InputGroupAddon>
        </InputGroup>
      </Field>
      <p className="min-w-0 text-body text-muted-foreground">
        Apps answer at{" "}
        <span data-testid="app-domain-preview" className="font-mono break-all text-foreground">
          <span className="text-faint">app</span>.
          {handle || <span className="text-faint">handle</span>}.{zone}
        </span>
      </p>
      <FormNote>
        Claiming publishes this server&apos;s IP address
        {status.publicAddress ? (
          <>
            , <span className="font-mono text-foreground">{status.publicAddress}</span>,
          </>
        ) : null}{" "}
        in public DNS.
      </FormNote>
      <div>
        <Button type="submit" disabled={!valid} pending={busy}>
          Claim
        </Button>
      </div>
      {refusal && (
        <div role="alert">
          <Notice tone="danger" icon={Warning} title="Could not claim the address">
            <p>{refusal.words}</p>
            {refusal.inUse && <InUse uses={status.usedBy} />}
          </Notice>
        </div>
      )}
    </form>
  )
}

type Held = NonNullable<AppDomainStatus["domain"]>

function Active({
  status,
  held,
  domain,
}: {
  status: AppDomainStatus
  held: Held
  domain: AppDomainControls
}) {
  const reading = certificateReading(status.certificate)
  const [issuing, setIssuing] = useState(false)
  const [assigning, setAssigning] = useState(false)
  const moving = status.publicAddress && status.publicAddress !== held.address

  const issue = async () => {
    setIssuing(true)
    try {
      await domain.issue()
    } catch (err) {
      notify.error("Could not issue the certificate", refusalWords(err, status))
    } finally {
      setIssuing(false)
    }
  }

  const assign = async (next: boolean) => {
    setAssigning(true)
    try {
      await domain.setAutoAssign(next)
      notify.success(
        next ? "New deployments get this address" : "New deployments keep their usual address",
      )
    } catch (err) {
      notify.error("Could not change the default", refusalWords(err, status))
    } finally {
      setAssigning(false)
    }
  }

  return (
    <>
      <DetailList ruled labelWidth="fixed" size="body">
        <Detail
          label="Address"
          mono
          after={<CopyButton label="Copy the address" text={held.hostname} />}
        >
          {held.hostname}
        </Detail>
        <Detail label="Apps" mono>
          <span className="text-faint">app</span>.{held.hostname}
        </Detail>
        <Detail label="Points at" mono>
          {held.address}
          {moving && (
            <span className="ml-2 font-sans text-hint text-warning">
              moving to {status.publicAddress} within 15 minutes
            </span>
          )}
        </Detail>
        <Detail label="Certificate" className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1">
          <Status
            tone={reading.tone}
            label={reading.issuing ? <TextShimmer>{reading.label}</TextShimmer> : reading.label}
          />
          <span className="font-mono text-hint text-muted-foreground">{held.wildcard}</span>
          {reading.action && (
            <Button
              size="xs"
              variant="outline"
              className="ml-auto"
              onClick={() => void issue()}
              pending={issuing}
              aria-label={`${reading.action} the certificate for ${held.wildcard}`}
            >
              <RefreshClockwise />
              {reading.action}
            </Button>
          )}
          {status.certificate?.state === "failed" && status.certificate.error && (
            <span role="alert" className="basis-full text-hint text-destructive">
              {status.certificate.error}
            </span>
          )}
        </Detail>
      </DetailList>

      <OptionList>
        <OptionRow
          title="New deployments get this address"
          hint={`A new project's public address starts as app.${held.hostname}. sslip.io and your own domains stay a choice.`}
          checked={status.autoAssign}
          disabled={assigning}
          onCheckedChange={(next) => void assign(next)}
        />
      </OptionList>

      <Section
        level={3}
        title="Used by"
        count={status.usedBy.length > 0 ? status.usedBy.length : undefined}
      >
        {status.usedBy.length === 0 ? (
          <EmptyNote dense className="text-left">
            No deployment uses it yet. Switch it on from a deployment&apos;s Domains settings.
          </EmptyNote>
        ) : (
          <RowList aria-label="Deployments using the address">
            {status.usedBy.map((use) => (
              <Row
                key={`${use.environmentId}-${use.hostname}`}
                className={ACTIVITY_ROW}
                href={domainsHref(use)}
                leading={<MarkTile mark={{ glyph: Globe, hue: "var(--tag-blue)" }} />}
                title={use.project}
                subtitle={use.hostname}
                mono
                trailing={<span className="text-xs text-muted-foreground">{use.environment}</span>}
              />
            ))}
          </RowList>
        )}
        {status.usedBy.length > 0 && (
          <FormNote>
            Release is off while {plural(status.usedBy.length, "deployment")}{" "}
            {status.usedBy.length === 1 ? "uses" : "use"} the address. Remove it from{" "}
            {status.usedBy.length === 1 ? "it" : "them"} first.
          </FormNote>
        )}
      </Section>
    </>
  )
}

function ReleaseButton({
  status,
  held,
  domain,
}: {
  status: AppDomainStatus
  held: Held
  domain: AppDomainControls
}) {
  const { confirm, dialog } = useConfirm()
  const inUse = status.usedBy.length > 0

  const ask = () =>
    confirm({
      title: "Release address",
      confirmLabel: "Release",
      subject: {
        mark: <MarkTile mark={{ glyph: Globe, hue: "var(--tag-blue)" }} />,
        name: <span className="font-mono">{held.hostname}</span>,
      },
      description: (
        <>
          <p>
            The DNS records for {held.hostname} and {held.wildcard} are deleted, and this server
            stops holding the handle.
          </p>
          <p className="text-muted-foreground">
            Only your account can claim {held.handle} again for the next 30 days. The certificate
            stays on this server until it expires.
          </p>
        </>
      ),
      action: async () => {
        try {
          await domain.release()
        } catch (err) {
          const words = refusalWords(err, status)
          const names =
            err instanceof ApiError && err.code === "domain_in_use"
              ? ` ${status.usedBy.map((use) => use.hostname).join(", ")}`
              : ""
          throw new ApiError(
            err instanceof ApiError ? err.status : 0,
            err instanceof ApiError ? err.code : "failed",
            `${words}${names}`,
          )
        }
        notify.success(`Released ${held.hostname}`)
        return "reported"
      },
    })

  return (
    <>
      {dialog}
      <Button
        variant="outline"
        size="sm"
        className="text-destructive"
        disabled={inUse}
        onClick={ask}
      >
        Release
      </Button>
    </>
  )
}
