#!/usr/bin/env bash
set -euo pipefail

GOOS=${1:?GOOS is required}
GOARCH=${2:?GOARCH is required}
DIST=${3:?output directory is required}
EVIDENCE=${4:?target conformance evidence is required}
TESTED_BINARY=${5:?tested binary is required}
normalize_path() {
  case "$1" in
    [A-Za-z]:[\\/]*)
      command -v cygpath >/dev/null 2>&1 || { echo "cygpath is required for Windows path: $1" >&2; exit 1; }
      cygpath -u "$1"
      ;;
    *) printf '%s\n' "$1" ;;
  esac
}
if [[ "${RUNNER_OS:-}" == "Windows" ]]; then
  DIST=$(normalize_path "$DIST")
  EVIDENCE=$(normalize_path "$EVIDENCE")
  TESTED_BINARY=$(normalize_path "$TESTED_BINARY")
fi
printf 'release paths: dist=%s evidence=%s tested_binary=%s\n' "$DIST" "$EVIDENCE" "$TESTED_BINARY" >&2
ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
MANIFEST="$ROOT/compatibility-manifest.json"
sha256_file() {
  if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | cut -d' ' -f1 | sed 's/^\\//'; return; fi
  if command -v shasum >/dev/null 2>&1; then shasum -a 256 "$1" | cut -d' ' -f1 | sed 's/^\\//'; return; fi
  local python_bin=python3
  command -v "$python_bin" >/dev/null 2>&1 || python_bin=python
  "$python_bin" -c 'import hashlib,sys; print(hashlib.sha256(open(sys.argv[1],"rb").read()).hexdigest())' "$1"
}
PLUGIN_NAME=$(jq -er '.plugin.name' "$MANIFEST")
PLUGIN_IDENTITY=${PLUGIN_NAME#perk-}
[[ "$PLUGIN_NAME" =~ ^perk-(sqlite|mysql|postgres|mongodb)$ ]] || { echo "invalid plugin name: $PLUGIN_NAME" >&2; exit 1; }
SDK_VERSION=$(jq -er 'if .sdk.version then .sdk.version else (.protocol.sdk | split("@")[1]) end' "$MANIFEST")
[[ "$SDK_VERSION" == v0.1.0 ]] || { echo "unsupported SDK version: $SDK_VERSION" >&2; exit 1; }
PROTOCOL=$(jq -er '.protocol.version' "$MANIFEST")
[[ "$PROTOCOL" == 1 ]] || { echo "unsupported protocol: $PROTOCOL" >&2; exit 1; }
HOST_REF=$(jq -er '.host.tested_ref' "$MANIFEST")
[[ "$HOST_REF" =~ ^[0-9a-f]{40}$ ]] || { echo "invalid host tested_ref" >&2; exit 1; }
case "$GOOS/$GOARCH" in darwin/amd64|darwin/arm64|linux/amd64|linux/arm64|windows/amd64) ;; *) echo "unsupported target: $GOOS/$GOARCH" >&2; exit 1 ;; esac
if [[ ! -s "$EVIDENCE" ]]; then
  echo "missing target conformance evidence: $EVIDENCE" >&2
  exit 1
fi
if [[ ! -f "$TESTED_BINARY" || ! -s "$TESTED_BINARY" ]]; then
  echo "missing tested binary: $TESTED_BINARY" >&2
  exit 1
fi
EVIDENCE_SHA=
if [[ "${RUNNER_OS:-}" == "Windows" ]]; then
  EVIDENCE_SHA=$(jq -er '.executable_sha256' "$EVIDENCE" 2>/dev/null || true)
  if [[ "$EVIDENCE_SHA" =~ ^\\[0-9a-f]{64}$ ]]; then
    EVIDENCE_SHA=${EVIDENCE_SHA:1}
    if ! jq --arg sha "$EVIDENCE_SHA" '.executable_sha256 = $sha' "$EVIDENCE" > "$EVIDENCE.tmp" || ! mv "$EVIDENCE.tmp" "$EVIDENCE"; then
      echo "failed to normalize escaped executable_sha256 in evidence: $EVIDENCE" >&2
      jq . "$EVIDENCE" >&2 || true
      exit 1
    fi
  fi
fi
if ! jq -e --arg identity "$PLUGIN_IDENTITY" --arg ref "$HOST_REF" --argjson protocol "$PROTOCOL" '(.evidence_schema == "perk/v1/plugin-test-evidence.schema.json") and (.evidence_version == 1) and (.protocol_version == $protocol) and (.tested_host_ref == $ref) and (.capabilities.name == $identity) and (.ok == true) and (.failed == 0) and (.passed > 0) and (.executable_sha256 | test("^[0-9a-f]{64}$"))' "$EVIDENCE" >/dev/null; then
  echo "invalid target conformance evidence: $EVIDENCE" >&2
  jq . "$EVIDENCE" >&2 || true
  exit 1
fi
VERSION=$(jq -r '.plugin.version // "0.1.0"' "$MANIFEST")
EPOCH=${SOURCE_DATE_EPOCH:-$(git -C "$ROOT" log -1 --format=%ct)}
[[ "$EPOCH" =~ ^[0-9]+$ ]] || { echo "invalid SOURCE_DATE_EPOCH" >&2; exit 1; }
TARGET="$GOOS-$GOARCH"
WORK="$DIST/work/$TARGET"
rm -rf "$WORK"
mkdir -p "$WORK/package" "$DIST"
EXECUTABLE="$PLUGIN_NAME"
[[ "$GOOS" == windows ]] && EXECUTABLE+=".exe"
BINARY="$WORK/package/$EXECUTABLE"
EXECUTABLE_SHA=$(sha256_file "$TESTED_BINARY")
EXPECTED_SHA=$(jq -er '.executable_sha256' "$EVIDENCE")
[[ "$EXPECTED_SHA" == "$EXECUTABLE_SHA" ]] || { echo "evidence executable_sha256 does not match target binary" >&2; exit 1; }
cp "$TESTED_BINARY" "$BINARY"
cp "$MANIFEST" "$WORK/package/compatibility-manifest.json"
cp "$EVIDENCE" "$WORK/package/plugin-test-evidence.json"
ASSET="$PLUGIN_NAME-$GOOS-$GOARCH.tar.gz"
PYTHON_BIN=python3
command -v "$PYTHON_BIN" >/dev/null 2>&1 || PYTHON_BIN=python
"$PYTHON_BIN" - "$WORK/package" "$DIST/$ASSET" "$EPOCH" "$EXECUTABLE" <<'PY'
import gzip
import os
import sys
import tarfile
root, output, epoch, executable = sys.argv[1:]
names = [executable, "compatibility-manifest.json", "plugin-test-evidence.json"]
with open(output, "wb") as raw:
    with gzip.GzipFile(fileobj=raw, mode="wb", mtime=0) as compressed:
        with tarfile.open(fileobj=compressed, mode="w", format=tarfile.USTAR_FORMAT) as archive:
            for name in names:
                path = os.path.join(root, name)
                info = archive.gettarinfo(path, arcname=name)
                info.uid = 0; info.gid = 0; info.uname = "root"; info.gname = "root"
                info.mtime = int(epoch); info.mode = 0o755 if name == executable else 0o644; info.pax_headers = {}
                with open(path, "rb") as source: archive.addfile(info, source)
PY
ARCHIVE_SHA=$(sha256_file "$DIST/$ASSET")
printf '%s  %s\n' "$ARCHIVE_SHA" "$ASSET" > "$DIST/$ASSET.sha256"
jq -n --arg plugin "$PLUGIN_NAME" --arg version "$VERSION" --arg target "$TARGET" --arg goos "$GOOS" --arg goarch "$GOARCH" --arg asset "$ASSET" --arg archive_sha256 "$ARCHIVE_SHA" --arg executable "$EXECUTABLE" --arg executable_sha256 "$EXECUTABLE_SHA" --arg host_ref "$HOST_REF" --argjson protocol "$PROTOCOL" '{schema_version:1,plugin:{name:$plugin,version:$version},target:{goos:$goos,goarch:$goarch},protocol:{version:$protocol},host:{tested_ref:$host_ref},asset:{name:$asset,sha256:$archive_sha256,executable:$executable,executable_sha256:$executable_sha256},conformance:{status:"verified",evidence:"plugin-test-evidence.json"}}' > "$DIST/$PLUGIN_NAME-$GOOS-$GOARCH-release-manifest.json"
printf '%s %s %s %s\n' "$PLUGIN_NAME" "$TARGET" "$ASSET" "$ARCHIVE_SHA"
