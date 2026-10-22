package coreproc

import (
	"encoding/json"
	"net/netip"
	"strings"
)

// Message — одна строка вывода ядра: событие, запрос капчи, итог проверки
// хеша или строка журнала.
type Message interface{ message() }

// События ядра (events.rs): __CSQTT_EVENT__|<вид>|<json>.
type (
	// PIDEvent — ядро запустилось, pid его процесса.
	PIDEvent struct {
		PID int `json:"pid"`
	}
	// ListenEvent — порт UDP-сокета ядра в режиме -listen (правка П2).
	ListenEvent struct {
		Port int `json:"port"`
	}
	// ConfigEvent — сервер выдал адрес туннеля и DNS (TUNCONF). Повторное
	// событие — смена DNS на ходу.
	ConfigEvent struct {
		Raw     string
		Address netip.Addr
		DNS     []netip.Addr
	}
	ProgressEvent struct {
		Kind string `json:"kind"`
	}
	// ReadyEvent — воркер подключился.
	ReadyEvent struct {
		Worker int `json:"worker"`
	}
	StatsEvent struct {
		Active    int   `json:"active"`
		BytesUp   int64 `json:"bytes_up"`
		BytesDown int64 `json:"bytes_down"`
	}
	// ActiveZeroEvent — живых воркеров не осталось.
	ActiveZeroEvent struct{}
	// NetworkSuspectEvent — серия таймаутов TURN.
	NetworkSuspectEvent struct{}
	// CallUnavailableEvent — звонок по хешу недоступен.
	CallUnavailableEvent struct {
		Hash string `json:"hash"`
		Code int64  `json:"code"`
	}
	// ServerRestartEvent — сервер перезапущен из панели.
	ServerRestartEvent struct {
		Source string `json:"source"`
	}
	// ErrorEvent — ошибка; Fatal — попытка подключения окончена.
	ErrorEvent struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Fatal   bool   `json:"fatal"`
	}
	// StoppedEvent — ядро закончило уборку и сейчас выйдет.
	StoppedEvent struct{}
)

// CaptchaRequest — ядро просит решить капчу (captcha.rs):
// CAPTCHA_SOLVE|<auto|manual>|<redirect_uri>|<session_token>. Ответ —
// команда CaptchaResult или CaptchaFailed.
type CaptchaRequest struct {
	Mode         string
	RedirectURI  string
	SessionToken string
}

// HashCheck — итог проверки хеша в режиме --validate-vk-hashes:
// HASH_CHECK:<json>. Status — valid, invalid или unavailable.
type HashCheck struct {
	Hash    string `json:"hash"`
	Status  string `json:"status"`
	Code    int64  `json:"code"`
	Message string `json:"message"`
}

// Log — строка журнала ядра: весь stderr и то в stdout, что не разобралось.
type Log struct {
	Text string
}

func (PIDEvent) message()             {}
func (ListenEvent) message()          {}
func (ConfigEvent) message()          {}
func (ProgressEvent) message()        {}
func (ReadyEvent) message()           {}
func (StatsEvent) message()           {}
func (ActiveZeroEvent) message()      {}
func (NetworkSuspectEvent) message()  {}
func (CallUnavailableEvent) message() {}
func (ServerRestartEvent) message()   {}
func (ErrorEvent) message()           {}
func (StoppedEvent) message()         {}
func (CaptchaRequest) message()       {}
func (HashCheck) message()            {}
func (Log) message()                  {}

const (
	eventPrefix     = "__CSQTT_EVENT__|"
	captchaPrefix   = "CAPTCHA_SOLVE|"
	hashCheckPrefix = "HASH_CHECK:"
	tunConfPrefix   = "TUNCONF:"
)

// ParseLine разбирает строку stdout ядра. Неизвестное событие или
// испорченная строка — Log: новое ядро может добавить своё, и оболочке
// незачем из-за этого падать.
func ParseLine(line string) Message {
	switch {
	case strings.HasPrefix(line, eventPrefix):
		kind, payload, ok := strings.Cut(line[len(eventPrefix):], "|")
		if ok {
			if m := parseEvent(kind, payload); m != nil {
				return m
			}
		}
	case strings.HasPrefix(line, captchaPrefix):
		f := strings.SplitN(line[len(captchaPrefix):], "|", 3)
		if len(f) == 3 && f[0] != "" && f[1] != "" && f[2] != "" {
			return CaptchaRequest{Mode: f[0], RedirectURI: f[1], SessionToken: f[2]}
		}
	case strings.HasPrefix(line, hashCheckPrefix):
		var h HashCheck
		if json.Unmarshal([]byte(line[len(hashCheckPrefix):]), &h) == nil && h.Hash != "" {
			// Ошибка сети «недоступного» хеша несёт адрес шага 2 гостевого
			// пути (auth.rs, check_vk_hash_attempt) с anonymous_token.
			h.Message = maskGuestKeys(h.Message)
			return h
		}
	}
	return Log{Text: maskLog(line)}
}

// parseEvent — событие по виду и JSON; nil, если разобрать нельзя.
func parseEvent(kind, payload string) Message {
	decode := func(v any) bool { return json.Unmarshal([]byte(payload), v) == nil }
	switch kind {
	case "PROCESS":
		var e PIDEvent
		if decode(&e) {
			return e
		}
	case "LISTEN":
		var e ListenEvent
		if decode(&e) && e.Port > 0 && e.Port <= 65535 {
			return e
		}
	case "CONFIG":
		var raw struct {
			Config string `json:"config"`
		}
		if decode(&raw) {
			if e, ok := parseTunConf(raw.Config); ok {
				return e
			}
		}
	case "PROGRESS":
		var e ProgressEvent
		if decode(&e) {
			return e
		}
	case "READY":
		var e ReadyEvent
		if decode(&e) {
			return e
		}
	case "STATS":
		var e StatsEvent
		if decode(&e) {
			return e
		}
	case "ACTIVE_ZERO":
		return ActiveZeroEvent{}
	case "NETWORK_SUSPECT":
		return NetworkSuspectEvent{}
	case "CALL_UNAVAILABLE":
		var e CallUnavailableEvent
		if decode(&e) {
			return e
		}
	case "SERVER_RESTART":
		var e ServerRestartEvent
		if decode(&e) {
			return e
		}
	case "ERROR":
		var e ErrorEvent
		if decode(&e) {
			return e
		}
	case "STOPPED":
		return StoppedEvent{}
	}
	return nil
}

// parseTunConf разбирает TUNCONF:<адрес>:<dns,…>. Ядро режет строку на три
// части по двоеточию (main.rs), третью не читает — здесь её тоже пропускаем.
// Только IPv4: адрес IPv6 в такой записи не уместился бы.
func parseTunConf(config string) (ConfigEvent, bool) {
	rest, ok := strings.CutPrefix(config, tunConfPrefix)
	if !ok {
		return ConfigEvent{}, false
	}
	f := strings.SplitN(rest, ":", 3)
	addr, err := netip.ParseAddr(f[0])
	if err != nil || !addr.Is4() {
		return ConfigEvent{}, false
	}
	e := ConfigEvent{Raw: config, Address: addr}
	if len(f) > 1 {
		for s := range strings.SplitSeq(f[1], ",") {
			if d, err := netip.ParseAddr(strings.TrimSpace(s)); err == nil && d.Is4() {
				e.DNS = append(e.DNS, d)
			}
		}
	}
	return e, true
}
