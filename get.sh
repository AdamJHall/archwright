#!/usr/bin/env bash
# archwright fetcher — pick a release, download + untar the binary.
# Designed to be typed by hand in the Arch live ISO (no jq, reads /dev/tty):
#
#   curl -fsSL https://raw.githubusercontent.com/AdamJHall/archwright/main/get.sh | bash
#
set -euo pipefail
repo=AdamJHall/archwright

case "$(uname -m)" in
  x86_64)  arch=amd64 ;;
  aarch64) arch=arm64 ;;
  *) echo "unsupported arch: $(uname -m)" >&2; exit 1 ;;
esac

# Newest few releases. No jq in the live ISO, so grep+sed the tags.
echo "Fetching releases…" >&2
mapfile -t tags < <(curl -fsSL "https://api.github.com/repos/$repo/releases?per_page=5" \
  | grep '"tag_name":' | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/')
[ "${#tags[@]}" -gt 0 ] || { echo "no releases found" >&2; exit 1; }

for i in "${!tags[@]}"; do
  printf '  %d) %s%s\n' "$((i+1))" "${tags[i]}" \
    "$([ "$i" -eq 0 ] && echo '  (latest)')" >&2
done

printf 'Select [1]: ' >&2
read -r n < /dev/tty 2>/dev/null || n=1
n=${n:-1}
tag=${tags[$((n-1))]:?invalid selection}
ver=${tag#v}

asset="archwright_${ver}_linux_${arch}.tar.gz"
base="https://github.com/$repo/releases/download/$tag"
echo "Downloading $asset…" >&2
curl -fSL "$base/$asset" -o "$asset"

# Verify against the release checksums if present.
if curl -fsSL "$base/checksums.txt" -o checksums.txt 2>/dev/null; then
  sha256sum --ignore-missing -c checksums.txt >&2 \
    || { echo "checksum FAILED" >&2; exit 1; }
fi

tar -xzf "$asset"
echo "Done → ./archwright ($tag)" >&2
