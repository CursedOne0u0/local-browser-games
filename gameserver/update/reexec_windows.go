//go:build windows

package update

import "errors"

// execSelf cannot replace a running image on Windows; the caller falls back to
// starting the new build as a child process and waiting on it.
func execSelf(string, []string) error {
	return errors.New("process replacement is unsupported on windows")
}
