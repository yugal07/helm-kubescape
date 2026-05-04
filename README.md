# helm-kubescape

A Helm plugin that scans a Helm chart with [Kubescape](https://kubescape.io/) for security misconfigurations, vulnerabilities, and compliance — applying user-supplied Helm value overrides (`-f` / `--set` / `--set-string` / `--set-file`) and release identity (`--release-name` / `--release-namespace`) before the scan.

The plugin is a Go binary that uses Helm's own SDK for chart resolution (so `oci://`, `https://`, `repo/chart`, and `.tgz` references all work) and then invokes the local `kubescape` CLI to do the actual rendering and scanning. Per-template source mapping in findings is preserved by Kubescape's own renderer (no `helm template | kubescape scan -` style flattening).

> **Status:** experimental — depends on the Helm-values-overrides change in `kubescape/kubescape` ([#1883](https://github.com/kubescape/kubescape/issues/1883) prerequisite). Build kubescape from `master` (or any release that includes the change) before installing this plugin.

## Requirements

- **Helm ≥ 3.18.10.** The plugin manifest uses `platformHooks`, which was introduced in Helm v3.18.10; older Helm 3.x versions reject the field during `plugin.yaml` unmarshal and refuse to install the plugin. Helm 4 moves `platformHooks` under `runtimeConfig`, so a v4-compatible release of this plugin will ship a separate manifest.
- A local Go toolchain (the install hook builds from source) and the [`kubescape` CLI](https://kubescape.io/docs/install-cli/) on `PATH`.

## Install

```bash
helm plugin install https://github.com/kubescape/helm-kubescape
```

The install hook builds the plugin binary using your local Go toolchain. (Prebuilt-binary downloads will be wired up once the plugin is tagged.)

The plugin shells out to a locally installed `kubescape` CLI for the scan itself. Install it from <https://kubescape.io/docs/install-cli/> if you don't already have it. Override the binary path with `KUBESCAPE_BIN=/path/to/kubescape` if needed.

## Usage

```text
helm kubescape scan <chart> [helm flags] [kubescape flags]
helm kubescape version
helm kubescape help
```

### Chart references

The plugin accepts the same kinds of references as `helm install`:

| Reference | Example |
|---|---|
| Local directory | `./mychart` |
| Local packaged chart | `./chart-1.0.0.tgz` |
| `repo/chart` (configured via `helm repo add`) | `bitnami/nginx` |
| OCI registry | `oci://ghcr.io/myorg/mychart` |
| HTTP(S) URL to a packaged chart | `https://example.com/chart-1.0.0.tgz` |

For everything except local directories, the plugin uses Helm's SDK to pull and unpack the chart into a temporary directory before scanning.

### Helm-style flags forwarded to Kubescape

| Helm flag | Forwarded as | Notes |
|---|---|---|
| `-f`, `--values FILE` | `--values FILE` | repeatable; comma-list splits like `helm install` |
| `--set KEY=VAL` | `--set KEY=VAL` | repeatable; commas inside braces preserved |
| `--set-string KEY=VAL` | `--set-string KEY=VAL` | repeatable |
| `--set-file KEY=PATH` | `--set-file KEY=PATH` | repeatable |
| `-n`, `--namespace NS` | `--release-namespace NS` | sets `.Release.Namespace` |
| `--release-name NAME` | `--release-name NAME` | sets `.Release.Name` |
| `--release-namespace NS` | `--release-namespace NS` | sets `.Release.Namespace` |

Any other flag is forwarded verbatim to `kubescape scan`, so you can mix in Kubescape-native options:

```bash
# CI gate: fail if any high-severity finding is reported
helm kubescape scan ./mychart \
    -f values-prod.yaml --set image.tag=v2 \
    --release-name prod -n prod \
    --severity-threshold high

# Save results as JSON
helm kubescape scan ./mychart --set image.pullPolicy=Never \
    --format json --output scan.json

# Scan an OCI chart
helm kubescape scan oci://ghcr.io/myorg/mychart --version 1.2.3
```

## How it works

1. **Flag parsing.** `pflag` parses argv with the same flag bindings as Helm (`StringSliceVar` for `--values`, `StringArrayVar` for `--set` / `--set-string` / `--set-file`), so comma handling and repeatability match `helm install` exactly.
2. **Chart resolution.** Local directories pass through. Remote refs (`oci://`, `https://`, `repo/chart`, `.tgz`) are pulled and unpacked into a temp dir using Helm's SDK (`action.Pull` for remote refs, `chartutil.ExpandFile` for local `.tgz`). The temp dir is cleaned up after the scan.
3. **Forward.** The plugin execs `kubescape scan <local-dir> --values ... --set ... --release-name ... --release-namespace ...` plus any unrecognized flags.
4. **Exit code.** Kubescape's exit code is propagated unchanged, so the plugin works as a CI gate. Invalid value overrides (bad `--set`, missing `-f` file, unreadable `--set-file` path, etc.) surface as a non-zero exit from kubescape rather than a silent fall-back to chart defaults.

### Flag-binding parity with Helm

The plugin does not split or rewrite values; it only renames flags whose names differ between Helm and Kubescape. Comma handling matches Helm exactly because both Helm and Kubescape use the same pflag bindings:

- `--values` is comma-split (`-f a.yaml,b.yaml` → two files), the same as `helm install -f a.yaml,b.yaml`.
- `--set` / `--set-string` / `--set-file` are taken verbatim (`--set tolerations={a,b}` is a single value with a brace), the same as `helm install --set tolerations={a,b}`.

## Development

```bash
# Run the unit tests (no kubescape required)
make test

# Build the binary into bin/
make build

# Install this checkout as a local plugin and try it
make install
helm kubescape help

# Lint
make lint    # currently runs go vet
```

The plugin is implemented in Go (see `cmd/helm-kubescape/`, `internal/flags/`, `internal/chartresolve/`). It runs on Linux, macOS, and Windows.

## License

Apache-2.0 (matches the parent Kubescape project).
