// Package coreproc — процесс ядра CSQTT: аргументы запуска, разбор вывода,
// команды в stdin и остановка (§04 doc/architecture.md).
//
// Ядро — отдельный бинарь csqtt-core. Оболочка запускает его с
// CSQTT_EVENTS=1: события идут строками в stdout, журнал — в stderr, команды —
// строками в stdin. Закрытый stdin ядро считает командой остановиться.
package coreproc

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
)

// VKHashModeAutoJS — режим, в котором звонок VK создаёт само ядро по токену
// аккаунта пользователя, а хешей звонков не нужно (§08а doc/architecture.md).
const VKHashModeAutoJS = "auto_js"

// Config — параметры запуска ядра. Пустое поле не передаётся, и ядро берёт
// своё значение по умолчанию.
type Config struct {
	// Peer — сервер CSQTT, хост:порт.
	Peer string
	// Password — пароль подключения, из него ядро выводит WRAP-ключ.
	// Hashes — хеши звонков VK. Оба идут не аргументами, а первой строкой
	// stdin (SecretsLine, правка П1).
	Password string
	Hashes   []string
	// Workers — число воркеров; в аргумент идёт NormalizeWorkers.
	Workers int
	// Obfs — audio или video; TurnTransport — udp или tcp_tls.
	Obfs          string
	TurnTransport string
	// TurnHost и TurnPort — свой TURN вместо выданного VK.
	TurnHost string
	TurnPort string
	// Fingerprint — отпечаток TLS для запросов к VK.
	Fingerprint string
	ClientIDs   []string
	// VKAuthMode — режим авторизации ядра (vkcalls, legacy) для живых
	// прогонов капчи; в профиле его нет. В auto_js не действует: там он
	// auto_js всегда.
	VKAuthMode  string
	CaptchaMode string
	// VKHashMode — пусто (хеши из Hashes) или VKHashModeAutoJS: звонок
	// создаёт ядро. В auto_js ядро получает оба своих режима — хешей и
	// авторизации — и --allow-hash-redistribution; Hashes ему не уходят.
	VKHashMode string
	// VKToken — access token VK для auto_js. Секрет: идёт только в stdin,
	// строкой VK_JS_BOOTSTRAP: той же записью, что SecretsLine, и только
	// ядру в auto_js.
	VKToken string
	// DeviceID — постоянный на профиль: сервер может закрепить пароль за
	// первым устройством (§10).
	DeviceID string
	// Listen — адрес UDP, где ядро ждёт пакеты оболочки (datapath socks);
	// ListenPeer — адрес сокета оболочки: ядро принимает датаграмы только
	// с него и отвечает только ему (правка П2), свой порт сообщает
	// ListenEvent. TunUDS — имя абстрактного unix-сокета для дескриптора TUN
	// (Linux), с ListenPeer несовместимо.
	Listen     string
	ListenPeer string
	TunUDS     string
	// BindIf — индекс физического интерфейса: все сокеты ядра наружу идут
	// через него, мимо туннеля (правка П3); сменить на ходу — BindIf.
	// Mark — метка SO_MARK на те же сокеты, только Linux и только вместе с
	// BindIf. Ноль — не передаётся.
	BindIf uint32
	Mark   uint32
}

// Args — аргументы командной строки ядра. Флаги — в виде --имя=значение.
// Пароля, хешей и токена VK среди них нет: командную строку процесса видит
// любой пользователь машины. Вместо них — --secrets-stdin, и ядро ждёт
// SecretsLine (startInput).
func (c Config) Args() []string {
	args := []string{"--secrets-stdin"}
	add := func(name, value string) {
		if value != "" {
			args = append(args, "--"+name+"="+value)
		}
	}
	add("peer", c.Peer)
	add("workers", strconv.Itoa(c.NormalizedWorkers()))
	add("obfs", c.Obfs)
	add("turn-transport", c.TurnTransport)
	add("turn", c.TurnHost)
	add("port", c.TurnPort)
	add("fingerprint", c.Fingerprint)
	add("client-ids", strings.Join(c.ClientIDs, ","))
	if c.AutoJS() {
		// Режим авторизации auto_js ядро без такого же режима хешей не
		// принимает, поэтому они идут парой. Перераспределение хешей ядро в
		// auto_js включает и само, флаг — как у других клиентов CSQTT.
		add("vk-hash-mode", VKHashModeAutoJS)
		add("vk-auth-mode", VKHashModeAutoJS)
		args = append(args, "--allow-hash-redistribution")
	} else {
		add("vk-auth-mode", c.VKAuthMode)
	}
	add("captcha-mode", c.CaptchaMode)
	add("device-id", c.DeviceID)
	add("listen", c.Listen)
	add("listen-peer", c.ListenPeer)
	add("tun-uds", c.TunUDS)
	if c.BindIf != 0 {
		add("bind-if", strconv.FormatUint(uint64(c.BindIf), 10))
	}
	if c.Mark != 0 {
		add("mark", strconv.FormatUint(uint64(c.Mark), 10))
	}
	return args
}

// AutoJS — звонок создаёт ядро (VKHashModeAutoJS).
func (c Config) AutoJS() bool { return c.VKHashMode == VKHashModeAutoJS }

// SecretsLine — первая строка stdin ядра, без перевода строки:
// SECRETS:<base64 json> с паролем и хешами. vk — то же, что значение -vk
// ядра: хеши через запятую. В auto_js хешей нет и поля vk тоже.
func (c Config) SecretsLine() string {
	var hashes string
	if !c.AutoJS() {
		hashes = strings.Join(c.Hashes, ",")
	}
	b, err := json.Marshal(struct {
		Password string `json:"password"`
		VK       string `json:"vk,omitempty"`
	}{c.Password, hashes})
	if err != nil {
		panic(err) // две строки маршалятся всегда
	}
	return "SECRETS:" + base64.StdEncoding.EncodeToString(b)
}

// ErrNoVKToken — ядру в auto_js не дали токена VK: без него оно не создаст
// звонок, и запускать его незачем.
var ErrNoVKToken = errors.New("ядру в режиме auto_js нужен токен VK")

// maxStartInput — предел первой записи в stdin ядра. Запись не длиннее
// PIPE_BUF ядро получает одним чтением, и строка VK_JS_BOOTSTRAP: целиком
// оказывается в буфере вместе с секретами (startInput). Токен VK — сотни
// знаков, так что предел с запасом.
const maxStartInput = 4096

// startInput — первая запись в stdin ядра: SecretsLine, а в auto_js следом
// VK_JS_BOOTSTRAP:<base64 {"token":"…"}>, каждая строка с переводом строки.
//
// Обе строки — одной записью, и это не удобство. Секреты ядро читает сразу
// при запуске из общего буфера stdin, а bootstrap — позже, после разрешения
// имени сервера, своим буфером, который потом выбрасывает. Если bootstrap
// пришёл той же записью, он уже лежит в общем буфере, и его читатель не
// тронет канал: команды, пришедшие позже, дождутся читателя команд. Отдельной
// записью bootstrap мог бы прийти в одном чтении с командами — и они бы
// пропали.
//
// Ядру не в auto_js bootstrap не уходит: его прочёл бы читатель команд и
// повторил бы в журнал вместе с токеном.
func (c Config) startInput() (string, error) {
	input := c.SecretsLine() + "\n"
	if !c.AutoJS() {
		return input, nil
	}
	if strings.TrimSpace(c.VKToken) == "" {
		return "", ErrNoVKToken
	}
	b, err := json.Marshal(struct {
		Token string `json:"token"`
	}{c.VKToken})
	if err != nil {
		panic(err) // строка маршалится всегда
	}
	input += bootstrapPrefix + base64.StdEncoding.EncodeToString(b) + "\n"
	if len(input) > maxStartInput {
		// Значения не печатаются: в записи пароль и токен.
		return "", errors.New("токен VK или пароль слишком длинные: секреты не уместились в одну запись в stdin ядра")
	}
	return input, nil
}

// Постоянные распределения воркеров — из ядра (main.rs, worker.rs).
const (
	workersPerGroup = 9
	maxWorkers      = 126
	maxHashes       = 6
	groupsPerHash   = 3
)

// MaxWorkersAutoJS — предел воркеров в auto_js. Ядро допускает 126, но
// каждый запрос кредов открывает новую сессию аккаунта VK, и чем больше
// воркеров, тем больше следов в аккаунте; 72 — как у Android-клиента CSQTT.
const MaxWorkersAutoJS = 72

// NormalizedWorkers — сколько воркеров получит ядро: NormalizeWorkers по
// хешам, а в auto_js — NormalizeWorkersAutoJS.
func (c Config) NormalizedWorkers() int {
	if c.AutoJS() {
		return NormalizeWorkersAutoJS(c.Workers)
	}
	return NormalizeWorkers(c.Workers, len(c.Hashes))
}

// NormalizeWorkers — сколько воркеров ядро запустит на самом деле: целые
// группы по 9, от 9 до 126 и не больше 27 на хеш (берутся первые шесть
// хешей). Повторяет normalize_worker_count_for_hashes ядра без
// перераспределения хешей — его оболочка включает только в auto_js
// (NormalizeWorkersAutoJS).
func NormalizeWorkers(requested, hashes int) int {
	return min(normalizeGroups(requested), min(max(hashes, 1), maxHashes)*groupsPerHash*workersPerGroup)
}

// NormalizeWorkersAutoJS — число воркеров в auto_js: целые группы по 9, от 9
// до MaxWorkersAutoJS. Предела на хеш нет: ядро в auto_js перераспределяет
// хеш созданного звонка всегда.
func NormalizeWorkersAutoJS(requested int) int {
	return min(normalizeGroups(requested), MaxWorkersAutoJS)
}

// normalizeGroups — целые группы по 9, от 9 до 126 (normalize_worker_count
// ядра).
func normalizeGroups(requested int) int {
	return min(max(requested, workersPerGroup), maxWorkers) / workersPerGroup * workersPerGroup
}
