#!/bin/sh
# Test suite for scripts/install-appliance.sh

set -eu

# Enable library-only test mode
export OPENPROPHET_TEST_LIBRARY=1

# Source the installer script
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
. "$SCRIPT_DIR/install-appliance.sh"

echo "=== Running Installer Tests ==="

# Test 1: OS and architecture normalization
if [ "$(detect_os Linux)" != "linux" ] || [ "$(detect_os Darwin)" != "darwin" ] || [ "$(detect_os FreeBSD)" != "unknown" ]; then
    echo "FAIL: OS normalization"
    exit 1
fi
if [ "$(detect_arch x86_64)" != "amd64" ] || [ "$(detect_arch amd64)" != "amd64" ] || [ "$(detect_arch arm64)" != "arm64" ] || [ "$(detect_arch aarch64)" != "arm64" ] || [ "$(detect_arch riscv64)" != "unknown" ]; then
    echo "FAIL: architecture normalization"
    exit 1
fi

# Confirm the current test host is supported as well.
OS=$(detect_os)
ARCH=$(detect_arch)
echo "Detected OS: $OS, Arch: $ARCH"
if [ "$OS" != "linux" ] && [ "$OS" != "darwin" ]; then
    echo "FAIL: OS should be linux or darwin on supported systems"
    exit 1
fi
if [ "$ARCH" != "amd64" ] && [ "$ARCH" != "arm64" ]; then
    echo "FAIL: Arch should be amd64 or arm64 on supported systems"
    exit 1
fi
echo "✓ OS/Arch detection looks valid"

# Setup temp workspace for testing
TEST_TEMP=$(mktemp -d)
cleanup() {
    rm -rf "$TEST_TEMP"
}
trap cleanup EXIT INT TERM

# Create the version subdirectory under TEST_TEMP
mkdir -p "$TEST_TEMP/vFake"

# Test 2: Checksum rejection
# Create a dummy asset
echo "dummy binary content" > "$TEST_TEMP/openprophet"
tar -czf "$TEST_TEMP/vFake/openprophet-${OS}-${ARCH}.tar.gz" -C "$TEST_TEMP" openprophet

# Generate valid checksums file
if command -v sha256sum >/dev/null 2>&1; then
    VALID_HASH=$(sha256sum "$TEST_TEMP/vFake/openprophet-${OS}-${ARCH}.tar.gz" | awk '{print $1}')
elif command -v shasum >/dev/null 2>&1; then
    VALID_HASH=$(shasum -a 256 "$TEST_TEMP/vFake/openprophet-${OS}-${ARCH}.tar.gz" | awk '{print $1}')
else
    VALID_HASH=$(openssl dgst -sha256 "$TEST_TEMP/vFake/openprophet-${OS}-${ARCH}.tar.gz" | awk '{print $2}')
fi

echo "$VALID_HASH  openprophet-${OS}-${ARCH}.tar.gz" > "$TEST_TEMP/vFake/checksums.txt"

# Test verify_sha256 with correct checksum
if verify_sha256 "$TEST_TEMP/vFake/openprophet-${OS}-${ARCH}.tar.gz" "$TEST_TEMP/vFake/checksums.txt"; then
    echo "✓ Checksum verification passed with valid checksum"
else
    echo "FAIL: Expected checksum verification to pass"
    exit 1
fi

# Modify checksums file to be invalid
echo "badhash  openprophet-${OS}-${ARCH}.tar.gz" > "$TEST_TEMP/vFake/checksums.txt"

# Test verify_sha256 with bad checksum
if verify_sha256 "$TEST_TEMP/vFake/openprophet-${OS}-${ARCH}.tar.gz" "$TEST_TEMP/vFake/checksums.txt"; then
    echo "FAIL: Expected checksum verification to fail with invalid checksum"
    exit 1
else
    echo "✓ Checksum verification failed (as expected) with invalid checksum"
fi

# Test 3: Successful fake local release install
# Re-create correct checksums file
echo "$VALID_HASH  openprophet-${OS}-${ARCH}.tar.gz" > "$TEST_TEMP/vFake/checksums.txt"

# Set release-root override to the local temp directory
export OPENPROPHET_RELEASE_ROOT="$TEST_TEMP"
export OPENPROPHET_VERSION="vFake"
export OPENPROPHET_INSTALL_DIR="$TEST_TEMP/bin"

# Make sure bin directory doesn't have the binary yet
if [ -f "$TEST_TEMP/bin/openprophet" ]; then
    echo "FAIL: Setup error, binary already exists"
    exit 1
fi

# Run the installation
echo "Running fake installation..."
install_appliance

# Verify the binary is installed, executable, and contains the correct content
if [ -x "$TEST_TEMP/bin/openprophet" ]; then
    INSTALLED_CONTENT=$(cat "$TEST_TEMP/bin/openprophet")
    if [ "$INSTALLED_CONTENT" = "dummy binary content" ]; then
        echo "✓ Fake installation verified successfully!"
    else
        echo "FAIL: Installed binary content does not match"
        exit 1
    fi
else
    echo "FAIL: Binary openprophet not installed or not executable"
    exit 1
fi

echo "=== All Installer Tests Passed! ==="
