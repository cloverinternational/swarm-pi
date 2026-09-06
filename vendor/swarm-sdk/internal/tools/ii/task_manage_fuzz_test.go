package ii

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
)

func FuzzTaskManageArbitraryJSON(f *testing.F) {
	for _, seed := range []string{
		`{}`,
		`{"operations":[]}`,
		`{"operations":[{"key":"x","op":"create","subject":"X","description":"X","addBlockedBy":[]}]}`,
		`{"mode":"atomic","operations":[{"key":"x","op":"create","subject":"X","description":"X","addBlocks":[{"ref":"missing"}]}]}`,
		`{"operations":[null,1,"x",[],{}]}`,
	} {
		f.Add([]byte(seed))
	}

	f.Fuzz(func(t *testing.T, input []byte) {
		if len(input) > 16*1024 {
			t.Skip()
		}
		var params map[string]any
		if err := json.Unmarshal(input, &params); err != nil || params == nil {
			return
		}

		manager := NewTodoManager()
		result, err := NewTaskManageToolWithManager(manager).Execute(context.Background(), params)
		if err != nil {
			t.Fatalf("Execute returned an infrastructure error: %v", err)
		}
		var batch TaskBatchResult
		if err := json.Unmarshal([]byte(result.Output), &batch); err != nil {
			t.Fatalf("TaskManage returned invalid JSON %q: %v", result.Output, err)
		}
		if err := validateTodoGraph(manager.Todos()); err != nil {
			t.Fatalf("TaskManage published an invalid graph: %v", err)
		}
	})
}

func FuzzTaskManageCreateDependencies(f *testing.F) {
	// n, completed mask, addBlockedBy mask, addBlocks mask, flags,
	// status selector, atomic mode.
	f.Add(uint8(3), uint8(0), uint8(1), uint8(2), uint8(0), uint8(0), false)
	f.Add(uint8(3), uint8(1), uint8(1), uint8(0), uint8(0), uint8(1), true)
	f.Add(uint8(3), uint8(0), uint8(1), uint8(1), uint8(0), uint8(0), false)
	f.Add(uint8(2), uint8(0), uint8(0), uint8(1), uint8(3), uint8(2), true)

	f.Fuzz(func(
		t *testing.T,
		nRaw, completedRaw, blockedByRaw, blocksRaw, flags, statusRaw uint8,
		atomic bool,
	) {
		n := int(nRaw%6) + 1
		validMask := uint8((1 << n) - 1)
		completedMask := completedRaw & validMask
		blockedByMask := blockedByRaw & validMask
		blocksMask := blocksRaw & validMask

		manager := NewTodoManager()
		for i := 0; i < n; i++ {
			status := TodoStatusPending
			if completedMask&(1<<i) != 0 {
				status = TodoStatusCompleted
			}
			if _, err := manager.AddTodoAutoID(transactionTodo("", status)); err != nil {
				t.Fatalf("seed task %d: %v", i, err)
			}
		}
		before := manager.Todos()

		blockedBy := fuzzDependencyTargets(n, blockedByMask, flags&1 != 0)
		blocks := fuzzDependencyTargets(n, blocksMask, flags&1 != 0)
		missingBlockedBy := flags&2 != 0
		missingBlocks := flags&4 != 0
		if missingBlockedBy {
			blockedBy = append(blockedBy, "999")
		}
		if missingBlocks {
			blocks = append(blocks, "999")
		}

		statuses := []string{"pending", "in_progress", "completed"}
		status := statuses[int(statusRaw)%len(statuses)]
		mode := "sequential"
		if atomic {
			mode = "atomic"
		}
		batch := executeTaskManage(t, manager, map[string]any{
			"mode": mode,
			"operations": []any{map[string]any{
				"key":          "created",
				"op":           "create",
				"subject":      "Created",
				"description":  "Created details",
				"status":       status,
				"addBlockedBy": blockedBy,
				"addBlocks":    blocks,
			}},
		})

		hasCycle := blockedByMask&blocksMask != 0
		incompleteBlockedBy := blockedByMask &^ completedMask
		shouldFail := missingBlockedBy || missingBlocks || hasCycle ||
			(status == "in_progress" && incompleteBlockedBy != 0)
		if shouldFail {
			if batch.Status == "succeeded" {
				t.Fatalf("unexpected success: n=%d completed=%08b blockedBy=%08b blocks=%08b flags=%08b status=%s",
					n, completedMask, blockedByMask, blocksMask, flags, status)
			}
			if got := manager.Todos(); !reflect.DeepEqual(got, before) {
				t.Fatalf("failed create mutated state\ngot:  %#v\nwant: %#v", got, before)
			}
			return
		}

		if batch.Status != "succeeded" {
			t.Fatalf("unexpected failure: %+v", batch)
		}
		if err := validateTodoGraph(manager.Todos()); err != nil {
			t.Fatalf("successful create produced invalid graph: %v", err)
		}
		createdID := fmt.Sprintf("%d", n+1)
		created := manager.ByID(createdID)
		if created == nil {
			t.Fatalf("created task %s missing", createdID)
		}
		if want := fuzzDependencyIDs(n, blockedByMask); !reflect.DeepEqual(created.DependsOn, want) {
			t.Fatalf("created dependencies = %v, want %v", created.DependsOn, want)
		}
		for i := 0; i < n; i++ {
			task := manager.ByID(fmt.Sprintf("%d", i+1))
			want := []string(nil)
			if blocksMask&(1<<i) != 0 {
				want = []string{createdID}
			}
			if !reflect.DeepEqual(task.DependsOn, want) {
				t.Fatalf("task %s dependencies = %v, want %v", task.ID, task.DependsOn, want)
			}
		}
	})
}

func TestTaskManageCreateDependenciesPersistenceFailureRollsBack(t *testing.T) {
	manager := NewTodoManager()
	if _, err := manager.AddTodoAutoID(transactionTodo("", TodoStatusPending)); err != nil {
		t.Fatal(err)
	}
	before := manager.Todos()
	syncer := &countingTaskSyncer{err: errors.New("persist failed")}
	manager.SetSyncer(syncer)

	batch := executeTaskManage(t, manager, map[string]any{"operations": []any{
		map[string]any{
			"key":          "created",
			"op":           "create",
			"subject":      "Created",
			"description":  "Created details",
			"addBlockedBy": []any{"1"},
		},
	}})
	if batch.Status != "failed" || batch.Results[0].Error == nil ||
		batch.Results[0].Error.Code != "persistence_failed" {
		t.Fatalf("unexpected persistence result: %+v", batch)
	}
	if got := manager.Todos(); !reflect.DeepEqual(got, before) {
		t.Fatalf("persistence failure mutated state\ngot:  %#v\nwant: %#v", got, before)
	}
	if got := syncer.Calls(); got != 1 {
		t.Fatalf("sync calls = %d, want 1", got)
	}
}

func TestTaskManageCreateDependenciesResolveAcrossCalls(t *testing.T) {
	manager := NewTodoManager()
	first := executeTaskManage(t, manager, map[string]any{"operations": []any{
		createOperation("issues-review", "Review issues"),
		createOperation("release", "Release"),
	}})
	if first.Status != "succeeded" {
		t.Fatalf("seed call failed: %+v", first)
	}

	second := executeTaskManage(t, manager, map[string]any{"operations": []any{
		map[string]any{
			"key":          "implementation",
			"op":           "create",
			"subject":      "Implementation",
			"description":  "Implementation details",
			"addBlockedBy": []any{taskRef("issues-review")},
			"addBlocks":    []any{taskRef("release")},
		},
	}})
	if second.Status != "succeeded" {
		t.Fatalf("cross-call create references failed: %+v", second)
	}
	if got := manager.ByID("3").DependsOn; !reflect.DeepEqual(got, []string{"1"}) {
		t.Fatalf("created dependencies = %v, want [1]", got)
	}
	if got := manager.ByID("2").DependsOn; !reflect.DeepEqual(got, []string{"3"}) {
		t.Fatalf("release dependencies = %v, want [3]", got)
	}
}

func TestTaskManageCreateInProgressHonorsDependencies(t *testing.T) {
	t.Run("pending dependency rejects and rolls back", func(t *testing.T) {
		manager := NewTodoManager()
		executeTaskManage(t, manager, map[string]any{"operations": []any{
			createOperation("prerequisite", "Prerequisite"),
		}})
		before := manager.Todos()
		batch := executeTaskManage(t, manager, map[string]any{"operations": []any{
			map[string]any{
				"key":          "work",
				"op":           "create",
				"subject":      "Work",
				"description":  "Work details",
				"status":       "in_progress",
				"addBlockedBy": []any{taskRef("prerequisite")},
			},
		}})
		if batch.Status != "failed" {
			t.Fatalf("unexpected success: %+v", batch)
		}
		if got := manager.Todos(); !reflect.DeepEqual(got, before) {
			t.Fatalf("dependency rejection mutated state\ngot:  %#v\nwant: %#v", got, before)
		}
	})

	t.Run("completed dependency permits active create", func(t *testing.T) {
		manager := NewTodoManager()
		executeTaskManage(t, manager, map[string]any{"operations": []any{
			map[string]any{
				"key":         "prerequisite",
				"op":          "create",
				"subject":     "Prerequisite",
				"description": "Prerequisite details",
				"status":      "completed",
			},
		}})
		batch := executeTaskManage(t, manager, map[string]any{"operations": []any{
			map[string]any{
				"key":          "work",
				"op":           "create",
				"subject":      "Work",
				"description":  "Work details",
				"status":       "in_progress",
				"addBlockedBy": []any{taskRef("prerequisite")},
			},
		}})
		if batch.Status != "succeeded" {
			t.Fatalf("completed dependency rejected: %+v", batch)
		}
		task := manager.ByID("2")
		if task == nil || task.Status != TodoStatusInProgress || !task.Active {
			t.Fatalf("created task is not active/in_progress: %+v", task)
		}
	})
}

func TestTaskManageAtomicLaterFailureRollsBackCreateDependencyMutations(t *testing.T) {
	manager := NewTodoManager()
	executeTaskManage(t, manager, map[string]any{"operations": []any{
		createOperation("release", "Release"),
	}})
	before := manager.Todos()

	batch := executeTaskManage(t, manager, map[string]any{
		"mode": "atomic",
		"operations": []any{
			map[string]any{
				"key":         "implementation",
				"op":          "create",
				"subject":     "Implementation",
				"description": "Implementation details",
				"addBlocks":   []any{taskRef("release")},
			},
			map[string]any{
				"key":    "missing",
				"op":     "update",
				"taskId": "999",
				"status": "completed",
			},
		},
	})
	if batch.Status != "failed" {
		t.Fatalf("atomic batch unexpectedly succeeded: %+v", batch)
	}
	if got := manager.Todos(); !reflect.DeepEqual(got, before) {
		t.Fatalf("atomic rollback leaked dependency mutations\ngot:  %#v\nwant: %#v", got, before)
	}
}

func fuzzDependencyTargets(n int, mask uint8, duplicate bool) []any {
	targets := make([]any, 0, n*2)
	for i := 0; i < n; i++ {
		if mask&(1<<i) == 0 {
			continue
		}
		id := fmt.Sprintf("%d", i+1)
		targets = append(targets, id)
		if duplicate {
			targets = append(targets, id)
		}
	}
	return targets
}

func fuzzDependencyIDs(n int, mask uint8) []string {
	var ids []string
	for i := 0; i < n; i++ {
		if mask&(1<<i) != 0 {
			ids = append(ids, fmt.Sprintf("%d", i+1))
		}
	}
	return ids
}
