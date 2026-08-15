//go:build linux || darwin

package localgateway

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

type peerListener struct{ net.Listener }

func (l peerListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		allowed, peerErr := peerAllowed(conn)
		if peerErr == nil && allowed {
			return conn, nil
		}
		_ = conn.Close()
	}
}

func listenLocal(endpoint string, _, _ int32) (net.Listener, func() error, error) {
	if !filepath.IsAbs(endpoint) {
		return nil, nil, errors.New("Unix gateway endpoint must be an absolute path")
	}
	if err := validateEndpointParent(filepath.Dir(endpoint)); err != nil {
		return nil, nil, err
	}
	if err := removeStaleSocket(endpoint); err != nil {
		return nil, nil, err
	}
	listener, err := net.Listen("unix", endpoint)
	if err != nil {
		return nil, nil, err
	}
	unixListener, ok := listener.(*net.UnixListener)
	if !ok {
		_ = listener.Close()
		_ = os.Remove(endpoint)
		return nil, nil, errors.New("Unix gateway listener type is unavailable")
	}
	unixListener.SetUnlinkOnClose(false)
	if err := os.Chmod(endpoint, 0o600); err != nil {
		_ = listener.Close()
		_ = os.Remove(endpoint)
		return nil, nil, fmt.Errorf("protect Unix gateway endpoint: %w", err)
	}
	if err := validateSocket(endpoint); err != nil {
		_ = listener.Close()
		_ = os.Remove(endpoint)
		return nil, nil, err
	}
	identity, err := socketIdentity(endpoint)
	if err != nil {
		_ = listener.Close()
		_ = os.Remove(endpoint)
		return nil, nil, err
	}
	cleanup := func() error { return removeOwnedSocket(endpoint, identity) }
	return peerListener{Listener: listener}, cleanup, nil
}

func removeStaleSocket(endpoint string) error {
	if err := validateSocket(endpoint); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("refuse unsafe existing gateway endpoint: %w", err)
	}
	identity, err := socketIdentity(endpoint)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", endpoint)
	if err == nil {
		_ = conn.Close()
		return errors.New("local gateway endpoint is already active")
	}
	if !errors.Is(err, syscall.ECONNREFUSED) {
		return fmt.Errorf("cannot prove gateway endpoint is stale: %w", err)
	}
	if err := removeOwnedSocket(endpoint, identity); err != nil {
		return fmt.Errorf("remove stale gateway endpoint: %w", err)
	}
	return nil
}

func validateEndpointParent(parent string) error {
	info, err := os.Stat(parent)
	if err != nil {
		return fmt.Errorf("inspect gateway endpoint parent: %w", err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || stat.Uid != uint32(os.Geteuid()) || info.Mode().Perm()&0o022 != 0 {
		return errors.New("gateway endpoint parent must be an effective-user-owned non-writable directory")
	}
	return nil
}

func validateSocket(endpoint string) error {
	info, err := os.Lstat(endpoint)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSocket == 0 || info.Mode().Perm()&0o077 != 0 {
		return errors.New("gateway endpoint is not a private Unix socket")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) {
		return errors.New("gateway endpoint is not owned by the effective user")
	}
	return nil
}

type unixSocketIdentity struct{ device, inode uint64 }

func socketIdentity(endpoint string) (unixSocketIdentity, error) {
	info, err := os.Lstat(endpoint)
	if err != nil {
		return unixSocketIdentity{}, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return unixSocketIdentity{}, errors.New("gateway endpoint identity is unavailable")
	}
	return unixSocketIdentity{device: uint64(stat.Dev), inode: uint64(stat.Ino)}, nil
}

func removeOwnedSocket(endpoint string, expected unixSocketIdentity) error {
	quarantine, err := cleanupPath(endpoint)
	if err != nil {
		return err
	}
	if err := os.Rename(endpoint, quarantine); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	actual, err := socketIdentity(quarantine)
	if err != nil {
		return errors.Join(err, restoreQuarantinedSocket(endpoint, quarantine))
	}
	if actual != expected {
		return errors.Join(errors.New("gateway endpoint identity changed; refusing cleanup"), restoreQuarantinedSocket(endpoint, quarantine))
	}
	return os.Remove(quarantine)
}

func cleanupPath(endpoint string) (string, error) {
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return "", fmt.Errorf("create gateway cleanup identity: %w", err)
	}
	return endpoint + ".cleanup-" + hex.EncodeToString(suffix[:]), nil
}

func restoreQuarantinedSocket(endpoint, quarantine string) error {
	if _, err := os.Lstat(endpoint); err == nil {
		return errors.New("gateway endpoint was replaced during cleanup; quarantined socket was retained")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return restoreSocketNoReplace(quarantine, endpoint)
}

func dialLocal(ctx context.Context, endpoint string) (net.Conn, error) {
	if err := validateSocket(endpoint); err != nil {
		return nil, fmt.Errorf("validate Unix gateway endpoint: %w", err)
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", endpoint)
	if err != nil {
		return nil, err
	}
	allowed, peerErr := peerAllowed(conn)
	if peerErr != nil || !allowed {
		_ = conn.Close()
		if peerErr != nil {
			return nil, fmt.Errorf("verify Unix gateway peer: %w", peerErr)
		}
		return nil, errors.New("Unix gateway peer belongs to another user")
	}
	return conn, nil
}
