// Тесты правки П3 для сокетов маршрутизации мимо туннеля (точки 6–8, §06):
// прямые сокеты, которые открывает DirectRelay, с -bind-if привязаны к
// интерфейсу и помечены. Пакеты идут через try_handle, как от приложения, а
// сокеты берутся те, что создало ядро, — из сессий UDP и среди дескрипторов
// процесса. Если правило в rules.go перестанет действовать, сокет создаст
// tokio без привязки, и тест упадёт.
//
// Привязка — к lo: соединения к документационным адресам через него никуда
// не уходят и ответа не ждут, а сокеты живут, пока ждут (TCP — до таймаута
// 5 с, DNS — по 600 мс на сервер), — их видно. Общую настройку меняет
// configure, поэтому сценарий — в своём процессе, как isolated в
// bind_if_tests.rs; там же — помощники и запуск в пространстве имён для
// проверки метки (фильтр bind_if ловит и этот модуль).
//
// Без -bind-if сокеты — как у апстрима: здесь это проверено на UDP-сессии
// (порт назначения 0 — отправка отклоняется ещё в ОС, наружу ничего не
// уходит). TCP и DNS без привязки пошли бы в сеть, поэтому без -bind-if они
// не проверяются; что помощники без -bind-if сокет не трогают — isolated в
// bind_if_tests.rs.

use super::*;
use crate::bind_if::tests::{CHILD_ENV, MARK, binding, loopback_index, mark_allowed, notice};
use socket2::Type;
use std::os::fd::{AsRawFd, BorrowedFd, RawFd};

/// Адрес приложения в туннеле.
const APP: &str = "10.66.67.2";
/// Перехват DNS ядро делает, только если ни отправитель, ни адресат не
/// частные, поэтому у запроса DNS — публичный отправитель.
const APP_PUBLIC: &str = "198.51.100.2";
/// Прямые адреса — правило --direct-ips.
const DIRECT_NET: &str = "203.0.113.0/24";

#[test]
fn in_own_process() {
    crate::bind_if::tests::run_in_own_process("direct_relay::bind_if_tests::isolated");
}

#[test]
#[ignore = "идёт в своём процессе из in_own_process"]
fn isolated() {
    if std::env::var_os(CHILD_ENV).is_none() {
        notice("direct_relay isolated меняет общую настройку и запускается только из in_own_process");
        return;
    }
    let runtime = tokio::runtime::Builder::new_multi_thread()
        .enable_all()
        .build()
        .unwrap();
    runtime.block_on(isolated_scenario());
}

async fn isolated_scenario() {
    let lo = loopback_index();
    let marked = mark_allowed("direct_relay");
    let mark = if marked { MARK } else { 0 };
    let (relay, pool, _inbound) = relay();

    // Без -bind-if: UDP-сессия — без привязки, как у апстрима.
    let src = address(APP, 40000);
    send(&relay, &pool, build_udp_packet(src, address("203.0.113.9", 0), b"x")).await;
    assert_eq!(binding(session(&relay, src).as_raw_fd()), (0, 0), "UDP-сессия без -bind-if");

    crate::bind_if::configure(lo, mark).unwrap();

    // Точка 7 — UDP-сессия.
    let src = address(APP, 40001);
    send(&relay, &pool, build_udp_packet(src, address("203.0.113.9", 9), b"x")).await;
    assert_eq!(binding(session(&relay, src).as_raw_fd()), (lo, mark), "UDP-сессия");

    // Точка 8 — DNS-запрос, который ядро перехватывает и задаёт само.
    let before = sockets();
    let query = build_udp_packet(address(APP_PUBLIC, 40002), address("203.0.113.53", 53), &dns_query());
    assert!(send(&relay, &pool, query).await, "DNS-запрос не перехвачен");
    let fd = new_socket(&before, Type::DGRAM).await;
    assert_bound(fd, (lo, mark), "DNS прямого домена").await;

    // Точка 6 — прямое TCP-соединение: стек smoltcp принимает SYN, ядро
    // соединяется с адресатом.
    let before = sockets();
    let syn = build_tcp_packet(address(APP, 40003), address("203.0.113.80", 80), 1000, 0, 0x02, &[]);
    assert!(send(&relay, &pool, syn).await, "SYN не принят напрямую");
    let fd = new_socket(&before, Type::STREAM).await;
    assert_bound(fd, (lo, mark), "прямое TCP-соединение").await;
}

/// DirectRelay с маршрутизацией по адресам: DIRECT_NET — напрямую. По
/// доменам — выключена: так ядро не разрешает домены заранее и не
/// открывает лишних сокетов. Третье — приёмник ответов стека и сессий: тесту
/// они не нужны, но канал должен жить.
fn relay() -> (DirectRelay, Arc<PacketPool>, mpsc::Receiver<PacketBuf>) {
    let domain_routing = Arc::new(ArcSwap::from_pointee(crate::dispatcher::DomainRoutingConfig {
        ip_routing_enabled: true,
        ip_rules: crate::sni::parse_ip_rules(DIRECT_NET).into(),
        ..Default::default()
    }));
    let direct_ips = Arc::new(ArcSwap::from_pointee(std::collections::HashSet::new()));
    let dns_domain_map = Arc::new(crate::sni::DnsDomainMap::default());
    let pool = PacketPool::new(64);
    let (inbound_tx, inbound_rx) = mpsc::channel(DIRECT_RELAY_INBOUND_CAPACITY);
    let relay = DirectRelay::new(domain_routing, direct_ips, dns_domain_map, pool.clone(), inbound_tx)
        .expect("стек smoltcp");
    (relay, pool, inbound_rx)
}

fn address(ip: &str, port: u16) -> SocketAddr {
    SocketAddr::new(ip.parse().unwrap(), port)
}

/// Пакет от приложения — в try_handle.
async fn send(relay: &DirectRelay, pool: &Arc<PacketPool>, packet: Option<Vec<u8>>) -> bool {
    let packet = packet.expect("пакет не собрался");
    let mut buf = pool.try_acquire().unwrap();
    buf.extend_tail(packet.len()).unwrap().copy_from_slice(&packet);
    relay.try_handle(&buf).await
}

/// Сокет UDP-сессии приложения src.
fn session(relay: &DirectRelay, src: SocketAddr) -> Arc<tokio::net::UdpSocket> {
    relay
        .udp_sessions
        .read()
        .unwrap()
        .get(&src)
        .cloned()
        .unwrap_or_else(|| panic!("нет UDP-сессии для {src}"))
}

/// Запрос A для example.com.
fn dns_query() -> Vec<u8> {
    let mut query = vec![0x12, 0x34, 0x01, 0x00, 0, 1, 0, 0, 0, 0, 0, 0];
    for label in ["example", "com"] {
        query.push(label.len() as u8);
        query.extend_from_slice(label.as_bytes());
    }
    query.extend_from_slice(&[0, 0, 1, 0, 1]);
    query
}

/// Сокеты процесса: inode → дескриптор.
fn sockets() -> HashMap<u64, RawFd> {
    std::fs::read_dir("/proc/self/fd")
        .unwrap()
        .filter_map(|entry| {
            let entry = entry.ok()?;
            let fd = entry.file_name().to_str()?.parse().ok()?;
            let target = std::fs::read_link(entry.path()).ok()?;
            let inode = target.to_str()?.strip_prefix("socket:[")?.strip_suffix(']')?.parse().ok()?;
            Some((inode, fd))
        })
        .collect()
}

/// Сокет IPv4 вида kind, которого не было в before: ядро создаёт его в своей
/// задаче, поэтому — с ожиданием.
async fn new_socket(before: &HashMap<u64, RawFd>, kind: Type) -> RawFd {
    let deadline = std::time::Instant::now() + Duration::from_secs(3);
    loop {
        let found = sockets().into_iter().find_map(|(inode, fd)| {
            if before.contains_key(&inode) {
                return None;
            }
            let borrowed = unsafe { BorrowedFd::borrow_raw(fd) };
            let socket = SockRef::from(&borrowed);
            let ipv4 = socket.local_addr().ok()?.as_socket()?.is_ipv4();
            (ipv4 && socket.r#type().ok()? == kind).then_some(fd)
        });
        if let Some(fd) = found {
            return fd;
        }
        assert!(std::time::Instant::now() < deadline, "ядро не создало сокет {kind:?} за 3 с");
        tokio::time::sleep(Duration::from_millis(10)).await;
    }
}

/// Привязка сокета fd — want. Сокет найден сразу после создания, а
/// привязку помощник ставит следующим вызовом, поэтому — с ожиданием; сокет
/// без привязки её так и не получит.
async fn assert_bound(fd: RawFd, want: (u32, u32), what: &str) {
    let deadline = std::time::Instant::now() + Duration::from_secs(1);
    loop {
        let got = binding(fd);
        if got == want {
            return;
        }
        assert!(std::time::Instant::now() < deadline, "{what}: привязка {got:?}, ожидалась {want:?}");
        tokio::time::sleep(Duration::from_millis(5)).await;
    }
}
