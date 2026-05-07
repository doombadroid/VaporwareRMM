#!/bin/bash
# Build all third-party components from source with security hardening
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
DOCKER_DIR="${PROJECT_ROOT}/docker"

GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m'

info() { echo -e "${YELLOW}[INFO]${NC} $1"; }
pass() { echo -e "${GREEN}[PASS]${NC} $1"; }
fail() { echo -e "${RED}[FAIL]${NC} $1"; }

cd "${PROJECT_ROOT}"

# Initialize submodules if not already done
if [ ! -f "third_party/sunshine/CMakeLists.txt" ]; then
    info "Initializing git submodules..."
    git submodule update --init --recursive --depth 1
fi

info "========================================"
info "  Building Security-Hardened Components"
info "========================================"

# Build Moonlight Web Stream
info "--- Building Moonlight Web Stream ---"
if docker build -f "${DOCKER_DIR}/Dockerfile.moonlight-web" -t vaporrmm/moonlight-web:latest "${PROJECT_ROOT}"; then
    pass "Moonlight Web Stream built successfully"
else
    fail "Moonlight Web Stream build failed"
    exit 1
fi

# Build Sunshine
info "--- Building Sunshine ---"
if docker build -f "${DOCKER_DIR}/Dockerfile.sunshine" -t vaporrmm/sunshine:latest "${PROJECT_ROOT}"; then
    pass "Sunshine built successfully"
else
    fail "Sunshine build failed"
    exit 1
fi

# Build Moonlight Qt (desktop client — produces artifacts)
info "--- Building Moonlight Qt ---"
if docker build -f "${DOCKER_DIR}/Dockerfile.moonlight-qt" -t vaporrmm/moonlight-qt:build "${PROJECT_ROOT}"; then
    pass "Moonlight Qt built successfully"
    # Extract artifacts
    info "Extracting Moonlight Qt artifacts..."
    mkdir -p "${PROJECT_ROOT}/dist"
    docker create --name mlqt-extract vaporrmm/moonlight-qt:build
    docker cp mlqt-extract:/artifacts "${PROJECT_ROOT}/dist/moonlight-qt" 2>/dev/null || true
    docker rm mlqt-extract
else
    fail "Moonlight Qt build failed"
    exit 1
fi

info "========================================"
info "  All components built successfully"
info "========================================"
pass "Images:"
pass "  vaporrmm/moonlight-web:latest"
pass "  vaporrmm/sunshine:latest"
pass "  vaporrmm/moonlight-qt:build"
pass "Artifacts:"
pass "  dist/moonlight-qt/"
