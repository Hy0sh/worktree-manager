package main

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"github.com/Hy0sh/worktree-manager/internal/execx"
)

func TestReportErrorSaysWhereDockerComesFrom(t *testing.T) {
	down := &execx.Error{
		Cmd: "docker compose -p app-wt-3 down",
		Err: &exec.Error{Name: "docker", Err: exec.ErrNotFound},
	}
	var out strings.Builder
	reportError(&out, fmt.Errorf("stopping the stack: %w", down))
	if !strings.HasPrefix(out.String(), "error: stopping the stack: `docker compose -p app-wt-3 down` failed:") {
		t.Fatalf("the failure itself must come first, got %q", out.String())
	}
	if !strings.Contains(out.String(), "OrbStack") {
		t.Fatalf("a missing docker should say where it comes from, got %q", out.String())
	}
}

func TestReportErrorLeavesOtherFailuresAlone(t *testing.T) {
	var out strings.Builder
	reportError(&out, fmt.Errorf("worktree 3 is locked"))
	if out.String() != "error: worktree 3 is locked\n" {
		t.Fatalf("out = %q", out.String())
	}
}
