#!/usr/bin/env bash
# Installs bubblewrap for fix.format_command's sandbox and the codex fix's
# command sandbox, and lets it create
# user namespaces where AppArmor restricts them (Ubuntu 24.04 runners): a
# profile for /usr/bin/bwrap only, not the system-wide switch.
set -euo pipefail

command -v bwrap >/dev/null || { sudo apt-get update -qq && sudo apt-get install -y -qq bubblewrap; } >/dev/null

if [ "$(cat /proc/sys/kernel/apparmor_restrict_unprivileged_userns 2>/dev/null)" = 1 ]; then
  sudo tee /etc/apparmor.d/codecanary-bwrap >/dev/null <<'PROFILE'
abi <abi/4.0>,
include <tunables/global>

profile codecanary-bwrap /usr/bin/bwrap flags=(unconfined) {
  userns,
}
PROFILE
  sudo apparmor_parser -r /etc/apparmor.d/codecanary-bwrap
fi

bwrap --ro-bind / / --unshare-user --unshare-pid true
