"use client"

import { useState } from "react"
import Link from "next/link"
import { Globe } from "@/components/icons"
import { ApiError, get, post } from "@/lib/api"
import { usePoll } from "@/hooks/use-poll"
import { notify } from "@/lib/toast"
import type {
  DeploymentHostnameSuggestion,
  SmartStackAddresses,
  SmartStackAddressesStarted,
  SmartStackCatalogueEntry,
} from "@/lib/types"
import { Field, FormNote } from "@/components/form"
import { INLINE_LINK } from "@/components/inline-link"
import { Modal } from "@/components/modal"
import { Notice } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { hostnameFromInput, hostnameProblem } from "@/components/deploy/settings/domain-rows"
import {
  PENDING_CHANGES_NOTE,
  addressesRefusal,
  addressesSentence,
  missingHostnames,
  onlyUnpublished,
} from "@/components/deploy/stack/stack-model"

/**
 * "Give every app its address", for a stack whose apps have none beside its
 * domain: a stack made before they did, or one whose names were taken off the
 * Domains page. It says which apps and the names they get, confirms, and
 * starts the release that serves them, then links to that run.
 *
 * The press is a domain change and a deploy, so it is an administrator's;
 * anyone else reads the sentence and who can do it. What the press started
 * is the page's to keep (`AddressesStarted`): the next read has no app
 * missing its address, and this step is gone with it. The release it starts
 * is of the saved configuration, so with changes saved but not deployed the
 * step and the dialog say those go out with it.
 */
export function AddressesStep({
  addresses,
  projectId,
  projectName,
  stackId,
  canChange,
  pendingChanges,
  first,
  onStarted,
  onSettled,
}: {
  addresses: SmartStackAddresses
  projectId: number
  projectName: string
  stackId: string
  canChange: boolean
  /** The deployment has changes saved but not deployed, which the release takes with it. */
  pendingChanges: boolean
  /** The page's one next step, so its command wears the brand. */
  first: boolean
  onStarted: (answer: SmartStackAddressesStarted) => void
  /** Nothing was missing after all: the page reads the stack again. */
  onSettled: () => void
}) {
  const [open, setOpen] = useState(false)
  const republish = onlyUnpublished(addresses)
  return (
    <Notice
      data-slot="stack-addresses"
      title={republish ? "Publish the apps' addresses" : "Give every app its address"}
    >
      <p>
        {addressesSentence(addresses)}
        {pendingChanges && ` ${PENDING_CHANGES_NOTE}`}
      </p>
      {canChange ? (
        <div className="pt-1">
          <Button size="sm" variant={first ? "default" : "outline"} onClick={() => setOpen(true)}>
            <Globe />
            {republish ? "Deploy again" : "Give every app its address"}
          </Button>
        </div>
      ) : (
        <p className="text-hint text-muted-foreground">An administrator can give them theirs.</p>
      )}
      <AddressesDialog
        open={open}
        onOpenChange={setOpen}
        addresses={addresses}
        projectId={projectId}
        projectName={projectName}
        stackId={stackId}
        pendingChanges={pendingChanges}
        onStarted={onStarted}
        onSettled={onSettled}
      />
    </Notice>
  )
}

/** The release "Give every app its address" started: the names it adds, and its run. */
export function AddressesStarted({
  started,
  projectId,
}: {
  started: SmartStackAddressesStarted
  projectId: number
}) {
  return (
    <Notice
      data-slot="stack-addresses"
      title="A deployment is giving every app its address"
      live="status"
    >
      <p>
        {started.domains.length > 0
          ? `${started.domains.join(", ")} ${started.domains.length === 1 ? "is" : "are"} added to the domains.`
          : "A deployment is publishing the saved addresses."}{" "}
        <Link href={`/deploy/${projectId}/runs/${started.runId}`} className={INLINE_LINK}>
          Watch the run
        </Link>
      </p>
    </Notice>
  )
}

function AddressesDialog({
  open,
  onOpenChange,
  addresses,
  projectId,
  projectName,
  stackId,
  pendingChanges,
  onStarted,
  onSettled,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  addresses: SmartStackAddresses
  projectId: number
  projectName: string
  stackId: string
  pendingChanges: boolean
  onStarted: (answer: SmartStackAddressesStarted) => void
  /** Nothing was missing after all: the page reads the stack again. */
  onSettled: () => void
}) {
  const republish = onlyUnpublished(addresses)
  const needsDomain = !republish && !addresses.primary
  // Without a primary domain the names are made here from the one typed, by
  // the labels the stack's catalogue gives each app.
  const catalogue = usePoll(
    (signal) => get<SmartStackCatalogueEntry[]>("/deploy/stacks", undefined, signal),
    0,
    [],
    { enabled: open && needsDomain },
  )
  const suggestion = usePoll(
    (signal) =>
      get<DeploymentHostnameSuggestion>(
        `/deploy/hostname?name=${encodeURIComponent(projectName)}`,
        undefined,
        signal,
      ),
    0,
    [projectName],
    { enabled: open && needsDomain },
  )
  const suggested =
    suggestion.data && suggestion.data.method !== "none" ? suggestion.data.hostname : ""
  const [typed, setTyped] = useState<string>()
  const domain = hostnameFromInput(typed ?? suggested)
  const labels = Object.fromEntries(
    (catalogue.data?.find((entry) => entry.id === stackId)?.services ?? []).map((service) => [
      service.name,
      service.address,
    ]),
  )
  const names = republish
    ? (addresses.unpublished ?? [])
    : needsDomain
      ? missingHostnames(addresses.missing, labels, domain)
      : addresses.missing
  const domainProblem = needsDomain
    ? domain
      ? hostnameProblem(domain, [])
      : "Required."
    : undefined
  const [busy, setBusy] = useState(false)
  const [refusal, setRefusal] = useState<string>()
  const [failure, setFailure] = useState<unknown>()

  const close = (next: boolean) => {
    if (busy) return
    if (!next) {
      setTyped(undefined)
      setRefusal(undefined)
      setFailure(undefined)
    }
    onOpenChange(next)
  }

  const submit = async () => {
    if (busy || domainProblem) return
    setBusy(true)
    setRefusal(undefined)
    setFailure(undefined)
    try {
      const answer = await post<SmartStackAddressesStarted>(
        `/deploy/${projectId}/stack/addresses`,
        needsDomain ? { domain } : {},
      )
      setBusy(false)
      onOpenChange(false)
      onStarted(answer)
    } catch (caught) {
      setBusy(false)
      const words = caught instanceof ApiError ? addressesRefusal(caught.code) : undefined
      if (caught instanceof ApiError && caught.code === "stack_addresses_present") {
        notify.success(words ?? "Every app already has its address.")
        onOpenChange(false)
        onSettled()
        return
      }
      if (words) setRefusal(words)
      else setFailure(caught)
    }
  }

  return (
    <Modal
      open={open}
      onOpenChange={close}
      title={republish ? "Publish the apps' addresses" : "Give every app its address"}
      footer={
        <>
          <Button variant="outline" onClick={() => close(false)} disabled={busy}>
            Cancel
          </Button>
          <Button pending={busy} disabled={Boolean(domainProblem)} onClick={() => void submit()}>
            {republish ? "Deploy again" : "Add the addresses and deploy"}
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        {needsDomain && (
          <Field
            label="The stack's domain"
            htmlFor="stack-addresses-domain"
            hint={
              typed === undefined && suggested
                ? `${suggestion.data?.detail ?? ""} Replace it with your own domain if you have one.`.trim()
                : "Jellyfin answers on it, and every other app beside it."
            }
            error={typed !== undefined ? domainProblem : undefined}
          >
            <Input
              id="stack-addresses-domain"
              value={typed ?? suggested}
              onChange={(event) => setTyped(event.target.value)}
              placeholder="media.example.com"
              className="font-mono"
              autoComplete="off"
              spellCheck={false}
            />
          </Field>
        )}
        <ul aria-label="The addresses the apps get" className="divide-y divide-hairline">
          {names.map((entry) => (
            <li key={entry.service} className="flex h-9 min-w-0 items-center gap-3">
              <span className="w-28 shrink-0 truncate text-body">{entry.title}</span>
              <span className="min-w-0 flex-1 truncate font-mono text-hint text-foreground">
                {entry.hostname || "beside the domain"}
              </span>
            </li>
          ))}
        </ul>
        <FormNote>
          {republish
            ? "The names are already among the deployment's domains; a deployment starts to serve them."
            : "The names are added to the deployment's domains, each with its certificate, and a deployment starts to serve them. They can be changed or removed on the Domains page afterwards."}
        </FormNote>
        {pendingChanges && <FormNote>{PENDING_CHANGES_NOTE}</FormNote>}
        {refusal && (
          <Notice tone="warning" title="Nothing was added">
            {refusal}
          </Notice>
        )}
        {failure !== undefined && (
          <Notice tone="danger" title="The addresses were not added">
            {failure instanceof Error ? failure.message : String(failure)}
          </Notice>
        )}
      </div>
    </Modal>
  )
}
