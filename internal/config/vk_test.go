package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Секция [vk] переживает запись и чтение вместе с профилями, а без входа её
// в файле нет вовсе.
func TestSaveVKSection(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	f := sampleFile()
	f.VK = VKSection{TokenRef: "secretservice:0a1b2c3d", UserID: "123456789"}
	if err := Save(path, f); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.VK != f.VK || !got.VK.LoggedIn() || len(got.Profiles) != 2 {
		t.Fatalf("после записи и чтения: [vk] %+v, профилей %d", got.VK, len(got.Profiles))
	}

	// Только [vk], без профилей: вход раньше первого профиля.
	if err := Save(path, &File{VK: f.VK}); err != nil {
		t.Fatalf("Save без профилей: %v", err)
	}
	if got, err := Load(path); err != nil || got.VK != f.VK {
		t.Fatalf("без профилей: %+v, %v", got, err)
	}

	f.VK = VKSection{}
	if err := Save(path, f); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "[vk]") {
		t.Errorf("пустая секция [vk] записана:\n%s", raw)
	}
}

func TestParseVKProblems(t *testing.T) {
	for _, tc := range []struct {
		name, text, want string
		secret           string // чего в сообщении быть не должно
	}{
		{"токен вместо ссылки", "[vk]\ntoken_ref = \"vk1.a.выдуманный\"\n", "token_ref", "vk1.a.выдуманный"},
		{"токен прямо в файле", "[vk]\ntoken = \"vk1.a.выдуманный\"\n", "vk login", "vk1.a.выдуманный"},
		{"user_id не число", "[vk]\ntoken_ref = \"file:00\"\nuser_id = \"id1x\"\n", "user_id", "id1x"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.text), "profiles.toml")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ошибка %v, ожидалось про %s", err, tc.want)
			}
			if strings.Contains(err.Error(), tc.secret) {
				t.Errorf("значение в сообщении: %v", err)
			}
		})
	}
}
