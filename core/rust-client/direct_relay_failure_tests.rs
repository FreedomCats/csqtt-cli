// П10 regression: a failed direct TCP connect resets the application's flow
// instead of closing it silently, and the destination address is remembered so
// new TCP connections use the tunnel for a bounded period. The reset sequence
// is taken from the SYN-ACK the direct stack already sent to the application,
// and the reset refers to the next expected sequence. Only documentation
// addresses are used; no live service is contacted, and the production connect
// to 192.0.2.10 stays pending for its own timeout and is dropped with the test
// runtime.

use super::*;

/// The application side of a flow.
fn app(port: u16) -> SocketAddr {
    SocketAddr::new("10.66.67.2".parse().unwrap(), port)
}

/// A destination from the documentation ranges.
fn destination(ip: &str, port: u16) -> SocketAddr {
    SocketAddr::new(ip.parse().unwrap(), port)
}

/// TCP fields of a frame: source, destination, sequence, acknowledgement and
/// flags.
fn tcp_fields(frame: &[u8]) -> Option<(SocketAddr, SocketAddr, u32, u32, u8)> {
    let src = crate::sni::extract_src_addr(frame)?;
    let dst = crate::sni::extract_dst_addr(frame)?;
    let ip_header_len = if src.is_ipv6() { 40 } else { 20 };
    let tcp = frame.get(ip_header_len..)?;
    if tcp.len() < 20 {
        return None;
    }
    Some((
        src,
        dst,
        u32::from_be_bytes([tcp[4], tcp[5], tcp[6], tcp[7]]),
        u32::from_be_bytes([tcp[8], tcp[9], tcp[10], tcp[11]]),
        tcp[13],
    ))
}

/// The IPv4 header and TCP checksums of a frame verify against its addresses.
fn assert_ipv4_checksums(frame: &[u8], src: SocketAddr, dst: SocketAddr) {
    assert_eq!(ip_checksum(&frame[..20]), 0, "IPv4 header checksum");
    let (SocketAddr::V4(src), SocketAddr::V4(dst)) = (src, dst) else {
        panic!("an IPv4 frame with IPv4 addresses");
    };
    assert_eq!(
        tcp_checksum_v4(*src.ip(), *dst.ip(), &frame[20..]),
        0,
        "TCP checksum"
    );
}

/// The production relay records the SYN-ACK sequence, and the failed outer
/// connect reaches the reset path. The test drives the handler itself while
/// the production task has not removed the record yet; either way the reset
/// carries the same sequence. The outer connect is never awaited: it may stay
/// pending for its five-second timeout.
#[tokio::test]
async fn syn_ack_record_reaches_the_failure_path() {
    let domain_routing = Arc::new(ArcSwap::from_pointee(crate::dispatcher::DomainRoutingConfig {
        enabled: false,
        ip_routing_enabled: true,
        ip_is_whitelist: false,
        ip_rules: Arc::new(vec![crate::sni::IpRule::Cidr(Ipv4Addr::new(192, 0, 2, 0), 24)]),
        ..Default::default()
    }));
    let direct_ips = Arc::new(ArcSwap::from_pointee(std::collections::HashSet::new()));
    let dns_domain_map = Arc::new(crate::sni::DnsDomainMap::default());
    let pool = PacketPool::new(16);
    let (inbound_tx, mut inbound_rx) = mpsc::channel(DIRECT_RELAY_INBOUND_CAPACITY);
    let relay = DirectRelay::new(
        domain_routing,
        direct_ips,
        dns_domain_map,
        pool.clone(),
        inbound_tx,
    )
    .expect("the direct stack must initialize");

    let src = app(54321);
    let dst = destination("192.0.2.10", 80);
    let syn = build_tcp_packet(src, dst, 1000, 0, 0x02, &[]).unwrap();
    let mut packet = pool.try_acquire().unwrap();
    packet
        .extend_tail(syn.len())
        .unwrap()
        .copy_from_slice(&syn);
    assert!(
        relay.try_handle(&packet).await,
        "the SYN must take the direct path"
    );

    let key = (src, dst);
    let deadline = tokio::time::Instant::now() + Duration::from_secs(2);
    let mut syn_ack_seq = None;
    let mut reset = None;
    while syn_ack_seq.is_none() || reset.is_none() {
        let remaining = deadline.saturating_duration_since(tokio::time::Instant::now());
        assert!(
            !remaining.is_zero(),
            "no SYN-ACK and reset within two seconds"
        );
        let frame = tokio::time::timeout(remaining, inbound_rx.recv())
            .await
            .expect("no SYN-ACK and reset within two seconds")
            .expect("the inbound channel closed");
        let Some((from, to, seq, ack, flags)) = tcp_fields(frame.as_slice()) else {
            continue;
        };
        if from == dst && to == src && flags == 0x12 {
            syn_ack_seq = Some(seq);
            let recorded = relay.syn_ack_seqs.read().unwrap().get(&key).copied();
            if recorded == Some(seq) {
                abort_failed_direct_tcp(
                    &relay.pool,
                    &relay.inbound_tx,
                    &relay.syn_ack_seqs,
                    key,
                    Duration::from_millis(100),
                    &relay.failed_direct_ips,
                    Duration::from_secs(300),
                )
                .await;
            }
            continue;
        }
        if from == dst && to == src && flags == 0x04 {
            reset = Some((frame, seq, ack));
        }
    }

    let seq = syn_ack_seq.unwrap();
    let (frame, reset_seq, reset_ack) = reset.unwrap();
    let (from, to, _, _, flags) = tcp_fields(frame.as_slice()).unwrap();
    assert_eq!((from, to), (dst, src), "reset address pair");
    assert_eq!(flags, 0x04, "reset flags");
    assert_eq!(reset_seq, seq.wrapping_add(1), "reset sequence is S+1");
    assert_eq!(reset_ack, 0, "reset acknowledgement");
    assert_eq!(
        frame.len(),
        40,
        "an IPv4 reset is a 20-byte IP header and a 20-byte TCP header"
    );
    assert_ipv4_checksums(frame.as_slice(), dst, src);
    assert!(
        relay.syn_ack_seqs.read().unwrap().is_empty(),
        "the SYN-ACK record must be gone"
    );
    assert!(
        relay
            .failed_direct_ips
            .read()
            .unwrap()
            .contains_key(&dst.ip()),
        "the failed destination must be remembered"
    );
}

/// The handler sends an RST built from the recorded sequence: the destination
/// is the source, the application is the target, the sequence is S+1 and the
/// acknowledgement is zero. Both address families carry a 20-byte TCP header
/// with valid checksums, and the record is removed.
#[tokio::test]
async fn failed_connect_emits_reset_with_recorded_sequence() {
    let pool = PacketPool::new(16);
    let (tx, mut rx) = mpsc::channel(64);
    let map = RwLock::new(HashMap::new());
    let failed = RwLock::new(HashMap::new());

    let src = app(54321);
    let dst = destination("192.0.2.10", 80);
    map.write().unwrap().insert((src, dst), 0x1122_3344);
    abort_failed_direct_tcp(
        &pool,
        &tx,
        &map,
        (src, dst),
        Duration::from_millis(10),
        &failed,
        Duration::from_secs(300),
    )
    .await;

    let packet = rx.try_recv().expect("the handler sent no reset");
    let frame = packet.as_slice();
    assert_eq!(frame[0] >> 4, 4, "IPv4 reset");
    assert_eq!(frame[9], 6, "TCP reset");
    assert_eq!(packet.len(), 40, "IPv4 total length");
    let (from, to, seq, ack, flags) = tcp_fields(frame).unwrap();
    assert_eq!((from, to), (dst, src), "reset address pair");
    assert_eq!(seq, 0x1122_3345, "the reset uses S+1");
    assert_eq!(ack, 0, "reset acknowledgement");
    assert_eq!(frame[20 + 12] >> 4, 5, "20-byte TCP header");
    assert_eq!(flags, 0x04, "reset flags");
    assert_ipv4_checksums(frame, dst, src);
    assert!(map.read().unwrap().is_empty(), "the record must be removed");
    assert!(
        failed.read().unwrap().contains_key(&dst.ip()),
        "the IPv4 destination must be remembered"
    );

    let src = destination("2001:db8::2", 12345);
    let dst = destination("2001:db8::1", 443);
    map.write().unwrap().insert((src, dst), 0x1122_3344);
    abort_failed_direct_tcp(
        &pool,
        &tx,
        &map,
        (src, dst),
        Duration::from_millis(10),
        &failed,
        Duration::from_secs(300),
    )
    .await;

    let packet = rx.try_recv().expect("the handler sent no IPv6 reset");
    let frame = packet.as_slice();
    assert_eq!(frame[0] >> 4, 6, "IPv6 reset");
    assert_eq!(frame[6], 6, "TCP reset");
    assert_eq!(packet.len(), 60, "IPv6 total length");
    let (from, to, seq, ack, flags) = tcp_fields(frame).unwrap();
    assert_eq!((from, to), (dst, src), "IPv6 reset address pair");
    assert_eq!(seq, 0x1122_3345, "the IPv6 reset uses S+1");
    assert_eq!(ack, 0, "IPv6 reset acknowledgement");
    assert_eq!(frame[40 + 12] >> 4, 5, "20-byte TCP header");
    assert_eq!(flags, 0x04, "IPv6 reset flags");
    let (SocketAddr::V6(rst_src), SocketAddr::V6(rst_dst)) = (dst, src) else {
        panic!("an IPv6 frame with IPv6 addresses");
    };
    assert_eq!(
        tcp_checksum_v6(*rst_src.ip(), *rst_dst.ip(), &frame[40..]),
        0,
        "IPv6 TCP checksum"
    );
    assert!(
        map.read().unwrap().is_empty(),
        "the IPv6 record must be removed"
    );
    assert!(
        failed.read().unwrap().contains_key(&dst.ip()),
        "the IPv6 destination must be remembered"
    );
}

/// Without a record the handler returns within its bounded wait and sends
/// nothing: no sequence is invented. The address is remembered anyway: the
/// failure does not depend on the SYN-ACK record.
#[tokio::test]
async fn missing_record_closes_without_reset() {
    let pool = PacketPool::new(16);
    let (tx, mut rx) = mpsc::channel(64);
    let map = RwLock::new(HashMap::new());
    let failed = RwLock::new(HashMap::new());
    let key = (app(54321), destination("192.0.2.10", 80));
    tokio::time::timeout(
        Duration::from_secs(1),
        abort_failed_direct_tcp(
            &pool,
            &tx,
            &map,
            key,
            Duration::from_millis(10),
            &failed,
            Duration::from_secs(300),
        ),
    )
    .await
    .expect("the handler hung without a record");
    assert!(
        failed.read().unwrap().contains_key(&key.1.ip()),
        "the address must be remembered even without a SYN-ACK record"
    );
    assert!(
        tokio::time::timeout(Duration::from_millis(50), rx.recv())
            .await
            .is_err(),
        "a reset was sent without a recorded sequence"
    );
}

/// Only a TCP SYN+ACK is accepted: the key is (application, destination) and
/// the sequence is the exact one from the frame. A plain ACK, a SYN without
/// ACK, a truncated frame and a non-TCP frame give None.
#[test]
fn syn_ack_flow_accepts_only_syn_ack() {
    let app4 = app(54321);
    let dst4 = destination("192.0.2.10", 80);
    let syn_ack4 = build_tcp_packet(dst4, app4, 0x0a0b_0c0d, 0x0102_0304, 0x12, &[]).unwrap();
    assert_eq!(syn_ack_flow(&syn_ack4), Some(((app4, dst4), 0x0a0b_0c0d)));

    let app6 = destination("2001:db8::2", 12345);
    let dst6 = destination("2001:db8::1", 443);
    let syn_ack6 = build_tcp_packet(dst6, app6, 0x0102_0304, 0x0a0b_0c0d, 0x12, &[]).unwrap();
    assert_eq!(syn_ack_flow(&syn_ack6), Some(((app6, dst6), 0x0102_0304)));

    let plain_ack = build_tcp_packet(dst4, app4, 7, 8, 0x10, &[]).unwrap();
    assert_eq!(syn_ack_flow(&plain_ack), None, "a plain ACK");
    let syn_only = build_tcp_packet(dst4, app4, 7, 0, 0x02, &[]).unwrap();
    assert_eq!(syn_ack_flow(&syn_only), None, "a SYN without ACK");
    assert_eq!(syn_ack_flow(&syn_ack4[..30]), None, "a truncated frame");
    let udp = build_udp_packet(dst4, app4, b"x").unwrap();
    assert_eq!(syn_ack_flow(&udp), None, "a UDP frame");
}

/// A new TCP SYN to a remembered address takes the tunnel, a non-SYN packet of
/// an established direct flow stays direct, and an expired entry allows a
/// direct attempt again. Only documentation addresses are used; the outer
/// connects started for the direct verdicts stay pending and are dropped with
/// the test runtime.
#[tokio::test]
async fn failed_direct_memory_routes_new_syns_to_the_tunnel() {
    let domain_routing = Arc::new(ArcSwap::from_pointee(
        crate::dispatcher::DomainRoutingConfig {
            enabled: false,
            ip_routing_enabled: true,
            ip_is_whitelist: false,
            ip_rules: Arc::new(vec![crate::sni::IpRule::Cidr(
                Ipv4Addr::new(192, 0, 2, 0),
                24,
            )]),
            ..Default::default()
        },
    ));
    let direct_ips = Arc::new(ArcSwap::from_pointee(std::collections::HashSet::new()));
    let dns_domain_map = Arc::new(crate::sni::DnsDomainMap::default());
    let pool = PacketPool::new(16);
    let (inbound_tx, _inbound_rx) = mpsc::channel(DIRECT_RELAY_INBOUND_CAPACITY);
    let relay = DirectRelay::new(
        domain_routing,
        direct_ips,
        dns_domain_map,
        pool.clone(),
        inbound_tx,
    )
    .expect("the direct stack must initialize");

    let direct_dst = destination("192.0.2.10", 80);
    let syn = build_tcp_packet(app(54321), direct_dst, 1000, 0, 0x02, &[]).unwrap();
    let mut packet = pool.try_acquire().unwrap();
    packet
        .extend_tail(syn.len())
        .unwrap()
        .copy_from_slice(&syn);
    assert!(
        relay.try_handle(&packet).await,
        "an address without a remembered failure must be tried directly"
    );

    let failed_dst = destination("192.0.2.11", 443);
    remember_failed_direct(
        &relay.failed_direct_ips,
        failed_dst.ip(),
        std::time::Instant::now(),
        FAILED_DIRECT_MEMORY_TTL,
    );
    let syn = build_tcp_packet(app(54322), failed_dst, 2000, 0, 0x02, &[]).unwrap();
    let mut packet = pool.try_acquire().unwrap();
    packet
        .extend_tail(syn.len())
        .unwrap()
        .copy_from_slice(&syn);
    assert!(
        !relay.try_handle(&packet).await,
        "a new SYN to a remembered address must take the tunnel"
    );

    let key = (app(54322), failed_dst);
    relay
        .active_tcp
        .write()
        .unwrap()
        .insert(key, std::time::Instant::now());
    let ack = build_tcp_packet(key.0, key.1, 2001, 0, 0x10, &[]).unwrap();
    let mut packet = pool.try_acquire().unwrap();
    packet
        .extend_tail(ack.len())
        .unwrap()
        .copy_from_slice(&ack);
    assert!(
        relay.try_handle(&packet).await,
        "an established direct flow must stay direct"
    );

    relay.failed_direct_ips.write().unwrap().insert(
        failed_dst.ip(),
        std::time::Instant::now() - Duration::from_secs(1),
    );
    let syn = build_tcp_packet(app(54323), failed_dst, 3000, 0, 0x02, &[]).unwrap();
    let mut packet = pool.try_acquire().unwrap();
    packet
        .extend_tail(syn.len())
        .unwrap()
        .copy_from_slice(&syn);
    assert!(
        relay.try_handle(&packet).await,
        "an expired memory entry must allow a direct attempt again"
    );
}

/// The memory expires exactly at its deadline when the time is injected; no
/// test waits for the production five minutes.
#[test]
fn failed_direct_memory_expires_with_injected_time() {
    let map = RwLock::new(HashMap::new());
    let ip: IpAddr = "192.0.2.10".parse().unwrap();
    let now = std::time::Instant::now();
    let ttl = Duration::from_secs(300);
    remember_failed_direct(&map, ip, now, ttl);
    assert!(
        failed_direct_active(&map, ip, now),
        "active at the recording instant"
    );
    assert!(
        failed_direct_active(&map, ip, now + ttl - Duration::from_millis(1)),
        "active just before the deadline"
    );
    assert!(
        !failed_direct_active(&map, ip, now + ttl),
        "absent at the deadline"
    );
    assert!(
        !failed_direct_active(&map, ip, now + ttl + Duration::from_millis(1)),
        "absent after the deadline"
    );
    assert!(
        !failed_direct_active(&map, "192.0.2.11".parse().unwrap(), now),
        "another address is not remembered"
    );
}

/// Turning domain routing off alone keeps the memory; disabling the whole
/// routing configuration clears it.
#[tokio::test]
async fn disabling_routing_clears_the_failed_address_memory() {
    let domain_routing = Arc::new(ArcSwap::from_pointee(
        crate::dispatcher::DomainRoutingConfig {
            enabled: false,
            ip_routing_enabled: true,
            ..Default::default()
        },
    ));
    let direct_ips = Arc::new(ArcSwap::from_pointee(std::collections::HashSet::new()));
    let dns_domain_map = Arc::new(crate::sni::DnsDomainMap::default());
    let pool = PacketPool::new(16);
    let (inbound_tx, _inbound_rx) = mpsc::channel(DIRECT_RELAY_INBOUND_CAPACITY);
    let relay = DirectRelay::new(domain_routing, direct_ips, dns_domain_map, pool, inbound_tx)
        .expect("the direct stack must initialize");

    let ip: IpAddr = "192.0.2.10".parse().unwrap();
    remember_failed_direct(
        &relay.failed_direct_ips,
        ip,
        std::time::Instant::now(),
        FAILED_DIRECT_MEMORY_TTL,
    );

    relay.sync_domain_routing(&crate::dispatcher::DomainRoutingConfig {
        enabled: false,
        ip_routing_enabled: true,
        ..Default::default()
    });
    assert!(
        failed_direct_active(&relay.failed_direct_ips, ip, std::time::Instant::now()),
        "turning domain routing off alone keeps the memory"
    );

    relay.sync_domain_routing(&crate::dispatcher::DomainRoutingConfig {
        enabled: false,
        ip_routing_enabled: false,
        ..Default::default()
    });
    assert!(
        relay.failed_direct_ips.read().unwrap().is_empty(),
        "disabling the whole routing configuration clears the memory"
    );
}
