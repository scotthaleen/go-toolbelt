//go:build !linux && !darwin && !windows

package processlock

func acquire(string) (lockHandle, error) {
	return nil, ErrUnsupported
}
