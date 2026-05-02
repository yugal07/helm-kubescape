// Package flags parses helm-kubescape's command line.
//
// The plugin's contract is "look like `helm install` on the input side, look like
// `kubescape scan` on the output side". We therefore bind every flag whose name or
// short form differs between the two CLIs, and forward everything else verbatim so
// kubescape-native flags (--format, --output, --severity-threshold, --compliance-threshold,
// ...) just work without the plugin having to know about them.
//
// Flag bindings mirror upstream Helm (helm.sh/helm/v3 cmd/helm/flags.go) so
// comma-handling and repeatability match `helm install` exactly:
//   --values     -> StringSliceVar  (Helm splits on commas: -f a.yaml,b.yaml = two files)
//   --set        -> StringArrayVar  (verbatim; commas inside the value belong to strvals)
//   --set-string -> StringArrayVar
//   --set-file   -> StringArrayVar
package flags

import (
	"fmt"

	"github.com/spf13/pflag"
)

// Parsed is the result of parsing the plugin's argv after the "scan" subcommand.
type Parsed struct {
	// Chart is the positional argument: a local directory, .tgz, repo/chart, oci://, or URL.
	Chart string
	// ValueFiles are the -f/--values entries (already comma-split by pflag's StringSliceVar).
	ValueFiles []string
	// SetValues are the --set entries (verbatim, commas preserved).
	SetValues []string
	// SetStringValues are the --set-string entries.
	SetStringValues []string
	// SetFileValues are the --set-file entries.
	SetFileValues []string
	// ReleaseName populates .Release.Name during chart rendering.
	ReleaseName string
	// ReleaseNamespace populates .Release.Namespace during chart rendering.
	// Both -n/--namespace and --release-namespace land here; --release-namespace wins
	// if both are set, matching the principle of "more-specific flag wins".
	ReleaseNamespace string
	// Passthrough is every other token in argv, preserved in original order. These
	// are forwarded verbatim to `kubescape scan` so kubescape-native flags work
	// without the plugin having to enumerate them.
	Passthrough []string
}

// KubescapeArgs renders Parsed back into the argv vector to pass to `kubescape scan`.
// The chart path is the first positional, then translated Helm flags, then passthrough.
// Order between translated and passthrough flags doesn't matter to cobra/pflag, but we
// emit translated flags first so kubescape's error messages, if any, surface against
// the inputs the plugin owns.
func (p Parsed) KubescapeArgs() []string {
	out := make([]string, 0, 4+2*len(p.ValueFiles)+2*len(p.SetValues)+len(p.Passthrough))
	if p.Chart != "" {
		out = append(out, p.Chart)
	}
	for _, v := range p.ValueFiles {
		out = append(out, "--values", v)
	}
	for _, v := range p.SetValues {
		out = append(out, "--set", v)
	}
	for _, v := range p.SetStringValues {
		out = append(out, "--set-string", v)
	}
	for _, v := range p.SetFileValues {
		out = append(out, "--set-file", v)
	}
	if p.ReleaseName != "" {
		out = append(out, "--release-name", p.ReleaseName)
	}
	if p.ReleaseNamespace != "" {
		out = append(out, "--release-namespace", p.ReleaseNamespace)
	}
	out = append(out, p.Passthrough...)
	return out
}

// Parse parses the argv following the "scan" subcommand.
//
// We use pflag.ContinueOnError + ParseErrorsAllowlist.UnknownFlags so unknown flags
// are not consumed by our FlagSet — they fall through to the passthrough collector.
// This keeps the plugin's flag table small (just the helm-style ones) without
// breaking any kubescape-native flag the plugin hasn't been taught about yet.
func Parse(argv []string) (Parsed, error) {
	var p Parsed
	var namespace string

	fs := pflag.NewFlagSet("helm-kubescape scan", pflag.ContinueOnError)
	fs.ParseErrorsAllowlist.UnknownFlags = true
	// Suppress pflag's default usage-on-error: we render our own help.
	fs.Usage = func() {}

	fs.StringSliceVarP(&p.ValueFiles, "values", "f", nil, "specify values in a YAML file or URL (can specify multiple)")
	fs.StringArrayVar(&p.SetValues, "set", nil, "set values on the command line (can specify multiple, e.g. --set key1=val1)")
	fs.StringArrayVar(&p.SetStringValues, "set-string", nil, "set STRING values on the command line (can specify multiple)")
	fs.StringArrayVar(&p.SetFileValues, "set-file", nil, "set values from respective files (can specify multiple)")
	fs.StringVarP(&namespace, "namespace", "n", "", "release namespace (sets .Release.Namespace)")
	fs.StringVar(&p.ReleaseName, "release-name", "", "release name (sets .Release.Name)")
	fs.StringVar(&p.ReleaseNamespace, "release-namespace", "", "release namespace (sets .Release.Namespace)")

	// Walk argv ourselves to capture passthrough tokens in original order.
	// pflag's parser would either error or silently drop unknown flags; we want
	// to see them so we can forward them to kubescape verbatim.
	//
	// Strategy: ask pflag to parse, and after each successful Parse we feed the
	// remaining args back through. We rely on UnknownFlags=true to skip — but
	// pflag still consumes the unknown token without storing it, so we instead
	// scan argv manually and only hand pflag the slices that contain known flags.
	// Simpler approach: tokenize argv ourselves into known-flag chunks vs passthrough,
	// then call fs.Parse with only the known chunks.
	known := map[string]bool{
		"-f": true, "--values": true,
		"--set": true, "--set-string": true, "--set-file": true,
		"-n": true, "--namespace": true,
		"--release-name": true, "--release-namespace": true,
	}
	var forFS []string
	for i := 0; i < len(argv); i++ {
		tok := argv[i]
		// Positional (chart path): the first non-flag token wins, the rest go to passthrough.
		// We tolerate the chart appearing anywhere in argv, matching `helm install`.
		if len(tok) == 0 || tok[0] != '-' {
			if p.Chart == "" {
				p.Chart = tok
				continue
			}
			p.Passthrough = append(p.Passthrough, tok)
			continue
		}
		if tok == "--" {
			// Conventional end-of-flags marker: everything after is passthrough.
			p.Passthrough = append(p.Passthrough, argv[i+1:]...)
			break
		}
		// Split --flag=value once so we can match the bare flag name.
		name := tok
		hasInlineValue := false
		for j := 0; j < len(tok); j++ {
			if tok[j] == '=' {
				name = tok[:j]
				hasInlineValue = true
				break
			}
		}
		if !known[name] {
			p.Passthrough = append(p.Passthrough, tok)
			continue
		}
		// Known flag. Hand pflag both the flag and its value (if separate).
		forFS = append(forFS, tok)
		if !hasInlineValue && i+1 < len(argv) {
			// All known flags take a value; consume the next token.
			forFS = append(forFS, argv[i+1])
			i++
		}
	}

	if err := fs.Parse(forFS); err != nil {
		return Parsed{}, fmt.Errorf("parsing flags: %w", err)
	}

	// -n/--namespace folds into --release-namespace, with --release-namespace winning
	// if both were given. This mirrors how `helm install -n foo --release-namespace bar`
	// would surface (release-namespace is the more specific flag).
	if p.ReleaseNamespace == "" {
		p.ReleaseNamespace = namespace
	}

	return p, nil
}
