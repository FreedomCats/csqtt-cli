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
}

// ================================================================================
