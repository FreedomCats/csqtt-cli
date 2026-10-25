package config

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// Хеши — выдуманные, длиной как настоящие.
const (
	hashA = "AbCdEfGhIjKlMnOpQrStUv-1"
	hashB = "AbCdEfGhIjKlMnOpQrStUv_2"
)

// link — ссылка так, как её собирает панель сервера (encodeURIComponent).
const link = "csqtt://connect?v=2&host=203.0.113.7&peer=46000&password=p%40ss%2Bw%3Drd"

func TestParseShareLink(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want ShareLink
	}{
		{"без хешей", link, ShareLink{Peer: "203.0.113.7:46000", Password: "p@ss+w=rd"}},
		{"хеши через +", "csqtt://connect?v=2&host=vps.example&peer=46001&password=secret&hashes=" + hashA + "+" + hashB,
			ShareLink{Peer: "vps.example:46001", Password: "secret", Hashes: []string{hashA, hashB}}},
		// %2B внутри хеша — часть хеша, а не разделитель.
		{"%2B в хеше", "csqtt://connect?v=2&host=vps.example&peer=46000&password=secret&hashes=abcdefghijklmno%2Bp",
			ShareLink{Peer: "vps.example:46000", Password: "secret", Hashes: []string{"abcdefghijklmno+p"}}},
		// «+» в пароле без кодирования — плюс, а не пробел.
		{"+ в пароле", "csqtt://connect?v=2&host=203.0.113.7&peer=46000&password=a+b",
			ShareLink{Peer: "203.0.113.7:46000", Password: "a+b"}},
		{"IPv6 закодирован", "csqtt://connect?v=2&host=2001%3Adb8%3A%3A1&peer=46000&password=secret",
			ShareLink{Peer: "[2001:db8::1]:46000", Password: "secret"}},
		// location.hostname в браузере отдаёт IPv6 в скобках.
		{"IPv6 в скобках", "csqtt://connect?v=2&host=%5B2001%3Adb8%3A%3A1%5D&peer=46000&password=secret",
			ShareLink{Peer: "[2001:db8::1]:46000", Password: "secret"}},
		{"ссылка на звонок вместо хеша",
			"csqtt://connect?v=2&host=203.0.113.7&peer=46000&password=secret&hashes=https%3A%2F%2Fvk.com%2Fcall%2Fjoin%2F" + hashA,
			ShareLink{Peer: "203.0.113.7:46000", Password: "secret", Hashes: []string{hashA}}},
		{"повтор хеша", "csqtt://connect?v=2&host=203.0.113.7&peer=46000&password=secret&hashes=" + hashA + "+" + hashB + "+" + hashA,
			ShareLink{Peer: "203.0.113.7:46000", Password: "secret", Hashes: []string{hashA, hashB}}},
		{"шесть хешей", "csqtt://connect?v=2&host=203.0.113.7&peer=46000&password=secret&hashes=" + hashes(6),
			ShareLink{Peer: "203.0.113.7:46000", Password: "secret", Hashes: strings.Split(hashes(6), "+")}},
		{"порядок и регистр", "CSQTT://Connect?password=secret&peer=46000&HOST=203.0.113.7&v=2",
			ShareLink{Peer: "203.0.113.7:46000", Password: "secret"}},
		{"неизвестный параметр", link + "&name=home",
			ShareLink{Peer: "203.0.113.7:46000", Password: "p@ss+w=rd"}},
		{"&amp; из HTML", "csqtt://connect?v=2&amp;host=203.0.113.7&amp;peer=46000&amp;password=secret",
			ShareLink{Peer: "203.0.113.7:46000", Password: "secret"}},
		{"точка с запятой", "csqtt://connect?v=2;host=203.0.113.7;peer=46000;password=secret",
			ShareLink{Peer: "203.0.113.7:46000", Password: "secret"}},
		{"пробелы по краям", "  " + link + "\n", ShareLink{Peer: "203.0.113.7:46000", Password: "p@ss+w=rd"}},
		// Мессенджер съел все & — Android-клиент такую ссылку принимает.
		{"склеенная", "csqtt://connect?v=2host=203.0.113.88peer=46000password=secret",
			ShareLink{Peer: "203.0.113.88:46000", Password: "secret"}},
		{"склеенная с хешами", "csqtt://connect?v=2host=203.0.113.88peer=46000password=secrethashes=" + hashA + "+" + hashB,
			ShareLink{Peer: "203.0.113.88:46000", Password: "secret", Hashes: []string{hashA, hashB}}},
		{"старая", "csqtt://secret@203.0.113.7:46000", ShareLink{Peer: "203.0.113.7:46000", Password: "secret"}},
		// Пароля в ссылке 2.5.0 может не быть: его спросят отдельно.
		{"без пароля", "csqtt://connect?v=2&host=203.0.113.7&peer=46000&hashes=" + hashA,
			ShareLink{Peer: "203.0.113.7:46000", Hashes: []string{hashA}}},
		{"токен VK", "csqtt://connect?v=2&host=203.0.113.7&peer=46000&password=secret&token=vk1.a.Fake_token-0%2B1",
			ShareLink{Peer: "203.0.113.7:46000", Password: "secret", VKToken: "vk1.a.Fake_token-0+1"}},
		{"склеенная с токеном", "csqtt://connect?v=2host=203.0.113.88peer=46000password=secrettoken=vk1.a.Fake",
			ShareLink{Peer: "203.0.113.88:46000", Password: "secret", VKToken: "vk1.a.Fake"}},
		{"склеенная без пароля", "csqtt://connect?v=2host=203.0.113.88peer=46000token=vk1.a.Fake",
			ShareLink{Peer: "203.0.113.88:46000", VKToken: "vk1.a.Fake"}},
		{"старая, пароль с @ и :", "csqtt://p%40s:s@203.0.113.7:46000/",
			ShareLink{Peer: "203.0.113.7:46000", Password: "p@s:s"}},
		{"старая, IPv6", "csqtt://secret@[2001:db8::1]:46000", ShareLink{Peer: "[2001:db8::1]:46000", Password: "secret"}},
	}
	for _, tt := range cases {
		got, err := ParseShareLink(tt.in)
		if err != nil {
			t.Errorf("%s: %v", tt.name, err)
			continue
		}
		if !reflect.DeepEqual(*got, tt.want) {
			t.Errorf("%s: %+v, ожидалось %+v", tt.name, *got, tt.want)
		}
	}
}

// hashes — n разных выдуманных хешей через «+».
func hashes(n int) string {
	var hs []string
	for i := range n {
		hs = append(hs, fmt.Sprintf("abcdefghijklmnop%d", i+1))
	}
	return strings.Join(hs, "+")
}

func TestParseShareLinkRejects(t *testing.T) {
	const base = "csqtt://connect?v=2&host=203.0.113.7&peer=46000&password=secret"
	cases := []struct {
		name, in, want string
	}{
		{"пусто", " ", "пуста"},
		{"не csqtt", "qwdtt://config?peer=203.0.113.7", "csqtt://"},
		{"адрес в интернете", "https://vps.example/csqtt", "адрес в интернете"},
		{"дважды подряд", base + base, "дважды"},
		{"версия 3", strings.Replace(base, "v=2", "v=3", 1), "v=3"},
		{"без версии", strings.Replace(base, "v=2&", "", 1), "v=2"},
		{"без параметров", "csqtt://connect", "нет параметров"},
		{"без параметров с ?", "csqtt://connect?", "нет параметров"},
		{"пустой host", strings.Replace(base, "host=203.0.113.7", "host=", 1), "host"},
		{"без host", strings.Replace(base, "host=203.0.113.7&", "", 1), "host"},
		{"пустой peer", strings.Replace(base, "peer=46000", "peer=", 1), "peer"},
		{"peer 0", strings.Replace(base, "peer=46000", "peer=0", 1), "порт"},
		{"peer велик", strings.Replace(base, "peer=46000", "peer=65536", 1), "порт"},
		{"peer — адрес", strings.Replace(base, "peer=46000", "peer=203.0.113.7%3A46000", 1), "порт"},
		{"peer со знаком", strings.Replace(base, "peer=46000", "peer=%2B46000", 1), "порт"},
		{"пустой пароль", strings.Replace(base, "password=secret", "password=", 1), "password"},
		{"пустой токен", base + "&token=", "пустой параметр token"},
		{"токен с пробелом", base + "&token=vk1.a%20sec", "пробел"},
		{"токен с переводом строки", base + "&token=vk1.a%0Asec", "пробел"},
		{"длинный токен", base + "&token=" + strings.Repeat("t", 4097), "длиннее 4096"},
		{"хеши и токен", base + "&hashes=" + hashA + "&token=vk1.a.sec", "и хеши звонков, и токен"},
		{"повтор токена", base + "&token=vk1.a.sec&token=vk1.a.sec", "token повторяется"},
		{"пароль с пробелом", strings.Replace(base, "password=secret", "password=sec%20%0Aret", 1), "пробел"},
		{"повтор параметра", base + "&host=198.51.100.1", "host повторяется"},
		{"пустой hashes", base + "&hashes=", "пустой параметр hashes"},
		{"пустой хеш", base + "&hashes=" + hashA + "++" + hashB, "пустой хеш"},
		{"хеш в конце +", base + "&hashes=" + hashA + "+", "пустой хеш"},
		{"короткий хеш", base + "&hashes=short", "короче 16"},
		{"чужая ссылка вместо хеша", base + "&hashes=https%3A%2F%2Fvps.example%2F" + hashA, "пустой хеш"},
		{"семь хешей", base + "&hashes=" + hashes(7), "7 хешей"},
		{"битый %", strings.Replace(base, "password=secret", "password=sec%zzret", 1), "%-последовательность"},
		{"#", base + "#home", "«#»"},
		{"host не адрес", strings.Replace(base, "host=203.0.113.7", "host=vps%20example", 1), "адрес сервера"},
		{"host — IPv6 с ошибкой", strings.Replace(base, "host=203.0.113.7", "host=2001%3Adb8%3A%3Ag", 1), "адрес сервера"},
		{"старая без порта", "csqtt://secret@203.0.113.7", "<хост>:<порт>"},
		{"старая без пароля", "csqtt://@203.0.113.7:46000", "нет пароля"},
		{"старая без @", "csqtt://203.0.113.7:46000", "ожидалась"},
		{"старая с путём", "csqtt://secret@203.0.113.7:46000/x", "лишнее"},
		{"старая, IPv6 без скобок", "csqtt://secret@2001:db8::1:46000", "<хост>:<порт>"},
	}
	for _, tt := range cases {
		_, err := ParseShareLink(tt.in)
		if err == nil {
			t.Errorf("%s: ссылка принята", tt.name)
			continue
		}
		if !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: в сообщении нет %q: %v", tt.name, tt.want, err)
		}
		if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "sec") {
			t.Errorf("%s: сообщение печатает пароль: %v", tt.name, err)
		}
	}
}
