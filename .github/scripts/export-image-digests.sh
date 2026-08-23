#!/usr/bin/env bash
set -euo pipefail

METADATA_FILE="${METADATA_FILE:-}"
PLATFORM="${PLATFORM:?PLATFORM environment variable is required}"
DIGEST_FILE="${DIGEST_FILE:?DIGEST_FILE environment variable is required}"

mkdir -p "$(dirname "$DIGEST_FILE")"

# shellcheck disable=SC2016
filter='
  del(."buildx.build.warnings")
  | to_entries
  | map(
      . as $entry
      | .value["containerimage.digest"] as $digest
      | if (($entry.key | test("^[a-z0-9]+([._-][a-z0-9]+)*$")) | not) then
          error("invalid target name: " + $entry.key)
        elif ($digest | type) != "string" then
          error("missing digest for target: " + $entry.key)
        elif (($digest | test("^sha256:[0-9a-f]{64}$")) | not) then
          error("invalid digest for target: " + $entry.key)
        else
          {
            key: $entry.key,
            value: $digest
          }
        end
    )
  | from_entries
  | if length == 0 then
      error("Bake metadata contains no image digests")
    else
      .
    end
  | {
      platform: $platform,
      images: .
    }
'

if [ -n "$METADATA_FILE" ]; then
  if [ ! -f "$METADATA_FILE" ]; then
    echo "::error::METADATA_FILE not found: $METADATA_FILE" >&2
    exit 1
  fi
  jq -e --arg platform "$PLATFORM" "$filter" "$METADATA_FILE" >"$DIGEST_FILE"
elif [ -n "${METADATA:-}" ]; then
  jq -e --arg platform "$PLATFORM" "$filter" <<<"$METADATA" >"$DIGEST_FILE"
else
  echo "::error::Either METADATA_FILE or METADATA environment variable is required" >&2
  exit 1
fi
