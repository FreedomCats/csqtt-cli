// Правка П2 csqtt-cli (doc/architecture.md, Р4): закреплённый отправитель на
// loopback. В режиме -listen апстрим отвечает адресу последнего, кто прислал
// датаграм, — один чужой датаграм от любого процесса машины уводит туннель к
// нему. С -listen-peer <IP:порт> ядро принимает датаграмы только с этого
// адреса и отвечает только ему; чужие отбрасываются и считаются.
//
// Файл не из апстрима: его кладёт в core/ scripts/vendorcore из
// scripts/vendorcore/files, там его и править.
//
// Свой порт ядро сообщает событием LISTEN {"port": N}: оболочка запускает ядро
// с -listen 127.0.0.1:0, и узнать порт иначе ей неоткуда (апстрим пишет его
// только в журнал и серверу в GETCONF).

use crate::events::{EVENT_PREFIX, Events};
use anyhow::{Result, anyhow, bail};
use arc_swap::ArcSwapOption;
use std::{net::SocketAddr, sync::Arc};

/// Разбор -listen-peer. Пусто — None: поведение апстрима.
pub(crate) fn parse(value: &str, tun_mode: bool) -> Result<Option<SocketAddr>> {
    if value.is_empty() {
        return Ok(None);
    }
    if tun_mode {
        bail!("[КЛИЕНТ] -listen-peer не сочетается с -tun-uds");
    }
    let address: SocketAddr = value
        .parse()
        .map_err(|_| anyhow!("[КЛИЕНТ] -listen-peer: нужен адрес IP:порт, а не {value:?}"))?;
    if address.port() == 0 || address.ip().is_unspecified() {
        bail!("[КЛИЕНТ] -listen-peer: нужен конкретный адрес и порт, а не {address}");
    }
    Ok(Some(address))
}

/// Решает, чьи датаграмы принимать и кому отвечать. Живёт в задаче чтения
/// UDP диспетчера.
pub(crate) struct PeerGate {
    pinned: Option<SocketAddr>,
    foreign: u64,
}

impl PeerGate {
    pub(crate) fn new(pinned: Option<SocketAddr>) -> Self {
        Self { pinned, foreign: 0 }
    }

    /// Адресат ответов до первого датаграма: закреплённый известен сразу, без
    /// закрепления — никто, как в апстриме.
    pub(crate) fn initial_client(&self) -> Option<SocketAddr> {
        self.pinned
    }

    /// true — датаграм с адреса source принять. Без закрепления — как в
    /// апстриме: принять любой, а отвечать его отправителю.
    pub(crate) fn admit(&mut self, source: SocketAddr, client: &ArcSwapOption<SocketAddr>) -> bool {
        let Some(peer) = self.pinned else {
            let previous = client.load();
            if previous.as_deref() != Some(&source) {
                client.store(Some(Arc::new(source)));
            }
            return true;
        };
        if source == peer {
            return true;
        }
        self.foreign += 1;
        // 1, 2, 4, 8… — поток чужих датаграмов не зальёт журнал.
        if self.foreign.is_power_of_two() {
            crate::log_error!(
                "[КЛИЕНТ] Отброшен датаграм с чужого адреса {source} (всего чужих: {})",
                self.foreign
            );
        }
        false
    }

    #[cfg(test)]
    fn foreign(&self) -> u64 {
        self.foreign
    }
}

/// Событие LISTEN с портом сокета ядра. В режиме -tun-uds диспетчер отдаёт
/// порт "0" — сокета нет, и события тоже.
pub(crate) fn report_port(events: &Events, local_port: &str) {
    if !events.enabled() {
        return;
    }
    if let Some(line) = listen_event(local_port) {
        crate::log_output!("{line}");
    }
}

fn listen_event(local_port: &str) -> Option<String> {
    let port: u16 = local_port.parse().ok().filter(|port| *port != 0)?;
    Some(format!(
        "{EVENT_PREFIX}LISTEN|{}",
        serde_json::json!({"port": port})
    ))
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::{
        dispatcher::{Dispatcher, DomainRoutingConfig},
        packet::PacketPool,
        stats::Stats,
    };
    use clap::Parser;
    use std::{sync::atomic::Ordering, time::Duration};
    use tokio::net::UdpSocket;
    use tokio_util::sync::CancellationToken;

    fn error(result: Result<impl Sized>) -> String {
        match result {
            Ok(_) => panic!("ожидалась ошибка"),
            Err(error) => format!("{error:#}"),
        }
    }

    fn address(value: &str) -> SocketAddr {
        value.parse().unwrap()
    }

    #[test]
    fn flag_parses_with_single_dash() {
        assert_eq!(
            crate::normalize_cli_argument("-listen-peer=127.0.0.1:5000".to_owned()),
            "--listen-peer=127.0.0.1:5000"
        );
        let arguments = crate::Arguments::try_parse_from([
            "csqtt-client",
            "--listen-peer",
            "127.0.0.1:5000",
        ])
        .unwrap();
        assert_eq!(arguments.listen_peer, "127.0.0.1:5000");
        let arguments = crate::Arguments::try_parse_from(["csqtt-client"]).unwrap();
        assert_eq!(arguments.listen_peer, "");
    }

    #[test]
    fn parse_accepts_empty_and_concrete_addresses() {
        assert_eq!(parse("", false).unwrap(), None);
        assert_eq!(parse("", true).unwrap(), None);
        assert_eq!(
            parse("127.0.0.1:5000", false).unwrap(),
            Some(address("127.0.0.1:5000"))
        );
        assert_eq!(parse("[::1]:5000", false).unwrap(), Some(address("[::1]:5000")));
    }

    #[test]
    fn parse_rejects_bad_addresses_and_tun_mode() {
        assert!(error(parse("127.0.0.1:5000", true)).contains("-tun-uds"));
        for value in ["localhost:5000", "127.0.0.1", "127.0.0.1:x", "-1"] {
            assert!(error(parse(value, false)).contains("IP:порт"), "{value}");
        }
        for value in ["127.0.0.1:0", "0.0.0.0:5000", "[::]:5000"] {
            assert!(error(parse(value, false)).contains("конкретный"), "{value}");
        }
    }

    #[test]
    fn pinned_gate_drops_and_counts_foreign_sources() {
        let shell = address("127.0.0.1:5000");
        let mut gate = PeerGate::new(Some(shell));
        let client = ArcSwapOption::from_pointee(gate.initial_client());
        assert!(!gate.admit(address("127.0.0.1:5001"), &client));
        assert!(!gate.admit(address("127.0.0.2:5000"), &client));
        assert!(gate.admit(shell, &client));
        assert!(!gate.admit(address("127.0.0.1:5001"), &client));
        assert_eq!(gate.foreign(), 3);
        assert_eq!(client.load().as_deref(), Some(&shell));
    }

    #[test]
    fn unpinned_gate_follows_the_last_sender() {
        let mut gate = PeerGate::new(None);
        let client = ArcSwapOption::from_pointee(gate.initial_client());
        assert_eq!(client.load().as_deref(), None);
        assert!(gate.admit(address("127.0.0.1:5000"), &client));
        assert!(gate.admit(address("127.0.0.1:5001"), &client));
        assert_eq!(client.load().as_deref(), Some(&address("127.0.0.1:5001")));
        assert_eq!(gate.foreign(), 0);
    }

    #[test]
    fn listen_event_carries_the_port() {
        assert_eq!(
            listen_event("40123").as_deref(),
            Some("__CSQTT_EVENT__|LISTEN|{\"port\":40123}")
        );
        assert_eq!(listen_event("0"), None);
        assert_eq!(listen_event(""), None);
        assert_eq!(listen_event("70000"), None);
    }

    /// Диспетчер без воркеров: что датаграм принят в туннель, видно по счётчику
    /// stats.total_bytes_up — он растёт после PeerGate::admit и до раздачи
    /// воркерам. Ответы (return_packet) уходят адресату из client — его и
    /// проверяем.
    struct Fixture {
        dispatcher: Arc<Dispatcher>,
        core: SocketAddr,
        stats: Arc<Stats>,
        pool: Arc<PacketPool>,
    }

    async fn start(pinned: Option<SocketAddr>) -> Fixture {
        let pool = PacketPool::new(32);
        let stats = Arc::new(Stats::default());
        let (dispatcher, port) = Dispatcher::start_with_domain_routing(
            "127.0.0.1:0",
            pinned,
            None,
            pool.clone(),
            stats.clone(),
            CancellationToken::new(),
            DomainRoutingConfig::default(),
            10,
        )
        .await
        .unwrap();
        Fixture {
            dispatcher,
            core: address(&format!("127.0.0.1:{port}")),
            stats,
            pool,
        }
    }

    /// Датаграм оболочки в ядро: read_udp пропускает только то, что похоже на
    /// IP-пакет, — заголовок IPv4 с адресатом вне loopback и байт полезной
    /// нагрузки.
    fn uplink(byte: u8) -> Vec<u8> {
        let mut packet = vec![
            0x45, 0, 0, 21, 0, 0, 0, 0, 64, 17, 0, 0, 10, 0, 0, 2, 10, 0, 0, 1,
        ];
        packet.push(byte);
        packet
    }

    impl Fixture {
        /// Пакет, который ядро прислало бы оболочке из туннеля.
        fn reply(&self, byte: u8) {
            let mut packet = self.pool.acquire();
            packet.set_read_len(1).unwrap();
            packet.as_mut_slice()[0] = byte;
            self.dispatcher.return_packet(packet);
        }

        /// Сколько байт вошло в туннель; ждёт, пока читатель обработает
        /// отправленное.
        async fn uplink_bytes(&self) -> i64 {
            tokio::time::sleep(Duration::from_millis(50)).await;
            self.stats.total_bytes_up.load(Ordering::Relaxed)
        }
    }

    async fn receive(socket: &UdpSocket, wait: Duration) -> Option<Vec<u8>> {
        let mut buffer = [0u8; 64];
        let (length, _) = tokio::time::timeout(wait, socket.recv_from(&mut buffer))
            .await
            .ok()?
            .unwrap();
        Some(buffer[..length].to_vec())
    }

    async fn sockets() -> (UdpSocket, UdpSocket) {
        (
            UdpSocket::bind("127.0.0.1:0").await.unwrap(),
            UdpSocket::bind("127.0.0.1:0").await.unwrap(),
        )
    }

    #[tokio::test]
    async fn foreign_datagram_neither_enters_the_tunnel_nor_redirects_replies() {
        let (shell, stranger) = sockets().await;
        let fixture = start(Some(shell.local_addr().unwrap())).await;

        stranger.send_to(&uplink(0xee), fixture.core).await.unwrap();
        shell.send_to(&uplink(0x01), fixture.core).await.unwrap();
        stranger.send_to(&uplink(0xef), fixture.core).await.unwrap();
        assert_eq!(fixture.uplink_bytes().await, 21, "чужой датаграм ушёл в туннель");

        fixture.reply(0x80);
        assert_eq!(receive(&shell, Duration::from_secs(1)).await, Some(vec![0x80]));
        assert_eq!(receive(&stranger, Duration::from_millis(200)).await, None);
        fixture.dispatcher.shutdown().await;
    }

    #[tokio::test]
    async fn pinned_peer_gets_replies_before_its_first_datagram() {
        let (shell, stranger) = sockets().await;
        let fixture = start(Some(shell.local_addr().unwrap())).await;
        stranger.send_to(&uplink(0xee), fixture.core).await.unwrap();
        assert_eq!(fixture.uplink_bytes().await, 0);

        fixture.reply(0x80);
        assert_eq!(receive(&shell, Duration::from_secs(1)).await, Some(vec![0x80]));
        assert_eq!(receive(&stranger, Duration::from_millis(200)).await, None);
        fixture.dispatcher.shutdown().await;
    }

    /// Без -listen-peer — поведение апстрима, вместе с дырой: ответы уходят
    /// последнему отправителю.
    #[tokio::test]
    async fn without_pin_replies_follow_the_last_sender() {
        let (shell, stranger) = sockets().await;
        let fixture = start(None).await;

        shell.send_to(&uplink(0x01), fixture.core).await.unwrap();
        assert_eq!(fixture.uplink_bytes().await, 21);
        stranger.send_to(&uplink(0xee), fixture.core).await.unwrap();
        assert_eq!(fixture.uplink_bytes().await, 42);

        fixture.reply(0x80);
        assert_eq!(receive(&stranger, Duration::from_secs(1)).await, Some(vec![0x80]));
        assert_eq!(receive(&shell, Duration::from_millis(200)).await, None);
        fixture.dispatcher.shutdown().await;
    }
}
