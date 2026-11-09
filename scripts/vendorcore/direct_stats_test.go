package main

import (
	"strings"
	"testing"
)

// directStatsRules selects the P8 rules for dispatcher.rs by their anchors.
// The listen_peer rules for the same file must not be selected.
func directStatsRules(t *testing.T) []rule {
	t.Helper()
	anchors := []string{
		"pub struct Dispatcher",
		"pool: Arc<PacketPool>,\\n( +)reassembler",
		`profile\.inbound_capacity`,
		"outbound_capacity: 64,",
		"outbound_capacity: 32,",
		`ArrayQueue::new\(inbound_capacity\)`,
		"pub async fn dispatch",
		`relay\.try_handle`,
		`let workers = self\.workers\.load`,
		"фрагмент целиком потерян",
		`\.total_bytes_up`,
		"pub fn return_packet",
		`pump_dispatcher\.push_inbound`,
		"tokio::io::unix::AsyncFd",
		"write_stats",
		`fetch_add\(packet\.len\(\) as i64`,
		`async fn write_udp\(`,
		`write_dispatcher\.write_udp`,
		"Ok\\(length\\) if length == (?:packet",
		`async fn write_udp_packet\(`,
		`self\.write_udp_packet\(&socket, Some\(address\), &stats`,
		`fetch_add\(length as i64, Ordering::Relaxed\);\n +return;`,
		"mod tests",
		"fn strip_legacy_downlink_prefix",
	}
	var selected []rule
	for _, candidate := range rulesForFile(t, "rust-client/dispatcher.rs") {
		for _, anchor := range anchors {
			if strings.Contains(candidate.Pattern, anchor) {
				selected = append(selected, candidate)
				break
			}
		}
	}
	if len(selected) != 26 {
		t.Fatalf("selected %d direct-stats rules, want 26", len(selected))
	}
	return selected
}

// representative dispatcher text with every P8 anchor, once per required
// match: the two constructors and the two equal-capacity test literals are
// duplicated exactly as in the upstream file.
const directStatsUpstream = `pub struct Dispatcher {
    workers: ArcSwap<Vec<WorkerEntry>>,
    inbound: ArrayQueue<PacketBuf>,
    outbound_capacity: usize,
    uplink_state: Mutex<UplinkState>,
    pool: Arc<PacketPool>,
    reassembler: crate::wire_protocol::ShardedReassembler,
    next_frag_id: std::sync::atomic::AtomicU16,
    termination: Arc<OnceLock<Termination>>,
}

impl Dispatcher {
    fn start_test_tun() {
        let dispatcher = Arc::new(Self {
            workers: ArcSwap::from_pointee(Vec::new()),
            inbound: ArrayQueue::new(profile.inbound_capacity),
            outbound_capacity: profile.outbound_capacity,
        });
    }

    fn start_with_domain_routing() {
        let dispatcher = Arc::new(Self {
            workers: ArcSwap::from_pointee(Vec::new()),
            inbound: ArrayQueue::new(profile.inbound_capacity),
            outbound_capacity: profile.outbound_capacity,
        });
    }

    pub async fn dispatch(&self, packet: PacketBuf) {
        self.inspect_domain_routing(&packet);
        if let Some(relay) = &self.direct_relay
            && relay.try_handle(&packet).await
        {
            return;
        }
        let workers = self.workers.load();
        if workers.is_empty() {
            return;
        }

        if packet.len() <= 1280 {
            self.send_to_workers(packet, &workers).await;
        } else {
            if frags.len() == expected_frags {
                self.send_fragments_to_workers(frags, &workers, flow, packet_len)
                    .await;
            } else {
                crate::log_error!(
                    "[ОШИБКА] Dispatcher: фрагмент целиком потерян: {} из {} (буферы)",
                    frags.len(),
                    expected_frags
                );
            }
        }
    }

    async fn read_tun(&self) {
                        if self.workers.load().is_empty() {
                            continue;
                        }
                        stats
                            .total_bytes_up
                            .fetch_add(length as i64, Ordering::Relaxed);
                        self.dispatch(packet).await;
    }

    async fn read_udp(&self) {
                        stats
                            .total_bytes_up
                            .fetch_add(packet.len() as i64, Ordering::Relaxed);
                        self.dispatch(packet).await;
    }

    pub fn return_packet(&self, mut packet: PacketBuf) {
        strip_legacy_downlink_prefix(&mut packet);
        self.inspect_domain_routing(&packet);
        self.push_inbound(packet);
    }

    async fn write_tun(
        &self,
        device: Arc<tokio::io::unix::AsyncFd<File>>,
        stats: Arc<Stats>,
        cancel: CancellationToken,
    ) {
                        if *written == packet.len() {
                            error_streak = 0;
                            stats
                                .total_bytes_down
                                .fetch_add(packet.len() as i64, Ordering::Relaxed);
                            burst += 1;
                        }
    }

    async fn write_udp(
        &self,
        socket: Arc<UdpSocket>,
        client: Arc<ArcSwapOption<SocketAddr>>,
        stats: Arc<Stats>,
    ) {
            match socket.try_send_to(packet.as_slice(), address) {
                Ok(length) if length == packet.len() => {
                    stats
                        .total_bytes_down
                        .fetch_add(length as i64, Ordering::Relaxed);
                }
                Ok(_) => {}
            }
            match socket.try_send_to(next_packet.as_slice(), address) {
                Ok(length) if length == next_packet.len() => {
                    stats
                        .total_bytes_down
                        .fetch_add(length as i64, Ordering::Relaxed);
                }
                Ok(_) => {}
            }
    }

    async fn write_udp_packet(
        &self,
        socket: &UdpSocket,
        address: Option<SocketAddr>,
        stats: &Stats,
        packet: &PacketBuf,
    ) {
                    Ok(length) if length == packet.len() => {
                        stats
                            .total_bytes_down
                            .fetch_add(length as i64, Ordering::Relaxed);
                        return;
                    }
                    Ok(_) => return,
    }
}

fn run_tun() {
            let write_dispatcher = self.clone();
            let write_cancel = generation_cancel.clone();
            let write_stats = stats.clone();
            let mut write_task = tokio::spawn(async move {
                write_dispatcher
                    .write_tun(device, write_stats, write_cancel)
                    .await;
            });
}

fn pump() {
                                    pump_dispatcher.push_inbound(pkt);
                write_dispatcher.write_udp(socket, client, stats).await;
                    self.write_udp_packet(&socket, Some(address), &stats, &packet)
                        .await;
                    self.write_udp_packet(&socket, Some(address), &stats, &next_packet)
                        .await;
}

#[inline(always)]
fn strip_legacy_downlink_prefix(packet: &mut PacketBuf) {
}

#[cfg(test)]
mod tests {
    fn literals() {
        let a = Dispatcher {
            workers: ArcSwap::from_pointee(Vec::new()),
            inbound: ArrayQueue::new(16),
            outbound_capacity: 64,
        };
        let b = Dispatcher {
            workers: ArcSwap::from_pointee(Vec::new()),
            inbound: ArrayQueue::new(16),
            outbound_capacity: 64,
        };
        let c = Dispatcher {
            workers: ArcSwap::from_pointee(Vec::new()),
            inbound: ArrayQueue::new(16),
            outbound_capacity: 32,
        };
        let d = Dispatcher {
            workers: ArcSwap::from_pointee(Vec::new()),
            inbound: ArrayQueue::new(inbound_capacity),
            outbound_capacity: profile.outbound_capacity,
        };
    }
}
`

func TestDirectStatsRules(t *testing.T) {
	withRules(t, directStatsRules(t)...)
	got, err := applyRules("rust-client/dispatcher.rs", directStatsUpstream)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"pub(crate) enum DispatchOutcome {",
		"pub async fn dispatch(&self, packet: PacketBuf) -> DispatchOutcome {",
		"return DispatchOutcome::Direct;",
		"return DispatchOutcome::Dropped;",
		"DispatchOutcome::Tunnel\n    }",
		"stats: Arc<Stats>,",
		"fn credit_uplink(stats: &Stats, outcome: DispatchOutcome, length: usize)",
		"pub fn push_direct_inbound(&self, packet: PacketBuf)",
		"pump_dispatcher.push_direct_inbound(pkt);",
		"#[path = \"direct_stats_tests.rs\"]",
		"mod direct_stats_tests;",
		"self.write_udp_packet(&socket, Some(address), &packet)",
		"self.write_udp_packet(&socket, Some(address), &next_packet)",
		"write_dispatcher.write_udp(socket, client).await;",
		".write_tun(device, write_cancel)",
		"self.stats\n            .total_bytes_down",
		"error_streak = 0;\n                            burst += 1;",
		"self.stats\n            .direct_bytes_down",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("generated source lacks %q", want)
		}
	}
	for text, want := range map[string]int{
		"stats: Arc<Stats>,":                         1,
		"stats: stats.clone(),":                      2,
		"stats: Arc::new(Stats::default()),":         4,
		"credit_uplink(&stats, outcome, length);":    2,
		"self.stats\n            .total_bytes_down":  1,
		"self.stats\n            .direct_bytes_down": 1,
		"fn credit_uplink(":                          1,
	} {
		if count := strings.Count(got, text); count != want {
			t.Errorf("%q appears %d times, want %d", text, count, want)
		}
	}
	for _, obsolete := range []string{
		"write_stats",
		"Some(address), &stats",
		"fetch_add(length as i64, Ordering::Relaxed);\n                        return;",
		"stats: Arc<Stats>,\n        cancel: CancellationToken",
		"stats: &Stats,\n        packet: &PacketBuf",
	} {
		if strings.Contains(got, obsolete) {
			t.Errorf("generated source still contains %q", obsolete)
		}
	}
}

// The event and stats rules: the STATS payload gets exactly the split keys and
// the periodic log line adds both paths to its traffic figure.
func TestDirectStatsEventRules(t *testing.T) {
	var statsRules []rule
	for _, candidate := range rulesForFile(t, "rust-client/stats.rs") {
		for _, anchor := range []string{"pub total_bytes_up", "let down = self", "let total_mb", `events\.stats`} {
			if strings.Contains(candidate.Pattern, anchor) {
				statsRules = append(statsRules, candidate)
				break
			}
		}
	}
	if len(statsRules) != 4 {
		t.Fatalf("selected %d stats rules, want 4", len(statsRules))
	}
	var eventRules []rule
	for _, candidate := range rulesForFile(t, "rust-client/events.rs") {
		for _, anchor := range []string{"pub fn stats", `self\.emit\("STATS"`, "fn encode_event"} {
			if strings.Contains(candidate.Pattern, anchor) {
				eventRules = append(eventRules, candidate)
				break
			}
		}
	}
	if len(eventRules) != 3 {
		t.Fatalf("selected %d event rules, want 3", len(eventRules))
	}

	const statsUpstream = `pub struct Stats {
    pub total_bytes_up: AtomicI64,
    pub total_bytes_down: AtomicI64,
    pub active_connections: AtomicI32,
}

impl Stats {
    async fn run() {
                    let down = self.total_bytes_down.load(Ordering::Relaxed);
                    log_counter += 1;
                    if log_counter >= 6 {
                        log_counter = 0;
                        let total_mb = (up + down) as f64 / (1024.0 * 1024.0);
                    }
                    events.stats(active, up, down);
    }
}
`
	withRules(t, statsRules...)
	got, err := applyRules("rust-client/stats.rs", statsUpstream)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"pub direct_bytes_up: AtomicI64,",
		"pub direct_bytes_down: AtomicI64,",
		"let direct_up = self.direct_bytes_up.load(Ordering::Relaxed);",
		"let direct_down = self.direct_bytes_down.load(Ordering::Relaxed);",
		"(up + down + direct_up + direct_down) as f64",
		"events.stats(active, up, down, direct_up, direct_down);",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("generated stats.rs lacks %q", want)
		}
	}
	if strings.Contains(got, "events.stats(active, up, down);") {
		t.Error("generated stats.rs still calls the three-argument stats")
	}

	const eventsUpstream = `impl Events {
    pub fn stats(&self, active: i32, bytes_up: i64, bytes_down: i64) {
        self.emit("STATS", || {
            serde_json::json!({
                "active": active,
                "bytes_up": bytes_up,
                "bytes_down": bytes_down
            })
        });
    }
}

fn encode_event<T: Serialize>(kind: &str, payload: &T) -> Option<String> {
}
`
	withRules(t, eventRules...)
	got, err = applyRules("rust-client/events.rs", eventsUpstream)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"direct_up: i64,",
		"direct_down: i64,",
		"stats_payload(active, bytes_up, bytes_down, direct_up, direct_down)",
		"pub(crate) fn stats_payload(",
		"\"direct_up\": direct_up,",
		"\"direct_down\": direct_down",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("generated events.rs lacks %q", want)
		}
	}
	if count := strings.Count(got, "stats_payload("); count != 2 {
		t.Errorf("stats_payload call and definition: %d, want 2", count)
	}
}

func TestDirectStatsRulesFailClosedOnAnchorDrift(t *testing.T) {
	var signature rule
	for _, candidate := range rulesForFile(t, "rust-client/dispatcher.rs") {
		if strings.Contains(candidate.Pattern, "pub async fn dispatch") {
			signature = candidate
			break
		}
	}
	if signature.File == "" {
		t.Fatal("dispatch signature rule not found")
	}
	withRules(t, signature)
	const line = "    pub async fn dispatch(&self, packet: PacketBuf) {\n"
	for name, upstream := range map[string]string{
		"missing":   "    pub async fn other(&self, packet: PacketBuf) {\n",
		"duplicate": line + line,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := applyRules("rust-client/dispatcher.rs", upstream); err == nil {
				t.Fatal("anchor drift was accepted")
			}
		})
	}
}

func TestDirectStatsUdpArmCountIsEnforced(t *testing.T) {
	var arm rule
	for _, candidate := range rulesForFile(t, "rust-client/dispatcher.rs") {
		if strings.Contains(candidate.Pattern, "Ok\\(length\\) if length == (?:packet") {
			arm = candidate
			break
		}
	}
	if arm.File == "" {
		t.Fatal("write_udp counter rule not found")
	}
	withRules(t, arm)
	const one = `                Ok(length) if length == packet.len() => {
                    stats
                        .total_bytes_down
                        .fetch_add(length as i64, Ordering::Relaxed);
                }
`
	for name, upstream := range map[string]string{
		"one":   one,
		"none":  "                Ok(_) => {}\n",
		"three": one + one + one,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := applyRules("rust-client/dispatcher.rs", upstream); err == nil {
				t.Fatal("unexpected arm count was accepted")
			}
		})
	}
}
