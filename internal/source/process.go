package source

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// configureProcess gives the source adapter ownership of all extractor helpers.
// The application deployment target is linux/amd64.
func configureProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.WaitDelay = time.Second
}
