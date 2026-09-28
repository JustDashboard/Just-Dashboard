import { json, now, type ProxyRoutes } from "./shared"

/** The Sites page's tools: named access lists sites include. */

export const accessDir = "/etc/nginx/jd-access"

/** Where the dashboard sees the browser; the showcase office list refuses it. */
export const clientAddress = "203.0.113.50"

export const accessLists = [
  {
    name: "office",
    path: `${accessDir}/office.conf`,
    include: `include ${accessDir}/office.conf;`,
    allow: ["10.0.0.0/8", "2001:db8::/32"],
    deny: ["10.0.0.9"],
    authFile: "staging",
    realm: "Restricted",
    satisfy: "any",
    usedBy: [
      {
        site: "app.example.com",
        path: "/etc/nginx/sites-available/app.example.com",
        enabled: true,
      },
      {
        site: "legacy.example.com",
        path: "/etc/nginx/sites-available/legacy.example.com",
        enabled: false,
      },
    ],
    modified: now,
  },
  {
    name: "admins",
    path: `${accessDir}/admins.conf`,
    include: `include ${accessDir}/admins.conf;`,
    allow: ["10.20.0.0/16"],
    deny: [],
    satisfy: "all",
    usedBy: [
      {
        site: "app.example.com",
        path: "/etc/nginx/sites-available/app.example.com",
        enabled: true,
      },
    ],
    modified: now,
  },
  {
    name: "vpn",
    path: `${accessDir}/vpn.conf`,
    include: `include ${accessDir}/vpn.conf;`,
    allow: [],
    deny: ["198.51.100.0/24"],
    satisfy: "all",
    usedBy: [],
    modified: now,
  },
]

export const routes: ProxyRoutes = {
  "/proxy/access-lists/": (route) => json(route, { dir: accessDir, clientAddress, lists: [] }),
}

export const showcase: ProxyRoutes = {
  "/proxy/access-lists/": (route) =>
    json(route, { dir: accessDir, clientAddress, lists: accessLists }),
}
