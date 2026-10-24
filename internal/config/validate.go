package config

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
)

// Пределы воркеров — те же, что у ядра (coreproc.NormalizeWorkers): ядро
// молча округлило бы негодное число, а человек должен знать, сколько
// воркеров запустится на самом деле. minWorkers — особый случай ядра: пять
// воркеров без целой группы.
const (
	minWorkers      = 5
	workersPerGroup = 10
	maxWorkers      = 100
)

// maxDeviceID — предел длины device_id. Ядро до 2.5.0 слало его серверу в
// служебных строках через «:» и «|», поэтому и набор знаков закрыт
// (deviceIDProblem); ядро 2.5.0 его не передаёт, поле остаётся в профиле.
const maxDeviceID = 64

// Validate проверяет файл целиком. Вызывается при разборе; отдельно нужен тем,
// кто собрал File в памяти — например, `csqtt-cli profile set` перед записью.
func (f *File) Validate() error {
	problems := validateFile(f)
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("настройки:\n  %s", strings.Join(problems, "\n  "))
}

// Validate проверяет один профиль — тот же набор правил, что и у файла.
// Нужен `csqtt-cli connect`: флаги командной строки перекрывают поля профиля,
// и собранное из двух источников обязано пройти ту же проверку, что и
// записанное в файл.
func (p *Profile) Validate() error {
	problems := profileProblems(p)
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("настройки профиля %q:\n  %s", p.Name, strings.Join(problems, "\n  "))
}

// validateFile собирает все претензии разом, а не первую попавшуюся: человек
// правит файл в редакторе, и показать ему весь список дешевле, чем гонять по
// кругу «исправил — запустил — снова ошибка».
func validateFile(f *File) []string {
	var problems []string

	names := f.Names()
	for _, name := range names {
		if bad := profileNameProblem(name); bad != "" {
			problems = append(problems, bad)
		}
	}

	if def := strings.TrimSpace(f.DefaultProfile); def != "" {
		if _, ok := f.Profiles[def]; !ok {
			if len(names) == 0 {
				problems = append(problems, fmt.Sprintf(
					"default_profile = %q: такого профиля нет, в файле нет ни одного профиля", def))
			} else {
				problems = append(problems, fmt.Sprintf(
					"default_profile = %q: такого профиля нет; в файле есть: %s", def, strings.Join(names, ", ")))
			}
		}
	}

	for _, name := range names {
		for _, problem := range profileProblems(f.Profiles[name]) {
			problems = append(problems, fmt.Sprintf("профиль %q: %s", name, problem))
		}
	}

	problems = append(problems, vkProblems(f.VK)...)
	return problems
}

// vkProblems проверяет секцию [vk]. Её пишет `csqtt-cli vk login`; пустая
// секция — входа нет.
func vkProblems(v VKSection) []string {
	var problems []string
	if ref := strings.TrimSpace(v.TokenRef); ref != "" {
		scheme, id, found := strings.Cut(ref, ":")
		if !found || strings.TrimSpace(scheme) == "" || strings.TrimSpace(id) == "" {
			// Значение не печатается: раз это не ссылка, это, скорее всего, сам токен.
			problems = append(problems, "[vk] token_ref: ожидается ссылка вида <хранилище>:<идентификатор>; "+
				"сам токен VK в файле не хранится — войдите заново: `csqtt-cli vk login`")
		}
	}
	if id := strings.TrimSpace(v.UserID); id != "" && !isDigits(id, 20) {
		// Значение не печатается: что бы туда ни попало, id это не похоже.
		problems = append(problems, "[vk] user_id: id аккаунта VK — только цифры")
	}
	return problems
}

// isDigits — непустая строка из одних цифр, не длиннее limit.
func isDigits(s string, limit int) bool {
	if s == "" || len(s) > limit {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// CheckProfileName проверяет имя профиля по тем же правилам, что и файл. Нужна
// тем, кто спрашивает имя у человека до записи.
func CheckProfileName(name string) error {
	if bad := profileNameProblem(name); bad != "" {
		return errors.New(bad)
	}
	return nil
}

// CheckPeer проверяет адрес сервера так же, как поле peer профиля.
func CheckPeer(peer string) error {
	if bad := peerProblem("адрес сервера", peer); bad != "" {
		return errors.New(bad)
	}
	return nil
}

// profileNameProblem бережёт имя профиля от того, что тяжело набрать в
// командной строке: пробелов, кавычек, кириллицы в неожиданной раскладке. Имя —
// это аргумент `csqtt-cli connect <имя>`, а не описание.
func profileNameProblem(name string) string {
	if name == "" {
		return "пустое имя профиля: секция называется [profile.<имя>]"
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return fmt.Sprintf(
				"имя профиля %q: допустимы латинские буквы, цифры, дефис и подчёркивание", name)
		}
	}
	return ""
}

func profileProblems(p *Profile) []string {
	var problems []string
	add := func(problem string) {
		if problem != "" {
			problems = append(problems, problem)
		}
	}

	if strings.TrimSpace(p.Peer) == "" {
		add("не задан peer: адрес сервера CSQTT, например 203.0.113.10:" + DefaultPeerPort)
	} else {
		add(peerProblem("peer", p.Peer))
	}
	add(secretRefProblem("password_ref", p.PasswordRef, "пароль подключения"))
	// В auto_js хеши не нужны, но и не мешают: ядро их не получит, а
	// профиль, переключённый обратно, найдёт их на месте. Форма ссылки
	// проверяется и тут — на месте ссылки может оказаться сам секрет.
	if !p.AutoJS() || strings.TrimSpace(p.VkHashesRef) != "" {
		add(secretRefProblem("vk_hashes_ref", p.VkHashesRef, "хеши звонков VK"))
	}
	add(oneOfProblem("vk_hash_mode", p.VkHashMode, VKHashModeManual, VKHashModeAutoJS))
	add(deviceIDProblem(p.DeviceID))
	add(workersProblem(p.Workers))
	add(oneOfProblem("obfs", p.Obfs, "audio", "video", "unified"))
	add(oneOfProblem("turn_transport", p.TurnTransport, "udp", "tcp_tls"))
	add(oneOfProblem("datapath", p.Datapath, DatapathTun, DatapathSocks))
	if strings.TrimSpace(p.Socks.Listen) != "" {
		add(socksListenProblem(p.Socks.Listen))
	}

	return problems
}

// peerProblem проверяет адрес сервера: host или host:port, порт по умолчанию
// дописывает PeerWithPort. host — IP или имя: имя ядро разрешит само.
func peerProblem(key, value string) string {
	value = strings.TrimSpace(value)
	if a, err := netip.ParseAddr(value); err == nil && a.Is6() {
		return fmt.Sprintf("%s = %q: IPv6 пишется в квадратных скобках, например [%s]:%s",
			key, value, value, DefaultPeerPort)
	}

	host, port, err := net.SplitHostPort(PeerWithPort(value))
	if err != nil {
		return fmt.Sprintf("%s = %q: адрес должен быть в виде host:port, например 203.0.113.10:%s",
			key, value, DefaultPeerPort)
	}
	if bad := portProblem(key, value, port); bad != "" {
		return bad
	}
	switch {
	case host == "":
		return fmt.Sprintf("%s = %q: не задан адрес до двоеточия", key, value)
	case strings.Contains(host, ":") || strings.HasPrefix(value, "["):
		if a, err := netip.ParseAddr(host); err != nil || !a.Is6() {
			return fmt.Sprintf("%s = %q: %q в скобках — не IPv6-адрес", key, value, host)
		}
	case !isHostname(host):
		return fmt.Sprintf("%s = %q: %q — не IP-адрес и не имя хоста", key, value, host)
	}
	return ""
}

// isHostname — имя из меток латиницы, цифр, дефиса и подчёркивания через
// точку. IPv4 под это правило тоже подходит.
func isHostname(host string) bool {
	if len(host) > 253 {
		return false
	}
	for label := range strings.SplitSeq(strings.TrimSuffix(host, "."), ".") {
		if label == "" || len(label) > 63 {
			return false
		}
		for _, r := range label {
			switch {
			case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			default:
				return false
			}
		}
	}
	return true
}

func portProblem(key, value, port string) string {
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return fmt.Sprintf("%s = %q: порт %q должен быть числом от 1 до 65535", key, value, port)
	}
	return ""
}

// socksListenProblem — то же правило, что у datapath.NewSocks: IP:порт на
// loopback. Проверка здесь — чтобы ошибка нашлась при записи профиля, а не
// при подключении.
func socksListenProblem(value string) string {
	value = strings.TrimSpace(value)
	addr, err := netip.ParseAddrPort(value)
	if err != nil {
		return fmt.Sprintf("socks.listen = %q: нужен IP:порт, например 127.0.0.1:1080", value)
	}
	if addr.Port() == 0 {
		return fmt.Sprintf("socks.listen = %q: порт должен быть числом от 1 до 65535", value)
	}
	if !addr.Addr().IsLoopback() {
		return fmt.Sprintf("socks.listen = %q: только loopback (127.0.0.1, [::1]) — "+
			"у SOCKS5 нет пароля, и на другом адресе туннель открылся бы всей сети", value)
	}
	return ""
}

// secretRefProblem проверяет ссылку на секрет, не заглядывая в хранилище:
// важна форма <хранилище>:<идентификатор>. Она и ловит главную ошибку —
// пароль, написанный прямо в файле, ссылкой не выглядит.
func secretRefProblem(key, ref, what string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return fmt.Sprintf(
			"не задан %s: ссылка на %s; секреты в файле не хранятся, ссылку записывает `csqtt-cli profile set`", key, what)
	}
	scheme, id, found := strings.Cut(ref, ":")
	if !found || strings.TrimSpace(scheme) == "" || strings.TrimSpace(id) == "" {
		// Значение не печатается: раз это не ссылка, это, скорее всего, сам секрет.
		return fmt.Sprintf(
			"%s: ожидается ссылка вида <хранилище>:<идентификатор>, например dpapi:8f2a1c; "+
				"сам %s в файле не хранится — запишите его через `csqtt-cli profile set`", key, what)
	}
	return ""
}

func deviceIDProblem(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return "" // заведёт Save
	}
	if len(id) > maxDeviceID {
		return fmt.Sprintf("device_id длиннее %d знаков", maxDeviceID)
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
		default:
			return fmt.Sprintf("device_id = %q: допустимы латинские буквы, цифры, точка, дефис и подчёркивание", id)
		}
	}
	return ""
}

// Прежние пределы воркеров — ядра 2.1.9 и csqtt-cli 0.3.0: группы по 9, до
// 126. Профиль с таким числом не переводится молча (решение пользователя),
// но ошибка объясняет, откуда оно и что с ним делать.
const (
	oldWorkersPerGroup = 9
	oldMaxWorkers      = 126
)

func workersProblem(n int) string {
	if n == 0 || n == minWorkers || n >= workersPerGroup && n <= maxWorkers && n%workersPerGroup == 0 {
		return ""
	}
	problem := fmt.Sprintf("workers = %d: ядро 2.5.0 делит воркеров на группы по %d — допустимы %d, %d, %d, %d … %d",
		n, workersPerGroup, minWorkers, workersPerGroup, 2*workersPerGroup, 3*workersPerGroup, maxWorkers)
	if n > 0 && n <= oldMaxWorkers && n%oldWorkersPerGroup == 0 {
		problem += fmt.Sprintf("; прежние значения (кратные %d) из csqtt-cli 0.3.0 и ядра 2.1.9 больше не подходят",
			oldWorkersPerGroup)
	}
	return problem + fmt.Sprintf(". Исправьте workers в файле профиля или удалите ключ — тогда будет умолчание %d",
		DefaultWorkers)
}

func oneOfProblem(key, value string, allowed ...string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	for _, ok := range allowed {
		if value == ok {
			return ""
		}
	}
	return fmt.Sprintf("%s = %q: допустимы %s", key, value, quotedList(allowed))
}

func quotedList(values []string) string {
	quoted := make([]string, len(values))
	for i, value := range values {
		quoted[i] = strconv.Quote(value)
	}
	return strings.Join(quoted, ", ")
}
