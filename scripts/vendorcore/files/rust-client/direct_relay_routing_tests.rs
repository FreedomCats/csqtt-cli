// П11 regression: a routing update that keeps IP routing on must not clear the
// live state of the direct path. An established direct TCP flow and a direct
// UDP session survive UPDATE_DOMAINS in the IP-only mode; only a full routing
// shutdown clears them, together with the П10 failed-address memory and the
// learned direct addresses. The domain map stays with domain routing and is
// still cleared when that mode is off. Only documentation addresses are used;
// the loopback UDP socket sends no datagram, and the non-SYN packet is only
// forwarded to the egress channel, so no outer connect starts.

use super::*;

/// The application side of a flow.
fn app(port: u16) -> SocketAddr {
    SocketAddr::new("10.66.67.2".parse().unwrap(), port)
}

/// A destination from the documentation ranges.
fn destination(ip: &str, port: u16) -> SocketAddr {
    SocketAddr::new(ip.parse().unwrap(), port)
}

/// The IP-only configuration the server rule enabled=0;ip_enabled=1
/// produces: domain routing is off, the direct IP path is on.
fn ip_only_config() -> crate::dispatcher::DomainRoutingConfig {
    crate::dispatcher::DomainRoutingConfig {
        enabled: false,
        ip_routing_enabled: true,
        ip_is_whitelist: false,
        ip_rules: Arc::new(vec![crate::sni::IpRule::Cidr(
            Ipv4Addr::new(192, 0, 2, 0),
            24,
        )]),
        ..Default::default()
    }
}

/// Builds the production relay with the given routing configuration and keeps
/// the inbound receiver alive: the egress path must stay open.
fn new_relay(
    config: crate::dispatcher::DomainRoutingConfig,
    pool: &Arc<PacketPool>,
) -> (DirectRelay, mpsc::Receiver<PacketBuf>) {
    let domain_routing = Arc::new(ArcSwap::from_pointee(config));
    let direct_ips = Arc::new(ArcSwap::from_pointee(std::collections::HashSet::new()));
    let dns_domain_map = Arc::new(crate::sni::DnsDomainMap::default());
    let (inbound_tx, inbound_rx) = mpsc::channel(DIRECT_RELAY_INBOUND_CAPACITY);
    let relay = DirectRelay::new(
        domain_routing,
        direct_ips,
        dns_domain_map,
        pool.clone(),
        inbound_tx,
    )
    .expect("the direct stack must initialize");
    (relay, inbound_rx)
}

/// Inserts the live state an IP-only update must keep and the state the update
/// may forget, and returns the TCP flow key.
fn fill_direct_state(
    relay: &DirectRelay,
    socket: Arc<tokio::net::UdpSocket>,
) -> (SocketAddr, SocketAddr) {
    let key = (app(54321), destination("192.0.2.10", 80));
    relay
        .active_tcp
        .write()
        .unwrap()
        .insert(key, std::time::Instant::now());
    relay
        .udp_sessions
        .write()
        .unwrap()
        .insert(app(54322), socket);
    relay
        .dns_domain_map
        .insert("192.0.2.10".parse().unwrap(), "example.com".to_string());
    remember_failed_direct(
        &relay.failed_direct_ips,
        "192.0.2.20".parse().unwrap(),
        std::time::Instant::now(),
        FAILED_DIRECT_MEMORY_TTL,
    );
    relay
        .direct_ips
        .store(Arc::new(std::collections::HashSet::from([
            "192.0.2.30".parse().unwrap(),
        ])));
    key
}

/// An IP-only update keeps the established TCP flow and the UDP session, the
/// П10 memory and the learned direct addresses; the domain map is rule state
/// and is still cleared with domain routing. A non-SYN packet of the flow
/// still takes the direct path, so an open direct connection is not broken by
/// the update.
#[tokio::test]
async fn ip_only_update_keeps_established_direct_state() {
    let pool = PacketPool::new(16);
    let (relay, _inbound_rx) = new_relay(ip_only_config(), &pool);
    let socket = Arc::new(
        tokio::net::UdpSocket::bind("127.0.0.1:0")
            .await
            .expect("the loopback UDP socket must bind"),
    );
    let key = fill_direct_state(&relay, socket.clone());

    relay.sync_domain_routing(&ip_only_config());

    assert!(
        relay.active_tcp.read().unwrap().contains_key(&key),
        "the established direct TCP flow must survive an IP-only update"
    );
    let kept = relay
        .udp_sessions
        .read()
        .unwrap()
        .get(&app(54322))
        .cloned();
    assert!(
        kept.is_some_and(|kept| Arc::ptr_eq(&socket, &kept)),
        "the direct UDP session must keep its socket across an IP-only update"
    );
    assert!(
        failed_direct_active(
            &relay.failed_direct_ips,
            "192.0.2.20".parse().unwrap(),
            std::time::Instant::now(),
        ),
        "the П10 failed-address memory must survive an IP-only update"
    );
    assert!(
        relay.direct_ips.load().contains(&"192.0.2.30".parse().unwrap()),
        "the learned direct address must survive an IP-only update"
    );
    assert!(
        relay
            .dns_domain_map
            .get(&"192.0.2.10".parse().unwrap())
            .is_none(),
        "the domain map stays with domain routing and is cleared with it"
    );

    let ack = build_tcp_packet(key.0, key.1, 1001, 0, 0x10, &[]).unwrap();
    let mut packet = pool.try_acquire().unwrap();
    packet
        .extend_tail(ack.len())
        .unwrap()
        .copy_from_slice(&ack);
    assert!(
        relay.try_handle(&packet).await,
        "an established direct flow must stay direct after an IP-only update"
    );
}

/// Turning the whole routing configuration off clears everything learned and
/// every live direct entry, as before; the flow is no longer established, so a
/// non-SYN packet of it returns false.
#[tokio::test]
async fn full_disable_clears_learned_direct_state() {
    let pool = PacketPool::new(16);
    let (relay, _inbound_rx) = new_relay(ip_only_config(), &pool);
    let socket = Arc::new(
        tokio::net::UdpSocket::bind("127.0.0.1:0")
            .await
            .expect("the loopback UDP socket must bind"),
    );
    let key = fill_direct_state(&relay, socket);

    relay.sync_domain_routing(&crate::dispatcher::DomainRoutingConfig {
        enabled: false,
        ip_routing_enabled: false,
        ..Default::default()
    });

    assert!(
        relay.active_tcp.read().unwrap().is_empty(),
        "a full disable clears the established direct TCP flows"
    );
    assert!(
        relay.udp_sessions.read().unwrap().is_empty(),
        "a full disable clears the direct UDP sessions"
    );
    assert!(
        relay.failed_direct_ips.read().unwrap().is_empty(),
        "a full disable clears the П10 failed-address memory"
    );
    assert!(
        relay.direct_ips.load().is_empty(),
        "a full disable clears the learned direct addresses"
    );
    assert!(
        relay
            .dns_domain_map
            .get(&"192.0.2.10".parse().unwrap())
            .is_none(),
        "a full disable clears the domain map with domain routing"
    );

    let ack = build_tcp_packet(key.0, key.1, 1001, 0, 0x10, &[]).unwrap();
    let mut packet = pool.try_acquire().unwrap();
    packet
        .extend_tail(ack.len())
        .unwrap()
        .copy_from_slice(&ack);
    assert!(
        !relay.try_handle(&packet).await,
        "a cleared direct flow must not take the direct path"
    );
}
