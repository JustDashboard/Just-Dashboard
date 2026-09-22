# Template and recipe review, 2026-09-22

The shipped catalogue contains **62 definitions**: **57 offered**, of which **55 can deploy** through the image runtime. The two Minecraft definitions remain preview-only. Five retired definitions stay resolvable for existing deployments and recovery; new drafts, preflight and commit refuse them.

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
| [minecraft-bedrock](../../../backend/internal/blueprint/builtin/minecraft-bedrock.json) | Yes | Preview only | open | eula |
| [minecraft-java](../../../backend/internal/blueprint/builtin/minecraft-java.json) | Yes | Preview only | open | eula |
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

Among the 55 deployable definitions, access kinds are: 18 setup, 13 open, 12 credentials, seven client and five token. Four require an administrator email (Directus, NocoDB, Open WebUI and pgAdmin), and Mongo Express requires a database connection. The remaining definitions can render from defaults plus an available suggested domain.

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
