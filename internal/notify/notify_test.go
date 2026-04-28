package notify_test

import (
	"testing"

	"github.com/arun-gupta/agentctl/internal/notify"
)

// TestSend_doesNotPanic verifies that Send does not panic on any supported
// platform, even when the notification tool is unavailable (e.g. in CI).
func TestSend_doesNotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Send panicked: %v", r)
		}
	}()
	notify.Send("agentctl", "test notification — ignore")
}

// TestSendFn_override verifies that replacing SendFn intercepts the call.
func TestSendFn_override(t *testing.T) {
	orig := notify.SendFn
	t.Cleanup(func() { notify.SendFn = orig })

	var gotTitle, gotMessage string
	notify.SendFn = func(title, message string) {
		gotTitle = title
		gotMessage = message
	}

	notify.Send("my title", "my message")

	if gotTitle != "my title" {
		t.Errorf("title = %q, want %q", gotTitle, "my title")
	}
	if gotMessage != "my message" {
		t.Errorf("message = %q, want %q", gotMessage, "my message")
	}
}
