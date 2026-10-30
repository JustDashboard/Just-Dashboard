#!/usr/bin/env bash
# Compose interprets unquoted dollar signs and whitespace in .env. Encode a
# literal scalar without executing it or relying on shell-style single quotes.
jd_dotenv_literal() {
 local value="$1"
 case "$value" in
  *$'\n'*|*$'\r'*) return 1 ;;
 esac
 value="${value//\\/\\\\}"
 value="${value//\"/\\\"}"
 value="${value//\$/\$\$}"
 printf '"%s"' "$value"
}

# Remove one variable's assignments from a dotenv file. sed -i keeps the file's
# mode and owner, which for .env are the point.
jd_dotenv_drop() {
 sed -i "/^$1=/d" "$2"
}
