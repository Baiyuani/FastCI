#!/bin/sh
# Installs the latest fastci release. VERSION=v1.2.3 picks a specific one;
# INSTALL_DIR overrides where the binary lands.
set -eu

repo="Baiyuani/FastCI"

case "$(uname -s)" in
Linux) os=linux ;;
Darwin) os=darwin ;;
*) echo "fastci: unsupported OS $(uname -s); build from source with 'go build ./cmd/fastci'" >&2; exit 1 ;;
esac

case "$(uname -m)" in
x86_64 | amd64) arch=amd64 ;;
aarch64 | arm64) arch=arm64 ;;
*) echo "fastci: unsupported architecture $(uname -m)" >&2; exit 1 ;;
esac

version="${VERSION:-}"
if [ -z "$version" ]; then
  version=$(curl -fsSL "https://api.github.com/repos/${repo}/releases/latest" |
    sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n 1)
  if [ -z "$version" ]; then
    echo "fastci: could not work out the latest version; set VERSION=vX.Y.Z" >&2
    exit 1
  fi
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
asset="fastci_${version}_${os}_${arch}.tar.gz"
url="https://github.com/${repo}/releases/download/${version}/${asset}"

echo "downloading ${asset}"
curl -fsSL -o "${tmp}/${asset}" "$url"
tar -xzf "${tmp}/${asset}" -C "$tmp"

dir="${INSTALL_DIR:-/usr/local/bin}"
if [ ! -w "$dir" ]; then
  dir="${HOME}/.local/bin"
  mkdir -p "$dir"
fi

install -m 0755 "${tmp}/fastci_${version}_${os}_${arch}/fastci" "${dir}/fastci"
echo "installed ${dir}/fastci"
case ":${PATH}:" in
*":${dir}:"*) ;;
*) echo "note: ${dir} is not in your PATH" ;;
esac
