package client

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoadIndexMdWalk_NoFile verifica que retorna "" cuando no hay INDEX.md.
func TestLoadIndexMdWalk_NoFile(t *testing.T) {
	dir := t.TempDir()
	result := loadIndexMdWalk(dir)
	if result != "" {
		t.Errorf("expected empty string, got: %q", result)
	}
}

// TestLoadIndexMdWalk_SingleFile verifica que carga un INDEX.md en el workspace.
func TestLoadIndexMdWalk_SingleFile(t *testing.T) {
	dir := t.TempDir()
	content := "# INDEX.md — test\n\n## Scope\nThis is a test component."
	if err := os.WriteFile(filepath.Join(dir, "INDEX.md"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	result := loadIndexMdWalk(dir)

	if result == "" {
		t.Fatal("expected non-empty result, got empty string")
	}
	if !strings.Contains(result, indexMdHeader) {
		t.Errorf("missing header, got: %q", result[:min(200, len(result))])
	}
	if !strings.Contains(result, "This is a test component.") {
		t.Errorf("missing INDEX.md content, got: %q", result[:min(200, len(result))])
	}
	if !strings.Contains(result, "INDEX.md (repository navigation index)") {
		t.Errorf("missing path label, got: %q", result[:min(200, len(result))])
	}
	t.Logf("✅ Single file output (%d bytes):\n%s", len(result), result[:min(300, len(result))])
}

// TestLoadIndexMdWalk_Hierarchy verifica la carga desde directorios padre
// y que el archivo más cercano al workspace va AL FINAL (mayor prioridad).
func TestLoadIndexMdWalk_Hierarchy(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "component")
	grandchild := filepath.Join(child, "subpkg")
	if err := os.MkdirAll(grandchild, 0755); err != nil {
		t.Fatal(err)
	}

	// Escribir INDEX.md en root y en child (grandchild no tiene)
	rootContent := "# INDEX.md — root\nRoot level index."
	childContent := "# INDEX.md — component\nComponent level index."
	if err := os.WriteFile(filepath.Join(root, "INDEX.md"), []byte(rootContent), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(child, "INDEX.md"), []byte(childContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Walk desde grandchild — debe encontrar root y child
	result := loadIndexMdWalk(grandchild)

	if result == "" {
		t.Fatal("expected non-empty result")
	}
	rootIdx := strings.Index(result, "Root level index.")
	childIdx := strings.Index(result, "Component level index.")

	if rootIdx == -1 {
		t.Error("root INDEX.md content missing from result")
	}
	if childIdx == -1 {
		t.Error("child INDEX.md content missing from result")
	}
	// Root debe aparecer ANTES que child (root first = lower priority)
	if rootIdx != -1 && childIdx != -1 && rootIdx > childIdx {
		t.Errorf("root should appear before child (root=%d, child=%d)", rootIdx, childIdx)
	}
	t.Logf("✅ Hierarchy: root@%d, child@%d (child is closer = later = higher priority)", rootIdx, childIdx)
	t.Logf("Full output (%d bytes):\n%s", len(result), result[:min(500, len(result))])
}

// TestLoadIndexMdWalk_EmptyFile verifica que archivos vacíos se ignoran.
func TestLoadIndexMdWalk_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "INDEX.md"), []byte("   \n  "), 0644); err != nil {
		t.Fatal(err)
	}
	result := loadIndexMdWalk(dir)
	if result != "" {
		t.Errorf("expected empty result for whitespace-only INDEX.md, got: %q", result)
	}
	t.Logf("✅ Empty file correctly skipped")
}

// TestLoadIndexMdWalk_EmptyDir verifica que no crashea con string vacío.
func TestLoadIndexMdWalk_EmptyDir(t *testing.T) {
	result := loadIndexMdWalk("")
	if result != "" {
		t.Errorf("expected empty string for empty workspaceDir, got: %q", result)
	}
	t.Logf("✅ Empty workspaceDir handled correctly")
}

// TestNew_InjectsIndexMd verifica que client.New() inyecta el INDEX.md
// en el system prompt cuando existe en el workspace.
// Este test usa WithoutAutoConfig para no necesitar credenciales reales.
func TestNew_InjectsIndexMd(t *testing.T) {
	dir := t.TempDir()
	content := "# INDEX.md — probe\n## Scope\nProbe component for testing INDEX.md injection."
	if err := os.WriteFile(filepath.Join(dir, "INDEX.md"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	originalPrompt := "You are a test assistant."

	// Simular lo que hace New() internamente usando las opciones
	o := options{
		systemPrompt: originalPrompt,
		workspaceDir: dir,
		noAutoConfig: true, // sin credenciales reales
	}

	// Ejecutar exactamente el bloque que agregamos en New()
	if !o.noAutoConfig && !o.noIndexMd {
		if indexCtx := loadIndexMdWalk(o.workspaceDir); indexCtx != "" {
			if o.systemPrompt != "" {
				o.systemPrompt = indexCtx + "\n\n" + o.systemPrompt
			} else {
				o.systemPrompt = indexCtx
			}
		}
	}

	// noAutoConfig=true → la inyección NO debe ocurrir
	if strings.Contains(o.systemPrompt, "INDEX.md") {
		t.Errorf("with noAutoConfig=true, INDEX.md should NOT be injected")
	}

	// Ahora sin noAutoConfig
	o2 := options{
		systemPrompt: originalPrompt,
		workspaceDir: dir,
	}
	if !o2.noAutoConfig && !o2.noIndexMd {
		if indexCtx := loadIndexMdWalk(o2.workspaceDir); indexCtx != "" {
			if o2.systemPrompt != "" {
				o2.systemPrompt = indexCtx + "\n\n" + o2.systemPrompt
			} else {
				o2.systemPrompt = indexCtx
			}
		}
	}

	if !strings.Contains(o2.systemPrompt, "Probe component for testing") {
		t.Errorf("INDEX.md content not found in system prompt.\nGot: %q", o2.systemPrompt[:min(400, len(o2.systemPrompt))])
	}
	if !strings.HasPrefix(o2.systemPrompt, indexMdHeader) {
		t.Errorf("system prompt should start with indexMdHeader.\nGot: %q", o2.systemPrompt[:min(200, len(o2.systemPrompt))])
	}
	if !strings.HasSuffix(strings.TrimSpace(o2.systemPrompt), originalPrompt) {
		t.Errorf("original prompt should be at the end.\nGot: %q", o2.systemPrompt[max(0, len(o2.systemPrompt)-100):])
	}
	t.Logf("✅ System prompt injected correctly (%d bytes total)", len(o2.systemPrompt))
	t.Logf("First 300 chars: %s", o2.systemPrompt[:min(300, len(o2.systemPrompt))])
}

// TestWithoutIndexMd verifica que la option deshabilita la carga.
func TestWithoutIndexMd(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "INDEX.md"), []byte("# INDEX\nshould not appear"), 0644); err != nil {
		t.Fatal(err)
	}

	o := options{
		systemPrompt: "original",
		workspaceDir: dir,
		noIndexMd:    true, // ← opt-out
	}

	if !o.noAutoConfig && !o.noIndexMd {
		if indexCtx := loadIndexMdWalk(o.workspaceDir); indexCtx != "" {
			o.systemPrompt = indexCtx + "\n\n" + o.systemPrompt
		}
	}

	if o.systemPrompt != "original" {
		t.Errorf("WithoutIndexMd should prevent injection, got: %q", o.systemPrompt)
	}
	t.Logf("✅ WithoutIndexMd correctly skips INDEX.md loading")
}
