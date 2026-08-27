#!/usr/bin/env bash
set -euo pipefail

repository="Mtrya/spolia"
latest_url="${SPOLIA_LATEST_URL:-https://github.com/${repository}/releases/latest}"
install_dir="${SPOLIA_BIN_DIR:-${HOME}/.local/bin}"

case "$(uname -s)" in
  Linux) os="linux" ;;
  Darwin) os="darwin" ;;
  *) printf 'spolia does not provide an archive for this operating system.\n' >&2; exit 1 ;;
esac

case "$(uname -m)" in
  x86_64|amd64) arch="amd64" ;;
  arm64|aarch64) arch="arm64" ;;
  *) printf 'spolia does not provide an archive for this architecture.\n' >&2; exit 1 ;;
esac

if [[ -n "${SPOLIA_INSTALL_VERSION:-}" ]]; then
  version="${SPOLIA_INSTALL_VERSION#v}"
  tag="v${version}"
else
  resolved_url="$(curl -fsSL -o /dev/null -w '%{url_effective}' "${latest_url}")"
  tag="${resolved_url##*/}"
  if [[ ! "${tag}" =~ ^v[0-9A-Za-z.+-]+$ ]]; then
    printf 'Could not determine the latest spolia release from %s.\n' "${resolved_url}" >&2
    exit 1
  fi
  version="${tag#v}"
fi

download_root="${SPOLIA_RELEASE_DOWNLOAD_URL:-https://github.com/${repository}/releases/download/${tag}}"
asset="spolia_${version}_${os}_${arch}.tar.gz"
archive_root="${asset%.tar.gz}"
temporary="$(mktemp -d)"
trap 'rm -rf "${temporary}"' EXIT

curl -fsSL "${download_root}/SHA256SUMS" -o "${temporary}/SHA256SUMS"
curl -fsSL "${download_root}/${asset}" -o "${temporary}/${asset}"
expected="$(awk -v name="${asset}" '$2 == name { print $1 }' "${temporary}/SHA256SUMS")"
if [[ ! "${expected}" =~ ^[0-9a-fA-F]{64}$ ]]; then
  printf 'No valid checksum was published for %s.\n' "${asset}" >&2
  exit 1
fi
if command -v sha256sum >/dev/null 2>&1; then
  actual="$(sha256sum "${temporary}/${asset}" | awk '{ print $1 }')"
elif command -v shasum >/dev/null 2>&1; then
  actual="$(shasum -a 256 "${temporary}/${asset}" | awk '{ print $1 }')"
else
  printf 'A SHA-256 checksum utility is required.\n' >&2
  exit 1
fi
actual="$(printf '%s' "${actual}" | tr '[:upper:]' '[:lower:]')"
expected="$(printf '%s' "${expected}" | tr '[:upper:]' '[:lower:]')"
if [[ "${actual}" != "${expected}" ]]; then
  printf 'Checksum verification failed for %s.\n' "${asset}" >&2
  exit 1
fi

tar -xzf "${temporary}/${asset}" -C "${temporary}"
mkdir -p "${install_dir}"
install -m 0755 "${temporary}/${archive_root}/spolia" "${install_dir}/spolia"

printf 'Installed spolia %s to %s/spolia.\n' "${version}" "${install_dir}"
case ":${PATH}:" in
  *":${install_dir}:"*) ;;
  *) printf 'Add %s to PATH, then run: spolia setup\n' "${install_dir}" ;;
esac
