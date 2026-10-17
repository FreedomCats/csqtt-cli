package config

import (
	"path/filepath"
	"testing"

	"github.com/FreedomCats/csqtt-cli/internal/sudo"
)

func TestConfigDir(t *testing.T) {
	underSudo := &sudo.User{Name: "user", Uid: 4242, Gid: 4242, HomeDir: "/home/user"}

	tests := []struct {
		name    string
		goos    string
		env     map[string]string
		invoker *sudo.User
		want    string
	}{
		{
			name: "windows берёт APPDATA",
			goos: "windows",
			env:  map[string]string{"APPDATA": `C:\Users\user\AppData\Roaming`},
			want: `C:\Users\user\AppData\Roaming`,
		},
		{
			name: "unix берёт XDG_CONFIG_HOME",
			goos: "linux",
			env:  map[string]string{"XDG_CONFIG_HOME": "/home/user/.config-2", "HOME": "/home/user"},
			want: "/home/user/.config-2",
		},
		{
			name: "unix без XDG откатывается на ~/.config",
			goos: "linux",
			env:  map[string]string{"HOME": "/home/user"},
			want: filepath.Join("/home/user", ".config"),
		},
		{
			// macOS консольный, и настройки лежат там же, где у остальных
			// консольных программ, а не в Library/Application Support.
			name: "macOS считается unix",
			goos: "darwin",
			env:  map[string]string{"HOME": "/Users/user"},
			want: filepath.Join("/Users/user", ".config"),
		},
		{
			// Под sudo окружение — root'а: настройки ищутся у вызвавшего.
			name:    "sudo берёт каталог вызвавшего",
			goos:    "linux",
			env:     map[string]string{"HOME": "/root"},
			invoker: underSudo,
			want:    filepath.Join("/home/user", ".config"),
		},
		{
			// XDG_CONFIG_HOME, доживший до sudo (sudo -E или настройки root),
			// вызвавшему не принадлежит — не учитывается.
			name:    "sudo не смотрит на XDG_CONFIG_HOME",
			goos:    "linux",
			env:     map[string]string{"XDG_CONFIG_HOME": "/root/.config", "HOME": "/root"},
			invoker: underSudo,
			want:    filepath.Join("/home/user", ".config"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := configDir(tt.goos, func(key string) string { return tt.env[key] }, tt.invoker)
			if err != nil {
				t.Fatalf("configDir: %v", err)
			}
			if got != tt.want {
				t.Errorf("configDir = %q, ожидалось %q", got, tt.want)
			}
		})
	}
}

func TestConfigDirWithoutEnv(t *testing.T) {
	empty := func(string) string { return "" }

	for _, goos := range []string{"windows", "linux"} {
		if _, err := configDir(goos, empty, nil); err == nil {
			t.Errorf("%s: пустое окружение принято за корректное", goos)
		}
	}
}

// Path складывает каталог платформы с именами каталога и файла.
func TestPath(t *testing.T) {
	path, err := Path()
	if err != nil {
		t.Skipf("окружение без каталога настроек: %v", err)
	}
	if filepath.Base(path) != FileName || filepath.Base(filepath.Dir(path)) != AppDir {
		t.Errorf("Path = %q, ожидался хвост %s", path, filepath.Join(AppDir, FileName))
	}
}
