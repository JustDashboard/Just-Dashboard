"use client"

import { Copy, External } from "@/components/icons"
import { copyText } from "@/lib/clipboard"
import type { SmartStackService, SmartStackState } from "@/lib/types"
import { ChoiceList, ChoiceListSkeleton, ChoiceRow } from "@/components/choice-row"
import { DimActions } from "@/components/icon-action"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ProductLogo, hasProductLogo, imageProduct } from "@/components/product-logo"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { VerbMenu, type VerbSpec } from "@/components/verbs"
import { hostOf } from "@/components/deploy/vocabulary"
import { appReach, serviceState } from "@/components/deploy/stack/stack-model"

type RuntimeContainer = { containerId: string; name: string; service?: string }

/** A service's mark: its own where the product has one, else what its image is. */
export function serviceProduct(service: Pick<SmartStackService, "name" | "image">) {
  return hasProductLogo(service.name) ? service.name : imageProduct(service.image)
}

/**
 * The stack's apps as a launcher, one row each and one height in every
 * state: what it is, what it is for, its address, its state, and **Open**,
 * which opens it at its address in a new tab. A worker has no interface and
 * says it runs in the background; an app with no address yet says so, with
 * the loopback address it is published on as the line's tooltip, and offers
 * no button that would open nothing. Its container and its address's copy are
 * behind the row's menu, in words.
 */
export function StackApps({
  state,
  runtime,
}: {
  state: SmartStackState
  runtime: RuntimeContainer[]
}) {
  // The stack names a container; the project's runtime read holds its id,
  // which is what the Docker pages are addressed by.
  const containerId = (service: SmartStackService) =>
    runtime.find(
      (one) =>
        one.name === service.container?.name || (one.service && one.service === service.name),
    )?.containerId
  return (
    <Panel plain data-slot="stack-apps">
      <PanelHeader title="Apps" count={state.services.length} />
      <PanelBody className="pt-3">
        <ChoiceList aria-label="Apps">
          {state.services.map((service) => {
            const id = service.container ? containerId(service) : undefined
            const reading = serviceState(service)
            const reach = appReach(service)
            const verbs: VerbSpec[] = [
              ...(reach.kind === "open"
                ? [
                    {
                      key: "copy",
                      label: "Copy the address",
                      icon: Copy,
                      run: () => void copyText(reach.url, `${service.title}'s address copied`),
                    },
                  ]
                : []),
              ...(id
                ? [
                    {
                      key: "container",
                      label: "Open its container",
                      icon: External,
                      href: `/docker/containers/${id}`,
                      internal: true,
                    },
                  ]
                : []),
            ]
            return (
              <ChoiceRow
                key={service.name}
                // A launcher's row is read, and its one verb is the Open beside it.
                disabled
                arrow={false}
                verb={service.title}
                leading={<ProductLogo id={serviceProduct(service)} size="sm" />}
                title={service.title}
                description={service.purpose}
                trailing={
                  <>
                    <AppAddress reach={reach} />
                    <span className="flex justify-end sm:w-28 sm:justify-start">
                      <Status tone={reading.tone} label={reading.label} />
                    </span>
                    <span className="flex justify-end sm:w-20">
                      {reach.kind === "open" && (
                        <Button size="sm" variant="outline" asChild>
                          <a
                            href={reach.url}
                            target="_blank"
                            rel="noopener noreferrer"
                            aria-label={`Open ${service.title} at ${hostOf(reach.url)}`}
                          >
                            <External />
                            <span className="max-sm:hidden">Open</span>
                          </a>
                        </Button>
                      )}
                    </span>
                  </>
                }
                actions={
                  verbs.length > 0 ? (
                    <DimActions>
                      <VerbMenu verbs={verbs} glyphs={false} label={`More for ${service.title}`} />
                    </DimActions>
                  ) : (
                    // The menu's room, so every row's Open stands on one edge.
                    <span aria-hidden className="size-8 shrink-0" />
                  )
                }
              />
            )
          })}
        </ChoiceList>
      </PanelBody>
    </Panel>
  )
}

/** Where an app answers, in the row's line: a phone keeps the state and the Open, and drops this. */
function AppAddress({ reach }: { reach: ReturnType<typeof appReach> }) {
  if (reach.kind === "open")
    return (
      <span className="max-w-56 truncate font-mono text-hint text-foreground max-md:hidden">
        {hostOf(reach.url)}
      </span>
    )
  if (reach.kind === "background")
    return <span className="text-hint text-muted-foreground max-md:hidden">In the background</span>
  return (
    <span title={reach.detail} className="text-hint text-muted-foreground max-md:hidden">
      No address yet
    </span>
  )
}

/** The apps' shape while the first read is in flight. */
export function StackAppsSkeleton() {
  return (
    <Panel plain>
      <PanelHeader title="Apps" />
      <PanelBody className="pt-3">
        <ChoiceListSkeleton rows={4} />
      </PanelBody>
    </Panel>
  )
}
