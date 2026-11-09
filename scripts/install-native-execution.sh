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
distribution=$(cd -- "$bundle/../.." && pwd -P)
[[ -f "$distribution/bundle.sha256" && ! -L "$distribution/bundle.sha256" && -f "$bundle/files.list" && ! -L "$bundle/files.list" ]] &&
  (cd -- "$distribution" && sha256sum --check --strict --status bundle.sha256) || { echo 'Verified distribution inventory is required for execution installation.' >&2; exit 1; }
read -r platform < "$distribution/platform"
[[ "$platform" == "$(uname -s)-$(uname -m)" ]] || { echo 'This execution bundle does not match this computer.' >&2; exit 1; }
if [[ "$install_root" == / ]]; then
  [[ -d /run/systemd/system && -f /sys/fs/cgroup/cgroup.controllers ]] && systemctl --version >/dev/null || { echo 'Execution installation requires systemd and cgroup v2.' >&2; exit 1; }
  # PrivatePIDs is required by the agent boundary; older managers ignore it.
  read -r manager manager_version rest < <(systemctl --version)
  [[ "$manager" == systemd && "$manager_version" =~ ^[0-9]+$ && "$manager_version" -ge 257 ]] || { echo 'Execution installation requires systemd 257 or newer (PrivatePIDs).' >&2; exit 1; }
  controllers=" $(cat /sys/fs/cgroup/cgroup.controllers) "
  for controller in cpu memory pids; do
    [[ "$controllers" == *" $controller "* ]] || { echo "Execution requires the cgroup v2 $controller controller." >&2; exit 1; }
  done
  read -r runtime_type runtime_blocks runtime_free runtime_block_size < <(stat -f -c '%T %b %a %S' -- /run)
  [[ "$runtime_type" == tmpfs && "$runtime_blocks" =~ ^[0-9]+$ && "$runtime_free" =~ ^[0-9]+$ && "$runtime_block_size" =~ ^[0-9]+$ ]] || { echo 'Execution requires /run on tmpfs.' >&2; exit 1; }
  # Admit one preparer's 1 GiB ceiling plus the broker's independent reserve.
  # The broker rechecks current space and all retained reservations per launch.
  runtime_reserve=$((runtime_blocks * runtime_block_size / 4))
  (( runtime_reserve >= 268435456 )) || runtime_reserve=268435456
  (( runtime_free * runtime_block_size >= runtime_reserve + 1073741824 )) || { echo 'Execution requires free /run tmpfs space for a 1 GiB preparation plus the larger of 256 MiB or one quarter of /run. Enlarge /run before installing execution services.' >&2; exit 1; }
  runtime_directory="/run/user/$execution_uid"
  [[ -d "$runtime_directory" && ! -L "$runtime_directory" && "$(realpath -e -- "$runtime_directory")" == "$runtime_directory" && "$(stat -c %u -- "$runtime_directory")" == "$execution_uid" && "$(stat -c %a -- "$runtime_directory")" == 700 ]] || { echo 'Execution requires the selected user’s private /run/user/UID session directory.' >&2; exit 1; }
  # These are fixed runtime paths in the native preparer/worker, independent
  # of developer toolchains and the installing shell's PATH.
  for runtime in /usr/bin/git /usr/bin/bash /usr/bin/timeout; do
    [[ -x "$runtime" ]] && "$runtime" --version >/dev/null 2>&1 || { echo "Execution runtime is unavailable: $runtime" >&2; exit 1; }
  done
fi
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
# Read every source before admitting an installation. Upgrades still need their
# retained-run protocol; only this exact bundle/user may resume partial files.
for source in "${binaries[@]}" "${units[@]}"; do
  [[ -f "$source" && ! -L "$source" ]] || { echo 'Execution payload must contain regular files.' >&2; exit 1; }
  name=${source##*/}
  if [[ "$source" == "$bundle/libexec/"* ]]; then
    [[ -x "$source" ]] || { echo 'A bundled helper is not executable.' >&2; exit 1; }
    target="$libexec/$name"
  else
    target="$systemd/${name%.in}"
  fi
  targets+=("$target")
done
# Keep the admitted manifest before publishing any target. A hard interruption
# can then adopt exact completed files, without overwriting an unknown file or
# inferring ownership from its pathname. The lock also covers service activation.
state="${install_root%/}/var/lib/soda-factory-install"
for directory in "${install_root%/}/var" "${install_root%/}/var/lib" "$state"; do
  if [[ -e "$directory" || -L "$directory" ]]; then
    [[ -d "$directory" && ! -L "$directory" && "$(realpath -e -- "$directory")" == "$directory" ]] || { echo 'Installation state must use canonical directories.' >&2; exit 1; }
  fi
done
if [[ -d "$state" ]]; then
  [[ "$(stat -c %u -- "$state")" == "$EUID" && "$(stat -c %a -- "$state")" == 700 ]] || { echo 'Installation state ownership or permissions differ.' >&2; exit 1; }
else
  install -d -m 0700 -- "$state"
fi
[[ ! -L "$state/lock" && ( ! -e "$state/lock" || ( -f "$state/lock" && "$(stat -c %u -- "$state/lock")" == "$EUID" ) ) ]] || { echo 'Installation lock ownership differs.' >&2; exit 1; }
exec 9>>"$state/lock"
flock -n 9 || { echo 'Another execution installer owns this root.' >&2; exit 1; }
stage=$(mktemp -d -- "$state/stage.XXXXXXXX")
pending_file=
cleanup() { if [[ -n "$pending_file" ]]; then rm -f -- "$pending_file"; fi; rm -rf -- "$stage"; }
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
printf 'user=%s\nuid=%s\n' "$execution_user" "$execution_uid" > "$stage/manifest"
index=0
for source in "${binaries[@]}" "${units[@]}"; do
  target=${targets[index]}
  mode=0755
  if [[ "$source" == "$bundle/libexec/"* ]]; then
    install -m "$mode" -- "$source" "$stage/$index"
  else
    mode=0644
    sed -e "s/@DESKTOP_USER@/$execution_user/g" -e "s/@DESKTOP_UID@/$execution_uid/g" -- "$source" | install -m "$mode" /dev/stdin "$stage/$index"
  fi
  digest=$(sha256sum "$stage/$index"); digest=${digest%% *}
  printf '%s\t%s\t%s\n' "$digest" "$mode" "${target#"${install_root%/}"}" >> "$stage/manifest"
  ((index+=1))
done
if [[ -e "$state/manifest" || -L "$state/manifest" ]]; then
  [[ -f "$state/manifest" && ! -L "$state/manifest" ]] && cmp -s -- "$stage/manifest" "$state/manifest" || { echo 'Execution installation differs; retained upgrade handling is required.' >&2; exit 1; }
else
  for target in "${targets[@]}"; do
    [[ ! -e "$target" && ! -L "$target" ]] || { echo "Existing installation file requires upgrade handling: $target" >&2; exit 1; }
  done
  ln -T -- "$stage/manifest" "$state/manifest"
fi
for directory in "$libexec" "$systemd"; do
  if [[ ! -d "$directory" ]]; then install -d -m 0755 -- "$directory"; fi
done
index=0
for target in "${targets[@]}"; do
  if [[ ! -e "$target" && ! -L "$target" ]]; then
    # Linking a complete file is atomic and refuses a competing destination.
    # Stage beside the destination: /var and /usr may be separate filesystems.
    pending_file=$(mktemp -- "${target%/*}/.soda-factory-install.XXXXXXXX")
    install -m "$(stat -c %a -- "$stage/$index")" -- "$stage/$index" "$pending_file"
    ln -T -- "$pending_file" "$target"
    rm -f -- "$pending_file"
    pending_file=
  fi
  [[ -f "$target" && ! -L "$target" && "$(stat -c %u -- "$target")" == "$EUID" && "$(stat -c %a -- "$target")" == "$(stat -c %a -- "$stage/$index")" ]] && cmp -s -- "$stage/$index" "$target" || { echo "Execution installation file changed: $target" >&2; exit 1; }
  ((index+=1))
done
# Leave a complete payload available for diagnosis if service activation fails.
if [[ "$install_root" == / ]]; then
  systemctl daemon-reload
  systemctl enable --now soda-factory-broker.socket
  echo "Factory execution services installed for $execution_user (UID $execution_uid)."
else
  echo "Factory execution image staged for $execution_user (UID $execution_uid); services were not activated."
fi
