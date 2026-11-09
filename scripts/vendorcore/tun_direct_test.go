package main

import (
	"strings"
	"testing"
)

// tunDirectBehaviorRule selects the П9 behavior rule by its anchor: the early
// "no workers" check in read_tun followed by the post-P8 outcome line.
func tunDirectBehaviorRule(t *testing.T) rule {
	t.Helper()
	var selected rule
	for _, candidate := range rulesForFile(t, "rust-client/dispatcher.rs") {
		if strings.Contains(candidate.Pattern, `workers\.load`) &&
			strings.Contains(candidate.Pattern, "let outcome") {
			selected = candidate
			break
		}
	}
	if selected.File == "" {
		t.Fatal("tun direct behavior rule not found")
	}
	return selected
}

// tunDirectModuleRule selects the module rule by its tun_direct_tests
// insertion.
func tunDirectModuleRule(t *testing.T) rule {
	t.Helper()
	var selected rule
	for _, candidate := range rulesForFile(t, "rust-client/dispatcher.rs") {
		if strings.Contains(candidate.InsertAfter, "tun_direct_tests") {
			selected = candidate
			break
		}
	}
	if selected.File == "" {
		t.Fatal("tun direct module rule not found")
	}
	return selected
}

// Representative post-P8 read_tun/read_udp text. The early check and the
// accounting are in the shape the P8 rules produce.
const tunDirectUpstream = `impl Dispatcher {
    async fn read_tun(&self) {
        if !Self::is_forwardable_packet(packet.as_slice()) {
            continue;
        }
        if self.workers.load().is_empty() {
            continue;
        }
        let outcome = self.dispatch(packet).await;
        credit_uplink(&stats, outcome, length);
    }

    async fn read_udp(&self) {
        let length = packet.len();
        let outcome = self.dispatch(packet).await;
        credit_uplink(&stats, outcome, length);
    }
}
`

const tunDirectReadUdp = `    async fn read_udp(&self) {
        let length = packet.len();
        let outcome = self.dispatch(packet).await;
        credit_uplink(&stats, outcome, length);
    }
`

func TestTunDirectBehaviorRule(t *testing.T) {
	behavior := tunDirectBehaviorRule(t)
	withRules(t, behavior)
	got, err := applyRules("rust-client/dispatcher.rs", tunDirectUpstream)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "if self.workers.load().is_empty()") {
		t.Errorf("early no-workers check is still in read_tun:\n%s", got)
	}
	if !strings.Contains(got, "П9") {
		t.Errorf("generated read_tun lacks the П9 comment:\n%s", got)
	}
	if count := strings.Count(got, "self.dispatch(packet).await"); count != 2 {
		t.Errorf("dispatch calls: %d, want 2", count)
	}
	if count := strings.Count(got, "credit_uplink(&stats, outcome, length)"); count != 2 {
		t.Errorf("credit_uplink calls: %d, want 2", count)
	}
	if !strings.Contains(got, tunDirectReadUdp) {
		t.Errorf("read_udp changed:\n%s", got)
	}
}

func TestTunDirectBehaviorRuleFailsClosedOnAnchorDrift(t *testing.T) {
	behavior := tunDirectBehaviorRule(t)
	withRules(t, behavior)
	missing := strings.Replace(tunDirectUpstream, "if self.workers.load().is_empty() {\n            continue;\n        }\n", "", 1)
	duplicated := tunDirectUpstream + tunDirectUpstream
	for name, upstream := range map[string]string{
		"missing":   missing,
		"duplicate": duplicated,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := applyRules("rust-client/dispatcher.rs", upstream); err == nil {
				t.Fatal("anchor drift was accepted")
			}
		})
	}
}

// The P8 direct-stats module block, as the module rule leaves it.
const directStatsModule = `#[cfg(test)]
#[path = "direct_stats_tests.rs"]
mod direct_stats_tests;

#[cfg(test)]
mod tests {
    use super::*;
}
`

func TestTunDirectModuleRule(t *testing.T) {
	module := tunDirectModuleRule(t)
	withRules(t, module)
	got, err := applyRules("rust-client/dispatcher.rs", directStatsModule)
	if err != nil {
		t.Fatal(err)
	}
	if count := strings.Count(got, "mod tun_direct_tests;"); count != 1 {
		t.Errorf("tun_direct_tests module: %d, want 1", count)
	}
	if !strings.Contains(got, "#[cfg(all(test, unix))]") {
		t.Errorf("tun_direct_tests module is not Unix-gated:\n%s", got)
	}
	if !strings.Contains(got, "#[cfg(test)]\n#[path = \"direct_stats_tests.rs\"]\nmod direct_stats_tests;") {
		t.Errorf("P8 module block was removed:\n%s", got)
	}
}

func TestTunDirectModuleRuleFailsClosedOnAnchorDrift(t *testing.T) {
	module := tunDirectModuleRule(t)
	withRules(t, module)
	if _, err := applyRules("rust-client/dispatcher.rs", "#[cfg(test)]\nmod tests {\n}\n"); err == nil {
		t.Fatal("missing module anchor was accepted")
	}
}
