#!/usr/bin/env bash
# Sourced by install.sh; safe to source without changing the host.

# jd_pkg_install <package>... installs through whichever manager the host has.
# apt's index is refreshed once per run: the installer calls this twice, and a
# second `apt-get update` is a minute of the operator's time for nothing.
jd_pkg_install() {
 if command -v apt-get >/dev/null 2>&1; then
  if [ "${JD_APT_UPDATED:-0}" -ne 1 ]; then
   apt-get update || return 1
   JD_APT_UPDATED=1
  fi
  DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends "$@" || return 1
 elif command -v dnf >/dev/null 2>&1; then
  dnf install -y "$@" || return 1
 elif command -v yum >/dev/null 2>&1; then
  yum install -y "$@" || return 1
 elif command -v apk >/dev/null 2>&1; then
  apk add --no-cache "$@" || return 1
 elif command -v zypper >/dev/null 2>&1; then
  zypper --non-interactive install "$@" || return 1
 elif command -v pacman >/dev/null 2>&1; then
  # Avoid a partial rolling-release upgrade: use the configured package databases.
  pacman -S --needed --noconfirm "$@" || return 1
 else
  printf 'No supported package manager can install: %s\n' "$*" >&2
  return 1
 fi
}

jd_install_dependencies() {
 local missing=() tool
 for tool in curl openssl certbot; do
  command -v "$tool" >/dev/null 2>&1 || missing+=("$tool")
 done
 if [ "${#missing[@]}" -gt 0 ]; then
  printf 'Installing required host tools: %s\n' "${missing[*]}"
  jd_pkg_install "${missing[@]}" || return 1
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

# The web terminal runs the host's shell, so the shell that gives it inline
# history suggestions and command colouring has to be a host package too. The
# plugin files land where the distribution puts them; the bundled zsh startup
# looks in both of the places used across the managers above.
jd_zsh_plugin_present() {
 local file
 for file in "/usr/share/$1/$1.zsh" "/usr/share/zsh/plugins/$1/$1.zsh"; do
  [ -r "$file" ] && return 0
 done
 return 1
}

# Best effort by design: a host whose manager lacks these packages still gets
# a working dashboard, just with its account's own shell in the terminal.
jd_install_terminal_extras() {
 local missing=() plugin
 command -v zsh >/dev/null 2>&1 || missing+=(zsh)
 for plugin in zsh-autosuggestions zsh-syntax-highlighting; do
  jd_zsh_plugin_present "$plugin" || missing+=("$plugin")
 done
 if [ "${#missing[@]}" -gt 0 ]; then
  printf 'Installing terminal shell packages: %s\n' "${missing[*]}"
  jd_pkg_install "${missing[@]}" || return 1
 fi
 command -v zsh >/dev/null 2>&1 || { printf 'zsh is still missing\n' >&2; return 1; }
 for plugin in zsh-autosuggestions zsh-syntax-highlighting; do
  jd_zsh_plugin_present "$plugin" || { printf 'zsh plugin is still missing: %s\n' "$plugin" >&2; return 1; }
 done
}
