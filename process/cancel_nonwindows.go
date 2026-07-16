//go:build !windows

package process

import "os"

func cancelProcess(process *os.Process) error {
	return process.Signal(os.Interrupt)
}
