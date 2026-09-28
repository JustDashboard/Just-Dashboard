"use client"

import { useState } from "react"
import Link from "next/link"
import { Copy, Eye, External, Key, Warning } from "@/components/icons"
import { get } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import { notify } from "@/lib/toast"
import type { BlueprintAccess } from "@/lib/types"
import { Field, FieldRow, FormFact, FormFacts } from "@/components/form"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ProductLogo } from "@/components/product-logo"
import { Notice } from "@/components/state"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import {
  InputGroup,
  InputGroupAddon,
  InputGroupButton,
  InputGroupInput,
  InputGroupToggle,
} from "@/components/ui/input-group"

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
  const { tone } = ACCESS_TAG[access.kind]
  // A severity is the one thing a notice's glyph may say (§14): the kinds
  // that warn carry it, and the rest are a promise with nothing to act on.
  return (
    <Notice title={ACCESS_TITLE[access.kind]} icon={tone && Warning} tone={tone ?? "default"}>
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
 *
 * It opens on the product it is for, drawn as itself beside what its own
 * documentation says about getting in, and the address it is reached at. The
 * username and the secret are each one box — the value, and the controls that
 * belong to it inside the same edge (§7) — where they were an input with
 * loose outline buttons beside it at two other heights. Showing the secret is
 * a toggle the height of the field, named for what it holds: a token was
 * announced as a password.
 *
 * It is a plain block like every other on the Overview: a sign-in is a
 * reading of the project, not a card standing in front of it.
 */
export function FirstSignIn({
  access,
  url,
  projectId,
  environmentId,
  canReveal,
  product,
  service,
  port,
}: {
  access: BlueprintAccess
  url?: string
  projectId: number
  environmentId: number
  /** Reading a stored value is privileged; without it the block still explains. */
  canReveal: boolean
  /** The template, as a `product-logo` id. */
  product?: string
  /** The live container's name, which is the host another container connects to. */
  service?: string
  port?: number
}) {
  const [revealed, setRevealed] = useState<Revealed>()
  const [busy, setBusy] = useState(false)
  const target = url && access.path ? new URL(access.path, url).toString() : url
  const variables = `/deploy/${projectId}/settings/variables`

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
  const secretWord = secretLabel.toLowerCase()

  const secretField = (
    <Field
      label={secretLabel}
      htmlFor="first-sign-in-secret"
      hint={
        canReveal ? (
          <>
            Reading it is recorded in the audit log ·{" "}
            <Link href={variables} className="rounded-sm focus-ring hover:text-foreground">
              <Tag mono>{access.secretVariable}</Tag>
            </Link>
          </>
        ) : (
          <>
            Held as{" "}
            <Link href={variables} className="rounded-sm focus-ring hover:text-foreground">
              <Tag mono>{access.secretVariable}</Tag>
            </Link>{" "}
            · reading it needs the deployment write capability
          </>
        )
      }
    >
      <InputGroup>
        <InputGroupInput
          id="first-sign-in-secret"
          readOnly
          type={revealed?.secret ? "text" : "password"}
          value={revealed?.secret ?? "••••••••••••"}
          className="font-mono"
        />
        {canReveal && (
          <InputGroupAddon align="inline-end" className="gap-0 p-0">
            <InputGroupToggle
              icon={Eye}
              label="Reveal"
              aria-label={`Reveal the ${secretWord}`}
              pressed={Boolean(revealed?.secret)}
              disabled={busy}
              onPressedChange={(next) => (next ? void reveal() : setRevealed(undefined))}
            />
            <InputGroupButton
              aria-label={`Copy the ${secretWord}`}
              disabled={!revealed?.secret}
              onClick={() => void copyText(revealed?.secret ?? "", `${secretLabel} copied`)}
            >
              <Copy className="size-3.5" />
              <span className="max-sm:hidden">Copy</span>
            </InputGroupButton>
          </InputGroupAddon>
        )}
      </InputGroup>
    </Field>
  )

  return (
    <Panel plain>
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
      <PanelBody className="space-y-5">
        <div className="flex min-w-0 items-start gap-3">
          <ProductLogo id={product} size="sm" fallback={Key} />
          <div className="min-w-0 space-y-1">
            <p className="text-body leading-relaxed">{access.note}</p>
            {target && (
              <a
                href={target}
                target="_blank"
                rel="noreferrer noopener"
                className="inline-flex max-w-full min-w-0 items-center gap-1 rounded-sm font-mono text-hint text-muted-foreground focus-ring hover:text-foreground"
              >
                <span className="truncate">
                  {target.replace(/^https?:\/\//, "").replace(/\/$/, "")}
                </span>
                <External aria-hidden className="size-3 shrink-0" />
              </a>
            )}
          </div>
        </div>

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
            <strong>Ask visitors for a password</strong> for this address under{" "}
            <Link
              href={`/deploy/${projectId}/settings/domains`}
              className="rounded-sm font-medium text-foreground underline underline-offset-2 focus-ring"
            >
              Settings → Domains
            </Link>{" "}
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

        {/* Another container reaches a database by the live container's name
            on the project's network, at the port it listens on inside. */}
        {access.kind === "client" && (service || port) ? (
          <FormFacts>
            {service && (
              <FormFact label="Host" mono>
                {service}
              </FormFact>
            )}
            {port ? (
              <FormFact label="Port" mono>
                {port}
              </FormFact>
            ) : null}
          </FormFacts>
        ) : null}

        {access.secretVariable &&
          (username ? (
            <FieldRow>
              <Field label="Username" htmlFor="first-sign-in-username">
                <InputGroup>
                  <InputGroupInput
                    id="first-sign-in-username"
                    readOnly
                    value={username}
                    className="font-mono"
                  />
                  <InputGroupAddon align="inline-end" className="gap-0 p-0">
                    <InputGroupButton
                      aria-label="Copy username"
                      onClick={() => void copyText(username, "Username copied")}
                    >
                      <Copy className="size-3.5" />
                      <span className="max-sm:hidden">Copy</span>
                    </InputGroupButton>
                  </InputGroupAddon>
                </InputGroup>
              </Field>
              {secretField}
            </FieldRow>
          ) : (
            secretField
          ))}
      </PanelBody>
    </Panel>
  )
}
