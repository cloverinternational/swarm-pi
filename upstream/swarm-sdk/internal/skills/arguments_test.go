package skills

import (
	"testing"
)

func TestSubstituteArguments_NamedArgs(t *testing.T) {
	content := "Clone {{repo}} on branch {{branch}}"
	result := SubstituteArguments(content, "myrepo main", []string{"repo", "branch"})
	expected := "Clone myrepo on branch main"
	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}

func TestSubstituteArguments_PositionalArgs(t *testing.T) {
	content := "First: {{1}}, Second: {{2}}"
	result := SubstituteArguments(content, "alpha beta", nil)
	expected := "First: alpha, Second: beta"
	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}

func TestSubstituteArguments_MixedNamedAndPositional(t *testing.T) {
	content := "Repo {{repo}}, Pos: {{1}}, Branch {{branch}}, Pos2: {{2}}"
	result := SubstituteArguments(content, "myrepo main", []string{"repo", "branch"})
	expected := "Repo myrepo, Pos: myrepo, Branch main, Pos2: main"
	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}

func TestSubstituteArguments_EmptyArgs(t *testing.T) {
	content := "No args {{repo}} here"
	result := SubstituteArguments(content, "", []string{"repo"})
	if result != content {
		t.Errorf("got %q, want %q (unchanged)", result, content)
	}
}

func TestSubstituteArguments_EmptyContent(t *testing.T) {
	result := SubstituteArguments("", "some args", []string{"repo"})
	if result != "" {
		t.Errorf("got %q, want empty string", result)
	}
}

func TestSubstituteArguments_MissingArg(t *testing.T) {
	content := "Repo {{repo}}, Missing {{unknown}}"
	result := SubstituteArguments(content, "myrepo", []string{"repo"})
	expected := "Repo myrepo, Missing {{unknown}}"
	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}

func TestSubstituteArguments_ExtraArgs(t *testing.T) {
	content := "Only {{first}}"
	result := SubstituteArguments(content, "one two three", []string{"first"})
	expected := "Only one"
	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}

func TestSubstituteArguments_PositionalOutOfRange(t *testing.T) {
	content := "First: {{1}}, Tenth: {{10}}"
	result := SubstituteArguments(content, "alpha", nil)
	expected := "First: alpha, Tenth: {{10}}"
	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}
