package main

import (
	"strings"
	"testing"
)

// directFallbackRule selects the only П10 rule of rust-client/direct_relay.rs
// whose pattern contains anchor and checks its Count. A missing or duplicated
// anchor fails the test: a future upstream change must not silently drop a
// rule.
func directFallbackRule(t *testing.T, anchor string) rule {
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

// directFallbackAnchors pins every П10 rule of rust-client/direct_relay.rs by
// a unique fragment of its pattern. The module rule must follow the П3
// bind_if_tests insertion, so its anchor is the generated block, not mod tests.
var directFallbackAnchors = []string{
	"DIRECT_RELAY_INBOUND_CAPACITY",
	`active_tcp: Arc<RwLock<HashMap`,
	`active_tcp,$`,
	`pool_egress = pool`,
	`pool_egress\.try_acquire`,
	`mut listener\) = tcp_listener`,
	`active_tcp_conn = active_tcp_c`,
	`drop\(server_stream\)`,
	`active_tcp_conn\.write`,
	`pub fn craft_tcp_packet_into`,
	`direct_relay_bind_if_tests`,
}

// TestDirectFallbackRulesAreSelectable exercises every anchor: each selects
// exactly one rule with Count 1, and no two anchors select the same rule, so
// all eleven П10 rules are pinned.
func TestDirectFallbackRulesAreSelectable(t *testing.T) {
	seen := map[string]string{}
	for _, anchor := range directFallbackAnchors {
		selected := directFallbackRule(t, anchor)
		if previous, ok := seen[selected.Pattern]; ok {
			t.Fatalf("anchors %q and %q select the same rule", previous, anchor)
		}
		seen[selected.Pattern] = anchor
	}
	if len(seen) != 11 {
		t.Fatalf("selected %d distinct П10 rules, want 11", len(seen))
	}
}

// A representative post-П9 listener: the success branch, its socket options
// and copy loop, the success branch tail and both drops.
const directFallbackListener = `tokio::spawn(async move {
    let connect_res = tokio::time::timeout(
        Duration::from_secs(5),
        crate::bind_if::connect_tcp(remote_addr),
    )
    .await;

    if let Ok(Ok(mut server_stream)) = connect_res {
        let _ = server_stream.set_nodelay(true);
        let _ = server_stream.set_recv_buffer_size(262144);
        let _ = tokio::io::copy_bidirectional_with_sizes(
            &mut client_stream,
            &mut server_stream,
            65536,
            65536,
        )
        .await;

        drop(server_stream);
    }

    drop(client_stream);
    if let Ok(mut guard) = active_tcp_conn.write() {
        guard.remove(&key);
    }
});
`

func TestDirectFallbackElseRule(t *testing.T) {
	elseRule := directFallbackRule(t, `drop\(server_stream\)`)
	withRules(t, elseRule)
	got, err := applyRules("rust-client/direct_relay.rs", directFallbackListener)
	if err != nil {
		t.Fatal(err)
	}
	if count := strings.Count(got, "} else {"); count != 1 {
		t.Errorf("else branches: %d, want 1", count)
	}
	if count := strings.Count(got, "abort_failed_direct_tcp("); count != 1 {
		t.Errorf("failure handler calls: %d, want 1", count)
	}
	for _, want := range []string{
		"drop(server_stream);",
		"drop(client_stream);",
		"if let Ok(Ok(mut server_stream)) = connect_res {",
		"copy_bidirectional_with_sizes(",
		"&abort_pool_conn,",
		"&abort_inbound_tx_conn,",
		"&abort_syn_ack_seqs_conn,",
		"SYN_ACK_RECORD_WAIT,",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("generated listener lacks %q:\n%s", want, got)
		}
	}
	if strings.Index(got, "} else {") > strings.Index(got, "abort_failed_direct_tcp(") {
		t.Errorf("the handler call is outside the else branch:\n%s", got)
	}
}

func TestDirectFallbackElseRuleFailsClosedOnAnchorDrift(t *testing.T) {
	elseRule := directFallbackRule(t, `drop\(server_stream\)`)
	withRules(t, elseRule)
	missing := strings.Replace(directFallbackListener, "drop(server_stream);\n    }\n\n", "", 1)
	duplicated := directFallbackListener + directFallbackListener
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

// A representative egress frame path: the frame is parsed, then the pool is
// asked for a buffer and the frame is forwarded.
const directFallbackEgress = `tokio::spawn(async move {
    let frame = next_frame;
    let Some(mut pkt) = pool_egress.try_acquire() else {
        continue;
    };
    if let Ok(dest) = pkt.extend_tail(frame.len()) {
        dest.copy_from_slice(&frame);
        if inbound_tx_egress.send(pkt).await.is_err() {
            break;
        }
    }
});
`

func TestDirectFallbackEgressRule(t *testing.T) {
	egress := directFallbackRule(t, `pool_egress\.try_acquire`)
	withRules(t, egress)
	got, err := applyRules("rust-client/direct_relay.rs", directFallbackEgress)
	if err != nil {
		t.Fatal(err)
	}
	record := "record_syn_ack(&syn_ack_seqs_egress, &frame);"
	if count := strings.Count(got, record); count != 1 {
		t.Fatalf("record calls: %d, want 1", count)
	}
	if strings.Index(got, record) > strings.Index(got, "pool_egress.try_acquire()") {
		t.Errorf("the record is after the pool check:\n%s", got)
	}
	for _, want := range []string{
		"pkt.extend_tail(frame.len())",
		"dest.copy_from_slice(&frame);",
		"inbound_tx_egress.send(pkt).await.is_err()",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the forwarding path was changed, lacks %q:\n%s", want, got)
		}
	}
}

func TestDirectFallbackEgressRuleFailsClosedOnAnchorDrift(t *testing.T) {
	egress := directFallbackRule(t, `pool_egress\.try_acquire`)
	withRules(t, egress)
	missing := strings.Replace(directFallbackEgress, "let Some(mut pkt) = pool_egress.try_acquire() else {", "let Some(mut pkt) = pool_egress.acquire() else {", 1)
	duplicated := directFallbackEgress + directFallbackEgress
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

// The П3 bind_if_tests block, as the module rule leaves it.
const directFallbackModules = `#[cfg(all(test, target_os = "linux"))]
#[path = "direct_relay_bind_if_tests.rs"]
mod bind_if_tests;

#[cfg(test)]
mod tests {
    use super::*;
}
`

func TestDirectFallbackModuleRule(t *testing.T) {
	module := directFallbackRule(t, `direct_relay_bind_if_tests`)
	withRules(t, module)
	got, err := applyRules("rust-client/direct_relay.rs", directFallbackModules)
	if err != nil {
		t.Fatal(err)
	}
	block := "#[cfg(test)]\n#[path = \"direct_relay_failure_tests.rs\"]\nmod failure_tests;"
	if count := strings.Count(got, block); count != 1 {
		t.Fatalf("failure_tests module blocks: %d, want 1:\n%s", count, got)
	}
	if !strings.Contains(got, "#[cfg(all(test, target_os = \"linux\"))]\n#[path = \"direct_relay_bind_if_tests.rs\"]\nmod bind_if_tests;") {
		t.Errorf("the П3 bind_if_tests block was removed:\n%s", got)
	}
}

func TestDirectFallbackModuleRuleFailsClosedOnAnchorDrift(t *testing.T) {
	module := directFallbackRule(t, `direct_relay_bind_if_tests`)
	withRules(t, module)
	// mod tests alone is the anchor of another rule; the module rule must not
	// match it.
	if _, err := applyRules("rust-client/direct_relay.rs", "#[cfg(test)]\nmod tests {\n}\n"); err == nil {
		t.Fatal("missing module anchor was accepted")
	}
}
