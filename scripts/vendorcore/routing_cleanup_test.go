package main

import (
	"strings"
	"testing"
)

// routingCleanupInsertAnchor is the П11 insert anchor: the direct_ips store
// line and the immediately following closing brace of the nested
// !ip_routing_enabled block. The store line alone is a fragment of the П10
// memory-clear rule's pattern, so it must not be used on its own.
const routingCleanupInsertAnchor = `self\.direct_ips\n( +)\.store\(Arc::new\(std::collections::HashSet::new\(\)\)\);\n( +)\}`

// routingCleanupRule selects the only rule of rust-client/direct_relay.rs
// whose pattern contains anchor and checks its Count. A missing or duplicated
// anchor fails the test: the П11 rules must not drift silently.
func routingCleanupRule(t *testing.T, anchor string) rule {
	t.Helper()
	var selected []rule
	for _, candidate := range rulesForFile(t, "rust-client/direct_relay.rs") {
		if strings.Contains(candidate.Pattern, anchor) {
			selected = append(selected, candidate)
		}
	}
	if len(selected) != 1 {
		t.Fatalf("anchor %q selected %d rules, want 1", anchor, len(selected))
	}
	if selected[0].Count != 1 {
		t.Fatalf("anchor %q selects a rule with Count %d, want 1", anchor, selected[0].Count)
	}
	return selected[0]
}

// routingCleanupRules selects the two П11 rules: the removal of the live
// direct-state clears from the outer !enabled branch and their insertion into
// the nested !ip_routing_enabled block. Neither pattern may contain the П10
// anchor: the П10 Go test selects its memory-clear rule by it, and a second
// matching pattern would break that selection.
func routingCleanupRules(t *testing.T) (rule, rule) {
	t.Helper()
	remove := routingCleanupRule(t, `self\.active_tcp\.write\(\)`)
	insert := routingCleanupRule(t, routingCleanupInsertAnchor)
	if remove.Pattern == insert.Pattern {
		t.Fatal("the two П11 anchors select the same rule")
	}
	for _, selected := range []rule{remove, insert} {
		if strings.Contains(selected.Pattern, `if !config\.ip_routing_enabled`) {
			t.Fatalf("a П11 pattern contains the П10 anchor:\n%s", selected.Pattern)
		}
	}
	return remove, insert
}

func TestRoutingCleanupRulesAreSelectable(t *testing.T) {
	remove, insert := routingCleanupRules(t)
	if remove.Pattern == insert.Pattern {
		t.Fatal("the П11 rules are not distinct")
	}
}

// A representative upstream sync_domain_routing: the outer !enabled branch
// clears the domain map, the established TCP flows and the UDP sessions; the
// nested IP-routing block clears the direct addresses, and the outer branch
// returns.
const routingCleanupSyncRouting = `fn sync_domain_routing(&self, config: &crate::dispatcher::DomainRoutingConfig) {
    if !config.enabled {
        if !config.ip_routing_enabled {
            self.direct_ips
                .store(Arc::new(std::collections::HashSet::new()));
        }
        self.dns_domain_map.clear();
        if let Ok(mut guard) = self.active_tcp.write() {
            guard.clear();
        }
        if let Ok(mut guard) = self.udp_sessions.write() {
            guard.clear();
        }
        return;
    }
}
`

func TestRoutingCleanupRemoveRule(t *testing.T) {
	remove, _ := routingCleanupRules(t)
	withRules(t, remove)
	got, err := applyRules("rust-client/direct_relay.rs", routingCleanupSyncRouting)
	if err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{
		"self.active_tcp.write()",
		"self.udp_sessions.write()",
	} {
		if strings.Contains(got, gone) {
			t.Errorf("the remove rule left %q:\n%s", gone, got)
		}
	}
	for _, want := range []string{
		"self.direct_ips\n                .store(Arc::new(std::collections::HashSet::new()));",
		"self.dns_domain_map.clear();",
		"return;",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the remove rule changed %q:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "        self.dns_domain_map.clear();\n        return;") {
		t.Errorf("the remove rule left a blank line before return:\n%s", got)
	}
}

func TestRoutingCleanupRemoveRuleFailsClosedOnAnchorDrift(t *testing.T) {
	remove, _ := routingCleanupRules(t)
	withRules(t, remove)
	missing := strings.Replace(routingCleanupSyncRouting, `        if let Ok(mut guard) = self.active_tcp.write() {
            guard.clear();
        }
`, "", 1)
	duplicated := routingCleanupSyncRouting + routingCleanupSyncRouting
	for name, upstream := range map[string]string{
		"missing":   missing,
		"duplicate": duplicated,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := applyRules("rust-client/direct_relay.rs", upstream); err == nil {
				t.Fatal("anchor drift was accepted")
			}
		})
	}
}

func TestRoutingCleanupNestedRule(t *testing.T) {
	remove, insert := routingCleanupRules(t)
	withRules(t, remove, insert)
	got, err := applyRules("rust-client/direct_relay.rs", routingCleanupSyncRouting)
	if err != nil {
		t.Fatal(err)
	}

	store := "self.direct_ips\n                .store(Arc::new(std::collections::HashSet::new()));"
	active := "            if let Ok(mut guard) = self.active_tcp.write() {\n                guard.clear();\n            }"
	udp := "            if let Ok(mut guard) = self.udp_sessions.write() {\n                guard.clear();\n            }"
	for _, want := range []string{store, active, udp} {
		if count := strings.Count(got, want); count != 1 {
			t.Fatalf("%q appears %d times, want 1:\n%s", want, count, got)
		}
	}
	if !strings.Contains(got, "            // П11: domain routing off alone leaves the direct IP path on, so an\n            // update must not break established direct TCP flows or UDP sessions.") {
		t.Errorf("the П11 comment is missing or has the wrong indentation:\n%s", got)
	}
	if strings.Index(got, store) > strings.Index(got, active) || strings.Index(got, active) > strings.Index(got, udp) {
		t.Errorf("the live-state clears are not after the direct_ips store in П10/П11 order:\n%s", got)
	}
	nestedEnd := strings.Index(got, "\n        }\n        self.dns_domain_map.clear();")
	if nestedEnd < 0 {
		t.Fatalf("the nested-block closing brace or the outer branch changed:\n%s", got)
	}
	if strings.Index(got, udp) > nestedEnd {
		t.Errorf("the UDP clear is outside the nested block:\n%s", got)
	}
	returnPos := strings.Index(got, "return;")
	if returnPos < nestedEnd {
		t.Errorf("the outer return moved before the nested block:\n%s", got)
	}
	dnsPos := strings.Index(got, "self.dns_domain_map.clear();")
	if dnsPos < nestedEnd {
		t.Errorf("dns_domain_map.clear() moved inside the nested block:\n%s", got)
	}
}

func TestRoutingCleanupNestedRuleFailsClosedOnAnchorDrift(t *testing.T) {
	remove, insert := routingCleanupRules(t)
	withRules(t, remove, insert)
	missing := strings.Replace(routingCleanupSyncRouting,
		"                .store(Arc::new(std::collections::HashSet::new()));\n        }\n",
		"                .store(Arc::new(std::collections::HashSet::new()));\n", 1)
	duplicated := routingCleanupSyncRouting + routingCleanupSyncRouting
	for name, upstream := range map[string]string{
		"missing":   missing,
		"duplicate": duplicated,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := applyRules("rust-client/direct_relay.rs", upstream); err == nil {
				t.Fatal("anchor drift was accepted")
			}
		})
	}
}

// The rule order is part of the change: the П10 memory-clear rule runs after
// the two П11 rules, so its clear lands right after the direct_ips store and
// before the П11 blocks.
func TestRoutingCleanupRuleOrder(t *testing.T) {
	remove, insert := routingCleanupRules(t)
	clear := directFallbackRule(t, `if !config\.ip_routing_enabled`)
	withRules(t, remove, insert, clear)
	got, err := applyRules("rust-client/direct_relay.rs", routingCleanupSyncRouting)
	if err != nil {
		t.Fatal(err)
	}
	store := "self.direct_ips\n                .store(Arc::new(std::collections::HashSet::new()));"
	failed := "self.failed_direct_ips.write()"
	active := "self.active_tcp.write()"
	udp := "self.udp_sessions.write()"
	if count := strings.Count(got, failed); count != 1 {
		t.Fatalf("memory clears: %d, want 1:\n%s", count, got)
	}
	if !(strings.Index(got, store) < strings.Index(got, failed) &&
		strings.Index(got, failed) < strings.Index(got, active) &&
		strings.Index(got, active) < strings.Index(got, udp)) {
		t.Errorf("the rules ran in the wrong order: the П10 memory clear must follow the store and precede the П11 clears:\n%s", got)
	}
	nestedEnd := strings.Index(got, "\n        }\n        self.dns_domain_map.clear();")
	if nestedEnd < 0 || strings.Index(got, failed) > nestedEnd {
		t.Errorf("the nested block is missing or the memory clear is outside it:\n%s", got)
	}
}
