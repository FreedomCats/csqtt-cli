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
	`let active_tcp = Arc::new`,
	`let active_tcp_cleanup = active_tcp`,
	`let domain = self\.dns_domain_map\.get`,
	`if !config\.ip_routing_enabled`,
	`active_tcp_cleanup\.write`,
}

// TestDirectFallbackRulesAreSelectable exercises every anchor: each selects
// exactly one rule with Count 1, and no two anchors select the same rule, so
// all sixteen П10 rules are pinned.
func TestDirectFallbackRulesAreSelectable(t *testing.T) {
	seen := map[string]string{}
	for _, anchor := range directFallbackAnchors {
		selected := directFallbackRule(t, anchor)
		if previous, ok := seen[selected.Pattern]; ok {
			t.Fatalf("anchors %q and %q select the same rule", previous, anchor)
		}
		seen[selected.Pattern] = anchor
	}
	if len(seen) != 16 {
		t.Fatalf("selected %d distinct П10 rules, want 16", len(seen))
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
		"&abort_failed_direct_ips_conn,",
		"FAILED_DIRECT_MEMORY_TTL,",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("generated listener lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "remember_failed_direct(") {
		t.Errorf("the listener records the failed address; only the handler must:\n%s", got)
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

// A representative routing decision: the established-flow branch is above,
// the domain lookup is followed by evaluate_route.
const directFallbackRoute = `fn try_handle(&self, packet: &PacketBuf) -> bool {
    let dst_ip = packet.dst_ip();
    let domain = self.dns_domain_map.get(&dst_ip);
    let verdict = crate::sni::evaluate_route(
        &dst_ip,
        domain.as_deref(),
    );
    matches!(verdict, crate::sni::RouteVerdict::Direct)
}
`

func TestDirectFallbackMemoryCheckRule(t *testing.T) {
	memory := directFallbackRule(t, `let domain = self\.dns_domain_map\.get`)
	withRules(t, memory)
	got, err := applyRules("rust-client/direct_relay.rs", directFallbackRoute)
	if err != nil {
		t.Fatal(err)
	}
	check := "failed_direct_active(&self.failed_direct_ips, dst_ip, std::time::Instant::now())"
	if count := strings.Count(got, check); count != 1 {
		t.Fatalf("memory checks: %d, want 1:\n%s", count, got)
	}
	if !strings.Contains(got, "if protocol == 6") {
		t.Errorf("the check is not guarded by the TCP protocol:\n%s", got)
	}
	if !strings.Contains(got, "return false;") {
		t.Errorf("the check does not reject the packet:\n%s", got)
	}
	if strings.Index(got, check) > strings.Index(got, "evaluate_route(") {
		t.Errorf("the memory check is after evaluate_route:\n%s", got)
	}
	if strings.Index(got, "let domain =") < strings.Index(got, check) {
		t.Errorf("the memory check is after the domain lookup:\n%s", got)
	}
}

func TestDirectFallbackMemoryCheckRuleFailsClosedOnAnchorDrift(t *testing.T) {
	memory := directFallbackRule(t, `let domain = self\.dns_domain_map\.get`)
	withRules(t, memory)
	missing := strings.Replace(directFallbackRoute, "    let domain = self.dns_domain_map.get(&dst_ip);\n", "", 1)
	duplicated := directFallbackRoute + directFallbackRoute
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

// A representative routing-configuration update: the nested IP-routing block
// holds the direct-address clear.
const directFallbackSyncRouting = `fn sync_domain_routing(&self, config: &crate::dispatcher::DomainRoutingConfig) {
    if !config.enabled {
        if !config.ip_routing_enabled {
            self.direct_ips
                .store(Arc::new(std::collections::HashSet::new()));
        }
        self.dns_domain_map.clear();
        return;
    }
}
`

func TestDirectFallbackMemoryClearRule(t *testing.T) {
	clear := directFallbackRule(t, `if !config\.ip_routing_enabled`)
	withRules(t, clear)
	got, err := applyRules("rust-client/direct_relay.rs", directFallbackSyncRouting)
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(got, "if !config.ip_routing_enabled {")
	end := strings.Index(got, "self.dns_domain_map.clear();")
	if start < 0 || end < start {
		t.Fatalf("the nested routing block is missing:\n%s", got)
	}
	block := got[start:end]
	if count := strings.Count(block, "self.failed_direct_ips.write()"); count != 1 {
		t.Fatalf("memory clears in the nested block: %d, want 1:\n%s", count, got)
	}
	if !strings.Contains(block, "guard.clear();") {
		t.Errorf("the memory clear lacks guard.clear():\n%s", got)
	}
	clearPos := strings.Index(block, "self.failed_direct_ips.write()")
	depth, nestedEnd := 0, -1
	for i := 0; i < len(block) && nestedEnd < 0; i++ {
		switch block[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				nestedEnd = i
			}
		}
	}
	if nestedEnd < 0 || clearPos > nestedEnd {
		t.Errorf("the memory clear is outside the nested IP-routing block:\n%s", got)
	}
	if clearPos < strings.Index(block, "self.direct_ips") {
		t.Errorf("the memory clear is not next to the direct addresses:\n%s", got)
	}
}

func TestDirectFallbackMemoryClearRuleFailsClosedOnAnchorDrift(t *testing.T) {
	clear := directFallbackRule(t, `if !config\.ip_routing_enabled`)
	withRules(t, clear)
	missing := strings.Replace(directFallbackSyncRouting, "        if !config.ip_routing_enabled {\n", "        if config.ip_routing_enabled {\n", 1)
	duplicated := directFallbackSyncRouting + directFallbackSyncRouting
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

// A representative cleanup task: the 30-second tick prunes active_tcp.
const directFallbackCleanup = `    let active_tcp_cleanup = active_tcp.clone();
    let shutdown_cleanup = shutdown.clone();
    tokio::spawn(async move {
        let mut interval = tokio::time::interval(Duration::from_secs(30));
        loop {
            tokio::select! {
                _ = shutdown_cleanup.cancelled() => break,
                _ = interval.tick() => {}
            }
            let now = std::time::Instant::now();
            if let Ok(mut guard) = active_tcp_cleanup.write() {
                guard.retain(|_, last_seen| {
                    now.duration_since(*last_seen) < Duration::from_secs(120)
                });
            }
        }
    });
`

func TestDirectFallbackCleanupRules(t *testing.T) {
	clone := directFallbackRule(t, `let active_tcp_cleanup = active_tcp`)
	prune := directFallbackRule(t, `active_tcp_cleanup\.write`)
	withRules(t, clone, prune)
	got, err := applyRules("rust-client/direct_relay.rs", directFallbackCleanup)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"let failed_direct_cleanup = failed_direct_ips.clone();",
		"if let Ok(mut guard) = failed_direct_cleanup.write() {",
		"guard.retain(|_, deadline| *deadline > now);",
	} {
		if count := strings.Count(got, want); count != 1 {
			t.Fatalf("%q appears %d times, want 1:\n%s", want, count, got)
		}
	}
	if strings.Index(got, "guard.retain(|_, deadline| *deadline > now);") < strings.Index(got, "now.duration_since(*last_seen)") {
		t.Errorf("the failed-address prune is before the active_tcp retain:\n%s", got)
	}
}

func TestDirectFallbackCleanupRulesFailClosedOnAnchorDrift(t *testing.T) {
	clone := directFallbackRule(t, `let active_tcp_cleanup = active_tcp`)
	prune := directFallbackRule(t, `active_tcp_cleanup\.write`)
	withRules(t, clone, prune)
	missingClone := strings.Replace(directFallbackCleanup, "let active_tcp_cleanup = active_tcp.clone();", "let cleanup = active_tcp.clone();", 1)
	missingPrune := strings.Replace(directFallbackCleanup, "Duration::from_secs(120)", "Duration::from_secs(60)", 1)
	duplicated := directFallbackCleanup + directFallbackCleanup
	for name, upstream := range map[string]string{
		"missing clone": missingClone,
		"missing prune": missingPrune,
		"duplicate":     duplicated,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := applyRules("rust-client/direct_relay.rs", upstream); err == nil {
				t.Fatal("anchor drift was accepted")
			}
		})
	}
}

// A representative packet builder: the П10 helpers are inserted before it.
const directFallbackCraftPacket = `pub fn craft_tcp_packet_into(
    src: SocketAddr,
    dst: SocketAddr,
    seq: u32,
    ack: u32,
    flags: u8,
    payload: &[u8],
    buf: &mut [u8],
) -> bool {
    false
}
`

func TestDirectFallbackHandlerCodeRule(t *testing.T) {
	handler := directFallbackRule(t, `pub fn craft_tcp_packet_into`)
	withRules(t, handler)
	got, err := applyRules("rust-client/direct_relay.rs", directFallbackCraftPacket)
	if err != nil {
		t.Fatal(err)
	}
	if count := strings.Count(got, "remember_failed_direct(failed, key.1.ip()"); count != 1 {
		t.Fatalf("recording calls at the handler entry: %d, want 1:\n%s", count, got)
	}
	for _, want := range []string{
		"fn remember_failed_direct(",
		"fn failed_direct_active(",
		"failed: &RwLock<HashMap<IpAddr, std::time::Instant>>",
		"// П10: the address is remembered even when no SYN-ACK record exists and",
		"let seq = wait_for_syn_ack_seq(map, key, wait).await;",
		"build_tcp_packet(key.1, key.0, seq.wrapping_add(1), 0, 0x04, &[])",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("generated handler code lacks %q:\n%s", want, got)
		}
	}
}
