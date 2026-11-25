#!/bin/bash
# Install the bundled macOS execution boundary for one explicitly selected
# desktop user: root-owned helpers, hidden pool users for isolated units, and
# the launchd broker socket. Run with sudo from the verified distribution.
set -euo pipefail
execution_user=
while (( $# )); do
  case "$1" in
    --user) [[ $# -ge 2 ]] || exit 2; execution_user=$2; shift 2 ;;
    *) echo 'usage: install.sh --user USER' >&2; exit 2 ;;
  esac
done
[[ "$execution_user" =~ ^[a-zA-Z_][a-zA-Z0-9_.-]*$ ]] || { echo 'An explicit local desktop user is required.' >&2; exit 2; }
[[ "$EUID" == 0 ]] || { echo 'Execution installation requires root (sudo).' >&2; exit 2; }
execution_uid=$(id -u -- "$execution_user")
[[ "$execution_uid" =~ ^[0-9]+$ && "$execution_uid" != 0 ]] || { echo 'The desktop execution user must not be root.' >&2; exit 2; }
bundle=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
distribution=$(cd -- "$bundle/../.." && pwd -P)
[[ -f "$distribution/bundle.sha256" && ! -L "$distribution/bundle.sha256" && -f "$bundle/files.list" && ! -L "$bundle/files.list" ]] &&
  (cd -- "$distribution" && /usr/bin/shasum -a 256 -c --status bundle.sha256) || { echo 'Verified distribution inventory is required for execution installation.' >&2; exit 1; }
read -r platform < "$distribution/platform"
[[ "$platform" == "$(uname -s)-$(uname -m)" ]] || { echo 'This execution bundle does not match this computer.' >&2; exit 1; }
# Fixed tools the units run. /usr/bin/git is an xcrun shim; units use the
# real Command Line Tools or Xcode binary.
git=
for candidate in /Library/Developer/CommandLineTools/usr/bin/git /Applications/Xcode.app/Contents/Developer/usr/bin/git; do
  if [[ -x "$candidate" ]] && "$candidate" --version >/dev/null 2>&1; then git=$candidate; break; fi
done
[[ -n "$git" ]] || { echo 'Execution requires Git from the Xcode Command Line Tools: run xcode-select --install.' >&2; exit 1; }
for runtime in /bin/bash /usr/bin/hdiutil /usr/bin/dscl /bin/launchctl; do
  [[ -x "$runtime" ]] || { echo "Execution runtime is unavailable: $runtime" >&2; exit 1; }
done

libexec=/Library/PrivilegedHelperTools/soda-factory
state=/private/var/run/soda-factory-broker
label=soda.factory.broker
host_label=soda.factory.host
for directory in "$state/runs" "$state/preparations"; do
  if [[ -d "$directory" && -n "$(find "$directory" -mindepth 1 -maxdepth 1 ! -name .lock -print -quit)" ]]; then
    echo 'Retained execution runs must retire before reinstalling. Quit Factory and retry.' >&2
    exit 1
  fi
done

binaries=()
while IFS=$'\t' read -r kind name extra; do
  [[ -z "$extra" && "$kind" == libexec && "$name" =~ ^soda-factory-[a-z-]+$ ]] || { echo 'Invalid execution payload inventory.' >&2; exit 1; }
  [[ -f "$bundle/libexec/$name" && ! -L "$bundle/libexec/$name" && -x "$bundle/libexec/$name" ]] || { echo "Missing execution helper: $name" >&2; exit 1; }
  binaries+=("$name")
done < "$bundle/files.list"
for required in soda-factory-broker soda-factory-preparer soda-factory-preparation-cleaner soda-factory-agent soda-factory-provider-relay soda-factory-reviewer soda-factory-timeout; do
  [[ " ${binaries[*]} " == *" $required "* ]] || { echo "Missing execution helper: $required" >&2; exit 1; }
done

# Every ancestor of the helper directory stays root-owned.
install -d -o root -g wheel -m 0755 "$libexec"
for name in "${binaries[@]}"; do
  pending=$(mktemp "$libexec/.install.XXXXXXXX")
  install -o root -g wheel -m 0755 "$bundle/libexec/$name" "$pending"
  mv -f "$pending" "$libexec/$name"
done

# Hidden pool users replace systemd DynamicUser: one user and same-numbered
# group per concurrently running unit, with no shell, home or password.
used=" $(dscl . -list /Users UniqueID | awk '{print $2}' | tr '\n' ' ') $(dscl . -list /Groups PrimaryGroupID | awk '{print $2}' | tr '\n' ' ') "
next_id=300
for index in $(seq -w 1 32); do
  name="_sodafactory$index"
  if dscl . -read "/Users/$name" UniqueID >/dev/null 2>&1; then continue; fi
  while [[ "$used" == *" $next_id "* ]]; do ((next_id+=1)); done
  (( next_id < 500 )) || { echo 'No free system account IDs for Factory pool users.' >&2; exit 1; }
  dscl . -create "/Groups/$name"
  dscl . -create "/Groups/$name" PrimaryGroupID "$next_id"
  dscl . -create "/Groups/$name" Password '*'
  dscl . -create "/Groups/$name" RealName "Factory isolated unit $index"
  dscl . -create "/Users/$name"
  dscl . -create "/Users/$name" UniqueID "$next_id"
  dscl . -create "/Users/$name" PrimaryGroupID "$next_id"
  dscl . -create "/Users/$name" UserShell /usr/bin/false
  dscl . -create "/Users/$name" NFSHomeDirectory /var/empty
  dscl . -create "/Users/$name" RealName "Factory isolated unit $index"
  dscl . -create "/Users/$name" Password '*'
  dscl . -create "/Users/$name" IsHidden 1
  used+="$next_id "
done

write_plist() {
  local path=$1 pending
  pending=$(mktemp "/Library/LaunchDaemons/.soda-factory.XXXXXXXX")
  cat > "$pending"
  chown root:wheel "$pending"
  chmod 0644 "$pending"
  /usr/bin/plutil -lint -s "$pending"
  mv -f "$pending" "$path"
}
write_plist "/Library/LaunchDaemons/$host_label.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>$host_label</string>
  <key>ProgramArguments</key>
  <array><string>$libexec/soda-factory-broker</string><string>prepare-host</string><string>$execution_uid</string></array>
  <key>RunAtLoad</key><true/>
  <key>UserName</key><string>root</string>
</dict>
</plist>
PLIST
write_plist "/Library/LaunchDaemons/$label.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>$label</string>
  <key>ProgramArguments</key><array><string>$libexec/soda-factory-broker</string></array>
  <key>inetdCompatibility</key><dict><key>Wait</key><false/></dict>
  <key>Sockets</key>
  <dict>
    <key>Listeners</key>
    <dict>
      <key>SockPathName</key><string>/private/var/run/soda-factory/broker.sock</string>
      <key>SockPathMode</key><integer>384</integer>
      <key>SockPathOwner</key><integer>$execution_uid</integer>
    </dict>
  </dict>
  <key>UserName</key><string>root</string>
  <key>Umask</key><integer>63</integer>
  <!-- inetd mode wires stderr to the client socket; refusals go to the log. -->
  <key>StandardErrorPath</key><string>/private/var/log/soda-factory-broker.log</string>
  <key>AbandonProcessGroup</key><true/>
</dict>
</plist>
PLIST

/bin/launchctl bootout "system/$label" 2>/dev/null || true
/bin/launchctl bootout "system/$host_label" 2>/dev/null || true
"$libexec/soda-factory-broker" prepare-host "$execution_uid"
/bin/launchctl bootstrap system "/Library/LaunchDaemons/$host_label.plist"
/bin/launchctl bootstrap system "/Library/LaunchDaemons/$label.plist"
[[ -S /private/var/run/soda-factory/broker.sock ]] || { echo 'The broker socket did not start.' >&2; exit 1; }
echo "Factory execution services installed for $execution_user (UID $execution_uid)."
