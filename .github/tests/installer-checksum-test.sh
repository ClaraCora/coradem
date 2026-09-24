#!/bin/sh
set -eu

# Exercise the installer's real download/verification functions without touching
# services, credentials or installed binaries. Run under both GNU and BusyBox.
installer=${1:-./install.sh}
test_root=$(mktemp -d)
trap 'rm -rf -- "$test_root"' EXIT HUP INT TERM
TMP_DIR="${test_root}/staging"
fixture_dir="${test_root}/release"
mkdir -p "$TMP_DIR" "$fixture_dir"

awk '
  /^verify_download\(\) \{/ || /^try_download\(\) \{/ { copying = 1 }
  copying { print }
  /^\}/ { copying = 0 }
' "$installer" >"${test_root}/download-functions.sh"
. "${test_root}/download-functions.sh"
command -v verify_download >/dev/null
command -v try_download >/dev/null

for name in corade-linux-amd64 coradectl-linux-amd64; do
  printf 'fixture for %s\n' "$name" >"${fixture_dir}/${name}"
  (cd "$fixture_dir" && sha256sum "$name" >"${name}.sha256")
  try_download "$name" "${test_root}/verified-${name}" "file://${fixture_dir}"
  cmp "${fixture_dir}/${name}" "${test_root}/verified-${name}"

  # A downloaded file must never be promoted when validation fails.
  printf 'corrupt\n' >>"${fixture_dir}/${name}"
  if try_download "$name" "${test_root}/corrupt-${name}" "file://${fixture_dir}"; then
    echo "corrupted $name unexpectedly accepted" >&2
    exit 1
  fi
  test ! -e "${test_root}/corrupt-${name}"

  : >"${fixture_dir}/${name}.sha256"
  if try_download "$name" "${test_root}/empty-${name}" "file://${fixture_dir}"; then
    echo "empty checksum unexpectedly accepted for $name" >&2
    exit 1
  fi
  test ! -e "${test_root}/empty-${name}"

  rm "${fixture_dir}/${name}.sha256"
  if try_download "$name" "${test_root}/missing-${name}" "file://${fixture_dir}"; then
    echo "missing checksum unexpectedly accepted for $name" >&2
    exit 1
  fi
  test ! -e "${test_root}/missing-${name}"
done
echo 'Installer checksum tests passed (Agent and CLI: valid, corrupt, empty, missing).'
