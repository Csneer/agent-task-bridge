#!/usr/bin/env bash
# Build public Linux artifacts. Go dependency metadata is an inventory, not an SBOM.
set -euo pipefail
cd "$(dirname "$0")/.."
version=${1:-v0.1.0}
output=${2:-dist/release}
[[ "$version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]] || { echo 'Expected a v-prefixed semantic version' >&2; exit 1; }
mkdir -p "$output"
output=$(cd "$output" && pwd)
if [[ -n $(find "$output" -mindepth 1 -maxdepth 1 -print -quit) ]]; then
  echo 'Use an empty release output directory; artifacts are never silently replaced.' >&2
  exit 1
fi
bridge_go=${BRIDGE_GO:-go}
commit=$(git rev-parse HEAD)
build_date=${BUILD_DATE:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}
stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT
mkdir -p "$stage/common"
while IFS= read -r -d '' file; do
  mkdir -p "$stage/common/$(dirname "$file")"
  cp "$file" "$stage/common/$file"
done < <(git ls-files --cached --others --exclude-standard -z -- LICENSE README.md SECURITY.md CONTRIBUTING.md CHANGELOG.md docs protocol examples/minimal)
test -f "$stage/common/LICENSE"
python3 tools/release-notices.py --go "$bridge_go" \
  --inventory "$output/go-dependencies.json" --notices "$stage/common/THIRD_PARTY_LICENSES"
printf 'version=%s\ncommit=%s\nbuild_date=%s\nCGO_ENABLED=0\nGo dependency inventory: go-dependencies.json (not an SBOM).\n' "$version" "$commit" "$build_date" > "$stage/common/BUILD-INFO.txt"
for arch in amd64 arm64; do
  name="agent-task-bridge_${version}_linux_${arch}"
  mkdir -p "$stage/$name/bin"
  cp -R "$stage/common/." "$stage/$name/"
  cp "$output/go-dependencies.json" "$stage/$name/"
  for target in bridge bridge-client; do
    source=./cmd/bridge
    [[ "$target" != bridge-client ]] || source=./clients/go/cmd/bridge-client
    CGO_ENABLED=0 GOOS=linux GOARCH="$arch" "$bridge_go" build -trimpath \
      -ldflags "-s -w -X main.version=$version -X main.commit=$commit -X main.buildDate=$build_date" \
      -o "$stage/$name/bin/$target" "$source"
  done
  tar -czf "$output/$name.tar.gz" -C "$stage" "$name"
done
tar -czf "$output/agent-task-bridge_${version}_protocol-m1.tar.gz" -C "$stage/common" protocol docs LICENSE README.md SECURITY.md CONTRIBUTING.md CHANGELOG.md BUILD-INFO.txt
(
  cd clients/typescript
  expected=${version#v}
  node --input-type=module -e 'import fs from "node:fs"; const p=JSON.parse(fs.readFileSync("package.json")); if(p.name!=="@csneer/agent-task-bridge" || p.version!==process.argv[1]) throw Error("SDK name/version does not match release");' "$expected"
  npm run build
  npm pack --pack-destination "$output"
)
(
  cd "$output"
  sha256sum ./*.tar.gz ./*.tgz go-dependencies.json > SHA256SUMS
)
printf 'Built Linux amd64/arm64 artifacts in %s (arm64 execution not tested).\n' "$output"
