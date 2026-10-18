# The "full" PostgreSQL template: the official image and the extensions a
# schema most often asks for that it does not ship, built on the server the
# first time somebody creates one.
#
# No published image carries all seven on the official image's layout, so
# this one is made rather than trusted: the base is the official Debian image,
# whose PostgreSQL already comes from the PostgreSQL project's own apt
# repository and which already trusts its key, and every extension is that
# repository's package of it for the same major. Nothing is fetched from
# anywhere else.
#
# The dashboard passes PG_MAJOR, and JD_RECIPE, the digest of this file, which
# the label carries so that an image built from an older copy of it is known
# for one and built again.
ARG PG_MAJOR=18
FROM postgres:${PG_MAJOR}-trixie

ARG JD_RECIPE=""
RUN set -eux; \
	apt-get update; \
	apt-get install -y --no-install-recommends \
		"postgresql-${PG_MAJOR}-pgvector" \
		"postgresql-${PG_MAJOR}-postgis-3" \
		"postgresql-${PG_MAJOR}-postgis-3-scripts" \
		"postgresql-${PG_MAJOR}-cron" \
		"postgresql-${PG_MAJOR}-partman" \
		"postgresql-${PG_MAJOR}-pgaudit" \
		"postgresql-${PG_MAJOR}-hypopg" \
		"postgresql-${PG_MAJOR}-repack"; \
	rm -rf /var/lib/apt/lists/*

# pg_cron and pgaudit can be created only in a server that loaded them at
# start. They are named in the configuration every new cluster is made from,
# the way TimescaleDB's image names its own library, rather than on the
# container's command line: a setting given there outranks every file, and the
# list could never be changed again from the server's own parameters.
#
# pg_cron's scheduler works in one database, and it is the one the server is
# created for: written once, when the cluster is initialised, by a script the
# image's entrypoint runs in that database. ALTER SYSTEM cannot run in a
# transaction, so psql runs the statement the query builds (\gexec).
RUN set -eux; \
	sed -ri "s/^#?(shared_preload_libraries)\s*=\s*'[^']*'/\1 = 'pg_cron,pgaudit'/" /usr/share/postgresql/postgresql.conf.sample; \
	grep -qF "shared_preload_libraries = 'pg_cron,pgaudit'" /usr/share/postgresql/postgresql.conf.sample; \
	printf '%s\n' "SELECT format('ALTER SYSTEM SET cron.database_name = %L', current_database())" '\gexec' \
		> /docker-entrypoint-initdb.d/jd-full-cron-database.sql

LABEL io.just-dashboard.recipe="${JD_RECIPE}"
