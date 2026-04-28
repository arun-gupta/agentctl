// Package notify provides native desktop notifications for agentctl events.
// Notifications are fire-and-forget: errors are silently dropped so that CI
// environments (where osascript / notify-send are absent) are unaffected.
package notify

import (
	"fmt"
	"os/exec"
	"runtime"
)

// SendFn is the notification backend called by Send. It can be replaced in
// tests to capture or suppress notifications without requiring OS tools.
var SendFn = defaultSend

// Send fires a native desktop notification with the given title and message.
// On unsupported platforms or when the required tool is unavailable the call
// is a no-op.
func Send(title, message string) {
	SendFn(title, message)
}

// defaultSend is the production notification sender.
func defaultSend(title, message string) {
	switch runtime.GOOS {
	case "darwin":
		// osascript display notification: message first, then title.
		script := fmt.Sprintf("display notification %q with title %q", message, title)
		_ = exec.Command("osascript", "-e", script).Run()
	case "linux":
		_ = exec.Command("notify-send", title, message).Run()
	}
}
