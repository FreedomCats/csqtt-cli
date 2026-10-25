package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// header — шапка записанного файла. Предупреждение о потере комментариев стоит
// в самом файле не из вежливости: узнать об этом иначе можно было бы только
// потеряв их.
const header = `# Файл настроек csqtt-cli. Записан командой ` + "`csqtt-cli profile`" + `.
#
# Править руками можно, но комментарии и порядок ключей при следующей записи
# не сохранятся: файл переписывается целиком.
`

// Save записывает файл настроек целиком.
//
// Профилю без device_id Save заводит его здесь же, прямо в f: идентификатор
// появляется при первой записи профиля и дальше переписывается как есть.
//
// Комментарии и порядок ключей исходного файла не сохраняются: разбор отдаёт
// структуру, а не дерево с комментариями, и класть их обратно неоткуда.
// Переписать файл целиком — честнее, чем править его построчно регулярками
// и однажды испортить чужую разметку.
//
// Перед записью настройки проверяются: писать файл, который сами же откажемся
// читать, нельзя. Запись идёт во временный файл с последующим Rename, чтобы
// обрыв не оставил профили наполовину записанными.
func Save(path string, f *File) error {
	if err := f.Validate(); err != nil {
		return err
	}
	for _, name := range f.Names() {
		if p := f.Profiles[name]; strings.TrimSpace(p.DeviceID) == "" {
			id, err := newDeviceID()
			if err != nil {
				return err
			}
			p.DeviceID = id
		}
	}

	if err := makeDir(filepath.Dir(path)); err != nil {
		return err
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(render(f)), 0o600); err != nil {
		return fmt.Errorf("файл настроек %s: %w", tmp, err)
	}
	if err := giveToInvoker(tmp); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("файл настроек %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("файл настроек %s: %w", path, err)
	}
	return nil
}

// newDeviceID — 16 шестнадцатеричных знаков, как ANDROID_ID, который
// передаёт ядру Android-клиент: сервер видит привычный вид.
func newDeviceID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("идентификатор устройства: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// render собирает текст файла.
//
// Пустое поле не записывается вовсе: пропуск означает «как по умолчанию»,
// и записать вместо него нынешнее умолчание значило бы заморозить его в файле
// пользователя. Порядок ключей — тот же, что в csqtt-cli.example.toml.
func render(f *File) string {
	var b strings.Builder

	b.WriteString(header)

	if def := strings.TrimSpace(f.DefaultProfile); def != "" {
		b.WriteString("\ndefault_profile = " + quote(def) + "\n")
	}

	if vk := f.VK; strings.TrimSpace(vk.TokenRef) != "" || strings.TrimSpace(vk.UserID) != "" {
		b.WriteString("\n[vk]\n")
		writeString(&b, "token_ref", vk.TokenRef)
		writeString(&b, "user_id", vk.UserID)
	}

	for _, name := range f.Names() {
		p := f.Profiles[name]

		fmt.Fprintf(&b, "\n[profile.%s]\n", name)
		writeString(&b, "peer", p.Peer)
		writeString(&b, "password_ref", p.PasswordRef)
		writeString(&b, "vk_hashes_ref", p.VkHashesRef)
		writeString(&b, "vk_hash_mode", p.VkHashMode)
		writeString(&b, "device_id", p.DeviceID)
		writeString(&b, "datapath", p.Datapath)
		if p.Workers != 0 {
			fmt.Fprintf(&b, "workers = %d\n", p.Workers)
		}
		writeString(&b, "obfs", p.Obfs)
		writeString(&b, "turn_transport", p.TurnTransport)
		if p.KillSwitch != nil {
			fmt.Fprintf(&b, "kill_switch = %t\n", *p.KillSwitch)
		}

		if listen := strings.TrimSpace(p.Socks.Listen); listen != "" {
			fmt.Fprintf(&b, "\n[profile.%s.socks]\n", name)
			writeString(&b, "listen", listen)
		}
		if r := p.Routing; r.Template != nil || r.Server != nil {
			fmt.Fprintf(&b, "\n[profile.%s.routing]\n", name)
			if r.Template != nil {
				fmt.Fprintf(&b, "template = %t\n", *r.Template)
			}
			if r.Server != nil {
				fmt.Fprintf(&b, "server = %t\n", *r.Server)
			}
		}
	}

	return b.String()
}

func writeString(b *strings.Builder, key, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	b.WriteString(key + " = " + quote(value) + "\n")
}

// quote заключает значение в кавычки по правилам TOML.
//
// strconv.Quote годится потому, что писать сюда попадают только проверенные
// значения: адреса host:port, ссылки вида <хранилище>:<идентификатор>,
// идентификатор устройства, id аккаунта VK и слова из закрытых списков. Экранирование Go и
// TOML на таком наборе совпадает.
func quote(value string) string {
	return strconv.Quote(value)
}
