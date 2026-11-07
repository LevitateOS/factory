#!/usr/bin/env bash
# Install this app bundle for the current user; system execution services are separate.
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
for binary in soda-desktop soda-factory-muse factory-runtime/soda-factory-server; do
  [[ -f "$bundle/$binary" && -x "$bundle/$binary" ]] || { echo "Missing app executable: $binary" >&2; exit 1; }
done
for item in factory-runtime/postgresql factory-runtime/execution licenses LICENSE NOTICE factory.png; do
  [[ -e "$bundle/$item" ]] || { echo "Missing app bundle item: $item" >&2; exit 1; }
done
if ! runtime=$(ldd "$bundle/soda-desktop" 2>&1) || [[ "$runtime" == *'not found'* ]]; then
  echo 'Required desktop runtime libraries are unavailable. Install the GTK/WebKit runtime for this bundle before installing Factory.' >&2
  printf '%s\n' "$runtime" | sed -n '1p;/not found/p' >&2
  exit 1
fi
app="$prefix/lib/factory"
launcher="$prefix/bin/factory"
entry="$prefix/share/applications/factory.desktop"
icon="$prefix/share/icons/hicolor/512x512/apps/factory.png"
for target in "$app" "$launcher" "$entry" "$icon"; do
  [[ ! -e "$target" && ! -L "$target" ]] || { echo "Existing installation requires upgrade handling: $target" >&2; exit 1; }
done
created=()
committed=0
cleanup() {
  local result=$?
  if (( result && ! committed )); then
    for target in "${created[@]}"; do rm -f -- "$target"; done
    if [[ "${app_created:-0}" == 1 ]]; then rm -rf -- "$app"; fi
  fi
}
trap cleanup EXIT
mkdir -p -- "$prefix/lib" "$prefix/bin" "${entry%/*}" "${icon%/*}"
mkdir -- "$app"
app_created=1
for item in soda-desktop soda-factory-muse LICENSE NOTICE licenses platform; do
  cp -a --reflink=auto -- "$bundle/$item" "$app/"
done
mkdir -- "$app/factory-runtime"
for item in soda-factory-server postgresql execution; do
  cp -a --reflink=auto -- "$bundle/factory-runtime/$item" "$app/factory-runtime/"
done
# No profile, credentials or generated state are copied or moved by installation.
ln -sT -- ../lib/factory/soda-desktop "$launcher"
created+=("$launcher")
(set -C; cat "$bundle/factory.png" > "$icon")
created+=("$icon")
escaped=$(printf '%s' "$launcher" | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g' -e 's/`/\\`/g' -e 's/\$/\\$/g' -e 's/%/%%/g' -e 's/\\/\\\\/g')
icon_value=$(printf '%s' "$icon" | sed -e 's/\\/\\\\/g')
(set -C; cat > "$entry" <<ENTRY
[Desktop Entry]
Type=Application
Name=Factory
Exec="$escaped"
Icon=$icon_value
Terminal=false
Categories=Development;
ENTRY
)
created+=("$entry")
committed=1
echo "Factory installed. Launch: $launcher"
echo "Execution services have a separate installer at $app/factory-runtime/execution/install.sh"
