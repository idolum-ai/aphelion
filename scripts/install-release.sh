#!/usr/bin/env bash
set -euo pipefail

repo="idolum-ai/aphelion"
version="${1:-}"
bin_dir="${HOME}/.local/bin"
tmp_dir="$(mktemp -d)"
trap 'rm -rf "${tmp_dir}"' EXIT

arch="$(uname -m)"
case "${arch}" in
  x86_64|amd64) arch="amd64" ;;
  aarch64|arm64) arch="arm64" ;;
  *)
    echo "Unsupported architecture: ${arch}" >&2
    exit 1
    ;;
esac

if [[ -z "${version}" ]]; then
  version="$(curl -fsSL "https://api.github.com/repos/${repo}/releases/latest" | grep '"tag_name"' | head -n1 | sed -E 's/.*"([^"]+)".*/\1/')"
fi

if [[ -z "${version}" ]]; then
  echo "Could not determine release version" >&2
  exit 1
fi

asset="aphelion-linux-${arch}.tar.gz"
url="https://github.com/${repo}/releases/download/${version}/${asset}"

mkdir -p "${bin_dir}"
curl -fsSL "${url}" -o "${tmp_dir}/${asset}"
tar -xzf "${tmp_dir}/${asset}" -C "${tmp_dir}"
install -m 0755 "${tmp_dir}/aphelion" "${bin_dir}/aphelion"

echo "Installed ${version} to ${bin_dir}/aphelion"
echo "If you use the user service, reinstall it with:"
echo "  APHELION_EXEC=${bin_dir}/aphelion APHELION_WORKDIR=${HOME} ./scripts/install-user-service.sh"
