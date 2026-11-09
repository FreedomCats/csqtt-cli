// Regression test for the П9 fix: with `-tun-uds` and no registered worker,
// `read_tun` must still hand a direct packet to the direct stack. The test
// starts the production TUN UDS path, passes one end of a Unix datagram pair
// through the abstract socket, and waits for the SYN-ACK written back to the
// same pair.
//
// The production listener answers the direct SYN with a SYN-ACK and then starts
// the outer connect. When that connect fails at once (no route, a reject), the
// П10 failure handler closes the flow with a reset written to the same TUN, in
// addition to the SYN-ACK. The test reads the TUN until it sees the SYN-ACK, so
// the reset does not matter whichever way the two are delivered, and it only
// requires `direct_bytes_down` to be at least the SYN-ACK length.

use super::*;
use std::io::IoSlice;
use std::os::fd::{AsRawFd, FromRawFd, IntoRawFd, OwnedFd};
use std::os::unix::net::UnixDatagram as StdUnixDatagram;
use std::sync::atomic::AtomicI64;

use nix::sys::socket::{
    AddressFamily, ControlMessage, MsgFlags, SockFlag, SockType, UnixAddr, connect, sendmsg, socket,
};

/// Sends `file` to the abstract UDS `name` and waits for the one-byte ack
/// `FdReceiver::receive` writes back. Blocking: call it from `spawn_blocking`.
fn send_fd_and_wait_ack(name: &str, file: &File) -> OwnedFd {
    let socket = socket(
        AddressFamily::Unix,
        SockType::Stream,
        SockFlag::SOCK_CLOEXEC,
        None,
    )
    .expect("UDS sender socket");
    connect(
        socket.as_raw_fd(),
        &UnixAddr::new_abstract(name.as_bytes()).expect("abstract name"),
    )
    .expect("connect to TUN FD listener");
    let payload = [1u8];
    let slices = [IoSlice::new(&payload)];
    let controls = [ControlMessage::ScmRights(&[file.as_raw_fd()])];
    sendmsg::<UnixAddr>(
        socket.as_raw_fd(),
        &slices,
        &controls,
        MsgFlags::empty(),
        None,
    )
    .expect("send TUN FD");

    let mut pollfd = libc::pollfd {
        fd: socket.as_raw_fd(),
        events: libc::POLLIN,
        revents: 0,
    };
    let ready = unsafe { libc::poll(&mut pollfd, 1, 1000) };
    assert_eq!(ready, 1, "TUN FD ack did not arrive within 1s");
    let mut ack = [0u8; 1];
    let received = unsafe { libc::recv(socket.as_raw_fd(), ack.as_mut_ptr().cast(), ack.len(), 0) };
    assert_eq!(received, 1, "TUN FD ack read failed");
    assert_eq!(ack[0], 1, "TUN FD ack byte");
    socket
}

/// True when the datagram is an IPv4/TCP packet whose flags byte (at
/// `ihl + 13`) is exactly SYN-ACK (`0x12`). Bounds are checked, so a malformed
/// or non-TCP datagram is simply not a SYN-ACK.
fn is_syn_ack(datagram: &[u8]) -> bool {
    if datagram.len() < 20 || datagram[0] >> 4 != 4 || datagram[9] != 6 {
        return false;
    }
    let ihl = (datagram[0] & 0x0f) as usize * 4;
    datagram.len() > ihl + 13 && datagram[ihl + 13] == 0x12
}

/// Asserts the datagram is an IPv4/TCP SYN-ACK from `192.0.2.10:80` to
/// `10.0.0.2:54321`.
fn assert_syn_ack(datagram: &[u8]) {
    assert!(datagram.len() >= 20, "short IPv4 datagram");
    assert_eq!(datagram[0] >> 4, 4, "not IPv4");
    assert_eq!(datagram[9], 6, "not TCP");
    let ihl = (datagram[0] & 0x0f) as usize * 4;
    assert!(ihl >= 20 && datagram.len() >= ihl + 20, "short TCP header");
    assert_eq!(&datagram[12..16], &[192, 0, 2, 10], "source address");
    assert_eq!(&datagram[16..20], &[10, 0, 0, 2], "destination address");
    assert_eq!(
        u16::from_be_bytes([datagram[ihl], datagram[ihl + 1]]),
        80,
        "source port"
    );
    assert_eq!(
        u16::from_be_bytes([datagram[ihl + 2], datagram[ihl + 3]]),
        54321,
        "destination port"
    );
    assert_eq!(datagram[ihl + 13], 0x12, "SYN-ACK flags");
}

/// Waits for a bounded time for the counter to reach `expected`. The read loop
/// credits the direct SYN after the SYN-ACK may already have been written, so
/// the test polls instead of assuming the ordering.
async fn wait_for_counter(counter: &AtomicI64, expected: i64) {
    let deadline = tokio::time::Instant::now() + Duration::from_secs(2);
    loop {
        if counter.load(Ordering::Relaxed) == expected {
            return;
        }
        assert!(
            tokio::time::Instant::now() < deadline,
            "counter did not reach {expected} within 2s"
        );
        tokio::time::sleep(Duration::from_millis(10)).await;
    }
}

/// Same as `wait_for_counter`, but accepts any value at or above `minimum`.
/// A failed outer connect may add a reset to `direct_bytes_down` before the
/// read loop credits the SYN-ACK, so an exact match would be missed.
async fn wait_for_counter_at_least(counter: &AtomicI64, minimum: i64) {
    let deadline = tokio::time::Instant::now() + Duration::from_secs(2);
    loop {
        if counter.load(Ordering::Relaxed) >= minimum {
            return;
        }
        assert!(
            tokio::time::Instant::now() < deadline,
            "counter did not reach at least {minimum} within 2s"
        );
        tokio::time::sleep(Duration::from_millis(10)).await;
    }
}

#[tokio::test]
async fn direct_syn_is_answered_with_zero_workers() {
    let (test_tun, dispatcher_tun) = StdUnixDatagram::pair().expect("test TUN pair");
    test_tun.set_nonblocking(true).expect("test TUN nonblocking");
    dispatcher_tun
        .set_nonblocking(true)
        .expect("dispatcher TUN nonblocking");
    let dispatcher_file = unsafe { File::from_raw_fd(dispatcher_tun.into_raw_fd()) };

    let name = format!(
        "csqtt-tun-direct-test-{}-{}",
        std::process::id(),
        rand::random::<u64>()
    );
    let pool = PacketPool::new(32);
    let stats = Arc::new(Stats::default());
    let cancel = CancellationToken::new();
    let routing = DomainRoutingConfig {
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
    };
    let (dispatcher, _) = Dispatcher::start_with_domain_routing(
        "127.0.0.1:0",
        None,
        Some(name.clone()),
        pool,
        stats.clone(),
        cancel,
        routing,
        0,
    )
    .await
    .expect("production TUN UDS path");
    assert_eq!(dispatcher.active_count(), 0);

    let _ack_socket = tokio::task::spawn_blocking(move || {
        send_fd_and_wait_ack(&name, &dispatcher_file)
    })
    .await
    .expect("TUN FD send task");

    let test_tun = tokio::net::UnixDatagram::from_std(test_tun).expect("tokio test TUN");
    let tunnel_syn = crate::direct_relay::build_tcp_packet(
        "10.0.0.2:54321".parse().unwrap(),
        "198.51.100.10:80".parse().unwrap(),
        1000,
        0,
        0x02,
        &[],
    )
    .expect("tunnel-bound SYN");
    let direct_syn = crate::direct_relay::build_tcp_packet(
        "10.0.0.2:54321".parse().unwrap(),
        "192.0.2.10:80".parse().unwrap(),
        1000,
        0,
        0x02,
        &[],
    )
    .expect("direct SYN");

    test_tun
        .send(&tunnel_syn)
        .await
        .expect("send tunnel-bound SYN");
    test_tun.send(&direct_syn).await.expect("send direct SYN");

    // The failed outer connect may write an application-facing reset to the
    // same TUN, so read past anything that is not the SYN-ACK. The whole loop
    // stays under one 5 s deadline.
    let deadline = tokio::time::Instant::now() + Duration::from_secs(5);
    let mut buf = [0u8; 2048];
    let received = loop {
        let remaining = deadline.saturating_duration_since(tokio::time::Instant::now());
        let received = match tokio::time::timeout(remaining, test_tun.recv(&mut buf)).await {
            Ok(Ok(received)) => received,
            Ok(Err(error)) => panic!("read test TUN: {error}"),
            Err(_) => panic!("no SYN-ACK on the test TUN within 5s"),
        };
        if is_syn_ack(&buf[..received]) {
            break received;
        }
    };
    assert_syn_ack(&buf[..received]);

    wait_for_counter(&stats.direct_bytes_up, direct_syn.len() as i64).await;
    assert_eq!(stats.total_bytes_up.load(Ordering::Relaxed), 0);
    wait_for_counter_at_least(&stats.direct_bytes_down, received as i64).await;
    assert_eq!(stats.total_bytes_down.load(Ordering::Relaxed), 0);

    dispatcher.shutdown().await;
}
