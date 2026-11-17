// Package version carries the build's version, stamped in at link time.
package version

// Version is set by the build: -ldflags "-X .../internal/version.Version=0.1.7".
var Version = "dev"
