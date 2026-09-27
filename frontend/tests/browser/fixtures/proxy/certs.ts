import { inThirtyDays, json, yesterday, type ProxyRoutes } from "./shared"

export const certs = [
  {
    name: "app.example.com",
    path: "/etc/letsencrypt/live/app.example.com/fullchain.pem",
    domains: ["app.example.com"],
    issuer: "R11",
    notBefore: yesterday,
    notAfter: inThirtyDays,
    daysLeft: 29,
    expired: false,
    expiring: true,
    selfSigned: false,
    source: "certbot",
    usedBy: ["app.example.com"],
  },
  {
    name: "old.example.com",
    path: "/etc/ssl/just-dashboard/old.example.com/fullchain.pem",
    domains: ["old.example.com"],
    issuer: "R11",
    notBefore: yesterday,
    notAfter: yesterday,
    daysLeft: -1,
    expired: true,
    expiring: false,
    selfSigned: false,
    source: "imported",
    usedBy: [],
  },
]

export const routes: ProxyRoutes = {
  "/certificates/": (route) => json(route, certs),
  "/certificates/certbot": (route) =>
    json(route, {
      available: true,
      version: "certbot 2.9.0",
      certs: [
        {
          name: "app.example.com",
          domains: ["app.example.com"],
          expiry: inThirtyDays,
          daysLeft: 29,
          valid: true,
        },
      ],
      autoRenew: false,
      renewUnit: "certbot.timer",
    }),
  "/certificates/dns-providers": (route) => json(route, []),
}

export const showcase: ProxyRoutes = {
  "/certificates/dns-providers": (route) =>
    json(route, [
      {
        key: "cloudflare",
        name: "Cloudflare",
        plugin: "dns-cloudflare",
        installed: true,
        hasCredentials: true,
        defaultWait: 30,
      },
    ]),
}
