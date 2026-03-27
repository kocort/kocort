#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"

RELEASE_REPO="${RELEASE_REPO:-kocort/kocort}"
WEBSITE_REPO="${WEBSITE_REPO:-kocort/kocort.github.io}"
WEBSITE_BRANCH="${WEBSITE_BRANCH:-main}"
WEBSITE_PROXY_PREFIX_ZH="${WEBSITE_PROXY_PREFIX_ZH:-https://g.cource.com/}"
WEBSITE_REPO_DIR="${WEBSITE_REPO_DIR:-}"

info() {
  printf '==> %s\n' "$*"
}

fail() {
  printf 'ERROR: %s\n' "$*" >&2
  exit 1
}

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || fail "Missing required command: $1"
}

resolve_tag() {
  local provided="${1:-}"
  if [[ -n "$provided" ]]; then
    printf '%s\n' "$provided"
    return
  fi

  gh api "repos/${RELEASE_REPO}/releases" \
    --jq 'map(select(.draft == false)) | first | .tag_name'
}

clone_or_resolve_site_repo() {
  if [[ -n "$WEBSITE_REPO_DIR" ]]; then
    printf '%s\n' "$WEBSITE_REPO_DIR"
    return
  fi

  local tmp_dir
  tmp_dir="$(mktemp -d)"
  gh repo clone "$WEBSITE_REPO" "$tmp_dir/site" -- --branch "$WEBSITE_BRANCH" --depth 1
  printf '%s\n' "$tmp_dir/site"
}

main() {
  require_cmd gh
  require_cmd node

  local tag
  tag="$(resolve_tag "${1:-}")"
  [[ -n "$tag" ]] || fail "Could not resolve release tag."

  info "Syncing website downloads for tag $tag"

  local release_json
  release_json="$(gh api "repos/${RELEASE_REPO}/releases/tags/${tag}")"

  local site_dir
  site_dir="$(clone_or_resolve_site_repo)"
  [[ -d "$site_dir/.git" ]] || fail "Website repo not found at $site_dir"

  local output_file="$site_dir/downloads.js"

  RELEASE_JSON="$release_json" \
  RELEASE_TAG="$tag" \
  WEBSITE_PROXY_PREFIX_ZH="$WEBSITE_PROXY_PREFIX_ZH" \
  OUTPUT_FILE="$output_file" \
  node <<'EOF'
const fs = require('fs');

const release = JSON.parse(process.env.RELEASE_JSON || '{}');
const tag = process.env.RELEASE_TAG || '';
const zhPrefix = process.env.WEBSITE_PROXY_PREFIX_ZH || '';
const outputFile = process.env.OUTPUT_FILE;

if (!release.assets || !Array.isArray(release.assets)) {
  throw new Error('Release payload does not include assets.');
}

function pickAsset(pattern) {
  const asset = release.assets.find((item) => pattern.test(item.name));
  if (!asset) {
    throw new Error(`Missing release asset for pattern: ${pattern}`);
  }
  return asset.name;
}

const platforms = [
  {
    id: 'windows-amd64',
    os: 'windows',
    arch: 'amd64',
    label: 'Windows AMD64',
    shortLabel: 'Win AMD64',
    assetName: pickAsset(/^kocort-desktop-windows-amd64.*\.zip$/)
  },
  {
    id: 'windows-arm64',
    os: 'windows',
    arch: 'arm64',
    label: 'Windows ARM64',
    shortLabel: 'Win ARM64',
    assetName: pickAsset(/^kocort-desktop-windows-arm64.*\.zip$/)
  },
  {
    id: 'macos-amd64',
    os: 'macos',
    arch: 'amd64',
    label: 'macOS AMD64',
    shortLabel: 'mac AMD64',
    assetName: pickAsset(/^kocort-desktop-macos-amd64\.dmg$/)
  },
  {
    id: 'macos-arm64',
    os: 'macos',
    arch: 'arm64',
    label: 'macOS ARM64',
    shortLabel: 'mac ARM64',
    assetName: pickAsset(/^kocort-desktop-macos-arm64\.dmg$/)
  },
  {
    id: 'linux-amd64',
    os: 'linux',
    arch: 'amd64',
    label: 'Linux AMD64',
    shortLabel: 'Linux AMD64',
    assetName: pickAsset(/^kocort-desktop-linux-amd64.*\.tar\.gz$/)
  },
  {
    id: 'linux-arm64',
    os: 'linux',
    arch: 'arm64',
    label: 'Linux ARM64',
    shortLabel: 'Linux ARM64',
    assetName: pickAsset(/^kocort-desktop-linux-arm64.*\.tar\.gz$/)
  }
];

const plainBase = `https://github.com/kocort/kocort/releases/download/${tag}/`;
const releasePage = `https://github.com/kocort/kocort/releases/tag/${tag}`;
const content = `window.KOCORT_DOWNLOADS = ${JSON.stringify({
  releasesPageByLang: {
    zh: `${zhPrefix}${releasePage}`,
    en: releasePage
  },
  latestDownloadBaseByLang: {
    zh: `${zhPrefix}${plainBase}`,
    en: plainBase
  },
  platforms
}, null, 4)};\n`;

fs.writeFileSync(outputFile, content);
EOF

  info "Updated $output_file"

  git -C "$site_dir" status --short
  if git -C "$site_dir" diff --quiet -- downloads.js; then
    info "downloads.js already up to date."
    return
  fi

  git -C "$site_dir" add downloads.js
  git -C "$site_dir" commit -m "Update downloads for ${tag}"
  git -C "$site_dir" push origin "$WEBSITE_BRANCH"
  info "Pushed website update to ${WEBSITE_REPO}@${WEBSITE_BRANCH}"
}

main "$@"
