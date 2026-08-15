//go:build linux || darwin

package main

import (
	"os"
	"path/filepath"
)

func echoEndpoint() (string, error) {
	directory, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	directory = filepath.Join(directory, "go-toolbelt", "localgateway-echo")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(directory, "gateway.sock"), nil
}
