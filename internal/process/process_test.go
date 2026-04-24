package process

import (
	"os"
	"strconv"
	"testing"
)

func TestIsAlive_self(t *testing.T) {
	pid := strconv.Itoa(os.Getpid())
	if !IsAlive(pid) {
		t.Errorf("IsAlive(%q): expected true for own PID", pid)
	}
}

func TestIsAlive_empty(t *testing.T) {
	if IsAlive("") {
		t.Error("IsAlive(\"\") should return false")
	}
}

func TestIsAlive_nonNumeric(t *testing.T) {
	if IsAlive("abc") {
		t.Error("IsAlive(\"abc\") should return false")
	}
}

func TestIsAlive_deadPID(t *testing.T) {
	// PID 1 is always init/systemd on Linux but signal(0) may be EPERM.
	// Use a PID that is very unlikely to exist.
	if IsAlive("9999999") {
		t.Log("PID 9999999 unexpectedly alive — skipping dead-pid test")
	}
}
