//go:build !linux && !darwin && !windows

package main

import "errors"

func echoEndpoint() (string, error) {
	return "", errors.New("local gateway echo is unsupported on this platform")
}
