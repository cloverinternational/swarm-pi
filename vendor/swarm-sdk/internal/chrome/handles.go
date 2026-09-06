package chrome

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"regexp"
	"strings"
)

// HandleKind identifies a model-visible opaque handle namespace.
type HandleKind string

const (
	HandleTab    HandleKind = "tab"
	HandleRef    HandleKind = "ref"
	HandleImage  HandleKind = "image"
	HandleUpload HandleKind = "upload"
	HandleWindow HandleKind = "window"
)

var handlePattern = regexp.MustCompile(`^[a-z]+_[A-Za-z0-9_-]{16,}$`)

// HandleBinding is server-side authority; none of these fields are encoded in the handle.
type HandleBinding struct {
	Handle        string
	Kind          HandleKind
	FamilyID      string
	Generation    uint64
	TabHandle     string
	DocumentEpoch uint64
}

// NewHandle creates a random opaque handle that contains no raw Chrome identifier.
func NewHandle(kind HandleKind) (string, error) {
	if !validHandleKind(kind) {
		return "", fmt.Errorf("chrome: invalid handle kind %q", kind)
	}
	random := make([]byte, 18)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("chrome: generate handle: %w", err)
	}
	return string(kind) + "_" + base64.RawURLEncoding.EncodeToString(random), nil
}

// ValidateHandleFormat checks the closed namespace, minimum entropy spelling, and expected prefix.
func ValidateHandleFormat(handle string, expected HandleKind) error {
	if !validHandleKind(expected) || !handlePattern.MatchString(handle) ||
		!strings.HasPrefix(handle, string(expected)+"_") {
		return NewError(ErrInvalidArguments, "Invalid "+string(expected)+" handle.")
	}
	return nil
}

// ValidateBinding authorizes a resolved handle against caller scope.
func ValidateBinding(binding HandleBinding, familyID string, generation uint64, tabHandle string, documentEpoch uint64) error {
	if err := ValidateHandleFormat(binding.Handle, binding.Kind); err != nil {
		return err
	}
	if binding.FamilyID != familyID {
		return NewError(ErrWrongOwner, "Handle belongs to another browser family.")
	}
	if binding.Generation != generation {
		return NewError(ErrStaleGeneration, "Handle belongs to an earlier browser generation.")
	}
	if tabHandle != "" && binding.TabHandle != tabHandle {
		return NewError(ErrTabNotOwned, "Handle does not belong to the requested tab.")
	}
	if binding.Kind == HandleRef && documentEpoch != 0 && binding.DocumentEpoch != documentEpoch {
		return NewError(ErrStaleRef, "Element reference is from an earlier document epoch.")
	}
	return nil
}

func validHandleKind(kind HandleKind) bool {
	switch kind {
	case HandleTab, HandleRef, HandleImage, HandleUpload, HandleWindow:
		return true
	default:
		return false
	}
}
