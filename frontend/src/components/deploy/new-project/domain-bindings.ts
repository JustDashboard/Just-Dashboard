import type { DeploymentConfiguration } from "@/lib/types"

type Domain = DeploymentConfiguration["domains"][number]

/** A domain-bound template's value for a domain; empty until one is planned. */
export function domainValue(template: string, domain: Domain | undefined) {
  const hostname = domain?.hostname.trim().toLowerCase()
  if (!hostname) return ""
  return template
    .replaceAll("{{hostname}}", hostname)
    .replaceAll("{{scheme}}", domain?.https ? "https" : "http")
}

/** Keep reviewed application URLs aligned with the route until explicitly overridden. */
export function synchronizePrimaryDomain(
  configuration: DeploymentConfiguration,
  domains: Domain[],
): DeploymentConfiguration {
  return {
    ...configuration,
    domains,
    variables: configuration.variables.map((variable) => {
      if (
        !variable.domainTemplate ||
        variable.reference ||
        variable.generate ||
        variable.sensitivity !== "plain" ||
        (variable.value ?? "") !== domainValue(variable.domainTemplate, configuration.domains[0])
      )
        return variable
      return { ...variable, value: domainValue(variable.domainTemplate, domains[0]) }
    }),
  }
}
