#!/usr/bin/env bash
# install-binary.sh: Helm plugin install/update hook.
#
# Builds the helm-kubescape Go binary into $HELM_PLUGIN_DIR/bin/helm-kubescape.
# Falls back to a prebuilt release asset for the user's OS/arch when no Go
# toolchain is available (TODO: wire to a real release URL once tags exist).
#
# We also verify that the `kubescape` CLI is reachable. We do not auto-install
# it — that's expected to happen out-of-band per https://kubescape.io/docs/install-cli/.

set -euo pipefail

PLUGIN_DIR="${HELM_PLUGIN_DIR:-$(cd "$(dirname "$0")" && pwd)}"
BIN_DIR="${PLUGIN_DIR}/bin"
BIN_PATH="${BIN_DIR}/helm-kubescape"
KUBESCAPE_BIN="${KUBESCAPE_BIN:-kubescape}"

mkdir -p "${BIN_DIR}"

build_from_source() {
  echo "helm-kubescape: building binary from source..."
  (cd "${PLUGIN_DIR}" && go build -trimpath -ldflags='-s -w' -o "${BIN_PATH}" ./cmd/helm-kubescape)
  chmod +x "${BIN_PATH}"
}

if command -v go >/dev/null 2>&1; then
  build_from_source
else
  cat >&2 <<EOF
helm-kubescape: 'go' is not in PATH and no prebuilt-binary download is wired up yet.

Install Go (https://go.dev/dl/) and re-run:
    helm plugin update kubescape

Or download a prebuilt release manually and place it at:
    ${BIN_PATH}
EOF
  exit 1
fi

if ! command -v "${KUBESCAPE_BIN}" >/dev/null 2>&1; then
  cat >&2 <<EOF
helm-kubescape: warning - '${KUBESCAPE_BIN}' was not found in PATH.

The plugin requires the kubescape CLI to be installed separately. Install it
following https://kubescape.io/docs/install-cli/ before running:

    helm kubescape scan <chart>

(You can also point KUBESCAPE_BIN at a custom path.)
EOF
  exit 0
fi

# Capability probe: warn if the installed kubescape predates the Helm-values flags.
if ! "${KUBESCAPE_BIN}" scan --help 2>/dev/null | grep -qE -- '--values|--set'; then
  KUBESCAPE_VERSION="$("${KUBESCAPE_BIN}" version 2>/dev/null | head -n1 || true)"
  cat >&2 <<EOF
helm-kubescape: warning - the installed kubescape (${KUBESCAPE_VERSION}) does not appear to support the Helm value-override flags (--values / --set).

Upgrade kubescape to a release that includes the Helm-values-overrides change before using this plugin.
EOF
fi

echo "helm-kubescape installed at ${BIN_PATH}. Try: helm kubescape help"
