#!/usr/bin/env bash
set -euo pipefail

APHELION_RELEASE_REPO="${APHELION_RELEASE_REPO:-idolum-ai/aphelion}"
APHELION_INSTALL_DIR="${APHELION_INSTALL_DIR:-${HOME}/.local/bin}"

log() {
  printf '%s\n' "$*"
}

die() {
  printf 'ERROR: %s\n' "$*" >&2
  exit 1
}

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "required command not found: $1"
}

normalize_arch() {
  local raw="${1:-}"
  case "${raw}" in
    x86_64|amd64) printf 'amd64\n' ;;
    aarch64|arm64) printf 'arm64\n' ;;
    *) die "unsupported architecture: ${raw}" ;;
  esac
}

download() {
  local url="$1"
  local dest="$2"
  if command -v curl >/dev/null 2>&1; then
    if curl --fail --location --silent --show-error --retry 3 --proto '=https' --tlsv1.2 "${url}" --output "${dest}"; then
      return
    fi
    die "download failed: ${url}"
  fi
  if command -v wget >/dev/null 2>&1; then
    if wget --https-only --tries=3 --quiet --output-document="${dest}" "${url}"; then
      return
    fi
    die "download failed: ${url}"
  fi
  die "curl or wget is required"
}

latest_version() {
  local tmp="$1"
  download "https://api.github.com/repos/${APHELION_RELEASE_REPO}/releases/latest" "${tmp}"
  sed -nE 's/.*"tag_name"[[:space:]]*:[[:space:]]*"([^"]+)".*/\1/p' "${tmp}" | head -n1
}

verify_checksum() {
  local checksums_path="$1"
  local asset="$2"
  local asset_path="$3"
  need_cmd sha256sum
  local expected
  expected="$(grep -E "[[:space:]]${asset}$" "${checksums_path}" | head -n1 || true)"
  [[ -n "${expected}" ]] || die "checksum for ${asset} not found"
  (
    cd "$(dirname "${asset_path}")"
    printf '%s\n' "${expected}" | sha256sum -c -
  )
}

install_release() {
  local version="${1:-}"
  local tmp_dir
  tmp_dir="$(mktemp -d)"
  trap '[[ -n "${tmp_dir:-}" ]] && rm -rf "${tmp_dir}"' EXIT

  need_cmd uname
  need_cmd tar
  need_cmd install
  need_cmd sed
  need_cmd grep

  local arch
  arch="$(normalize_arch "$(uname -m)")"
  if [[ -z "${version}" ]]; then
    version="$(latest_version "${tmp_dir}/latest.json")"
  fi
  [[ -n "${version}" ]] || die "could not determine release version"

  local asset="aphelion-linux-${arch}.tar.gz"
  local base_url="https://github.com/${APHELION_RELEASE_REPO}/releases/download/${version}"
  local asset_path="${tmp_dir}/${asset}"
  local checksums_path="${tmp_dir}/checksums.txt"

  download "${base_url}/${asset}" "${asset_path}"
  download "${base_url}/checksums.txt" "${checksums_path}"
  verify_checksum "${checksums_path}" "${asset}" "${asset_path}"

  tar -xzf "${asset_path}" -C "${tmp_dir}"
  mkdir -p "${APHELION_INSTALL_DIR}"
  install -m 0755 "${tmp_dir}/aphelion" "${APHELION_INSTALL_DIR}/aphelion"

  log "Installed ${version} to ${APHELION_INSTALL_DIR}/aphelion"
  log "Next:"
  log "  ${APHELION_INSTALL_DIR}/aphelion quickstart --detect-admin --install-service"
}

if [[ "${APHELION_INSTALL_RELEASE_NO_RUN:-}" != "1" ]]; then
  install_release "${1:-}"
fi
