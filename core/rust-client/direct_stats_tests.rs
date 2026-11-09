// Regression tests for the path split of the STATS traffic counters: the
// tunnel owns bytes_up/bytes_down, the direct relay owns direct_up/direct_down,
// and a packet is credited once, by the path that accepted it.

use super::*;
use std::sync::atomic::Ordering;

/// Dispatcher fixture with no registered workers. With `with_direct_relay` the
/// direct stack exists and the IP rule sends `192.0.2.0/24` to it; without the
/// relay every packet is tunnel-bound.
struct Fixture {
    dispatcher: Arc<Dispatcher>,
    stats: Arc<Stats>,
    pool: Arc<PacketPool>,
    /// Keeps the direct relay's inbound channel open for the test duration.
    _inbound_rx: Option<mpsc::Receiver<PacketBuf>>,
}

impl Fixture {
    fn new(with_direct_relay: bool) -> Self {
        let pool = PacketPool::new(32);
        let stats = Arc::new(Stats::default());
        let domain_routing = Arc::new(ArcSwap::from_pointee(DomainRoutingConfig {
            enabled: false,
            is_whitelist: false,
            exclude_ru_zones: false,
            template_domains: false,
            direct_domains: Arc::new(Vec::new()),
            ip_routing_enabled: true,
            ip_is_whitelist: false,
            ip_rules: Arc::new(vec![crate::sni::IpRule::Cidr(
                std::net::Ipv4Addr::new(192, 0, 2, 0),
                24,
            )]),
        }));
        let direct_ips = Arc::new(ArcSwap::from_pointee(HashSet::new()));
        let dns_domain_map = Arc::new(crate::sni::DnsDomainMap::default());
        let (direct_relay, inbound_rx) = if with_direct_relay {
            let (inbound_tx, inbound_rx) = mpsc::channel(64);
            let relay = crate::direct_relay::DirectRelay::new(
                domain_routing.clone(),
                direct_ips.clone(),
                dns_domain_map.clone(),
                pool.clone(),
                inbound_tx,
            )
            .expect("smoltcp test stack must initialize");
            (Some(Arc::new(relay)), Some(inbound_rx))
        } else {
            (None, None)
        };
        let dispatcher = Arc::new(Dispatcher {
            workers: ArcSwap::from_pointee(Vec::new()),
            inbound: ArrayQueue::new(16),
            outbound_capacity: 64,
            tun_wake: Arc::new(Notify::new()),
            tun_io_lost: Arc::new(Notify::new()),
            cancel: CancellationToken::new(),
            tasks: tokio::sync::Mutex::new(Vec::new()),
            domain_routing,
            direct_ips,
            dns_domain_map,
            direct_relay,
            uplink_state: Mutex::new(UplinkState::new()),
            pool: pool.clone(),
            stats: stats.clone(),
            reassembler: crate::wire_protocol::ShardedReassembler::new(),
            next_frag_id: std::sync::atomic::AtomicU16::new(1),
            termination: Arc::new(OnceLock::new()),
        });
        Self {
            dispatcher,
            stats,
            pool,
            _inbound_rx: inbound_rx,
        }
    }

    /// Packet buffer holding `raw`, as the read loops hand it to `dispatch`.
    fn packet(&self, raw: &[u8]) -> PacketBuf {
        let mut packet = self.pool.try_acquire().expect("packet buffer");
        packet
            .extend_tail(raw.len())
            .expect("packet headroom")
            .copy_from_slice(raw);
        packet
    }

    fn uplink(&self) -> i64 {
        self.stats.total_bytes_up.load(Ordering::Relaxed)
    }

    fn direct_uplink(&self) -> i64 {
        self.stats.direct_bytes_up.load(Ordering::Relaxed)
    }

    fn tunnel_downlink(&self) -> i64 {
        self.stats.total_bytes_down.load(Ordering::Relaxed)
    }

    fn direct_downlink(&self) -> i64 {
        self.stats.direct_bytes_down.load(Ordering::Relaxed)
    }
}

/// Synthetic TCP SYN to a documentation address, built like the direct relay
/// tests build their packets.
fn tcp_syn(dst: &str) -> Vec<u8> {
    crate::direct_relay::build_tcp_packet(
        "10.0.0.2:54321".parse().unwrap(),
        dst.parse().unwrap(),
        1000,
        0,
        0x02,
        &[],
    )
    .expect("synthetic TCP SYN")
}

/// Minimal well-formed IPv4 header for the downlink tests.
fn ipv4_header() -> Vec<u8> {
    let mut packet = vec![0u8; 20];
    packet[0] = 0x45;
    packet[2..4].copy_from_slice(&20u16.to_be_bytes());
    packet[8] = 64;
    packet[9] = 6;
    packet[12..16].copy_from_slice(&[10, 0, 0, 2]);
    packet[16..20].copy_from_slice(&[198, 51, 100, 10]);
    packet
}

#[tokio::test]
async fn direct_syn_is_credited_only_to_the_direct_uplink() {
    let fixture = Fixture::new(true);
    let syn = tcp_syn("192.0.2.10:80");
    let length = syn.len();

    let outcome = fixture.dispatcher.dispatch(fixture.packet(&syn)).await;
    assert_eq!(outcome, DispatchOutcome::Direct);
    // Dispatch itself only classifies; the read loops do the accounting.
    assert_eq!(fixture.direct_uplink(), 0);
    assert_eq!(fixture.uplink(), 0);

    credit_uplink(&fixture.stats, outcome, length);
    assert_eq!(fixture.direct_uplink(), length as i64);
    assert_eq!(fixture.uplink(), 0);
}

#[tokio::test]
async fn tunnel_packet_without_workers_is_dropped_without_counters() {
    let fixture = Fixture::new(true);
    let syn = tcp_syn("198.51.100.10:80");
    let length = syn.len();

    let outcome = fixture.dispatcher.dispatch(fixture.packet(&syn)).await;
    assert_eq!(outcome, DispatchOutcome::Dropped);
    credit_uplink(&fixture.stats, outcome, length);
    assert_eq!(fixture.uplink(), 0);
    assert_eq!(fixture.direct_uplink(), 0);
}

#[test]
fn tunnel_outcome_is_credited_to_the_tunnel_uplink() {
    let stats = Stats::default();
    credit_uplink(&stats, DispatchOutcome::Tunnel, 123);
    credit_uplink(&stats, DispatchOutcome::Dropped, 456);
    assert_eq!(stats.total_bytes_up.load(Ordering::Relaxed), 123);
    assert_eq!(stats.direct_bytes_up.load(Ordering::Relaxed), 0);
}

#[tokio::test]
async fn return_packet_credits_the_tunnel_downlink() {
    let fixture = Fixture::new(false);
    let raw = ipv4_header();

    fixture.dispatcher.return_packet(fixture.packet(&raw));
    assert_eq!(fixture.tunnel_downlink(), raw.len() as i64);
    assert_eq!(fixture.direct_downlink(), 0);

    let queued = fixture.dispatcher.pop_inbound().expect("queued packet");
    assert_eq!(queued.as_slice(), raw.as_slice());
    assert_eq!(fixture.tunnel_downlink(), raw.len() as i64);
    assert_eq!(fixture.direct_downlink(), 0);
}

#[tokio::test]
async fn push_direct_inbound_credits_the_direct_downlink_once() {
    let fixture = Fixture::new(false);
    let raw = ipv4_header();

    fixture.dispatcher.push_direct_inbound(fixture.packet(&raw));
    assert_eq!(fixture.direct_downlink(), raw.len() as i64);
    assert_eq!(fixture.tunnel_downlink(), 0);

    // Taking the packet out of the receive queue credits nothing: the counter
    // is written once, at the point where the path accepts the packet.
    let queued = fixture.dispatcher.pop_inbound().expect("queued packet");
    assert_eq!(queued.as_slice(), raw.as_slice());
    assert_eq!(fixture.direct_downlink(), raw.len() as i64);
    assert_eq!(fixture.tunnel_downlink(), 0);
}

#[test]
fn stats_payload_has_exactly_the_split_keys() {
    let payload = crate::events::stats_payload(2, 1000, 2000, 3000, 4000);
    let object = payload.as_object().expect("STATS payload is a JSON object");
    let mut keys: Vec<&str> = object.keys().map(String::as_str).collect();
    keys.sort_unstable();
    assert_eq!(
        keys,
        [
            "active",
            "bytes_down",
            "bytes_up",
            "direct_down",
            "direct_up"
        ]
    );
    assert_eq!(payload["active"], 2);
    assert_eq!(payload["bytes_up"], 1000);
    assert_eq!(payload["bytes_down"], 2000);
    assert_eq!(payload["direct_up"], 3000);
    assert_eq!(payload["direct_down"], 4000);
}
