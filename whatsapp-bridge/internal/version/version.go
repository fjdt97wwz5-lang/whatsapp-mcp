// Package version holds the build version of the connector.
package version

// Version is overridden at build time with -ldflags "-X .../version.Version=x.y.z".
var Version = "2.0.0-dev"
