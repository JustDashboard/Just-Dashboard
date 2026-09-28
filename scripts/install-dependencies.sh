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

# gh from GitHub's own repository on Debian and Ubuntu, whose packaged copy is
# years behind on an LTS release; the distribution's elsewhere, falling back to
# GitHub's RPM repository where the distribution has none. The same source the
# backend image installs from, so both read the token in one format.
jd_install_gh() {
 command -v gh >/dev/null 2>&1 && return 0
 if command -v apt-get >/dev/null 2>&1; then
  mkdir -p /etc/apt/keyrings /etc/apt/sources.list.d || return 1
  curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg \
   -o /etc/apt/keyrings/githubcli-archive-keyring.gpg || return 1
  chmod a+r /etc/apt/keyrings/githubcli-archive-keyring.gpg || return 1
  printf 'deb [arch=%s signed-by=/etc/apt/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main\n' \
   "$(dpkg --print-architecture)" > /etc/apt/sources.list.d/github-cli.list || return 1
  # The index was refreshed before this repository existed.
  JD_APT_UPDATED=0
  jd_pkg_install gh || return 1
 elif command -v dnf >/dev/null 2>&1 || command -v yum >/dev/null 2>&1; then
  if ! jd_pkg_install gh; then
   mkdir -p /etc/yum.repos.d || return 1
   curl -fsSL https://cli.github.com/packages/rpm/gh-cli.repo -o /etc/yum.repos.d/gh-cli.repo || return 1
   jd_pkg_install gh || return 1
  fi
 elif command -v zypper >/dev/null 2>&1; then
  if ! jd_pkg_install gh; then
   zypper --non-interactive addrepo https://cli.github.com/packages/rpm/gh-cli.repo || return 1
   zypper --non-interactive --gpg-auto-import-keys refresh || return 1
   jd_pkg_install gh || return 1
  fi
 else
  jd_pkg_install github-cli || return 1
 fi
 command -v gh >/dev/null 2>&1
}

# The web terminal and ssh are shells on the host, so a push from either runs
# the host's git — and the GitHub sign-in made on the Git page reaches it only
# through the host's gh. The Security tools page runs whois and traceroute on
# the host for the same reason. Best effort by design: each missing tool is
# installed on its own so one unavailable package does not cost the others,
# and what could not be installed is named rather than fatal.
jd_install_host_tools() {
 local failed=() tool
 for tool in git git-lfs whois traceroute; do
  command -v "$tool" >/dev/null 2>&1 && continue
  printf 'Installing host tool: %s\n' "$tool"
  jd_pkg_install "$tool" && command -v "$tool" >/dev/null 2>&1 || failed+=("$tool")
 done
 if ! command -v gh >/dev/null 2>&1; then
  printf 'Installing host tool: gh\n'
  jd_install_gh || failed+=(gh)
 fi
 if [ "${#failed[@]}" -gt 0 ]; then
  printf 'Could not install: %s\n' "${failed[*]}" >&2
  return 1
 fi
}
