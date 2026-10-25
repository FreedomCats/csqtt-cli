// sharelink.go — профиль из ссылки csqtt://.
//
// Ссылку выдаёт панель сервера CSQTT («Скопировать ссылку csqtt://»), её же
// вставляют в Android-клиент. Три вида:
//
//	csqtt://<base64url>
//	csqtt://connect?v=2&host=<хост>&peer=<порт>[&password=<пароль>][&hashes=<хеш>+<хеш>… | &token=<токен VK>]
//	csqtt://<пароль>@<хост>:<порт>
//
// Первый пишет панель сервера 2.5.0: внутри — второй вид без «csqtt://»,
// зашифрованный (sealedlink.go). Во втором peer — не адрес, а только порт
// сервера; значения закодированы (encodeURIComponent у старой панели,
// процентное кодирование всего, кроме букв, цифр и -_.~, у 2.5.0), поэтому
// «+» между хешами — разделитель, а «+» внутри значения пришёл бы как %2B.
// Пробелом «+» не считается нигде. Хешей — от одного до шести. Пароля в
// ссылке 2.5.0 может не быть — его спрашивают отдельно. Вместо хешей панель
// 2.5.0 может дать токен VK владельца сервера: звонки тогда создаёт ядро от
// его аккаунта (auto_js). В третьем, старом, виде хешей нет.
//
// В ссылке пароль и хеши — она сама секрет. Принимать её аргументом командной
// строки нельзя, и сообщения об ошибках её не повторяют.
package config

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"unicode"
)

// ShareLink — то, что прочитано из ссылки.
type ShareLink struct {
	// Peer — адрес сервера, host:port.
	Peer string
	// Password — пароль подключения; пусто — в ссылке его не было.
	Password string
	// Hashes — хеши звонков VK; пусто — в ссылке их не было.
	Hashes []string
	// VKToken — access token VK, который сервер выдал вместе с паролем
	// (параметр token): звонки по нему создаёт ядро в режиме auto_js от
	// аккаунта владельца сервера. Пусто — в ссылке его не было. С Hashes
	// вместе не бывает.
	VKToken string
}

const linkScheme = "csqtt://"

// maxLinkToken — длиннее токен VK панель сервера не примет.
const maxLinkToken = 4096

// minLinkHash — короче хеш звонка VK не бывает; более короткий в ссылке
// значит, что её обрезали при копировании.
const minLinkHash = 16

// ParseShareLink разбирает ссылку csqtt://.
func ParseShareLink(text string) (*ShareLink, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, errors.New("ссылка пуста")
	}
	lower := strings.ToLower(text)
	if !strings.HasPrefix(lower, linkScheme) {
		if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
			return nil, errors.New("это адрес в интернете, а не ссылка csqtt://")
		}
		return nil, errors.New("это не ссылка на сервер: она начинается с csqtt://")
	}
	// Ссылку вставляют скрытым вводом, и вставленная дважды подряд не видна.
	// Вторая копия приклеилась бы к последнему параметру — паролю или хешам.
	if strings.Count(lower, linkScheme) > 1 {
		return nil, errors.New("в тексте две ссылки подряд — похоже, ссылка вставлена дважды; вставьте её один раз")
	}
	rest := text[len(linkScheme):]
	if strings.Contains(rest, "#") {
		return nil, errors.New("ссылка повреждена: в ней «#», которого панель сервера не пишет; скопируйте её целиком ещё раз")
	}

	kind, query, isQuery := strings.Cut(rest, "?")
	if strings.EqualFold(kind, "connect") || strings.EqualFold(rest, "connect") {
		if !isQuery || query == "" {
			return nil, errors.New("в ссылке csqtt://connect нет параметров; скопируйте её целиком ещё раз")
		}
		return parseConnectLink(query)
	}
	if isSealedLink(rest) {
		return parseSealedLink(rest)
	}
	return parseLegacyLink(rest)
}

// linkKeys — параметры ссылки connect в том порядке, в каком их пишет панель.
var linkKeys = []string{"v", "host", "peer", "password", "hashes", "token"}

func parseConnectLink(query string) (*ShareLink, error) {
	// Ссылка, побывавшая в HTML, приходит с &amp; вместо &.
	query = strings.ReplaceAll(query, "&amp;", "&")
	var raw map[string]string
	var err error
	if strings.ContainsAny(query, "&;") {
		raw, err = splitQuery(query)
	} else {
		raw, err = splitGlued(query)
	}
	if err != nil {
		return nil, err
	}
	// Хеши раскодируются по одному: %2B внутри хеша — не разделитель.
	params := map[string]string{}
	for _, key := range []string{"v", "host", "peer", "password", "token"} {
		if params[key], err = linkUnescape(key, raw[key]); err != nil {
			return nil, err
		}
	}

	if _, ok := raw["v"]; !ok {
		return nil, errors.New("в ссылке нет версии формата (v=2); скопируйте её целиком ещё раз")
	} else if v := params["v"]; v != "2" {
		return nil, fmt.Errorf("ссылка формата v=%s, а csqtt-cli понимает только v=2", safeValue(v))
	}
	for _, key := range []string{"host", "peer"} {
		if params[key] == "" {
			return nil, fmt.Errorf("в ссылке пустой или пропущен параметр %s; скопируйте её целиком ещё раз", key)
		}
	}
	// Без пароля панель 2.5.0 ссылку выдаёт — тогда параметра нет вовсе.
	// Пустой параметр она не пишет: он значит, что ссылку испортили.
	for _, key := range []string{"password", "token"} {
		if _, ok := raw[key]; ok && params[key] == "" {
			return nil, fmt.Errorf("в ссылке пустой параметр %s; скопируйте её целиком ещё раз", key)
		}
	}

	host := params["host"]
	// Адрес IPv6 панель берёт из location.hostname — в квадратных скобках.
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = host[1 : len(host)-1]
	}
	link := &ShareLink{Password: params["password"]}
	if link.Peer, err = linkPeer(host, params["peer"]); err != nil {
		return nil, err
	}
	if err := checkLinkPassword(link.Password); err != nil {
		return nil, err
	}
	if hashes, ok := raw["hashes"]; ok {
		if link.Hashes, err = linkHashes(hashes); err != nil {
			return nil, err
		}
	}
	if link.VKToken = params["token"]; link.VKToken != "" {
		if len(link.Hashes) > 0 {
			// Панель даёт одно из двух; что из них главнее, не угадать.
			return nil, errors.New("в ссылке и хеши звонков, и токен VK — панель сервера так не пишет; скопируйте её целиком ещё раз")
		}
		if err := checkLinkToken(link.VKToken); err != nil {
			return nil, err
		}
	}
	return link, nil
}

// linkUnescape раскодирует значение параметра. «+» остаётся плюсом: панель
// кодирует encodeURIComponent, у которой пробел — %20.
func linkUnescape(key, value string) (string, error) {
	v, err := url.PathUnescape(value)
	if err != nil {
		return "", fmt.Errorf("ссылка повреждена: в параметре %s неверная %%-последовательность", key)
	}
	return v, nil
}

// splitQuery делит параметры по & и ;. Значения — как есть, не раскодированы.
// Неизвестные параметры пропускаются: панель может добавить новые, и ссылка
// от неё не должна ломаться. Повтор известного — ошибка: какое значение
// верное, не угадать.
func splitQuery(query string) (map[string]string, error) {
	params := map[string]string{}
	for part := range strings.FieldsFuncSeq(query, func(r rune) bool { return r == '&' || r == ';' }) {
		rawKey, rawValue, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		key, err := url.PathUnescape(rawKey)
		if err != nil {
			continue
		}
		key = strings.ToLower(key)
		if !isLinkKey(key) {
			continue
		}
		if _, dup := params[key]; dup {
			return nil, fmt.Errorf("в ссылке параметр %s повторяется; скопируйте её целиком ещё раз", key)
		}
		params[key] = rawValue
	}
	return params, nil
}

// splitGlued разбирает ссылку, у которой потерялись все & между параметрами
// (v=2host=…peer=…password=…): так её портят некоторые мессенджеры, и
// Android-клиент такую принимает. Без разделителей границы видны только по
// именам параметров, поэтому порядок — тот, в каком их пишет панель.
func splitGlued(query string) (map[string]string, error) {
	type span struct {
		key        string
		start, end int // значение — query[start:end]
	}
	var spans []span
	pos := 0
	for _, key := range linkKeys {
		marker := key + "="
		var i int
		if key == "hashes" || key == "token" {
			// Пароль идёт перед хешами и токеном и мог бы сам содержать
			// «hashes=»; последнее вхождение — граница вернее.
			i = strings.LastIndex(query[pos:], marker)
		} else {
			i = strings.Index(query[pos:], marker)
		}
		if i < 0 || key == "v" && i != 0 {
			continue
		}
		i += pos
		if n := len(spans); n > 0 {
			spans[n-1].end = i
		}
		pos = i + len(marker)
		spans = append(spans, span{key: key, start: pos, end: len(query)})
	}
	if len(spans) == 0 {
		return nil, errors.New("ссылка повреждена: параметры не разбираются; скопируйте её целиком ещё раз")
	}
	params := map[string]string{}
	for _, s := range spans {
		params[s.key] = query[s.start:s.end]
	}
	return params, nil
}

func isLinkKey(key string) bool {
	for _, k := range linkKeys {
		if key == k {
			return true
		}
	}
	return false
}

// parseLegacyLink — старый вид csqtt://<пароль>@<хост>:<порт>.
func parseLegacyLink(rest string) (*ShareLink, error) {
	rest = strings.TrimSuffix(rest, "/")
	at := strings.LastIndex(rest, "@")
	if at < 0 {
		return nil, errors.New("ссылка не разбирается: ожидалась ссылка из панели сервера CSQTT, csqtt://connect?… или csqtt://<пароль>@<хост>:<порт>")
	}
	if strings.ContainsAny(rest, "/?") {
		return nil, errors.New("ссылка повреждена: после адреса сервера лишнее; скопируйте её целиком ещё раз")
	}
	password, err := url.PathUnescape(rest[:at])
	if err != nil {
		return nil, errors.New("ссылка повреждена: в пароле неверная %-последовательность")
	}
	if password == "" {
		return nil, errors.New("в ссылке нет пароля перед «@»")
	}
	if err := checkLinkPassword(password); err != nil {
		return nil, err
	}
	host, port, err := net.SplitHostPort(rest[at+1:])
	if err != nil {
		return nil, errors.New("в ссылке адрес сервера не в виде <хост>:<порт>; IPv6 — в квадратных скобках")
	}
	peer, err := linkPeer(host, port)
	if err != nil {
		return nil, err
	}
	return &ShareLink{Peer: peer, Password: password}, nil
}

// linkPeer собирает host:port из хоста и порта ссылки.
func linkPeer(host, port string) (string, error) {
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 || strconv.Itoa(n) != port {
		return "", errors.New("в ссылке неверный порт сервера: нужно число от 1 до 65535")
	}
	if strings.Contains(host, ":") {
		if a, err := netip.ParseAddr(host); err != nil || !a.Is6() {
			return "", errors.New("в ссылке неверный адрес сервера: не IP-адрес и не имя хоста")
		}
	}
	peer := net.JoinHostPort(host, port)
	if err := CheckPeer(peer); err != nil {
		return "", errors.New("в ссылке неверный адрес сервера: не IP-адрес и не имя хоста")
	}
	return peer, nil
}

// checkLinkPassword: пробельных знаков в пароле панель не создаёт; пробел
// значит, что ссылку разорвали переносом строки.
func checkLinkPassword(password string) error {
	if strings.IndexFunc(password, unicode.IsSpace) >= 0 {
		return errors.New("в пароле из ссылки пробел или перевод строки — ссылка повреждена; скопируйте её целиком ещё раз")
	}
	return nil
}

// checkLinkToken — токен VK из ссылки идёт ядру строкой stdin. Пробелов и
// управляющих знаков панель в нём не допускает, длиннее maxLinkToken — тоже.
func checkLinkToken(token string) error {
	switch {
	case strings.IndexFunc(token, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0:
		return errors.New("в токене VK из ссылки пробел или перевод строки — ссылка повреждена; скопируйте её целиком ещё раз")
	case len(token) > maxLinkToken:
		return fmt.Errorf("токен VK в ссылке длиннее %d знаков — ссылка повреждена; скопируйте её целиком ещё раз", maxLinkToken)
	}
	return nil
}

// linkHashes — хеши из нераскодированного параметра hashes: через «+»,
// каждый раскодируется отдельно. Ссылка на звонок вместо хеша сводится к хешу, повторы
// выбрасываются.
func linkHashes(raw string) ([]string, error) {
	if raw == "" {
		return nil, errors.New("в ссылке пустой параметр hashes; скопируйте её целиком ещё раз")
	}
	var hashes []string
	seen := map[string]bool{}
	for part := range strings.SplitSeq(raw, "+") {
		decoded, err := linkUnescape("hashes", part)
		if err != nil {
			return nil, err
		}
		h := normalizeHash(decoded)
		switch {
		case h == "":
			return nil, errors.New("в ссылке пустой хеш между «+»; скопируйте её целиком ещё раз")
		case strings.IndexFunc(h, unicode.IsSpace) >= 0:
			return nil, errors.New("в хеше из ссылки пробел — ссылка повреждена; скопируйте её целиком ещё раз")
		case len(h) < minLinkHash:
			return nil, fmt.Errorf("в ссылке хеш короче %d знаков — похоже, ссылку обрезали; скопируйте её целиком ещё раз",
				minLinkHash)
		case seen[h]:
			continue
		}
		seen[h] = true
		hashes = append(hashes, h)
	}
	if len(hashes) > MaxHashes {
		return nil, fmt.Errorf("в ссылке %d хешей, а больше %d не бывает: ядро взяло бы только первые", len(hashes), MaxHashes)
	}
	return hashes, nil
}

// safeValue — значение параметра для сообщения: только начало.
func safeValue(v string) string {
	if r := []rune(v); len(r) > 8 {
		return string(r[:8]) + "…"
	}
	return v
}
