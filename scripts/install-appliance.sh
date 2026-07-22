#!/bin/sh
# OpenProphet Appliance Installer
# Installs only the launcher binary to the target installation directory.

set -eu

detect_os() {
    OS_UNAME=${1:-$(uname -s)}
    case "$OS_UNAME" in
        Linux*)   echo "linux" ;;
        Darwin*)  echo "darwin" ;;
        *)        echo "unknown" ;;
    esac
}

detect_arch() {
    ARCH_UNAME=${1:-$(uname -m)}
    case "$ARCH_UNAME" in
        x86_64|amd64)  echo "amd64" ;;
        arm64|aarch64) echo "arm64" ;;
        *)             echo "unknown" ;;
    esac
}

download_url() {
    url="$1"
    dest="$2"
    if command -v curl >/dev/null 2>&1; then
        curl -sSL -f "$url" -o "$dest"
    elif command -v wget >/dev/null 2>&1; then
        wget -q -O "$dest" "$url"
    else
        echo "Error: curl or wget is required to download assets." >&2
        return 1
    fi
}

fetch_asset() {
    local_root="$1"
    asset_name="$2"
    dest_path="$3"

    case "$local_root" in
        http://*|https://*)
            download_url "${local_root}/${asset_name}" "$dest_path"
            ;;
        file://*)
            path="${local_root#file://}"
            cp "$path/$asset_name" "$dest_path"
            ;;
        /*|./*|../*)
            cp "$local_root/$asset_name" "$dest_path"
            ;;
        *)
            download_url "${local_root}/${asset_name}" "$dest_path"
            ;;
    esac
}

verify_sha256() {
    file_path="$1"
    checksums_file="$2"
    asset_name=$(basename "$file_path")

    expected_checksum=$(awk -v name="$asset_name" '$2 == name { print $1; exit }' "$checksums_file")
    if ! printf '%s' "$expected_checksum" | grep -Eq '^[a-fA-F0-9]{64}$'; then
        echo "Error: Checksum for $asset_name not found in checksums file." >&2
        return 1
    fi

    actual_checksum=""
    if command -v sha256sum >/dev/null 2>&1; then
        actual_checksum=$(sha256sum "$file_path" | awk '{print $1}')
    elif command -v shasum >/dev/null 2>&1; then
        actual_checksum=$(shasum -a 256 "$file_path" | awk '{print $1}')
    elif command -v openssl >/dev/null 2>&1; then
        actual_checksum=$(openssl dgst -sha256 "$file_path" | awk '{print $2}')
    else
        echo "Error: No tool (sha256sum, shasum, openssl) found to calculate checksum." >&2
        return 1
    fi

    if [ "$actual_checksum" != "$expected_checksum" ]; then
        echo "Error: Checksum verification failed for $asset_name." >&2
        echo "Expected: $expected_checksum" >&2
        echo "Actual:   $actual_checksum" >&2
        return 1
    fi
    return 0
}

install_appliance() {
    OS=$(detect_os)
    ARCH=$(detect_arch)

    if [ "$OS" = "unknown" ] || [ "$ARCH" = "unknown" ]; then
        echo "Error: Unsupported OS ($OS) or architecture ($ARCH)." >&2
        exit 1
    fi

    # Determine release root URL/path
    RELEASE_ROOT="${OPENPROPHET_RELEASE_ROOT:-https://github.com/JakeNesler/OpenProphet/releases}"
    
    # Determine release paths
    if [ -n "${OPENPROPHET_VERSION:-}" ]; then
        # Check if RELEASE_ROOT is a URL or a file path
        case "$RELEASE_ROOT" in
            http://*|https://*)
                if echo "$RELEASE_ROOT" | grep -q "github.com"; then
                    RELEASE_URL="${RELEASE_ROOT}/download/${OPENPROPHET_VERSION}"
                else
                    RELEASE_URL="${RELEASE_ROOT}/${OPENPROPHET_VERSION}"
                fi
                ;;
            *)
                RELEASE_URL="${RELEASE_ROOT}/${OPENPROPHET_VERSION}"
                ;;
        esac
    else
        case "$RELEASE_ROOT" in
            http://*|https://*)
                if echo "$RELEASE_ROOT" | grep -q "github.com"; then
                    RELEASE_URL="${RELEASE_ROOT}/latest/download"
                else
                    RELEASE_URL="${RELEASE_ROOT}/latest"
                fi
                ;;
            *)
                RELEASE_URL="${RELEASE_ROOT}/latest"
                ;;
        esac
    fi

    # Create temporary directory for downloads
    TEMP_DIR=$(mktemp -d)
    cleanup() {
        rm -rf "$TEMP_DIR"
    }
    trap cleanup EXIT INT TERM

    ASSET_NAME="openprophet-${OS}-${ARCH}.tar.gz"
    ARCHIVE_PATH="$TEMP_DIR/$ASSET_NAME"
    CHECKSUMS_PATH="$TEMP_DIR/checksums.txt"

    echo "Downloading checksums..."
    fetch_asset "$RELEASE_URL" "checksums.txt" "$CHECKSUMS_PATH"

    echo "Downloading $ASSET_NAME..."
    fetch_asset "$RELEASE_URL" "$ASSET_NAME" "$ARCHIVE_PATH"

    echo "Verifying checksum..."
    verify_sha256 "$ARCHIVE_PATH" "$CHECKSUMS_PATH"

    # Extraction & Installation
    INSTALL_DIR="${OPENPROPHET_INSTALL_DIR:-$HOME/.local/bin}"
    echo "Installing launcher to $INSTALL_DIR..."
    mkdir -p "$INSTALL_DIR"
    
    archive_entries=$(tar -tzf "$ARCHIVE_PATH")
    if [ "$archive_entries" != "openprophet" ]; then
        echo "Error: Release archive contains unexpected paths." >&2
        return 1
    fi
    tar -xzf "$ARCHIVE_PATH" -C "$TEMP_DIR"
    install -m 0755 "$TEMP_DIR/openprophet" "$INSTALL_DIR/openprophet"

    echo "Successfully installed openprophet to $INSTALL_DIR/openprophet"
}

if [ "${OPENPROPHET_TEST_LIBRARY:-}" != "1" ]; then
    install_appliance "$@"
fi
