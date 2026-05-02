// helm-kubescape is a Helm plugin that scans a Helm chart with Kubescape using
// the same flags users would pass to `helm install`.
//
// The plugin's contract:
//   1. Parse argv with Helm-style flag bindings (pflag, mirroring helm.sh/helm/v3
//      cmd/helm/flags.go) so -f / --set / --release-name / -n behave identically
//      to `helm install`.
//   2. If the chart reference is remote (oci://, http(s)://, repo/chart, .tgz),
//      resolve it via Helm's SDK to a local directory.
//   3. Exec `kubescape scan <local-dir> --values ... --set ... --release-name ...`,
//      forwarding any unrecognized flags verbatim. Kubescape does the rendering;
//      per-template source mapping in findings is preserved by kubescape's
//      renderer.
//   4. Inherit kubescape's exit code so CI gates (e.g. --severity-threshold) work
//      transparently.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"

	"github.com/kubescape/helm-kubescape/internal/chartresolve"
	"github.com/kubescape/helm-kubescape/internal/flags"
)

const (
	pluginName    = "helm-kubescape"
	pluginVersion = "0.1.0"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(argv []string) int {
	if len(argv) == 0 {
		printHelp()
		return 0
	}

	sub, rest := argv[0], argv[1:]
	switch sub {
	case "-h", "--help", "help":
		printHelp()
		return 0
	case "-v", "--version", "version":
		printVersion()
		return 0
	case "scan":
		return runScan(rest)
	default:
		fmt.Fprintf(os.Stderr, "%s: unknown subcommand %q (try 'helm kubescape help')\n", pluginName, sub)
		return 2
	}
}

func runScan(argv []string) int {
	parsed, err := flags.Parse(argv)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", pluginName, err)
		return 2
	}
	if parsed.Chart == "" {
		fmt.Fprintf(os.Stderr, "%s: scan requires a chart argument (path, .tgz, repo/chart, oci://..., or URL)\n", pluginName)
		return 2
	}

	kubescape := kubescapeBin()
	if _, err := exec.LookPath(kubescape); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %q not found in PATH; install from https://kubescape.io/docs/install-cli/ (or set KUBESCAPE_BIN)\n", pluginName, kubescape)
		return 127
	}

	res, cleanup, err := chartresolve.Resolve(parsed.Chart)
	defer cleanup()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", pluginName, err)
		return 1
	}
	parsed.Chart = res.LocalPath

	cmd := exec.CommandContext(context.Background(), kubescape, append([]string{"scan"}, parsed.KubescapeArgs()...)...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode()
		}
		fmt.Fprintf(os.Stderr, "%s: failed to invoke %s: %v\n", pluginName, kubescape, err)
		return 1
	}
	return 0
}

// kubescapeBin returns the kubescape binary to invoke. KUBESCAPE_BIN overrides
// the default of "kubescape" from PATH.
func kubescapeBin() string {
	if v := os.Getenv("KUBESCAPE_BIN"); v != "" {
		return v
	}
	return "kubescape"
}

func printVersion() {
	fmt.Printf("%s version: %s\n", pluginName, pluginVersion)
	if path, err := exec.LookPath(kubescapeBin()); err == nil {
		// Best-effort: surface the kubescape version too so users can correlate.
		out, err := exec.CommandContext(context.Background(), path, "version").Output()
		if err == nil {
			fmt.Print(string(out))
			return
		}
	}
	fmt.Fprintln(os.Stderr, "kubescape: not found in PATH (set KUBESCAPE_BIN to override)")
}

func printHelp() {
	fmt.Print(`helm kubescape - scan a Helm chart with Kubescape

Usage:
  helm kubescape scan <chart> [helm flags] [kubescape flags]
  helm kubescape version
  helm kubescape help

Chart references (matches 'helm install'):
  ./mychart                   local directory
  ./chart-1.0.0.tgz           local packaged chart
  bitnami/nginx               repo/chart from a configured 'helm repo add'
  oci://ghcr.io/org/chart     OCI registry reference
  https://host/chart-1.tgz    URL to a packaged chart

Helm-style value overrides (forwarded to kubescape):
  -f, --values FILE           values file (repeatable; comma-list splits like Helm)
      --set KEY=VAL           inline value (repeatable; commas inside braces preserved)
      --set-string KEY=VAL    inline STRING value (repeatable)
      --set-file KEY=PATH     value loaded from file (repeatable)
  -n, --namespace NS          release namespace (.Release.Namespace)
      --release-name NAME     release name (.Release.Name)
      --release-namespace NS  release namespace (.Release.Namespace)

Anything else is forwarded verbatim to 'kubescape scan' (--format, --output,
--severity-threshold, --compliance-threshold, ...). Examples:

  # CI gate: fail on any high-severity finding
  helm kubescape scan ./mychart \
      -f values-prod.yaml --set image.tag=v2 \
      --release-name prod --namespace prod \
      --severity-threshold high

  # OCI chart, JSON results
  helm kubescape scan oci://ghcr.io/myorg/mychart --format json --output scan.json

Environment:
  KUBESCAPE_BIN   path to the kubescape binary (default: 'kubescape' from PATH)
`)
}
