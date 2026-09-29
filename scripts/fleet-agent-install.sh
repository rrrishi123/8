#!/usr/bin/env bash
# Install a per-user pull watcher. Its config is private and read at every start.
set -euo pipefail
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
mode="${1:?usage: fleet-agent-install.sh native|colima}"
case "$mode" in native|colima) ;; *) echo 'mode must be native or colima' >&2; exit 1;; esac
node_bin="$(command -v node)"
command -v gh >/dev/null
state="${FLEET_STATE_DIR:-$HOME/.8/fleet/$mode}"
mkdir -p "$state"
chmod 700 "$state"
config="$state/agent.env"
if [ ! -f "$config" ]; then
  # bash %q preserves paths/spaces without evaluating configuration values.
  { printf 'export FLEET_MODE=%q\n' "$mode"
    printf 'export FLEET_STATE_DIR=%q\n' "$state"
    printf 'export FLEET_ROOT=%q\n' "${FLEET_ROOT:-$(cd "$here/../.." && pwd)}"
    printf 'export FLEET_HOST=%q\n' "${FLEET_HOST:-$(hostname -s)-$mode}"
    printf 'export PATH=%q\n' "$PATH"
  } >"$config"
  chmod 600 "$config"
fi
runner="$state/run.sh"
{ echo '#!/usr/bin/env bash'
  echo 'set -euo pipefail'
  printf 'source %q\n' "$config"
  printf 'exec %q %q\n' "$node_bin" "$here/fleet-agent.mjs"
} >"$runner"
chmod 700 "$runner"
label="dev.eight.fleet.$mode"
if [ "$(uname -s)" = Darwin ]; then
  mkdir -p "$HOME/Library/LaunchAgents"
  plist="$HOME/Library/LaunchAgents/$label.plist"
  # plutil handles XML escaping and typed values for paths containing spaces.
  plutil -create xml1 "$plist"
  plutil -insert Label -string "$label" "$plist"
  plutil -insert ProgramArguments -json "[\"/bin/bash\"]" "$plist"
  plutil -insert ProgramArguments.1 -string "$runner" "$plist"
  plutil -insert RunAtLoad -bool YES "$plist"
  plutil -insert KeepAlive -bool YES "$plist"
  plutil -insert ThrottleInterval -integer 30 "$plist"
  plutil -insert StandardOutPath -string "$state/agent.log" "$plist"
  plutil -insert StandardErrorPath -string "$state/agent.log" "$plist"
  launchctl bootout "gui/$(id -u)/$label" 2>/dev/null || true
  launchctl bootstrap "gui/$(id -u)" "$plist"
else
  unit_dir="$HOME/.config/systemd/user"
  mkdir -p "$unit_dir"
  # systemd quoting differs from shell quoting; encode path characters explicitly.
  escaped="$(printf '%s' "$runner" | sed 's/\\/\\\\/g; s/"/\\"/g; s/%/%%/g')"
  cat >"$unit_dir/$label.service" <<EOF
[Unit]
Description=Eight CI-gated fleet deployment ($mode)
After=network-online.target
[Service]
ExecStart=/bin/bash "$escaped"
Restart=always
RestartSec=30
[Install]
WantedBy=default.target
EOF
  systemctl --user daemon-reload
  systemctl --user enable --now "$label.service"
fi
echo "Installed $label; private config: $config; receipts: $state/deployed.json"
