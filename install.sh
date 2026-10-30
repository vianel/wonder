#!/bin/sh
# Installs the latest wonder release:
#   curl -fsSL https://raw.githubusercontent.com/vianel/wonder/main/install.sh | sh
# Set WONDER_INSTALL_DIR to install somewhere other than ~/.local/bin.
set -eu

base=${WONDER_BASE_URL:-https://github.com/vianel/wonder/releases/latest/download}
dir=${WONDER_INSTALL_DIR:-$HOME/.local/bin}

case $(uname -s) in
  Darwin) os=darwin ;;
  Linux) os=linux ;;
  *) echo "wonder: unsupported OS $(uname -s)" >&2; exit 1 ;;
esac
case $(uname -m) in
  x86_64 | amd64) arch=amd64 ;;
  arm64 | aarch64) arch=arm64 ;;
  *) echo "wonder: unsupported CPU $(uname -m)" >&2; exit 1 ;;
esac

file=wonder_${os}_${arch}.tar.gz
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

echo "🎩 downloading $file"
curl -fsSL "$base/$file" -o "$tmp/$file"
curl -fsSL "$base/checksums.txt" -o "$tmp/checksums.txt"

want=$(grep " $file\$" "$tmp/checksums.txt" | cut -d' ' -f1)
if command -v sha256sum >/dev/null 2>&1; then
  got=$(sha256sum "$tmp/$file" | cut -d' ' -f1)
else
  got=$(shasum -a 256 "$tmp/$file" | cut -d' ' -f1)
fi
if [ -z "$want" ] || [ "$want" != "$got" ]; then
  echo "wonder: checksum mismatch for $file" >&2
  exit 1
fi

tar -xzf "$tmp/$file" -C "$tmp" wonder
mkdir -p "$dir"
mv "$tmp/wonder" "$dir/wonder"
chmod +x "$dir/wonder"
echo "✓ installed $("$dir/wonder" --version) to $dir/wonder"

case ":$PATH:" in
  *":$dir:"*) ;;
  *) echo "! $dir is not on your PATH; add it to your shell profile:"
     echo "  echo 'export PATH=\"$dir:\$PATH\"' >> ~/.zshrc" ;;
esac
