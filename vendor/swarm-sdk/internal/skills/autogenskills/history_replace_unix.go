//go:build !windows

package autogenskills

import "os"

func replaceHistoryFile(source, destination string) error {
	return os.Rename(source, destination)
}
