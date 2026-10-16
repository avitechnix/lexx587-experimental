#!/usr/bin/env bash
# Removes the ffagent service, binary, config, encrypted credentials and state.
set -euo pipefail
[[ $EUID -eq 0 ]] || { echo "run with sudo" >&2; exit 1; }
read -r -p "Remove ffagent, its config, encrypted vault and state? [y/N] " r </dev/tty
[[ $r =~ ^[Yy] ]] || exit 0
systemctl disable --now ffagent-watch.service 2>/dev/null || true
rm -f /etc/systemd/system/ffagent-watch.service
systemctl daemon-reload
rm -f /usr/local/bin/ffagent /usr/local/bin/ffagentctl /etc/credstore.encrypted/ffagent-passphrase
rm -rf /etc/ffagent /var/lib/ffagent
id ffagent >/dev/null 2>&1 && userdel ffagent
echo "ffagent removed. (Go in /usr/local/go, if the installer added it, was left in place.)"
