#!/usr/bin/env bash
# Install a verified bundle for this user; execution services remain separate.
set -euo pipefail
prefix=${HOME:?}/.local
upgrade_from=
uninstall_from=
while (( $# )); do
  case "$1" in
    --prefix) [[ $# -ge 2 ]] || exit 2; prefix=$2; shift 2 ;;
    --upgrade-from) [[ $# -ge 2 && -z "$upgrade_from" ]] || exit 2; upgrade_from=$2; shift 2 ;;
    --uninstall-from) [[ $# -ge 2 && -z "$uninstall_from" ]] || exit 2; uninstall_from=$2; shift 2 ;;
    *) echo 'usage: install.sh [--prefix ABSOLUTE_DIRECTORY] [--upgrade-from PRIOR_BUNDLE_SHA256 | --uninstall-from INSTALLED_BUNDLE_SHA256]' >&2; exit 2 ;;
  esac
done
[[ -z "$upgrade_from" || "$upgrade_from" =~ ^[a-f0-9]{64}$ ]] || { echo 'Upgrade requires the exact prior bundle inventory digest.' >&2; exit 2; }
[[ -z "$uninstall_from" || ( -z "$upgrade_from" && "$uninstall_from" =~ ^[a-f0-9]{64}$ ) ]] || { echo 'Uninstall requires one exact installed bundle digest, without upgrade.' >&2; exit 2; }
[[ "$EUID" != 0 ]] || { echo 'Run the app installer as your desktop user.' >&2; exit 2; }
[[ "$prefix" == /* && "$prefix" != / && "$(realpath -m -- "$prefix")" == "$prefix" ]] || { echo 'Use a canonical absolute installation prefix.' >&2; exit 2; }
[[ "$prefix" != *$'\n'* && "$prefix" != *$'\r'* && "$prefix" != *$'\t'* && "$prefix" != *'='* ]] || { echo 'The installation path cannot contain control characters or an equal sign.' >&2; exit 2; }
bundle=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
read -r platform < "$bundle/platform"
[[ "$platform" == "$(uname -s)-$(uname -m)" ]] || { echo 'This bundle does not match this computer.' >&2; exit 1; }

verify_bundle() {
  local directory=$1 receipt=${2:-0} checksum path extra target resolved files=0 links=0 actual
  [[ -f "$directory/bundle.sha256" && ! -L "$directory/bundle.sha256" ]] || return 1
  while read -r checksum path extra; do
    [[ "$checksum" =~ ^[a-f0-9]{64}$ && "$path" =~ ^[a-zA-Z0-9._/@+-]+$ && -z "$extra" && "$path" != /* && "/$path/" != *'/../'* ]] || return 1
    [[ -f "$directory/$path" && ! -L "$directory/$path" ]] || return 1
    ((files+=1))
  done < "$directory/bundle.sha256"
  (( files > 0 )) || return 1
  while IFS=$'\t' read -r path target extra; do
    [[ "$path" =~ ^[a-zA-Z0-9._/@+-]+$ && "$path" != /* && "/$path/" != *'/../'* && -z "$extra" && -L "$directory/$path" ]] || return 1
    [[ "$target" =~ ^[a-zA-Z0-9._/@+-]+$ && "$target" != /* && "$(readlink -- "$directory/$path")" == "$target" ]] || return 1
    resolved=$(realpath -e -- "$directory/$path") || return 1
    [[ "$resolved" == "$directory/"* ]] || return 1
    ((links+=1))
  done < "$directory/bundle-links.list"
  actual=$(find "$directory" -type l -printf '.\n' | wc -l)
  (( links == actual )) || return 1
  actual=$(find "$directory" -type f -printf '.\n' | wc -l)
  (( actual == files + 1 + receipt )) || return 1
  (cd -- "$directory" && sha256sum --check --strict --status bundle.sha256)
}
verify_bundle "$bundle" || { echo 'The bundle inventory or checksum is invalid.' >&2; exit 1; }
bundle_digest=$(sha256sum "$bundle/bundle.sha256"); bundle_digest=${bundle_digest%% *}
for binary in soda-desktop soda-factory-muse factory-runtime/soda-factory-server factory-runtime/postgresql/bin/postgres factory-runtime/postgresql/bin/initdb factory-runtime/postgresql/bin/pg_isready; do
  [[ -x "$bundle/$binary" ]] || { echo "Missing executable: $binary" >&2; exit 1; }
  runtime=$(LD_LIBRARY_PATH="$bundle/factory-runtime/postgresql/lib" ldd "$bundle/$binary" 2>&1) || {
    [[ "$runtime" == *'not a dynamic executable'* || "$runtime" == *'statically linked'* ]] || { echo "Cannot inspect runtime dependencies: $binary" >&2; exit 1; }
  }
  [[ "$runtime" != *'not found'* ]] || { echo "Required runtime libraries are unavailable: $binary" >&2; printf '%s\n' "$runtime" | sed -n '/not found/p' >&2; exit 1; }
done
if [[ -e "$bundle/factory-dev" ]]; then
  [[ -x "$bundle/factory-dev" && ! -L "$bundle/factory-dev" ]] || { echo 'Invalid developer CLI.' >&2; exit 1; }
fi
app="$prefix/lib/factory"
launcher="$prefix/bin/factory"
entry="$prefix/share/applications/factory.desktop"
icon="$prefix/share/icons/hicolor/512x512/apps/factory.png"
if [[ -n "$upgrade_from" && ! -L "$launcher" ]]; then
  echo 'Upgrade requires the original installed launcher.' >&2; exit 1
fi
if [[ -n "$uninstall_from" && ! -f "$prefix/lib/.factory-install.lock" ]]; then
  echo 'Uninstall requires an existing installation.' >&2; exit 1
fi
escaped=$(printf '%s' "$launcher" | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g' -e 's/`/\\`/g' -e 's/\$/\\$/g' -e 's/%/%%/g' -e 's/\\/\\\\/g')
icon_value=$(printf '%s' "$icon" | sed -e 's/\\/\\\\/g')
desktop_entry=$(cat <<ENTRY
[Desktop Entry]
Type=Application
Name=Factory
Exec="$escaped"
Icon=$icon_value
Terminal=false
Categories=Development;
ENTRY
)
for directory in "$prefix/lib" "$prefix/bin" "${entry%/*}" "${icon%/*}"; do
  [[ "$(realpath -m -- "$directory")" == "$directory" ]] || { echo 'Installation directories must be canonical directories.' >&2; exit 1; }
done
mkdir -p -- "$prefix/lib" "$prefix/bin" "${entry%/*}" "${icon%/*}"
lock="$prefix/lib/.factory-install.lock"
[[ ! -L "$lock" && ( ! -e "$lock" || ( -f "$lock" && "$(stat -c %u -- "$lock")" == "$EUID" ) ) ]] || { echo 'Installation lock is not owned by this user.' >&2; exit 1; }
umask 077
exec 9>>"$lock"
flock -n 9 || { echo 'Another installer owns this prefix.' >&2; exit 1; }
receipt=$(printf 'user=%s\nbundle=%s\nprefix=%s' "$EUID" "$bundle_digest" "$prefix")
stage=
cleanup() { if [[ -n "$stage" ]]; then rm -rf -- "$stage"; fi; }
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
expected_launcher=../lib/factory/soda-desktop
next_launcher=$expected_launcher
uninstall_capture="$prefix/lib/.factory-uninstall-$uninstall_from"
selection_digest=$uninstall_from
if [[ -z "$upgrade_from$uninstall_from" && ! -e "$launcher" && ! -L "$launcher" && ( -e "$prefix/lib/.factory-uninstall-$bundle_digest" || -L "$prefix/lib/.factory-uninstall-$bundle_digest" ) ]]; then
  selection_digest=$bundle_digest
fi
if [[ -n "$selection_digest" && ! -e "$launcher" && ! -L "$launcher" ]]; then
  selection_capture="$prefix/lib/.factory-uninstall-$selection_digest"
  [[ -f "$selection_capture" && ! -L "$selection_capture" && "$(stat -c %u -- "$selection_capture")" == "$EUID" && "$(stat -c %s -- "$selection_capture")" -lt 4096 ]] || { echo 'Missing launcher has no retained uninstall capture.' >&2; exit 1; }
  mapfile -t uninstall_lines < "$selection_capture"
  [[ ${#uninstall_lines[@]} == 4 ]] || { echo 'Uninstall capture differs.' >&2; exit 1; }
  expected_launcher=${uninstall_lines[3]#launcher=}
  selection_receipt=$(printf 'user=%s\nbundle=%s\nprefix=%s\nlauncher=%s' "$EUID" "$selection_digest" "$prefix" "$expected_launcher")
  [[ "$(cat -- "$selection_capture")" == "$selection_receipt" ]] || { echo 'Uninstall capture differs.' >&2; exit 1; }
elif [[ -e "$launcher" || -L "$launcher" ]]; then
  [[ -L "$launcher" ]] || { echo 'Existing launcher differs.' >&2; exit 1; }
  expected_launcher=$(readlink -- "$launcher")
fi
if [[ -e "$launcher" || -L "$launcher" || -n "$selection_digest" ]]; then
  case "$expected_launcher" in
    ../lib/factory/soda-desktop) ;;
    ../lib/factory-releases/*/soda-desktop)
      selected_digest=${expected_launcher#../lib/factory-releases/}; selected_digest=${selected_digest%/soda-desktop}
      [[ "$selected_digest" =~ ^[a-f0-9]{64}$ ]] || { echo 'Existing launcher differs.' >&2; exit 1; } ;;
    *) echo 'Existing launcher differs.' >&2; exit 1 ;;
  esac
  current_app=$(realpath -e -- "$prefix/bin/$expected_launcher"); current_app=${current_app%/soda-desktop}
  [[ "$current_app" == "$prefix/lib/factory" || "$current_app" == "$prefix/lib/factory-releases/${selected_digest:-}" ]] || { echo 'Installed payload path differs.' >&2; exit 1; }
  [[ -f "$current_app/bundle.sha256" && ! -L "$current_app/bundle.sha256" ]] || { echo 'Installed inventory is unavailable.' >&2; exit 1; }
  current_digest=$(sha256sum "$current_app/bundle.sha256"); current_digest=${current_digest%% *}
  prior_receipt=$(printf 'user=%s\nbundle=%s\nprefix=%s' "$EUID" "$current_digest" "$prefix")
  [[ -f "$current_app/.factory-install-receipt" && ! -L "$current_app/.factory-install-receipt" && "$(cat -- "$current_app/.factory-install-receipt")" == "$prior_receipt" ]] && verify_bundle "$current_app" 1 || { echo 'Installed payload or ownership differs.' >&2; exit 1; }
  if [[ -n "$uninstall_from" ]]; then
    [[ "$current_digest" == "$uninstall_from" ]] || { echo 'Installed bundle differs from the uninstall command.' >&2; exit 1; }
    app=$current_app
  elif [[ "$current_digest" == "$bundle_digest" ]]; then
    app=$current_app
    next_launcher=$expected_launcher
  elif [[ -n "$upgrade_from" && "$current_digest" == "$upgrade_from" ]]; then
    releases="$prefix/lib/factory-releases"
    [[ ! -e "$releases" || ( -d "$releases" && ! -L "$releases" && "$(realpath -e -- "$releases")" == "$releases" ) ]] || { echo 'Release directory differs.' >&2; exit 1; }
    mkdir -p -- "$releases"
    app="$releases/$bundle_digest"
    next_launcher="../lib/factory-releases/$bundle_digest/soda-desktop"
  else
    echo 'Existing installation differs; an exact --upgrade-from digest is required.' >&2; exit 1
  fi
else
  [[ -z "$upgrade_from" ]] || { echo 'Upgrade requires the original installed launcher.' >&2; exit 1; }
fi
if [[ -n "$uninstall_from" ]]; then
  uninstall_receipt=$(printf 'user=%s\nbundle=%s\nprefix=%s\nlauncher=%s' "$EUID" "$uninstall_from" "$prefix" "$expected_launcher")
  captured=0
  if [[ -e "$uninstall_capture" || -L "$uninstall_capture" ]]; then
    [[ -f "$uninstall_capture" && ! -L "$uninstall_capture" && "$(stat -c %u -- "$uninstall_capture")" == "$EUID" && "$(cat -- "$uninstall_capture")" == "$uninstall_receipt" ]] || { echo 'Uninstall capture differs.' >&2; exit 1; }
    captured=1
  fi
  for target in "$entry" "$icon"; do
    if [[ -e "$target" || -L "$target" ]]; then
      [[ -f "$target" && ! -L "$target" ]] || { echo 'Installed entry differs.' >&2; exit 1; }
      if [[ "$target" == "$entry" ]]; then
        [[ "$(cat -- "$entry")" == "$desktop_entry" ]] || { echo 'Existing desktop entry differs.' >&2; exit 1; }
      else
        cmp -s -- "$app/factory.png" "$icon" || { echo 'Existing icon differs.' >&2; exit 1; }
      fi
    else
      [[ "$captured" == 1 ]] || { echo 'Missing entry has no retained uninstall capture.' >&2; exit 1; }
    fi
  done
  if [[ "$captured" == 0 ]]; then
    (set -C; printf '%s\n' "$uninstall_receipt" > "$uninstall_capture")
  fi
  # Retain every payload path: active app/backend/provider/worker captures and
  # uncertain retirement never become permission to remove their executables.
  for target in "$launcher" "$entry" "$icon"; do
    if [[ -e "$target" || -L "$target" ]]; then rm -- "$target"; fi
  done
  echo 'Factory launch entries removed. Payloads, profiles, credentials and execution services are retained.'
  exit 0
fi
if [[ -n "$upgrade_from" ]]; then
  activation="$prefix/lib/.factory-upgrade-$bundle_digest"
  activation_receipt=$(printf 'user=%s\nprior=%s\nbundle=%s\nprefix=%s' "$EUID" "$upgrade_from" "$bundle_digest" "$prefix")
  if [[ -e "$activation" || -L "$activation" ]]; then
    [[ -f "$activation" && ! -L "$activation" && "$(stat -c %u -- "$activation")" == "$EUID" && "$(cat -- "$activation")" == "$activation_receipt" ]] || { echo 'Upgrade capture differs.' >&2; exit 1; }
  else
    [[ "$current_digest" == "$upgrade_from" ]] || { echo 'This installed bundle has no matching prior upgrade capture.' >&2; exit 1; }
    (set -C; printf '%s\n' "$activation_receipt" > "$activation")
  fi
fi
if [[ -e "$app" || -L "$app" ]]; then
  [[ -d "$app" && ! -L "$app" && -f "$app/.factory-install-receipt" && ! -L "$app/.factory-install-receipt" && "$(cat -- "$app/.factory-install-receipt")" == "$receipt" ]] &&
    cmp -s -- "$bundle/bundle.sha256" "$app/bundle.sha256" && verify_bundle "$app" 1 || {
      echo 'Existing installation differs; retained upgrade handling is required.' >&2; exit 1;
    }
else
  if [[ -z "$upgrade_from" ]]; then
    for target in "$launcher" "$entry" "$icon"; do
      [[ ! -e "$target" && ! -L "$target" ]] || { echo "Existing unrelated installation file: $target" >&2; exit 1; }
    done
  fi
  stage=$(mktemp -d -- "${app%/*}/.factory-install.XXXXXXXX")
  cp -a --reflink=auto -- "$bundle/." "$stage/"
  verify_bundle "$stage" || { echo 'Staged bundle verification failed.' >&2; exit 1; }
  printf '%s\n' "$receipt" > "$stage/.factory-install-receipt"
  chmod 0755 -- "$stage"
  # A killed copy exposes no app; publish the complete verified payload together.
  mv -T --no-clobber -- "$stage" "$app"
  [[ ! -e "$stage" ]] || { echo 'Installation destination changed during staging.' >&2; exit 1; }
  stage=
fi
# Entries are verified before activation; old payload paths and running captures
# remain intact. A killed staging copy never changes the selected launcher.
if [[ -e "$icon" || -L "$icon" ]]; then
  [[ -f "$icon" && ! -L "$icon" ]] && cmp -s -- "$bundle/factory.png" "$icon" || { echo 'Existing icon differs.' >&2; exit 1; }
else
  (set -C; cat "$bundle/factory.png" > "$icon")
  chmod 0644 -- "$icon"
fi
if [[ -e "$entry" || -L "$entry" ]]; then
  [[ -f "$entry" && ! -L "$entry" && "$(cat -- "$entry")" == "$desktop_entry" ]] || { echo 'Existing desktop entry differs.' >&2; exit 1; }
else
  (set -C; printf '%s\n' "$desktop_entry" > "$entry")
  chmod 0644 -- "$entry"
fi
if [[ -e "$launcher" || -L "$launcher" ]]; then
  [[ -L "$launcher" && "$(readlink -- "$launcher")" == "$expected_launcher" ]] || { echo 'Existing launcher changed during installation.' >&2; exit 1; }
  if [[ "$expected_launcher" != "$next_launcher" ]]; then
    stage=$(mktemp -d -- "$prefix/bin/.factory-launcher.XXXXXXXX")
    ln -sT -- "$next_launcher" "$stage/factory"
    mv -T -- "$stage/factory" "$launcher"
    rmdir -- "$stage"
    stage=
  fi
else
  ln -sT -- "$next_launcher" "$launcher"
fi
echo "Factory installed. Launch: $launcher"
echo "Execution services have a separate installer at $app/factory-runtime/execution/install.sh"
