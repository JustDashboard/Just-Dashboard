"use client"

import { useEffect, useRef, useState } from "react"
import Link from "next/link"
import { Plus, RefreshClockwise, Trash, Warning } from "@/components/icons"
import { errorMessage, get } from "@/lib/api"
import { zoneWords } from "@/lib/app-domain"
import { useAppDomain } from "@/hooks/use-app-domain"
import { useAuth } from "@/hooks/use-auth"
import { Field, FormNote, FormSection, OptionRow } from "@/components/form"
import { INLINE_LINK } from "@/components/inline-link"
import { Segments } from "@/components/segments"
import { Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  InputGroup,
  InputGroupAddon,
  InputGroupButton,
  InputGroupInput,
} from "@/components/ui/input-group"
import { HttpsToggle } from "@/components/deploy/https-toggle"
import { ServiceRoute } from "@/components/deploy/service-route"
import { useServiceTitles } from "@/components/deploy/stack/use-stack"
import type { DeploymentConfiguration, DeploymentHostnameSuggestion } from "@/lib/types"

type Domain = DeploymentConfiguration["domains"][number]

/**
 * The address the deployment answers on, and the certificate it needs to do
 * it over HTTPS — ported from `quick-deploy.tsx`'s `PublicAddress` with the
 * same behaviour (suggestion, re-check, the certificate reading, the
 * no-public-address warning), now operating on the plan's own `domains`
 * entry instead of a parallel `hostname`/`https`/`publish` triple. Whether
 * the name is covered is a `Status` at the field's label, what the run will
 * do about it a line of note, and only the case that needs a decision is a
 * `Notice`; the scheme is `HttpsToggle`, the segment the Domains rows use.
 *
 * Activation resolves an existing certificate or refuses the cutover, so a
 * name with no certificate is a release that fails at the last step; this is
 * said here, before the deploy, rather than explained afterwards.
 */
export function PublicAddress({
  id,
  name,
  domains,
  onChange,
  suggestion,
}: {
  id?: string
  /** The project's name, which the just-dashboard.com name is made from. */
  name?: string
  /**
   * Every name this deployment will answer to. The first is the primary one,
   * with the suggestion and the certificate reading; the rest are a plain
   * list, each saying the app it routes to where that is not the primary
   * service (a stack's), and keeping its `service` and `port` as it is edited. It used to be exactly one, so apex-plus-www — the commonest thing
   * anyone wants on day one — meant creating the project and then going to
   * its Domains settings to add the second name.
   */
  domains: Domain[]
  onChange: (domains: Domain[]) => void
  suggestion?: DeploymentHostnameSuggestion
}) {
  const [checked, setChecked] = useState<DeploymentHostnameSuggestion | undefined>(suggestion)
  const offer = useAppDomainOffer(name, suggestion)
  const [checking, setChecking] = useState(false)
  const [checkError, setCheckError] = useState<{ hostname: string; message: string }>()
  const checkRequest = useRef<AbortController | undefined>(undefined)
  useEffect(() => () => checkRequest.current?.abort(), [])

  // Annotated rather than inferred: indexing an array answers `Domain` under
  // this compiler's settings, and "is there a primary domain at all" is the
  // question the whole section turns on.
  const domain: Domain | undefined = domains[0]
  const extra = domains.slice(1)
  // A stack's template gives each app a name of its own, routed to its service.
  const serviceTitle = useServiceTitles(extra.some((entry) => entry.service))
  const setPrimary = (next: Domain) => onChange([next, ...extra])
  const publish = domain !== undefined
  const hostname = domain?.hostname ?? suggestion?.hostname ?? ""
  const https = domain?.https ?? true
  const protection = domain?.protection
  // The primary domain as it stands, or the one the suggestion would create.
  // Spelled out rather than spread over defaults: the keys were written twice
  // in one literal, which reads as a merge and is really a dead branch.
  const base: Domain = domain ?? { hostname, https, ownership: "managed" }
  const update = (patch: Partial<Domain>) => setPrimary({ ...base, ...patch })
  const normalizedHostname = hostname.trim().toLowerCase()
  const current = checked?.hostname.toLowerCase() === normalizedHostname ? checked : suggestion
  const matches = current?.hostname.toLowerCase() === normalizedHostname
  /**
   * Whether the name will answer over HTTPS, as a reading at its own label.
   *
   * It was a full-width tinted banner under the field, and in the commonest
   * case — a certificate already covers the name — it repeated the field's own
   * hint word for word: the server's `detail` line under the input says "the
   * caddy-… certificate already covers it", and the banner said "the caddy-…
   * certificate already covers it, so the deploy reuses it". A green box, a
   * green rule and a green plate, to say a second time what the grey line
   * above it had already said. A state is a `Status` — a dot and a word (§4) —
   * and the place for it is beside the thing it is a state of.
   */
  const certificate =
    !https || !matches || !current
      ? undefined
      : current.covered
        ? { tone: "running" as const, label: "HTTPS ready" }
        : current.certificateMethod
          ? { tone: "notice" as const, label: "Certificate on deploy" }
          : { tone: "warning" as const, label: "HTTPS unavailable" }

  // The addresses the name can be: the just-dashboard.com one, the one that needs nothing from
  // the site (the operator's wildcard, or sslip.io), and the reader's own.
  const choices = addressChoices(suggestion, offer.suggestion, offer.zone, offer.standard)
  const chosen = choices.find((choice) => choice.hostname === normalizedHostname)
  const pick = (value: string) => {
    if (value === "own") {
      update({ hostname: "" })
      setCheckError(undefined)
      requestAnimationFrame(() => document.getElementById("public-hostname")?.focus())
      return
    }
    const choice = choices.find((item) => item.value === value)
    if (!choice) return
    update({ hostname: choice.hostname })
    // An answer already read is the answer: only a name nobody has asked about is checked.
    if (choice.answer) {
      checkRequest.current?.abort()
      setChecking(false)
      setCheckError(undefined)
      setChecked(choice.answer)
    } else void recheck(choice.hostname)
  }

  const recheck = async (value: string) => {
    const trimmed = value.trim().toLowerCase()
    if (!trimmed) return
    checkRequest.current?.abort()
    const controller = new AbortController()
    checkRequest.current = controller
    setChecking(true)
    setCheckError(undefined)
    try {
      const result = await get<DeploymentHostnameSuggestion>(
        "/deploy/hostname",
        { hostname: trimmed },
        controller.signal,
      )
      if (!controller.signal.aborted) setChecked(result)
    } catch (error) {
      if (!controller.signal.aborted)
        setCheckError({ hostname: trimmed, message: errorMessage(error) })
    } finally {
      if (!controller.signal.aborted) setChecking(false)
    }
  }

  return (
    <FormSection id={id} title="Public address">
      <OptionRow
        title="Publish on a public hostname"
        checked={publish}
        onCheckedChange={(next) =>
          onChange(next ? [{ ...base, hostname: normalizedHostname }] : [])
        }
      >
        <div className="space-y-3">
          {choices.length > 1 && (
            <Segments
              label="Address"
              value={chosen?.value ?? "own"}
              options={[
                ...choices.map((choice) => ({ value: choice.value, label: choice.label })),
                { value: "own", label: "Your domain" },
              ]}
              onChange={pick}
              onReselect={(value) => value === "own" && pick("own")}
            />
          )}
          {/* One field, not three controls that happen to be adjacent.
              The name, the scheme it is served over and the check on both are
              one decision, and they were three boxes at three heights in a
              wrapping flex: a 36px input, a 14px switch and a 36px button,
              with the smallest of them — the one that decides whether the
              site answers on 443 — reading as a stray control that had
              drifted next to the field. In an `InputGroup` they are one box
              with one border, one height and one focus ring, and the scheme
              is a `Toggle` the size of the field it belongs to, lit with the
              same `bg-accent` every other chosen thing in the product wears
              (§3). */}
          <Field
            label="Hostname"
            htmlFor="public-hostname"
            error={checkError?.hostname === normalizedHostname ? checkError.message : undefined}
            trailing={certificate && <Status tone={certificate.tone} label={certificate.label} />}
            hint={
              matches
                ? current?.detail
                : "Point a record at this server, or use the one suggested here."
            }
          >
            <InputGroup>
              <InputGroupInput
                id="public-hostname"
                value={hostname}
                onChange={(event) => update({ hostname: event.target.value })}
                onBlur={(event) => void recheck(event.target.value)}
                placeholder="app.example.com"
                className="font-mono"
              />
              <InputGroupAddon align="inline-end" className="gap-0 p-0">
                <HttpsToggle
                  checked={https}
                  onChange={(nextHttps) => update({ https: nextHttps })}
                />
                <InputGroupButton
                  aria-label="Re-check this hostname"
                  pending={checking}
                  onClick={() => void recheck(hostname)}
                >
                  <RefreshClockwise className="size-3.5" />
                  <span className="max-sm:hidden">Re-check</span>
                </InputGroupButton>
              </InputGroupAddon>
            </InputGroup>
          </Field>
          {/* Only what nothing above says. That a certificate is coming is the
              `Status` at the label and the server's hint under the field; the
              name is in the field. What is left is who orders it, when, and
              that nobody has to renew it. */}
          {https && matches && current && !current.covered && current.certificateMethod && (
            <FormNote>
              {current.certificateMethod === "caddy"
                ? "The managed Caddy ingress orders the certificate before the release starts, and renews it automatically."
                : current.certificateMethod === "app-domain"
                  ? `The release issues this server's wildcard certificate for ${wildcardOf(current.hostname)} through ${zoneWords(offer)} before it starts, and renews it automatically.`
                  : `Certbot orders the certificate over the ${current.certificateMethod} challenge before the release starts, and renews it automatically.`}
            </FormNote>
          )}
          {offer.hint && choices.length < 2 && (
            <FormNote>
              {offer.hint}{" "}
              <Link href="/dashboard/link#app-domain" className={INLINE_LINK}>
                Settings › Connect
              </Link>
            </FormNote>
          )}
          <OptionRow
            title="Ask visitors for a password"
            checked={Boolean(protection)}
            onCheckedChange={(next) =>
              update({ protection: next ? { username: "", password: "" } : undefined })
            }
          >
            <div className="grid gap-3 sm:grid-cols-2">
              <Field label="User name" htmlFor="public-protection-user">
                <Input
                  id="public-protection-user"
                  value={protection?.username ?? ""}
                  autoComplete="off"
                  className="font-mono"
                  onChange={(event) =>
                    update({
                      protection: {
                        ...(protection ?? { password: "" }),
                        username: event.target.value,
                      },
                    })
                  }
                />
              </Field>
              <Field
                label="Password"
                htmlFor="public-protection-password"
                hint="At least 8 characters. Kept only as a hash."
              >
                <Input
                  id="public-protection-password"
                  type="password"
                  autoComplete="new-password"
                  value={protection?.password ?? ""}
                  className="font-mono"
                  onChange={(event) =>
                    update({
                      protection: {
                        ...(protection ?? { username: "" }),
                        password: event.target.value,
                      },
                    })
                  }
                />
              </Field>
            </div>
          </OptionRow>
          {/* One banner, and only for the case that needs a decision. A
              `Notice` is for what the reader has to act on; the two that said
              "this is fine" and "this will happen by itself" were framed
              blocks spending a tinted box each on an outcome nobody has to do
              anything about. */}
          {https && matches && current && !current.covered && !current.certificateMethod && (
            <Notice tone="warning" icon={Warning} title="Automatic HTTPS unavailable">
              {current.certificateIssue ?? "Certificate readiness could not be confirmed."} Review
              certificate options on the{" "}
              <Link href="/proxy/certificates" className="underline underline-offset-2">
                Certificates page
              </Link>
              .
            </Notice>
          )}

          {/* The other names, as a plain list: the certificate reading above
              belongs to the primary one, and repeating it per row would say
              the same thing four times. */}
          {extra.length > 0 && (
            <ul className="space-y-2" aria-label="Additional hostnames">
              {extra.map((entry, index) => (
                <li key={index} className="min-w-0">
                  <Field
                    label={`Also answers to ${index + 2}`}
                    htmlFor={`extra-hostname-${index}`}
                    trailing={
                      entry.service && (
                        <ServiceRoute
                          title={serviceTitle(entry.service)}
                          className="text-hint text-muted-foreground"
                        />
                      )
                    }
                  >
                    <InputGroup>
                      <InputGroupInput
                        id={`extra-hostname-${index}`}
                        value={entry.hostname}
                        placeholder="www.example.com"
                        className="font-mono"
                        onChange={(event) =>
                          onChange(
                            domains.map((item, position) =>
                              position === index + 1
                                ? { ...item, hostname: event.target.value }
                                : item,
                            ),
                          )
                        }
                      />
                      <InputGroupAddon align="inline-end" className="gap-0 p-0">
                        <HttpsToggle
                          checked={entry.https}
                          onChange={(nextHttps) =>
                            onChange(
                              domains.map((item, position) =>
                                position === index + 1 ? { ...item, https: nextHttps } : item,
                              ),
                            )
                          }
                        />
                        <InputGroupButton
                          aria-label={`Remove ${entry.hostname || `hostname ${index + 2}`}`}
                          onClick={() =>
                            onChange(domains.filter((_, position) => position !== index + 1))
                          }
                        >
                          <Trash className="size-3.5" />
                        </InputGroupButton>
                      </InputGroupAddon>
                    </InputGroup>
                  </Field>
                </li>
              ))}
            </ul>
          )}
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={() =>
              onChange([...domains, { hostname: "", https, ownership: "managed" as const }])
            }
          >
            <Plus className="size-3.5" /> Add another hostname
          </Button>
        </div>
      </OptionRow>
      {current?.method === "none" && (
        <Notice tone="warning" icon={Warning} title="This server has no public address">
          A hostname cannot be generated. The deployment still runs; reach it through the port it
          publishes, or set a domain that resolves here.
        </Notice>
      )}
    </FormSection>
  )
}

/** `*.acme.just-dashboard.com` for `shop.acme.just-dashboard.com`. */
function wildcardOf(hostname: string) {
  return `*.${hostname.split(".").slice(1).join(".")}`
}

type AddressChoice = {
  value: string
  label: string
  hostname: string
  /** What the server already said about the name, when it has. */
  answer?: DeploymentHostnameSuggestion
}

/**
 * The names the step offers as choices, the just-dashboard.com one first. Without one there is
 * nothing to choose between: the field holds the suggestion, as it always has.
 */
function addressChoices(
  suggestion: DeploymentHostnameSuggestion | undefined,
  appSuggestion: DeploymentHostnameSuggestion | undefined,
  zone: string | undefined,
  standard: DeploymentHostnameSuggestion | undefined,
): AddressChoice[] {
  if (!appSuggestion?.hostname) return []
  const app: AddressChoice = {
    value: "app-domain",
    label: zone ?? "just-dashboard.com",
    hostname: appSuggestion.hostname.toLowerCase(),
    answer: appSuggestion,
  }
  // The other name is the backend's own suggestion without the address: the default itself
  // when that is not the app-domain name, else what `method=standard` answered.
  const other = suggestion?.method !== "app-domain" ? suggestion : standard
  if (other?.hostname && other.method !== "app-domain" && other.method !== "none") {
    return [
      app,
      {
        value: other.method,
        label: other.method === "sslip" ? "sslip.io" : (other.base ?? "Wildcard"),
        hostname: other.hostname.toLowerCase(),
        answer: other,
      },
    ]
  }
  return [app]
}

/**
 * Whether this server can give the project a just-dashboard.com name, and the name: the
 * suggestion's own when it already is one (the address is the default for new deployments),
 * otherwise asked for with `method=app-domain` while the server holds an active address.
 * When it holds none, `hint` is the quiet line that says where one is set up. Administrators
 * only, since the address is a link setting; anyone else sees the step as it was.
 */
function useAppDomainOffer(
  name: string | undefined,
  suggestion: DeploymentHostnameSuggestion | undefined,
) {
  const { can } = useAuth()
  const { status } = useAppDomain({ enabled: can("system.admin") })
  const active = status?.enabled && status.domain?.status === "active" ? status.domain : undefined
  const own = suggestion?.method === "app-domain" ? suggestion : undefined
  const [asked, setAsked] = useState<{ key: string; answer: DeploymentHostnameSuggestion }>()
  const key = active && name && !own ? `${active.handle}:${name}` : ""
  // With the app-domain name the default, the alternative is asked for too: the name the
  // backend would suggest without it (`method=standard`), never one composed here.
  const [standard, setStandard] = useState<{
    key: string
    answer: DeploymentHostnameSuggestion
  }>()
  const standardKey = own && name ? `${own.hostname}:${name}` : ""

  useEffect(() => {
    if (!standardKey || !name) return
    const controller = new AbortController()
    get<DeploymentHostnameSuggestion>(
      "/deploy/hostname",
      { name, method: "standard" },
      controller.signal,
    )
      .then((answer) => {
        if (answer.hostname && answer.method !== "none") setStandard({ key: standardKey, answer })
      })
      .catch(() => {
        // Best-effort: without it the step offers the app-domain name and your own domain.
      })
    return () => controller.abort()
  }, [standardKey, name])

  useEffect(() => {
    if (!key || !name) return
    const controller = new AbortController()
    get<DeploymentHostnameSuggestion>(
      "/deploy/hostname",
      { name, method: "app-domain" },
      controller.signal,
    )
      .then((answer) => {
        if (answer.method === "app-domain" && answer.hostname) setAsked({ key, answer })
      })
      .catch(() => {
        // Best-effort, as the first suggestion is: the step works without the choice.
      })
    return () => controller.abort()
  }, [key, name])

  const hint =
    !status?.enabled || status.available === false || active || own
      ? undefined
      : !status.linked
        ? `Apps can answer at a ${zoneWords(status)} address once this server is linked and holds one:`
        : status.domain?.status === "suspended"
          ? `This server's ${zoneWords(status)} address is suspended:`
          : `Apps can answer at a ${zoneWords(status)} address once this server claims one:`

  return {
    suggestion: own ?? (asked?.key === key ? asked.answer : undefined),
    zone: status?.zone ?? undefined,
    standard: standard?.key === standardKey ? standard.answer : undefined,
    hint,
  }
}
