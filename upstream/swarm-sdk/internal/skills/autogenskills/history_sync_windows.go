//go:build windows

package autogenskills

// Windows does not provide a supported equivalent of fsync on a directory
// handle. File contents are still synced before ReplaceFile/Rename publishes
// them; there is no additional directory operation available here.
func syncHistoryDir(string) error {
	return nil
}
