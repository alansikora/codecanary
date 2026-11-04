#!/usr/bin/env bash
# Installs bubblewrap for fix.format_command's sandbox and the codex fix's
# command sandbox, and makes sure it can create user namespaces where AppArmor
# restricts them. Ubuntu 24.04 needs a profile for /usr/bin/bwrap only, not the
# system-wide switch. Ubuntu 26.04 ships its own, and a second profile attaching
# to the same binary makes AppArmor apply neither, so ours goes in only when
# bwrap doesn't already work.
set -euo pipefail

command -v bwrap >/dev/null || { sudo apt-get update -qq && sudo apt-get install -y -qq bubblewrap; } >/dev/null

probe() { bwrap --ro-bind / / --unshare-user --unshare-pid true; }

probe 2>/dev/null && exit 0

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

probe
