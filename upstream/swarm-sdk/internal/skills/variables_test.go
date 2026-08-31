package skills

import (
	"testing"
)

func TestSubstituteVariables_SkillDir(t *testing.T) {
	content := "Run ${SWARM_SKILL_DIR}/scripts/deploy.sh"
	result := SubstituteVariables(content, "/home/user/.swarm/skills/deploy", "sess_abc")
	expected := "Run /home/user/.swarm/skills/deploy/scripts/deploy.sh"
	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}

func TestSubstituteVariables_SessionID(t *testing.T) {
	content := "Session: ${SWARM_SESSION_ID}"
	result := SubstituteVariables(content, "/skill/path", "sess_12345")
	expected := "Session: sess_12345"
	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}

func TestSubstituteVariables_Both(t *testing.T) {
	content := "Skill dir: ${SWARM_SKILL_DIR}, session: ${SWARM_SESSION_ID}"
	result := SubstituteVariables(content, "/opt/skills/test", "sess_abc")
	expected := "Skill dir: /opt/skills/test, session: sess_abc"
	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}

func TestSubstituteVariables_EmptyContent(t *testing.T) {
	result := SubstituteVariables("", "/path", "sess")
	if result != "" {
		t.Errorf("got %q, want empty string", result)
	}
}

func TestSubstituteVariables_EmptyValues(t *testing.T) {
	content := "Dir: ${SWARM_SKILL_DIR}, Sess: ${SWARM_SESSION_ID}"
	result := SubstituteVariables(content, "", "")
	expected := "Dir: , Sess: "
	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}

func TestSubstituteVariables_NoVariables(t *testing.T) {
	content := "No variables here"
	result := SubstituteVariables(content, "/path", "sess")
	if result != content {
		t.Errorf("got %q, want %q (unchanged)", result, content)
	}
}

func TestSubstituteVariables_MultipleOccurrences(t *testing.T) {
	content := "${SWARM_SKILL_DIR}/a and ${SWARM_SKILL_DIR}/b"
	result := SubstituteVariables(content, "/root", "sess")
	expected := "/root/a and /root/b"
	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}
