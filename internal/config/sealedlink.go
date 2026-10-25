// sealedlink.go — зашифрованная ссылка csqtt:// панели сервера CSQTT 2.5.0.
//
// Формат (сервер, web_panel.rs, encrypt_csqtt_link): после «csqtt://» —
// base64url без «=» от IV (12 байт), за которым шифртекст AES-256-GCM
// открытой строки с тегом (16 байт).
//
// Ключ — SHA-256 от linkCipherSeed, дополнительных данных GCM нет. Открытая
// строка — ссылка connect?v=2&… без «csqtt://». IV сервер берёт из
// HMAC-SHA256 открытой строки на том же ключе, но сверять его незачем:
// целостность проверяет тег GCM, ключ и так общий для всех, а свой разбор
// сервера IV не сверяет. Не удалось зашифровать — сервер пишет ту же строку
// просто в base64url; такая ссылка тоже принимается.
//
// Ключ один на все серверы и записан в их коде: шифрование прячет
// содержимое ссылки от взгляда, но не от того, у кого она в руках. Ссылка
// остаётся секретом.
package config

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

// linkCipherSeed — из чего сервер выводит ключ шифрования ссылок
// (CSQTT_CIPHER_KEY_SEED в web_panel.rs; «chiper» — как у сервера).
const linkCipherSeed = "csqtt-chiper-key"

// sealedNonceSize — длина IV в начале зашифрованной ссылки: та же, что у
// cipher.NewGCM по умолчанию.
const sealedNonceSize = 12

// connectPrefix — с чего начинается открытая строка внутри ссылки.
const connectPrefix = "connect?"

// isSealedLink — похоже ли то, что после «csqtt://», на base64. В двух других
// видах есть «?» или «@», которых в base64 нет. Знаки обычного base64 («+»,
// «/», «=») тоже допускаются: ссылку могли перекодировать по дороге.
// Пробелы — разрыв строки при копировании, их уберёт parseSealedLink.
func isSealedLink(rest string) bool {
	seen := false
	for _, r := range rest {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r >= '0' && r <= '9',
			r == '-', r == '_', r == '+', r == '/', r == '=':
			seen = true
		case unicode.IsSpace(r):
		default:
			return false
		}
	}
	return seen
}

// parseSealedLink расшифровывает ссылку и разбирает открытую строку так же,
// как csqtt://connect?…. Ошибки содержимого не повторяют.
func parseSealedLink(rest string) (*ShareLink, error) {
	body := strings.Map(func(r rune) rune {
		switch {
		case unicode.IsSpace(r), r == '=':
			return -1
		case r == '+':
			return '-'
		case r == '/':
			return '_'
		}
		return r
	}, rest)
	raw, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return nil, errors.New("ссылка обрезана или повреждена: она не раскодируется; скопируйте её целиком ещё раз")
	}

	plain, ok := openSealed(raw)
	if !ok {
		// Запасной вид сервера — открытая строка в base64 без шифрования.
		if !hasConnectPrefix(raw) || !utf8.Valid(raw) {
			return nil, errors.New("ссылка повреждена или выдана не сервером CSQTT: она не расшифровывается; " +
				"скопируйте её целиком ещё раз")
		}
		plain = raw
	}
	if !hasConnectPrefix(plain) || !utf8.Valid(plain) {
		return nil, errors.New("ссылка расшифрована, но внутри не ссылка connect — такую панель сервера CSQTT не выдаёт")
	}
	query := string(plain[len(connectPrefix):])
	if query == "" {
		return nil, errors.New("в ссылке нет параметров; скопируйте её целиком ещё раз")
	}
	return parseConnectLink(query)
}

// openSealed снимает шифрование: IV ‖ шифртекст с тегом. false — тег не
// сошёлся (ссылку испортили или зашифровал не сервер CSQTT) или данных
// меньше, чем IV и тег.
func openSealed(raw []byte) ([]byte, bool) {
	key := sha256.Sum256([]byte(linkCipherSeed))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, false
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, false
	}
	if len(raw) < sealedNonceSize+gcm.Overhead() {
		return nil, false
	}
	plain, err := gcm.Open(nil, raw[:sealedNonceSize], raw[sealedNonceSize:], nil)
	if err != nil {
		return nil, false
	}
	return plain, true
}

func hasConnectPrefix(b []byte) bool {
	return len(b) >= len(connectPrefix) && strings.EqualFold(string(b[:len(connectPrefix)]), connectPrefix)
}
