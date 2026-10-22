package config

import (
	"fmt"
	"net"
	"sort"
	"strings"

	"github.com/FreedomCats/csqtt-cli/internal/coreproc"
)

// Способы отдать трафик наружу (§05 doc/architecture.md).
const (
	// DatapathTun — свой адаптер, весь трафик системы. Нужны права
	// администратора (root).
	DatapathTun = "tun"
	// DatapathSocks — netstack в процессе и локальный SOCKS5. Прав не требует,
	// но через туннель ходят только программы, настроенные на прокси.
	DatapathSocks = "socks"
)

// DefaultDatapath — режим профиля без поля datapath: клиенту нужен туннель
// для всего, а не прокси, который ещё надо прописывать в программах.
const DefaultDatapath = DatapathTun

// EffectiveDatapath — каким режимом профиль подключится: значение поля, а
// пустое — DefaultDatapath. Умолчание читается только здесь, чтобы выбор
// реализации, проверка прав и показ профиля не разошлись.
func EffectiveDatapath(value string) string {
	if value = strings.TrimSpace(value); value == "" {
		return DefaultDatapath
	}
	return value
}

// DefaultWorkers — воркеров, если в профиле не сказано: умолчание ядра (-n).
const DefaultWorkers = 18

// File — разобранный profiles.toml целиком.
type File struct {
	// DefaultProfile — какой профиль брать, если в командной строке имя
	// не названо. Может быть пустым: когда профиль в файле один, выбирать
	// не из чего.
	DefaultProfile string `toml:"default_profile"`
	// Profiles — секции [profile.<имя>].
	Profiles map[string]*Profile `toml:"profile"`
	// VK — секция [vk]: вход в аккаунт VK для режима auto_js. Один на файл
	// настроек, а не на профиль; пишет её `csqtt-cli vk login`.
	VK VKSection `toml:"vk"`
}

// VKTokenSecret — имя access token VK в хранилище секретов. С точкой, как
// <профиль>.password: имя профиля точки не содержит, и записи не спутать.
const VKTokenSecret = "vk.token"

// VKSection — секция [vk] (§08а, §10 doc/architecture.md). Самого токена
// здесь нет, только ссылка на него в хранилище секретов — как у пароля
// профиля.
type VKSection struct {
	// TokenRef — ссылка на access token VK в хранилище секретов.
	TokenRef string `toml:"token_ref"`
	// UserID — id аккаунта VK, которому выдан токен. Не секрет: его
	// показывает `csqtt-cli vk status`.
	UserID string `toml:"user_id"`
}

// LoggedIn — вход VK выполнен: ссылка на токен есть.
func (v VKSection) LoggedIn() bool { return strings.TrimSpace(v.TokenRef) != "" }

// Значения vk_hash_mode — те же слова, что у флага -vk-hash-mode ядра.
const (
	// VKHashModeManual — хеши звонков из профиля; то же, что пустое поле.
	VKHashModeManual = "manual"
	// VKHashModeAutoJS — звонок создаёт ядро по токену аккаунта VK.
	VKHashModeAutoJS = coreproc.VKHashModeAutoJS
)

// Profile — одна секция [profile.<имя>] (§10 doc/architecture.md).
//
// Пустое поле означает «не задано»: умолчание подставляет Core или ядро.
type Profile struct {
	// Name — имя секции. Заполняется при разборе, в файле его нет.
	Name string `toml:"-"`

	// Peer — сервер CSQTT: host:port или host, тогда порт — DefaultPeerPort.
	Peer string `toml:"peer"`
	// PasswordRef — ссылка на пароль подключения в хранилище секретов. Из
	// пароля ядро выводит и WRAP-ключ маскировки.
	PasswordRef string `toml:"password_ref"`
	// VkHashesRef — ссылка на хеши звонков VK. В auto_js не нужна.
	VkHashesRef string `toml:"vk_hashes_ref"`
	// VkHashMode — откуда звонок VK: пусто или VKHashModeManual — хеши
	// профиля; VKHashModeAutoJS — звонок создаёт ядро по входу VK
	// (`csqtt-cli vk login`, §08а), хеши не нужны. Читать — AutoJS.
	VkHashMode string `toml:"vk_hash_mode"`
	// DeviceID — идентификатор устройства для сервера. Save заводит его при
	// первой записи профиля и потом не трогает: сервер может закрепить
	// главный пароль за первым подключившимся устройством, и новый id
	// выглядел бы как чужое устройство.
	DeviceID string `toml:"device_id"`

	// Workers — число воркеров: кратно 9, от 9 до 126. Не больше 27 на хеш,
	// а в auto_js — не больше 72: это урезает coreproc.Config.NormalizedWorkers.
	Workers int `toml:"workers"`
	// Obfs — маскировка RTP: audio или video.
	Obfs string `toml:"obfs"`
	// TurnTransport — до TURN по udp или tcp_tls. TCP помогает в сетях, где
	// душат UDP.
	TurnTransport string `toml:"turn_transport"`
	// Datapath — как отдавать трафик: tun или socks.
	Datapath string `toml:"datapath"`
	// KillSwitch — блокировать ли трафик системы, пока туннель не поднят
	// (datapath tun). Указатель: пропущенное поле значит «включён», выключает
	// только явное false. Читать — KillSwitchOn.
	KillSwitch *bool `toml:"kill_switch"`

	// Socks — секция [profile.<имя>.socks].
	Socks SocksSection `toml:"socks"`
}

// SocksSection — настройки локального SOCKS5. Учитываются при datapath = "socks".
//
// Пароля к SOCKS5 нет, поэтому слушать он может только loopback: иначе
// туннель открылся бы всей локальной сети.
type SocksSection struct {
	Listen string `toml:"listen"`
}

// DefaultPeerPort — порт сервера CSQTT, если в адресе его не назвали: тот же,
// что по умолчанию у сервера и в ссылках его панели.
const DefaultPeerPort = "46000"

// PeerWithPort дописывает к адресу без порта DefaultPeerPort. Голый IPv6 без
// скобок остаётся как был: где в нём кончается адрес, не угадать, и проверка
// потом попросит скобки.
func PeerWithPort(peer string) string {
	peer = strings.TrimSpace(peer)
	switch {
	case peer == "":
		return peer
	case strings.HasPrefix(peer, "[") && strings.HasSuffix(peer, "]"):
		return peer + ":" + DefaultPeerPort
	case strings.Contains(peer, ":"):
		return peer
	}
	return net.JoinHostPort(peer, DefaultPeerPort)
}

// Core переводит профиль в параметры запуска ядра.
//
// Секреты не подставляются: Password, Hashes и VKToken остаются пустыми, их
// заполняет вызывающий, развернув PasswordRef, VkHashesRef и токен входа VK
// ([vk] token_ref) через хранилище. Адреса канала пакетов (Listen,
// ListenPeer, TunUDS) — дело datapath.
func (p *Profile) Core() coreproc.Config {
	cfg := coreproc.Config{
		Peer:          PeerWithPort(p.Peer),
		Workers:       p.Workers,
		Obfs:          strings.TrimSpace(p.Obfs),
		TurnTransport: strings.TrimSpace(p.TurnTransport),
		DeviceID:      strings.TrimSpace(p.DeviceID),
	}
	if p.AutoJS() {
		cfg.VKHashMode = coreproc.VKHashModeAutoJS
	}
	if cfg.Workers == 0 {
		cfg.Workers = DefaultWorkers
	}
	return cfg
}

// AutoJS — звонок VK создаёт ядро (vk_hash_mode = "auto_js"), хеши профиля
// не нужны.
func (p *Profile) AutoJS() bool {
	return strings.TrimSpace(p.VkHashMode) == VKHashModeAutoJS
}

// KillSwitchOn — включён ли kill-switch: да, если в профиле не сказано
// обратного.
func (p *Profile) KillSwitchOn() bool {
	return p.KillSwitch == nil || *p.KillSwitch
}

// Names возвращает имена профилей по алфавиту — для выбора и для сообщений
// об ошибках, где порядок обязан быть устойчивым.
func (f *File) Names() []string {
	names := make([]string, 0, len(f.Profiles))
	for name := range f.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Profile возвращает профиль по имени. Пустое имя означает «профиль
// по умолчанию»: сначала default_profile, а если его нет — единственный
// профиль в файле.
//
// Сообщения об ошибках перечисляют, что в файле есть: чаще всего промах
// с именем — это опечатка, и список избавляет от похода в файл.
func (f *File) Profile(name string) (*Profile, error) {
	name = strings.TrimSpace(name)

	if name == "" {
		switch {
		case f.DefaultProfile != "":
			name = f.DefaultProfile
		case len(f.Profiles) == 1:
			name = f.Names()[0]
		case len(f.Profiles) == 0:
			return nil, fmt.Errorf("в файле настроек нет ни одного профиля")
		default:
			return nil, fmt.Errorf(
				"не выбран профиль: в файле их %d (%s), а default_profile не задан",
				len(f.Profiles), strings.Join(f.Names(), ", "))
		}
	}

	profile, ok := f.Profiles[name]
	if !ok {
		if len(f.Profiles) == 0 {
			return nil, fmt.Errorf("профиля %q нет: в файле настроек нет ни одного профиля", name)
		}
		return nil, fmt.Errorf("профиля %q нет; в файле есть: %s", name, strings.Join(f.Names(), ", "))
	}
	return profile, nil
}
