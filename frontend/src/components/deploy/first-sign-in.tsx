"use client"

import { useState } from "react"
import { Copy, Eye, EyeOff, External, Key, LockOpen, UserPlus, Warning } from "@/components/icons"
import { get } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import { notify } from "@/lib/toast"
import type { BlueprintAccess } from "@/lib/types"
import { Field } from "@/components/form"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Notice } from "@/components/state"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"

/**
 * How the operator gets in, said before the deploy and again after it.
 *
 * The catalogue used to leave this to prose. n8n's own first visit asks you to
 * create the owner account, so n8n worked; File Browser generates a random
 * admin password, prints it into its own container log and shows a login form,
 * so File Browser did not — and nothing in the definition distinguished the
 * two. `access` on the blueprint is that distinction, and this is the pair of
 * surfaces that spends it: a word on the card while choosing, and the actual
 * credential on the project once it is running.
 */

const ACCESS_TAG: Record<BlueprintAccess["kind"], { label: string; tone?: "warning" }> = {
  setup: { label: "you create the first account" },
  credentials: { label: "password generated here" },
  token: { label: "token generated here" },
  client: { label: "no sign-in page" },
  open: { label: "no sign-in at all", tone: "warning" },
  unavailable: { label: "no credential to hand over", tone: "warning" },
}

const ACCESS_TITLE: Record<BlueprintAccess["kind"], string> = {
  setup: "You create the first account",
  credentials: "Sign in with the password generated here",
  token: "Sign in with the token generated here",
  client: "Nothing signs in through a browser",
  open: "This has no sign-in of its own",
  unavailable: "There is no first credential this dashboard can give you",
}

const ACCESS_ICON = {
  setup: UserPlus,
  credentials: Key,
  token: Key,
  client: Key,
  open: LockOpen,
  unavailable: Warning,
} as const

/** The one word a template's card carries about getting in. */
export function AccessTag({ access }: { access: BlueprintAccess | undefined }) {
  if (!access?.kind) return null
  const { label, tone } = ACCESS_TAG[access.kind]
  return <Tag tone={tone}>{label}</Tag>
}

/**
 * What the picker shows beside a chosen template: the promise, before anything
 * has been created. There is no credential to show yet — it does not exist
 * until the plan is committed — so this says what will exist.
 */
export function AccessPromise({ access }: { access: BlueprintAccess | undefined }) {
  if (!access?.kind) return null
  return (
    <Notice
      title={ACCESS_TITLE[access.kind]}
      icon={ACCESS_ICON[access.kind]}
      tone={ACCESS_TAG[access.kind].tone ?? "default"}
    >
      {access.note}
    </Notice>
  )
}

type Revealed = { username?: string; secret?: string }

/**
 * The same promise on the running project, with the values in it.
 *
 * Values are read one at a time through the audited reveal route rather than
 * carried on the project payload: a password that arrives with every poll of
 * the overview is a password in every browser cache and every proxy log.
 */
export function FirstSignIn({
  access,
  url,
  projectId,
  environmentId,
  canReveal,
}: {
  access: BlueprintAccess
  url?: string
  projectId: number
  environmentId: number
  /** Reading a stored value is privileged; without it the card still explains. */
  canReveal: boolean
}) {
  const [revealed, setRevealed] = useState<Revealed>()
  const [busy, setBusy] = useState(false)
  const target = url && access.path ? new URL(access.path, url).toString() : url

  const reveal = async () => {
    setBusy(true)
    try {
      const base = `/deploy/${projectId}/environments/${environmentId}/variables`
      const read = async (name: string | undefined) =>
        name
          ? (
              await get<{ name: string; value: string }>(
                `${base}/${encodeURIComponent(name)}/reveal`,
              )
            ).value
          : undefined
      setRevealed({
        username: await read(access.usernameVariable),
        secret: await read(access.secretVariable),
      })
    } catch (caught) {
      notify.error("Could not read the sign-in details", caught)
    } finally {
      setBusy(false)
    }
  }

  const username = access.username ?? revealed?.username
  const secretLabel =
    access.kind === "token" ? "Token" : access.kind === "client" ? "Credential" : "Password"

  return (
    <Panel>
      <PanelHeader
        // A database has no first sign-in; it has a credential another program
        // connects with, and calling that a sign-in would send the reader
        // looking for a login page that does not exist.
        title={access.kind === "client" ? "How to connect" : "First sign-in"}
        actions={
          target && (
            <Button variant="outline" size="sm" asChild>
              <a href={target} target="_blank" rel="noreferrer noopener">
                Open it <External className="size-3.5" />
              </a>
            </Button>
          )
        }
      />
      <PanelBody className="space-y-4">
        <p className="text-body text-muted-foreground">{access.note}</p>

        {access.kind === "setup" && (
          <Notice title="Do this now" icon={Warning} tone="warning">
            Nobody owns this yet. The first person to open the address creates the account, so open
            it before anyone else can.
          </Notice>
        )}

        {/* Only where there is a public address to protect: an `open` workload
            with no route (Redis) is reachable on the Docker network alone, and
            pointing its operator at a domain setting it does not have would
            send them looking for a switch that is not there. */}
        {access.kind === "open" && url && (
          <Notice title="Anyone who reaches this address can use it" icon={Warning} tone="warning">
            It has no accounts and no password of its own. Switch on{" "}
            <strong>Ask visitors for a password</strong> for this address under Settings → Domains
            and the dashboard&rsquo;s own proxy will ask for one before anything reaches it.
          </Notice>
        )}

        {access.kind === "unavailable" && (
          <Notice title="This template is no longer offered" icon={Warning} tone="warning">
            Your deployment keeps working and can still be redeployed, but the first credential is
            not something this dashboard can hand over &mdash; which is why the template was
            withdrawn. The paragraph above says where it actually is.
          </Notice>
        )}

        {access.secretVariable && (
          <>
            {username && (
              <Field label="Username" htmlFor="first-sign-in-username">
                <div className="flex min-w-0 gap-2">
                  <Input
                    id="first-sign-in-username"
                    readOnly
                    value={username}
                    className="min-w-0 flex-1 font-mono"
                  />
                  <Button
                    variant="outline"
                    size="icon-sm"
                    aria-label="Copy username"
                    onClick={() => void copyText(username, "Username copied")}
                  >
                    <Copy className="size-4" />
                  </Button>
                </div>
              </Field>
            )}
            <Field
              label={secretLabel}
              htmlFor="first-sign-in-secret"
              hint={
                canReveal
                  ? `Generated on this server when the plan was saved. Reading it is recorded in the audit log. Rotate it under Settings → Variables (${access.secretVariable}).`
                  : `Held as ${access.secretVariable} under Settings → Variables. Reading it needs the deployment write capability.`
              }
            >
              <div className="flex min-w-0 gap-2">
                <Input
                  id="first-sign-in-secret"
                  readOnly
                  type={revealed?.secret ? "text" : "password"}
                  value={revealed?.secret ?? "••••••••••••"}
                  className="min-w-0 flex-1 font-mono"
                />
                {canReveal && (
                  <Button
                    variant="outline"
                    size="icon-sm"
                    pending={busy}
                    aria-label={revealed?.secret ? "Hide the password" : "Reveal the password"}
                    onClick={() => (revealed ? setRevealed(undefined) : void reveal())}
                  >
                    {revealed?.secret ? <EyeOff className="size-4" /> : <Eye className="size-4" />}
                  </Button>
                )}
                {revealed?.secret && (
                  <Button
                    variant="outline"
                    size="icon-sm"
                    aria-label={`Copy the ${secretLabel.toLowerCase()}`}
                    onClick={() => void copyText(revealed.secret ?? "", `${secretLabel} copied`)}
                  >
                    <Copy className="size-4" />
                  </Button>
                )}
              </div>
            </Field>
          </>
        )}
      </PanelBody>
    </Panel>
  )
}
