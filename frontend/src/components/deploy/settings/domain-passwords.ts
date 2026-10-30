import type { DeploymentEnvironmentConfiguration } from "@/lib/types"

type DomainValue = DeploymentEnvironmentConfiguration["domains"][number]

/** Passwords typed and not yet saved, by hostname: what `useMemoryState` holds for the form. */
export type TypedPasswords = Record<string, string>

/** One name's typed password, set or, when empty, gone. */
export function withTypedPassword(
  typed: TypedPasswords,
  hostname: string,
  password: string,
): TypedPasswords {
  const next = { ...typed }
  if (password) next[hostname] = password
  else delete next[hostname]
  return next
}

/** A domain as the stored draft may hold it: the sealed hash, never the password. */
export function withoutPassword(domain: DomainValue): DomainValue {
  if (!domain.protection) return domain
  const protection = { ...domain.protection }
  delete protection.password
  return { ...domain, protection }
}

/**
 * The draft as the server is sent it. A password goes only with a name that
 * still asks for one, and an empty one is left off, which keeps the saved hash.
 */
export function withPasswords(domains: DomainValue[], typed: TypedPasswords): DomainValue[] {
  return domains.map((domain) => {
    const password = typed[domain.hostname]
    return domain.protection && password
      ? { ...domain, protection: { ...domain.protection, password } }
      : domain
  })
}
