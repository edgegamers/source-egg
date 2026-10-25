#!/usr/bin/env bash
set -euo pipefail
command -v jq >/dev/null || { echo 'Bootstrap tests require jq' >&2; exit 1; }
source_test_repo=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
source_test_tmp=$(mktemp -d)
trap 'rm -rf -- "$source_test_tmp"' EXIT
chmod 755 "$source_test_tmp"
mkdir -p "$source_test_tmp/bin"
cat > "$source_test_tmp/node" <<'NODE'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "$INSTALL_LOG"
NODE
chmod 755 "$source_test_tmp/node"
(cd "$source_test_tmp" && sha256sum node | sed 's/  node$/  sourcenode_linux_amd64/' > checksums.txt)
printf '%064d  sourcenode_linux_amd64\n' 0 > "$source_test_tmp/bad-checksums.txt"
cat > "$source_test_tmp/stable.json" <<'JSON'
{"tag_name":"v1.2.3","draft":false,"prerelease":false,"assets":[{"name":"sourcenode_linux_amd64"},{"name":"checksums.txt"}]}
JSON
cat > "$source_test_tmp/beta.json" <<'JSON'
[{"tag_name":"v9.0.0","draft":true,"prerelease":true,"assets":[]},{"tag_name":"v1.3.0-rc.1","draft":false,"prerelease":true,"assets":[{"name":"sourcenode_linux_amd64"},{"name":"checksums.txt"}]}]
JSON
printf '{"tag_name":"v1.2.3","draft":false,"prerelease":false,"assets":[]}\n' > "$source_test_tmp/incomplete.json"
jq '.assets=[{"name":"sourcenode_linux_amd64"},{"name":"sourcenode_linux_amd64.sha256"}]' "$source_test_tmp/stable.json" > "$source_test_tmp/legacy.json"
cat > "$source_test_tmp/bin/curl" <<'CURL'
#!/usr/bin/env bash
set -euo pipefail
out=''; url=''
while (($#)); do case "$1" in -o) out=$2; shift 2;; -H) shift 2;; -*) shift;; *) url=$1; shift;; esac; done
printf '%s\n' "$url" >> "$CURL_LOG"
case "$url" in
  */releases/latest|*/releases/tags/v1.2.3) file=$FIXTURE_DIR/stable.json; [[ ${LEGACY:-0} != 1 ]] || file=$FIXTURE_DIR/legacy.json; [[ ${INCOMPLETE:-0} != 1 ]] || file=$FIXTURE_DIR/incomplete.json;;
  */releases\?per_page=100) file=$FIXTURE_DIR/beta.json;;
  */sourcenode_linux_amd64) file=$FIXTURE_DIR/node;;
  */checksums.txt|*/sourcenode_linux_amd64.sha256) file=$FIXTURE_DIR/checksums.txt; [[ ${BAD_CHECKSUM:-0} != 1 ]] || file=$FIXTURE_DIR/bad-checksums.txt;;
  *) echo "Unexpected URL: $url" >&2; exit 2;;
esac
if [[ -n $out ]]; then cp "$file" "$out"; else cat "$file"; fi
CURL
cat > "$source_test_tmp/bin/sudo" <<'SUDO'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "$SUDO_LOG"
exec "$@"
SUDO
cat > "$source_test_tmp/bin/dpkg-query" <<'DPKG'
#!/usr/bin/env bash
if [[ ${MISSING_PACKAGES:-0} == 1 ]]; then printf 'not-installed'; else printf 'installed'; fi
DPKG
cat > "$source_test_tmp/bin/apt-get" <<'APT'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "$APT_LOG"
APT
chmod 755 "$source_test_tmp/bin/"*
source_test_user=(setsid)
if [[ $EUID == 0 ]]; then source_test_user=(setpriv --reuid=65534 --regid=65534 --clear-groups setsid); fi
run_bootstrap() {
  cat "$source_test_repo/scripts/install-node.sh" | env PATH="$source_test_tmp/bin:$PATH" \
    FIXTURE_DIR="$source_test_tmp" INSTALL_LOG="$source_test_case/install.log" \
    CURL_LOG="$source_test_case/curl.log" SUDO_LOG="$source_test_case/sudo.log" APT_LOG="$source_test_case/apt.log" \
    SOURCE_NODE_CHANNEL="${channel:-stable}" SOURCE_NODE_RELEASE="${tag:-}" \
    BAD_CHECKSUM="${bad:-0}" INCOMPLETE="${incomplete:-0}" MISSING_PACKAGES="${missing:-0}" LEGACY="${legacy:-0}" \
    "${source_test_user[@]}" bash -s -- "$@" > "$source_test_case/output" 2>&1
}
new_case() { source_test_case="$source_test_tmp/$1";mkdir "$source_test_case";chmod 777 "$source_test_case";channel=stable;tag='';bad=0;incomplete=0;missing=0;legacy=0; }
new_case stable
if ! run_bootstrap --yes; then cat "$source_test_case/output";exit 1; fi
grep -Fxq 'install --channel stable --yes' "$source_test_case/install.log"
grep -Fq '/releases/latest' "$source_test_case/curl.log"
grep -Fq '/download/v1.2.3/sourcenode_linux_amd64' "$source_test_case/curl.log"
grep -Fq 'install --channel stable --yes' "$source_test_case/sudo.log"
[[ ! -e $source_test_case/apt.log ]]
new_case beta;channel=beta
run_bootstrap --yes
grep -Fxq 'install --channel beta --yes' "$source_test_case/install.log"
grep -Fq '/download/v1.3.0-rc.1/sourcenode_linux_amd64' "$source_test_case/curl.log"
new_case exact;tag=v1.2.3
run_bootstrap --yes
grep -Fq '/releases/tags/v1.2.3' "$source_test_case/curl.log"
new_case legacy-checksum;legacy=1
run_bootstrap --yes
grep -Fq '/download/v1.2.3/sourcenode_linux_amd64.sha256' "$source_test_case/curl.log"
new_case tampered;bad=1
if run_bootstrap --yes; then echo 'Accepted bad checksum' >&2;exit 1;fi
[[ ! -e $source_test_case/install.log ]]
new_case incomplete;incomplete=1
if run_bootstrap --yes; then echo 'Accepted incomplete release' >&2;exit 1;fi
[[ ! -e $source_test_case/install.log ]]
new_case bad-channel;channel=unexpected
if run_bootstrap --yes; then echo 'Accepted invalid channel' >&2;exit 1;fi
[[ ! -e $source_test_case/curl.log ]]
new_case no-terminal
if run_bootstrap; then echo 'Interactive pipeline accepted no terminal' >&2;exit 1;fi
[[ ! -e $source_test_case/curl.log ]]
new_case missing-libraries;missing=1
run_bootstrap --yes
grep -Fq 'lib32gcc-s1 lib32stdc++6' "$source_test_case/apt.log"
new_case local
run_bootstrap --local "$source_test_tmp/node" --yes
[[ ! -e $source_test_case/curl.log ]]
grep -Fxq 'install --channel stable --yes' "$source_test_case/install.log"
echo 'Bootstrap fixtures passed: non-root pipeline, stable/beta/pin, checksums, assets, terminal, dependencies, local binary'
