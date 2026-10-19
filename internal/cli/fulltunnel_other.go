//go:build !linux

package cli

import "github.com/FreedomCats/csqtt-cli/internal/netguard"

// reversePathProblem — обратного фильтра, который мешал бы привязке
// IP_UNICAST_IF, на Windows нет. См. fulltunnel_linux.go.
func reversePathProblem(netguard.Choice) (string, error) { return "", nil }

// prepareSystemDNS — DNS туннеля на Windows — свойство адаптера, остатков в
// системе он не оставляет. См. fulltunnel_linux.go.
var prepareSystemDNS = func(*printer) error { return nil }
