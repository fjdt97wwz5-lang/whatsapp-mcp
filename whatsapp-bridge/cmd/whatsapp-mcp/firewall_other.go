//go:build !windows

package main

func allowFirewall(exe string, port int) error { return nil }

func removeFirewall() {}
