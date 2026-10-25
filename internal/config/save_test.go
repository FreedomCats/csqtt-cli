package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func sampleFile() *File {
	return &File{
		DefaultProfile: "home",
		Profiles: map[string]*Profile{
			"home": {
				Name:          "home",
				Peer:          "203.0.113.10:46000",
				PasswordRef:   "dpapi:8f2a1c",
				VkHashesRef:   "dpapi:1c9bd4",
				DeviceID:      "3f9c2a71d04be658",
				Workers:       30,
				Obfs:          "video",
				TurnTransport: "tcp_tls",
				Datapath:      DatapathSocks,
				KillSwitch:    boolPtr(false),
				Socks:         SocksSection{Listen: "127.0.0.1:1081"},
				Routing:       RoutingSection{Template: boolPtr(true), Server: boolPtr(false)},
			},
			"work": {
				Name:        "work",
				Peer:        "198.51.100.7",
				PasswordRef: "file:5d0e77",
				VkHashesRef: "file:aa31b9",
				DeviceID:    "0c1d2e3f40516273",
			},
		},
	}
}

// Записанный файл читается обратно тем же разбором и даёт то же самое.
// Это и есть главное требование к Save: писать можно только то, что мы
// сами согласны прочитать.
func TestSaveRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	if err := Save(path, sampleFile()); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.DefaultProfile != "home" {
		t.Errorf("default_profile = %q", got.DefaultProfile)
	}
	want := sampleFile()
	if len(got.Profiles) != len(want.Profiles) {
		t.Fatalf("профилей %d, ожидалось %d: %v", len(got.Profiles), len(want.Profiles), got.Names())
	}
	for name, p := range want.Profiles {
		if !sameProfile(got.Profiles[name], p) {
			t.Errorf("%s после записи и чтения = %+v,\nожидалось %+v", name, *got.Profiles[name], *p)
		}
	}
}

// sameProfile сравнивает профили; kill_switch — по значению, а не по
// указателю.
func sameProfile(a, b *Profile) bool {
	if (a.KillSwitch == nil) != (b.KillSwitch == nil) ||
		a.KillSwitch != nil && *a.KillSwitch != *b.KillSwitch {
		return false
	}
	if (a.Routing.Template == nil) != (b.Routing.Template == nil) ||
		a.Routing.Template != nil && *a.Routing.Template != *b.Routing.Template {
		return false
	}
	x, y := *a, *b
	x.KillSwitch, y.KillSwitch = nil, nil
	if (a.Routing.Server == nil) != (b.Routing.Server == nil) ||
		a.Routing.Server != nil && *a.Routing.Server != *b.Routing.Server {
		return false
	}
	x.Routing.Template, y.Routing.Template = nil, nil
	x.Routing.Server, y.Routing.Server = nil, nil
	return x == y
}

func boolPtr(v bool) *bool { return &v }

// Профиль auto_js без хешей записывается и читается обратно; режим — после
// ссылки на хеши, как в csqtt-cli.example.toml.
func TestSaveAutoJS(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	want := &Profile{Name: "home", Peer: "203.0.113.10", PasswordRef: "dpapi:8f2a1c",
		VkHashMode: VKHashModeAutoJS, DeviceID: "3f9c2a71d04be658"}
	if err := Save(path, &File{Profiles: map[string]*Profile{"home": want}}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	text, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(text), "password_ref = \"dpapi:8f2a1c\"\nvk_hash_mode = \"auto_js\"\n") ||
		strings.Contains(string(text), "vk_hashes_ref") {
		t.Errorf("файл:\n%s", text)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !sameProfile(got.Profiles["home"], want) {
		t.Errorf("после записи и чтения = %+v,\nожидалось %+v", *got.Profiles["home"], *want)
	}
}

// Токен VK от сервера: ссылка на него пишется после режима и читается обратно.
func TestSaveServerVKToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	want := &Profile{Name: "home", Peer: "203.0.113.10", PasswordRef: "dpapi:8f2a1c",
		VkHashMode: VKHashModeAutoJS, VkTokenRef: "dpapi:7e1f", DeviceID: "3f9c2a71d04be658"}
	if err := Save(path, &File{Profiles: map[string]*Profile{"home": want}}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	text, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(text), "vk_hash_mode = \"auto_js\"\nvk_token_ref = \"dpapi:7e1f\"\n") {
		t.Errorf("файл:\n%s", text)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !sameProfile(got.Profiles["home"], want) || !got.Profiles["home"].ServerVKToken() {
		t.Errorf("после записи и чтения = %+v,\nожидалось %+v", *got.Profiles["home"], *want)
	}
}

// kill_switch пишется, только если задан, и тем значением, что задано:
// пропуск — «включён» (KillSwitchOn).
func TestSaveKillSwitch(t *testing.T) {
	for _, tc := range []struct {
		value *bool
		line  string
		on    bool
	}{
		{nil, "", true},
		{boolPtr(true), "kill_switch = true\n", true},
		{boolPtr(false), "kill_switch = false\n", false},
	} {
		path := filepath.Join(t.TempDir(), FileName)
		f := &File{Profiles: map[string]*Profile{"home": {
			Name: "home", Peer: "203.0.113.10", PasswordRef: "dpapi:8f2a1c", VkHashesRef: "dpapi:1c9bd4",
			KillSwitch: tc.value,
		}}}
		if err := Save(path, f); err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if has := strings.Contains(string(raw), "kill_switch"); has != (tc.line != "") ||
			tc.line != "" && !strings.Contains(string(raw), tc.line) {
			t.Errorf("kill_switch %v: в файле\n%s", tc.value, raw)
		}
		got, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		if p := got.Profiles["home"]; p.KillSwitchOn() != tc.on || (p.KillSwitch == nil) != (tc.value == nil) {
			t.Errorf("kill_switch %v: после чтения KillSwitch %v, KillSwitchOn %v", tc.value, p.KillSwitch, p.KillSwitchOn())
		}
	}
}

// Пустые поля не записываются: пропуск означает «как по умолчанию», и вписать
// вместо него нынешнее умолчание значило бы заморозить его в чужом файле.
// Секция routing пишется, только если template задан, и тем значением, что
// задано: пропуск — «выключено» (RoutingTemplateOn). Старый файл без секции
// после чтения и записи её не получает.
func TestSaveRoutingTemplate(t *testing.T) {
	for _, tc := range []struct {
		value *bool
		text  string
		on    bool
	}{
		{nil, "", false},
		{boolPtr(true), "\n[profile.home.routing]\ntemplate = true\n", true},
		{boolPtr(false), "\n[profile.home.routing]\ntemplate = false\n", false},
	} {
		path := filepath.Join(t.TempDir(), FileName)
		f := &File{Profiles: map[string]*Profile{"home": {
			Name: "home", Peer: "203.0.113.10", PasswordRef: "dpapi:8f2a1c", VkHashesRef: "dpapi:1c9bd4",
			Routing: RoutingSection{Template: tc.value},
		}}}
		if err := Save(path, f); err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if has := strings.Contains(string(raw), "routing"); has != (tc.text != "") ||
			tc.text != "" && !strings.HasSuffix(string(raw), tc.text) {
			t.Errorf("template %v: в файле\n%s", tc.value, raw)
		}
		got, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		p := got.Profiles["home"]
		if p.RoutingTemplateOn() != tc.on || (p.Routing.Template == nil) != (tc.value == nil) {
			t.Errorf("template %v: после чтения %v, RoutingTemplateOn %v", tc.value, p.Routing.Template, p.RoutingTemplateOn())
		}
		if p.Core().TemplateDomains != tc.on {
			t.Errorf("template %v: Core().TemplateDomains = %v", tc.value, p.Core().TemplateDomains)
		}
	}

	// Старый файл, без секции: перезапись её не добавляет.
	path := filepath.Join(t.TempDir(), FileName)
	if err := os.WriteFile(path, []byte(minimalFile), 0o600); err != nil {
		t.Fatal(err)
	}
	old, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := Save(path, old); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(path); strings.Contains(string(raw), "routing") {
		t.Errorf("старый файл получил секцию routing:\n%s", raw)
	}
}

// server — так же: пишется, только если задан, и может стоять без template.
func TestSaveRoutingServer(t *testing.T) {
	for _, tc := range []struct {
		template, server *bool
		text             string
	}{
		{nil, boolPtr(true), "\n[profile.home.routing]\nserver = true\n"},
		{nil, boolPtr(false), "\n[profile.home.routing]\nserver = false\n"},
		{boolPtr(true), boolPtr(true), "\n[profile.home.routing]\ntemplate = true\nserver = true\n"},
		{boolPtr(false), nil, "\n[profile.home.routing]\ntemplate = false\n"},
	} {
		path := filepath.Join(t.TempDir(), FileName)
		f := &File{Profiles: map[string]*Profile{"home": {
			Name: "home", Peer: "203.0.113.10", PasswordRef: "dpapi:8f2a1c", VkHashesRef: "dpapi:1c9bd4",
			Routing: RoutingSection{Template: tc.template, Server: tc.server},
		}}}
		if err := Save(path, f); err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(string(raw), tc.text) {
			t.Errorf("template %v, server %v: в файле\n%s", tc.template, tc.server, raw)
		}
		got, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		p := got.Profiles["home"]
		on := tc.server != nil && *tc.server
		if p.RoutingServerOn() != on || (p.Routing.Server == nil) != (tc.server == nil) || p.Core().ServerRouting != on {
			t.Errorf("server %v: после чтения %v, RoutingServerOn %v", tc.server, p.Routing.Server, p.RoutingServerOn())
		}
	}
}

func TestSaveOmitsEmptyFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	f := &File{Profiles: map[string]*Profile{
		"bare": {
			Name:        "bare",
			Peer:        "203.0.113.10:46000",
			PasswordRef: "dpapi:8f2a1c",
			VkHashesRef: "dpapi:1c9bd4",
		},
	}}
	if err := Save(path, f); err != nil {
		t.Fatalf("Save: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, key := range []string{"workers", "obfs", "turn_transport", "datapath", "kill_switch", "socks", "routing", "template", "server"} {
		if strings.Contains(text, key+" =") || strings.Contains(text, "."+key+"]") {
			t.Errorf("в файле есть незаданный ключ %q:\n%s", key, text)
		}
	}
	if strings.Contains(text, "default_profile") {
		t.Errorf("записан пустой default_profile:\n%s", text)
	}
}

// device_id заводится при первой записи профиля и дальше не меняется: ни
// при повторной записи, ни после чтения файла.
func TestSaveCreatesDeviceIDOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	f := &File{Profiles: map[string]*Profile{
		"home": {Name: "home", Peer: "203.0.113.10", PasswordRef: "dpapi:1", VkHashesRef: "dpapi:2"},
		"work": {Name: "work", Peer: "198.51.100.7", PasswordRef: "dpapi:3", VkHashesRef: "dpapi:4"},
	}}
	if err := Save(path, f); err != nil {
		t.Fatal(err)
	}
	id := f.Profiles["home"].DeviceID
	if len(id) != 16 || deviceIDProblem(id) != "" {
		t.Fatalf("device_id = %q, ожидались 16 шестнадцатеричных знаков", id)
	}
	if f.Profiles["work"].DeviceID == id {
		t.Error("у двух профилей один device_id")
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.Profiles["home"].DeviceID; got != id {
		t.Fatalf("device_id в файле = %q, в памяти %q", got, id)
	}
	loaded.Profiles["home"].Workers = 40
	if err := Save(path, loaded); err != nil {
		t.Fatal(err)
	}
	again, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := again.Profiles["home"].DeviceID; got != id {
		t.Errorf("device_id сменился при повторной записи: %q → %q", id, got)
	}
}

// Запись повторяема: тот же файл даёт те же байты. Иначе `csqtt-cli profile set`
// перетасовывал бы профили при каждом запуске.
func TestSaveIsStable(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "first.toml")
	second := filepath.Join(dir, "second.toml")

	if err := Save(first, sampleFile()); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(first)
	if err != nil {
		t.Fatal(err)
	}
	if err := Save(second, loaded); err != nil {
		t.Fatal(err)
	}

	a, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(second)
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Errorf("повторная запись дала другой файл:\n--- первый ---\n%s\n--- второй ---\n%s", a, b)
	}
}

// Негодные настройки не попадают в файл: писать то, что сами откажемся
// прочитать, нельзя.
func TestSaveRejectsInvalid(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	f := &File{Profiles: map[string]*Profile{
		"home": {Name: "home", Peer: "не адрес", PasswordRef: "dpapi:1", VkHashesRef: "dpapi:2"},
	}}

	if err := Save(path, f); err == nil {
		t.Fatal("негодные настройки записаны")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("файл всё-таки создан: %v", err)
	}
	if f.Profiles["home"].DeviceID != "" {
		t.Error("негодному профилю заведён device_id")
	}
}

// Права 0600: в файле нет секретов, но есть адреса серверов, и лежит он рядом
// с хранилищем секретов. На Windows биты режима роли не играют.
func TestSaveFileMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("на Windows права файла проверяются не битами режима")
	}

	path := filepath.Join(t.TempDir(), FileName)
	if err := Save(path, sampleFile()); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("права %v, ожидались 0600", mode)
	}
}
