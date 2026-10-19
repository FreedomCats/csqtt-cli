// Правка П4 csqtt-cli (doc/architecture.md, Р4): дескриптор TUN принимается
// только от процесса того же пользователя. В режиме -tun-uds ядро слушает
// абстрактный unix-сокет, а он открыт всем процессам машины: у абстрактного
// имени нет прав файла. Без проверки любой процесс мог бы подсунуть ядру свой
// TUN и читать трафик туннеля.
//
// Файл не из апстрима: его кладёт в core/ scripts/vendorcore из
// scripts/vendorcore/files, там его и править. Модуль — дочерний к tun.rs
// (его подключает правило vendorcore), чтобы тесты видели слушатель изнутри.
//
// После accept ядро берёт SO_PEERCRED соединения — uid процесса, который
// вызвал connect, — и сравнивает с эффективным uid ядра. Ядро считает именно
// эффективный: SO_PEERCRED отдаёт эффективный uid отправителя, и права на
// файлы и устройства у обоих тоже определяет он. Чужой uid или ошибка
// getsockopt — соединение закрывается, не читая из него ни байта, отказ
// считается, а FdReceiver::receive тут же ждёт следующего соединения: ни
// ошибки вызывающему, ни паузы, законный отправитель за отвергнутым
// принимается сразу.
//
// SO_PEERCRED есть на Linux и Android. На других unix абстрактных сокетов нет,
// и -tun-uds там не собирается; если соберётся — отказ всем, а не приём без
// проверки.

use std::{
    io,
    os::fd::AsFd,
    sync::atomic::{AtomicU32, AtomicU64, Ordering},
};

/// Проверка отправителя дескриптора. Живёт в FdReceiver весь сеанс.
pub(super) struct PeerCheck {
    /// Чьи дескрипторы принимать: эффективный uid ядра. Атомик — ради тестов:
    /// они меняют его на ходу, чтобы свой процесс выглядел чужим.
    expected: AtomicU32,
    rejected: AtomicU64,
}

impl PeerCheck {
    pub(super) fn new() -> Self {
        Self {
            expected: AtomicU32::new(unsafe { libc::geteuid() }),
            rejected: AtomicU64::new(0),
        }
    }

    /// true — соединение принять. false — закрыть его не читая: отказ уже
    /// посчитан и записан в журнал.
    pub(super) fn admit(&self, connection: &impl AsFd) -> bool {
        let expected = self.expected.load(Ordering::Relaxed);
        let peer = peer_uid(connection);
        if allowed(&peer, expected) {
            return true;
        }
        let count = self.rejected.fetch_add(1, Ordering::Relaxed) + 1;
        // 1, 2, 4, 8… — поток соединений не зальёт журнал.
        if count.is_power_of_two() {
            match peer {
                Ok(uid) => crate::log_error!(
                    "[П4] Отвергнут отправитель TUN FD: uid {uid}, а ядро работает под uid {expected} (отказ #{count})"
                ),
                Err(error) => crate::log_error!(
                    "[П4] Отвергнут отправитель TUN FD: его uid не узнать ({error}) (отказ #{count})"
                ),
            }
        }
        false
    }
}

fn allowed(peer: &io::Result<libc::uid_t>, expected: libc::uid_t) -> bool {
    matches!(peer, Ok(uid) if *uid == expected)
}

#[cfg(any(target_os = "linux", target_os = "android"))]
fn peer_uid(connection: &impl AsFd) -> io::Result<libc::uid_t> {
    use nix::sys::socket::{getsockopt, sockopt::PeerCredentials};

    Ok(getsockopt(connection, PeerCredentials)?.uid())
}

#[cfg(not(any(target_os = "linux", target_os = "android")))]
fn peer_uid(_connection: &impl AsFd) -> io::Result<libc::uid_t> {
    Err(io::Error::new(
        io::ErrorKind::Unsupported,
        "SO_PEERCRED есть только на Linux и Android",
    ))
}

#[cfg(all(test, any(target_os = "linux", target_os = "android")))]
mod tests {
    use super::*;
    use crate::tun::FdReceiver;
    use nix::sys::socket::{
        AddressFamily, ControlMessage, MsgFlags, SockFlag, SockType, UnixAddr, connect, sendmsg,
        socket,
    };
    use std::{
        fs::File,
        io::{IoSlice, Read, Write},
        os::{fd::AsRawFd, unix::net::UnixStream},
        sync::Arc,
        time::Duration,
    };
    use tokio_util::sync::CancellationToken;

    fn own_uid() -> libc::uid_t {
        unsafe { libc::geteuid() }
    }

    fn receiver_name() -> String {
        format!(
            "csqtt-tun-peer-test-{}-{}",
            std::process::id(),
            rand::random::<u64>()
        )
    }

    /// Отправитель, как у оболочки: connect к абстрактному имени и
    /// дескриптор в SCM_RIGHTS. Сокет возвращается, чтобы прочесть ответ.
    fn send_fd(name: &str, descriptor: &impl AsRawFd) -> UnixStream {
        let socket = socket(
            AddressFamily::Unix,
            SockType::Stream,
            SockFlag::SOCK_CLOEXEC,
            None,
        )
        .unwrap();
        connect(
            socket.as_raw_fd(),
            &UnixAddr::new_abstract(name.as_bytes()).unwrap(),
        )
        .unwrap();
        let payload = [1u8];
        let slices = [IoSlice::new(&payload)];
        let controls = [ControlMessage::ScmRights(&[descriptor.as_raw_fd()])];
        sendmsg::<UnixAddr>(
            socket.as_raw_fd(),
            &slices,
            &controls,
            MsgFlags::empty(),
            None,
        )
        .unwrap();
        let stream = UnixStream::from(socket);
        stream
            .set_read_timeout(Some(Duration::from_secs(2)))
            .unwrap();
        stream
    }

    fn pipe() -> (File, File) {
        let mut descriptors = [0; 2];
        assert_eq!(
            unsafe { libc::pipe2(descriptors.as_mut_ptr(), libc::O_CLOEXEC | libc::O_NONBLOCK) },
            0
        );
        unsafe {
            use std::os::fd::FromRawFd;
            (
                File::from_raw_fd(descriptors[0]),
                File::from_raw_fd(descriptors[1]),
            )
        }
    }

    async fn wait_for(what: &str, mut condition: impl FnMut() -> bool) {
        let deadline = tokio::time::Instant::now() + Duration::from_secs(2);
        while !condition() {
            assert!(
                tokio::time::Instant::now() < deadline,
                "не дождались: {what}"
            );
            tokio::time::sleep(Duration::from_millis(5)).await;
        }
    }

    #[test]
    fn only_the_same_uid_is_allowed() {
        assert!(allowed(&Ok(1000), 1000));
        assert!(allowed(&Ok(0), 0));
        assert!(!allowed(&Ok(1001), 1000));
        assert!(!allowed(&Ok(0), 1000));
        assert!(!allowed(&Ok(1000), 0));
        assert!(!allowed(
            &Err(io::Error::from_raw_os_error(libc::ENOTSOCK)),
            1000
        ));
    }

    #[test]
    fn peer_uid_of_a_connected_socket_is_the_effective_uid() {
        let (left, _right) = UnixStream::pair().unwrap();
        assert_eq!(peer_uid(&left).unwrap(), own_uid());
    }

    #[test]
    fn check_admits_own_uid_and_counts_nothing() {
        let (left, _right) = UnixStream::pair().unwrap();
        let check = PeerCheck::new();
        assert!(check.admit(&left));
        assert_eq!(check.rejected.load(Ordering::Relaxed), 0);
    }

    #[test]
    fn check_rejects_foreign_uid_and_unknown_peer() {
        let (left, _right) = UnixStream::pair().unwrap();
        let check = PeerCheck::new();
        check
            .expected
            .store(own_uid().wrapping_add(1), Ordering::Relaxed);
        assert!(!check.admit(&left));
        // Не сокет: getsockopt падает — отказ, а не приём без проверки.
        let check = PeerCheck::new();
        let not_a_socket = File::open("/dev/null").unwrap();
        assert!(!check.admit(&not_a_socket));
        assert!(!check.admit(&not_a_socket));
        assert_eq!(check.rejected.load(Ordering::Relaxed), 2);
    }

    /// Один и тот же вызов receive: отправителя с «чужим» uid отвергает, его
    /// дескриптор не принимает, а следующего, законного, принимает сразу.
    /// Сменить uid без root нельзя, поэтому чужим ядро на время делает свой
    /// uid: ждёт другого.
    #[tokio::test]
    async fn listener_rejects_a_foreign_sender_and_then_accepts_the_owner() {
        let name = receiver_name();
        let receiver = Arc::new(FdReceiver::bind(&name).unwrap());
        receiver
            .peer_check
            .expected
            .store(own_uid().wrapping_add(1), Ordering::Relaxed);
        let cancel = CancellationToken::new();
        let receiving = tokio::spawn({
            let receiver = receiver.clone();
            let cancel = cancel.clone();
            async move { receiver.receive(&cancel).await }
        });

        // Чужой шлёт конец канала и закрывает свою копию: если ядро не взяло
        // дескриптор, канал закроется вместе с соединением.
        let (mut foreign_read, foreign_write) = pipe();
        let mut foreign = send_fd(&name, &foreign_write);
        drop(foreign_write);
        wait_for("отказа чужому", || {
            receiver.peer_check.rejected.load(Ordering::Relaxed) == 1
        })
        .await;
        let mut ack = [0u8; 1];
        assert!(
            !matches!(foreign.read(&mut ack), Ok(1)),
            "чужой отправитель получил подтверждение"
        );
        // Канал без писателей читается как конец файла; пока дескриптор чужого
        // где-то держат, чтение отвечает WouldBlock.
        wait_for(
            "закрытия дескриптора чужого",
            || matches!(foreign_read.read(&mut [0u8; 1]), Ok(0)),
        )
        .await;
        assert!(!receiving.is_finished(), "receive вернулся после отказа");

        receiver
            .peer_check
            .expected
            .store(own_uid(), Ordering::Relaxed);
        let (owner_tun, mut owner_peer) = UnixStream::pair().unwrap();
        let mut owner = send_fd(&name, &owner_tun);
        let mut received = tokio::time::timeout(Duration::from_secs(2), receiving)
            .await
            .expect("законный отправитель не принят")
            .unwrap()
            .unwrap();
        assert_eq!(owner.read(&mut ack).unwrap(), 1);
        assert_eq!(ack[0], 1);
        drop(owner_tun);
        // Принят именно дескриптор законного отправителя.
        received.write_all(&[0x45]).unwrap();
        owner_peer
            .set_read_timeout(Some(Duration::from_secs(2)))
            .unwrap();
        assert_eq!(owner_peer.read(&mut ack).unwrap(), 1);
        assert_eq!(ack[0], 0x45);
        assert_eq!(receiver.peer_check.rejected.load(Ordering::Relaxed), 1);
    }
}
