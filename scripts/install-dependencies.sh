#!/usr/bin/env bash
# Sourced by install.sh; safe to source without changing the host.

# jd_apt_refresh refreshes apt's index once per run: the installer installs
# through apt several times, and a second `apt-get update` is a minute of the
# operator's time for nothing. Setting JD_APT_UPDATED=0 asks for another, after
# a repository has been added.
jd_apt_refresh() {
 if [ "${JD_APT_UPDATED:-0}" -ne 1 ]; then
  apt-get update || return 1
  JD_APT_UPDATED=1
 fi
}

# jd_pkg_install <package>... installs through whichever manager the host has.
jd_pkg_install() {
 if command -v apt-get >/dev/null 2>&1; then
  jd_apt_refresh || return 1
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

# jd_os_release <field> prints one field of /etc/os-release, empty when the
# file or the field is missing. The field is a fixed name from this file.
jd_os_release() {
 local file="${JD_OS_RELEASE:-/etc/os-release}"
 [ -r "$file" ] || return 0
 (set +u; . "$file" >/dev/null 2>&1; eval "printf '%s' \"\${$1-}\"")
}

# jd_rhel_family is true on Red Hat Enterprise Linux and its rebuilds, where
# certbot and the terminal's zsh plugins come from EPEL rather than the
# distribution. Fedora has them itself, and Amazon Linux has no EPEL.
jd_rhel_family() {
 case "$(jd_os_release ID)" in
  rhel | centos | rocky | almalinux | ol) return 0 ;;
  fedora | amzn) return 1 ;;
 esac
 case " $(jd_os_release ID_LIKE) " in *" rhel "* | *" centos "*) return 0 ;; esac
 return 1
}

# jd_enable_epel turns on EPEL, and the CodeReady Builder repository its
# packages are built against, where the host is in the Red Hat family and
# EPEL is not on yet. Each distribution ships EPEL its own way.
jd_enable_epel() {
 jd_rhel_family || return 0
 command -v dnf >/dev/null 2>&1 || return 0
 local id major
 id="$(jd_os_release ID)"
 major="$(jd_os_release VERSION_ID)"; major="${major%%.*}"
 if dnf repolist --enabled 2>/dev/null | grep -qiE '^\S*epel'; then
  return 0
 fi
 printf 'Turning on EPEL, where %s keeps certbot\n' "$(jd_os_release NAME)"
 case "$id" in
  ol) jd_pkg_install "oracle-epel-release-el$major" || return 1 ;;
  rhel)
   jd_pkg_install "https://dl.fedoraproject.org/pub/epel/epel-release-latest-$major.noarch.rpm" || return 1
   if command -v subscription-manager >/dev/null 2>&1; then
    subscription-manager repos --enable "codeready-builder-for-rhel-$major-$(uname -m)-rpms" || true
   fi
   ;;
  *) jd_pkg_install epel-release || return 1 ;;
 esac
 if command -v crb >/dev/null 2>&1; then
  crb enable || true
 elif [ "$id" = ol ]; then
  dnf config-manager --set-enabled "ol${major}_codeready_builder" || true
 elif [ "$id" != rhel ]; then
  dnf config-manager --set-enabled crb || true
 fi
}

jd_install_dependencies() {
 local missing=() tool
 for tool in curl openssl certbot; do
  command -v "$tool" >/dev/null 2>&1 || missing+=("$tool")
 done
 if [ "${#missing[@]}" -gt 0 ]; then
  if [[ " ${missing[*]} " == *" certbot "* ]]; then
   jd_enable_epel || return 1
  fi
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
 # openSUSE ships that schedule apart from Certbot, in certbot-systemd-timer;
 # without it nothing would ever renew a certificate.
 if command -v systemctl >/dev/null 2>&1; then
  local timer
  if command -v zypper >/dev/null 2>&1 &&
   ! systemctl cat certbot.timer >/dev/null 2>&1 && ! systemctl cat certbot-renew.timer >/dev/null 2>&1; then
   jd_pkg_install certbot-systemd-timer || return 1
  fi
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
  if ! jd_pkg_install "${missing[@]}"; then
   # Amazon Linux and openSUSE package zsh but neither plugin: zsh on its
   # own, then the plugins from their own repositories.
   command -v zsh >/dev/null 2>&1 || jd_pkg_install zsh || return 1
   jd_clone_zsh_plugins || return 1
  fi
 fi
 command -v zsh >/dev/null 2>&1 || { printf 'zsh is still missing\n' >&2; return 1; }
 for plugin in zsh-autosuggestions zsh-syntax-highlighting; do
  jd_zsh_plugin_present "$plugin" || { printf 'zsh plugin is still missing: %s\n' "$plugin" >&2; return 1; }
 done
}

# jd_clone_zsh_plugins puts each plugin the distribution does not package
# where a package would (/usr/share/<plugin>/<plugin>.zsh), cloned at a
# release tag of the zsh-users repository the packages are built from.
jd_clone_zsh_plugins() {
 local entry plugin tag
 for entry in zsh-autosuggestions:v0.7.1 zsh-syntax-highlighting:0.8.0; do
  plugin="${entry%%:*}" tag="${entry#*:}"
  jd_zsh_plugin_present "$plugin" && continue
  command -v git >/dev/null 2>&1 || jd_pkg_install git || return 1
  printf 'Installing %s %s from github.com/zsh-users\n' "$plugin" "$tag"
  rm -rf "/usr/share/$plugin"
  git -c advice.detachedHead=false clone --quiet --depth 1 --branch "$tag" \
   "https://github.com/zsh-users/$plugin.git" "/usr/share/$plugin" || return 1
  rm -rf "/usr/share/$plugin/.git"
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

# jd_host_tool_package <tool> names the package that carries a host tool
# where its name is not the tool's: ip and ss come in iproute2 (iproute in
# the Red Hat family, which keeps tc apart in iproute-tc), and last, since
# Debian 13 and Ubuntu 26.04 dropped util-linux's, in wtmpdb.
jd_host_tool_package() {
 local rpm=0
 { command -v dnf >/dev/null 2>&1 || command -v yum >/dev/null 2>&1; } && rpm=1
 case "$1" in
  ip | ss) [ "$rpm" -eq 1 ] && echo iproute || echo iproute2 ;;
  tc) [ "$rpm" -eq 1 ] && echo iproute-tc || echo iproute2 ;;
  last) echo wtmpdb ;;
  *) echo "$1" ;;
 esac
}

# The web terminal and ssh are shells on the host, so a push from either runs
# the host's git — and the GitHub sign-in made on the Git page reaches it only
# through the host's gh. The Security tools page runs whois and traceroute on
# the host for the same reason, the Network pages read the host's ip, ss and
# tc, a packet capture runs its tcpdump, and Security's login history its
# last. Every server install has ip and ss, but a minimal or container image
# may not. Best effort by design: each
# missing tool is installed on its own so one unavailable package does not
# cost the others, and what could not be installed is named rather than fatal.
jd_install_host_tools() {
 local failed=() tool package
 for tool in git git-lfs whois traceroute ip ss tc tcpdump last; do
  command -v "$tool" >/dev/null 2>&1 && continue
  package="$(jd_host_tool_package "$tool")"
  printf 'Installing host tool: %s\n' "$tool"
  jd_pkg_install "$package" && command -v "$tool" >/dev/null 2>&1 || failed+=("$tool")
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

# ── Docker ──────────────────────────────────────────────────────────────────

# jd_debian_codename prints the Debian release a Debian-based distribution is
# built on, from its os-release or /etc/debian_version, or nothing when it is
# a rolling one that names no release.
jd_debian_codename() {
 local codename version
 codename="$(jd_os_release DEBIAN_CODENAME)"
 if [ -z "$codename" ] && [ -r "${JD_DEBIAN_VERSION:-/etc/debian_version}" ]; then
  IFS= read -r version <"${JD_DEBIAN_VERSION:-/etc/debian_version}" || true
  case "$version" in
   11 | 11.* | bullseye*) codename=bullseye ;;
   12 | 12.* | bookworm*) codename=bookworm ;;
   13 | 13.* | trixie*) codename=trixie ;;
  esac
 fi
 printf '%s' "$codename"
}

# jd_docker_plan prints how this host gets Docker Engine with its Compose and
# Buildx plugins, as one line:
#
#   get-docker               Docker's own script, which knows this distribution
#   apt-repo <dist> <code>   Docker's apt repository for the Ubuntu or Debian
#                            release this distribution is built on
#   rpm-repo <dist>          Docker's rpm repository for the CentOS or Fedora
#                            this distribution is built on
#   packages <package>...    the distribution's own packages
#   plugins <package>...     the distribution's engine, with Compose and Buildx
#                            from Docker's releases where it packages neither
#
# get.docker.com refuses every distribution it does not name, so a derivative
# such as Pop!_OS, Kali or deepin, and Arch, openSUSE and Amazon Linux, are
# given the nearest of the others.
jd_docker_plan() {
 local id like codename
 id="$(jd_os_release ID)"
 like=" $(jd_os_release ID_LIKE) "
 case "$id" in
  ubuntu | debian | raspbian | centos | rhel | fedora | rocky | almalinux | ol)
   echo get-docker
   return 0
   ;;
  amzn)
   echo "plugins docker"
   return 0
   ;;
 esac
 if command -v apt-get >/dev/null 2>&1; then
  codename="$(jd_os_release UBUNTU_CODENAME)"
  if [ -n "$codename" ]; then
   echo "apt-repo ubuntu $codename"
   return 0
  fi
  codename="$(jd_debian_codename)"
  if [ -n "$codename" ]; then
   echo "apt-repo debian $codename"
   return 0
  fi
  echo "packages docker.io docker-compose docker-buildx"
  return 0
 fi
 if command -v pacman >/dev/null 2>&1 || command -v zypper >/dev/null 2>&1; then
  echo "packages docker docker-compose docker-buildx"
  return 0
 fi
 if command -v dnf >/dev/null 2>&1; then
  case "$like" in
   *" rhel "* | *" centos "*) echo "rpm-repo centos"; return 0 ;;
   *" fedora "*) echo "rpm-repo fedora"; return 0 ;;
  esac
 fi
 return 1
}

# jd_docker_apt_repo <ubuntu|debian> <codename> installs Docker's packages
# from its apt repository, as get.docker.com would for that release. It fails,
# leaving no repository behind, when Docker publishes nothing for it.
jd_docker_apt_repo() {
 local dist="$1" codename="$2" list=/etc/apt/sources.list.d/docker.list
 mkdir -p /etc/apt/keyrings || return 1
 curl -fsSL "https://download.docker.com/linux/$dist/gpg" -o /etc/apt/keyrings/docker.asc || return 1
 chmod a+r /etc/apt/keyrings/docker.asc
 printf 'deb [arch=%s signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/%s %s stable\n' \
  "$(dpkg --print-architecture)" "$dist" "$codename" >"$list" || return 1
 JD_APT_UPDATED=0
 if ! jd_pkg_install docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin; then
  rm -f "$list"
  JD_APT_UPDATED=0
  return 1
 fi
}

# jd_github_release_file <repo> <tag> <file> <dest> downloads one asset of a
# GitHub release and checks it against the checksum the release publishes,
# either beside it (<file>.sha256) or in one checksums.txt.
jd_github_release_file() {
 local repo="$1" tag="$2" file="$3" dest="$4" base sums want
 base="https://github.com/$repo/releases/download/$tag"
 curl -fsSL "$base/$file" -o "$dest" || return 1
 if sums="$(curl -fsSL "$base/$file.sha256" 2>/dev/null)"; then
  want="${sums%% *}"
 else
  sums="$(curl -fsSL "$base/checksums.txt")" || return 1
  want="$(printf '%s\n' "$sums" | awk -v f="$file" '$2 == f || $2 == "*" f {print $1; exit}')"
 fi
 [ -n "$want" ] && [ "$(sha256sum "$dest" | cut -d' ' -f1)" = "$want" ] && return 0
 printf '%s from %s %s does not match its published checksum\n' "$file" "$repo" "$tag" >&2
 rm -f "$dest"
 return 1
}

# jd_github_latest_tag <repo> prints the tag GitHub's latest release redirects to.
jd_github_latest_tag() {
 local url
 url="$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/$1/releases/latest")" || return 1
 url="${url##*/}"
 [ -n "$url" ] && [ "$url" != latest ] && printf '%s' "$url"
}

# jd_buildx_recent is true when docker buildx answers and is at least 0.17,
# the oldest Compose builds with ("compose build requires buildx 0.17.0 or
# later"). Amazon Linux packages an older one.
jd_buildx_recent() {
 local version major minor
 version="$(docker buildx version 2>/dev/null | grep -oE '[0-9]+\.[0-9]+' | head -n 1)" || return 1
 [ -n "$version" ] || return 1
 major="${version%%.*}" minor="${version#*.}"
 [ "$major" -gt 0 ] || [ "$minor" -ge 17 ]
}

# jd_install_docker_plugins installs Compose and Buildx from Docker's own
# releases, checksums checked, where the distribution has no Compose v2 or a
# Buildx older than Compose builds with (Amazon Linux packages neither
# plugin, and an old Buildx inside its engine). The CLI looks in
# /usr/local/lib/docker/cli-plugins before the packages' own directories.
jd_install_docker_plugins() {
 local dir=/usr/local/lib/docker/cli-plugins arch goarch tag
 case "$(uname -m)" in
  x86_64 | amd64) arch=x86_64 goarch=amd64 ;;
  aarch64 | arm64) arch=aarch64 goarch=arm64 ;;
  *) return 1 ;;
 esac
 mkdir -p "$dir" || return 1
 if ! docker compose version >/dev/null 2>&1; then
  tag="$(jd_github_latest_tag docker/compose)" || return 1
  jd_github_release_file docker/compose "$tag" "docker-compose-linux-$arch" "$dir/docker-compose" || return 1
  chmod 0755 "$dir/docker-compose"
 fi
 if ! jd_buildx_recent; then
  tag="$(jd_github_latest_tag docker/buildx)" || return 1
  jd_github_release_file docker/buildx "$tag" "buildx-$tag.linux-$goarch" "$dir/docker-buildx" || return 1
  chmod 0755 "$dir/docker-buildx"
 fi
}

# jd_apt_docker_packages names a Debian-based distribution's own Docker
# packages. Debian testing, and Kali after it, moved the client out of
# docker.io into docker-cli, which docker.io only recommends; the older
# releases have no docker-cli and carry the client in docker.io.
jd_apt_docker_packages() {
 local packages="docker.io docker-compose docker-buildx"
 jd_apt_refresh >/dev/null || true
 if apt-cache show docker-cli >/dev/null 2>&1; then
  packages="docker.io docker-cli docker-compose docker-buildx"
 fi
 printf '%s' "$packages"
}

# jd_install_docker installs Docker Engine, Compose and Buildx the way this
# distribution can have them (jd_docker_plan), starts it at boot and checks
# that the build the stack needs will answer: Compose v2 builds through Buildx.
jd_install_docker() {
 local plan
 plan="$(jd_docker_plan)" || {
  printf 'No known way to install Docker on %s. Install Docker Engine with its Compose and Buildx plugins, then run this again.\n' \
   "$(jd_os_release PRETTY_NAME)" >&2
  return 1
 }
 set -- $plan
 case "$1" in
  get-docker)
   curl -fsSL https://get.docker.com | sh || return 1
   ;;
  apt-repo)
   if ! jd_docker_apt_repo "$2" "$3"; then
    printf "Docker's repository has nothing for %s %s; using %s's own packages.\n" "$2" "$3" "$(jd_os_release NAME)"
    jd_pkg_install $(jd_apt_docker_packages) || return 1
   fi
   ;;
  rpm-repo)
   curl -fsSL "https://download.docker.com/linux/$2/docker-ce.repo" -o /etc/yum.repos.d/docker-ce.repo || return 1
   jd_pkg_install docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin || return 1
   ;;
  packages)
   shift
   if [ "$1" = docker.io ]; then
    jd_pkg_install $(jd_apt_docker_packages) || return 1
   else
    jd_pkg_install "$@" || return 1
   fi
   ;;
  plugins)
   shift
   jd_pkg_install "$@" || return 1
   jd_install_docker_plugins || return 1
   ;;
 esac
 systemctl enable --now docker || return 1
 if ! docker compose version >/dev/null 2>&1 || ! jd_buildx_recent; then
  jd_install_docker_plugins || true
 fi
 if ! docker compose version >/dev/null 2>&1; then
  printf 'Docker is installed, but `docker compose` does not answer: the stack needs the Compose v2 plugin.\n' >&2
  return 1
 fi
 if ! jd_buildx_recent; then
  printf 'Docker is installed, but `docker buildx` is missing or older than 0.17, which Compose builds the images with.\n' >&2
  return 1
 fi
}

# jd_docker_source names where jd_install_docker would take Docker from, for
# the question that asks first.
jd_docker_source() {
 local plan
 plan="$(jd_docker_plan)" || { echo "nowhere this installer knows"; return 0; }
 set -- $plan
 case "$1" in
  get-docker) echo "Docker's own repository (get.docker.com)" ;;
  apt-repo) echo "Docker's own repository for ${2^} $3" ;;
  rpm-repo) [ "$2" = centos ] && echo "Docker's own repository for CentOS" || echo "Docker's own repository for Fedora" ;;
  packages) echo "$(jd_os_release NAME)'s own packages" ;;
  plugins) echo "$(jd_os_release NAME)'s own packages, with Compose and Buildx from Docker's releases" ;;
 esac
}
