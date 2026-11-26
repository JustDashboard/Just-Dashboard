/**
 * What axe finds today that the pages' owners fix: by route, by rule, and by
 * the element, named by its tag and its `aria-label` with what a fixture's
 * clock moves taken out (`steady` in axe-gate.spec.ts), with how many of it
 * the route has. Each rule's finding in the UI-system analysis is `FINDING` in
 * the spec.
 *
 * Nothing outside this list may fail, and an element that fails no more, or
 * fails fewer times, fails the gate too, so the list only ever shrinks: a fix
 * lands with its line taken out or its count lowered. It was read from a full
 * run of axe-gate.spec.ts on 26 Nov 2026, when it moved from a rule per route
 * to the elements themselves.
 */
export const KNOWN: Record<string, Record<string, Record<string, number>>> = {
  backups__job: {
    "label-content-name-mismatch": {
      'a "Databases nightly"': 1,
      'a "Nginx configuration"': 1,
      'a "n8n data"': 1,
      'button "Run 100, success, <date> <time>"': 1,
      'button "Run 101, success, <date> <time>"': 1,
      'button "Run 102, success, <date> <time>"': 1,
      'button "Run 103, success, <date> <time>"': 1,
      'button "Run 104, success, <date> <time>"': 1,
      'button "Run 105, success, <date> <time>"': 1,
    },
  },
  databases__4: {
    "label-content-name-mismatch": {
      'a "Cache on Performance"': 1,
      'a "Clients on Performance"': 1,
      'a "Commands on Performance"': 1,
      'a "Memory on Performance"': 1,
      'a "Network on Performance"': 1,
    },
  },
  databases__id: {
    "label-content-name-mismatch": {
      'a "Cache on Performance"': 1,
      'a "Rows on Performance"': 1,
      'a "Sessions on Performance"': 1,
      'a "Throughput on Performance"': 1,
    },
  },
  deploy__id: {
    "label-content-name-mismatch": {
      'a "CPU on Runtime"': 1,
      'a "Failing requests on Logs"': 1,
      'a "Memory on Runtime"': 1,
      'a "Open #14 Deploy"': 1,
      'a "Requests on Logs"': 1,
    },
  },
  "deploy__id at 390": {
    "label-content-name-mismatch": {
      'a "Open #14 Deploy"': 1,
    },
  },
  deploy__id__deployments: {
    "label-content-name-mismatch": {
      'a "Open #14 Deploy"': 1,
      'button "Show the failed deployments"': 2,
    },
  },
  docker__containers: {
    "label-content-name-mismatch": {
      'button "Only the automation stack"': 1,
      'button "Only the monitoring stack"': 4,
      'button "Only the shop stack"': 2,
    },
  },
  docker__containers__id: {
    "label-content-name-mismatch": {
      'a "automations-n8n-1"': 1,
      'a "automations-postgres-1"': 1,
      'a "automations-redis-1"': 1,
      'a "automations-runner-1"': 1,
      'a "automations-worker-1"': 1,
      'a "uptime-kuma"': 1,
    },
  },
  docker__events: {
    "label-content-name-mismatch": {
      'button "Where this event came from"': 8,
    },
  },
  docker__images: {
    "label-content-name-mismatch": {
      'button "Only images that are built here"': 1,
      'button "Only images that are current"': 1,
      'button "Only images that are not checked"': 1,
      'button "Only images that are not in use"': 1,
      'button "Only images that are pinned by digest"': 1,
      'button "Only images that are update available"': 1,
      'button "Only the 2 images with an update"': 1,
    },
  },
  docker__images__id: {
    "label-content-name-mismatch": {
      'a "acme/api:dev"': 1,
      'a "caddy:2"': 1,
      'a "grafana/grafana:11.2.0"': 1,
      'a "mongo:7"': 1,
      'a "n8nio/n8n:1.64.3"': 1,
      'a "nginx:1.27-alpine"': 1,
      'a "node:22-alpine"': 1,
      'a "postgres:15"': 1,
      'a "postgres:16"': 1,
      'a "prom/prometheus:latest"': 1,
      'a "redis:7.2-alpine"': 1,
      'a "untagged cdcdcdcdcdcd"': 1,
      'a "untagged dededededede"': 1,
      'a "vaultwarden/server:latest"': 1,
      'button "Step 1: ADD file:9a4f77dfaba7fd2aa in /"': 1,
      'button "Step 3: RUN apk add --no-cache curl ca-certificates tzdata"': 1,
      'button "Step 4: RUN set -x && apkArch="$(cat /etc/apk/arch)" && apk add --no-cache --virtual .build-deps gcc libc-dev make openssl-dev pcre2-dev zlib-dev linux-headers"': 1,
      'button "Step 5: COPY docker-entrypoint.sh /"': 1,
      'button "Step 6: COPY 30-tune-worker-processes.sh /docker-entrypoint.d"': 1,
    },
  },
  docker__networks__name: {
    "label-content-name-mismatch": {
      'a "bridge"': 1,
      'a "host"': 1,
      'a "jd-e6-db-gqfymsmso5i7"': 1,
      'a "lan"': 1,
      'a "monitoring_default"': 1,
      'a "none"': 1,
      'a "old-staging"': 1,
      'a "proxy"': 1,
      'a "shop_backend"': 1,
      'a "shop_default"': 1,
      'a "wiki_default"': 1,
    },
  },
  docker__stacks: {
    "label-content-name-mismatch": {
      'a "Open analytics"': 1,
    },
  },
  docker__volumes: {
    "label-content-name-mismatch": {
      'button "Only volumes: anonymous, not mounted"': 1,
      'button "Only volumes: held by stopped containers"': 1,
      'button "Only volumes: in use"': 1,
      'button "Only volumes: left by a stack"': 1,
      'button "Only volumes: not mounted"': 1,
    },
  },
  docker__volumes__name: {
    "label-content-name-mismatch": {
      'a "3f9ad2c81e7b4f60a5d29c3e8b7f1a04d6e2c95b8a7f3d1e0c4b6a92f8e5d7c1"': 1,
      'a "jellyfin-config"': 1,
      'a "jellyfin-media"': 1,
      'a "minio-data"': 1,
      'a "monitoring_grafana"': 1,
      'a "monitoring_prometheus"': 1,
      'a "n8n_data"': 1,
      'a "nas-backups"': 1,
      'a "nextcloud_db"': 1,
      'a "nextcloud_html"': 1,
      'a "scratch"': 1,
      'a "shop_pgdata"': 1,
      'a "shop_redis"': 1,
      'a "shop_uploads"': 1,
      'a "uptime-kuma"': 1,
      'a "vaultwarden-data"': 1,
    },
  },
  "logs--insights": {
    "label-content-name-mismatch": {
      'button "Show only the errors"': 1,
      'button "Show only the warnings"': 1,
    },
  },
  metrics: {
    "label-content-name-mismatch": {
      'a "Open caddy, process 3301"': 1,
      'a "Open containerd, process 737"': 1,
      'a "Open cron, process 848"': 1,
      'a "Open dockerd, process 700"': 1,
      'a "Open node, process 2210"': 1,
      'a "Open postgres, process 1204"': 1,
      'a "Open sshd, process 811"': 1,
      'a "Open systemd-journald, process 774"': 1,
    },
  },
  network__connections: {
    "label-content-name-mismatch": {
      'a "Listening ports"': 1,
    },
  },
  network__connections__address: {
    "label-content-name-mismatch": {
      'a "10.0.1.2"': 1,
      'a "10.0.4.3"': 1,
      'a "100.110.34.9"': 1,
      'a "100.84.53.82"': 1,
      'a "192.0.2.145"': 1,
      'a "198.51.100.200"': 1,
      'a "198.51.100.23"': 1,
      'a "203.0.113.77"': 1,
    },
  },
  network__dns__services: {
    "label-content-name-mismatch": {
      'a "Open AdGuard Home\'s web page on port 3000"': 1,
    },
  },
  network__dns__services__id: {
    "label-content-name-mismatch": {
      'a "Fixture DNS"': 1,
    },
  },
  network__gateway: {
    "label-content-name-mismatch": {
      'button "Edit :25565 → 10.0.1.9:25565"': 1,
      'button "Policy evidence"': 1,
    },
  },
  network__interfaces: {
    "label-content-name-mismatch": {
      'a "Open br-93e5e9c9442b"': 1,
      'a "Open br-b17d23d019f2"': 1,
      'a "Open docker0"': 1,
      'a "Open ens3"': 1,
      'a "Open jd-lab"': 1,
      'a "Open tailscale0"': 1,
      'a "Open wg0"': 1,
    },
  },
  network__interfaces__name: {
    "label-content-name-mismatch": {
      'a "br-041b209e03b1, up"': 1,
      'a "br-93e5e9c9442b, up"': 1,
      'a "br-b17d23d019f2, up"': 1,
      'a "docker0, up"': 1,
      'a "ens3, up"': 1,
      'a "jd-lab, up"': 1,
      'a "tailscale0, up"': 1,
      'a "vlan30, up"': 1,
      'a "wg0, up"': 1,
    },
  },
  "network__traffic--ebpf": {
    "label-content-name-mismatch": {
      'a "Open program 30"': 1,
      'a "Open program 4633"': 1,
      'a "Open program 9101"': 1,
    },
  },
  network__traffic__containers__name: {
    "label-content-name-mismatch": {
      'a "api, 829.7 MB"': 1,
      'a "caddy, 1.9 GB"': 1,
      'a "grafana, 1.4 GB"': 1,
      'a "postgres, 1.6 GB"': 1,
      'a "redis, 1.1 GB"': 1,
      'a "registry, 276.6 MB"': 1,
      'a "worker, 553.1 MB"': 1,
    },
  },
  network__traffic__ebpf__id: {
    "label-content-name-mismatch": {
      'a "jd_flow_egress, program 9101"': 1,
      'a "sd_fw_egress, program 4633"': 1,
    },
  },
  network__vpn__tunnel: {
    "label-content-name-mismatch": {
      'a "wg0"': 1,
    },
  },
  overview: {
    "label-content-name-mismatch": {
      'a "CPU on Metrics"': 1,
      'a "Disk I/O on Metrics"': 1,
      'a "Load on Metrics"': 1,
      'a "Memory on Metrics"': 1,
      'a "Network on Metrics"': 1,
    },
  },
  packages__name: {
    "label-content-name-mismatch": {
      'a "curl"': 1,
      'a "docker.io"': 1,
      'a "git"': 1,
      'a "linux-image-6.8.0-45-generic"': 1,
      'a "nginx"': 1,
      'a "nodejs"': 1,
      'a "postgresql-16"': 1,
      'a "python3.12"': 1,
      'a "redis-server"': 1,
    },
  },
  processes__pm2__app: {
    "label-content-name-mismatch": {
      'a "Open /srv/api in Files"': 1,
      'a "Open pm2-deploy.service"': 1,
      'a "Open process 4021"': 1,
      'a "Open process 4022"': 1,
      'a "Open the PM2 daemon, PID 900"': 1,
      'a "Show what it listens on and its connections"': 1,
      'a "api"': 1,
      'a "queue"': 1,
      'a "worker"': 1,
      'button "Read its log"': 1,
    },
  },
  "processes__pm2__app at 390": {
    "label-content-name-mismatch": {
      'a "Open pm2-deploy.service"': 1,
      'a "Open process 4021"': 1,
      'a "Open process 4022"': 1,
      'a "Open the PM2 daemon, PID 900"': 1,
      'a "Show what it listens on and its connections"': 1,
    },
  },
  processes__services__unit: {
    "label-content-name-mismatch": {
      'a "certbot.service"': 1,
      'a "containerd.service"': 1,
      'a "docker.service"': 1,
      'a "fail2ban.service"': 1,
      'a "grafana-server.service"': 1,
      'a "mongod.service"': 1,
      'a "nginx.service"': 1,
      'a "php8.3-fpm.service"': 1,
      'a "pm2-deploy.service"': 1,
      'a "postgresql.service"': 1,
      'a "prometheus-node-exporter.service"': 1,
      'a "prometheus.service"': 1,
      'a "redis-server.service"': 1,
      'a "ssh.service"': 1,
      'a "systemd-journald.service"': 1,
      'a "tailscaled.service"': 1,
      'button "Read its journal"': 1,
    },
  },
  proxy__certificates__name: {
    "label-content-name-mismatch": {
      'a "app.example.com, 29 days left"': 1,
      'a "old.example.com, Expired"': 1,
    },
  },
  proxy__ports: {
    "label-content-name-mismatch": {
      'a "Open tcp 0.0.0.0:443"': 1,
      'a "Open tcp 0.0.0.0:5432"': 1,
      'a "Open tcp 127.0.0.1:3000"': 1,
    },
  },
  proxy__ports__port: {
    "label-content-name-mismatch": {
      'a "3000/tcp, node"': 1,
      'a "443/tcp, nginx"': 1,
      'a "5432/tcp, postgres"': 1,
    },
  },
  proxy__sites: {
    "label-content-name-mismatch": {
      'a "Open app.example.com"': 1,
    },
  },
  proxy__sites__name: {
    "label-content-name-mismatch": {
      'a "app.example.com"': 1,
      'a "just-dashboard-shop"': 1,
      'a "legacy.example.com"': 1,
    },
  },
  security__intrusion__jails__jail: {
    "label-content-name-mismatch": {
      'a "sshd, 2 banned"': 1,
    },
  },
  "system-users__groups": {
    "label-content-name-mismatch": {
      'button "Only the groups that reach root"': 1,
    },
  },
  "system-users__groups__name": {
    "label-content-name-mismatch": {
      'a "developers"': 1,
      'a "docker"': 1,
      'a "lxd"': 1,
      'a "sudo"': 1,
    },
  },
  "system-users__user__name": {
    "label-content-name-mismatch": {
      'a "alex"': 1,
      'a "deploy"': 1,
      'a "ion"': 1,
      'a "mira"': 1,
      'a "root"': 1,
      'a "ubuntu"': 1,
      'a "zoro"': 1,
    },
  },
}
