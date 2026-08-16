//go:build !linux && !darwin && !windows

package privatedir

func ensure(string) error   { return ErrUnsupported }
func validate(string) error { return ErrUnsupported }
