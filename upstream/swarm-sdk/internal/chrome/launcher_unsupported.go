//go:build !linux && !darwin && !windows

package chrome

func platformDiscoverChrome() (string, error) {
	return "", NewError(ErrUnsupportedHost, "Chrome launcher is unsupported on this host")
}
