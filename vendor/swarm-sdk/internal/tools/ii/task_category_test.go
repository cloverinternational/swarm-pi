package ii

import (
	"slices"
	"testing"
)

// TestTaskCategory_ValidValues tests that all 6 categories are valid
func TestTaskCategory_ValidValues(t *testing.T) {
	categories := ValidCategories()
	expectedCount := 6
	if len(categories) != expectedCount {
		t.Errorf("Expected %d categories, got %d", expectedCount, len(categories))
	}

	// Verify each category is defined
	expected := []TaskCategory{
		TaskCategoryResearching,
		TaskCategoryPlanning,
		TaskCategoryActing,
		TaskCategoryVerifying,
		TaskCategoryDebugging,
		TaskCategoryDocumenting,
	}

	for _, cat := range expected {
		found := slices.Contains(categories, cat)
		if !found {
			t.Errorf("Category %s not found in ValidCategories()", cat)
		}
	}
}

// TestTaskCategory_InvalidValue tests that invalid categories fail validation
func TestTaskCategory_InvalidValue(t *testing.T) {
	item := TodoItem{
		ID:       "test-1",
		Content:  "Test task",
		Status:   TodoStatusPending,
		Priority: TodoPriorityMedium,
		Category: TaskCategory("invalid"),
	}

	err := item.Validate()
	if err == nil {
		t.Error("Expected validation error for invalid category, got nil")
	}
	if err != nil && !containsString(err.Error(), "invalid category") {
		t.Errorf("Expected error message to contain 'invalid category', got: %s", err.Error())
	}
}

// TestTaskCategory_EmptyAllowed tests that empty category is valid (defaults to acting)
func TestTaskCategory_EmptyAllowed(t *testing.T) {
	item := TodoItem{
		ID:       "test-1",
		Content:  "Test task",
		Status:   TodoStatusPending,
		Priority: TodoPriorityMedium,
		Category: TaskCategory(""), // Empty should be valid
	}

	err := item.Validate()
	if err != nil {
		t.Errorf("Expected no validation error for empty category, got: %v", err)
	}
}

// TestInferCategory_DebuggingKeywords tests debugging keyword inference.
// Debugging keywords are now compound phrases to avoid false positives from
// single words like "error" or "fix" in normal task descriptions.
func TestInferCategory_DebuggingKeywords(t *testing.T) {
	testCases := []struct {
		content  string
		expected TaskCategory
	}{
		{"Fix bug in login", TaskCategoryDebugging},
		{"Debug the connection issue", TaskCategoryDebugging},
		{"Trace error in memory allocator", TaskCategoryDebugging},
		{"Resolve error in payment flow", TaskCategoryDebugging},
		{"Fix crash on startup", TaskCategoryDebugging},
		{"Troubleshoot timeout issues", TaskCategoryDebugging},
		{"Fix error in serialization", TaskCategoryDebugging},
		{"Trace bug in event loop", TaskCategoryDebugging},
		{"Fix panic on nil pointer", TaskCategoryDebugging},
		{"Resolve issue in auth module", TaskCategoryDebugging},
	}

	for _, tc := range testCases {
		result := InferCategory(tc.content)
		if result != tc.expected {
			t.Errorf("InferCategory(%q) = %s, expected %s", tc.content, result, tc.expected)
		}
	}
}

// TestInferCategory_ResearchingKeywords tests researching keyword inference
func TestInferCategory_ResearchingKeywords(t *testing.T) {
	testCases := []struct {
		content  string
		expected TaskCategory
	}{
		{"Research best practices for auth", TaskCategoryResearching},
		{"Explore codebase structure", TaskCategoryResearching},
		{"Investigate database schema", TaskCategoryResearching},
		{"Search for API documentation", TaskCategoryResearching},
		{"Analyze performance patterns", TaskCategoryResearching},
		{"Read configuration file", TaskCategoryResearching},
		{"Understand the build process", TaskCategoryResearching},
		{"Study the protocol spec", TaskCategoryResearching},
		{"Review docs for deployment", TaskCategoryResearching},
		{"Examine the log files", TaskCategoryResearching},
	}

	for _, tc := range testCases {
		result := InferCategory(tc.content)
		if result != tc.expected {
			t.Errorf("InferCategory(%q) = %s, expected %s", tc.content, result, tc.expected)
		}
	}
}

// TestInferCategory_VerifyingKeywords tests verifying keyword inference
func TestInferCategory_VerifyingKeywords(t *testing.T) {
	testCases := []struct {
		content  string
		expected TaskCategory
	}{
		{"Test integration with API", TaskCategoryVerifying},
		{"Verify deployment succeeded", TaskCategoryVerifying},
		{"Validate user input", TaskCategoryVerifying},
		{"Check configuration is correct", TaskCategoryVerifying},
		{"Confirm the fix works", TaskCategoryVerifying},
		{"Ensure all tests pass", TaskCategoryVerifying},
		{"Assert behavior is correct", TaskCategoryVerifying},
		{"Inspect the output", TaskCategoryVerifying},
	}

	for _, tc := range testCases {
		result := InferCategory(tc.content)
		if result != tc.expected {
			t.Errorf("InferCategory(%q) = %s, expected %s", tc.content, result, tc.expected)
		}
	}
}

// TestInferCategory_PlanningKeywords tests planning keyword inference
func TestInferCategory_PlanningKeywords(t *testing.T) {
	testCases := []struct {
		content  string
		expected TaskCategory
	}{
		{"Plan the migration strategy", TaskCategoryPlanning},
		{"Design the API schema", TaskCategoryPlanning},
		{"Architect the new module", TaskCategoryPlanning},
		{"Create implementation strategy", TaskCategoryPlanning},
		{"Draft the technical specification", TaskCategoryPlanning},
		{"Outline the refactoring approach", TaskCategoryPlanning},
		{"Write proposal for new feature", TaskCategoryPlanning},
	}

	for _, tc := range testCases {
		result := InferCategory(tc.content)
		if result != tc.expected {
			t.Errorf("InferCategory(%q) = %s, expected %s", tc.content, result, tc.expected)
		}
	}
}

// TestInferCategory_DocumentingKeywords tests documenting keyword inference
func TestInferCategory_DocumentingKeywords(t *testing.T) {
	testCases := []struct {
		content  string
		expected TaskCategory
	}{
		{"Document the API endpoints", TaskCategoryDocumenting},
		{"Write docs for the module", TaskCategoryDocumenting},
		{"Add changelog entry for v2.0", TaskCategoryDocumenting},
		{"Write documentation for the CLI", TaskCategoryDocumenting},
		{"Add godoc comments to exported functions", TaskCategoryDocumenting},
		{"Update the docstrings", TaskCategoryDocumenting},
		{"Write javadoc for the class", TaskCategoryDocumenting},
		{"Create documentation for the feature", TaskCategoryDocumenting},
	}

	for _, tc := range testCases {
		result := InferCategory(tc.content)
		if result != tc.expected {
			t.Errorf("InferCategory(%q) = %s, expected %s", tc.content, result, tc.expected)
		}
	}
}

// TestInferCategory_ActingDefault tests that unrecognized tasks default to acting.
// This ensures tasks without keyword matches don't get write tools blocked.
func TestInferCategory_ActingDefault(t *testing.T) {
	testCases := []struct {
		content string
	}{
		{"Implement user authentication"},
		{"Write the API handler"},
		{"Create database migration"},
		{"Update configuration file"},
		{"Refactor the legacy code"},
		{"Add new feature"},
		{"Build the component"},
		{"Some random task"},
	}

	for _, tc := range testCases {
		result := InferCategory(tc.content)
		if result != TaskCategoryActing {
			t.Errorf("InferCategory(%q) = %s, expected %s (acting default)", tc.content, result, TaskCategoryActing)
		}
	}
}

// TestInferCategory_Priority tests priority ordering: planning > researching > debugging > verifying
func TestInferCategory_Priority(t *testing.T) {
	// "plan" should trigger planning even though "test" would trigger verifying
	result := InferCategory("Plan the test strategy")
	if result != TaskCategoryPlanning {
		t.Errorf("InferCategory('Plan the test strategy') = %s, expected %s (planning priority)", result, TaskCategoryPlanning)
	}

	// "research" should trigger researching even though "debug" would trigger debugging
	result = InferCategory("Research and debug the issue")
	if result != TaskCategoryResearching {
		t.Errorf("InferCategory('Research and debug the issue') = %s, expected %s (researching priority)", result, TaskCategoryResearching)
	}

	// "fix bug" should trigger debugging even though "test" would trigger verifying
	result = InferCategory("Fix bug in test suite")
	if result != TaskCategoryDebugging {
		t.Errorf("InferCategory('Fix bug in test suite') = %s, expected %s (debugging priority)", result, TaskCategoryDebugging)
	}
}

// TestTodoItem_CloneWithCategory tests that Clone preserves category
func TestTodoItem_CloneWithCategory(t *testing.T) {
	original := TodoItem{
		ID:        "test-1",
		Content:   "Test task",
		Status:    TodoStatusPending,
		Priority:  TodoPriorityHigh,
		Category:  TaskCategoryDebugging,
		DependsOn: []string{"dep-1", "dep-2"},
		Blocks:    []string{"block-1"},
	}

	clone := original.Clone()

	// Verify category is preserved
	if clone.Category != original.Category {
		t.Errorf("Clone().Category = %s, expected %s", clone.Category, original.Category)
	}

	// Verify it's a true copy (modifying original doesn't affect clone)
	original.Category = TaskCategoryResearching
	if clone.Category == original.Category {
		t.Error("Clone shares Category reference with original")
	}
}

// TestTodoManager_ByCategory tests filtering by category
func TestTodoManager_ByCategory(t *testing.T) {
	manager := NewTodoManager()

	// Add tasks with different categories (Status and Priority are required)
	manager.AddTodo(TodoItem{ID: "1", Content: "Fix bug", Status: TodoStatusPending, Priority: TodoPriorityMedium, Category: TaskCategoryDebugging})
	manager.AddTodo(TodoItem{ID: "2", Content: "Research API", Status: TodoStatusPending, Priority: TodoPriorityMedium, Category: TaskCategoryResearching})
	manager.AddTodo(TodoItem{ID: "3", Content: "Write code", Status: TodoStatusPending, Priority: TodoPriorityMedium, Category: TaskCategoryActing})
	manager.AddTodo(TodoItem{ID: "4", Content: "Debug issue", Status: TodoStatusPending, Priority: TodoPriorityMedium, Category: TaskCategoryDebugging})

	// Filter by debugging
	debugging := manager.ByCategory(TaskCategoryDebugging)
	if len(debugging) != 2 {
		t.Errorf("ByCategory(debugging) returned %d items, expected 2", len(debugging))
	}

	// Filter by researching
	researching := manager.ByCategory(TaskCategoryResearching)
	if len(researching) != 1 {
		t.Errorf("ByCategory(researching) returned %d items, expected 1", len(researching))
	}

	// Filter by planning (none)
	planning := manager.ByCategory(TaskCategoryPlanning)
	if len(planning) != 0 {
		t.Errorf("ByCategory(planning) returned %d items, expected 0", len(planning))
	}
}

// TestTodoItemFromMap_WithCategory tests parsing category from map
func TestTodoItemFromMap_WithCategory(t *testing.T) {
	m := map[string]any{
		"id":       "test-1",
		"content":  "Test task",
		"status":   "pending",
		"priority": "medium",
		"category": "debugging",
	}

	item, err := TodoItemFromMap(m)
	if err != nil {
		t.Fatalf("TodoItemFromMap returned error: %v", err)
	}

	if item.Category != TaskCategoryDebugging {
		t.Errorf("Category = %s, expected %s", item.Category, TaskCategoryDebugging)
	}
}

// TestTodoItemFromMap_WithoutCategory tests that missing category is ok
func TestTodoItemFromMap_WithoutCategory(t *testing.T) {
	m := map[string]any{
		"id":       "test-1",
		"content":  "Test task",
		"status":   "pending",
		"priority": "medium",
	}

	item, err := TodoItemFromMap(m)
	if err != nil {
		t.Fatalf("TodoItemFromMap returned error: %v", err)
	}

	if item.Category != "" {
		t.Errorf("Category = %s, expected empty string", item.Category)
	}
}

// TestTodoItemToMap_WithCategory tests that category is included in map
func TestTodoItemToMap_WithCategory(t *testing.T) {
	item := TodoItem{
		ID:       "test-1",
		Content:  "Test task",
		Status:   TodoStatusPending,
		Priority: TodoPriorityMedium,
		Category: TaskCategoryResearching,
	}

	m := TodoItemToMap(item)

	cat, ok := m["category"].(string)
	if !ok {
		t.Error("TodoItemToMap did not include category key")
	}
	if cat != "researching" {
		t.Errorf("category = %s, expected 'researching'", cat)
	}
}

// TestTodoItemToMap_WithoutCategory tests that empty category is not included
func TestTodoItemToMap_WithoutCategory(t *testing.T) {
	item := TodoItem{
		ID:       "test-1",
		Content:  "Test task",
		Status:   TodoStatusPending,
		Priority: TodoPriorityMedium,
		Category: "", // Empty
	}

	m := TodoItemToMap(item)

	if _, ok := m["category"]; ok {
		t.Error("TodoItemToMap included category key for empty category, expected it to be omitted")
	}
}

// Helper function to check if a string contains a substring
func containsString(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && findSubstring(s, substr)))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
