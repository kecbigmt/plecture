package service

import "testing"

func TestMergeTaskInput_NoTask(t *testing.T) {
	inputs := map[string]any{"a": 1}
	got, err := MergeTaskInput(inputs, "")
	if err != nil {
		t.Fatalf("MergeTaskInput: %v", err)
	}
	if len(got) != 1 || got["a"] != 1 {
		t.Errorf("inputs mutated with no task: %+v", got)
	}
}

func TestMergeTaskInput_SetsTaskOnNilInputs(t *testing.T) {
	got, err := MergeTaskInput(nil, "work")
	if err != nil {
		t.Fatalf("MergeTaskInput: %v", err)
	}
	if got["task"] != "work" {
		t.Errorf("task = %v, want work", got["task"])
	}
}

func TestMergeTaskInput_AddsToExistingInputs(t *testing.T) {
	got, err := MergeTaskInput(map[string]any{"a": 1}, "review")
	if err != nil {
		t.Fatalf("MergeTaskInput: %v", err)
	}
	if got["a"] != 1 || got["task"] != "review" {
		t.Errorf("merged inputs = %+v", got)
	}
}

func TestMergeTaskInput_IdempotentWithMatchingInputsTask(t *testing.T) {
	got, err := MergeTaskInput(map[string]any{"task": "work"}, "work")
	if err != nil {
		t.Fatalf("MergeTaskInput: %v", err)
	}
	if got["task"] != "work" {
		t.Errorf("task = %v, want work", got["task"])
	}
}

func TestMergeTaskInput_ConflictWithInputsTaskErrors(t *testing.T) {
	if _, err := MergeTaskInput(map[string]any{"task": "work"}, "review"); err == nil {
		t.Fatal("expected conflict error")
	}
}

func TestMergeTaskInput_DoesNotMutateCallerMap(t *testing.T) {
	inputs := map[string]any{"a": 1}
	if _, err := MergeTaskInput(inputs, "work"); err != nil {
		t.Fatalf("MergeTaskInput: %v", err)
	}
	if _, ok := inputs["task"]; ok {
		t.Error("caller's map was mutated")
	}
}
