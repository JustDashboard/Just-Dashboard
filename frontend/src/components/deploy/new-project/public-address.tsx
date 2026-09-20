"use client"

import { useState } from "react"
import Link from "next/link"
import { CheckCircle, LockClosed, Plus, RefreshClockwise, Trash, Warning } from "@/components/icons"
import { get } from "@/lib/api"
import { Field, FormSection, OptionRow } from "@/components/form"
import { IconAction } from "@/components/icon-action"
import { Notice } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Switch } from "@/components/ui/switch"
import { Label } from "@/components/ui/label"
import type { DeploymentConfiguration, DeploymentHostnameSuggestion } from "@/lib/types"

type Domain = DeploymentConfiguration["domains"][number]

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

  // Annotated rather than inferred: indexing an array answers `Domain` under
  // this compiler's settings, and "is there a primary domain at all" is the
  // question the whole section turns on.
  const domain: Domain | undefined = domains[0]
  const extra = domains.slice(1)
  const setPrimary = (next: Domain | undefined) =>
    onChange(next ? [next, ...extra] : extra.length > 0 ? extra : [])
  const publish = domain !== undefined
  const hostname = domain?.hostname ?? suggestion?.hostname ?? ""
  const https = domain?.https ?? true
  const protection = domain?.protection
  // The primary domain as it stands, or the one the suggestion would create.
  // Spelled out rather than spread over defaults: the keys were written twice
  // in one literal, which reads as a merge and is really a dead branch.
  const base: Domain = domain ?? { hostname, https, ownership: "managed" }
  const update = (patch: Partial<Domain>) => setPrimary({ ...base, ...patch })
  const current = checked ?? suggestion
  const matches = current?.hostname.toLowerCase() === hostname.toLowerCase()

  const recheck = async (value: string) => {
    const trimmed = value.trim().toLowerCase()
    if (!trimmed) return
    setChecking(true)
    try {
      setChecked(
        await get<DeploymentHostnameSuggestion>(
          `/deploy/hostname?hostname=${encodeURIComponent(trimmed)}`,
        ),
      )
    } catch {
      // Leaving the last answer on screen is better than blanking the panel:
      // it was true a moment ago, and the run itself is what settles this.
    } finally {
      setChecking(false)
    }
  }

  return (
    <FormSection id={id} title="Public address">
      <OptionRow
        title="Publish on a public hostname"
        checked={publish}
        onCheckedChange={(next) =>
          setPrimary(
            next ? { hostname: hostname.toLowerCase(), https, ownership: "managed" } : undefined,
          )
        }
      >
        <div className="space-y-3">
          <div className="grid gap-3 sm:grid-cols-[minmax(0,1fr)_auto]">
            <Field
              label="Hostname"
              htmlFor="public-hostname"
              hint={
                matches
                  ? current?.detail
                  : "Point a record at this server, or use the one suggested here."
              }
            >
              <Input
                id="public-hostname"
                value={hostname}
                onChange={(event) =>
                  setPrimary({ hostname: event.target.value, https, ownership: "managed" })
                }
                onBlur={(event) => void recheck(event.target.value)}
                placeholder="app.example.com"
                className="font-mono"
              />
            </Field>
            <div className="flex items-end gap-3 pb-1">
              <Label className="flex min-h-9 items-center gap-2 text-xs">
                <Switch
                  checked={https}
                  onCheckedChange={(nextHttps) =>
                    setPrimary({ hostname, https: nextHttps, ownership: "managed" })
                  }
                />
                HTTPS
              </Label>
              <Button
                type="button"
                size="sm"
                variant="outline"
                pending={checking}
                onClick={() => void recheck(hostname)}
              >
                <RefreshClockwise className="size-3.5" />
                Re-check
              </Button>
            </div>
          </div>
          <OptionRow
            title="Ask visitors for a password"
            hint="A staging site or a preview a customer should not find. The proxy asks before the application sees the request; previews inherit it."
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
          {https && matches && current?.covered && (
            <Notice tone="success" icon={CheckCircle} title="HTTPS is ready for this name">
              The <code className="font-mono">{current.certificateName}</code> certificate already
              covers it, so the deploy reuses it rather than ordering another.
            </Notice>
          )}
          {https && matches && current && !current.covered && (
            <Notice
              tone={current.certificateMethod ? "default" : "warning"}
              icon={current.certificateMethod ? LockClosed : Warning}
              title={
                current.certificateMethod
                  ? "A certificate will be issued during the deploy"
                  : "Automatic HTTPS needs attention"
              }
            >
              {current.certificateMethod ? (
                <>
                  The run orders one for <code className="font-mono">{hostname}</code> over the{" "}
                  {current.certificateMethod === "caddy"
                    ? "managed Caddy ingress"
                    : `${current.certificateMethod} challenge`}{" "}
                  before it starts anything.{" "}
                  {current.certificateMethod === "caddy" ? "Caddy" : "Certbot"} handles renewal
                  automatically.
                </>
              ) : (
                <>
                  {current.certificateIssue ?? "Certificate readiness could not be confirmed."}{" "}
                  Review certificate options on the{" "}
                  <Link href="/proxy/certificates" className="underline underline-offset-2">
                    Certificates page
                  </Link>
                  .
                </>
              )}
            </Notice>
          )}

          {/* The other names, as a plain list: the certificate reading above
              belongs to the primary one, and repeating it per row would say
              the same thing four times. */}
          {extra.length > 0 && (
            <ul className="space-y-2" aria-label="Additional hostnames">
              {extra.map((entry, index) => (
                <li key={index} className="flex min-w-0 items-end gap-2">
                  <Field
                    label={`Also answers to ${index + 2}`}
                    htmlFor={`extra-hostname-${index}`}
                    className="min-w-0 flex-1"
                  >
                    <Input
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
                  </Field>
                  <Label className="flex min-h-9 items-center gap-2 pb-1 text-xs">
                    <Switch
                      checked={entry.https}
                      onCheckedChange={(nextHttps) =>
                        onChange(
                          domains.map((item, position) =>
                            position === index + 1 ? { ...item, https: nextHttps } : item,
                          ),
                        )
                      }
                    />
                    HTTPS
                  </Label>
                  <IconAction
                    label={`Remove ${entry.hostname || `hostname ${index + 2}`}`}
                    className="mb-1"
                    onClick={() =>
                      onChange(domains.filter((_, position) => position !== index + 1))
                    }
                  >
                    <Trash />
                  </IconAction>
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
