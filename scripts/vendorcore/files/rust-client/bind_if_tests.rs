// Тесты правки П3 (bind_if.rs и csqtt_bind в primp-hyper-util): после
// создания сокета привязка стоит — это видно getsockopt.
//
// Общая настройка — на весь процесс, а тесты ядра идут параллельно: сокет
// соседнего теста, привязанный не к тому интерфейсу, не дошёл бы до своего
// сервера на loopback. Поэтому всё, что меняет настройку, идёт в отдельном
// процессе — этот же тестовый бинарь запускает сам себя на тесте isolated.
//
// Метке нужен CAP_NET_ADMIN. Без него проверяется только, что отказ виден, а
// о пропуске полной проверки тест пишет мимо захвата вывода. Полная проверка —
// в непривилегированном пространстве имён user+net, где права есть (lo там
// выключен — его нужно поднять):
//
//     unshare -rn sh -c 'ip link set lo up && <тестовый бинарь ядра> bind_if'

use super::*;
#[cfg(target_os = "linux")]
use std::os::fd::{AsRawFd, BorrowedFd, RawFd};
#[cfg(target_os = "linux")]
use std::io::Write;

/// Метка для тестов.
const MARK: u32 = 0x5153;

/// Индекс, которого нет ни у одного интерфейса.
#[cfg(target_os = "linux")]
const MISSING_INDEX: u32 = 0x7fff_fff0;

/// Переменная окружения процесса, в котором идёт isolated.
#[cfg(target_os = "linux")]
const CHILD_ENV: &str = "CSQTT_BIND_IF_TEST_CHILD";

/// Строка о пропуске — мимо захвата вывода тестов, чтобы её было видно.
#[cfg(target_os = "linux")]
fn notice(text: &str) {
    let _ = writeln!(std::io::stderr(), "bind_if: {text}");
}

#[cfg(target_os = "linux")]
fn loopback_index() -> u32 {
    let index = unsafe { libc::if_nametoindex(c"lo".as_ptr()) };
    assert_ne!(index, 0, "нет интерфейса lo");
    index
}

#[cfg(windows)]
fn loopback_index() -> u32 {
    // Loopback Pseudo-Interface 1.
    1
}

#[cfg(target_os = "linux")]
fn socket_option(fd: RawFd, option: libc::c_int) -> u32 {
    let mut value: libc::c_int = 0;
    let mut length = std::mem::size_of::<libc::c_int>() as libc::socklen_t;
    let result = unsafe {
        libc::getsockopt(
            fd,
            libc::SOL_SOCKET,
            option,
            (&mut value as *mut libc::c_int).cast(),
            &mut length,
        )
    };
    assert_eq!(result, 0, "getsockopt: {}", io::Error::last_os_error());
    value as u32
}

/// Индекс, к которому привязан сокет, и его метка.
#[cfg(target_os = "linux")]
fn binding(fd: RawFd) -> (u32, u32) {
    (
        socket_option(fd, libc::SO_BINDTOIFINDEX),
        socket_option(fd, libc::SO_MARK),
    )
}

/// Разрешает ли ядро ставить метку. Нет — тест пишет, что пропустил.
#[cfg(target_os = "linux")]
fn mark_allowed(test: &str) -> bool {
    let socket = Socket::new(Domain::IPV4, Type::DGRAM, None).unwrap();
    match SockRef::from(&socket).set_mark(MARK) {
        Ok(()) => true,
        Err(error) if error.kind() == io::ErrorKind::PermissionDenied => {
            notice(&format!(
                "{test}: пропуск проверки метки — нет CAP_NET_ADMIN ({error}); \
                 проверено только, что отказ виден. Полная проверка: \
                 unshare -rn sh -c 'ip link set lo up && <тестовый бинарь> bind_if'"
            ));
            false
        }
        Err(error) => panic!("SO_MARK на пробном сокете: {error}"),
    }
}

// --- без общей настройки ----------------------------------------------------

#[cfg(target_os = "linux")]
#[test]
fn explicit_binding_sets_index_and_mark() {
    let lo = loopback_index();
    let marked = mark_allowed("explicit_binding_sets_index_and_mark");
    let mark = if marked { MARK } else { 0 };

    let v4 = Socket::new(Domain::IPV4, Type::DGRAM, None).unwrap();
    csqtt_bind::bind_socket_to(SockRef::from(&v4), false, lo, mark).unwrap();
    assert_eq!(binding(v4.as_raw_fd()), (lo, mark));

    match Socket::new(Domain::IPV6, Type::STREAM, None) {
        Ok(v6) => {
            csqtt_bind::bind_socket_to(SockRef::from(&v6), true, lo, mark).unwrap();
            assert_eq!(binding(v6.as_raw_fd()), (lo, mark));
        }
        Err(error) => notice(&format!("IPv6 недоступен, сокет IPv6 не проверен: {error}")),
    }

    if !marked {
        let socket = Socket::new(Domain::IPV4, Type::DGRAM, None).unwrap();
        let error = csqtt_bind::bind_socket_to(SockRef::from(&socket), false, lo, MARK).unwrap_err();
        assert_eq!(error.kind(), io::ErrorKind::PermissionDenied, "{error}");
        assert!(error.to_string().contains("SO_MARK"), "{error}");
    }
}

// SO_BINDTOIFINDEX индекс не проверяет, но сокет, привязанный к
// несуществующему интерфейсу, никуда не шлёт: мимо привязки он не уйдёт.
#[cfg(target_os = "linux")]
#[test]
fn socket_bound_to_missing_interface_sends_nothing() {
    let socket = std::net::UdpSocket::bind("127.0.0.1:0").unwrap();
    csqtt_bind::bind_socket_to(SockRef::from(&socket), false, MISSING_INDEX, 0).unwrap();
    assert_eq!(binding(socket.as_raw_fd()).0, MISSING_INDEX);
    let error = socket.send_to(b"x", socket.local_addr().unwrap()).unwrap_err();
    assert_eq!(error.raw_os_error(), Some(libc::ENODEV), "{error}");
}

#[test]
fn zero_index_and_mark_leave_socket_alone() {
    let socket = Socket::new(Domain::IPV4, Type::DGRAM, None).unwrap();
    csqtt_bind::bind_socket_to(SockRef::from(&socket), false, 0, 0).unwrap();
    #[cfg(target_os = "linux")]
    assert_eq!(binding(socket.as_raw_fd()), (0, 0));
}

// -mark без -bind-if отклоняется до всякой настройки. Остальные отказы
// configure — в isolated: удачный вызов поменял бы общую настройку.
#[test]
fn configure_refuses_mark_without_bind_if() {
    let error = configure(0, MARK).unwrap_err().to_string();
    assert!(error.contains("-mark") && error.contains("без -bind-if"), "{error}");
}

#[cfg(windows)]
#[test]
fn unicast_if_byte_order() {
    use std::os::windows::io::AsRawSocket;

    #[link(name = "ws2_32")]
    unsafe extern "system" {
        fn getsockopt(s: usize, level: i32, optname: i32, optval: *mut u8, optlen: *mut i32) -> i32;
    }
    fn unicast_if(socket: &Socket, ipv6: bool) -> u32 {
        let (level, option) = if ipv6 { (41, 31) } else { (0, 31) };
        let mut value = 0u32;
        let mut length = std::mem::size_of::<u32>() as i32;
        let result = unsafe {
            getsockopt(
                socket.as_raw_socket() as usize,
                level,
                option,
                (&mut value as *mut u32).cast(),
                &mut length,
            )
        };
        assert_eq!(result, 0, "getsockopt: {}", io::Error::last_os_error());
        value
    }

    let lo = loopback_index();
    let v4 = Socket::new(Domain::IPV4, Type::DGRAM, None).unwrap();
    csqtt_bind::bind_socket_to(SockRef::from(&v4), false, lo, 0).unwrap();
    assert_eq!(unicast_if(&v4, false), lo.to_be(), "IPv4: индекс в сетевом порядке");
    let v6 = Socket::new(Domain::IPV6, Type::DGRAM, None).unwrap();
    csqtt_bind::bind_socket_to(SockRef::from(&v6), true, lo, 0).unwrap();
    assert_eq!(unicast_if(&v6, true), lo, "IPv6: индекс в порядке хоста");

    let error = csqtt_bind::bind_socket_to(SockRef::from(&v4), false, lo, MARK).unwrap_err();
    assert_eq!(error.kind(), io::ErrorKind::Unsupported, "{error}");
}

// --- с общей настройкой: в своём процессе -----------------------------------

#[cfg(target_os = "linux")]
#[test]
fn in_own_process() {
    let output = std::process::Command::new(std::env::current_exe().unwrap())
        .args(["--exact", "bind_if::tests::isolated", "--ignored", "--nocapture", "--test-threads=1"])
        .env(CHILD_ENV, "1")
        .output()
        .unwrap();
    let stdout = String::from_utf8_lossy(&output.stdout);
    let stderr = String::from_utf8_lossy(&output.stderr);
    // Строки о пропуске — дальше, мимо захвата; остальное — на случай провала.
    for line in stderr.lines().filter(|line| line.starts_with("bind_if: ")) {
        notice(line.trim_start_matches("bind_if: "));
    }
    assert!(
        output.status.success() && stdout.contains("test bind_if::tests::isolated ... ok"),
        "isolated в своём процессе: {}\n{stdout}\n{stderr}",
        output.status
    );
}

#[cfg(target_os = "linux")]
#[test]
#[ignore = "идёт в своём процессе из in_own_process"]
fn isolated() {
    if std::env::var_os(CHILD_ENV).is_none() {
        notice("isolated меняет общую настройку и запускается только из in_own_process");
        return;
    }
    let runtime = tokio::runtime::Builder::new_multi_thread()
        .enable_all()
        .build()
        .unwrap();
    runtime.block_on(isolated_scenario());
}

#[cfg(target_os = "linux")]
async fn isolated_scenario() {
    let lo = loopback_index();
    let marked = mark_allowed("isolated");
    let mark = if marked { MARK } else { 0 };

    // Без -bind-if сокеты не трогаются, BIND_IF: отклоняется.
    let socket = udp_socket("0.0.0.0:0".parse().unwrap()).await.unwrap();
    assert_eq!(binding(socket.as_raw_fd()), (0, 0));
    command(&lo.to_string());
    assert_eq!(csqtt_bind::index(), 0, "BIND_IF без -bind-if");

    // Неверные флаги — отказ, настройка прежняя.
    let error = configure(MISSING_INDEX, 0).unwrap_err().to_string();
    assert!(error.contains("-bind-if") && error.contains("нет интерфейса"), "{error}");
    if !marked {
        let error = configure(lo, MARK).unwrap_err().to_string();
        assert!(error.contains("CAP_NET_ADMIN"), "{error}");
    }
    assert_eq!((csqtt_bind::index(), csqtt_bind::mark()), (0, 0));
    assert!(!ENABLED.load(Ordering::Acquire));

    configure(lo, mark).unwrap();
    assert_eq!((csqtt_bind::index(), csqtt_bind::mark()), (lo, mark));

    // Точки 1 и 2.
    let v4 = udp_socket("0.0.0.0:0".parse().unwrap()).await.unwrap();
    assert_eq!(binding(v4.as_raw_fd()), (lo, mark), "UDP IPv4");
    match udp_socket("[::]:0".parse().unwrap()).await {
        Ok(v6) => assert_eq!(binding(v6.as_raw_fd()), (lo, mark), "UDP IPv6"),
        Err(error) => notice(&format!("IPv6 недоступен, UDP IPv6 не проверен: {error}")),
    }

    // Точка 3.
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let stream = connect_tcp(listener.local_addr().unwrap()).await.unwrap();
    assert_eq!(binding(stream.as_raw_fd()), (lo, mark), "TCP");

    // Точки 4 и 5: соединение клиента primp — от пропатченного коннектора.
    // Если [patch.crates-io] не действует, сокет окажется без привязки.
    let client = primp_client();
    let (url, seen) = http_server_reporting_client_binding();
    let response = client.get(&url).send().await.unwrap();
    assert_eq!(response.status().as_u16(), 200);
    assert_eq!(seen.recv().unwrap(), (lo, mark), "соединение primp");

    // BIND_IF: — неверный индекс отклоняется, верный берут новые сокеты.
    for bad in ["abc", "0", "", &MISSING_INDEX.to_string()] {
        command(bad);
        assert_eq!(csqtt_bind::index(), lo, "BIND_IF:{bad}");
    }
    match other_interface(lo) {
        Some((name, other)) => {
            command(&format!(" {other} "));
            assert_eq!(csqtt_bind::index(), other, "BIND_IF:{other}");
            let socket = udp_socket("0.0.0.0:0".parse().unwrap()).await.unwrap();
            assert_eq!(binding(socket.as_raw_fd()), (other, mark), "UDP после BIND_IF:{other} ({name})");
            command(&lo.to_string());
            assert_eq!(csqtt_bind::index(), lo);
        }
        None => notice("кроме lo интерфейсов нет — смена индекса по BIND_IF: не проверена"),
    }

    // Интерфейс пропал после BIND_IF: ни один сокет не уходит мимо привязки —
    // ядро ОС не шлёт через несуществующий интерфейс (ENODEV).
    csqtt_bind::set_index(MISSING_INDEX);
    let socket = udp_socket("127.0.0.1:0".parse().unwrap()).await.unwrap();
    let error = socket.send_to(b"x", socket.local_addr().unwrap()).await.unwrap_err();
    assert_eq!(error.raw_os_error(), Some(libc::ENODEV), "UDP: {error}");
    let error = connect_tcp(listener.local_addr().unwrap()).await.unwrap_err();
    assert_eq!(error.raw_os_error(), Some(libc::ENODEV), "TCP: {error}");
    let error = primp_client().get(&url).send().await.unwrap_err();
    assert!(format!("{error:?}").contains("code: 19"), "primp: {error:?}");
    assert!(seen.try_recv().is_err(), "соединение primp дошло до сервера");
}

/// Клиент как в точке 5: с отпечатком браузера.
#[cfg(target_os = "linux")]
fn primp_client() -> primp::Client {
    let profile = crate::profiles::random_profile("chrome");
    client_builder()
        .impersonate(profile.impersonate)
        .impersonate_os(profile.os)
        .timeout(std::time::Duration::from_secs(10))
        .build()
        .unwrap()
}

/// HTTP-сервер на loopback, который на каждое соединение находит сокет
/// клиента в этом же процессе (по адресу — среди /proc/self/fd) и сообщает
/// его привязку.
#[cfg(target_os = "linux")]
fn http_server_reporting_client_binding() -> (String, std::sync::mpsc::Receiver<(u32, u32)>) {
    use std::io::Read;

    let listener = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
    let url = format!("http://{}/", listener.local_addr().unwrap());
    let (report, seen) = std::sync::mpsc::channel();
    std::thread::spawn(move || {
        for stream in listener.incoming() {
            let Ok(mut stream) = stream else { return };
            let client = stream.peer_addr().unwrap();
            let _ = report.send(client_socket(client).map(binding).unwrap_or((u32::MAX, u32::MAX)));
            let mut request = [0u8; 4096];
            let _ = stream.read(&mut request);
            let _ = stream.write_all(b"HTTP/1.1 200 OK\r\nContent-Length: 0\r\nConnection: close\r\n\r\n");
        }
    });
    (url, seen)
}

/// Дескриптор сокета этого процесса с локальным адресом address.
#[cfg(target_os = "linux")]
fn client_socket(address: SocketAddr) -> Option<RawFd> {
    std::fs::read_dir("/proc/self/fd").ok()?.find_map(|entry| {
        let fd: RawFd = entry.ok()?.file_name().to_str()?.parse().ok()?;
        let borrowed = unsafe { BorrowedFd::borrow_raw(fd) };
        let local = SockRef::from(&borrowed).local_addr().ok()?.as_socket()?;
        (local == address).then_some(fd)
    })
}

/// Другой интерфейс этого пространства имён, кроме lo: имя и индекс.
#[cfg(target_os = "linux")]
fn other_interface(lo: u32) -> Option<(String, u32)> {
    let list = unsafe { libc::if_nameindex() };
    if list.is_null() {
        return None;
    }
    let mut found = None;
    let mut entry = list;
    unsafe {
        while (*entry).if_index != 0 {
            if (*entry).if_index != lo {
                let name = std::ffi::CStr::from_ptr((*entry).if_name);
                found = Some((name.to_string_lossy().into_owned(), (*entry).if_index));
                break;
            }
            entry = entry.add(1);
        }
        libc::if_freenameindex(list);
    }
    found
}
