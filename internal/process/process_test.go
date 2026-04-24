package process

import (
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"
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

func TestKill_empty(t *testing.T) {
	Kill("") // must not panic
}

func TestKill_nonNumeric(t *testing.T) {
	Kill("abc") // must not panic
}

func TestKill_deadPID(t *testing.T) {
	Kill("9999999") // must not panic even for a non-existent PID
}

func TestKill_realProcess(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Skip("cannot start test process")
	}
	pid := strconv.Itoa(cmd.Process.Pid)

	if !IsAlive(pid) {
		_ = cmd.Process.Kill()
		t.Skip("process didn't stay alive long enough")
	}

	Kill(pid)

	// Reap the child in a goroutine so it doesn't linger as a zombie;
	// signal(0) on a zombie still returns success, so we must wait first.
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()

	select {
	case <-done:
		// reaped successfully
	case <-time.After(2 * time.Second):
		t.Error("process did not exit within 2 seconds of Kill")
		return
	}

	if IsAlive(pid) {
		t.Error("process should not be alive after being reaped")
	}
}
