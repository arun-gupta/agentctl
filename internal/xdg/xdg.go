// Package xdg provides cross-platform user configuration directory resolution.
// It prefers $XDG_CONFIG_HOME when set so that tests can override the path
// portably on all platforms (os.UserConfigDir ignores XDG_CONFIG_HOME on macOS).
package xdg

import "os"

// UserConfigDir returns the user-level configuration directory.
// $XDG_CONFIG_HOME takes precedence over os.UserConfigDir() so that tests
// and non-Linux platforms behave consistently.
func UserConfigDir() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return dir
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return dir
}
