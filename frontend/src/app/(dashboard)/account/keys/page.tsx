"use client"

import { Key } from "@/components/icons"
import { Page, PageHeader } from "@/components/page"
import { Well } from "@/components/panel"
import { Notice } from "@/components/state"
import {
  ApiKeysTable,
  CreateApiKeyDialog,
  curlExample,
  useApiKeys,
  useApiOrigin,
} from "@/components/account/api-keys"

/**
 * Keys, opened by what they are for — because the page used to be a table of
 * prefixes and roles with no sentence saying why anybody would want one.
 */
export default function AccountKeysPage() {
  const keys = useApiKeys()
  const origin = useApiOrigin()
  return (
    <Page className="animate-rise">
      <PageHeader
        eyebrow="Account"
        title="API keys"
        actions={<CreateApiKeyDialog onDone={keys.refresh} />}
      />
      <Notice icon={Key} title="For scripts and other machines">
        A key lets a CI pipeline, a cron job or a tool on your laptop call this dashboard the way
        you do — restart a container, pull a metrics window, trigger a deployment — without a
        browser or your password. Every request carries it as a bearer token:
        <Well className="mt-2">{curlExample(origin)}</Well>
      </Notice>
      <ApiKeysTable keys={keys} />
    </Page>
  )
}
