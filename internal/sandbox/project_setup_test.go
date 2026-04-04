package sandbox

import (
	"context"
	"fmt"
	"testing"
)

func TestRunProjectSetupSkipsCommandsWhenVerifyPasses(t *testing.T) {
	var ran []string
	err := RunProjectSetup(context.Background(), ProjectSetup{
		Commands: []string{"bun install"},
		Verify:   []string{"test -d node_modules"},
	}, func(_ context.Context, command string) (ExecResult, error) {
		ran = append(ran, command)
		return ExecResult{ExitCode: 0}, nil
	}, nil)
	if err != nil {
		t.Fatalf("RunProjectSetup: %v", err)
	}
	if len(ran) != 1 || ran[0] != "test -d node_modules" {
		t.Fatalf("ran = %#v", ran)
	}
}

func TestRunProjectSetupRunsCommandsThenVerifies(t *testing.T) {
	runs := 0
	err := RunProjectSetup(context.Background(), ProjectSetup{
		Commands: []string{"bun install", "bun run build"},
		Verify:   []string{"test -d node_modules"},
	}, func(_ context.Context, command string) (ExecResult, error) {
		runs++
		if command == "test -d node_modules" && runs == 1 {
			return ExecResult{ExitCode: 1}, nil
		}
		return ExecResult{ExitCode: 0}, nil
	}, nil)
	if err != nil {
		t.Fatalf("RunProjectSetup: %v", err)
	}
}

func TestRunProjectSetupFailsWhenVerifyStillFails(t *testing.T) {
	err := RunProjectSetup(context.Background(), ProjectSetup{
		Commands: []string{"bun install"},
		Verify:   []string{"test -d node_modules"},
	}, func(_ context.Context, command string) (ExecResult, error) {
		if command == "test -d node_modules" {
			return ExecResult{ExitCode: 1}, nil
		}
		return ExecResult{ExitCode: 0}, nil
	}, nil)
	if err == nil {
		t.Fatal("expected verify failure")
	}
}

func TestRunProjectSetupFailsOnCommandError(t *testing.T) {
	err := RunProjectSetup(context.Background(), ProjectSetup{
		Commands: []string{"bun install"},
	}, func(_ context.Context, command string) (ExecResult, error) {
		return ExecResult{}, fmt.Errorf("boom: %s", command)
	}, nil)
	if err == nil {
		t.Fatal("expected command error")
	}
}
