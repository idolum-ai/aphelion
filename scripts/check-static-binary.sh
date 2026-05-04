#!/usr/bin/env bash
set -euo pipefail

bin="${1:-}"
if [[ -z "${bin}" ]]; then
  echo "usage: $0 /path/to/binary" >&2
  exit 2
fi
if [[ ! -x "${bin}" ]]; then
  echo "static binary check failed: ${bin} is not executable" >&2
  exit 1
fi

file_output="$(file "${bin}")"
if [[ "${file_output}" != *"statically linked"* ]]; then
  echo "static binary check failed: ${bin} is not statically linked" >&2
  echo "${file_output}" >&2
  exit 1
fi

ldd_output="$(ldd "${bin}" 2>&1 || true)"
if [[ "${ldd_output}" != *"not a dynamic executable"* ]]; then
  echo "static binary check failed: ${bin} appears to have dynamic dependencies" >&2
  echo "${ldd_output}" >&2
  exit 1
fi

echo "static binary check passed: ${bin}"
