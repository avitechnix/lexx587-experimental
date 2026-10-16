//go:build !unix

package main

// acquireLock is a no-op where flock isn't available; don't run two
// applying agents at once on these platforms.
func acquireLock(string) (func(), error) { return func() {}, nil }
