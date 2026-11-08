#!/usr/bin/env bash
# Install a verified bundle for this user; execution services remain separate.
set -euo pipefail
prefix=${HOME:?}/.local
while (( $# )); do
  case "$1" in
    --prefix) [[ $# -ge 2 ]] || exit 2; prefix=$2; shift 2 ;;
    *) echo 'usage: install.sh [--prefix ABSOLUTE_DIRECTORY]' >&2; exit 2 ;;
  esac
done
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
app="$prefix/lib/factory"
launcher="$prefix/bin/factory"
entry="$prefix/share/applications/factory.desktop"
icon="$prefix/share/icons/hicolor/512x512/apps/factory.png"
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
if [[ -e "$app" || -L "$app" ]]; then
  [[ -d "$app" && ! -L "$app" && -f "$app/.factory-install-receipt" && ! -L "$app/.factory-install-receipt" && "$(cat -- "$app/.factory-install-receipt")" == "$receipt" ]] &&
    cmp -s -- "$bundle/bundle.sha256" "$app/bundle.sha256" && verify_bundle "$app" 1 || {
      echo 'Existing installation differs; retained upgrade handling is required.' >&2; exit 1;
    }
else
  for target in "$launcher" "$entry" "$icon"; do
    [[ ! -e "$target" && ! -L "$target" ]] || { echo "Existing unrelated installation file: $target" >&2; exit 1; }
  done
  stage=$(mktemp -d -- "$prefix/lib/.factory-install.XXXXXXXX")
  cp -a --reflink=auto -- "$bundle/." "$stage/"
  verify_bundle "$stage" || { echo 'Staged bundle verification failed.' >&2; exit 1; }
  printf '%s\n' "$receipt" > "$stage/.factory-install-receipt"
  chmod 0755 -- "$stage"
  # A killed copy exposes no app; publish the complete verified payload together.
  mv -T --no-clobber -- "$stage" "$app"
  [[ ! -e "$stage" ]] || { echo 'Installation destination changed during staging.' >&2; exit 1; }
  stage=
fi
# Resume entries only for the exact verified bundle. Never roll published files
# back by pathname over concurrent human edits.
if [[ -e "$launcher" || -L "$launcher" ]]; then
  [[ -L "$launcher" && "$(readlink -- "$launcher")" == ../lib/factory/soda-desktop ]] || { echo 'Existing launcher differs.' >&2; exit 1; }
else
  ln -sT -- ../lib/factory/soda-desktop "$launcher"
fi
if [[ -e "$icon" || -L "$icon" ]]; then
  [[ -f "$icon" && ! -L "$icon" ]] && cmp -s -- "$bundle/factory.png" "$icon" || { echo 'Existing icon differs.' >&2; exit 1; }
else
  (set -C; cat "$bundle/factory.png" > "$icon")
  chmod 0644 -- "$icon"
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
if [[ -e "$entry" || -L "$entry" ]]; then
  [[ -f "$entry" && ! -L "$entry" && "$(cat -- "$entry")" == "$desktop_entry" ]] || { echo 'Existing desktop entry differs.' >&2; exit 1; }
else
  (set -C; printf '%s\n' "$desktop_entry" > "$entry")
  chmod 0644 -- "$entry"
fi
echo "Factory installed. Launch: $launcher"
echo "Execution services have a separate installer at $app/factory-runtime/execution/install.sh"
