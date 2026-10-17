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
	"strconv"
	"strings"
)

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
	VKAuthMode  string
	CaptchaMode string
	// DeviceID — постоянный на профиль: сервер может закрепить пароль за
	// первым устройством (§10).
	DeviceID string
	// Listen — адрес UDP, где ядро ждёт пакеты оболочки (datapath socks);
	// TunUDS — имя абстрактного unix-сокета для дескриптора TUN (Linux).
	Listen string
	TunUDS string
}

// Args — аргументы командной строки ядра. Флаги — в виде --имя=значение.
// Пароля и хешей среди них нет: командную строку процесса видит любой
// пользователь машины. Вместо них — --secrets-stdin, и ядро ждёт SecretsLine.
func (c Config) Args() []string {
	args := []string{"--secrets-stdin"}
	add := func(name, value string) {
		if value != "" {
			args = append(args, "--"+name+"="+value)
		}
	}
	add("peer", c.Peer)
	add("workers", strconv.Itoa(NormalizeWorkers(c.Workers, len(c.Hashes))))
	add("obfs", c.Obfs)
	add("turn-transport", c.TurnTransport)
	add("turn", c.TurnHost)
	add("port", c.TurnPort)
	add("fingerprint", c.Fingerprint)
	add("client-ids", strings.Join(c.ClientIDs, ","))
	add("vk-auth-mode", c.VKAuthMode)
	add("captcha-mode", c.CaptchaMode)
	add("device-id", c.DeviceID)
	add("listen", c.Listen)
	add("tun-uds", c.TunUDS)
	return args
}

// SecretsLine — первая строка stdin ядра, без перевода строки:
// SECRETS:<base64 json> с паролем и хешами. vk — то же, что значение -vk
// ядра: хеши через запятую.
func (c Config) SecretsLine() string {
	b, err := json.Marshal(struct {
		Password string `json:"password"`
		VK       string `json:"vk"`
	}{c.Password, strings.Join(c.Hashes, ",")})
	if err != nil {
		panic(err) // две строки маршалятся всегда
	}
	return "SECRETS:" + base64.StdEncoding.EncodeToString(b)
}

// Постоянные распределения воркеров — из ядра (main.rs, worker.rs).
const (
	workersPerGroup = 9
	maxWorkers      = 126
	maxHashes       = 6
	groupsPerHash   = 3
)

// NormalizeWorkers — сколько воркеров ядро запустит на самом деле: целые
// группы по 9, от 9 до 126 и не больше 27 на хеш (берутся первые шесть
// хешей). Повторяет normalize_worker_count_for_hashes ядра без
// перераспределения хешей — его оболочка не включает.
func NormalizeWorkers(requested, hashes int) int {
	n := min(max(requested, workersPerGroup), maxWorkers) / workersPerGroup * workersPerGroup
	return min(n, min(max(hashes, 1), maxHashes)*groupsPerHash*workersPerGroup)
}
