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

	// П5: ручное окно капчи в конце цепочки auto — 180 секунд вместо 60:
	// человеку минуты не хватает. Тесты — files/rust-client/captcha_window_tests.rs.
	{
		File: "rust-client/captcha.rs", Count: 1,
		Pattern: `(?m)^()pub struct CaptchaSolver \{$`,
		InsertBefore: "/// Правка П5 csqtt-cli: ручное окно капчи. У апстрима — 60 секунд.\n" +
			"const MANUAL_WEBVIEW_TIMEOUT: Duration = Duration::from_secs(180);\n",
	},
	{
		File: "rust-client/captcha.rs", Count: 1,
		Pattern: `(?m)^( +)self\.webview\(stream_id, captcha, "manual", Duration::from_secs\(60\)\)$`,
		Replace: to(`${1}self.webview(stream_id, captcha, "manual", MANUAL_WEBVIEW_TIMEOUT)`),
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
}

// ================================================================================
