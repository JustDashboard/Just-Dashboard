#!/usr/bin/env bash
# Sourced by install.sh; safe to source without changing the host.
jd_install_dependencies() {
 local missing=() tool
 for tool in curl openssl certbot; do
  command -v "$tool" >/dev/null 2>&1 || missing+=("$tool")
 done
 if [ "${#missing[@]}" -gt 0 ]; then
  printf 'Installing required host tools: %s\n' "${missing[*]}"
  if command -v apt-get >/dev/null 2>&1; then
   apt-get update || return 1
   DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends "${missing[@]}" || return 1
  elif command -v dnf >/dev/null 2>&1; then
   dnf install -y "${missing[@]}" || return 1
  elif command -v yum >/dev/null 2>&1; then
   yum install -y "${missing[@]}" || return 1
  elif command -v apk >/dev/null 2>&1; then
   apk add --no-cache "${missing[@]}" || return 1
  elif command -v zypper >/dev/null 2>&1; then
   zypper --non-interactive install "${missing[@]}" || return 1
  elif command -v pacman >/dev/null 2>&1; then
   # Avoid a partial rolling-release upgrade: use the configured package databases.
   pacman -S --needed --noconfirm "${missing[@]}" || return 1
  else
   printf 'No supported package manager can install: %s\n' "${missing[*]}" >&2
   return 1
  fi
 fi
 for tool in curl openssl certbot; do
  command -v "$tool" >/dev/null 2>&1 || { printf 'Required tool is still missing: %s\n' "$tool" >&2; return 1; }
 done
 certbot --version || return 1
 local plugins
 plugins="$(certbot plugins --non-interactive)" || return 1
 if [[ "$plugins" != *standalone* || "$plugins" != *webroot* ]]; then
  printf 'Certbot is installed but its standalone/webroot authenticators are unavailable.\n' >&2
  return 1
 fi
 # Distribution packages own the renewal schedule. Do not start a web server
 # or stop an existing port owner as a side effect of installing Certbot.
 if command -v systemctl >/dev/null 2>&1; then
  local timer
  for timer in certbot.timer certbot-renew.timer; do
   if systemctl cat "$timer" >/dev/null 2>&1; then
    systemctl enable --now "$timer" || return 1
    break
   fi
  done
 fi
}
