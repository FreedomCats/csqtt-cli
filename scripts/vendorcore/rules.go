package main

// =============================== правки =========================================
// Всё, что меняется в коде апстрима, описано здесь и только здесь (решение Р2
// в doc/architecture.md, правки П1–П6 — §07).

// vendoredDirs — каталоги апстрима, которые переносятся в core/ целиком.
// Они переносятся только вместе: main.rs подключает shared/*.rs путями
// #[path = "../shared/…"], а между версиями меняется протокол.
var vendoredDirs = []string{"rust-client", "shared"}

// vendoredFiles — отдельные файлы апстрима: путь в апстриме → путь в core/.
// Лицензия ядра лежит в корне апстрима, а PolyForm требует, чтобы её текст
// шёл вместе с каждой копией кода. domains.txt — список доменов для
// маршрутизации мимо туннеля (2.5.0): sni.rs вшивает его
// include_str!("../domains.txt") из корня апстрима, без него ядро не
// собирается. Оболочка пускает этот список мимо туннеля по опции профиля
// routing.template (--template-domains) и показывает число доменов в нём.
var vendoredFiles = map[string]string{
	"LICENSE":     "LICENSE",
	"domains.txt": "domains.txt",
}

// addedDir — каталог репозитория с нашими файлами для core/: правками,
// которые проще написать отдельным модулем, чем вставками в код апстрима
// (П1 — rust-client/secrets_stdin.rs). Пути под ним — те же, что в core/;
// файлы переносятся как есть, правила к ним не применяются. Файл с тем же
// путём в апстриме — ошибка: апстрим завёл свой, и наш молча затёр бы его.
const addedDir = "scripts/vendorcore/files"

// ignoredPaths — пути в core/, которые команда не считает ни своими, ни
// лишними: выход сборки cargo.
var ignoredPaths = []string{"rust-client/target"}

// excludedFile — файл апстрима, который в десктопной сборке не нужен.
// Reason попадает в сообщение проверки.
//
// Наличие файла в апстриме проверяется: если его переименуют, команда упадёт,
// а не перенесёт молча то, что мы выбрасывали. Исключённый файл, оставшийся
// в core/, удаляется при переносе и считается лишним при -check.
type excludedFile struct {
	Path, Reason string
}

var excludedFiles = []excludedFile{
	{"rust-client/build_android_abis.ps1", "сборка под Android: ядро собирает scripts/buildcore"},
	{"rust-client/build_so.bat", "сборка библиотеки под Android: ядро собирает scripts/buildcore"},
}

const outboundCompletionCode = `struct OutboundState {
    closed: bool,
    pending: usize,
}

impl Default for OutboundState {
    fn default() -> Self {
        Self {
            closed: false,
            pending: 0,
        }
    }
}

#[derive(Default)]
struct OutboundCompletion {
    state: Mutex<OutboundState>,
    changed: Notify,
}

impl OutboundCompletion {
    fn close(&self) {
        self.state
            .lock()
            .unwrap_or_else(|poisoned| poisoned.into_inner())
            .closed = true;
    }

    fn pending(&self) -> usize {
        self.state
            .lock()
            .unwrap_or_else(|poisoned| poisoned.into_inner())
            .pending
    }

    fn complete(&self) {
        let mut state = self
            .state
            .lock()
            .unwrap_or_else(|poisoned| poisoned.into_inner());
        state.pending = state
            .pending
            .checked_sub(1)
            .expect("outbound completion underflow");
        drop(state);
        self.changed.notify_one();
    }
}

struct OutboundPacket {
    packet: Option<PacketBuf>,
    completion: Option<Arc<OutboundCompletion>>,
}

impl std::ops::Deref for OutboundPacket {
    type Target = PacketBuf;

    fn deref(&self) -> &Self::Target {
        self.packet.as_ref().expect("outbound packet missing")
    }
}

impl std::ops::DerefMut for OutboundPacket {
    fn deref_mut(&mut self) -> &mut Self::Target {
        self.packet.as_mut().expect("outbound packet missing")
    }
}

impl Drop for OutboundPacket {
    fn drop(&mut self) {
        drop(self.packet.take());
        if let Some(completion) = self.completion.take() {
            completion.complete();
        }
    }
}

impl OutboundQueue {
    fn try_enqueue(&self, packet: PacketBuf) -> Result<(), PacketBuf> {
        let mut state = self
            .completion
            .state
            .lock()
            .unwrap_or_else(|poisoned| poisoned.into_inner());
        if state.closed {
            return Err(packet);
        }

        state.pending += 1;
        let outbound = OutboundPacket {
            packet: Some(packet),
            completion: Some(self.completion.clone()),
        };
        match self.queue.push(outbound) {
            Ok(()) => Ok(()),
            Err(mut returned) => {
                returned.completion.take();
                state.pending -= 1;
                Err(returned.packet.take().expect("outbound packet missing"))
            }
        }
    }

    fn discard_queued(&self) {
        while let Some(packet) = self.queue.pop() {
            self.space.notify_one();
            drop(packet);
        }
    }
}
`

// rule — замена в файле апстрима.
//
// Count проверяется: если апстрим перепишет строку и шаблон перестанет
// совпадать, команда упадёт. Молча потерять правку хуже, чем упасть.
type rule struct {
	// File — путь файла в core/ через «/», например rust-client/captcha.rs.
	File string
	// Count — сколько раз шаблон обязан совпасть.
	Count int
	// Pattern — regexp по тексту с LF; группа 1 обязана захватывать отступ.
	Pattern string
	// Ровно одно из трёх. Replace — чем заменить совпадение целиком (nil —
	// не задано, "" — вырезать); InsertAfter — строка, дописываемая после
	// совпадения с тем же отступом; InsertBefore — то же, но перед совпадением.
	Replace      *string
	InsertAfter  string
	InsertBefore string
}

// to — Replace правила: строка, в том числе пустая.
func to(s string) *string { return &s }

// synAckRecordCode — the П10 free functions: the SYN-ACK record of a direct
// TCP flow and the failure path that turns a failed outer connect into an
// application-facing reset. They sit beside the packet builders, so the egress
// task and the listener share one code path.
const synAckRecordCode = `/// П10: the flow and the SYN-ACK sequence of a frame leaving the direct
/// stack. Only a TCP SYN+ACK is accepted: it is the frame that establishes the
/// application's flow, and its sequence is what an application-facing reset
/// must refer to. A data frame, a plain ACK, a SYN without ACK, a truncated or
/// a non-TCP frame returns None.
fn syn_ack_flow(frame: &[u8]) -> Option<((SocketAddr, SocketAddr), u32)> {
    let src = crate::sni::extract_src_addr(frame)?;
    let dst = crate::sni::extract_dst_addr(frame)?;
    let (ip_header_len, protocol) = match *frame.first()? >> 4 {
        4 => {
            let ihl = (*frame.get(0)? & 0x0f) as usize * 4;
            if ihl < 20 {
                return None;
            }
            (ihl, *frame.get(9)?)
        }
        6 => (40, *frame.get(6)?),
        _ => return None,
    };
    if protocol != 6 {
        return None;
    }
    let tcp = frame.get(ip_header_len..)?;
    if tcp.len() < 20 || (tcp[13] & 0x12) != 0x12 {
        return None;
    }
    let seq = u32::from_be_bytes([tcp[4], tcp[5], tcp[6], tcp[7]]);
    // П10: a SYN-ACK travels from the destination to the application.
    Some(((dst, src), seq))
}

/// П10: records the SYN-ACK sequence of a direct TCP flow under the same key
/// as active_tcp: the application address first, the destination second.
fn record_syn_ack(map: &RwLock<HashMap<(SocketAddr, SocketAddr), u32>>, frame: &[u8]) {
    let Some((key, seq)) = syn_ack_flow(frame) else {
        return;
    };
    if let Ok(mut guard) = map.write() {
        guard.insert(key, seq);
    }
}

/// П10: waits a bounded time for the SYN-ACK record of a flow: one check, then
/// a poll every 10 ms. The wait is a parameter so tests inject a short one.
async fn wait_for_syn_ack_seq(
    map: &RwLock<HashMap<(SocketAddr, SocketAddr), u32>>,
    key: (SocketAddr, SocketAddr),
    wait: Duration,
) -> Option<u32> {
    if let Ok(guard) = map.read()
        && let Some(&seq) = guard.get(&key)
    {
        return Some(seq);
    }
    let deadline = tokio::time::Instant::now() + wait;
    loop {
        if tokio::time::Instant::now() >= deadline {
            return None;
        }
        tokio::time::sleep(Duration::from_millis(10)).await;
        if let Ok(guard) = map.read()
            && let Some(&seq) = guard.get(&key)
        {
            return Some(seq);
        }
    }
}

/// П10: remembers that a destination's direct connect failed. The entry is
/// valid until now + ttl; a lookup ignores it at and after the deadline.
fn remember_failed_direct(
    map: &RwLock<HashMap<IpAddr, std::time::Instant>>,
    ip: IpAddr,
    now: std::time::Instant,
    ttl: Duration,
) {
    if let Ok(mut guard) = map.write() {
        guard.insert(ip, now + ttl);
    }
}

/// П10: true while a destination's failed direct connect is remembered, so a
/// new TCP connection must use the tunnel instead of a direct attempt.
fn failed_direct_active(
    map: &RwLock<HashMap<IpAddr, std::time::Instant>>,
    ip: IpAddr,
    now: std::time::Instant,
) -> bool {
    map.read()
        .ok()
        .and_then(|guard| guard.get(&ip).copied())
        .is_some_and(|deadline| deadline > now)
}

/// П10: turns a failed outer direct connect into an application-facing reset.
/// The stack answered the application's SYN with SYN-ACK, so the application's
/// next expected sequence is the recorded one plus one: a reset at the
/// recorded value would be outside its receive window. Without a record the
/// flow is closed silently and no sequence is invented.
async fn abort_failed_direct_tcp(
    pool: &Arc<PacketPool>,
    inbound_tx: &mpsc::Sender<PacketBuf>,
    map: &RwLock<HashMap<(SocketAddr, SocketAddr), u32>>,
    key: (SocketAddr, SocketAddr),
    wait: Duration,
    failed: &RwLock<HashMap<IpAddr, std::time::Instant>>,
    ttl: Duration,
) {
    // П10: the address is remembered even when no SYN-ACK record exists and
    // no reset is sent: a failure is independent of the record.
    remember_failed_direct(failed, key.1.ip(), std::time::Instant::now(), ttl);
    let seq = wait_for_syn_ack_seq(map, key, wait).await;
    if let Some(seq) = seq
        && let Some(packet) = build_tcp_packet(key.1, key.0, seq.wrapping_add(1), 0, 0x04, &[])
        && let Some(mut pkt) = pool.try_acquire()
        && let Ok(dest) = pkt.extend_tail(packet.len())
    {
        dest.copy_from_slice(&packet);
        let _ = inbound_tx.send(pkt).await;
    }
    // The record goes whether or not a packet was sent, so a second caller
    // cannot reuse the same sequence.
    if let Ok(mut guard) = map.write() {
        guard.remove(&key);
    }
}
`

// rules — правки П1–П5. Набор пополнялся задачами C3, C4, W2, L1; П6 (отброс
// пакета, который ОС не приняла в TUN) с ядра 2.5.0 делает сам апстрим.
var rules = []rule{
	// П1: флаг -secrets-stdin — пароль и хеши VK первой строкой stdin. Сам
	// разбор — в files/rust-client/secrets_stdin.rs; здесь только подключение.
	{
		File: "rust-client/main.rs", Count: 1,
		Pattern:     `(?m)^()mod repair;$`,
		InsertAfter: "mod secrets_stdin;",
	},
	{
		// Поле password — не аргумент (skip): апстрим с 2.5.0 берёт пароль
		// из переменной окружения CSQTT_PASSWORD, флага --password у него нет,
		// и возвращать его в командную строку незачем.
		File: "rust-client/main.rs", Count: 1,
		Pattern: `(?m)^( +)validate_vk_hashes: bool,$`,
		InsertAfter: "#[arg(long, default_value_t = false)]\n${1}secrets_stdin: bool,\n" +
			"${1}#[arg(skip)]\n${1}password: String,",
	},
	{
		// С -secrets-stdin пароль — из строки SECRETS, без флага — из
		// окружения, как у апстрима.
		File: "rust-client/main.rs", Count: 1,
		Pattern: `(?m)^( +)let password = std::env::var\("CSQTT_PASSWORD"\)\.unwrap_or_default\(\);$`,
		Replace: to("${1}let password = secrets_stdin::password(&arguments);"),
	},
	{
		// normalize_cli_argument: одиночный дефис -secrets-stdin, как у
		// остальных флагов ядра.
		File: "rust-client/main.rs", Count: 1,
		Pattern:     `(?m)^( +)"validate-vk-hashes",$`,
		InsertAfter: `"secrets-stdin",`,
	},
	{
		// До рантайма и до run: и туннель, и -validate-vk-hashes берут хеши
		// из аргументов, уже дополненных секретами.
		File: "rust-client/main.rs", Count: 1,
		Pattern:     `(?m)^( +)let arguments = Arguments::parse_from\(normalized_arguments\(\)\);$`,
		InsertAfter: "let arguments = secrets_stdin::apply_or_exit(arguments);",
	},

	// П2: флаг -listen-peer — на loopback принимать датаграмы только с этого
	// адреса и отвечать только ему; порт ядра — событием LISTEN. Сама логика —
	// в files/rust-client/listen_peer.rs; здесь — подключение к main.rs и
	// диспетчеру.
	{
		File: "rust-client/main.rs", Count: 1,
		Pattern:      `(?m)^()mod logging;$`,
		InsertBefore: "mod listen_peer;",
	},
	{
		File: "rust-client/main.rs", Count: 1,
		Pattern:     `(?m)^( +)tun_uds: String,$`,
		InsertAfter: "#[arg(long, default_value = \"\")]\n${1}listen_peer: String,",
	},
	{
		File: "rust-client/main.rs", Count: 1,
		Pattern:     `(?m)^( +)"listen",$`,
		InsertAfter: `"listen-peer",`,
	},
	{
		// До рантайма туннеля: ошибка в флаге — отказ стартовать, а не
		// молча незакреплённый сокет.
		File: "rust-client/main.rs", Count: 1,
		Pattern:     `(?m)^( +)let turn_transport = TurnTransportMode::parse\(&arguments\.turn_transport\)\?;$`,
		InsertAfter: "let listen_peer = listen_peer::parse(&arguments.listen_peer, !arguments.tun_uds.is_empty())?;",
	},
	{
		File: "rust-client/main.rs", Count: 1,
		Pattern: `(?m)^( +)let dispatcher_result = Dispatcher::start_with_domain_routing\(\n( +)&arguments\.listen,$`,
		Replace: to("${1}let dispatcher_result = Dispatcher::start_with_domain_routing(\n${2}&arguments.listen,\n${2}listen_peer,"),
	},
	{
		File: "rust-client/main.rs", Count: 1,
		Pattern:     `(?m)^( +)let local_port: Arc<str> = Arc::from\(local_port\);$`,
		InsertAfter: "listen_peer::report_port(&events, &local_port);",
	},
	{
		// Второй параметр start_with_domain_routing — закреплённый адрес
		// оболочки. Тестовый start апстрима (#[cfg(test)]) зовёт её без
		// закрепления.
		File: "rust-client/dispatcher.rs", Count: 1,
		Pattern: `(?m)^( +)pub async fn start_with_domain_routing\(\n( +)listen: &str,$`,
		Replace: to("${1}pub async fn start_with_domain_routing(\n${2}listen: &str,\n${2}listen_peer: Option<SocketAddr>,"),
	},
	{
		File: "rust-client/dispatcher.rs", Count: 1,
		Pattern: `(?m)^( +)Self::start_with_domain_routing\(\n( +)listen,$`,
		Replace: to("${1}Self::start_with_domain_routing(\n${2}listen,\n${2}None,"),
	},
	{
		File: "rust-client/dispatcher.rs", Count: 1,
		Pattern: `(?m)^( +)let client = Arc::new\(ArcSwapOption::<SocketAddr>::from_pointee\(None\)\);$`,
		Replace: to("${1}let gate = crate::listen_peer::PeerGate::new(listen_peer);\n" +
			"${1}let client = Arc::new(ArcSwapOption::<SocketAddr>::from_pointee(gate.initial_client()));"),
	},
	{
		File: "rust-client/dispatcher.rs", Count: 1,
		Pattern: `(?m)^( +)\.read_udp\(read_socket, read_client, read_pool, read_stats\)$`,
		Replace: to("${1}.read_udp(read_socket, read_client, gate, read_pool, read_stats)"),
	},
	{
		// Сигнатура read_udp; у write_udp за client идёт другой параметр.
		File: "rust-client/dispatcher.rs", Count: 1,
		Pattern: `(?m)^( +)client: Arc<ArcSwapOption<SocketAddr>>,\n( +pool: Arc<PacketPool>,)$`,
		Replace: to("${1}client: Arc<ArcSwapOption<SocketAddr>>,\n${1}mut gate: crate::listen_peer::PeerGate,\n${2}"),
	},
	{
		// Апстрим здесь запоминает отправителя адресатом ответов; без
		// -listen-peer PeerGate::admit делает то же.
		File: "rust-client/dispatcher.rs", Count: 1,
		Pattern: `(?m)^( +)let previous = client\.load\(\);\n +if previous\.as_deref\(\) != Some\(&address\) \{\n +client\.store\(Some\(Arc::new\(address\)\)\);\n +\}$`,
		Replace: to("${1}if !gate.admit(address, &client) {\n${1}    continue;\n${1}}"),
	},

	// П5: окно капчи человеку — 180 секунд: ручное окно в конце цепочки auto
	// (у апстрима 60) и окно режима wv, selected (у апстрима 120), с которым
	// оболочка запускает ядро. Человеку минуты не хватает, а в окне wv часть
	// срока уйдёт ещё на автоклик оболочки. Тесты —
	// files/rust-client/captcha_window_tests.rs, сторож литералов —
	// TestCoreCaptchaWindowInCore в internal/captcha.
	{
		File: "rust-client/captcha.rs", Count: 1,
		Pattern: `(?m)^()pub struct CaptchaSolver \{$`,
		InsertBefore: "/// Правка П5 csqtt-cli: окно капчи человеку — ручное (manual) и режима wv\n" +
			"/// (selected). У апстрима — 60 и 120 секунд.\n" +
			"const MANUAL_WEBVIEW_TIMEOUT: Duration = Duration::from_secs(180);\n",
	},
	{
		File: "rust-client/captcha.rs", Count: 1,
		Pattern: `(?m)^( +)self\.webview\(stream_id, captcha, "manual", Duration::from_secs\(60\)\)$`,
		Replace: to(`${1}self.webview(stream_id, captcha, "manual", MANUAL_WEBVIEW_TIMEOUT)`),
	},
	{
		File: "rust-client/captcha.rs", Count: 1,
		Pattern: `(?m)^( +)self\.webview\(stream_id, captcha, "selected", Duration::from_secs\(120\)\)$`,
		Replace: to(`${1}self.webview(stream_id, captcha, "selected", MANUAL_WEBVIEW_TIMEOUT)`),
	},
	{
		File: "rust-client/captcha.rs", Count: 1,
		Pattern:      `(?m)^()#\[cfg\(test\)\]\nmod tests \{$`,
		InsertBefore: "#[cfg(test)]\n#[path = \"captcha_window_tests.rs\"]\nmod window_tests;\n",
	},

	// П7: выключатель решателя капчи ядра — переменная окружения
	// ONLY_PRINT_URL. До 2.5.0 её проверяло само ядро в начале solve_once, и
	// оболочка на этом строит Р7: с ONLY_PRINT_URL=1 ядро не ходит на страницу
	// капчи, а сразу отдаёт её окну оболочки строкой CAPTCHA_SOLVE. Апстрим
	// проверку убрал; без неё ядро само делало бы попытки Rust v2 и портило
	// сессию капчи (ERROR_LIMIT). Сторож — TestCoreSolverSwitchInCore в
	// internal/coreproc.
	{
		File: "rust-client/captcha.rs", Count: 1,
		Pattern: `(?m)^( +)async fn solve_once\(&self, captcha: &VkCaptchaError\) -> Result<String> \{$`,
		Replace: to("${1}async fn solve_once(&self, captcha: &VkCaptchaError) -> Result<String> {\n" +
			"${1}    // Правка П7 csqtt-cli: выключатель решателя ядра, как было до 2.5.0.\n" +
			"${1}    if std::env::var_os(\"ONLY_PRINT_URL\").is_some() {\n" +
			"${1}        bail!(\"ONLY_PRINT_URL active, skipping solve\");\n" +
			"${1}    }"),
	},

	// П3: -bind-if <индекс>, -mark <метка> и команда BIND_IF:<индекс> —
	// собственные сокеты ядра идут через физический интерфейс, мимо туннеля.
	// Флаги, проверка и сокеты точек 1–3 — files/rust-client/bind_if.rs;
	// общая настройка и setsockopt — в вендоренном primp-hyper-util
	// (files/…/src/csqtt_bind.rs), его коннектор привязывает соединения primp
	// (точки 4–5). Все места создания сокетов сверяет сторож — sockets.go.
	{
		File: "rust-client/main.rs", Count: 1,
		Pattern:     `(?m)^()mod auth;$`,
		InsertAfter: "mod bind_if;",
	},
	{
		File: "rust-client/main.rs", Count: 1,
		Pattern: `(?m)^( +)secrets_stdin: bool,$`,
		InsertAfter: "#[arg(long, default_value_t = 0)]\n${1}bind_if: u32,\n" +
			"${1}#[arg(long, default_value_t = 0)]\n${1}mark: u32,",
	},
	{
		File: "rust-client/main.rs", Count: 1,
		Pattern:     `(?m)^( +)"secrets-stdin",$`,
		InsertAfter: "\"bind-if\",\n${1}\"mark\",",
	},
	{
		// Первым делом в run: и туннель, и -validate-vk-hashes ходят к VK.
		// Неверный флаг — отказ стартовать, а не молча непривязанные сокеты.
		File: "rust-client/main.rs", Count: 1,
		Pattern:      `(?m)^( +)if arguments\.validate_vk_hashes \{$`,
		InsertBefore: "bind_if::configure(arguments.bind_if, arguments.mark)?;",
	},
	{
		File: "rust-client/main.rs", Count: 1,
		Pattern: `(?m)^( +)if let Some\(result\) = line\.strip_prefix\("CAPTCHA_RESULT\|"\) \{$`,
		InsertBefore: "if let Some(index) = line.strip_prefix(\"BIND_IF:\") {\n" +
			"${1}    bind_if::command(index);\n${1}    continue;\n${1}}",
	},
	// Точка 1 — DNS для имён VK.
	{
		File: "rust-client/dns.rs", Count: 1,
		Pattern: `(?m)^( +)let socket = UdpSocket::bind\(\(Ipv4Addr::UNSPECIFIED, 0\)\)$`,
		Replace: to("${1}let socket = crate::bind_if::udp_socket((Ipv4Addr::UNSPECIFIED, 0).into())"),
	},
	{
		// UdpSocket остаётся нужен тестам апстрима в этом же файле.
		File: "rust-client/dns.rs", Count: 1,
		Pattern: `(?m)^()use tokio::\{net::UdpSocket, time::timeout\};$`,
		Replace: to("use tokio::time::timeout;\n#[cfg(test)]\nuse tokio::net::UdpSocket;"),
	},
	// Точка 2 — TURN по UDP.
	{
		File: "rust-client/turn.rs", Count: 1,
		Pattern: `(?m)^( +)let socket = Arc::new\(UdpSocket::bind\(bind\)\.await\.context\("TURN UDP bind"\)\?\);$`,
		Replace: to("${1}let socket = Arc::new(\n" +
			"${1}    crate::bind_if::udp_socket(bind)\n${1}        .await\n${1}        .context(\"TURN UDP bind\")?,\n${1});"),
	},
	// UDP outbound accounting covers both queued and writer-owned packets.
	{
		File: "rust-client/turn.rs", Count: 1,
		Pattern: `(?m)^( +)queue: ArrayQueue<PacketBuf>,$`,
		Replace: to("${1}queue: ArrayQueue<OutboundPacket>,"),
	},
	{
		File: "rust-client/turn.rs", Count: 1,
		Pattern:     `(?m)^( +)socket: Arc<UdpSocket>,$`,
		InsertAfter: "completion: Arc<OutboundCompletion>,\n${1}#[cfg(test)]\n${1}test_writer_gate: Mutex<Option<Arc<outbound_tests::WriterGate>>>,",
	},
	{
		File: "rust-client/turn.rs", Count: 1,
		Pattern:      `(?m)^()async fn outbound_writer_loop\(queue: Arc<OutboundQueue>\) \{$`,
		InsertBefore: outboundCompletionCode,
	},
	{
		File: "rust-client/turn.rs", Count: 1,
		Pattern:     `(?m)^( +)socket: socket\.clone\(\),$`,
		InsertAfter: "completion: Arc::new(OutboundCompletion::default()),\n${1}#[cfg(test)]\n${1}test_writer_gate: Mutex::new(None),",
	},
	{
		File: "rust-client/turn.rs", Count: 1,
		Pattern: `(?m)^( +)while let Some\(mut packet\) = queue\.queue\.pop\(\) \{\n( +)queue\.space\.notify_one\(\);$`,
		Replace: to("${1}while let Some(mut packet) = queue.queue.pop() {\n${2}queue.space.notify_one();\n${2}#[cfg(test)]\n${2}let test_writer_gate = {\n${2}    queue\n${2}        .test_writer_gate\n${2}        .lock()\n${2}        .unwrap_or_else(|poisoned| poisoned.into_inner())\n${2}        .clone()\n${2}};\n${2}#[cfg(test)]\n${2}if let Some(gate) = test_writer_gate {\n${2}    gate.pause_after_pop().await;\n${2}}"),
	},
	{
		File: "rust-client/turn.rs", Count: 3,
		Pattern: `(?m)^( +)match queue\.queue\.push\(packet\) \{$`,
		Replace: to("${1}match queue.try_enqueue(packet) {"),
	},
	{
		File: "rust-client/turn.rs", Count: 1,
		Pattern: `(?m)^            while !queue\.queue\.is_empty\(\) && Instant::now\(\) < flush_deadline \{\n                let remaining = flush_deadline\.saturating_duration_since\(Instant::now\(\)\);\n                let space = queue\.space\.notified\(\);\n                if queue\.queue\.is_empty\(\) \{\n                    break;\n                \}\n                let _ = tokio::time::timeout\(remaining, space\)\.await;\n            \}$`,
		Replace: to("            queue.completion.close();\n            while Instant::now() < flush_deadline {\n                let remaining = flush_deadline.saturating_duration_since(Instant::now());\n                let completed = queue.completion.changed.notified();\n                tokio::pin!(completed);\n                completed.as_mut().enable();\n                if queue.completion.pending() == 0 {\n                    break;\n                }\n                if tokio::time::timeout(remaining, completed).await.is_err() {\n                    break;\n                }\n            }"),
	},
	{
		File: "rust-client/turn.rs", Count: 1,
		Pattern: `(?m)^        if let Some\(mut outbound_writer\) = outbound_writer\n            && tokio::time::timeout\(Duration::from_secs\(1\), &mut outbound_writer\)\n                \.await\n                \.is_err\(\)\n        \{\n            outbound_writer\.abort\(\);\n            let _ = outbound_writer\.await;\n        \}$`,
		Replace: to("        if let Some(mut outbound_writer) = outbound_writer\n            && tokio::time::timeout(Duration::from_secs(1), &mut outbound_writer)\n                .await\n                .is_err()\n        {\n            outbound_writer.abort();\n            let _ = outbound_writer.await;\n        }\n        if let Some(queue) = &self.outbound_queue {\n            queue.discard_queued();\n        }"),
	},
	{
		File: "rust-client/turn.rs", Count: 1,
		Pattern: `(?m)^()#\[cfg\(test\)\]\n#\[path = "turn_integration_tests\.rs"\]\nmod integration_tests;$`,
		Replace: to("#[cfg(test)]\n#[path = \"turn_integration_tests.rs\"]\npub(super) mod integration_tests;\n#[cfg(test)]\n#[path = \"turn_outbound_tests.rs\"]\nmod outbound_tests;"),
	},
	{
		File: "rust-client/turn_integration_tests.rs", Count: 1,
		Pattern: `(?m)^async fn connect\($`,
		Replace: to("pub(super) async fn connect("),
	},
	{
		File: "rust-client/turn_integration_tests.rs", Count: 1,
		Pattern: `(?m)^#\[tokio::test\(flavor = "multi_thread", worker_threads = 2\)\]\nasync fn data_packets_preserve_channel_order_and_release_idle_pool_leases\(\) \{$`,
		InsertBefore: "pub(super) async fn serve_outbound_test_flow(\n" +
			"    server: Arc<UdpSocket>,\n" +
			"    peer: SocketAddr,\n" +
			"    relay: SocketAddr,\n" +
			") -> Vec<Vec<u8>> {\n" +
			"    let client = receive_challenged_allocate(&server, relay).await;\n" +
			"    receive_create_permission(&server, client, peer).await;\n" +
			"    let (channel_wire, channel_client) = recv_datagram(&server).await;\n" +
			"    assert_eq!(channel_client, client);\n" +
			"    let channel_request = assert_authenticated_request(&channel_wire, CHANNEL_BIND_REQUEST);\n" +
			"    let channel = channel_number(&channel_request);\n" +
			"    server\n" +
			"        .send_to(\n" +
			"            &empty_authenticated_success(CHANNEL_BIND_SUCCESS, channel_request.transaction()),\n" +
			"            client,\n" +
			"        )\n" +
			"        .await\n" +
			"        .unwrap();\n" +
			"\n" +
			"    let mut outbound = Vec::new();\n" +
			"    loop {\n" +
			"        let (wire, outbound_client) = recv_datagram(&server).await;\n" +
			"        assert_eq!(outbound_client, client);\n" +
			"        if let Some((received_channel, payload_length)) = channel_data_header(&wire) {\n" +
			"            assert_eq!(received_channel, channel);\n" +
			"            outbound.push(wire[4..4 + payload_length].to_vec());\n" +
			"            continue;\n" +
			"        }\n" +
			"\n" +
			"        let refresh = assert_authenticated_request(&wire, REFRESH_REQUEST);\n" +
			"        assert_eq!(attribute_u32(&refresh, ATTR_LIFETIME), 0);\n" +
			"        server\n" +
			"            .send_to(&refresh_zero_success(refresh.transaction()), client)\n" +
			"            .await\n" +
			"            .unwrap();\n" +
			"        return outbound;\n" +
			"    }\n" +
			"}\n",
	},
	// Точка 3 — TURN по TCP и TLS: привязка до connect.
	{
		File: "rust-client/turn_stream.rs", Count: 1,
		Pattern: `(?m)^( +)let socket = timeout\(STREAM_CONNECT_TIMEOUT, TcpStream::connect\(server\)\)$`,
		Replace: to("${1}let socket = timeout(STREAM_CONNECT_TIMEOUT, crate::bind_if::connect_tcp(server))"),
	},
	{
		File: "rust-client/turn_stream.rs", Count: 1,
		Pattern: `(?m)^( +)net::TcpStream,\n`,
		Replace: to(""),
	},
	// Точки 6–8 — сокеты маршрутизации мимо туннеля (ядро 2.5.0): прямые
	// соединения к сайтам. Без привязки в tun они ушли бы маршрутами /1 обратно
	// в TUN, к самому ядру, — петля. Тесты —
	// files/rust-client/direct_relay_bind_if_tests.rs.
	{
		// Точка 6 — прямое TCP-соединение, под прежним таймаутом 5 с.
		File: "rust-client/direct_relay.rs", Count: 1,
		Pattern: `(?m)^( +)tokio::net::TcpStream::connect\(remote_addr\),$`,
		Replace: to("${1}crate::bind_if::connect_tcp(remote_addr),"),
	},
	{
		// Точка 7 — прямая UDP-сессия; адрес — того же семейства, что у
		// назначения, как у апстрима.
		File: "rust-client/direct_relay.rs", Count: 1,
		Pattern: `(?m)^( +)let bind_addr = if dst_addr\.is_ipv6\(\) \{\n +"\[::\]:0"\n +\} else \{\n +"0\.0\.0\.0:0"\n +\};\n +let Ok\(new_sock\) = tokio::net::UdpSocket::bind\(bind_addr\)\.await else \{$`,
		Replace: to("${1}let bind_addr = if dst_addr.is_ipv6() {\n" +
			"${1}    SocketAddr::from((Ipv6Addr::UNSPECIFIED, 0))\n" +
			"${1}} else {\n" +
			"${1}    SocketAddr::from((Ipv4Addr::UNSPECIFIED, 0))\n" +
			"${1}};\n" +
			"${1}let Ok(new_sock) = crate::bind_if::udp_socket(bind_addr).await else {"),
	},
	{
		// Точка 8 — DNS-запрос прямого домена к DNS Яндекса и прочим.
		File: "rust-client/direct_relay.rs", Count: 1,
		Pattern: `(?m)^( +)let socket = match tokio::net::UdpSocket::bind\("0\.0\.0\.0:0"\)\.await \{$`,
		Replace: to("${1}let socket = match crate::bind_if::udp_socket((Ipv4Addr::UNSPECIFIED, 0).into()).await {"),
	},
	{
		File: "rust-client/direct_relay.rs", Count: 1,
		Pattern:      `(?m)^()#\[cfg\(test\)\]\nmod tests \{$`,
		InsertBefore: "#[cfg(all(test, target_os = \"linux\"))]\n#[path = \"direct_relay_bind_if_tests.rs\"]\nmod bind_if_tests;\n",
	},
	// Точки 4 и 5 — клиенты primp: с -bind-if без прокси из окружения;
	// соединения привязывает коннектор primp-hyper-util.
	{
		File: "rust-client/auth.rs", Count: 1,
		Pattern: `(?m)^( +)let mut builder = Client::builder\(\)$`,
		Replace: to("${1}let mut builder = crate::bind_if::client_builder()"),
	},
	{
		File: "rust-client/vk_js_calls.rs", Count: 1,
		Pattern: `(?m)^( +)let client = Client::builder\(\)$`,
		Replace: to("${1}let client = crate::bind_if::client_builder()"),
	},
	// primp-hyper-util — из архива crates.io (crates.go), подключён через
	// [patch.crates-io]. Прямая зависимость нужна ядру ради настройки
	// csqtt_bind; версия — та же, что берёт primp-reqwest, поэтому в графе
	// крейт один. Не та версия — перенос падает (crates.go), а cargo --locked
	// не соберёт ядро с запиской в Cargo.lock ниже.
	{
		File: "rust-client/Cargo.toml", Count: 1,
		Pattern: `(?m)^()primp = "1\.3\.1"$`,
		InsertAfter: "# Правка П3 csqtt-cli: настройка привязки сокетов в вендоренном коннекторе primp.\n" +
			`primp_hyper_util = { package = "primp-hyper-util", version = "=0.1.22", features = ["client-legacy"] }`,
	},
	{
		File: "rust-client/Cargo.toml", Count: 1,
		Pattern:     `(?m)^()primp = \{ path = "vendor/primp" \}$`,
		InsertAfter: `primp-hyper-util = { path = "vendor/primp-hyper-util" }`,
	},
	{
		// Пакет по пути в Cargo.lock — без источника и суммы.
		File: "rust-client/Cargo.lock", Count: 1,
		Pattern: `(?m)^()name = "primp-hyper-util"\nversion = "0\.1\.22"\nsource = "registry\+https://github\.com/rust-lang/crates\.io-index"\nchecksum = "[0-9a-f]{64}"$`,
		Replace: to("name = \"primp-hyper-util\"\nversion = \"0.1.22\""),
	},
	{
		// Зависимости csqtt-client: "primp" там один на весь файл.
		File: "rust-client/Cargo.lock", Count: 1,
		Pattern:     `(?m)^( )"primp",$`,
		InsertAfter: `"primp-hyper-util",`,
	},
	{
		File: "rust-client/vendor/primp-hyper-util/src/lib.rs", Count: 1,
		Pattern:     `(?m)^()pub mod rt;$`,
		InsertAfter: "#[cfg(feature = \"client-legacy\")]\npub mod csqtt_bind;",
	},
	{
		// Каждое новое соединение — к текущим индексу и метке, после
		// штатных настроек сокета и до bind и connect. Не вышло — соединения
		// нет: непривязанный сокет ушёл бы в туннель.
		File: "rust-client/vendor/primp-hyper-util/src/client/legacy/connect/http.rs", Count: 1,
		Pattern: `(?m)^( +)bind_local_address\($`,
		InsertBefore: "// Правка П3 csqtt-cli: привязка к физическому интерфейсу (csqtt_bind.rs).\n" +
			"${1}crate::csqtt_bind::bind_socket(socket2::SockRef::from(&socket), addr.is_ipv6())\n" +
			"${1}    .map_err(ConnectError::m(\"tcp bind interface index error\"))?;\n",
	},

	// П4: дескриптор TUN по -tun-uds — только от процесса с тем же uid, что у
	// ядра (SO_PEERCRED). Чужое соединение закрывается, не читая, и приём
	// ждёт следующего. Проверка и тесты — files/rust-client/tun_peer.rs,
	// дочерний модуль tun.rs: тестам нужен слушатель изнутри.
	{
		File: "rust-client/tun.rs", Count: 1,
		Pattern: `(?m)^()use tokio_util::sync::CancellationToken;$`,
		InsertAfter: "\n// Правка П4 csqtt-cli: проверка отправителя дескриптора TUN.\n" +
			"#[cfg(unix)]\n#[path = \"tun_peer.rs\"]\nmod peer;",
	},
	{
		File: "rust-client/tun.rs", Count: 1,
		Pattern:     `(?m)^( +)listener: tokio::io::unix::AsyncFd<std::os::fd::OwnedFd>,$`,
		InsertAfter: "peer_check: peer::PeerCheck,",
	},
	{
		File: "rust-client/tun.rs", Count: 1,
		Pattern:     `(?m)^( +)listener: tokio::io::unix::AsyncFd::new\(listener\)\?,$`,
		InsertAfter: "peer_check: peer::PeerCheck::new(),",
	},
	{
		// Сразу после accept, до чтения: отказ — не ошибка receive, так что
		// у вызывающего нет ни ошибки в журнале, ни паузы перед повтором.
		// continue — к ожиданию следующего соединения (внутренний loop).
		File: "rust-client/tun.rs", Count: 1,
		Pattern: `(?m)^( +)let descriptor = unsafe \{ OwnedFd::from_raw_fd\(descriptor\) \};$`,
		InsertAfter: "// Правка П4 csqtt-cli: чужой uid — закрыть соединение и ждать следующего.\n" +
			"${1}if !self.peer_check.admit(&descriptor) {\n${1}    continue;\n${1}}",
	},
	// P8: STATS traffic split by path — bytes_up/bytes_down count the tunnel
	// only, direct_up/direct_down count the direct path in direct_relay.rs. A
	// packet is credited once, by the path that accepted it. The Rust
	// regression is files/rust-client/direct_stats_tests.rs; the event keys are
	// covered there too.
	{
		File: "rust-client/stats.rs", Count: 1,
		Pattern: `(?m)^( +)pub total_bytes_up: AtomicI64,\n( +)pub total_bytes_down: AtomicI64,$`,
		Replace: to("${1}/// Tunnel uplink only: packets `Dispatcher::dispatch` accepted for the tunnel.\n" +
			"${1}pub total_bytes_up: AtomicI64,\n" +
			"${1}/// Tunnel downlink only: worker packets and reassembled fragments. A packet\n" +
			"${1}/// is counted once, when it enters the inbound queue; a later queue eviction\n" +
			"${1}/// or a failed device write does not adjust the counter.\n" +
			"${1}pub total_bytes_down: AtomicI64,\n" +
			"${1}/// Direct-path uplink only: packets accepted by `DirectRelay::try_handle`.\n" +
			"${1}pub direct_bytes_up: AtomicI64,\n" +
			"${1}/// Direct-path downlink only: packets produced by the direct stack, counted\n" +
			"${1}/// once at the same inbound-queue point as the tunnel downlink.\n" +
			"${1}pub direct_bytes_down: AtomicI64,"),
	},
	{
		File: "rust-client/stats.rs", Count: 1,
		Pattern: `(?m)^( +)let down = self\.total_bytes_down\.load\(Ordering::Relaxed\);$`,
		Replace: to("${1}let down = self.total_bytes_down.load(Ordering::Relaxed);\n" +
			"${1}let direct_up = self.direct_bytes_up.load(Ordering::Relaxed);\n" +
			"${1}let direct_down = self.direct_bytes_down.load(Ordering::Relaxed);"),
	},
	{
		// The periodic Russian log line keeps its wording; its traffic figure
		// includes both paths.
		File: "rust-client/stats.rs", Count: 1,
		Pattern: `(?m)^( +)let total_mb = \(up \+ down\) as f64 / \(1024\.0 \* 1024\.0\);$`,
		Replace: to("${1}let total_mb = (up + down + direct_up + direct_down) as f64 / (1024.0 * 1024.0);"),
	},
	{
		File: "rust-client/stats.rs", Count: 1,
		Pattern: `(?m)^( +)events\.stats\(active, up, down\);$`,
		Replace: to("${1}events.stats(active, up, down, direct_up, direct_down);"),
	},

	{
		File: "rust-client/events.rs", Count: 1,
		Pattern: `(?m)^( +)pub fn stats\(&self, active: i32, bytes_up: i64, bytes_down: i64\) \{$`,
		Replace: to("${1}pub fn stats(\n" +
			"${1}    &self,\n" +
			"${1}    active: i32,\n" +
			"${1}    bytes_up: i64,\n" +
			"${1}    bytes_down: i64,\n" +
			"${1}    direct_up: i64,\n" +
			"${1}    direct_down: i64,\n" +
			"${1}) {"),
	},
	{
		File: "rust-client/events.rs", Count: 1,
		Pattern: `(?m)^( +)self\.emit\("STATS", \|\| \{\n( +)serde_json::json!\(\{\n( +)"active": active,\n( +)"bytes_up": bytes_up,\n( +)"bytes_down": bytes_down\n( +)\}\)\n( +)\}\);$`,
		Replace: to("${1}self.emit(\"STATS\", || {\n" +
			"${2}stats_payload(active, bytes_up, bytes_down, direct_up, direct_down)\n" +
			"${7}});"),
	},
	{
		// The payload is a function of its own so the Rust test asserts the
		// same key set the event emits.
		File: "rust-client/events.rs", Count: 1,
		Pattern: `(?m)^()fn encode_event<T: Serialize>`,
		InsertBefore: "/// JSON payload of the STATS event. Kept out of the emit closure so a\n" +
			"/// test can assert the exact key set without copying the literal.\n" +
			"pub(crate) fn stats_payload(\n" +
			"    active: i32,\n" +
			"    bytes_up: i64,\n" +
			"    bytes_down: i64,\n" +
			"    direct_up: i64,\n" +
			"    direct_down: i64,\n" +
			") -> serde_json::Value {\n" +
			"    serde_json::json!({\n" +
			"        \"active\": active,\n" +
			"        \"bytes_up\": bytes_up,\n" +
			"        \"bytes_down\": bytes_down,\n" +
			"        \"direct_up\": direct_up,\n" +
			"        \"direct_down\": direct_down\n" +
			"    })\n" +
			"}\n",
	},

	{
		File: "rust-client/dispatcher.rs", Count: 1,
		Pattern: `(?m)^()pub struct Dispatcher \{$`,
		InsertBefore: "/// Which path accepted an uplink packet. The read loops credit exactly one\n" +
			"/// counter per packet from this outcome.\n" +
			"#[derive(Clone, Copy, Debug, PartialEq, Eq)]\n" +
			"pub(crate) enum DispatchOutcome {\n" +
			"    /// The direct relay accepted the packet.\n" +
			"    Direct,\n" +
			"    /// The packet was accepted for the tunnel: handed to the worker path,\n" +
			"    /// whole or fragmented. A fragment set lost to buffer exhaustion still\n" +
			"    /// counts as tunnel uplink.\n" +
			"    Tunnel,\n" +
			"    /// No worker is registered, so the packet went nowhere.\n" +
			"    Dropped,\n" +
			"}\n",
	},
	{
		File: "rust-client/dispatcher.rs", Count: 1,
		Pattern:     `(?m)^( +)pool: Arc<PacketPool>,\n( +)reassembler: crate::wire_protocol::ShardedReassembler,$`,
		InsertAfter: "stats: Arc<Stats>,",
	},
	{
		// Both dispatcher constructors: start_test_tun and
		// start_with_domain_routing.
		File: "rust-client/dispatcher.rs", Count: 2,
		Pattern:     `(?m)^( +)inbound: ArrayQueue::new\(profile\.inbound_capacity\),\n( +)outbound_capacity: profile\.outbound_capacity,$`,
		InsertAfter: "stats: stats.clone(),",
	},
	{
		// The test literals that share these capacities.
		File: "rust-client/dispatcher.rs", Count: 2,
		Pattern:     `(?m)^( +)inbound: ArrayQueue::new\(16\),\n( +)outbound_capacity: 64,$`,
		InsertAfter: "stats: Arc::new(Stats::default()),",
	},
	{
		File: "rust-client/dispatcher.rs", Count: 1,
		Pattern:     `(?m)^( +)inbound: ArrayQueue::new\(16\),\n( +)outbound_capacity: 32,$`,
		InsertAfter: "stats: Arc::new(Stats::default()),",
	},
	{
		// The inbound-capacity test literal uses the queue profile variables.
		File: "rust-client/dispatcher.rs", Count: 1,
		Pattern:     `(?m)^( +)inbound: ArrayQueue::new\(inbound_capacity\),\n( +)outbound_capacity: profile\.outbound_capacity,$`,
		InsertAfter: "stats: Arc::new(Stats::default()),",
	},
	{
		File: "rust-client/dispatcher.rs", Count: 1,
		Pattern: `(?m)^( +)pub async fn dispatch\(&self, packet: PacketBuf\) \{$`,
		Replace: to("${1}pub async fn dispatch(&self, packet: PacketBuf) -> DispatchOutcome {"),
	},
	{
		File: "rust-client/dispatcher.rs", Count: 1,
		Pattern: `(?m)^( +)if let Some\(relay\) = &self\.direct_relay\n( +)&& relay\.try_handle\(&packet\)\.await\n( +)\{\n( +)return;\n( +)\}$`,
		Replace: to("${1}if let Some(relay) = &self.direct_relay\n" +
			"${2}&& relay.try_handle(&packet).await\n" +
			"${3}{\n" +
			"${4}return DispatchOutcome::Direct;\n" +
			"${5}}"),
	},
	{
		File: "rust-client/dispatcher.rs", Count: 1,
		Pattern: `(?m)^( +)let workers = self\.workers\.load\(\);\n( +)if workers\.is_empty\(\) \{\n( +)return;\n( +)\}$`,
		Replace: to("${1}let workers = self.workers.load();\n" +
			"${2}if workers.is_empty() {\n" +
			"${3}return DispatchOutcome::Dropped;\n" +
			"${4}}"),
	},
	{
		// The fragmentation branch and a fragment lost to packet buffers still
		// belong to the tunnel: the packet passed the worker check and never
		// reached the direct relay, so it cannot be Direct.
		File: "rust-client/dispatcher.rs", Count: 1,
		Pattern: `(?m)^( +)if frags\.len\(\) == expected_frags \{\n( +)self\.send_fragments_to_workers\(frags, &workers, flow, packet_len\)\n( +)\.await;\n( +)\} else \{\n( +)crate::log_error!\(\n( +)"\[ОШИБКА\] Dispatcher: фрагмент целиком потерян: \{\} из \{\} \(буферы\)",\n( +)frags\.len\(\),\n( +)expected_frags\n( +)\);\n( +)\}\n( +)\}\n( +)\}$`,
		Replace: to("${1}if frags.len() == expected_frags {\n" +
			"${2}self.send_fragments_to_workers(frags, &workers, flow, packet_len)\n" +
			"${3}.await;\n" +
			"${4}} else {\n" +
			"${5}crate::log_error!(\n" +
			"${6}\"[ОШИБКА] Dispatcher: фрагмент целиком потерян: {} из {} (буферы)\",\n" +
			"${7}frags.len(),\n" +
			"${8}expected_frags\n" +
			"${9});\n" +
			"${10}}\n" +
			"${11}}\n" +
			"${11}DispatchOutcome::Tunnel\n" +
			"${12}}"),
	},
	{
		// П9 later removes the early "no workers" exit in read_tun; the
		// accounting moves after dispatch.
		File: "rust-client/dispatcher.rs", Count: 1,
		Pattern: `(?m)^( +)stats\n( +)\.total_bytes_up\n( +)\.fetch_add\(length as i64, Ordering::Relaxed\);\n( +)self\.dispatch\(packet\)\.await;$`,
		Replace: to("${4}let outcome = self.dispatch(packet).await;\n" +
			"${4}credit_uplink(&stats, outcome, length);"),
	},
	{
		File: "rust-client/dispatcher.rs", Count: 1,
		Pattern: `(?m)^( +)stats\n( +)\.total_bytes_up\n( +)\.fetch_add\(packet\.len\(\) as i64, Ordering::Relaxed\);\n( +)self\.dispatch\(packet\)\.await;$`,
		Replace: to("${4}let length = packet.len();\n" +
			"${4}let outcome = self.dispatch(packet).await;\n" +
			"${4}credit_uplink(&stats, outcome, length);"),
	},
	{
		// Tunnel downlink from workers, reassembly and session.rs: counted where
		// it enters the inbound queue, not where the device write succeeds.
		File: "rust-client/dispatcher.rs", Count: 1,
		Pattern: `(?m)^( +)pub fn return_packet\(&self, mut packet: PacketBuf\) \{\n( +)strip_legacy_downlink_prefix\(&mut packet\);\n( +)self\.inspect_domain_routing\(&packet\);$`,
		Replace: to("${1}pub fn return_packet(&self, mut packet: PacketBuf) {\n" +
			"${2}strip_legacy_downlink_prefix(&mut packet);\n" +
			"${2}self.stats\n" +
			"${2}    .total_bytes_down\n" +
			"${2}    .fetch_add(packet.len() as i64, Ordering::Relaxed);\n" +
			"${3}self.inspect_domain_routing(&packet);"),
	},
	{
		// Direct downlink, counted at the direct stack's origin.
		File: "rust-client/dispatcher.rs", Count: 1,
		Pattern: `(?m)^( +)pub fn return_packet\(&self, mut packet: PacketBuf\) \{$`,
		InsertBefore: "/// Queues a packet produced by the direct path. Its bytes are credited\n" +
			"${1}/// here, at the origin, so a later queue eviction or a failed device write\n" +
			"${1}/// does not change the counter.\n" +
			"${1}pub fn push_direct_inbound(&self, packet: PacketBuf) {\n" +
			"${1}    self.stats\n" +
			"${1}        .direct_bytes_down\n" +
			"${1}        .fetch_add(packet.len() as i64, Ordering::Relaxed);\n" +
			"${1}    self.push_inbound(packet);\n" +
			"${1}}\n",
	},
	{
		File: "rust-client/dispatcher.rs", Count: 1,
		Pattern: `(?m)^( +)pump_dispatcher\.push_inbound\(pkt\);$`,
		Replace: to("${1}pump_dispatcher.push_direct_inbound(pkt);"),
	},
	{
		File: "rust-client/dispatcher.rs", Count: 1,
		Pattern: `(?m)^( +)async fn write_tun\(\n( +)&self,\n( +)device: Arc<tokio::io::unix::AsyncFd<File>>,\n( +)stats: Arc<Stats>,\n( +)cancel: CancellationToken,\n( +)\) \{$`,
		Replace: to("${1}async fn write_tun(\n" +
			"${2}&self,\n" +
			"${3}device: Arc<tokio::io::unix::AsyncFd<File>>,\n" +
			"${5}cancel: CancellationToken,\n" +
			"${6}) {"),
	},
	{
		File: "rust-client/dispatcher.rs", Count: 1,
		Pattern: `(?m)^( +)let write_stats = stats\.clone\(\);\n( +)let mut write_task = tokio::spawn\(async move \{\n( +)write_dispatcher\n( +)\.write_tun\(device, write_stats, write_cancel\)\n( +)\.await;\n( +)\}\);$`,
		Replace: to("${2}let mut write_task = tokio::spawn(async move {\n" +
			"${3}write_dispatcher\n" +
			"${4}.write_tun(device, write_cancel)\n" +
			"${5}.await;\n" +
			"${6}});"),
	},
	{
		File: "rust-client/dispatcher.rs", Count: 1,
		Pattern: `(?m)^ +stats\n +\.total_bytes_down\n +\.fetch_add\(packet\.len\(\) as i64, Ordering::Relaxed\);\n`,
		Replace: to(""),
	},
	{
		File: "rust-client/dispatcher.rs", Count: 1,
		Pattern: `(?m)^( +)async fn write_udp\(\n( +)&self,\n( +)socket: Arc<UdpSocket>,\n( +)client: Arc<ArcSwapOption<SocketAddr>>,\n( +)stats: Arc<Stats>,\n( +)\) \{$`,
		Replace: to("${1}async fn write_udp(\n" +
			"${2}&self,\n" +
			"${3}socket: Arc<UdpSocket>,\n" +
			"${4}client: Arc<ArcSwapOption<SocketAddr>>,\n" +
			"${6}) {"),
	},
	{
		File: "rust-client/dispatcher.rs", Count: 1,
		Pattern: `(?m)^( +)write_dispatcher\.write_udp\(socket, client, stats\)\.await;$`,
		Replace: to("${1}write_dispatcher.write_udp(socket, client).await;"),
	},
	{
		File: "rust-client/dispatcher.rs", Count: 2,
		Pattern: `(?m)^ +Ok\(length\) if length == (?:packet|next_packet)\.len\(\) => \{\n +stats\n +\.total_bytes_down\n +\.fetch_add\(length as i64, Ordering::Relaxed\);\n +\}\n`,
		Replace: to(""),
	},
	{
		File: "rust-client/dispatcher.rs", Count: 1,
		Pattern: `(?m)^( +)async fn write_udp_packet\(\n( +)&self,\n( +)socket: &UdpSocket,\n( +)address: Option<SocketAddr>,\n( +)stats: &Stats,\n( +)packet: &PacketBuf,\n( +)\) \{$`,
		Replace: to("${1}async fn write_udp_packet(\n" +
			"${2}&self,\n" +
			"${3}socket: &UdpSocket,\n" +
			"${4}address: Option<SocketAddr>,\n" +
			"${6}packet: &PacketBuf,\n" +
			"${7}) {"),
	},
	{
		File: "rust-client/dispatcher.rs", Count: 2,
		Pattern: `(?m)^( +)self\.write_udp_packet\(&socket, Some\(address\), &stats, &(next_packet|packet)\)$`,
		Replace: to("${1}self.write_udp_packet(&socket, Some(address), &${2})"),
	},
	{
		File: "rust-client/dispatcher.rs", Count: 1,
		Pattern: `(?m)^ +Ok\(length\) if length == packet\.len\(\) => \{\n +stats\n +\.total_bytes_down\n +\.fetch_add\(length as i64, Ordering::Relaxed\);\n +return;\n +\}\n`,
		Replace: to(""),
	},
	{
		// Uplink accounting: one counter per packet, chosen by the outcome.
		File: "rust-client/dispatcher.rs", Count: 1,
		Pattern: `(?m)^()#\[inline\(always\)\]\nfn strip_legacy_downlink_prefix\(packet: &mut PacketBuf\) \{$`,
		InsertBefore: "/// Credits an uplink packet to the path that accepted it. The read loops\n" +
			"/// call this once per packet after `dispatch`; a packet dropped before\n" +
			"/// dispatch never reaches this point.\n" +
			"fn credit_uplink(stats: &Stats, outcome: DispatchOutcome, length: usize) {\n" +
			"    match outcome {\n" +
			"        DispatchOutcome::Direct => {\n" +
			"            stats\n" +
			"                .direct_bytes_up\n" +
			"                .fetch_add(length as i64, Ordering::Relaxed);\n" +
			"        }\n" +
			"        DispatchOutcome::Tunnel => {\n" +
			"            stats\n" +
			"                .total_bytes_up\n" +
			"                .fetch_add(length as i64, Ordering::Relaxed);\n" +
			"        }\n" +
			"        DispatchOutcome::Dropped => {}\n" +
			"    }\n" +
			"}\n",
	},
	{
		// П9: the direct path needs no worker, so read_tun must not drop the
		// packet before dispatch. dispatch drops tunnel-bound packets itself.
		File: "rust-client/dispatcher.rs", Count: 1,
		Pattern: `(?m)^( +)if self\.workers\.load\(\)\.is_empty\(\) \{\n( +)continue;\n( +)\}\n( +)let outcome = self\.dispatch\(packet\)\.await;`,
		Replace: to("${1}// П9: the direct path needs no worker; dispatch drops tunnel-bound packets itself.\n" +
			"${4}let outcome = self.dispatch(packet).await;"),
	},
	{
		File: "rust-client/dispatcher.rs", Count: 1,
		Pattern:      `(?m)^()#\[cfg\(test\)\]\nmod tests \{$`,
		InsertBefore: "#[cfg(test)]\n#[path = \"direct_stats_tests.rs\"]\nmod direct_stats_tests;\n",
	},
	{
		// The TUN direct regression is a child module of dispatcher, like the
		// P8 stats tests. It is Unix-only: read_tun is Unix-only.
		File: "rust-client/dispatcher.rs", Count: 1,
		Pattern:     `(?m)^#\[cfg\(test\)\]\n#\[path = "direct_stats_tests\.rs"\]\nmod direct_stats_tests;\n`,
		InsertAfter: "#[cfg(all(test, unix))]\n#[path = \"tun_direct_tests.rs\"]\nmod tun_direct_tests;\n",
	},

	// П10: a failed direct TCP connect must reach the application as a reset,
	// not as a silent close. The stack answers the application's SYN with
	// SYN-ACK before the outer connect starts, so the server-side sequence is
	// recorded per flow from the frames leaving the direct stack, keyed like
	// active_tcp. On a connect error or the five-second timeout the listener
	// sends an RST with the recorded sequence plus one. The destination address
	// is remembered for a bounded period, so new TCP connections to it take the
	// tunnel instead of another direct attempt. The regression is
	// files/rust-client/direct_relay_failure_tests.rs.
	{
		// The bounded wait for the record; production passes it to the handler.
		File: "rust-client/direct_relay.rs", Count: 1,
		Pattern: `(?m)^()pub const DIRECT_RELAY_INBOUND_CAPACITY: usize = 128;$`,
		InsertAfter: `
/// П10: how long the failure path waits for the SYN-ACK record of a flow. The
/// stack emits the SYN-ACK before the outer connect starts, so this covers
/// only the accept-versus-egress task-start race: the stack's frame normally
/// arrives first.
const SYN_ACK_RECORD_WAIT: Duration = Duration::from_millis(500);

/// П10: how long a destination whose direct connect failed is not tried
/// directly. Production uses five minutes; tests inject a shorter period.
const FAILED_DIRECT_MEMORY_TTL: Duration = Duration::from_secs(300);`,
	},
	{
		File: "rust-client/direct_relay.rs", Count: 1,
		Pattern: `(?m)^( +)active_tcp: Arc<RwLock<HashMap<\(SocketAddr, SocketAddr\), std::time::Instant>>>,$`,
		InsertBefore: "/// П10: the server-side sequence of the SYN-ACK the stack sent to the\n" +
			"${1}/// application, keyed like active_tcp. An application-facing reset\n" +
			"${1}/// refers to it.\n" +
			"${1}#[cfg_attr(not(test), allow(dead_code))]\n" +
			"${1}syn_ack_seqs: Arc<RwLock<HashMap<(SocketAddr, SocketAddr), u32>>>,\n" +
			"${1}/// П10: destination addresses whose direct connect failed, with the\n" +
			"${1}/// instant after which a direct attempt is allowed again. The lookup\n" +
			"${1}/// ignores an expired entry; the cleanup task prunes it.\n" +
			"${1}failed_direct_ips: Arc<RwLock<HashMap<IpAddr, std::time::Instant>>>,",
	},
	{
		File: "rust-client/direct_relay.rs", Count: 1,
		Pattern:     `(?m)^( +)active_tcp,$`,
		InsertAfter: "syn_ack_seqs,\n${1}failed_direct_ips,",
	},
	{
		// The map must exist before the egress task reads the first frame and
		// before the listener accepts a connection.
		File: "rust-client/direct_relay.rs", Count: 1,
		Pattern: `(?m)^( +)let pool_egress = pool\.clone\(\);$`,
		InsertBefore: "// П10: the SYN-ACK record of a flow, created before the egress task\n" +
			"${1}// reads the first frame and before the listener accepts a connection.\n" +
			"${1}let syn_ack_seqs = Arc::new(RwLock::new(HashMap::new()));\n" +
			"${1}let syn_ack_seqs_egress = syn_ack_seqs.clone();",
	},
	{
		// The failed-address memory must exist before the listener accepts a
		// connection and before the cleanup task starts.
		File: "rust-client/direct_relay.rs", Count: 1,
		Pattern: `(?m)^( +)let active_tcp = Arc::new\(RwLock::new\(HashMap::new\(\)\)\);$`,
		InsertAfter: "// П10: destinations whose direct connect failed, created before the\n" +
			"${1}// listener accepts a connection and before the cleanup task starts.\n" +
			"${1}let failed_direct_ips = Arc::new(RwLock::new(HashMap::new()));",
	},
	{
		// The record must not depend on pool availability and must exist before
		// the SYN-ACK can reach the pump.
		File: "rust-client/direct_relay.rs", Count: 1,
		Pattern: `(?m)^( +)let Some\(mut pkt\) = pool_egress\.try_acquire\(\) else \{$`,
		InsertBefore: "// П10: record the SYN-ACK sequence before the pool check.\n" +
			"${1}record_syn_ack(&syn_ack_seqs_egress, &frame);",
	},
	{
		// Clones shared by the per-connection tasks of the listener.
		File: "rust-client/direct_relay.rs", Count: 1,
		Pattern: `(?m)^( +)if let Some\(mut listener\) = tcp_listener \{$`,
		InsertBefore: "// П10: the failure path of an accepted flow needs the pool, the inbound\n" +
			"${1}// channel, the SYN-ACK records and the failed-address memory.\n" +
			"${1}let abort_pool = pool.clone();\n" +
			"${1}let abort_inbound_tx = inbound_tx.clone();\n" +
			"${1}let abort_syn_ack_seqs = syn_ack_seqs.clone();\n" +
			"${1}let abort_failed_direct_ips = failed_direct_ips.clone();",
	},
	{
		File: "rust-client/direct_relay.rs", Count: 1,
		Pattern: `(?m)^( +)let active_tcp_conn = active_tcp_c\.clone\(\);$`,
		InsertAfter: "let abort_pool_conn = abort_pool.clone();\n" +
			"${1}let abort_inbound_tx_conn = abort_inbound_tx.clone();\n" +
			"${1}let abort_syn_ack_seqs_conn = abort_syn_ack_seqs.clone();\n" +
			"${1}let abort_failed_direct_ips_conn = abort_failed_direct_ips.clone();",
	},
	{
		// A successful connect and a later broken copy loop do not reset: only
		// the unsuccessful outcome of the five-second attempt takes the else.
		// The anchor includes the success branch tail, so it cannot match
		// another block.
		File: "rust-client/direct_relay.rs", Count: 1,
		Pattern: `(?m)^( +)drop\(server_stream\);\n( +)\}\n\n( +)drop\(client_stream\);$`,
		Replace: to("${1}drop(server_stream);\n" +
			"${2}} else {\n" +
			"${2}    // П10: the application already saw the stack's SYN-ACK, so a\n" +
			"${2}    // failed outer connect closes it with a reset built from the\n" +
			"${2}    // recorded sequence.\n" +
			"${2}    abort_failed_direct_tcp(\n" +
			"${2}        &abort_pool_conn,\n" +
			"${2}        &abort_inbound_tx_conn,\n" +
			"${2}        &abort_syn_ack_seqs_conn,\n" +
			"${2}        key,\n" +
			"${2}        SYN_ACK_RECORD_WAIT,\n" +
			"${2}        &abort_failed_direct_ips_conn,\n" +
			"${2}        FAILED_DIRECT_MEMORY_TTL,\n" +
			"${2}    )\n" +
			"${2}    .await;\n" +
			"${2}}\n" +
			"\n" +
			"${3}drop(client_stream);"),
	},
	{
		// A long successful flow and a failed one both leave no stale record.
		File: "rust-client/direct_relay.rs", Count: 1,
		Pattern: `(?m)^( +)if let Ok\(mut guard\) = active_tcp_conn\.write\(\) \{\n( +)guard\.remove\(&key\);\n( +)\}$`,
		InsertAfter: "if let Ok(mut guard) = abort_syn_ack_seqs_conn.write() {\n" +
			"${1}    guard.remove(&key);\n${1}}",
	},
	{
		// A new TCP SYN to a destination whose direct connect failed takes the
		// tunnel. The check goes after the established-flow branch and before
		// evaluate_route, so open direct flows stay direct and UDP is untouched.
		File: "rust-client/direct_relay.rs", Count: 1,
		Pattern: `(?m)^( +)let domain = self\.dns_domain_map\.get\(&dst_ip\);$`,
		InsertBefore: "// П10: a destination whose direct connect failed is not tried directly\n" +
			"${1}// again while the memory entry is valid; the SYN takes the tunnel.\n" +
			"${1}if protocol == 6\n" +
			"${1}    && failed_direct_active(&self.failed_direct_ips, dst_ip, std::time::Instant::now())\n" +
			"${1}{\n" +
			"${1}    return false;\n" +
			"${1}}",
	},
	{
		// The memory is cleared only when the whole routing configuration is
		// off: with IP routing alone the direct path can still be chosen.
		File: "rust-client/direct_relay.rs", Count: 1,
		Pattern: `(?m)^( +)if !config\.ip_routing_enabled \{\n( +)self\.direct_ips\n( +)\.store\(Arc::new\(std::collections::HashSet::new\(\)\)\);$`,
		Replace: to("${0}\n" +
			"${2}// П10: the whole routing configuration is off, so the failed-address\n" +
			"${2}// memory goes with the direct addresses.\n" +
			"${2}if let Ok(mut guard) = self.failed_direct_ips.write() {\n" +
			"${2}    guard.clear();\n" +
			"${2}}"),
	},
	{
		// The cleanup task prunes expired failed-address entries as well.
		File: "rust-client/direct_relay.rs", Count: 1,
		Pattern: `(?m)^( +)let active_tcp_cleanup = active_tcp\.clone\(\);$`,
		InsertAfter: "// П10: the failed-address memory is pruned by the same task.\n" +
			"${1}let failed_direct_cleanup = failed_direct_ips.clone();",
	},
	{
		// An expired entry is already ignored by the lookup; the prune only
		// keeps the map small, on the same 30-second tick as active_tcp.
		File: "rust-client/direct_relay.rs", Count: 1,
		Pattern: `(?m)^( +)if let Ok\(mut guard\) = active_tcp_cleanup\.write\(\) \{\n( +)guard\.retain\(\|_, last_seen\| \{\n( +)now\.duration_since\(\*last_seen\) < Duration::from_secs\(120\)\n( +)\}\);\n( +)\}$`,
		InsertAfter: "// П10: keep the failed-address memory small; the lookup already\n" +
			"${1}// ignores an expired entry.\n" +
			"${1}if let Ok(mut guard) = failed_direct_cleanup.write() {\n" +
			"${1}    guard.retain(|_, deadline| *deadline > now);\n" +
			"${1}}",
	},
	{
		// The record and the handler are free functions beside the packet
		// builders, so the egress task and the listener share one code path.
		File: "rust-client/direct_relay.rs", Count: 1,
		Pattern:      `(?m)^()pub fn craft_tcp_packet_into\($`,
		InsertBefore: synAckRecordCode,
	},
	{
		// A child of direct_relay, like the file's own tests: the private
		// fields and the handler are reachable. The rule goes after the П3
		// bind_if_tests insertion and anchors on its generated block: mod tests
		// is already used by another rule.
		File: "rust-client/direct_relay.rs", Count: 1,
		Pattern:     `(?m)^#\[cfg\(all\(test, target_os = "linux"\)\)\]\n#\[path = "direct_relay_bind_if_tests\.rs"\]\nmod bind_if_tests;\n`,
		InsertAfter: "#[cfg(test)]\n#[path = \"direct_relay_failure_tests.rs\"]\nmod failure_tests;\n",
	},
}

// ================================================================================
