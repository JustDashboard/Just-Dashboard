import { redirect } from "next/navigation"

// The connected engines are listed in the DNS page's Services view, and an
// engine's own page is `/network/dns/services/<id>`. The address between the
// two is the list's, so a trimmed link lands on the list rather than on a
// page that is not there.
export default function NetworkDNSServicesLink() {
  redirect("/network/dns?tab=services")
}
