"use client"

import { useState } from "react"
import { Monorepo, Warning } from "@/components/icons"
import { get } from "@/lib/api"
import { plural } from "@/lib/format"
import { passwordChecks } from "@/lib/password"
import { usePoll } from "@/hooks/use-poll"
import { useMemoryState, useSessionState } from "@/lib/view-state"
import type {
  BlueprintDetail,
  BlueprintInput,
  SmartStackCatalogueEntry,
  SmartStackCatalogueService,
  SmartStackPreset,
} from "@/lib/types"
import { Disclosure, FieldCheck, FieldChecks } from "@/components/form"
import { FlowActions, FlowPanel, FlowPanelBody, FlowPanelHeader } from "@/components/flow"
import { ChoiceCard, ChoiceGrid } from "@/components/choice-card"
import { ChipStrip, FilterChip } from "@/components/chips"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyState, ErrorState, Notice } from "@/components/state"
import { LoadingRows } from "@/components/loading"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { ProductLogo, hasProductLogo, imageProduct } from "@/components/product-logo"
import { SecretField } from "@/components/secret-field"
import { AccessPromise } from "@/components/deploy/first-sign-in"
import { discoveredEnvironmentRows } from "@/components/deploy/deployment-defaults"
import { deploymentName } from "@/components/deploy/vocabulary"
import {
  inspectAndPrepare,
  type ConfigureFlow,
  type EnvironmentDraft,
} from "@/components/deploy/new-project/draft"
import {
  addressSentence,
  chosenPreset,
  footprintWords,
  loginPasswordProblem,
  memoryWords,
  plannedAddresses,
  presetServices,
} from "@/components/deploy/stack/stack-model"
import { BlueprintField, useSuggestedDomain } from "./blueprint-field"
import { templateDraftSource, templateInputErrors } from "./template-inputs"
import { useSourceInspection } from "./use-source-inspection"

function asError(error: unknown) {
  return error instanceof Error ? error : new Error(String(error))
}

/**
 * A stack: several applications the dashboard installs together and then
 * connects to each other. What is chosen here is a preset, which is a
 * reviewed Compose template, so everything after this screen is the product's
 * own (the name, the address, the review and the deploy), and what the stack
 * adds, setting the applications up once they run, is read on the project's
 * Stack page.
 *
 * The screen says what a preset is before anything is pressed: what it runs
 * and how each service is reached, what it asks of the server, and what it
 * cannot do. Nothing here is offered that is not built: a preset is the
 * whole choice.
 *
 * The preset's options are its template's inputs, asked with the fields the
 * Template tab asks them with and held to the same rules, and remembered for
 * each preset, so trying the other one loses nothing typed. Two are the
 * stack's own: under the domain, the address each app gets beside it, by the
 * server's rule (`serviceHostname`); and the password every app signs in
 * with, optional, which is a secret and so is held in memory only and handed
 * to Configure's Variables as the variable the template declares for it.
 */
export function SourceStack({ onInspected }: { onInspected: (flow: ConfigureFlow) => void }) {
  const catalogue = usePoll(
    (signal) => get<SmartStackCatalogueEntry[]>("/deploy/stacks", undefined, signal),
    0,
  )
  const [stackId, setStackId] = useSessionState("deploy.new.stack.selected", "")
  const entries = catalogue.data ?? []
  const entry = entries.find((one) => one.id === stackId) ?? entries[0]
  const [presetId, setPresetId] = useSessionState(`deploy.new.stack.preset.${entry?.id ?? ""}`, "")
  const preset = entry && chosenPreset(entry, presetId || undefined)
  const templateId = preset?.templateId ?? ""
  const [inputs, setInputs] = useSessionState<Record<string, string>>(
    `deploy.new.stack.inputs.${templateId}`,
    {},
  )
  const [showAdvanced, setShowAdvanced] = useSessionState(
    `deploy.new.stack.advanced.${templateId}`,
    false,
  )
  // The password for every app is a secret: memory only, never the tab's storage.
  const [login, setLogin] = useMemoryState(`deploy.new.stack.login.${templateId}`, "")
  const [, setEnvironment] = useMemoryState<EnvironmentDraft | null>(
    "deploy.new.configure.environment",
    null,
  )
  const [busy, setBusy] = useState(false)
  const [attempted, setAttempted] = useState(false)
  const [failure, setFailure] = useState<Error>()
  const inspection = useSourceInspection(onInspected)

  const detail = usePoll(
    (signal) => get<BlueprintDetail>(`/deploy/blueprints/${templateId}`, undefined, signal),
    0,
    [templateId],
    { enabled: Boolean(templateId) },
  )
  const definition = detail.data?.id === templateId ? detail.data : undefined
  const declared = definition?.inputs ?? []
  // A field that must be filled is never behind a fold, as on the Template tab.
  const basic = declared.filter((input) => !input.advanced || input.required)
  const advanced = declared.filter((input) => input.advanced && !input.required)
  const blocked = definition?.deploymentSupported === false

  // The project is named for the stack, not for the preset: "media-server"
  // is what it is, and the preset is a fact about it.
  const projectName = entry ? deploymentName(entry.title) : ""
  const suggestion = useSuggestedDomain(definition, projectName, setInputs)

  const loginInput = declared.find((input) => input.kind === "secret" && input.variable)
  const errors = {
    ...templateInputErrors(declared, inputs),
    // Blank is the dashboard's to make; a typed one is held to the rule the
    // stack's page holds a change to.
    ...(loginInput && login ? { [loginInput.name]: loginPasswordProblem(login) } : {}),
  }
  const invalid = declared.filter((input) => errors[input.name])

  const choose = (next: SmartStackPreset) => {
    if (next.id === preset?.id) return
    inspection.cancel()
    setBusy(false)
    setAttempted(false)
    setFailure(undefined)
    setPresetId(next.id)
  }

  const proceed = async () => {
    if (!entry || !preset || !definition || blocked || busy) return
    setAttempted(true)
    if (invalid.length > 0) {
      if (invalid.some((input) => input.advanced && !input.required)) setShowAdvanced(true)
      setFailure(
        new Error(invalid.map((input) => `${input.label}: ${errors[input.name]}`).join(" ")),
      )
      return
    }
    setBusy(true)
    setFailure(undefined)
    let current = true
    try {
      const { profile, source } = templateDraftSource(definition, inputs)
      current = await inspection.inspect(async () => {
        const flow = await inspectAndPrepare(projectName, profile, source, {
          sourceLabel: definition.name,
        })
        // The password goes where Configure keeps a draft's typed variables,
        // so it is sent with them and nowhere else.
        if (loginInput?.variable && login)
          setEnvironment({
            draftId: flow.draft.id,
            rows: [
              ...discoveredEnvironmentRows(flow.candidate).filter(
                (row) => row.name !== loginInput.variable,
              ),
              {
                name: loginInput.variable,
                value: login,
                note: "Typed on the Stacks tab: the password every app signs in with",
              },
            ],
            dotenv: "",
            retainedKeys: flow.draft.environmentKeys ?? [],
          })
        return flow
      })
    } catch (error) {
      setFailure(asError(error))
    } finally {
      if (current) setBusy(false)
    }
  }

  if (catalogue.error && !catalogue.data)
    return <ErrorState error={catalogue.error} onRetry={catalogue.refresh} what="the stacks" />
  if (!catalogue.data)
    return (
      <div role="status" aria-label="Loading stacks">
        <LoadingRows rows={5} />
      </div>
    )
  if (!entry || !preset)
    return (
      <EmptyState
        icon={Monorepo}
        title="No stacks"
        description="This dashboard version has no stack to set up. A single application is on the Template tab."
      />
    )

  const services = presetServices(entry, preset)

  return (
    // The same two columns every source is: what is being chosen, read down
    // on the page's own ground, and beside it the one surface with depth,
    // which holds the preset's options and the command that leaves the
    // screen. Stacked, the choice and what it means come first, so the
    // limitations are read before the command is reached.
    <div className="grid min-w-0 items-start gap-x-6 gap-y-6 xl:h-full xl:min-h-0 xl:grid-cols-[minmax(0,1fr)_24rem] xl:grid-rows-fill xl:items-stretch">
      <Panel plain className="min-w-0 xl:min-h-0">
        <PanelHeader
          title={entry.title}
          actions={
            entries.length > 1 && (
              <ChipStrip role="group" aria-label="Stacks">
                {entries.map((one) => (
                  <FilterChip
                    key={one.id}
                    selected={one.id === entry.id}
                    onClick={() => setStackId(one.id)}
                  >
                    {one.title}
                  </FilterChip>
                ))}
              </ChipStrip>
            )
          }
        />
        <PanelBody className="flex min-h-0 flex-1 flex-col">
          <div className="-mx-3 space-y-5 px-3 pt-1 pb-3 xl:min-h-0 xl:flex-1 xl:overflow-y-auto">
            <div className="max-w-3xl space-y-1.5 text-body leading-relaxed text-muted-foreground">
              <p>{entry.description}</p>
              <p className="text-foreground">
                No films, series or sources are included. Media is kept in the stack&apos;s own
                volume, separate from the applications&apos; data.
              </p>
            </div>

            {/* Kinds, so cards (§16), and one choice out of the set, so a
                radiogroup: picking one unpicks the other. */}
            <ChoiceGrid mode="single" label="Preset" columns={2}>
              {entry.presets.map((one, index) => (
                <ChoiceCard
                  key={one.id}
                  index={index}
                  verb={`${one.title} preset`}
                  title={one.title}
                  description={one.description}
                  selected={one.id === preset.id}
                  onClick={() => choose(one)}
                  trailing={
                    <span className="mt-auto flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 pt-0.5">
                      {one.recommended && <Tag tone="success">Recommended</Tag>}
                      <Tag>{plural(one.services.length, "service")}</Tag>
                      <Tag>{memoryWords(one.memoryMb)} memory</Tag>
                    </span>
                  }
                />
              ))}
            </ChoiceGrid>

            <section className="min-w-0 space-y-2" aria-labelledby="stack-services">
              <p id="stack-services" className="eyebrow">
                What {preset.title} runs
              </p>
              <ul
                className="grid min-w-0 gap-x-6 sm:grid-cols-2"
                aria-label={`Services ${preset.title} runs`}
              >
                {services.map((service) => (
                  <li
                    key={service.name}
                    className="flex min-w-0 items-start gap-2.5 border-b border-hairline py-2"
                  >
                    <ProductLogo id={serviceProduct(service)} size="sm" />
                    <span className="min-w-0 flex-1 space-y-0.5">
                      <span className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
                        <span className="text-body font-medium text-foreground">
                          {service.title}
                        </span>
                        {service.access === "worker" && <Tag>Background</Tag>}
                      </span>
                      <span className="block text-hint leading-relaxed text-muted-foreground">
                        {service.purpose}
                      </span>
                    </span>
                  </li>
                ))}
              </ul>
              <p className="max-w-3xl text-hint leading-relaxed text-muted-foreground">
                {addressSentence(services)} {footprintWords(preset)}
              </p>
            </section>

            {(preset.limitations?.length ?? 0) > 0 && (
              <section className="min-w-0 space-y-2" aria-labelledby="stack-limitations">
                <p id="stack-limitations" className="eyebrow">
                  What it does not do
                </p>
                <ul
                  aria-label={`What ${preset.title} does not do`}
                  className="max-w-3xl list-disc space-y-1 pl-4 text-body leading-relaxed text-foreground"
                >
                  {preset.limitations?.map((limitation) => (
                    <li key={limitation}>{limitation}</li>
                  ))}
                </ul>
              </section>
            )}

            {/* The pins are for whoever checks them, not a step of choosing. */}
            <Disclosure quiet summary="Pinned images">
              <ul className="min-w-0 space-y-1" aria-label="Pinned images">
                {services.map((service) => (
                  <li
                    key={service.name}
                    className="flex min-w-0 flex-wrap items-baseline gap-x-2 text-hint"
                  >
                    <span className="shrink-0 text-foreground">{service.title}</span>
                    <span className="min-w-0 font-mono break-all text-muted-foreground">
                      {service.image}
                    </span>
                  </li>
                ))}
              </ul>
            </Disclosure>
          </div>
        </PanelBody>
      </Panel>

      {/* The one surface with depth on this screen (§16): the preset's options
          and the command that takes them to Configure. */}
      <FlowPanel
        id="stack-settings"
        aria-label="Stack settings"
        className="min-w-0 scroll-mt-4 xl:max-h-full xl:min-h-0 xl:self-start"
        aria-busy={detail.loading || busy}
      >
        <FlowPanelHeader
          title={`${entry.title} · ${preset.title}`}
          actions={definition && <Tag mono>v{definition.version}</Tag>}
        />
        <FlowPanelBody className="space-y-4 xl:min-h-0 xl:flex-1 xl:overflow-y-auto">
          {detail.loading && !definition && (
            <div role="status" aria-label="Loading stack settings">
              <LoadingRows rows={4} />
            </div>
          )}
          {detail.error && (
            <ErrorState
              error={detail.error}
              onRetry={detail.refresh}
              what="this preset's settings"
            />
          )}
          {/* Beside the command that raised it: what Continue refused, or
              what the inspection it started ran into. */}
          {failure && <ErrorState error={failure} />}
          {definition && (
            <fieldset disabled={busy} className="min-w-0 space-y-4">
              {blocked && (
                <Notice tone="warning" icon={Warning} title="This preset cannot be deployed here">
                  {definition.unavailableReason || "Choose the other preset."}
                </Notice>
              )}
              <AccessPromise access={definition.access} />
              {basic.map((input) =>
                input === loginInput ? (
                  <LoginField
                    key={input.name}
                    input={input}
                    value={login}
                    error={attempted ? errors[input.name] : undefined}
                    onChange={setLogin}
                  />
                ) : (
                  <div key={input.name} className="min-w-0 space-y-2">
                    <BlueprintField
                      input={input}
                      value={inputs[input.name] ?? input.default ?? ""}
                      error={attempted ? errors[input.name] : undefined}
                      suggested={input.kind === "domain" ? suggestion : undefined}
                      onChange={(value) =>
                        setInputs((current) => ({ ...current, [input.name]: value }))
                      }
                    />
                    {input.kind === "domain" && (
                      <AddressPreview
                        services={services}
                        domain={inputs[input.name] ?? input.default ?? ""}
                      />
                    )}
                  </div>
                ),
              )}
              {advanced.length > 0 && (
                <Disclosure
                  quiet
                  summary="Advanced settings"
                  open={showAdvanced}
                  onOpenChange={setShowAdvanced}
                >
                  {showAdvanced && (
                    <div className="space-y-4">
                      {advanced.map((input) =>
                        input === loginInput ? (
                          <LoginField
                            key={input.name}
                            input={input}
                            value={login}
                            error={attempted ? errors[input.name] : undefined}
                            onChange={setLogin}
                          />
                        ) : (
                          <BlueprintField
                            key={input.name}
                            input={input}
                            value={inputs[input.name] ?? input.default ?? ""}
                            error={attempted ? errors[input.name] : undefined}
                            onChange={(value) =>
                              setInputs((current) => ({ ...current, [input.name]: value }))
                            }
                          />
                        ),
                      )}
                    </div>
                  )}
                </Disclosure>
              )}
            </fieldset>
          )}
        </FlowPanelBody>
        <FlowActions note="Nothing is created until you deploy. The name, the address and the review come next.">
          <Button pending={busy} disabled={!definition || blocked} onClick={() => void proceed()}>
            Continue
          </Button>
        </FlowActions>
      </FlowPanel>
    </div>
  )
}

/**
 * The address each app gets from the domain above, as the server will make
 * them: one line each, the name in mono, so what Deploy creates is read
 * before it is pressed. Nothing is drawn until there is a domain.
 */
function AddressPreview({
  services,
  domain,
}: {
  services: SmartStackCatalogueService[]
  domain: string
}) {
  const addresses = plannedAddresses(services, domain)
  if (addresses.length === 0) return null
  return (
    <ul aria-label="The address each app gets" className="min-w-0 divide-y divide-hairline">
      {addresses.map((address) => (
        <li key={address.service} className="flex h-7 min-w-0 items-center gap-3 text-hint">
          <span className="w-24 shrink-0 truncate text-muted-foreground">{address.title}</span>
          <span
            className="min-w-0 flex-1 truncate font-mono text-foreground"
            title={address.hostname}
          >
            {address.hostname}
          </span>
        </li>
      ))}
    </ul>
  )
}

/**
 * The password every app signs in with: optional, since a blank one is made
 * by the dashboard, and held to the dashboard's own rule when typed. Hidden
 * while typed, with Show, Generate and Copy in the box.
 */
function LoginField({
  input,
  value,
  error,
  onChange,
}: {
  input: BlueprintInput
  value: string
  error?: string
  onChange: (value: string) => void
}) {
  const checks = passwordChecks(value)
  return (
    <SecretField
      id={`blueprint-${input.name}`}
      label={input.label}
      value={value}
      onChange={onChange}
      shown={false}
      error={error}
      hint={
        value ? (
          <FieldChecks>
            <FieldCheck met={checks.long}>12 characters or more</FieldCheck>
            <FieldCheck met={checks.mixed}>
              three of upper case, lower case, digits, symbols
            </FieldCheck>
          </FieldChecks>
        ) : (
          input.description ||
          "Optional. Leave it empty and the dashboard makes one; the stack's page shows it."
        )
      }
    />
  )
}

/** A service's mark: its own where the product has one, else what its image is. */
function serviceProduct(service: Pick<SmartStackCatalogueService, "name" | "image">) {
  return hasProductLogo(service.name) ? service.name : imageProduct(service.image)
}
