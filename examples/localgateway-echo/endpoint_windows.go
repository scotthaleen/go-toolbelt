//go:build windows

package main

func echoEndpoint() (string, error) {
	return `\\.\pipe\go-toolbelt-localgateway-echo`, nil
}
