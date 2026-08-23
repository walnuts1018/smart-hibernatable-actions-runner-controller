#!/usr/bin/env bash
set -euo pipefail
shopt -s nullglob

DIGEST_DIR="${DIGEST_DIR:?DIGEST_DIR is required}"
MERGED_DIGESTS="${MERGED_DIGESTS:?MERGED_DIGESTS is required}"
REGISTRY="${REGISTRY:?REGISTRY is required}"
REPOSITORY="${REPOSITORY:?REPOSITORY is required}"
RELEASE_TAG="${RELEASE_TAG:?RELEASE_TAG is required}"

digest_files=("$DIGEST_DIR"/digests-*.json)

if ((${#digest_files[@]} == 0)); then
  echo "::error::No digest artifacts found in ${DIGEST_DIR}" >&2
  exit 1
fi

jq -s -e '
  def valid_digest:
    type == "string"
    and (try test("^sha256:[0-9a-f]{64}$") catch false);

  if length == 0 then
    error("no digest artifacts")
  else
    .
  end

  | . as $artifacts

  | if any(
      $artifacts[];
      (.platform | type) != "string"
      or (.images | type) != "object"
      or (.images | length) == 0
    ) then
      error("invalid digest artifact schema")
    else
      .
    end

  | if (
      ($artifacts | map(.platform) | unique | length)
      !=
      ($artifacts | length)
    ) then
      error("duplicate platform digest artifacts")
    else
      .
    end

  | ($artifacts[0].images | keys) as $targets

  | if any(
      $artifacts[];
      (.images | keys) != $targets
    ) then
      error("image target sets differ between architectures")
    else
      .
    end

  | if any(
      $artifacts[];
      any(.images[]; valid_digest | not)
    ) then
      error("invalid image digest")
    else
      .
    end

  | reduce $artifacts[] as $artifact (
      {};
      reduce ($artifact.images | to_entries[]) as $image (
        .;
        .[$image.key] =
          ((.[$image.key] // []) + [$image.value])
      )
    )
' "${digest_files[@]}" >"$MERGED_DIGESTS"

mapfile -t targets < <(
  jq -r 'keys[]' "$MERGED_DIGESTS"
)

if ((${#targets[@]} == 0)); then
  echo "::error::No image targets found" >&2
  exit 1
fi

for target in "${targets[@]}"; do
  destination="${REGISTRY}/${REPOSITORY}/${target}:${RELEASE_TAG}"

  mapfile -t sources < <(
    jq -r \
      --arg image "${REGISTRY}/${REPOSITORY}/${target}" \
      --arg target "$target" \
      '.[$target][] | "\($image)@\(.)"' \
      "$MERGED_DIGESTS"
  )

  if ((${#sources[@]} == 0)); then
    echo "::error::No image sources found for ${target}" >&2
    exit 1
  fi

  echo "Creating ${destination}"

  docker buildx imagetools create \
    --tag "$destination" \
    "${sources[@]}"
done
