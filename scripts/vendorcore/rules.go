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
// шёл вместе с каждой копией кода.
var vendoredFiles = map[string]string{
	"LICENSE": "LICENSE",
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

// rules — правки П1–П6. Набор пополняется задачами C3, C4, W2, L1.
var rules = []rule{
	// П1: флаг -secrets-stdin — пароль и хеши VK первой строкой stdin. Сам
	// разбор — в files/rust-client/secrets_stdin.rs; здесь только подключение.
	{
		File: "rust-client/main.rs", Count: 1,
		Pattern:     `(?m)^()mod repair;$`,
		InsertAfter: "mod secrets_stdin;",
	},
	{
		File: "rust-client/main.rs", Count: 1,
		Pattern:     `(?m)^( +)validate_vk_hashes: bool,$`,
		InsertAfter: "#[arg(long, default_value_t = false)]\n${1}secrets_stdin: bool,",
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
		Pattern: `(?m)^( +)let dispatcher_result = Dispatcher::start\(\n( +)&arguments\.listen,$`,
		Replace: to("${1}let dispatcher_result = Dispatcher::start_with_peer(\n${2}&arguments.listen,\n${2}listen_peer,"),
	},
	{
		File: "rust-client/main.rs", Count: 1,
		Pattern:     `(?m)^( +)let local_port: Arc<str> = Arc::from\(local_port\);$`,
		InsertAfter: "listen_peer::report_port(&events, &local_port);",
	},
	{
		// start остаётся для тестов апстрима — без закрепления; в сборке его
		// больше никто не зовёт.
		File: "rust-client/dispatcher.rs", Count: 1,
		Pattern: `(?m)^( +)pub async fn start\(\n( +)listen: &str,\n( +tun_uds: Option<String>,\n +pool: Arc<PacketPool>,\n +stats: Arc<Stats>,\n +cancel: CancellationToken,\n +\) -> Result<\(Arc<Self>, String\)> \{)$`,
		Replace: to("${1}#[cfg(test)]\n${1}pub async fn start(\n${2}listen: &str,\n${3}\n" +
			"${2}Self::start_with_peer(listen, None, tun_uds, pool, stats, cancel).await\n" +
			"${1}}\n\n" +
			"${1}pub async fn start_with_peer(\n${2}listen: &str,\n${2}listen_peer: Option<SocketAddr>,\n${3}"),
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
		Pattern: `(?m)^()#\[cfg\(test\)\]\nmod tests \{$`,
		InsertBefore: "#[cfg(test)]\n#[path = \"captcha_window_tests.rs\"]\nmod window_tests;\n",
	},
}

// ================================================================================
