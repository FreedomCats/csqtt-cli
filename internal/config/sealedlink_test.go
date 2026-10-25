package config

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"reflect"
	"strings"
	"testing"
)

// Эталоны — ссылки, собранные кодом панели сервера CSQTT 2.5.0 (web_panel.rs,
// make_client_link) с выдуманными данными: сервер 203.0.113.10, порт 46000,
// пароль test-password, хеши hashA/hashB, токен serverToken. IV у сервера
// выводится из открытой строки, поэтому ссылки постоянные.
const (
	serverToken = "vk1.a.TestOnlyFakeToken_0123456789-xyz"

	// connect?v=2&host=203.0.113.10&peer=46000&password=test-password&hashes=<hashA>+<hashB>
	sealedHashes = "csqtt://nfdZdIKJlTLSnnoOyG8uYV3XMvERdcH-Cyzu1RKKLdLebaCWF_UcxyVa4nQLaqkIuxGkFMLv5NSuYaKQL5kiv893T-" +
		"cvTf6akVlpFjP6RdKgnBx07h4ha-uXNy3lDINg1-q9MhOUbGMXC0E4XG_R-tzUb-fP8ehUW30IrRbLJNwqeS-WowI9pL8Oe89mcAs_XSgp1g"
	// connect?v=2&host=203.0.113.10&peer=46000&password=test-password&token=<serverToken>
	sealedToken = "csqtt://7SP-1s2C8n2-AjxM_M52KEijaC1JvZtqpJaM7j-xN6WaT1K0DGgMxlgVHoqA-rMSPsmupz1tC5NBM1i16_VBK2w6890M" +
		"zAPgEzihd94BxJm28UzAXtZH8oVFUJHjP2p9hcYSf60ksQX4IbkUzH2-pJEPe_Txh0rUALTPEL8zChsjPPiMuMJ0LA"
	// connect?v=2&host=203.0.113.10&peer=46000&hashes=<hashA> — без пароля.
	sealedNoPassword = "csqtt://kQNd_JMxPCDhIVpWViay208dNTp8DvwkUc08lGQp6CXPyud9F_CuNidOOGU80ldlq3V2qrSTQcoylFl_8M2EH_yB" +
		"4dwjesq303rng19Oq0sl4r7Xrck7S5_6acZAhxxqOGNHDQ"
	// connect?v=2&host=203.0.113.10&peer=46000&token=<serverToken> — без пароля.
	sealedNoPasswordToken = "csqtt://Csp3ZBwkm49WUjSKCFhHRiGkwLZtp6kAjel0is8jQsZrwxkT0b0AtdX9xZlkJnd8q5rtyRtypYKzveh7vFTfSRmM" +
		"XclLZyyMR0ErWGTvpgbYTnBAmYl5tXJtu2yzBOH6iTCMAr9dCB16T6tTsKbcnmk"
	// connect?v=2&host=203.0.113.10&peer=46000&password=p%40ss%2Bw%3Drd%2Fx
	sealedEncodedPassword = "csqtt://RqB7oc_pDxUpCHpJos5PAFVnJWBs521p0Rh9XaZIJgcwaVrdBmuCRBNPYCaChBnQpSVwQ85cm5Xi_nPlLu1ArKgq-" +
		"Q3smxpAknijLFrvD-1jK28wGi2QDUOLG8wixF-lBQ"
	// connect?v=2&host=2001%3Adb8%3A%3A10&peer=46001&password=test-password
	sealedIPv6 = "csqtt://IkyrDFxFivX3EkaN5sueVpVTHxQnDIor8jtuWzy2Pzoa0-wqX9g8_weBdNg89SikFpodjZr6jC3_a9AFOqH1LX9UtKbsclEqA" +
		"DEf5HkPuFwas85-mZsBSV7Rke2MEajB-A"
)

// sealLink шифрует открытую строку так же, как сервер: ключ — SHA-256
// затравки, IV — первые 12 байт HMAC-SHA256 открытой строки на том же ключе.
func sealLink(t *testing.T, plain string) string {
	t.Helper()
	return "csqtt://" + base64.RawURLEncoding.EncodeToString(sealRaw(t, linkCipherSeed, plain))
}

func sealRaw(t *testing.T, seed, plain string) []byte {
	t.Helper()
	key := sha256.Sum256([]byte(seed))
	mac := hmac.New(sha256.New, key[:])
	mac.Write([]byte(plain))
	iv := mac.Sum(nil)[:sealedNonceSize]
	block, err := aes.NewCipher(key[:])
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	return gcm.Seal(append([]byte(nil), iv...), iv, []byte(plain), nil)
}

// Своё шифрование по описанию формата даёт те же ссылки, что сервер: формат
// понят верно, и тесты ниже, собранные sealLink, — того же вида.
func TestSealLinkMatchesServer(t *testing.T) {
	for plain, want := range map[string]string{
		"connect?v=2&host=203.0.113.10&peer=46000&password=test-password&hashes=" + hashA + "+" + hashB: sealedHashes,
		"connect?v=2&host=203.0.113.10&peer=46000&password=test-password&token=" + serverToken:          sealedToken,
		"connect?v=2&host=203.0.113.10&peer=46000&hashes=" + hashA:                                      sealedNoPassword,
		"connect?v=2&host=2001%3Adb8%3A%3A10&peer=46001&password=test-password":                         sealedIPv6,
	} {
		if got := sealLink(t, plain); got != want {
			t.Errorf("%s:\nсвоё  %s\nсервер %s", plain, got, want)
		}
	}
}

func TestParseSealedLink(t *testing.T) {
	const peer = "203.0.113.10:46000"
	cases := []struct {
		name string
		in   string
		want ShareLink
	}{
		{"сервер: хеши", sealedHashes, ShareLink{Peer: peer, Password: "test-password", Hashes: []string{hashA, hashB}}},
		{"сервер: токен", sealedToken, ShareLink{Peer: peer, Password: "test-password", VKToken: serverToken}},
		{"сервер: без пароля", sealedNoPassword, ShareLink{Peer: peer, Hashes: []string{hashA}}},
		{"сервер: без пароля, токен", sealedNoPasswordToken, ShareLink{Peer: peer, VKToken: serverToken}},
		{"сервер: пароль с кодированием", sealedEncodedPassword, ShareLink{Peer: peer, Password: "p@ss+w=rd/x"}},
		{"сервер: IPv6", sealedIPv6, ShareLink{Peer: "[2001:db8::10]:46001", Password: "test-password"}},
		{"только пароль", sealLink(t, "connect?v=2&host=vps.example&peer=46001&password=secret"),
			ShareLink{Peer: "vps.example:46001", Password: "secret"}},
		{"пробелы по краям", "  " + sealedToken + "\n",
			ShareLink{Peer: peer, Password: "test-password", VKToken: serverToken}},
		// Ссылку разорвали переносом строки при копировании.
		{"перенос внутри", sealedToken[:60] + "\r\n" + sealedToken[60:],
			ShareLink{Peer: peer, Password: "test-password", VKToken: serverToken}},
		{"с «=» и обычным base64", "csqtt://" + base64.StdEncoding.EncodeToString(
			sealRaw(t, linkCipherSeed, "connect?v=2&host=203.0.113.10&peer=46000&password=a/b+c")),
			ShareLink{Peer: peer, Password: "a/b+c"}},
		// Запасной вид сервера: шифрование не удалось — открытая строка в base64.
		{"base64 без шифрования", "csqtt://" + base64.RawURLEncoding.EncodeToString(
			[]byte("connect?v=2&host=203.0.113.10&peer=46000&password=test-password")),
			ShareLink{Peer: peer, Password: "test-password"}},
	}
	for _, tt := range cases {
		got, err := ParseShareLink(tt.in)
		if err != nil {
			t.Errorf("%s: %v", tt.name, err)
			continue
		}
		if !reflect.DeepEqual(*got, tt.want) {
			t.Errorf("%s: %+v, ожидалось %+v", tt.name, *got, tt.want)
		}
	}
}

func TestParseSealedLinkRejects(t *testing.T) {
	// Последний знак base64 несёт только часть байта; портится предпоследний —
	// и это байт тега.
	broken := []byte(sealedToken)
	if broken[len(broken)-2] == 'A' {
		broken[len(broken)-2] = 'B'
	} else {
		broken[len(broken)-2] = 'A'
	}
	body := strings.TrimPrefix(sealedToken, linkScheme)
	foreign := "csqtt://" + base64.RawURLEncoding.EncodeToString(
		sealRaw(t, "other-key", "connect?v=2&host=203.0.113.10&peer=46000&password=test-password"))
	cases := []struct {
		name, in, want string
	}{
		{"битый тег", string(broken), "не расшифровывается"},
		{"чужой ключ", foreign, "не расшифровывается"},
		{"обрезана", sealedToken[:40], "не расшифровывается"},
		// Знаков base64 на один больше, чем кратно четырём, не бывает.
		{"обрезана до лишнего знака", linkScheme + body[:len(body)/4*4-3], "не раскодируется"},
		{"мусор", "csqtt://AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", "не расшифровывается"},
		{"короткий мусор", "csqtt://abc", "не расшифровывается"},
		{"не base64", "csqtt://abc!def", "ожидалась"},
		{"внутри не connect", sealLink(t, "https://203.0.113.10/?password=test-password"), "не ссылка connect"},
		{"внутри пусто", sealLink(t, "connect?"), "нет параметров"},
		{"хеши и токен", sealLink(t, "connect?v=2&host=203.0.113.10&peer=46000&password=test-password&hashes="+
			hashA+"&token="+serverToken), "и хеши звонков, и токен"},
		{"внутри v=3", sealLink(t, "connect?v=3&host=203.0.113.10&peer=46000&password=test-password"), "v=3"},
		{"дважды подряд", sealedToken + sealedToken, "дважды"},
	}
	for _, tt := range cases {
		_, err := ParseShareLink(tt.in)
		if err == nil {
			t.Errorf("%s: ссылка принята", tt.name)
			continue
		}
		if !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: в сообщении нет %q: %v", tt.name, tt.want, err)
		}
		for _, secret := range []string{"test-password", serverToken, "TestOnly", "203.0.113.10"} {
			if strings.Contains(err.Error(), secret) {
				t.Errorf("%s: сообщение печатает содержимое ссылки: %v", tt.name, err)
			}
		}
	}
}
