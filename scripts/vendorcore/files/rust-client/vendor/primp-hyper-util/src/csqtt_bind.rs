// Правка П3 csqtt-cli (doc/architecture.md, §06): привязка сокетов к
// физическому интерфейсу. Когда весь трафик системы уходит в туннель
// маршрутами /1, собственные сокеты ядра CSQTT должны идти мимо него — иначе
// туннель зависит сам от себя.
//
// Файл не из primp-hyper-util: его кладёт в core/ scripts/vendorcore из
// scripts/vendorcore/files, там его и править. Крейт рядом — нетронутый
// архив crates.io, правка к нему — правила П3 в scripts/vendorcore/rules.go.
//
// Настройка общая для процесса. Ядро ставит индекс и метку по флагам -bind-if
// и -mark и меняет индекс по команде BIND_IF:; `connect` в connect/http.rs
// привязывает к текущим значениям каждое новое соединение, ядро — тем же
// вызовом свои сокеты DNS и TURN. Ноль — привязки нет: поведение крейта
// прежнее.

//! Привязка сокетов к интерфейсу по индексу — правка csqtt-cli, не из
//! primp-hyper-util.

use std::io;
use std::sync::atomic::{AtomicU32, Ordering};

use socket2::SockRef;

static INDEX: AtomicU32 = AtomicU32::new(0);
static MARK: AtomicU32 = AtomicU32::new(0);

/// Индекс интерфейса для новых сокетов; 0 — без привязки.
pub fn set_index(index: u32) {
    INDEX.store(index, Ordering::Release);
}

/// Текущий индекс интерфейса; 0 — без привязки.
pub fn index() -> u32 {
    INDEX.load(Ordering::Acquire)
}

/// Метка `SO_MARK` для новых сокетов (только Linux); 0 — без метки.
pub fn set_mark(mark: u32) {
    MARK.store(mark, Ordering::Release);
}

/// Текущая метка; 0 — без метки.
pub fn mark() -> u32 {
    MARK.load(Ordering::Acquire)
}

/// Привязывает сокет к текущим индексу и метке, до `connect`. `ipv6` —
/// семейство сокета. Ошибка значит, что сокет непригоден: без привязки он
/// ушёл бы в туннель.
pub fn bind_socket(socket: SockRef<'_>, ipv6: bool) -> io::Result<()> {
    bind_socket_to(socket, ipv6, index(), mark())
}

/// То же, что [`bind_socket`], с явными индексом и меткой.
///
/// Linux — `SO_BINDTOIFINDEX` (на ядре старше 5.0 — `SO_BINDTODEVICE` по
/// имени) и `SO_MARK`; метке нужен `CAP_NET_ADMIN`. Windows —
/// `IP_UNICAST_IF` или `IPV6_UNICAST_IF`, метки нет.
pub fn bind_socket_to(socket: SockRef<'_>, ipv6: bool, index: u32, mark: u32) -> io::Result<()> {
    if index == 0 && mark == 0 {
        return Ok(());
    }
    sys::bind(&socket, ipv6, index, mark)
}

/// Ошибка опции: её имя и значение плюс ошибка ОС, вид ошибки — прежний.
fn option_error(error: io::Error, option: &str, value: u32) -> io::Error {
    io::Error::new(error.kind(), format!("{option}={value}: {error}"))
}

#[cfg(target_os = "linux")]
mod sys {
    use super::option_error;
    use socket2::Socket;
    use std::{ffi::CStr, io, os::fd::AsRawFd};

    pub(super) fn bind(socket: &Socket, _ipv6: bool, index: u32, mark: u32) -> io::Result<()> {
        if index != 0 {
            bind_to_index(socket, index)
                .map_err(|e| option_error(e, "SO_BINDTOIFINDEX", index))?;
        }
        if mark != 0 {
            setsockopt(socket, libc::SO_MARK, mark).map_err(|e| option_error(e, "SO_MARK", mark))?;
        }
        Ok(())
    }

    fn bind_to_index(socket: &Socket, index: u32) -> io::Result<()> {
        match setsockopt(socket, libc::SO_BINDTOIFINDEX, index) {
            Err(e) if e.raw_os_error() == Some(libc::ENOPROTOOPT) => {
                let mut name = [0 as libc::c_char; libc::IF_NAMESIZE];
                if unsafe { libc::if_indextoname(index, name.as_mut_ptr()) }.is_null() {
                    return Err(io::Error::last_os_error());
                }
                let name = unsafe { CStr::from_ptr(name.as_ptr()) };
                socket.bind_device(Some(name.to_bytes()))
            }
            result => result,
        }
    }

    fn setsockopt(socket: &Socket, option: libc::c_int, value: u32) -> io::Result<()> {
        let value = value as libc::c_int;
        let result = unsafe {
            libc::setsockopt(
                socket.as_raw_fd(),
                libc::SOL_SOCKET,
                option,
                (&value as *const libc::c_int).cast(),
                std::mem::size_of::<libc::c_int>() as libc::socklen_t,
            )
        };
        if result == 0 {
            Ok(())
        } else {
            Err(io::Error::last_os_error())
        }
    }
}

#[cfg(windows)]
mod sys {
    use super::option_error;
    use socket2::Socket;
    use std::{io, os::windows::io::AsRawSocket};

    const IPPROTO_IP: i32 = 0;
    const IPPROTO_IPV6: i32 = 41;
    const IP_UNICAST_IF: i32 = 31;
    const IPV6_UNICAST_IF: i32 = 31;

    // ws2_32 и так связан std; своя декларация вместо windows-sys — чтобы не
    // заводить ради одного вызова зависимость.
    #[link(name = "ws2_32")]
    unsafe extern "system" {
        fn setsockopt(s: usize, level: i32, optname: i32, optval: *const u8, optlen: i32) -> i32;
        fn WSAGetLastError() -> i32;
    }

    pub(super) fn bind(socket: &Socket, ipv6: bool, index: u32, mark: u32) -> io::Result<()> {
        if mark != 0 {
            return Err(io::Error::new(
                io::ErrorKind::Unsupported,
                format!("SO_MARK={mark}: меток сокетов на Windows нет"),
            ));
        }
        if index == 0 {
            return Ok(());
        }
        // Порядок байт — как в wireguard-go (conn/bind_windows.go): для IPv4
        // индекс в сетевом порядке, для IPv6 — в порядке хоста. Перепутанный
        // порядок Windows 11 отвергает, а старые сборки могут молча принять.
        let (level, option, name, value) = if ipv6 {
            (IPPROTO_IPV6, IPV6_UNICAST_IF, "IPV6_UNICAST_IF", index)
        } else {
            (IPPROTO_IP, IP_UNICAST_IF, "IP_UNICAST_IF", index.to_be())
        };
        let result = unsafe {
            setsockopt(
                socket.as_raw_socket() as usize,
                level,
                option,
                (&value as *const u32).cast(),
                std::mem::size_of::<u32>() as i32,
            )
        };
        if result == 0 {
            Ok(())
        } else {
            let error = io::Error::from_raw_os_error(unsafe { WSAGetLastError() });
            Err(option_error(error, name, index))
        }
    }
}

#[cfg(not(any(target_os = "linux", windows)))]
mod sys {
    use socket2::Socket;
    use std::io;

    pub(super) fn bind(_socket: &Socket, _ipv6: bool, _index: u32, _mark: u32) -> io::Result<()> {
        Err(io::Error::new(
            io::ErrorKind::Unsupported,
            "привязка сокета к интерфейсу по индексу есть только на Linux и Windows",
        ))
    }
}
