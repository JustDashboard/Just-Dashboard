import type { ForwardAuthProvider } from "@/lib/types"

/**
 * The auth servers single sign-on is through, with the check address and
 * sign-in page each usually answers at. A Proxy site and a deployment's
 * public route offer the same four, because both are written with nginx's
 * auth_request convention — the deployment route on Caddy too.
 */
export const SSO_PROVIDERS: {
  value: ForwardAuthProvider
  label: string
  verify: string
  signIn: string
  sends: string
}[] = [
  {
    value: "authelia",
    label: "Authelia",
    verify: "http://127.0.0.1:9091/api/authz/auth-request",
    signIn: "https://auth.example.com/",
    sends: "Remote-User and Remote-Email",
  },
  {
    value: "authentik",
    label: "Authentik",
    verify: "http://127.0.0.1:9000/outpost.goauthentik.io/auth/nginx",
    signIn: "https://authentik.example.com/outpost.goauthentik.io/start",
    sends: "X-authentik-username and X-authentik-email",
  },
  {
    value: "oauth2-proxy",
    label: "oauth2-proxy",
    verify: "http://127.0.0.1:4180/oauth2/auth",
    signIn: "https://auth.example.com/oauth2/start",
    sends: "X-Auth-Request-User and X-Auth-Request-Email (run it with --set-xauthrequest)",
  },
  {
    value: "custom",
    label: "Other",
    verify: "",
    signIn: "https://auth.example.com/login",
    sends: "Remote-User and Remote-Email",
  },
]

/** The preset for a provider, Authelia's when there is none. */
export function ssoPreset(provider: ForwardAuthProvider | undefined) {
  return SSO_PROVIDERS.find((p) => p.value === provider) ?? SSO_PROVIDERS[0]
}

/**
 * The change choosing another provider makes. A check address still at
 * another preset's default follows the choice; one the operator typed stays.
 */
export function chooseSsoProvider(
  current: { verify?: string } | undefined,
  provider: ForwardAuthProvider,
): { provider: ForwardAuthProvider; verify?: string } {
  const next = SSO_PROVIDERS.find((p) => p.value === provider)
  const typed = current?.verify && !SSO_PROVIDERS.some((p) => p.verify === current.verify)
  return { provider, ...(next && !typed ? { verify: next.verify } : {}) }
}
