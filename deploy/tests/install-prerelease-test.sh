#!/bin/bash

set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
TEMP_DIR=$(mktemp -d)
trap 'rm -f "$TEMP_DIR/sub2api" "$TEMP_DIR/api-calls"; rmdir "$TEMP_DIR"' EXIT

# Match the existing installer tests without running main or requiring root.
source <(head -n -1 "$ROOT_DIR/deploy/install.sh")
LANG_CHOICE=en

fail() {
    echo "$*" >&2
    exit 1
}

github_api_curl() {
    local url="${!#}"
    printf '%s\n' "$url" >> "$TEMP_DIR/api-calls"
    case "$url" in
        "https://api.github.com/repos/zhjai/sub2api-plus/releases/tags/$EXPECTED_TAG")
            printf '%s' "${MOCK_HTTP_CODE-200}"
            ;;
        "https://api.github.com/repos/zhjai/sub2api-plus/releases?per_page=100")
            printf '%s\n' \
                '[' \
                '{"tag_name": "v0.2.11-zhjai.15"},' \
                '{"tag_name": "v0.2.11-zhjai.15-rc.1"},' \
                '{"tag_name": "v0.2.11-zhjai.15-beta.2"},' \
                '{"tag_name": "v0.2.11-zhjai.15-alpha.1"},' \
                '{"tag_name": "v0.2.11-zhjai.13"},' \
                '{"tag_name": "v0.2.11-zhjai.12"},' \
                '{"tag_name": "v0.2.10"}' \
                ']'
            ;;
        *)
            echo "UNEXPECTED_GITHUB_API: $url" >&2
            return 1
            ;;
    esac
}

for version in \
    v0.2.11-zhjai.13 \
    0.2.11-zhjai.13 \
    v0.2.11-zhjai.15-rc.1 \
    0.2.11-zhjai.15-rc.1 \
    v0.2.11-zhjai.15-beta.2 \
    v0.2.11-zhjai.15-alpha.1 \
    v0.2.11-zhjai.15-rc \
    v0.2.11-zhjai.15-beta-2; do
    EXPECTED_TAG="v${version#v}"
    normalized=$(validate_version "$version" 2>/dev/null) || fail "Rejected valid version: $version"
    [ "$normalized" = "$EXPECTED_TAG" ] || fail "Incorrect normalized version: $normalized"
done

for version in \
    v0.2.11 \
    v0.2.11-zhjai.15-preview.1 \
    v0.2.11-zhjai.15-rc. \
    v0.2.11-zhjai.15-rc..1 \
    v0.2.11-zhjai.15-rc.1/other \
    v0.2.11-zhjai.15-rc.1+build \
    v0.2.11-zhjai.15-RC.1 \
    v0.2.11-zhjai.15-rc.1-extra; do
    EXPECTED_TAG="invalid-must-not-reach-api"
    previous_calls=$(wc -l < "$TEMP_DIR/api-calls")
    if output=$(validate_version "$version" 2>&1); then
        fail "Accepted invalid version: $version"
    fi
    [ "$(wc -l < "$TEMP_DIR/api-calls")" -eq "$previous_calls" ] || fail "Invalid version reached GitHub: $version"
done

EXPECTED_TAG=v0.2.11-zhjai.15-rc.1
warning=$(validate_version "$EXPECTED_TAG" 2>&1 >/dev/null)
[[ "$warning" == *"Selecting a pre-release"* ]] || fail "Missing pre-release warning"
LANG_CHOICE=zh
warning=$(validate_version "$EXPECTED_TAG" 2>&1 >/dev/null)
[[ "$warning" == *"$(msg 'prerelease_warning')"* ]] || fail "Missing localized pre-release warning"
LANG_CHOICE=en

for MOCK_HTTP_CODE in 404 403 500 ''; do
    if output=$(validate_version "$EXPECTED_TAG" 2>&1); then
        fail "Accepted unavailable release with HTTP status: $MOCK_HTTP_CODE"
    fi
done
unset MOCK_HTTP_CODE

get_latest_version >/dev/null
[ "$LATEST_VERSION" = v0.2.11-zhjai.15 ] || fail "Default selection did not choose the latest stable release: $LATEST_VERSION"

# Exercise real CLI dispatch and install_version while mocking all system writes.
touch "$TEMP_DIR/sub2api"
select_language() { :; }
check_root() { :; }
detect_platform() { :; }
check_dependencies() { :; }
configure_server() { :; }
create_user() { :; }
setup_directories() { :; }
install_service() { :; }
prepare_for_setup() { :; }
get_public_ip() { :; }
start_service() { :; }
enable_autostart() { :; }
print_completion() { :; }
get_current_version() { echo 0.2.11-zhjai.12; }
systemctl() { :; }
cp() { :; }
chown() { :; }
download_and_extract() { echo "download=$LATEST_VERSION"; }

assert_cli_download() {
    local expected="$1"
    shift
    local output
    output=$(main "$@" 2>&1) || fail "CLI failed: $*; $output"
    [[ "$output" == *"download=$expected"* ]] || fail "CLI selected the wrong release: $*; $output"
}

EXPECTED_TAG=v0.2.11-zhjai.15-rc.1
INSTALL_DIR="$TEMP_DIR/fresh"
assert_cli_download "$EXPECTED_TAG" install -v "$EXPECTED_TAG"
assert_cli_download "$EXPECTED_TAG" install --version="${EXPECTED_TAG#v}"
assert_cli_download "$EXPECTED_TAG" -v "$EXPECTED_TAG"
assert_cli_download v0.2.11-zhjai.15 install

INSTALL_DIR="$TEMP_DIR"
assert_cli_download "$EXPECTED_TAG" install -v "$EXPECTED_TAG"
assert_cli_download "$EXPECTED_TAG" upgrade -v "$EXPECTED_TAG"
assert_cli_download "$EXPECTED_TAG" update --version="$EXPECTED_TAG"
assert_cli_download "$EXPECTED_TAG" rollback "$EXPECTED_TAG"

echo "install pre-release checks passed"
