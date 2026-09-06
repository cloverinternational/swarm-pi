package lan

import (
	"fmt"
	"os"
)

func openSecureCredentialFile(string) (*os.File, error) {
	// Correct validation requires Windows security-descriptor and ACL checks.
	// Fail closed until those checks can establish owner-only storage.
	return nil, fmt.Errorf("%w: Windows ACL validation is not implemented", ErrCredentialStorage)
}

func ownedByCurrentUser(os.FileInfo) bool {
	// Do not claim ownership without validating the Windows security descriptor.
	return false
}
