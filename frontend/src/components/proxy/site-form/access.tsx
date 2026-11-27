"use client"

import { SSO_PROVIDERS, chooseSsoProvider, ssoPreset } from "@/lib/sso-providers"
import { get } from "@/lib/api"
import type {
  AuthFile,
  SiteClientCert,
  SiteForwardAuth,
  ForwardAuthProvider,
  SiteSpec,
} from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { FormNote, OptionList, OptionRow } from "@/components/form"
import { Input } from "@/components/ui/input"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { Field, Part, ListField } from "@/components/proxy/site-form/fields"

/** GET /proxy/access-lists/, as far as the form reads it. */
type AccessListNames = { lists: { name: string }[] }

export function AccessSection({
  spec,
  set,
  open,
  authFiles,
}: {
  spec: SiteSpec
  set: <K extends keyof SiteSpec>(key: K, value: SiteSpec[K]) => void
  open: boolean
  authFiles: AuthFile[] | undefined
}) {
  const shared = spec.accessList !== undefined
  const lists = usePoll<AccessListNames>(
    (signal) => get("/proxy/access-lists/", undefined, signal),
    0,
    [],
    { enabled: open && shared },
  )
  const cert = spec.clientCert
  const setCert = (patch: Partial<SiteClientCert>) =>
    set("clientCert", { caPath: "", ...cert, ...patch })

  return (
    <Part title="Who may reach it">
      {spec.kind !== "redirect" && <SingleSignOn spec={spec} set={set} />}
      {spec.forwardAuth && spec.kind !== "redirect" ? (
        <ListField
          id="site-allow"
          label="Allow only these"
          placeholder="10.0.0.0/8"
          values={spec.allowFrom}
          onChange={(v) => set("allowFrom", v)}
          hint="Checked as well as signing in: an address outside it is refused before the sign-in page."
        />
      ) : (
        <AddressesAndPassword spec={spec} set={set} lists={lists.data} authFiles={authFiles} />
      )}
      {spec.tls && (
        <OptionList>
          <OptionRow
            title="Require a client certificate"
            hint="Visitors must present a certificate signed by your CA. Only for a site that redirects plain HTTP, which would otherwise serve it without one."
            checked={Boolean(cert)}
            disabled={!spec.forceHttps && !cert}
            onCheckedChange={(on) => set("clientCert", on ? { caPath: "" } : undefined)}
          >
            {cert && (
              <div className="flex flex-col gap-3">
                {!spec.forceHttps && (
                  <FormNote tone="warning">
                    Redirect plain HTTP to HTTPS, or port 80 serves the site without a certificate.
                    The save is refused until then.
                  </FormNote>
                )}
                <Field
                  label="CA certificate"
                  htmlFor="site-client-ca"
                  tip="The PEM file of the CA, or CAs, a client certificate must be signed by."
                >
                  <Input
                    id="site-client-ca"
                    value={cert.caPath}
                    onChange={(e) => setCert({ caPath: e.target.value.trim() })}
                    placeholder="/etc/nginx/client-ca.pem"
                    className="font-mono text-hint"
                  />
                </Field>
                <ToggleGroup
                  type="single"
                  aria-label="Without a certificate"
                  value={cert.mode ?? "required"}
                  onValueChange={(v) => {
                    if (v === "required") setCert({ mode: undefined })
                    if (v === "optional") setCert({ mode: "optional", passSubject: true })
                  }}
                  variant="outline"
                  size="sm"
                  className="w-full"
                >
                  <ToggleGroupItem value="required" className="flex-1 text-hint">
                    Refuse with 400
                  </ToggleGroupItem>
                  <ToggleGroupItem
                    value="optional"
                    className="flex-1 text-hint"
                    disabled={spec.kind !== "proxy"}
                  >
                    Let the application decide
                  </ToggleGroupItem>
                </ToggleGroup>
                {cert.mode === "optional" && (
                  <FormNote tone="warning">
                    A request without a valid certificate still reaches the application, which has
                    to refuse it itself by X-Client-Verify.
                  </FormNote>
                )}
                {spec.kind === "proxy" && (
                  <OptionList>
                    <OptionRow
                      title="Tell the application"
                      hint="Sends X-Client-Verify (SUCCESS, NONE or why it failed) and X-Client-Subject, replacing any the visitor sent."
                      checked={cert.passSubject ?? false}
                      disabled={cert.mode === "optional"}
                      onCheckedChange={(v) => setCert({ passSubject: v || undefined })}
                    />
                  </OptionList>
                )}
              </div>
            )}
          </OptionRow>
        </OptionList>
      )}
    </Part>
  )
}

/** Single sign-on through an auth server nginx asks about each request. */
function SingleSignOn({
  spec,
  set,
}: {
  spec: SiteSpec
  set: <K extends keyof SiteSpec>(key: K, value: SiteSpec[K]) => void
}) {
  const sso = spec.forwardAuth
  const preset = ssoPreset(sso?.provider)
  const update = (patch: Partial<SiteForwardAuth>) =>
    set("forwardAuth", { provider: "authelia", verify: "", signIn: "", ...sso, ...patch })
  const choose = (provider: ForwardAuthProvider) => update(chooseSsoProvider(sso, provider))
  const conflicts = [
    (spec.basicAuthFile || spec.accessList !== undefined) && "the password and access list",
    spec.interceptErrors && "replacing the application's own error pages",
  ].filter(Boolean)
  return (
    <OptionList>
      <OptionRow
        title="Sign in through an auth server"
        hint="Every request is checked with Authelia, Authentik, oauth2-proxy or another server first. A visitor without a session is sent to sign in and brought back; if the server refuses or fails, so does the site."
        checked={Boolean(sso)}
        onCheckedChange={(on) =>
          set(
            "forwardAuth",
            on ? { provider: "authelia", verify: SSO_PROVIDERS[0].verify, signIn: "" } : undefined,
          )
        }
      >
        {sso && (
          <div className="flex flex-col gap-3">
            <ToggleGroup
              type="single"
              aria-label="Auth server"
              value={sso.provider}
              onValueChange={(v) => v && choose(v as ForwardAuthProvider)}
              variant="outline"
              size="sm"
              className="w-full flex-wrap"
            >
              {SSO_PROVIDERS.map((p) => (
                <ToggleGroupItem key={p.value} value={p.value} className="flex-1 text-hint">
                  {p.label}
                </ToggleGroupItem>
              ))}
            </ToggleGroup>
            <Field
              label="Check address"
              htmlFor="site-sso-verify"
              tip="Where nginx asks, with the visitor's headers and the address they asked for, without the body."
            >
              <Input
                id="site-sso-verify"
                value={sso.verify}
                onChange={(e) => update({ verify: e.target.value.trim() })}
                placeholder={preset.verify || "http://127.0.0.1:8080/verify"}
                className="font-mono text-hint"
              />
            </Field>
            <Field
              label="Sign-in page"
              htmlFor="site-sso-signin"
              tip="A 401 sends the visitor here, with rd= set to the address they asked for."
            >
              <Input
                id="site-sso-signin"
                value={sso.signIn}
                onChange={(e) => update({ signIn: e.target.value.trim() })}
                placeholder={preset.signIn}
                className="font-mono text-hint"
              />
            </Field>
            {spec.kind === "proxy" && (
              <ToggleGroup
                type="single"
                aria-label="Where to sign in"
                value={sso.pathsOnly ? "paths" : "site"}
                onValueChange={(v) => v && update({ pathsOnly: v === "paths" || undefined })}
                variant="outline"
                size="sm"
                className="w-full"
              >
                <ToggleGroupItem value="site" className="flex-1 text-hint">
                  Every path
                </ToggleGroupItem>
                <ToggleGroupItem value="paths" className="flex-1 text-hint">
                  Chosen paths
                </ToggleGroupItem>
              </ToggleGroup>
            )}
            <FormNote>
              {spec.kind === "proxy"
                ? `The application is sent Remote-User and Remote-Email, taken from the server's ${preset.sends}, and never the visitor's own.`
                : "Only an application behind a proxy site is told who signed in."}{" "}
              {spec.kind === "proxy" &&
                (sso.pathsOnly
                  ? "Choose the paths under Paths that go somewhere else."
                  : "A path can skip it under Paths that go somewhere else.")}
            </FormNote>
            {conflicts.length > 0 && (
              <FormNote tone="warning">
                Signing in replaces {conflicts.join(" and ")}.{" "}
                {spec.interceptErrors
                  ? "Turn that off under Pages & files, or the application's own 401s would be sent to sign in; the save is refused until then."
                  : "They are cleared on save."}
              </FormNote>
            )}
          </div>
        )}
      </OptionRow>
    </OptionList>
  )
}

/** The site's own addresses and password, or a shared list in their place. */
function AddressesAndPassword({
  spec,
  set,
  lists,
  authFiles,
}: {
  spec: SiteSpec
  set: <K extends keyof SiteSpec>(key: K, value: SiteSpec[K]) => void
  lists: AccessListNames | undefined
  authFiles: AuthFile[] | undefined
}) {
  const shared = spec.accessList !== undefined
  return (
    <>
      <OptionList>
        <OptionRow
          title="Use a shared access list"
          hint="The addresses and password come from a list kept on the Sites page, so one edit there changes every site that takes it in."
          checked={shared}
          onCheckedChange={(on) => set("accessList", on ? "" : undefined)}
        >
          {shared && (
            <Field label="List" htmlFor="site-access-list">
              <Input
                id="site-access-list"
                value={spec.accessList ?? ""}
                onChange={(e) => set("accessList", e.target.value.trim())}
                placeholder="office"
                list="site-access-lists"
                className="font-mono text-hint"
              />
              <datalist id="site-access-lists">
                {lists?.lists.map((l) => (
                  <option key={l.name} value={l.name} />
                ))}
              </datalist>
            </Field>
          )}
        </OptionRow>
      </OptionList>
      {spec.accessList ? (
        <FormNote>
          The site&apos;s own addresses and password are cleared on save; the list decides who gets
          in, and nginx refuses the save if there is no list by that name.
        </FormNote>
      ) : (
        <>
          <ListField
            id="site-allow"
            label="Allow only these"
            placeholder="10.0.0.0/8"
            values={spec.allowFrom}
            onChange={(v) => set("allowFrom", v)}
            hint="Filling this in refuses everything else. Include however you reach the site yourself."
          />
          <ListField
            id="site-deny"
            label="Deny"
            placeholder="203.0.113.0/24"
            values={spec.denyFrom}
            onChange={(v) => set("denyFrom", v)}
            hint="Exceptions, checked before the allow list. The fence at the end is written for you."
          />
          <Field
            label="Password file"
            htmlFor="site-auth"
            hint={
              authFiles && authFiles.length > 0
                ? "One of the files managed below, or any htpasswd file. Leave empty for no password."
                : "An htpasswd file. Create one in Password files below, or leave empty for no password."
            }
          >
            <Input
              id="site-auth"
              value={spec.basicAuthFile ?? ""}
              onChange={(e) => set("basicAuthFile", e.target.value)}
              placeholder="/etc/nginx/jd-auth/staging"
              list="site-auth-files"
              className="font-mono text-hint"
            />
            <datalist id="site-auth-files">
              {authFiles?.map((f) => (
                <option key={f.path} value={f.path} />
              ))}
            </datalist>
          </Field>
          {spec.basicAuthFile && (
            <Field
              label="Login prompt"
              htmlFor="site-realm"
              tip="The name the browser's password box shows. Empty is Restricted."
            >
              <Input
                id="site-realm"
                value={spec.basicAuthRealm ?? ""}
                onChange={(e) => set("basicAuthRealm", e.target.value || undefined)}
                placeholder="Restricted"
              />
            </Field>
          )}
          {spec.basicAuthFile && spec.allowFrom.length > 0 && (
            <OptionList>
              <OptionRow
                title="Let allowed addresses in without a password"
                hint="An address on the allow list gets straight in and anyone else is asked for the password, on every path — a path's own list or password included. Off asks allowed addresses for the password too."
                checked={spec.satisfyAny ?? false}
                onCheckedChange={(v) => set("satisfyAny", v || undefined)}
              />
            </OptionList>
          )}
        </>
      )}
    </>
  )
}
