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

# profile_adds_to_path reports whether a shell profile contains an active
# PATH assignment covering the install directory — comments, unrelated
# variable assignments, and other textual mentions do not count.
profile_adds_to_path() {
  local profile="$1" assignments suffix
  [[ -f "${profile}" ]] || return 1
  assignments="$(grep -E '^[[:space:]]*(export[[:space:]]+)?PATH=' "${profile}" || true)"
  [[ -n "${assignments}" ]] || return 1
  if grep -qF "${install_dir}" <<<"${assignments}"; then
    return 0
  fi
  if [[ "${install_dir}" == "${HOME}/"* ]]; then
    suffix="${install_dir#"${HOME}/"}"
    grep -qF "\$HOME/${suffix}" <<<"${assignments}" && return 0
    grep -qF "\${HOME}/${suffix}" <<<"${assignments}" && return 0
  fi
  return 1
}

printf 'Installed spolia %s to %s/spolia.\n' "${version}" "${install_dir}"
case ":${PATH}:" in
  *":${install_dir}:"*) ;;
  *)
    profile_hit=""
    for profile in "${HOME}/.profile" "${HOME}/.bashrc" "${HOME}/.zshrc"; do
      if profile_adds_to_path "${profile}"; then
        profile_hit="${profile}"
        break
      fi
    done
    if [[ -n "${profile_hit}" ]]; then
      printf '%s already adds %s to PATH; open a new terminal, then run: spolia setup\n' "${profile_hit}" "${install_dir}"
    else
      printf 'PATH does not include %s yet. To fix it now and permanently:\n' "${install_dir}"
      printf '  export PATH="%s:$PATH"\n' "${install_dir}"
      printf "  echo 'export PATH=\"%s:\$PATH\"' >> ~/.profile\n" "${install_dir}"
      printf 'Then run: spolia setup\n'
    fi
    ;;
esac
