"use client"

import type { BlueprintDetail } from "@/lib/types"
import { FormNote } from "@/components/form"
import { ProductLogo, imageProduct } from "@/components/product-logo"
import { Tag } from "@/components/tag"

/**
 * What a template made from a Compose file or a deployment will ask, read off
 * the definition the server built in a dry run: what it runs, which answers
 * the next deployment gives when it chooses it, which secrets are asked for
 * on its Variables screen, and which are generated there. Nothing in it is a
 * value of the deployment it came from — the definition holds none.
 */
export function TemplatePreviewFacts({ definition }: { definition: BlueprintDetail }) {
  const inputs = definition.inputs ?? []
  const asked = inputs.filter((input) => input.kind !== "secret")
  const secrets = inputs.filter((input) => input.kind === "secret")
  const generated = definition.secrets ?? []
  const services = definition.services ?? []
  return (
    <div className="min-w-0 space-y-4" aria-label="What the template asks">
      <div className="min-w-0 space-y-1.5">
        <p className="eyebrow">Runs</p>
        {services.length > 0 ? (
          <ul className="min-w-0 space-y-1">
            {services.map((service) => {
              const owner = service.ports?.some((port) => port.exposure === "proxy")
              return (
                <li key={service.name} className="flex min-w-0 items-center gap-2 text-body">
                  <ProductLogo id={imageProduct(service.image.reference)} size="sm" />
                  <span className="font-mono text-foreground">{service.name}</span>
                  <span className="min-w-0 truncate font-mono text-hint text-muted-foreground">
                    {service.image.reference}
                  </span>
                  {owner && <Tag tone="success">Public</Tag>}
                </li>
              )
            })}
          </ul>
        ) : (
          <p className="flex min-w-0 items-center gap-2 text-body">
            <ProductLogo id={imageProduct(definition.image.reference)} size="sm" />
            <span className="min-w-0 truncate font-mono text-foreground">
              {definition.image.reference}
            </span>
          </p>
        )}
      </div>
      {asked.length > 0 && (
        <div className="min-w-0 space-y-1.5">
          <p className="eyebrow">Asked when it is chosen</p>
          <ul className="min-w-0 space-y-1 text-body">
            {asked.map((input) => (
              <li key={input.name} className="min-w-0">
                <span className="text-foreground">{input.label}</span>
                <span className="text-hint text-muted-foreground">
                  {input.default
                    ? ` — defaults to ${input.default}`
                    : input.required
                      ? " — required"
                      : " — optional"}
                </span>
              </li>
            ))}
          </ul>
        </div>
      )}
      {secrets.length > 0 && (
        <div className="min-w-0 space-y-1.5">
          <p className="eyebrow">Asked on the Variables screen</p>
          <p className="min-w-0 font-mono text-body break-words text-foreground">
            {secrets.map((input) => input.variable).join(", ")}
          </p>
        </div>
      )}
      {generated.length > 0 && (
        <div className="min-w-0 space-y-1.5">
          <p className="eyebrow">Generated for each deployment</p>
          <p className="min-w-0 font-mono text-body break-words text-foreground">
            {generated.map((secret) => secret.variable).join(", ")}
          </p>
        </div>
      )}
      <FormNote>
        No value of this server&apos;s is kept: a password becomes a secret the next deployment is
        asked for or generates, and its address is the domain it is given.
      </FormNote>
    </div>
  )
}
