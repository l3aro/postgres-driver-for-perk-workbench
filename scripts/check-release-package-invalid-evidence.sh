#!/usr/bin/env bash
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

hash_file() {
  if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | cut -d' ' -f1; return; fi
  if command -v shasum >/dev/null 2>&1; then shasum -a 256 "$1" | cut -d' ' -f1; return; fi
  local python_bin=python3
  command -v "$python_bin" >/dev/null 2>&1 || python_bin=python
  "$python_bin" -c 'import hashlib,sys; print(hashlib.sha256(open(sys.argv[1],"rb").read()).hexdigest())' "$1"
}

write_evidence() {
  local output=$1 sha=$2
  jq -n --arg ref "$(jq -er '.host.tested_ref' "$ROOT/compatibility-manifest.json")" --arg sha "$sha" '{evidence_schema:"perk/v1/plugin-test-evidence.schema.json",evidence_version:1,protocol_version:1,tested_host_ref:$ref,capabilities:{name:"postgres"},ok:true,failed:0,passed:1,executable_sha256:$sha}' > "$output"
}

expect_failure() {
  local expected=$1; shift
  local stderr="$TMP/stderr.log"
  if "$@" > "$TMP/stdout.log" 2> "$stderr"; then echo "expected failure: $expected" >&2; exit 1; fi
  grep -Fq "$expected" "$stderr" || { echo "expected stderr to contain: $expected" >&2; cat "$stderr" >&2; exit 1; }
}

tested_binary="$TMP/perk-postgres"
printf 'tested binary bytes\n' > "$tested_binary"
evidence="$TMP/plugin-test-evidence.json"
write_evidence "$evidence" "$(hash_file "$tested_binary")"
expect_failure "missing tested binary" "$ROOT/scripts/release-package.sh" linux amd64 "$TMP/missing-dist" "$evidence" "$TMP/missing-binary"
printf 'different binary bytes\n' > "$TMP/different-perk-postgres"
expect_failure "evidence executable_sha256 does not match target binary" "$ROOT/scripts/release-package.sh" linux amd64 "$TMP/mismatch-dist" "$evidence" "$TMP/different-perk-postgres"
