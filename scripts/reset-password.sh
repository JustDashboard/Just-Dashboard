#!/usr/bin/env bash
set -euo pipefail
script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
case "${1:-}" in
	--help|-h) printf 'Usage: sudo ./scripts/reset-password.sh USER [--password-stdin]\n'; exit 0 ;;
esac
exec "$script_dir/manage.sh" reset-password "$@"
