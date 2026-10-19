// Правка П6 csqtt-cli (doc/architecture.md, §05 и §07): пакет, который ОС не
// приняла в TUN, отбрасывается, а запись идёт дальше. У апстрима запись 0 байт
// и любая ошибка, кроме повторяемых и «интерфейс закрыт», останавливали
// писателя TUN, и туннель стоял до нового дескриптора от оболочки. Так бывает
// от одного пакета: TUN с IFF_NO_PI узнаёт протокол по первому полубайту
// пакета, и не-IP пакет от сервера получает EINVAL.
//
// Файл не из апстрима: его кладёт в core/ scripts/vendorcore из
// scripts/vendorcore/files, там его и править. Модуль — дочерний к
// dispatcher.rs (его подключает правило vendorcore), чтобы тесты могли
// запустить закрытый Dispatcher::write_tun на своём дескрипторе.
//
// Правило в try_write_tun_packet зовёт rejected и отвечает Complete: для
// write_tun пакет обработан, и он берёт следующий с нулевым смещением. Поэтому
// и после частичной записи следующий пакет не начнётся с хвоста отброшенного.
// Повторяемые ошибки и «закрыт» ведут себя как у апстрима.
//
// Отбросы считаются за весь процесс. В журнал — первые FIRST_LOGGED, дальше
// каждый LOG_EVERY-й: поток мусора от сервера не зальёт журнал.

use std::{
    io,
    sync::atomic::{AtomicU64, Ordering},
};

/// Сколько отбросов подряд пишется в журнал каждый.
const FIRST_LOGGED: u64 = 8;
/// Дальше — каждый такой.
const LOG_EVERY: u64 = 1000;

static DROPPED: AtomicU64 = AtomicU64::new(0);

/// Пакет длиной length не принят: запись вернула 0 байт (error — None) или
/// ошибку. written — сколько байт пакета успело уйти до этого.
pub(super) fn rejected(length: usize, written: usize, error: Option<&io::Error>) {
    note(&DROPPED, length, written, error);
}

/// Считает отброс в counter и пишет его в журнал, если пора. true — записал.
fn note(counter: &AtomicU64, length: usize, written: usize, error: Option<&io::Error>) -> bool {
    let count = counter.fetch_add(1, Ordering::Relaxed) + 1;
    if !should_log(count) {
        return false;
    }
    let reason = match error {
        Some(error) => format!("ошибка записи: {error}"),
        None => "запись вернула 0 байт".to_owned(),
    };
    let partial = if written > 0 {
        format!(", записано {written} из {length}")
    } else {
        String::new()
    };
    let rare = if count == FIRST_LOGGED {
        format!("; дальше в журнал — каждый {LOG_EVERY}-й")
    } else {
        String::new()
    };
    crate::log_error!(
        "[П6] Пакет TUN отброшен ({length} байт{partial}): ОС его не приняла, {reason} (отброшено всего: {count}{rare})"
    );
    true
}

fn should_log(count: u64) -> bool {
    count <= FIRST_LOGGED || count.is_multiple_of(LOG_EVERY)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::{
        dispatcher::{Dispatcher, PacketSender, packet_channel},
        packet::{PacketBuf, PacketPool},
        stats::Stats,
    };
    use std::{
        fs::File,
        io::Read,
        os::{
            fd::{FromRawFd, IntoRawFd},
            unix::net::UnixDatagram,
        },
        sync::Arc,
        time::Duration,
    };
    use tokio::io::unix::AsyncFd;
    use tokio_util::sync::CancellationToken;

    #[test]
    fn journal_is_throttled_after_the_first_drops() {
        let counter = AtomicU64::new(0);
        let einval = io::Error::from_raw_os_error(libc::EINVAL);
        let logged: Vec<u64> = (1..=3000)
            .filter(|_| note(&counter, 28, 0, Some(&einval)))
            .collect();
        assert_eq!(logged, [1, 2, 3, 4, 5, 6, 7, 8, 1000, 2000, 3000]);
        assert_eq!(counter.load(Ordering::Relaxed), 3000);
        assert!(!note(&counter, 28, 0, None));
    }

    /// Писатель TUN диспетчера на дескрипторе device: пакеты packets уходят в
    /// очередь возврата, очередь закрывается, и write_tun работает, пока не
    /// разберёт её или не остановится сам. Возвращает, сколько пакетов
    /// осталось в очереди.
    async fn write_through(device: File, packets: &[&[u8]]) -> usize {
        let pool = PacketPool::new(16);
        let cancel = CancellationToken::new();
        let (dispatcher, _) = Dispatcher::start(
            "127.0.0.1:0",
            None,
            pool.clone(),
            Arc::new(Stats::default()),
            cancel.clone(),
        )
        .await
        .unwrap();
        let (latency, mut latency_rx) = packet_channel(16, true);
        let (priority, mut priority_rx) = packet_channel(16, true);
        let (bulk, mut bulk_rx) = packet_channel(16, true);
        for bytes in packets {
            send(&latency, packet(&pool, bytes));
        }
        drop((latency, priority, bulk));
        tokio::time::timeout(
            Duration::from_secs(2),
            dispatcher.write_tun(
                Arc::new(AsyncFd::new(device).unwrap()),
                &mut latency_rx,
                &mut priority_rx,
                &mut bulk_rx,
                Arc::new(Stats::default()),
            ),
        )
        .await
        .expect("write_tun не завершился");
        let mut left = 0;
        while latency_rx.try_recv().is_some() {
            left += 1;
        }
        dispatcher.shutdown().await;
        left
    }

    fn packet(pool: &Arc<PacketPool>, bytes: &[u8]) -> PacketBuf {
        let mut packet = pool.acquire();
        packet.set_read_len(bytes.len()).unwrap();
        packet.as_mut_slice()[..bytes.len()].copy_from_slice(bytes);
        packet
    }

    fn send(sender: &PacketSender, packet: PacketBuf) {
        assert!(sender.try_send(packet).is_ok(), "очередь возврата полна");
    }

    fn eventfd() -> File {
        let descriptor = unsafe { libc::eventfd(0, libc::EFD_NONBLOCK | libc::EFD_CLOEXEC) };
        assert!(descriptor >= 0, "{}", io::Error::last_os_error());
        unsafe { File::from_raw_fd(descriptor) }
    }

    /// EINVAL, как от TUN на не-IP пакет: eventfd принимает только 8 байт и не
    /// принимает u64::MAX. Хороший пакет после двух плохих доходит.
    #[tokio::test]
    async fn rejected_packet_is_dropped_and_the_next_one_is_written() {
        let device = eventfd();
        let mut counter = device.try_clone().unwrap();
        let before = DROPPED.load(Ordering::Relaxed);
        let left = write_through(
            device,
            &[&[0xde, 0xad, 0xbe, 0xef], &[0xff; 8], &7u64.to_ne_bytes()],
        )
        .await;
        assert_eq!(left, 0);
        let mut value = [0u8; 8];
        counter
            .read_exact(&mut value)
            .expect("хороший пакет не записан");
        assert_eq!(u64::from_ne_bytes(value), 7);
        assert!(DROPPED.load(Ordering::Relaxed) >= before + 2);
    }

    /// Запись 0 байт: пустой датаграм в сокет датаграм отвечает 0.
    #[tokio::test]
    async fn zero_byte_write_is_dropped_and_the_next_one_is_written() {
        let (device, peer) = UnixDatagram::pair().unwrap();
        peer.set_nonblocking(true).unwrap();
        let device = unsafe { File::from_raw_fd(device.into_raw_fd()) };
        let left = write_through(device, &[&[], &[0x45, 0x00]]).await;
        assert_eq!(left, 0);
        let mut buffer = [0u8; 16];
        let mut received = Vec::new();
        // write_tun уже вернулся: всё записанное лежит в очереди сокета.
        while let Ok(length) = peer.recv(&mut buffer) {
            received.push(buffer[..length].to_vec());
        }
        assert_eq!(received.last().map(Vec::as_slice), Some(&[0x45, 0x00][..]));
    }

    /// «Интерфейс закрыт» по-прежнему останавливает писателя: следующий пакет
    /// остаётся в очереди. EBADF даёт мастер псевдотерминала, открытый только
    /// на чтение: epoll считает его готовым к записи, а write отказывает.
    /// (/dev/null не годится — epoll его не берёт.)
    #[tokio::test]
    async fn closed_interface_still_stops_the_writer() {
        use std::os::unix::fs::OpenOptionsExt;

        let device = std::fs::OpenOptions::new()
            .read(true)
            .custom_flags(libc::O_NOCTTY | libc::O_NONBLOCK)
            .open("/dev/ptmx")
            .expect("нет /dev/ptmx");
        let left = write_through(device, &[&[0x45, 0x00], &[0x45, 0x01]]).await;
        assert_eq!(left, 1);
    }
}
