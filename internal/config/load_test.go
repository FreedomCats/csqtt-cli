package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Полный файл: все поля заданы, оба профиля разбираются, значения доходят
// до полей структуры без потерь.
const fullFile = `
default_profile = "home"

[profile.home]
peer           = "203.0.113.10:46000"
password_ref   = "dpapi:8f2a1c"
vk_hashes_ref  = "dpapi:1c9bd4"
device_id      = "3f9c2a71d04be658"
workers        = 30
obfs           = "video"
turn_transport = "tcp_tls"
datapath       = "socks"

[profile.home.socks]
listen = "127.0.0.1:1081"

[profile.work]
peer          = "[2001:db8::7]:46000"
password_ref  = "file:5d0e77"
vk_hashes_ref = "file:aa31b9"
datapath      = "tun"
`

// Минимальный файл: только обязательное. Всё остальное — умолчания.
const minimalFile = `
[profile.home]
peer          = "203.0.113.10"
password_ref  = "dpapi:8f2a1c"
vk_hashes_ref = "dpapi:1c9bd4"
`

func parse(t *testing.T, text string) *File {
	t.Helper()
	file, err := Parse([]byte(text), "тест.toml")
	if err != nil {
		t.Fatalf("разбор корректного файла не удался: %v", err)
	}
	return file
}

func TestParseFullFile(t *testing.T) {
	file := parse(t, fullFile)

	if file.DefaultProfile != "home" {
		t.Errorf("default_profile = %q, ожидалось %q", file.DefaultProfile, "home")
	}
	if got := file.Names(); len(got) != 2 || got[0] != "home" || got[1] != "work" {
		t.Fatalf("профили = %v, ожидались [home work]", got)
	}

	home := file.Profiles["home"]
	want := Profile{
		Name:          "home",
		Peer:          "203.0.113.10:46000",
		PasswordRef:   "dpapi:8f2a1c",
		VkHashesRef:   "dpapi:1c9bd4",
		DeviceID:      "3f9c2a71d04be658",
		Workers:       30,
		Obfs:          "video",
		TurnTransport: "tcp_tls",
		Datapath:      DatapathSocks,
		Socks:         SocksSection{Listen: "127.0.0.1:1081"},
	}
	if *home != want {
		t.Errorf("home = %+v,\nожидалось %+v", *home, want)
	}
	if work := file.Profiles["work"]; work.Peer != "[2001:db8::7]:46000" || work.Datapath != DatapathTun {
		t.Errorf("work = %+v", *work)
	}
}

// Пропущенные поля остаются пустыми: умолчания подставляют Core и ядро.
func TestParseMinimalFile(t *testing.T) {
	home := parse(t, minimalFile).Profiles["home"]
	want := Profile{Name: "home", Peer: "203.0.113.10", PasswordRef: "dpapi:8f2a1c", VkHashesRef: "dpapi:1c9bd4"}
	if *home != want {
		t.Errorf("пропущенные поля заполнены при разборе: %+v", *home)
	}
}

// Пример из репозитория попадает в релизный архив, и он же — первое, что
// увидит пользователь. Он обязан разбираться и проходить проверку.
func TestExampleFileParses(t *testing.T) {
	path := filepath.Join("..", "..", "csqtt-cli.example.toml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("не прочитан %s: %v", path, err)
	}

	file, err := Parse(data, path)
	if err != nil {
		t.Fatalf("пример не проходит разбор: %v", err)
	}
	profile, err := file.Profile("")
	if err != nil {
		t.Fatalf("в примере не выбирается профиль по умолчанию: %v", err)
	}
	if profile.Name != "home" {
		t.Errorf("профиль по умолчанию = %q, ожидался home", profile.Name)
	}
}

// Секция routing разбирается; неизвестный ключ в ней — ошибка, как везде:
// правила сервера и свои списки здесь пока не задаются.
func TestParseRoutingSection(t *testing.T) {
	on := parse(t, minimalFile+"\n[profile.home.routing]\ntemplate = true\n").Profiles["home"]
	if !on.RoutingTemplateOn() {
		t.Errorf("template = true: %v", on.Routing.Template)
	}
	if off := parse(t, minimalFile).Profiles["home"]; off.RoutingTemplateOn() || off.Routing.Template != nil {
		t.Errorf("без секции: %v", off.Routing.Template)
	}
	if empty := parse(t, minimalFile+"\n[profile.home.routing]\n").Profiles["home"]; empty.RoutingTemplateOn() {
		t.Error("пустая секция включила шаблон")
	}
	for _, tc := range []struct{ text, want string }{
		{"\n[profile.home.routing]\nserver = \"apply\"\n", "неизвестный ключ profile.home.routing.server"},
		{"\n[profile.home.routing]\ntemplate = \"yes\"\n", "template"},
	} {
		_, err := Parse([]byte(minimalFile+tc.text), "тест.toml")
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%q: ошибка %v, ждали %q", tc.text, err, tc.want)
		}
	}
}

func TestLoadReadsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	if err := os.WriteFile(path, []byte(minimalFile), 0o600); err != nil {
		t.Fatal(err)
	}

	file, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, err := file.Profile("home"); err != nil {
		t.Errorf("профиль home не найден: %v", err)
	}
}

// «Настроек ещё нет» отличается от «настройки испорчены» через errors.Is,
// а не разбором текста ошибки: первое — обычное состояние до `csqtt-cli profile set`.
func TestLoadMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)

	_, err := Load(path)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ошибка = %v, ожидалась обёртка вокруг os.ErrNotExist", err)
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("в сообщении нет пути к файлу: %v", err)
	}
}

// Ошибка синтаксиса показывает место: у TOML есть готовое сообщение
// с номером строки и указателем, и оно куда полезнее нашего пересказа.
func TestParseSyntaxError(t *testing.T) {
	_, err := Parse([]byte("[profile.home\npeer = \"x:1\"\n"), "тест.toml")
	if err == nil {
		t.Fatal("испорченный TOML разобран без ошибки")
	}
	if !strings.Contains(err.Error(), "тест.toml") {
		t.Errorf("в сообщении нет источника: %v", err)
	}
	if !strings.Contains(err.Error(), "1") {
		t.Errorf("в сообщении нет номера строки: %v", err)
	}
}

// Опечатка в имени ключа означает, что настройка не применилась. Молчать
// об этом нельзя: узнать было бы можно только по странному поведению.
func TestParseUnknownKey(t *testing.T) {
	for _, tt := range []struct{ extra, key string }{
		{"workerz = 18\n", "profile.home.workerz"},
		{"[profile.home.socks]\nlisten = \"127.0.0.1:1080\"\nuser = \"u\"\n", "profile.home.socks.user"},
	} {
		_, err := Parse([]byte(minimalFile+tt.extra), "тест.toml")
		if err == nil {
			t.Errorf("неизвестный ключ %s пропущен", tt.key)
		} else if !strings.Contains(err.Error(), tt.key) {
			t.Errorf("в сообщении нет самого ключа %s: %v", tt.key, err)
		}
	}
}

// Секрет, положенный прямо в файл, — не просто неизвестный ключ: человек
// хотел задать пароль, и ему нужно сказать, куда тот кладётся на самом деле.
func TestParseSecretInFile(t *testing.T) {
	for _, key := range []string{"password", "vk_hashes", "hashes"} {
		text := minimalFile + key + " = \"хорошийсекрет\"\n"
		_, err := Parse([]byte(text), "тест.toml")
		if err == nil {
			t.Errorf("ключ %s пропущен", key)
			continue
		}
		if !strings.Contains(err.Error(), "csqtt-cli profile set") {
			t.Errorf("%s: в сообщении нет подсказки про хранилище: %v", key, err)
		}
		if strings.Contains(err.Error(), "хорошийсекрет") {
			t.Errorf("%s: сообщение печатает сам секрет: %v", key, err)
		}
	}
}

// Значение не того типа ловит библиотека; наше дело — назвать источник.
func TestParseWrongType(t *testing.T) {
	_, err := Parse([]byte("[profile.home]\nworkers = \"восемнадцать\"\n"), "тест.toml")
	if err == nil {
		t.Fatal("строка в числовом поле принята")
	}
	if !strings.Contains(err.Error(), "тест.toml") {
		t.Errorf("в сообщении нет источника: %v", err)
	}
}

// Профиль от csqtt-cli 0.3.0 с прежним числом воркеров (кратным 9) не
// переводится молча: ошибка называет файл, объясняет, почему число не
// годится, и что сделать. Число, которого не было и раньше, — без отсылки к
// прежним версиям.
func TestParseOldWorkersExplained(t *testing.T) {
	const head = "[profile.home]\npeer = \"203.0.113.10\"\npassword_ref = \"dpapi:8f2a1c\"\nvk_hashes_ref = \"dpapi:1c9bd4\"\n"
	_, err := Parse([]byte(head+"workers = 18\n"), "тест.toml")
	if err == nil {
		t.Fatal("профиль с workers = 18 принят")
	}
	for _, want := range []string{"тест.toml", "workers = 18", "группы по 10", "кратные 9", "csqtt-cli 0.3.0", "2.1.9", "удалите ключ", "умолчание 10"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("в сообщении нет %q:\n%v", want, err)
		}
	}

	_, err = Parse([]byte(head+"workers = 25\n"), "тест.toml")
	if err == nil || !strings.Contains(err.Error(), "удалите ключ") || strings.Contains(err.Error(), "0.3.0") {
		t.Errorf("workers = 25 — без отсылки к прежним версиям, с подсказкой: %v", err)
	}
}

// Ошибки проверки собираются все разом и с именем профиля.
func TestParseReportsAllProblems(t *testing.T) {
	text := `
[profile.home]
peer          = "203.0.113.10:0"
password_ref  = "dpapi:8f2a1c"
vk_hashes_ref = "dpapi:1c9bd4"
workers       = 25
obfs          = "noise"
`
	_, err := Parse([]byte(text), "тест.toml")
	if err == nil {
		t.Fatal("негодный профиль разобран")
	}
	for _, want := range []string{`профиль "home"`, "peer", "workers = 25", `obfs = "noise"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("в сообщении нет %q:\n%v", want, err)
		}
	}
}
