"use client"

import { useAuth } from "@/hooks/use-auth"
import { PERSONAL_NAV } from "@/components/app-sidebar"
import { SectionNav } from "@/components/tabs"

/**
 * The account is five pages rather than one screen of tabs, for the same
 * reason Security is: each is a destination the footer menu and the palette
 * can name, the URL says where you are, and the strip is the switcher once you
 * are here. The tabs are the same list the menu draws, so adding a page is one
 * entry — and the one only a system.admin may open is hidden from the rest by
 * the same capability the backend enforces on its routes.
 */
export default function AccountLayout({ children }: { children: React.ReactNode }) {
  const { can } = useAuth()
  const tabs = PERSONAL_NAV.filter((item) => !item.capability || can(item.capability))
  return (
    <>
      <SectionNav tabs={tabs} root="/account" />
      {children}
    </>
  )
}
