//go:build !windows

package main

// shortPath — коротких путей 8.3 вне Windows нет.
func shortPath(string) string { return "" }
