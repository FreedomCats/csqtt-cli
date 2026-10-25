package coreproc

import (
	"net/netip"
	"reflect"
	"testing"
)

// sampleOutput — строки stdout ядра в его формате (events.rs, captcha.rs,
// main.rs) и что из них должно получиться. Адреса — документационные,
// хеш и токены выдуманы.
var sampleOutput = []struct {
	line string
	want Message
}{
	{`__CSQTT_EVENT__|PROCESS|{"pid":4242}`, PIDEvent{PID: 4242}},
	{`__CSQTT_EVENT__|LISTEN|{"port":40123}`, ListenEvent{Port: 40123}},
	{`__CSQTT_EVENT__|CONFIG|{"config":"TUNCONF:10.66.66.2:198.51.100.53,198.51.100.54"}`, ConfigEvent{
		Raw:     "TUNCONF:10.66.66.2:198.51.100.53,198.51.100.54",
		Address: netip.MustParseAddr("10.66.66.2"),
		DNS:     []netip.Addr{netip.MustParseAddr("198.51.100.53"), netip.MustParseAddr("198.51.100.54")},
	}},
	{`__CSQTT_EVENT__|READY|{"worker":3}`, ReadyEvent{Worker: 3}},
	{`__CSQTT_EVENT__|STATS|{"active":18,"bytes_down":2048,"bytes_up":1024}`, StatsEvent{Active: 18, BytesUp: 1024, BytesDown: 2048}},
	{`__CSQTT_EVENT__|PROGRESS|{"kind":"credentials"}`, ProgressEvent{Kind: "credentials"}},
	{`__CSQTT_EVENT__|ACTIVE_ZERO|{}`, ActiveZeroEvent{}},
	{`__CSQTT_EVENT__|NETWORK_SUSPECT|{}`, NetworkSuspectEvent{}},
	{`__CSQTT_EVENT__|CALL_UNAVAILABLE|{"code":954,"hash":"fake-hash"}`, CallUnavailableEvent{Hash: "fake-hash", Code: 954}},
	{`__CSQTT_EVENT__|SERVER_RESTART|{"source":"panel"}`, ServerRestartEvent{Source: "panel"}},
	{`__CSQTT_EVENT__|WORKERS|{"workers":30}`, WorkersEvent{Limit: 30}},
	{`__CSQTT_EVENT__|WORKERS|{"workers":0}`, WorkersEvent{Limit: 0}},
	{`__CSQTT_EVENT__|TUN_LOST|{}`, TunLostEvent{}},
	{`__CSQTT_EVENT__|DTLS|{"message":"DTLS-хендшейк успешен (ECDSA P-256)","worker":2}`,
		DTLSEvent{Worker: 2, Message: "DTLS-хендшейк успешен (ECDSA P-256)"}},
	{`__CSQTT_EVENT__|DTLS|{"message":"рукопожатие не состоялось (timeout)","worker":11}`,
		DTLSEvent{Worker: 11, Message: "рукопожатие не состоялось (timeout)"}},
	{`__CSQTT_EVENT__|ERROR|{"code":"handshake_timeout","fatal":true,"message":"FATAL_HANDSHAKE: no response"}`,
		ErrorEvent{Code: "handshake_timeout", Message: "FATAL_HANDSHAKE: no response", Fatal: true}},
	{`CAPTCHA_SOLVE|auto|https://id.vk.com/not_robot_captcha?a=1|fake-session`,
		CaptchaRequest{Mode: "auto", RedirectURI: "https://id.vk.com/not_robot_captcha?a=1", SessionToken: "fake-session"}},
	{`HASH_CHECK:{"code":954,"hash":"fake-hash","message":"Call not found","status":"invalid"}`,
		HashCheck{Hash: "fake-hash", Status: "invalid", Code: 954, Message: "Call not found"}},
	{`HASH_CHECK:{"hash":"fake-hash","status":"valid"}`, HashCheck{Hash: "fake-hash", Status: "valid"}},
	{`__CSQTT_EVENT__|STOPPED|{}`, StoppedEvent{}},
}

func TestParseLine(t *testing.T) {
	for _, c := range sampleOutput {
		if got := ParseLine(c.line); !reflect.DeepEqual(got, c.want) {
			t.Errorf("ParseLine(%q) = %#v, want %#v", c.line, got, c.want)
		}
	}
}

// Неизвестное и испорченное — строка журнала, а не ошибка.
func TestParseLineFallsBackToLog(t *testing.T) {
	for _, line := range []string{
		"[КЛИЕНТ] Воркеров: 18 (групп: 2, по 9)",
		`__CSQTT_EVENT__|NEW_KIND|{"x":1}`,
		`__CSQTT_EVENT__|READY|{"worker":`,
		`__CSQTT_EVENT__|READY`,
		`__CSQTT_EVENT__|LISTEN|{"port":0}`,
		`__CSQTT_EVENT__|LISTEN|{"port":70000}`,
		`__CSQTT_EVENT__|WORKERS|{"workers":-1}`,
		`__CSQTT_EVENT__|WORKERS|{"workers":"30"}`,
		`__CSQTT_EVENT__|DTLS|{"worker":"2"}`,
		`__CSQTT_EVENT__|ROUTING|{}`,
		`__CSQTT_EVENT__|ROUTING|{"routing":5}`,
		`__CSQTT_EVENT__|ROUTING|{"routing":`,
		`__CSQTT_EVENT__|CONFIG|{"config":"NOCONF"}`,
		`__CSQTT_EVENT__|CONFIG|{"config":"TUNCONF:fd00::2:198.51.100.53"}`,
		`CAPTCHA_SOLVE|auto|https://id.vk.com/x`,
		`HASH_CHECK:not json`,
		`HASH_CHECK:{}`,
	} {
		if got, want := ParseLine(line), (Log{Text: line}); got != want {
			t.Errorf("ParseLine(%q) = %#v, want Log", line, got)
		}
	}
}

func TestParseTunConfSkipsBadDNS(t *testing.T) {
	got, ok := parseTunConf("TUNCONF:10.66.66.2:bad, 198.51.100.53 ,:extra")
	want := ConfigEvent{
		Raw:     "TUNCONF:10.66.66.2:bad, 198.51.100.53 ,:extra",
		Address: netip.MustParseAddr("10.66.66.2"),
		DNS:     []netip.Addr{netip.MustParseAddr("198.51.100.53")},
	}
	if !ok || !reflect.DeepEqual(got, want) {
		t.Errorf("parseTunConf = %#v, %v", got, ok)
	}
	if got, ok := parseTunConf("TUNCONF:10.66.66.2"); !ok || got.DNS != nil {
		t.Errorf("TUNCONF без DNS = %#v, %v", got, ok)
	}
}
