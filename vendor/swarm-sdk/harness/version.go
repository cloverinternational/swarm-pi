package harness

import (
	"strconv"
	"strings"
)

// Stable identity constants for the v1alpha1 document.
const (
	// GroupName is the API group for harness documents.
	GroupName = "swarm.ai"
	// APIVersionV1Alpha1 is the only currently supported apiVersion string.
	APIVersionV1Alpha1 = GroupName + "/v1alpha1"
	// KindHarness is the required document kind.
	KindHarness = "Harness"

	// supportedMajor is the only accepted major version.
	supportedMajor = 1
)

// splitAPIVersion splits "group/version" into its parts. An apiVersion with no
// "/" is invalid; there is no implicit core group for harness documents.
func splitAPIVersion(s string) (group, version string, ok bool) {
	i := strings.LastIndex(s, "/")
	if i <= 0 || i == len(s)-1 {
		return "", "", false
	}
	return s[:i], s[i+1:], true
}

// majorFromVersion extracts the leading major number from a Kubernetes-style
// version segment such as "v1alpha1" (major 1) or "v2" (major 2).
func majorFromVersion(v string) (int, bool) {
	if !strings.HasPrefix(v, "v") {
		return 0, false
	}
	j := 1
	for j < len(v) && v[j] >= '0' && v[j] <= '9' {
		j++
	}
	if j == 1 { // no digits after the leading 'v'
		return 0, false
	}
	n, err := strconv.Atoi(v[1:j])
	if err != nil {
		return 0, false
	}
	return n, true
}

// validateVersion checks apiVersion/kind and returns diagnostics with actionable
// messages. It rejects unknown groups, unknown kinds, and unknown/future major
// versions distinctly so an operator can tell an upgrade from a typo.
func validateVersion(sourcePath, apiVersion, kind string) Diagnostics {
	var ds Diagnostics

	if apiVersion == "" {
		ds = append(ds, newDiag("harness.version.missing", "field",
			"apiVersion is required; expected \""+APIVersionV1Alpha1+"\"", sourcePath))
		return ds
	}

	group, version, ok := splitAPIVersion(apiVersion)
	if !ok {
		ds = append(ds, newDiag("harness.version.malformed", "apiVersion",
			"apiVersion "+quote(apiVersion)+" is malformed; expected group/version like "+quote(APIVersionV1Alpha1), sourcePath))
		return ds
	}
	if group != GroupName {
		ds = append(ds, newDiag("harness.version.unknownGroup", "apiVersion",
			"unknown API group "+quote(group)+"; this compiler only understands group "+quote(GroupName), sourcePath))
	}

	major, ok := majorFromVersion(version)
	switch {
	case !ok:
		ds = append(ds, newDiag("harness.version.malformed", "apiVersion",
			"version segment "+quote(version)+" is malformed; expected a value like \"v1alpha1\"", sourcePath))
	case major > supportedMajor:
		ds = append(ds, newDiag("harness.version.futureMajor", "apiVersion",
			"apiVersion "+quote(apiVersion)+" requires a newer major version (v"+strconv.Itoa(major)+"); this build supports "+quote(APIVersionV1Alpha1)+" (major v"+strconv.Itoa(supportedMajor)+"). Upgrade the tool or pin apiVersion to "+quote(APIVersionV1Alpha1), sourcePath))
	case group == GroupName && apiVersion != APIVersionV1Alpha1:
		ds = append(ds, newDiag("harness.version.unsupported", "apiVersion",
			"apiVersion "+quote(apiVersion)+" is not supported; use "+quote(APIVersionV1Alpha1), sourcePath))
	}

	if kind == "" {
		ds = append(ds, newDiag("harness.kind.missing", "kind",
			"kind is required; expected "+quote(KindHarness), sourcePath))
	} else if kind != KindHarness {
		ds = append(ds, newDiag("harness.kind.unknown", "kind",
			"unknown kind "+quote(kind)+"; expected "+quote(KindHarness), sourcePath))
	}

	return ds
}

func quote(s string) string { return "\"" + s + "\"" }
