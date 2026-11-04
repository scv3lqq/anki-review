#!/usr/bin/env bash
# Install only the isolated privileged daemon. The owner HTTP service is never restarted here.
set -euo pipefail
umask 077

fail() { echo "install-multi-account: $*" >&2; exit 1; }
[[ $(uname -s) == Linux && $EUID == 0 ]] || fail "run as root on Linux"
[[ $# == 1 && $1 =~ ^[a-z_][a-z0-9_-]*\$?$ ]] || fail "usage: $0 <public-http-user>"
http_user=$1
id "$http_user" >/dev/null 2>&1 || fail "public HTTP user does not exist"
[[ $(id -u "$http_user") != 0 ]] || fail "public HTTP user cannot be root"
command -v runuser >/dev/null || fail "runuser is required to verify Docker socket isolation"
[[ " $(id -nG "$http_user") " != *" docker "* ]] || fail "remove the HTTP user from the Docker group first"
if runuser -u "$http_user" -- test -w /var/run/docker.sock; then
  fail "remove direct Docker socket access from the public HTTP user first"
fi

validate_settings() {
  [[ $1 =~ ^[1-9][0-9]{0,8}$ && $2 =~ ^[1-9][0-9]{0,8}$ ]] || fail "invalid image UID/GID"
  [[ $3 =~ ^[1-7]$ ]] || fail "MAX_GUESTS must be 1..7"
  [[ $4 =~ ^[1-9][0-9]{7,10}$ ]] || fail "invalid STORAGE_BYTES"
  (( $4 >= 67108864 && $4 <= 68719476736 )) || fail "STORAGE_BYTES must be 64 MiB..64 GiB"
}
saved_anki_uid=1000
saved_anki_gid=1000
saved_max_guests=3
saved_storage_bytes=2147483648
if [[ -e /etc/anki-review-provisioner.conf || -L /etc/anki-review-provisioner.conf ]]; then
  [[ -f /etc/anki-review-provisioner.conf && ! -L /etc/anki-review-provisioner.conf ]] || fail "invalid saved daemon configuration"
  [[ $(stat -c '%u:%a' /etc/anki-review-provisioner.conf) == 0:600 ]] || fail "saved daemon configuration must be root-owned mode 0600"
  # Parse only four numeric settings. Never source or evaluate the saved configuration.
  seen_settings=' '
  saved_count=0
  while IFS='=' read -r setting value; do
    [[ $seen_settings != *" $setting "* ]] || fail "duplicate saved daemon setting"
    case $setting in
      ANKI_UID) saved_anki_uid=$value ;;
      ANKI_GID) saved_anki_gid=$value ;;
      MAX_GUESTS) saved_max_guests=$value ;;
      STORAGE_BYTES) saved_storage_bytes=$value ;;
      *) fail "unknown saved daemon setting" ;;
    esac
    seen_settings+="$setting "
    saved_count=$((saved_count + 1))
  done </etc/anki-review-provisioner.conf
  [[ $saved_count == 4 ]] || fail "saved daemon configuration must contain all four settings"
  validate_settings "$saved_anki_uid" "$saved_anki_gid" "$saved_max_guests" "$saved_storage_bytes"
fi
anki_uid=${ANKI_UID-$saved_anki_uid}
anki_gid=${ANKI_GID-$saved_anki_gid}
max_guests=${MAX_GUESTS-$saved_max_guests}
storage_bytes=${STORAGE_BYTES-$saved_storage_bytes}
validate_settings "$anki_uid" "$anki_gid" "$max_guests" "$storage_bytes"
[[ $(id -u "$http_user") != "$anki_uid" ]] || fail "container UID must differ from the public HTTP user"

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
binary="$script_dir/bin/anki-review-provisioner-linux-amd64"
if [[ -f "$script_dir/../go.mod" && -d "$script_dir/../cmd/anki-review-provisioner" ]]; then
  binary="$script_dir/../bin/anki-review-provisioner-linux-amd64"
fi
[[ -f $binary && ! -L $binary ]] || fail "build bin/anki-review-provisioner-linux-amd64 first"
for tool in /usr/bin/docker /usr/bin/findmnt /usr/bin/mount /usr/bin/umount /usr/sbin/losetup /usr/sbin/blkid /usr/sbin/mkfs.ext4; do
  [[ -x $tool ]] || fail "required tool missing: $tool"
done
export DOCKER_HOST=unix:///var/run/docker.sock
unset DOCKER_CONTEXT DOCKER_TLS_VERIFY DOCKER_CERT_PATH
image=anki-desktop:26.09.3-ac26.10.14.0-audio1
/usr/bin/docker image inspect "$image" >/dev/null || fail "build the pinned Anki image first, with ANKI_UID/ANKI_GID matching this daemon"
probe=(/usr/bin/docker run --rm --network none --read-only --cap-drop ALL --security-opt no-new-privileges:true --memory 16m --memory-swap 16m --cpus 0.1 --pids-limit 16 --entrypoint /usr/bin/id "$image")
image_uid=$("${probe[@]}" -u) || fail "cannot check pinned image UID"
image_gid=$("${probe[@]}" -g) || fail "cannot check pinned image GID"
[[ $image_uid == "$anki_uid" && $image_gid == "$anki_gid" ]] || fail "pinned image UID/GID must match ANKI_UID/ANKI_GID"
getent group anki-review >/dev/null || groupadd --system anki-review
[[ $(getent group anki-review | cut -d: -f3) != 0 ]] || fail "broker group cannot be a root group alias"
usermod -a -G anki-review "$http_user"

for directory in /var/lib/anki-review-provisioner /run/anki-review-provisioner; do
  [[ ! -L $directory ]] || fail "refusing symlink directory: $directory"
  if [[ -e $directory ]]; then
    [[ $(stat -c '%u' "$directory") == 0 ]] || fail "daemon directory must be root-owned: $directory"
    [[ $(find "$directory" -maxdepth 0 -perm /0022 -print) == '' ]] || fail "daemon directory cannot be writable by other users: $directory"
  fi
done
install -d -o root -g root -m 0700 /var/lib/anki-review-provisioner
install -d -o root -g anki-review -m 0750 /run/anki-review-provisioner
install -d -o root -g root -m 0755 /usr/local/libexec
if systemctl cat anki-review-provisioner.service >/dev/null 2>&1; then
  systemctl stop anki-review-provisioner.service
fi
# Disable old Docker auto-start policy only after validating every managed guest candidate.
managed_container_ids=$(/usr/bin/docker container ls --all --filter label=io.anki-review.managed=provisioner-v1 --format '{{.ID}}') || fail "cannot inspect managed guests"
managed_ids=()
while IFS= read -r container_id; do
  [[ -n $container_id ]] || continue
  [[ $container_id =~ ^[0-9a-f]{12,64}$ ]] || fail "invalid managed container identifier"
  metadata=$(/usr/bin/docker container inspect --format '{{ index .Config.Labels "io.anki-review.managed" }} {{ index .Config.Labels "io.anki-review.account" }} {{ index .Config.Labels "io.anki-review.reservation" }} {{.Name}}' "$container_id") || fail "cannot verify managed guest labels"
  read -r managed account reservation container_name extra <<<"$metadata"
  [[ $managed == provisioner-v1 && $account =~ ^[0-9a-f]{32}$ && $reservation =~ ^ar-[0-9a-f]{32}$ && $container_name == "/$reservation" && -z ${extra:-} ]] || fail "refusing foreign managed-label container"
  managed_ids+=("$container_id")
  (( ${#managed_ids[@]} <= 7 )) || fail "managed container count exceeds the independent guest bound"
done <<<"$managed_container_ids"
if (( ${#managed_ids[@]} > 0 )); then
  for container_id in "${managed_ids[@]}"; do
    /usr/bin/docker container update --restart=no "$container_id" >/dev/null || fail "cannot disable guest Docker restart policy"
  done
fi
install -o root -g root -m 0755 "$binary" /usr/local/libexec/anki-review-provisioner
config_file=$(mktemp /etc/.anki-review-provisioner.XXXXXX)
trap 'rm -f -- "$config_file"' EXIT
printf 'ANKI_UID=%s\nANKI_GID=%s\nMAX_GUESTS=%s\nSTORAGE_BYTES=%s\n' "$anki_uid" "$anki_gid" "$max_guests" "$storage_bytes" >"$config_file"
chmod 0600 "$config_file"
mv -f -- "$config_file" /etc/anki-review-provisioner.conf
install -o root -g root -m 0644 "$script_dir/systemd/anki-review-provisioner.service" /etc/systemd/system/anki-review-provisioner.service
systemctl daemon-reload
systemctl enable anki-review-provisioner.service
systemctl restart anki-review-provisioner.service
echo 'Provisioner installed. The owner HTTP service was not restarted.'
echo 'Set ACCOUNT_SOCKET=/run/anki-review-provisioner/control.sock during separately approved HTTP maintenance.'
echo 'The HTTP service needs its new anki-review supplementary group after that maintenance restart.'
