package chrome

import "testing"

func TestOpaqueHandleFormatAndScope(t *testing.T) {
	handle, err := NewHandle(HandleRef)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateHandleFormat(handle, HandleRef); err != nil {
		t.Fatal(err)
	}
	binding := HandleBinding{
		Handle: handle, Kind: HandleRef, FamilyID: "family-a",
		Generation: 3, TabHandle: "tab_AAAAAAAAAAAAAAAA", DocumentEpoch: 9,
	}
	if err := ValidateBinding(binding, "family-a", 3, binding.TabHandle, 9); err != nil {
		t.Fatal(err)
	}
	assertCode := func(err error, code ErrorCode) {
		t.Helper()
		typed, ok := err.(*Error)
		if !ok || typed.Code != code {
			t.Fatalf("error = %#v, want %s", err, code)
		}
	}
	assertCode(ValidateBinding(binding, "family-b", 3, binding.TabHandle, 9), ErrWrongOwner)
	assertCode(ValidateBinding(binding, "family-a", 4, binding.TabHandle, 9), ErrStaleGeneration)
	assertCode(ValidateBinding(binding, "family-a", 3, "tab_BBBBBBBBBBBBBBBB", 9), ErrTabNotOwned)
	assertCode(ValidateBinding(binding, "family-a", 3, binding.TabHandle, 10), ErrStaleRef)
}

func TestHandleRejectsWrongKindAndEncodedIdentifiers(t *testing.T) {
	for _, value := range []string{"tab_123", "ref_1234567890123456", "tab_1234567890123456/window=42"} {
		if err := ValidateHandleFormat(value, HandleTab); err == nil {
			t.Fatalf("accepted invalid handle %q", value)
		}
	}
}
