#!/usr/bin/env sh
set -eu

VERSION="${KONTEXT_GO_VERSION:-v0.1.4}"
SKILLS_DIR="${CODEX_SKILLS_DIR:-$HOME/.codex/skills}"
ZIP_URL="https://github.com/kontext-security/kontext-go/releases/download/${VERSION}/kontext-go-integrator.zip"

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

zip_path="$tmpdir/kontext-go-integrator.zip"

echo "Downloading Kontext Go Integrator skill ${VERSION}..."
curl -fsSL "$ZIP_URL" -o "$zip_path"

mkdir -p "$SKILLS_DIR"
unzip -oq "$zip_path" -d "$SKILLS_DIR"

echo "Installed kontext-go-integrator to $SKILLS_DIR/kontext-go-integrator"
echo "In the target Go repo, ask Codex to use the kontext-go-integrator skill."
