import { useSyncExternalStore } from "react"
import { post } from "@/lib/api"
import type { AuthStatus, Passkey } from "@/lib/types"

/**
 * Passkeys in the browser: the server's WebAuthn options in, the
 * authenticator's answer out, as the base64url JSON the server reads. The
 * conversion is done here rather than with `parseCreationOptionsFromJSON`,
 * which not every browser that has passkeys has yet.
 */

export function toBase64Url(buffer: ArrayBuffer | Uint8Array) {
  const bytes = buffer instanceof Uint8Array ? buffer : new Uint8Array(buffer)
  let binary = ""
  for (const byte of bytes) binary += String.fromCharCode(byte)
  return btoa(binary).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "")
}

export function fromBase64Url(value: string): ArrayBuffer {
  const base64 = value.replace(/-/g, "+").replace(/_/g, "/")
  const padded = base64 + "=".repeat((4 - (base64.length % 4)) % 4)
  const binary = atob(padded)
  const bytes = new Uint8Array(binary.length)
  for (let index = 0; index < binary.length; index++) bytes[index] = binary.charCodeAt(index)
  return bytes.buffer
}

/**
 * Whether a passkey can be used where the page is open, and why not. WebAuthn
 * needs a secure context (https, or http on localhost) and a relying party
 * that is a name: a dashboard opened at its IP address — a tailnet address, a
 * LAN one — can hold no passkey, and saying so beats a button that fails.
 */
export type PasskeyPlace = { usable: true } | { usable: false; reason: string }

const POLICY_FEATURE = "publickey-credentials-get"

/** What to say, and what to change, where the page's policy forbids a passkey. */
export const PASSKEY_POLICY_REASON = `This page's Permissions-Policy forbids passkeys, so the browser will not offer one. The proxy in front of this dashboard has to send ${POLICY_FEATURE}=(self).`

type PolicyReader = {
  allowsFeature?: (feature: string) => boolean
  features?: () => string[]
}

/**
 * Whether the document's Permissions-Policy leaves passkeys on. A browser that
 * cannot say, or does not know the feature's name, is taken as allowing it:
 * the refusal then arrives as the error `refusedByPolicy` reads.
 */
export function policyAllowsPasskeys(doc: {
  permissionsPolicy?: PolicyReader
  featurePolicy?: PolicyReader
}) {
  const policy = doc.permissionsPolicy ?? doc.featurePolicy
  if (!policy?.allowsFeature || !policy.features?.().includes(POLICY_FEATURE)) return true
  return policy.allowsFeature(POLICY_FEATURE)
}

/**
 * Whether a NotAllowedError is the page's policy rather than the person. Both
 * are the same error, and closing the prompt is a choice nobody should be told
 * off for, so the only thing to tell them apart by is what the browser wrote.
 */
export function refusedByPolicy(err: unknown) {
  return (
    err instanceof Error &&
    err.name === "NotAllowedError" &&
    /permissions.?policy|not enabled in this document/i.test(err.message)
  )
}

export function passkeyPlace(where: {
  hostname: string
  secure: boolean
  supported: boolean
  /** The document's policy; only a page that can read it says no. */
  allowedByPolicy?: boolean
}): PasskeyPlace {
  if (!where.supported) return { usable: false, reason: "This browser has no passkeys." }
  const host = where.hostname.replace(/^\[|\]$/g, "")
  if (/^\d{1,3}(\.\d{1,3}){3}$/.test(host) || host.includes(":")) {
    return {
      usable: false,
      reason: `Passkeys need this dashboard's name, and it is open at the address ${host}. Open it by its host name to use one.`,
    }
  }
  if (!where.secure) {
    return { usable: false, reason: "Passkeys need https, or http on localhost." }
  }
  if (where.allowedByPolicy === false) return { usable: false, reason: PASSKEY_POLICY_REASON }
  return { usable: true }
}

let place: PasskeyPlace | undefined

function placeSnapshot(): PasskeyPlace {
  place ??= passkeyPlace({
    hostname: window.location.hostname,
    secure: window.isSecureContext,
    supported: typeof window.PublicKeyCredential === "function",
    allowedByPolicy: policyAllowsPasskeys(document as Parameters<typeof policyAllowsPasskeys>[0]),
  })
  return place
}

const unchanging = () => () => undefined

/**
 * Where the page is open, for a component: nothing during the server's
 * render, which has no location, and the reading on the client. A page does
 * not change host without a navigation, so it is read once.
 */
export function usePasskeyPlace(): PasskeyPlace | null {
  return useSyncExternalStore(unchanging, placeSnapshot, () => null)
}

function hostnameSnapshot() {
  return window.location.hostname
}

/** The host name the page is open at; empty during the server's render. */
export function useHostname() {
  return useSyncExternalStore(unchanging, hostnameSnapshot, () => "")
}

type CredentialDescriptorJSON = { id: string; type: string; transports?: string[] }

type CreationOptionsJSON = {
  challenge: string
  rp: PublicKeyCredentialRpEntity
  user: { id: string; name: string; displayName: string }
  pubKeyCredParams: PublicKeyCredentialParameters[]
  timeout?: number
  excludeCredentials?: CredentialDescriptorJSON[]
  authenticatorSelection?: AuthenticatorSelectionCriteria
  attestation?: AttestationConveyancePreference
}

type RequestOptionsJSON = {
  challenge: string
  timeout?: number
  rpId?: string
  allowCredentials?: CredentialDescriptorJSON[]
  userVerification?: UserVerificationRequirement
}

type Begin<T> = { challengeId: string; publicKey: T }

function descriptor(item: CredentialDescriptorJSON): PublicKeyCredentialDescriptor {
  return {
    id: fromBase64Url(item.id),
    type: "public-key",
    transports: item.transports as AuthenticatorTransport[] | undefined,
  }
}

export function creationOptions(json: CreationOptionsJSON): PublicKeyCredentialCreationOptions {
  return {
    ...json,
    challenge: fromBase64Url(json.challenge),
    user: { ...json.user, id: fromBase64Url(json.user.id) },
    excludeCredentials: (json.excludeCredentials ?? []).map(descriptor),
  }
}

export function requestOptions(json: RequestOptionsJSON): PublicKeyCredentialRequestOptions {
  return {
    ...json,
    challenge: fromBase64Url(json.challenge),
    allowCredentials: (json.allowCredentials ?? []).map(descriptor),
  }
}

/** A credential as the server reads it: every buffer base64url. */
export function credentialJSON(credential: PublicKeyCredential) {
  const response = credential.response as AuthenticatorAttestationResponse &
    AuthenticatorAssertionResponse
  const out: Record<string, unknown> = {
    id: credential.id,
    rawId: toBase64Url(credential.rawId),
    type: credential.type,
    authenticatorAttachment: credential.authenticatorAttachment ?? undefined,
    clientExtensionResults: credential.getClientExtensionResults(),
  }
  const body: Record<string, unknown> = { clientDataJSON: toBase64Url(response.clientDataJSON) }
  if ("attestationObject" in response && response.attestationObject) {
    body.attestationObject = toBase64Url(response.attestationObject)
    body.transports = response.getTransports?.() ?? []
  }
  if ("authenticatorData" in response && response.authenticatorData) {
    body.authenticatorData = toBase64Url(response.authenticatorData)
    body.signature = toBase64Url(response.signature)
    if (response.userHandle) body.userHandle = toBase64Url(response.userHandle)
  }
  out.response = body
  return out
}

/**
 * Whether the browser can offer passkeys in the username field's autofill
 * (conditional mediation), so the sign-in page can ask for one quietly.
 */
export async function conditionalSignInAvailable() {
  if (typeof window === "undefined" || typeof window.PublicKeyCredential !== "function")
    return false
  const check = (
    window.PublicKeyCredential as unknown as {
      isConditionalMediationAvailable?: () => Promise<boolean>
    }
  ).isConditionalMediationAvailable
  return check ? check().catch(() => false) : false
}

/**
 * Signs in with a passkey: the server's challenge, the browser's prompt (or,
 * conditionally, its autofill), the server's verdict — which, for an account
 * with an authenticator, still asks for the code.
 */
export async function signInWithPasskey(options: {
  conditional?: boolean
  signal?: AbortSignal
}): Promise<AuthStatus> {
  const begin = await post<Begin<RequestOptionsJSON>>("/auth/passkey/begin")
  const credential = (await navigator.credentials.get({
    publicKey: requestOptions(begin.publicKey),
    signal: options.signal,
    ...(options.conditional ? { mediation: "conditional" as CredentialMediationRequirement } : {}),
  })) as PublicKeyCredential | null
  if (!credential) throw new Error("No passkey was chosen.")
  return post<AuthStatus>("/auth/passkey/finish", {
    challengeId: begin.challengeId,
    credential: credentialJSON(credential),
  })
}

/**
 * Registers a passkey for the signed-in account: the password and code again,
 * then the browser's prompt, then the server keeps it under the given name.
 */
export async function registerPasskey(input: {
  password: string
  code: string
  name: string
}): Promise<Passkey> {
  const begin = await post<Begin<CreationOptionsJSON>>("/account/passkeys/register/begin", {
    password: input.password,
    code: input.code,
  })
  const credential = (await navigator.credentials.create({
    publicKey: creationOptions(begin.publicKey),
  })) as PublicKeyCredential | null
  if (!credential) throw new Error("No passkey was made.")
  return post<Passkey>("/account/passkeys/register/finish", {
    challengeId: begin.challengeId,
    name: input.name,
    credential: credentialJSON(credential),
  })
}

/** A name for a new passkey from where it is being made: "Chrome on Linux". */
export function suggestedPasskeyName(userAgent: string) {
  const browser = /Edg\//.test(userAgent)
    ? "Edge"
    : /Firefox\//.test(userAgent)
      ? "Firefox"
      : /Chrome\//.test(userAgent)
        ? "Chrome"
        : /Safari\//.test(userAgent)
          ? "Safari"
          : "Browser"
  const system = /iPhone|iPad/.test(userAgent)
    ? "iOS"
    : /Android/.test(userAgent)
      ? "Android"
      : /Mac OS X/.test(userAgent)
        ? "macOS"
        : /Windows/.test(userAgent)
          ? "Windows"
          : /Linux/.test(userAgent)
            ? "Linux"
            : ""
  return system ? `${browser} on ${system}` : browser
}
