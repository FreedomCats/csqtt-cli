// Правка П3 csqtt-cli (doc/architecture.md, Р3 и §06): собственные сокеты
// ядра — мимо туннеля, через физический интерфейс. Когда tun забирает весь
// трафик маршрутами /1, сокеты ядра к VK, DNS и TURN без привязки ушли бы в
// тот же туннель.
//
// Файл не из апстрима: его кладёт в core/ scripts/vendorcore из
// scripts/vendorcore/files, там его и править.
//
// -bind-if <индекс> — интерфейс для всех новых сокетов ядра; -mark <метка> —
// ещё и SO_MARK на них, только Linux (защита DNS и kill-switch пропускают
// ядро по метке, §09б). Команда BIND_IF:<индекс> меняет интерфейс на ходу:
// его берут новые сокеты, старые доживают своё.
//
// Пять точек (§06): DNS (dns.rs) и TURN по UDP (turn.rs) берут сокет из
// udp_socket, TURN по TCP (turn_stream.rs) — из connect_tcp, клиенты primp
// (auth.rs, vk_js_calls.rs) строятся client_builder, а их соединения
// привязывает пропатченный коннектор primp-hyper-util. Настройка у всех одна —
// csqtt_bind в этом крейте. Новые места ловит сторож в scripts/vendorcore.
//
// Без -bind-if ничего не меняется. С ним привязка обязательна: сокет, который
// не удалось привязать, не используется — ошибка уходит вызывающему и в
// журнал, а неверный флаг — отказ стартовать.

use anyhow::{Result, anyhow, bail};
use primp_hyper_util::csqtt_bind;
use socket2::{Domain, SockRef, Socket, Type};
use std::{
    io,
    net::SocketAddr,
    sync::atomic::{AtomicBool, Ordering},
};
use tokio::net::{TcpSocket, TcpStream, UdpSocket};

/// Ядро запущено с -bind-if: привязка обязательна, прокси из окружения не
/// берутся.
static ENABLED: AtomicBool = AtomicBool::new(false);

/// Флаги -bind-if и -mark: проверяет их пробным сокетом и ставит общую
/// настройку. Ошибка — отказ стартовать.
pub(crate) fn configure(index: u32, mark: u32) -> Result<()> {
    if index == 0 {
        if mark != 0 {
            bail!("[П3] -mark {mark} без -bind-if: метка ставится только вместе с привязкой");
        }
        return Ok(());
    }
    if mark != 0 && !cfg!(target_os = "linux") {
        bail!("[П3] -mark {mark}: метка сокетов есть только на Linux");
    }
    probe(index, mark).map_err(|error| anyhow!("[П3] -bind-if {index}: {}", explain(&error)))?;
    csqtt_bind::set_mark(mark);
    csqtt_bind::set_index(index);
    ENABLED.store(true, Ordering::Release);
    if mark != 0 {
        crate::log_error!("[П3] Сокеты ядра — через интерфейс {index}, метка {mark}");
    } else {
        crate::log_error!("[П3] Сокеты ядра — через интерфейс {index}");
    }
    Ok(())
}

/// Команда BIND_IF:<индекс>. Неверный индекс или непригодный интерфейс —
/// отказ в журнале; сокеты остаются на прежнем интерфейсе.
pub(crate) fn command(value: &str) {
    if !ENABLED.load(Ordering::Acquire) {
        crate::log_error!("[П3] BIND_IF отклонён: ядро запущено без -bind-if");
        return;
    }
    let index = match value.trim().parse::<u32>() {
        Ok(index) if index != 0 => index,
        _ => {
            crate::log_error!("[П3] BIND_IF отклонён: {value:?} — не индекс интерфейса");
            return;
        }
    };
    let previous = csqtt_bind::index();
    match probe(index, csqtt_bind::mark()) {
        Ok(()) => {
            csqtt_bind::set_index(index);
            crate::log_error!("[П3] Интерфейс {previous} → {index}: новые сокеты — через него");
        }
        Err(error) => crate::log_error!(
            "[П3] BIND_IF:{index} отклонён, сокеты остаются на интерфейсе {previous}: {}",
            explain(&error)
        ),
    }
}

/// Годятся ли индекс и метка: интерфейс есть, пробный сокет привязывается.
/// Сокет — только IPv4: интерфейс без IPv6 — не повод отказать.
fn probe(index: u32, mark: u32) -> io::Result<()> {
    // SO_BINDTOIFINDEX индекс не проверяет: сокет с несуществующим
    // привязывается, а отказ приходит только на connect или send (ENODEV).
    // IP_UNICAST_IF на старых сборках Windows тоже может его принять.
    if !interface_exists(index) {
        return Err(io::Error::new(
            io::ErrorKind::NotFound,
            "нет интерфейса с таким индексом",
        ));
    }
    let socket = Socket::new(Domain::IPV4, Type::DGRAM, None)?;
    csqtt_bind::bind_socket_to(SockRef::from(&socket), false, index, mark)
}

#[cfg(target_os = "linux")]
fn interface_exists(index: u32) -> bool {
    let mut name = [0 as libc::c_char; libc::IF_NAMESIZE];
    !unsafe { libc::if_indextoname(index, name.as_mut_ptr()) }.is_null()
}

#[cfg(windows)]
fn interface_exists(index: u32) -> bool {
    #[link(name = "iphlpapi")]
    unsafe extern "system" {
        fn if_indextoname(index: u32, name: *mut u8) -> *mut u8;
    }
    // IF_MAX_STRING_SIZE + 1.
    let mut name = [0u8; 257];
    !unsafe { if_indextoname(index, name.as_mut_ptr()) }.is_null()
}

#[cfg(not(any(target_os = "linux", windows)))]
fn interface_exists(_index: u32) -> bool {
    true
}

fn explain(error: &io::Error) -> String {
    if error.kind() == io::ErrorKind::PermissionDenied {
        format!("{error} — метке нужен CAP_NET_ADMIN")
    } else {
        error.to_string()
    }
}

/// Привязывает сокет к текущему интерфейсу; без -bind-if ничего не делает.
pub(crate) fn bind<'s>(socket: impl Into<SockRef<'s>>, ipv6: bool) -> io::Result<()> {
    csqtt_bind::bind_socket(socket.into(), ipv6)
}

/// UDP-сокет на address, привязанный к текущему интерфейсу (точки 1 и 2).
pub(crate) async fn udp_socket(address: SocketAddr) -> io::Result<UdpSocket> {
    let socket = UdpSocket::bind(address).await?;
    bind(&socket, address.is_ipv6())?;
    Ok(socket)
}

/// TCP-соединение с address через текущий интерфейс (точка 3): привязка —
/// до connect.
pub(crate) async fn connect_tcp(address: SocketAddr) -> io::Result<TcpStream> {
    let socket = if address.is_ipv4() {
        TcpSocket::new_v4()?
    } else {
        TcpSocket::new_v6()?
    };
    bind(&socket, address.is_ipv6())?;
    socket.connect(address).await
}

/// Построитель клиента primp (точки 4 и 5). Соединения клиента привязывает
/// коннектор primp-hyper-util; с -bind-if прокси из окружения выключены:
/// привязан был бы только сокет до прокси, а к VK прокси ходил бы своими.
pub(crate) fn client_builder() -> primp::ClientBuilder {
    let builder = primp::Client::builder();
    if ENABLED.load(Ordering::Acquire) {
        builder.no_proxy()
    } else {
        builder
    }
}

#[cfg(test)]
#[path = "bind_if_tests.rs"]
mod tests;
