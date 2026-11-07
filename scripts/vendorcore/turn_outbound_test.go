package main

import (
	"strings"
	"testing"
)

func rulesForFile(t *testing.T, name string) []rule {
	t.Helper()
	var selected []rule
	for _, candidate := range rules {
		if candidate.File == name {
			selected = append(selected, candidate)
		}
	}
	if len(selected) == 0 {
		t.Fatalf("no rules found for %s", name)
	}
	return selected
}

func outboundTurnRules(t *testing.T) []rule {
	t.Helper()
	anchors := []string{
		`queue: ArrayQueue<PacketBuf>`,
		`socket: Arc<UdpSocket>,`,
		`async fn outbound_writer_loop`,
		`socket: socket\.clone\(\),`,
		`while let Some\(mut packet\) = queue\.queue\.pop`,
		`match queue\.queue\.push`,
		`while !queue\.queue\.is_empty`,
		`if let Some\(mut outbound_writer\) = outbound_writer`,
		`#\[cfg\(test\)\]\n#\[path = "turn_integration_tests`,
	}
	var selected []rule
	for _, candidate := range rulesForFile(t, "rust-client/turn.rs") {
		for _, anchor := range anchors {
			if strings.Contains(candidate.Pattern, anchor) {
				selected = append(selected, candidate)
				break
			}
		}
	}
	if len(selected) != 9 {
		t.Fatalf("selected %d outbound rules, want 9", len(selected))
	}
	return selected
}

func TestTurnOutboundRules(t *testing.T) {
	withRules(t, outboundTurnRules(t)...)
	const upstream = `struct OutboundQueue {
    queue: ArrayQueue<PacketBuf>,
    waiter: Notify,
    space: Notify,
    shared: Arc<DriverShared>,
    socket: Arc<UdpSocket>,
}

fn new_queue() {
    OutboundQueue {
        socket: socket.clone(),
    }
}

async fn outbound_writer_loop(queue: Arc<OutboundQueue>) {
    while let Some(mut packet) = queue.queue.pop() {
        queue.space.notify_one();
    }
}

fn send_fast() {
    match queue.queue.push(packet) {
    }
}

async fn send_async() {
    match queue.queue.push(packet) {
    }
    match queue.queue.push(packet) {
    }
}

async fn deallocate() {
            while !queue.queue.is_empty() && Instant::now() < flush_deadline {
                let remaining = flush_deadline.saturating_duration_since(Instant::now());
                let space = queue.space.notified();
                if queue.queue.is_empty() {
                    break;
                }
                let _ = tokio::time::timeout(remaining, space).await;
            }
        if let Some(mut outbound_writer) = outbound_writer
            && tokio::time::timeout(Duration::from_secs(1), &mut outbound_writer)
                .await
                .is_err()
        {
            outbound_writer.abort();
            let _ = outbound_writer.await;
        }
}

#[cfg(test)]
#[path = "turn_integration_tests.rs"]
mod integration_tests;
`

	got, err := applyRules("rust-client/turn.rs", upstream)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"queue: ArrayQueue<OutboundPacket>,",
		"struct OutboundState {",
		"pending: usize,",
		"fn try_enqueue(&self, packet: PacketBuf) -> Result<(), PacketBuf>",
		"state.pending += 1;",
		"returned.completion.take();",
		"fn discard_queued(&self)",
		"test_writer_gate: Mutex<Option<Arc<outbound_tests::WriterGate>>>,",
		"gate.pause_after_pop().await;",
		"match queue.try_enqueue(packet) {",
		"queue.completion.close();",
		"completed.as_mut().enable();",
		"queue.completion.pending() == 0",
		"queue.completion.pending()",
		"queue.discard_queued();",
		"#[path = \"turn_outbound_tests.rs\"]",
		"mod outbound_tests;",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("generated source lacks %q", want)
		}
	}
	if strings.Contains(got, "ArrayQueue<PacketBuf>") || strings.Contains(got, "!queue.queue.is_empty() && Instant::now() < flush_deadline") {
		t.Fatal("obsolete queue type or queue-only flush remains")
	}
	if got := strings.Count(got, "match queue.try_enqueue(packet) {"); got != 3 {
		t.Errorf("try_enqueue substitutions = %d, want 3", got)
	}
}

func TestTurnOutboundRulesFailClosedOnAnchorDrift(t *testing.T) {
	var queueRule rule
	for _, candidate := range rulesForFile(t, "rust-client/turn.rs") {
		if strings.Contains(candidate.Pattern, `queue: ArrayQueue<PacketBuf>`) {
			queueRule = candidate
			break
		}
	}
	if queueRule.File == "" {
		t.Fatal("queue type substitution rule not found")
	}
	withRules(t, queueRule)
	for name, upstream := range map[string]string{
		"missing":   "struct OutboundQueue {}\n",
		"duplicate": "struct OutboundQueue {\n    queue: ArrayQueue<PacketBuf>,\n    queue: ArrayQueue<PacketBuf>,\n}\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := applyRules("rust-client/turn.rs", upstream); err == nil {
				t.Fatal("anchor drift was accepted")
			}
		})
	}
}

func TestTurnOutboundIntegrationRules(t *testing.T) {
	withRules(t, rulesForFile(t, "rust-client/turn_integration_tests.rs")...)
	const upstream = `async fn connect(
    server: SocketAddr,
    peer: SocketAddr,
    pool: Arc<PacketPool>,
) -> Arc<TurnAllocation> {
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn data_packets_preserve_channel_order_and_release_idle_pool_leases() {
    original_test_body();
}
`

	got, err := applyRules("rust-client/turn_integration_tests.rs", upstream)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"pub(super) async fn connect(",
		"pub(super) async fn serve_outbound_test_flow(",
		"let client = receive_challenged_allocate(&server, relay).await;",
		"assert_eq!(attribute_u32(&refresh, ATTR_LIFETIME), 0);",
		"server\n            .send_to(&refresh_zero_success(refresh.transaction()), client)",
		"async fn data_packets_preserve_channel_order_and_release_idle_pool_leases() {\n    original_test_body();",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("generated source lacks %q", want)
		}
	}
}

func TestTurnOutboundIntegrationRulesFailClosedOnMissingOrDuplicateAnchors(t *testing.T) {
	var testRule rule
	for _, candidate := range rulesForFile(t, "rust-client/turn_integration_tests.rs") {
		if strings.Contains(candidate.Pattern, "data_packets_preserve_channel_order") {
			testRule = candidate
			break
		}
	}
	if testRule.File == "" {
		t.Fatal("integration helper insertion rule not found")
	}
	withRules(t, testRule)
	anchor := "#[tokio::test(flavor = \"multi_thread\", worker_threads = 2)]\n" +
		"async fn data_packets_preserve_channel_order_and_release_idle_pool_leases() {\n}\n"
	for name, upstream := range map[string]string{
		"missing":   "#[tokio::test]\nasync fn unrelated_test() {}\n",
		"duplicate": anchor + "\n" + anchor,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := applyRules("rust-client/turn_integration_tests.rs", upstream); err == nil {
				t.Fatal("anchor drift was accepted")
			}
		})
	}
}
