# Template and recipe review, 2026-09-22

The shipped catalogue contains **122 definitions**: **117 offered**, all of which **can deploy** — 82 single images through the image runtime and 35 multi-service templates through the Compose release path. The first 62 are in the table below; the 20 multi-service templates added 2026-10-27 and the second batch of 20 added 2026-10-28 are each listed in their own table further down. The two Minecraft definitions deploy since 2026-10-29: their `server.properties` is an install-time file written into the data volume before the first start, Bedrock's UDP port is published on the plan's bind address, and both are gated on a protocol check (`game_handshake`, `udp_probe`); see [game servers](../../internal/deployments/game-servers.md). `backend/internal/blueprint/catalogue_counts.go` declares these three counts (built in, deployable, Compose) and `catalogue_counts_test.go` fails when the shipped definitions disagree, so a template cannot join or leave the catalogue without the numbers being bumped on purpose. Five retired definitions stay resolvable for existing deployments and recovery; new drafts, preflight and commit refuse them. A third batch of twenty (fourteen single images and six stacks, 2026-10-28) is listed in its own table as well.

This table records the current definitions and the automated configuration checks. Fresh runtime results are recorded in the companion review report; a rendered plan is not proof that an image started or that first sign-in succeeded.

Access is intentional per application: `credentials`, `token` and `client` use generated secrets; `setup` opens an application-owned first-account flow; `open` has no application authentication. Automatic credentials are therefore not a promise of universal automatic account creation. Open applications can use deployment domain password protection. Database templates do not request public exposure.

Required inputs exclude supplied defaults. `domain` is filled by the hostname suggestion when available and remains editable. Mongo Express asks for its connection in Configure through a linked MongoDB or an encrypted external URL, never in plain template inputs.

| Definition | Offered | Deployable | First access | Required inputs without defaults |
| --- | --- | --- | --- | --- |
| [actual](../../../backend/internal/blueprint/builtin/actual.json) | Yes | Yes | setup | None |
| [adminer](../../../backend/internal/blueprint/builtin/adminer.json) | Yes | Yes | open | None |
| [audiobookshelf](../../../backend/internal/blueprint/builtin/audiobookshelf.json) | Yes | Yes | setup | None |
| [beszel](../../../backend/internal/blueprint/builtin/beszel.json) | Yes | Yes | setup | domain (suggested) |
| [caddy](../../../backend/internal/blueprint/builtin/caddy.json) | Yes | Yes | open | None |
| [code-server](../../../backend/internal/blueprint/builtin/code-server.json) | Yes | Yes | token | domain (suggested) |
| [cyberchef](../../../backend/internal/blueprint/builtin/cyberchef.json) | Yes | Yes | open | None |
| [directus](../../../backend/internal/blueprint/builtin/directus.json) | Yes | Yes | credentials | domain (suggested), admin-email |
| [docuseal](../../../backend/internal/blueprint/builtin/docuseal.json) | Yes | Yes | setup | domain (suggested) |
| [dozzle](../../../backend/internal/blueprint/builtin/dozzle.json) | Yes | Yes | setup | None |
| [drawio](../../../backend/internal/blueprint/builtin/drawio.json) | Yes | Yes | open | domain (suggested) |
| [filebrowser](../../../backend/internal/blueprint/builtin/filebrowser.json) | Retired | Existing deployments only | unavailable | None |
| [freshrss](../../../backend/internal/blueprint/builtin/freshrss.json) | Yes | Yes | setup | None |
| [gitea](../../../backend/internal/blueprint/builtin/gitea.json) | Yes | Yes | setup | domain (suggested) |
| [gotify](../../../backend/internal/blueprint/builtin/gotify.json) | Yes | Yes | credentials | None |
| [grafana](../../../backend/internal/blueprint/builtin/grafana.json) | Yes | Yes | credentials | domain (suggested) |
| [healthchecks](../../../backend/internal/blueprint/builtin/healthchecks.json) | Retired | Existing deployments only | unavailable | domain (suggested) |
| [homepage](../../../backend/internal/blueprint/builtin/homepage.json) | Yes | Yes | token | domain (suggested) |
| [influxdb](../../../backend/internal/blueprint/builtin/influxdb.json) | Yes | Yes | credentials | None |
| [it-tools](../../../backend/internal/blueprint/builtin/it-tools.json) | Yes | Yes | open | None |
| [jellyfin](../../../backend/internal/blueprint/builtin/jellyfin.json) | Yes | Yes | setup | None |
| [jupyter](../../../backend/internal/blueprint/builtin/jupyter.json) | Yes | Yes | token | None |
| [kavita](../../../backend/internal/blueprint/builtin/kavita.json) | Yes | Yes | setup | None |
| [linkding](../../../backend/internal/blueprint/builtin/linkding.json) | Yes | Yes | credentials | domain (suggested) |
| [mariadb](../../../backend/internal/blueprint/builtin/mariadb.json) | Yes | Yes | client | None |
| [meilisearch](../../../backend/internal/blueprint/builtin/meilisearch.json) | Yes | Yes | client | None |
| [memos](../../../backend/internal/blueprint/builtin/memos.json) | Yes | Yes | setup | None |
| [metabase](../../../backend/internal/blueprint/builtin/metabase.json) | Yes | Yes | setup | None |
| [minecraft-bedrock](../../../backend/internal/blueprint/builtin/minecraft-bedrock.json) | Yes | Yes | open | eula |
| [minecraft-java](../../../backend/internal/blueprint/builtin/minecraft-java.json) | Yes | Yes | open | eula |
| [minio](../../../backend/internal/blueprint/builtin/minio.json) | Retired | Existing deployments only | client | None |
| [mongo-express](../../../backend/internal/blueprint/builtin/mongo-express.json) | Yes | Yes | credentials | mongodb-url (encrypted) |
| [mongodb](../../../backend/internal/blueprint/builtin/mongodb.json) | Yes | Yes | client | None |
| [mysql](../../../backend/internal/blueprint/builtin/mysql.json) | Yes | Yes | client | None |
| [n8n](../../../backend/internal/blueprint/builtin/n8n.json) | Yes | Yes | setup | domain (suggested) |
| [navidrome](../../../backend/internal/blueprint/builtin/navidrome.json) | Yes | Yes | setup | None |
| [nextcloud](../../../backend/internal/blueprint/builtin/nextcloud.json) | Yes | Yes | credentials | domain (suggested) |
| [nginx-static](../../../backend/internal/blueprint/builtin/nginx-static.json) | Yes | Yes | open | None |
| [nocodb](../../../backend/internal/blueprint/builtin/nocodb.json) | Yes | Yes | credentials | domain (suggested), admin-email |
| [ntfy](../../../backend/internal/blueprint/builtin/ntfy.json) | Yes | Yes | open | domain (suggested) |
| [ollama](../../../backend/internal/blueprint/builtin/ollama.json) | Yes | Yes | open | None |
| [open-webui](../../../backend/internal/blueprint/builtin/open-webui.json) | Yes | Yes | credentials | domain (suggested), admin-email |
| [opengist](../../../backend/internal/blueprint/builtin/opengist.json) | Yes | Yes | setup | domain (suggested) |
| [pgadmin](../../../backend/internal/blueprint/builtin/pgadmin.json) | Yes | Yes | credentials | admin-email |
| [phpmyadmin](../../../backend/internal/blueprint/builtin/phpmyadmin.json) | Yes | Yes | open | None |
| [portainer](../../../backend/internal/blueprint/builtin/portainer.json) | Yes | Yes | setup | None |
| [postgresql](../../../backend/internal/blueprint/builtin/postgresql.json) | Yes | Yes | client | None |
| [prometheus](../../../backend/internal/blueprint/builtin/prometheus.json) | Yes | Yes | open | None |
| [qdrant](../../../backend/internal/blueprint/builtin/qdrant.json) | Yes | Yes | token | None |
| [rabbitmq](../../../backend/internal/blueprint/builtin/rabbitmq.json) | Yes | Yes | credentials | None |
| [redis](../../../backend/internal/blueprint/builtin/redis.json) | Yes | Yes | open | None |
| [searxng](../../../backend/internal/blueprint/builtin/searxng.json) | Yes | Yes | open | domain (suggested) |
| [seerr](../../../backend/internal/blueprint/builtin/seerr.json) | Yes | Yes | setup | None |
| [shlink](../../../backend/internal/blueprint/builtin/shlink.json) | Yes | Yes | client | domain (suggested) |
| [stirling-pdf](../../../backend/internal/blueprint/builtin/stirling-pdf.json) | Yes | Yes | credentials | None |
| [syncthing](../../../backend/internal/blueprint/builtin/syncthing.json) | Retired | Existing deployments only | unavailable | None |
| [trilium](../../../backend/internal/blueprint/builtin/trilium.json) | Yes | Yes | setup | None |
| [typesense](../../../backend/internal/blueprint/builtin/typesense.json) | Yes | Yes | client | None |
| [uptime-kuma](../../../backend/internal/blueprint/builtin/uptime-kuma.json) | Yes | Yes | setup | None |
| [vaultwarden](../../../backend/internal/blueprint/builtin/vaultwarden.json) | Yes | Yes | token | domain (suggested) |
| [wallabag](../../../backend/internal/blueprint/builtin/wallabag.json) | Retired | Existing deployments only | unavailable | domain (suggested) |
| [whoami](../../../backend/internal/blueprint/builtin/whoami.json) | Yes | Yes | open | None |

Among the 55 deployable single-image definitions of this table, access kinds are: 18 setup, 13 open, 12 credentials, seven client and five token. Four require an administrator email (Directus, NocoDB, Open WebUI and pgAdmin), and Mongo Express requires a database connection. The remaining definitions can render from defaults plus an available suggested domain.

## Multi-service templates (2026-10-27)

Each runs as one Compose stack: one public service behind the domain, the others on a private network with no published port and no route out (except the authentik worker, which declares egress for outbound mail). Every one was started live by `TestLiveEveryComposeTemplateStartsAndAnswersItsOwnChecks`, which requires its readiness checks to pass, every service healthcheck to report healthy and only the public port to be published, on loopback.

| Definition | Offered | Deployable | First access | Required inputs without defaults |
| --- | --- | --- | --- | --- |
| [authentik](../../../backend/internal/blueprint/builtin/authentik.json) | Yes | Yes | credentials | domain (suggested), admin-email |
| [directus-postgres](../../../backend/internal/blueprint/builtin/directus-postgres.json) | Yes | Yes | credentials | domain (suggested), admin-email |
| [docmost](../../../backend/internal/blueprint/builtin/docmost.json) | Yes | Yes | setup | domain (suggested) |
| [firefly-iii](../../../backend/internal/blueprint/builtin/firefly-iii.json) | Yes | Yes | setup | domain (suggested) |
| [forgejo](../../../backend/internal/blueprint/builtin/forgejo.json) | Yes | Yes | setup | domain (suggested) |
| [ghost](../../../backend/internal/blueprint/builtin/ghost.json) | Yes | Yes | setup | domain (suggested) |
| [gitea-postgres](../../../backend/internal/blueprint/builtin/gitea-postgres.json) | Yes | Yes | setup | domain (suggested) |
| [keycloak](../../../backend/internal/blueprint/builtin/keycloak.json) | Yes | Yes | credentials | domain (suggested) |
| [listmonk](../../../backend/internal/blueprint/builtin/listmonk.json) | Yes | Yes | credentials | domain (suggested) |
| [matomo](../../../backend/internal/blueprint/builtin/matomo.json) | Yes | Yes | setup | None |
| [mattermost](../../../backend/internal/blueprint/builtin/mattermost.json) | Yes | Yes | setup | domain (suggested) |
| [metabase-postgres](../../../backend/internal/blueprint/builtin/metabase-postgres.json) | Yes | Yes | setup | domain (suggested) |
| [miniflux](../../../backend/internal/blueprint/builtin/miniflux.json) | Yes | Yes | credentials | domain (suggested) |
| [n8n-postgres](../../../backend/internal/blueprint/builtin/n8n-postgres.json) | Yes | Yes | setup | domain (suggested) |
| [nextcloud-mariadb](../../../backend/internal/blueprint/builtin/nextcloud-mariadb.json) | Yes | Yes | credentials | domain (suggested) |
| [paperless-ngx](../../../backend/internal/blueprint/builtin/paperless-ngx.json) | Yes | Yes | credentials | domain (suggested) |
| [planka](../../../backend/internal/blueprint/builtin/planka.json) | Yes | Yes | credentials | domain (suggested), admin-email |
| [plausible](../../../backend/internal/blueprint/builtin/plausible.json) | Yes | Yes | setup | domain (suggested) |
| [wikijs](../../../backend/internal/blueprint/builtin/wikijs.json) | Yes | Yes | setup | None |
| [wordpress](../../../backend/internal/blueprint/builtin/wordpress.json) | Yes | Yes | setup | None |

Access kinds among them: 12 setup and eight credentials. Directus, authentik and Planka require an administrator e-mail; every other required input is the suggested domain. Umami (it creates `admin`/`umami` for everybody, with no variable to change it) and Outline (no sign-in without an external OIDC, Slack or Google provider or SMTP) were reviewed and not added; Immich was left for a later pass.

## Second batch (2026-10-28)

Twenty more applications that both Coolify and Dokploy offer and this catalogue did not: nine run as Compose stacks (one public service behind the domain, the database or queue on a private network with nothing published and no route out) and eleven as single images. Every one was started live from its fixture inputs with generated secrets and answered its own readiness checks: the single images through `TestLiveEveryBlueprintStartsAndAnswersItsOwnChecks`, the stacks through `TestLiveEveryComposeTemplateStartsAndAnswersItsOwnChecks`, which also requires every service healthy and only the public port published, on loopback.

| Definition | Profile | Offered | Deployable | First access | Required inputs without defaults |
| --- | --- | --- | --- | --- | --- |
| [activepieces](../../../backend/internal/blueprint/builtin/activepieces.json) | compose (+PostgreSQL/pgvector, Valkey) | Yes | Yes | setup | domain (suggested) |
| [anythingllm](../../../backend/internal/blueprint/builtin/anythingllm.json) | web | Yes | Yes | setup | None |
| [appsmith](../../../backend/internal/blueprint/builtin/appsmith.json) | web | Yes | Yes | setup | None |
| [apprise-api](../../../backend/internal/blueprint/builtin/apprise-api.json) | web | Yes | Yes | open | None |
| [budibase](../../../backend/internal/blueprint/builtin/budibase.json) | web | Yes | Yes | credentials | admin-email |
| [chroma](../../../backend/internal/blueprint/builtin/chroma.json) | database | Yes | Yes | open (internal port only) | None |
| [documenso](../../../backend/internal/blueprint/builtin/documenso.json) | compose (+PostgreSQL) | Yes | Yes | setup | domain (suggested) |
| [duplicati](../../../backend/internal/blueprint/builtin/duplicati.json) | web | Yes | Yes | token | domain (suggested) |
| [flowise](../../../backend/internal/blueprint/builtin/flowise.json) | compose (+PostgreSQL) | Yes | Yes | setup | domain (suggested) |
| [glitchtip](../../../backend/internal/blueprint/builtin/glitchtip.json) | compose (+PostgreSQL, Valkey) | Yes | Yes | setup | domain (suggested) |
| [goatcounter](../../../backend/internal/blueprint/builtin/goatcounter.json) | web | Yes | Yes | setup | None |
| [grist](../../../backend/internal/blueprint/builtin/grist.json) | web | Yes | Yes | setup | domain (suggested) |
| [infisical](../../../backend/internal/blueprint/builtin/infisical.json) | compose (+PostgreSQL, Valkey) | Yes | Yes | setup | domain (suggested) |
| [langflow](../../../backend/internal/blueprint/builtin/langflow.json) | compose (+PostgreSQL) | Yes | Yes | credentials | None |
| [librechat](../../../backend/internal/blueprint/builtin/librechat.json) | compose (+MongoDB) | Yes | Yes | setup | domain (suggested) |
| [litellm](../../../backend/internal/blueprint/builtin/litellm.json) | compose (+PostgreSQL) | Yes | Yes | credentials | None |
| [lobe-chat](../../../backend/internal/blueprint/builtin/lobe-chat.json) | web | Yes | Yes | token | None |
| [rallly](../../../backend/internal/blueprint/builtin/rallly.json) | compose (+PostgreSQL) | Yes | Yes | open | domain (suggested) |
| [sftpgo](../../../backend/internal/blueprint/builtin/sftpgo.json) | web | Yes | Yes | credentials | None |
| [wakapi](../../../backend/internal/blueprint/builtin/wakapi.json) | web | Yes | Yes | setup | domain (suggested) |

Access kinds among them: 11 setup, four credentials, three open and two token. Budibase is the only one that requires an administrator e-mail (it creates the account from it on the first start); every other required input is the suggested domain. Three "open" entries are open for different reasons and say so on their cards: Chroma's 1.x server has no authentication, so its port is internal-only and it is for applications on this host; Rallly works as a guest and its accounts need SMTP for the sign-in code; Apprise API has none and is meant for the deployment's own password protection. Two from the planned list were replaced: **Strapi** has no official image since v3 (Strapi 5 is built per project), so it gave way to Apprise API; **Formbricks** 6 refuses to start without SpiceDB, Formbricks Hub and Cube beside PostgreSQL and Valkey — its own compose file is eight services and a bootstrap script mounted from the host, which a template cannot carry — so it gave way to Infisical. Garage (needs a configuration file, which runtime materialization does not write) and SeaweedFS (its only authentication is a JSON file the template cannot write) were passed over on the way to Apprise API, and Baby Buddy (a fixed admin/admin first login the image gives no variable for) on the way to Infisical. LobeChat is pinned to the last 1.x release, the last with the client-side access code; LobeHub 2.x refuses `ACCESS_CODE` and needs a database. Documenso starts without a signing certificate and says so: completing a document needs a `.p12` pasted into `NEXT_PRIVATE_SIGNING_LOCAL_FILE_CONTENTS`. Flowise was planned as a single image on SQLite, but the 3.1.4 image's SQLite session store fails on start (`connect-sqlite3`: `this.db.exec is not a function`, reproduced with no environment at all), so it runs on PostgreSQL as a stack, which is upstream's production recommendation anyway. LobeChat needs 1 GB: Node sizes its heap from the container limit and the 1.x server runs out of memory at 512 MB.

## One-click templates, third batch (2026-10-28)

Twenty more templates present in both Coolify's and Dokploy's catalogues: fourteen single images and six stacks. Every single image was started by `TestLiveEveryBlueprintStartsAndAnswersItsOwnChecks` and every stack by `TestLiveEveryComposeTemplateStartsAndAnswersItsOwnChecks`, with the same requirements as the tables above. Two images whose registry publishes no release tag (Excalidraw, BentoPDF) are pinned by content digest.

| Definition | Offered | Deployable | Profile | First access | Required inputs without defaults |
| --- | --- | --- | --- | --- | --- |
| [bentopdf](../../../backend/internal/blueprint/builtin/bentopdf.json) | Yes | Yes | web | open | None |
| [changedetection](../../../backend/internal/blueprint/builtin/changedetection.json) | Yes | Yes | web | open | domain (suggested) |
| [convertx](../../../backend/internal/blueprint/builtin/convertx.json) | Yes | Yes | web | setup | None |
| [dashy](../../../backend/internal/blueprint/builtin/dashy.json) | Yes | Yes | web | open | None |
| [excalidraw](../../../backend/internal/blueprint/builtin/excalidraw.json) | Yes | Yes | web | open | None |
| [gotenberg](../../../backend/internal/blueprint/builtin/gotenberg.json) | Yes | Yes | web | client | None |
| [homarr](../../../backend/internal/blueprint/builtin/homarr.json) | Yes | Yes | web | setup | None |
| [homebox](../../../backend/internal/blueprint/builtin/homebox.json) | Yes | Yes | web | setup | None |
| [karakeep](../../../backend/internal/blueprint/builtin/karakeep.json) | Yes | Yes | compose (web, chrome, meilisearch) | setup | domain (suggested) |
| [kimai](../../../backend/internal/blueprint/builtin/kimai.json) | Yes | Yes | compose (kimai, db) | credentials | domain (suggested), admin-email |
| [librespeed](../../../backend/internal/blueprint/builtin/librespeed.json) | Yes | Yes | web | open | None |
| [libretranslate](../../../backend/internal/blueprint/builtin/libretranslate.json) | Yes | Yes | web | open | None |
| [linkwarden](../../../backend/internal/blueprint/builtin/linkwarden.json) | Yes | Yes | compose (linkwarden, db) | setup | domain (suggested) |
| [mailpit](../../../backend/internal/blueprint/builtin/mailpit.json) | Yes | Yes | web | open | None |
| [metube](../../../backend/internal/blueprint/builtin/metube.json) | Yes | Yes | web | open | None |
| [onetimesecret](../../../backend/internal/blueprint/builtin/onetimesecret.json) | Yes | Yes | compose (app, maindb) | open | domain (suggested) |
| [pairdrop](../../../backend/internal/blueprint/builtin/pairdrop.json) | Yes | Yes | web | open | None |
| [privatebin](../../../backend/internal/blueprint/builtin/privatebin.json) | Yes | Yes | web | open | None |
| [yamtrack](../../../backend/internal/blueprint/builtin/yamtrack.json) | Yes | Yes | compose (yamtrack, cache) | setup | domain (suggested) |
| [zipline](../../../backend/internal/blueprint/builtin/zipline.json) | Yes | Yes | compose (zipline, db) | setup | domain (suggested) |

Access kinds among them: 11 open, seven setup, one credentials (Kimai, whose `admin` account takes the generated `ADMINPASS` and the administrator e-mail typed in) and one client (Gotenberg, an API whose basic authentication the template switches on with a named user and a generated password; its `/health` route is registered outside that middleware, so the readiness probe still answers). The open ones are tools without accounts of their own (a whiteboard, a pastebin, a speed test, a mail catcher); each note says so and points at the domain's password protection. LibreSpeed generates a password for its statistics page and Mailpit's own interface authentication is one `user:password` variable the catalogue cannot compose, which is why both stay `open`.

Reviewed and **not added**, each replaced from the agreed list of alternates: **Umami**, **BookStack** (`admin@admin.com`/`password`), **Calibre-Web** (`admin`/`admin123`) and **Mealie** (`changeme@example.com`/`MyPassword`, whose settings mark the two values as no longer settable) all create the same first account for everybody with no variable to change it, which is the `unavailable` access kind and may ship only retired; **PocketBase** has no official image and prints its first-superuser link only into its own log; **Glance** refuses to start without a `glance.yml` the template cannot install; **Hoppscotch** needs a one-shot `prisma migrate deploy` container before the first start (a service that must exit, which a stack cannot declare) and signs in only by e-mailed link or OAuth; **Vikunja**'s image is `FROM scratch` running as uid 1000 with no data directory in the image, so a fresh managed volume is root-owned and it exits with `permission denied` (upstream tells users to `chown` a bind mount). Among the alternates, **mosquitto** was skipped because any authentication needs a configuration file and the only file-less mode is the image's anonymous broker on a published port, **chibisafe** because it is two images behind a reverse-proxy configuration file, **Snipe-IT** on the Laravel `APP_KEY` rule that rejected Speedtest Tracker, **Fider** because it signs in only by e-mailed link, **Reactive Resume** because it needs MinIO and a headless browser beside it, and **SilverBullet** because its only authentication is one `user:password` variable, so it would ship open.

## Confirmed defects and fixes

- Stateless HTTP tools were normalized as non-HTTP services while retaining blue/green activation. The resulting preflight rejected their own defaults. Tool blueprints now use the web profile.
- Mongo Express accepted a credential-bearing connection URL in plain template inputs. A new secret-input declaration defers that value to encrypted variables; new drafts and source revisions reject direct plaintext input without echoing it. Required-value preflight still applies. Existing committed sources continue to run from their saved variable revisions; materialization removes historical secret inputs from its local copy before writing a source marker, without changing the stored source or runtime values.
- A retired definition could be selected by a direct new-draft API request. Source save, preflight and commit now reject new installations while runtime validation continues to accept already-deployed definitions.
- Changing the public address after detection left application URLs on the old hostname. Reviewed domain-derived variables carry binding metadata and requiredness; the form updates untouched defaults and preserves explicit overrides. Domain inputs also accept DNS case-insensitivity and reject hostnames longer than 253 bytes.

`TestEveryTemplateDeploysFromWhatTheNewProjectPageCanFillIn` checks page-like defaults through rendering, configuration validation and runtime/profile preflight. `TestEveryDeployableBlueprintPassesConfigurationAndRuntimePreflight` does the same for reviewed fixtures and checks that all declared generated credentials retain their requested lengths, secret sensitivity and requiredness. The real-Docker catalogue sweep separately pulls, starts and checks all 55 deployable entries.

## Framework recipes

The reviewed automatic recipe set covers Node/static (Next.js, React/Vite, Vue/Nuxt and the other recognized Node frameworks), Python, Go, Rust, Java/Kotlin, .NET, Deno and PHP. Dockerfile and Compose remain the explicit route for other stacks and unsupported arrangements. This review does not claim automatic support for every framework or deployment topology.

- Rust previously selected the first declared binary even when `package.default-run` named the service. Detection and image preparation now honor that declaration, including automatically discovered binary targets. See the [Cargo manifest contract](https://doc.rust-lang.org/cargo/reference/manifest.html#the-default-run-field).
- .NET previously accepted `TargetFrameworks` but omitted the required publish framework and could choose an older SDK. The recipe now selects the newest supported portable target and uses it for both restore and publish; platform-specific-only targets produce a planning refusal. See [Microsoft publishing guidance](https://learn.microsoft.com/en-us/dotnet/core/deploying/).
- Deno JSONC parsing rejected inline comments and could rewrite comment-looking text inside task strings. Parsing now preserves strings while removing comments/trailing commas, and malformed JSONC cannot yield partial tasks. See [Deno configuration](https://docs.deno.com/runtime/fundamentals/configuration/).
- A suspected n8n URL-variable mismatch was rejected after checking current primary documentation: n8n 2.35+ uses `N8N_WEBHOOK_URL`, and the existing 2.39 template already used it correctly. See the [upstream endpoint-variable documentation](https://github.com/n8n-io/n8n-docs/blob/main/docs/deploy/host-n8n/configure-n8n/basic-configuration/use-environment-variables/endpoints.md).

The existing real-framework fixtures now exercise a Rust maintenance binary beside the `default-run` HTTP application, a .NET 8/10 multi-target service and Deno inline JSONC comments. Detection/Dockerfile regressions cover those cases separately from runtime evidence.
