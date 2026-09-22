"use client"

import { useEffect, useRef, useState } from "react"
import Link from "next/link"
import { LockClosed, Plus, RefreshClockwise, Trash, Warning } from "@/components/icons"
import { errorMessage, get } from "@/lib/api"
import { cn } from "@/lib/utils"
import { Field, FormNote, FormSection, OptionRow } from "@/components/form"
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
import { Toggle } from "@/components/ui/toggle"
import type { DeploymentConfiguration, DeploymentHostnameSuggestion } from "@/lib/types"

type Domain = DeploymentConfiguration["domains"][number]

/**
 * Whether the name is served over HTTPS, as the last segment of the field it
 * belongs to.
 *
 * A `Switch` is the right control for an option in a list of options, where a
 * sentence names it and the switch answers. It is the wrong one at the end of
 * a text field: at 14px beside a 36px input it is the smallest thing in a row
 * of the largest, and the two read as unrelated. Pressed state here is
 * `aria-pressed` on a control the size of the field, lit with `bg-accent` —
 * §6's three mechanisms are untouched, the selection one is simply on a
 * control that can carry it.
 */
function HttpsToggle({
  checked,
  onChange,
}: {
  checked: boolean
  onChange: (checked: boolean) => void
}) {
  return (
    <Toggle
      aria-label="Serve this hostname over HTTPS"
      pressed={checked}
      onPressedChange={onChange}
      className="h-full gap-1.5 rounded-none px-3 text-xs font-medium focus-ring-inset data-[state=off]:text-muted-foreground"
    >
      {/* The lock is the mark, and it takes the brand when the scheme is on —
          the same blue `ChoiceCard` puts on a chosen option's glyph and
          `FlowSteps` on a completed step. `bg-accent` alone is a step of
          ground a reader has to compare with the field beside it to see; the
          tinted glyph says it without the comparison. */}
      <LockClosed className={cn("size-3.5", checked ? "text-brand" : "text-muted-foreground")} />
      {/* The word goes on a phone, where 390px of field had become 140px of
          field and 250px of two labels. The lock is the mark, the group is
          still one box, and `aria-label` above is what names the control
          either way. */}
      <span className="max-sm:hidden">HTTPS</span>
    </Toggle>
  )
}

/**
 * The address the deployment answers on, and the certificate it needs to do
 * it over HTTPS — ported from `quick-deploy.tsx`'s `PublicAddress` with the
 * same behaviour (suggestion, re-check, covered / will-issue / needs-attention
 * notices, the no-public-address notice), now operating on the plan's own
 * `domains` entry instead of a parallel `hostname`/`https`/`publish` triple.
 *
 * Activation resolves an existing certificate or refuses the cutover, so a
 * name with no certificate is a release that fails at the last step; this is
 * said here, before the deploy, rather than explained afterwards.
 */
export function PublicAddress({
  id,
  domains,
  onChange,
  suggestion,
}: {
  id?: string
  /**
   * Every name this deployment will answer to. The first is the primary one,
   * with the suggestion and the certificate reading; the rest are a plain
   * list. It used to be exactly one, so apex-plus-www — the commonest thing
   * anyone wants on day one — meant creating the project and then going to
   * its Domains settings to add the second name.
   */
  domains: Domain[]
  onChange: (domains: Domain[]) => void
  suggestion?: DeploymentHostnameSuggestion
}) {
  const [checked, setChecked] = useState<DeploymentHostnameSuggestion | undefined>(suggestion)
  const [checking, setChecking] = useState(false)
  const [checkError, setCheckError] = useState<{ hostname: string; message: string }>()
  const checkRequest = useRef<AbortController | undefined>(undefined)
  useEffect(() => () => checkRequest.current?.abort(), [])

  // Annotated rather than inferred: indexing an array answers `Domain` under
  // this compiler's settings, and "is there a primary domain at all" is the
  // question the whole section turns on.
  const domain: Domain | undefined = domains[0]
  const extra = domains.slice(1)
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
          : { tone: "warning" as const, label: "HTTPS needs attention" }

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
          {/* Only where something is still going to happen. What the run will
              do about a certificate it has not got is worth a line; that it
              already has one is what the `Status` above says, in two words. */}
          {https && matches && current && !current.covered && current.certificateMethod && (
            <FormNote>
              A certificate will be issued during the deploy. The run orders one for{" "}
              <code className="font-mono">{hostname}</code> over the{" "}
              {current.certificateMethod === "caddy"
                ? "managed Caddy ingress"
                : `${current.certificateMethod} challenge`}{" "}
              before it starts anything.{" "}
              {current.certificateMethod === "caddy" ? "Caddy" : "Certbot"} handles renewal
              automatically.
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
            <Notice tone="warning" icon={Warning} title="Automatic HTTPS needs attention">
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
                  <Field label={`Also answers to ${index + 2}`} htmlFor={`extra-hostname-${index}`}>
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
        <Notice icon={Warning} title="This server has no public address">
          A hostname cannot be generated. The deployment still runs; reach it through the port it
          publishes, or set a domain that resolves here.
        </Notice>
      )}
    </FormSection>
  )
}
