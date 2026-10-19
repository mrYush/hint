package config

import (
	"errors"
	"fmt"
	"strings"

	gokeyring "github.com/zalando/go-keyring"
)

// Keyring is the OS secret store as the loader and setup use it: one
// secret per account under hint's own service name. The loader reads a
// profile whose api_key says keyring:<account> through it; setup writes
// there. Tests hand in a map; the process hands in [OSKeyring].
type Keyring interface {
	Get(account string) (string, error)
	Set(account, secret string) error
	Delete(account string) error
}

// ErrKeyringNotFound reports an account the store has no secret for.
var ErrKeyringNotFound = errors.New("config: no such entry in the secret store")

// KeyringService is the service name hint's entries are filed under.
const KeyringService = "hint"

// keyringScheme is the api_key prefix that names a secret-store entry.
const keyringScheme = "keyring:"

// KeyringRef returns the account an api_key value refers to, and whether
// it is a reference at all. An empty account means the profile's name.
func KeyringRef(value string) (account string, ok bool) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(value), keyringScheme)
	if !ok {
		return "", false
	}
	return strings.TrimSpace(rest), true
}

// KeyringValue is the api_key value that refers to account.
func KeyringValue(account string) string { return keyringScheme + account }

// OSKeyring is the process's secret store: the Secret Service over D-Bus
// on Linux, the Keychain on macOS, the Credential Manager on Windows —
// what github.com/zalando/go-keyring reaches without cgo.
type OSKeyring struct{}

// Get implements Keyring.
func (OSKeyring) Get(account string) (string, error) {
	s, err := gokeyring.Get(KeyringService, account)
	if errors.Is(err, gokeyring.ErrNotFound) {
		return "", fmt.Errorf("%w: %s/%s", ErrKeyringNotFound, KeyringService, account)
	}
	if err != nil {
		return "", fmt.Errorf("config: secret store: %w", err)
	}
	return s, nil
}

// Set implements Keyring.
func (OSKeyring) Set(account, secret string) error {
	if err := gokeyring.Set(KeyringService, account, secret); err != nil {
		return fmt.Errorf("config: secret store: %w", err)
	}
	return nil
}

// Delete implements Keyring.
func (OSKeyring) Delete(account string) error {
	err := gokeyring.Delete(KeyringService, account)
	if errors.Is(err, gokeyring.ErrNotFound) {
		return fmt.Errorf("%w: %s/%s", ErrKeyringNotFound, KeyringService, account)
	}
	if err != nil {
		return fmt.Errorf("config: secret store: %w", err)
	}
	return nil
}

// KeyringAvailable reports whether k answers at all: a store that has no
// entry for a probe account is reachable, one that fails to answer is
// not (no Secret Service on a headless box, no D-Bus session).
func KeyringAvailable(k Keyring) bool {
	if k == nil {
		return false
	}
	_, err := k.Get("hint-probe")
	return err == nil || errors.Is(err, ErrKeyringNotFound)
}

// MapKeyring is a Keyring over a map, for tests and for a process that
// must not touch the real store.
type MapKeyring map[string]string

// Get implements Keyring.
func (m MapKeyring) Get(account string) (string, error) {
	s, ok := m[account]
	if !ok {
		return "", fmt.Errorf("%w: %s/%s", ErrKeyringNotFound, KeyringService, account)
	}
	return s, nil
}

// Set implements Keyring.
func (m MapKeyring) Set(account, secret string) error { m[account] = secret; return nil }

// Delete implements Keyring.
func (m MapKeyring) Delete(account string) error {
	if _, ok := m[account]; !ok {
		return fmt.Errorf("%w: %s/%s", ErrKeyringNotFound, KeyringService, account)
	}
	delete(m, account)
	return nil
}
