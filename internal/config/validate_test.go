package config

import (
	"strings"
	"testing"
)

func validProfile() *Profile {
	return &Profile{Name: "home", Peer: "203.0.113.10:46000", PasswordRef: "dpapi:8f2a1c", VkHashesRef: "dpapi:1c9bd4"}
}

func TestValidateAccepts(t *testing.T) {
	cases := map[string]func(p *Profile){
		"минимальный":               func(p *Profile) {},
		"peer без порта":            func(p *Profile) { p.Peer = "203.0.113.10" },
		"peer — имя":                func(p *Profile) { p.Peer = "vps.example" },
		"peer — имя с портом":       func(p *Profile) { p.Peer = "vps.example:443" },
		"peer — IPv6 в скобках":     func(p *Profile) { p.Peer = "[2001:db8::1]:46000" },
		"peer — IPv6 без порта":     func(p *Profile) { p.Peer = "[2001:db8::1]" },
		"workers 5":                 func(p *Profile) { p.Workers = 5 },
		"workers 10":                func(p *Profile) { p.Workers = 10 },
		"workers 100":               func(p *Profile) { p.Workers = 100 },
		"obfs video":                func(p *Profile) { p.Obfs = "video" },
		"turn_transport tcp_tls":    func(p *Profile) { p.TurnTransport = "tcp_tls" },
		"datapath socks":            func(p *Profile) { p.Datapath = DatapathSocks },
		"datapath tun":              func(p *Profile) { p.Datapath = DatapathTun },
		"socks.listen":              func(p *Profile) { p.Socks.Listen = "127.0.0.1:1080" },
		"socks.listen 127.0.0.2":    func(p *Profile) { p.Socks.Listen = "127.0.0.2:1080" },
		"socks.listen IPv6":         func(p *Profile) { p.Socks.Listen = "[::1]:1080" },
		"device_id как ANDROID_ID":  func(p *Profile) { p.DeviceID = "3f9c2a71d04be658" },
		"device_id как UUID":        func(p *Profile) { p.DeviceID = "123e4567-e89b-12d3-a456-426614174000" },
		"ссылки на секреты Linux":   func(p *Profile) { p.PasswordRef, p.VkHashesRef = "secretservice:a1", "file:b2" },
		"пробелы по краям значений": func(p *Profile) { p.Obfs, p.Peer = " audio ", " 203.0.113.10 " },
		"auto_js без хешей":         func(p *Profile) { p.VkHashMode, p.VkHashesRef = VKHashModeAutoJS, "" },
		"auto_js с хешами":          func(p *Profile) { p.VkHashMode = VKHashModeAutoJS },
		"vk_hash_mode manual":       func(p *Profile) { p.VkHashMode = VKHashModeManual },
		"auto_js с токеном сервера": func(p *Profile) { p.VkHashMode, p.VkHashesRef, p.VkTokenRef = VKHashModeAutoJS, "", "dpapi:7e1f" },
		"manual с токеном сервера":  func(p *Profile) { p.VkTokenRef = "secretservice:c3" },
	}
	for name, change := range cases {
		p := validProfile()
		change(p)
		if err := p.Validate(); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestValidateRejects(t *testing.T) {
	cases := []struct {
		name   string
		change func(p *Profile)
		want   string // что обязано быть в сообщении
	}{
		{"нет peer", func(p *Profile) { p.Peer = "" }, "не задан peer"},
		{"peer с пробелом", func(p *Profile) { p.Peer = "не адрес" }, "не IP-адрес и не имя"},
		{"порт 0", func(p *Profile) { p.Peer = "203.0.113.10:0" }, "от 1 до 65535"},
		{"порт словом", func(p *Profile) { p.Peer = "203.0.113.10:порт" }, "от 1 до 65535"},
		{"порт велик", func(p *Profile) { p.Peer = "203.0.113.10:65536" }, "от 1 до 65535"},
		{"нет адреса", func(p *Profile) { p.Peer = ":46000" }, "не задан адрес"},
		{"голый IPv6", func(p *Profile) { p.Peer = "2001:db8::1" }, "в квадратных скобках"},
		{"имя в скобках", func(p *Profile) { p.Peer = "[vps.example]:46000" }, "не IPv6"},
		{"нет password_ref", func(p *Profile) { p.PasswordRef = "" }, "не задан password_ref"},
		{"нет vk_hashes_ref", func(p *Profile) { p.VkHashesRef = "" }, "не задан vk_hashes_ref"},
		{"manual без хешей", func(p *Profile) { p.VkHashMode, p.VkHashesRef = VKHashModeManual, "" }, "не задан vk_hashes_ref"},
		{"auto_js и хеши не ссылкой", func(p *Profile) { p.VkHashMode, p.VkHashesRef = VKHashModeAutoJS, "abcdef" }, "<хранилище>:<идентификатор>"},
		{"vk_hash_mode", func(p *Profile) { p.VkHashMode = "auto_api" }, `"manual", "auto_js"`},
		{"пароль вместо ссылки", func(p *Profile) { p.PasswordRef = "хорошийпароль" }, "<хранилище>:<идентификатор>"},
		{"токен вместо ссылки", func(p *Profile) { p.VkHashMode, p.VkTokenRef = VKHashModeAutoJS, "vk1.a.токен" }, "vk_token_ref"},
		{"workers не кратно 10", func(p *Profile) { p.Workers = 25 }, "группы по 10"},
		{"workers мало", func(p *Profile) { p.Workers = 3 }, "группы по 10"},
		{"workers между пятью и группой", func(p *Profile) { p.Workers = 9 }, "группы по 10"},
		{"workers много", func(p *Profile) { p.Workers = 110 }, "100"},
		{"workers меньше нуля", func(p *Profile) { p.Workers = -9 }, "workers = -9"},
		{"obfs", func(p *Profile) { p.Obfs = "noise" }, `"audio", "video"`},
		{"obfs регистр", func(p *Profile) { p.Obfs = "Audio" }, `obfs = "Audio"`},
		{"turn_transport tcp", func(p *Profile) { p.TurnTransport = "tcp" }, `"udp", "tcp_tls"`},
		{"datapath", func(p *Profile) { p.Datapath = "external" }, `"tun", "socks"`},
		{"socks.listen не loopback", func(p *Profile) { p.Socks.Listen = "0.0.0.0:1080" }, "только loopback"},
		{"socks.listen в сети", func(p *Profile) { p.Socks.Listen = "198.51.100.5:1080" }, "только loopback"},
		{"socks.listen имя", func(p *Profile) { p.Socks.Listen = "localhost:1080" }, "IP:порт"},
		{"socks.listen порт 0", func(p *Profile) { p.Socks.Listen = "127.0.0.1:0" }, "от 1 до 65535"},
		{"device_id с разделителем", func(p *Profile) { p.DeviceID = "a|b" }, "device_id"},
		{"device_id длинный", func(p *Profile) { p.DeviceID = strings.Repeat("a", 65) }, "длиннее 64"},
	}
	for _, tt := range cases {
		p := validProfile()
		tt.change(p)
		err := p.Validate()
		if err == nil {
			t.Errorf("%s: профиль принят", tt.name)
			continue
		}
		if !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: в сообщении нет %q: %v", tt.name, tt.want, err)
		}
	}
}

// Значение, которое не похоже на ссылку, — скорее всего, сам секрет: в
// сообщение оно не попадает.
func TestValidateDoesNotPrintSecret(t *testing.T) {
	p := validProfile()
	p.PasswordRef, p.VkHashesRef = "хорошийпароль", "abcdefghijklmnopqrstu"
	err := p.Validate()
	if err == nil {
		t.Fatal("секреты вместо ссылок приняты")
	}
	for _, secret := range []string{"хорошийпароль", "abcdefghijklmnopqrstu"} {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("сообщение печатает секрет: %v", err)
		}
	}
}

func TestValidateFile(t *testing.T) {
	f := &File{
		DefaultProfile: "hom",
		Profiles:       map[string]*Profile{"home": validProfile(), "мой": validProfile()},
	}
	err := f.Validate()
	if err == nil {
		t.Fatal("файл принят")
	}
	for _, want := range []string{`default_profile = "hom"`, `имя профиля "мой"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("в сообщении нет %q: %v", want, err)
		}
	}
}

func TestCheckProfileName(t *testing.T) {
	for _, ok := range []string{"home", "work-2", "a_b", "HOME"} {
		if err := CheckProfileName(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "мой", "home work", "a.b", `"home"`} {
		if CheckProfileName(bad) == nil {
			t.Errorf("%q принято", bad)
		}
	}
}
