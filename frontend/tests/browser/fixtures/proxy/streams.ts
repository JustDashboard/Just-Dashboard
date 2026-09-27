import { json, type ProxyRoutes } from "./shared"

export const snippet = "stream {\n    include /etc/nginx/streams/*.conf;\n}"

export const routes: ProxyRoutes = {
  "/proxy/streams/": (route, { included }) =>
    json(route, { included, snippet, dir: "/etc/nginx/streams", streams: [] }),
}

export const showcase: ProxyRoutes = {
  "/proxy/streams/": (route) =>
    json(route, {
      included: true,
      snippet,
      dir: "/etc/nginx/streams",
      streams: [
        {
          name: "postgres-replica",
          listen: 5432,
          protocol: "tcp",
          upstream: "10.0.0.5:5432",
          proxyProtocol: false,
          allowFrom: [],
        },
        {
          name: "private-redis",
          listen: 6379,
          protocol: "tcp",
          upstream: "10.0.0.9:6379",
          proxyProtocol: false,
          allowFrom: ["10.0.0.0/8"],
          timeout: 600,
        },
        {
          name: "minecraft",
          listen: 25565,
          protocol: "tcp",
          upstream: "10.0.0.6:25565",
          proxyProtocol: true,
          allowFrom: [],
        },
      ],
    }),
}
