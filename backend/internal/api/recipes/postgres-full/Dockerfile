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

LABEL io.just-dashboard.recipe="${JD_RECIPE}"
