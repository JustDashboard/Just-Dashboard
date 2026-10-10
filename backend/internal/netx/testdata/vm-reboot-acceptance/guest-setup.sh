#!/usr/bin/env bash
#
# Runs once as root inside the disposable guest (vm.sh provision). It installs
# the owners and the dashboard backend and writes the three native owner
# fixtures; the host then reboots the guest so every owner starts from its
# saved configuration.

set -euo pipefail
stage=${1:?stage directory}
steps=${2:-packages,owners,backend}
exec > >(tee -a /var/log/jd-vm-setup.log) 2>&1

packages() {
echo "== offline owner closure"
dpkg -i "$stage"/debs/*.deb
echo "== nftables, iptables, Docker and ping from the guest's signed archive"
apt-get -q update
DEBIAN_FRONTEND=noninteractive apt-get -q install -y --no-install-recommends nftables iptables docker.io iputils-ping
for module in vrf ifb sch_cake tcp_bbr; do
	modprobe "$module" && echo "module $module loaded"
done
}

owners() {
echo "== native owner fixtures"
# Netplan's network-level renderer is global across every file (the last one
# wins), so each fixture names its renderer on its own definition.
# enp0s4: authored Netplan with the networkd renderer.
install -m 0600 /dev/stdin /etc/netplan/60-jdvm-networkd.yaml <<'EOF'
network:
  version: 2
  ethernets:
    enp0s4:
      renderer: networkd
      dhcp4: false
      dhcp6: false
      accept-ra: false
      addresses: [10.0.4.15/24, 'fd00:4::15/64']
      nameservers:
        addresses: [10.0.4.3]
        search: [nd.jdvm.test]
      routes:
        - to: 10.90.4.0/24
          via: 10.0.4.2
          metric: 100
EOF
# enp0s5: authored Netplan with the NetworkManager renderer (Ubuntu's
# exact-origin strategy; NetworkManager 1.46 carries the Netplan writer).
install -m 0600 /dev/stdin /etc/netplan/61-jdvm-nm.yaml <<'EOF'
network:
  version: 2
  ethernets:
    enp0s5:
      renderer: NetworkManager
      dhcp4: false
      dhcp6: false
      accept-ra: false
      addresses: [10.0.5.15/24, 'fd00:5::15/64']
      nameservers:
        addresses: [10.0.5.3]
        search: [nm.jdvm.test]
      networkmanager:
        uuid: '5d2b0c39-0b5e-4c8e-9a43-6a0f0e6a4d05'
        name: jdvm-nm
EOF
# jdnm0: an authored Netplan dummy device rendered by NetworkManager. Netplan
# renders every NetworkManager ethernet with wake-on-lan=0, a property the
# adapter does not yet verify, so enp0s5 above stays a refusal witness and
# this device carries the editable NetworkManager owner path.
install -m 0600 /dev/stdin /etc/netplan/62-jdvm-nm-dummy.yaml <<'EOF'
network:
  version: 2
  dummy-devices:
    jdnm0:
      renderer: NetworkManager
      dhcp4: false
      dhcp6: false
      accept-ra: false
      addresses: [10.0.7.15/24, 'fd00:7::15/64']
      nameservers:
        addresses: [10.0.7.3]
        search: [nmd.jdvm.test]
      routes:
        - to: 10.90.7.0/24
          via: 10.0.7.2
          metric: 100
      networkmanager:
        uuid: '8c4b6c8e-2f8a-4d55-9a54-3b1a6c0d7e07'
        name: jdnm0
EOF
# enp0s6: a direct systemd-networkd profile with no Netplan origin.
install -m 0644 /dev/stdin /etc/systemd/network/30-jdvm-direct.network <<'EOF'
[Match]
Name=enp0s6

[Network]
Address=10.0.6.15/24
Address=fd00:6::15/64
DNS=10.0.6.3
Domains=direct.jdvm.test
DHCP=no
IPv6AcceptRA=no
LinkLocalAddressing=ipv6

[Route]
Destination=10.90.6.0/24
Gateway=10.0.6.2
Metric=100
EOF
# NetworkManager manages every device Netplan does not deny it; keep the
# direct networkd profile single-owned.
install -m 0644 /dev/stdin /etc/udev/rules.d/90-jdvm-nm-unmanaged.rules <<'EOF'
SUBSYSTEM=="net", ACTION=="add|change|move", ATTR{address}=="52:54:00:4a:44:06", ENV{NM_UNMANAGED}="1"
EOF
netplan generate
}

backend() {
echo "== dashboard backend"
install -m 0755 "$stage/just-dashboard" /usr/local/bin/just-dashboard
install -d -m 0700 /var/lib/just-dashboard /etc/jd-vm-acceptance
install -d -m 0755 /usr/local/lib/jd-vm-acceptance /usr/local/lib/jd-vm-acceptance/shim
install -m 0755 "$stage/guest.py" /usr/local/lib/jd-vm-acceptance/guest.py
install -m 0755 "$stage/fault-shim.sh" /usr/local/lib/jd-vm-acceptance/fault-shim.sh
ln -sf ../fault-shim.sh /usr/local/lib/jd-vm-acceptance/shim/ip
ln -sf ../fault-shim.sh /usr/local/lib/jd-vm-acceptance/shim/systemctl
if [ ! -e /etc/jd-vm-acceptance/backend.env ]; then
	umask 077
	cat >/etc/jd-vm-acceptance/backend.env <<EOF
JD_ADDR=127.0.0.1:8080
JD_DATA_DIR=/var/lib/just-dashboard
JD_MASTER_KEY=$(od -An -tx1 -N32 /dev/urandom | tr -d ' \n')
JD_ALLOWED_CIDRS=127.0.0.1/32,::1/128
JD_TRUSTED_PROXIES=127.0.0.1/32
JD_SITE=localhost
JD_TLS=internal
JD_TERMINAL_ENABLED=false
JD_BOOTSTRAP_USER=admin
JD_BOOTSTRAP_PASSWORD=Vm-$(od -An -tx1 -N18 /dev/urandom | tr -d ' \n')-Z
JD_DOCKER_HOST=unix:///var/run/docker.sock
JD_UPDATE_CHECK=false
JD_LOG_LEVEL=info
EOF
fi
install -m 0644 "$stage/jd-vm-backend.service" /etc/systemd/system/jd-vm-backend.service
systemctl daemon-reload
# The backend is deliberately not enabled: every boot is first inspected
# without it, so what the guest finds was restored by the host units alone.
systemctl disable jd-vm-backend.service 2>/dev/null || true
}

none() { :; }

for step in ${steps//,/ }; do
	"$step"
done
rm -rf "$stage/debs"
echo "== setup complete: $steps"
