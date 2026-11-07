#!/usr/bin/env bash
# Install the bundled system boundary for one explicitly selected desktop user.
set -euo pipefail
execution_user=
install_root=/
while (( $# )); do
  case "$1" in
    --user) [[ $# -ge 2 ]] || exit 2; execution_user=$2; shift 2 ;;
    --root) [[ $# -ge 2 ]] || exit 2; install_root=$2; shift 2 ;;
    *) echo 'usage: install.sh --user USER [--root EXISTING_IMAGE_DIRECTORY]' >&2; exit 2 ;;
  esac
done
[[ "$execution_user" =~ ^[a-zA-Z_][a-zA-Z0-9_.-]*$ ]] || { echo 'An explicit local desktop user is required.' >&2; exit 2; }
execution_uid=$(id -u -- "$execution_user")
[[ "$execution_uid" =~ ^[0-9]+$ && "$execution_uid" != 0 ]] || { echo 'The desktop execution user must not be root.' >&2; exit 2; }
[[ "$install_root" == /* && -d "$install_root" && "$(realpath -e -- "$install_root")" == "$install_root" ]] || { echo 'The installation root must be an existing canonical absolute directory.' >&2; exit 2; }
if [[ "$install_root" == / && "$EUID" != 0 ]]; then
  echo 'System installation requires root; --root stages an image without activating services.' >&2
  exit 2
fi
bundle=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
binaries=()
units=()
while IFS=$'\t' read -r kind name extra; do
  [[ -z "$extra" ]] || { echo 'Invalid execution payload inventory.' >&2; exit 1; }
  case "$kind" in
    libexec)
      [[ "$name" =~ ^soda-factory-[a-z-]+$ ]] || exit 1
      binaries+=("$bundle/libexec/$name") ;;
    systemd)
      [[ "$name" =~ ^soda-factory-[a-z-]+@?\.(service|socket)(\.in)?$ ]] || exit 1
      units+=("$bundle/systemd/$name") ;;
    *) echo 'Invalid execution payload inventory.' >&2; exit 1 ;;
  esac
done < "$bundle/files.list"
(( ${#binaries[@]} && ${#units[@]} )) || { echo 'The bundled execution payload is missing.' >&2; exit 1; }
libexec="${install_root%/}/usr/libexec"
systemd="${install_root%/}/etc/systemd/system"
targets=()
for directory in "${install_root%/}/usr" "$libexec" "${install_root%/}/etc" "${install_root%/}/etc/systemd" "$systemd"; do
  if [[ -e "$directory" || -L "$directory" ]]; then
    [[ -d "$directory" && ! -L "$directory" && "$(realpath -e -- "$directory")" == "$directory" ]] || { echo 'Installation directories must be canonical directories.' >&2; exit 1; }
  fi
done
# Check the complete destination before writing. Upgrades need their own retained-run protocol.
for source in "${binaries[@]}" "${units[@]}"; do
  [[ -f "$source" && ! -L "$source" ]] || { echo 'Execution payload must contain regular files.' >&2; exit 1; }
  name=${source##*/}
  if [[ "$source" == "$bundle/libexec/"* ]]; then
    [[ -x "$source" ]] || { echo 'A bundled helper is not executable.' >&2; exit 1; }
    target="$libexec/$name"
  else
    target="$systemd/${name%.in}"
  fi
  [[ ! -e "$target" && ! -L "$target" ]] || { echo "Existing installation file requires upgrade handling: $target" >&2; exit 1; }
  targets+=("$target")
done
created=()
committed=0
cleanup() {
  local result=$?
  if (( result && ! committed )); then
    for target in "${created[@]}"; do rm -f -- "$target"; done
  fi
}
trap cleanup EXIT
for directory in "$libexec" "$systemd"; do
  if [[ ! -d "$directory" ]]; then install -d -m 0755 -- "$directory"; fi
done
index=0
for source in "${binaries[@]}"; do
  target=${targets[index++]}
  created+=("$target")
  install -m 0755 -- "$source" "$target"
done
for source in "${units[@]}"; do
  target=${targets[index++]}
  created+=("$target")
  sed -e "s/@DESKTOP_USER@/$execution_user/g" -e "s/@DESKTOP_UID@/$execution_uid/g" -- "$source" | install -m 0644 /dev/stdin "$target"
done
# Leave a complete payload available for diagnosis if service activation fails.
committed=1
if [[ "$install_root" == / ]]; then
  systemctl daemon-reload
  systemctl enable --now soda-factory-broker.socket
  echo "Factory execution services installed for $execution_user (UID $execution_uid)."
else
  echo "Factory execution image staged for $execution_user (UID $execution_uid); services were not activated."
fi
