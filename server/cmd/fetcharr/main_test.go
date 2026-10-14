package main

import "testing"

func TestRunExitsCleanly(t *testing.T) {
	if code := run([]string{"--config", "x.yaml"}); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if code := run([]string{"--bogus"}); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
}
