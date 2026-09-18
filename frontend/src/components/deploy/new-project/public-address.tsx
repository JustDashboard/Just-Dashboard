"use client"

import { useState } from "react"
import Link from "next/link"
import { CheckCircle, LockClosed, RefreshClockwise, Warning } from "@/components/icons"
import { get } from "@/lib/api"
import { Field, FormSection, OptionRow } from "@/components/form"
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
  domain,
  onChange,
  suggestion,
}: {
  domain?: Domain
  onChange: (domain: Domain | undefined) => void
  suggestion?: DeploymentHostnameSuggestion
}) {
  const [checked, setChecked] = useState<DeploymentHostnameSuggestion | undefined>(suggestion)
  const [checking, setChecking] = useState(false)

  const publish = domain !== undefined
  const hostname = domain?.hostname ?? suggestion?.hostname ?? ""
  const https = domain?.https ?? true
  const protection = domain?.protection
  const update = (patch: Partial<Domain>) =>
    onChange({ hostname, https, ownership: "managed", ...(domain ?? {}), ...patch })
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
    <FormSection title="Public address">
      <OptionRow
        title="Publish on a public hostname"
        checked={publish}
        onCheckedChange={(next) =>
          onChange(
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
                  onChange({ hostname: event.target.value, https, ownership: "managed" })
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
                    onChange({ hostname, https: nextHttps, ownership: "managed" })
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
