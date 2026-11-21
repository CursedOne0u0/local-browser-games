//go:build !windows

package update

import (
	"os"
	"syscall"
)

// execSelf replaces this process image with exe. The PID survives, so a
// launcher that started us (and traps kill on that PID) keeps working.
func execSelf(exe string, args []string) error {
	argv := append([]string{exe}, args...)
	return syscall.Exec(exe, argv, append(os.Environ(), "GAMESERVER_UPDATED=1"))
}
