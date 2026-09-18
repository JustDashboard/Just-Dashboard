#!/bin/sh
# Turns JD_TLS into the two things the Caddyfile needs: a scheme for the site
# address, and a tls directive.
#
# It exists because a Caddyfile cannot branch. The three ways this dashboard is
# reached want three different answers — a real certificate over Tailscale, a
# self-signed one over any other private network, and no certificate at all on
# loopback where an SSH tunnel already provides the encryption — and the
# alternative to computing them here was three Caddyfiles, or an installer that
# edits a tracked one and makes every future `git pull` a merge conflict.
#
# Everything below is derived from one setting. JD_TLS is what an operator sets
# and what the dashboard's settings page writes; JD_SCHEME and JD_TLS_DIRECTIVE
# are never written anywhere and cannot drift from it.

set -eu

CERT_DIR=${JD_CERT_DIR:-/certs}

case "${JD_TLS:-internal}" in
off)
	# Plain HTTP, and the Caddyfile's own bind keeps it on loopback. This is
	# the only configuration a browser raises nothing at all about: http on
	# localhost is a secure context, so there is no warning, no exception to
	# add, and nothing for an operator to learn to click through.
	JD_SCHEME=http
	JD_TLS_DIRECTIVE=""
	;;
tailscale)
	# A publicly trusted certificate for a name that resolves only inside the
	# tailnet. The backend runs `tailscale cert` on the host and renews it well
	# before it expires; this is only the half that serves the result.
	if [ ! -s "$CERT_DIR/site.crt" ] || [ ! -s "$CERT_DIR/site.key" ]; then
		echo "just-dashboard: JD_TLS=tailscale but no certificate in $CERT_DIR;" >&2
		echo "  falling back to Caddy's internal CA so the dashboard still answers." >&2
		echo "  Run: tailscale cert --cert-file $CERT_DIR/site.crt --key-file $CERT_DIR/site.key \$JD_SITE" >&2
		JD_SCHEME=https
		JD_TLS_DIRECTIVE="tls internal"
	else
		JD_SCHEME=https
		JD_TLS_DIRECTIVE="tls $CERT_DIR/site.crt $CERT_DIR/site.key"
	fi
	;;
*)
	JD_SCHEME=https
	JD_TLS_DIRECTIVE="tls internal"
	;;
esac

# What Caddy listens on, as opposed to what it answers for. They are usually
# the same string and are deliberately separable, because a Tailscale install
# answers for a MagicDNS name it must not try to bind: this container inherits
# a resolver from Docker rather than from the host, so `box.tailnet.ts.net`
# may not resolve in here even where it resolves perfectly on the machine —
# and a proxy that cannot resolve its own bind address does not start at all.
# The installer records the tailnet IP in JD_BIND for exactly that reason.
JD_BIND=${JD_BIND:-${JD_SITE:-localhost}}

export JD_SCHEME JD_TLS_DIRECTIVE JD_BIND

exec caddy run --config /etc/caddy/Caddyfile --adapter caddyfile
