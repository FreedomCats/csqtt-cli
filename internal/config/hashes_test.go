package config

import (
	"slices"
	"testing"
)

// Разбор повторяет -vk ядра (parse_hashes в worker.rs).
func TestParseHashes(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{" , ;\n", nil},
		{hashA, []string{hashA}},
		{hashA + "," + hashB, []string{hashA, hashB}},
		{hashA + "; " + hashB + "\r\n" + hashA, []string{hashA, hashB}},
		{"\t<" + hashA + ">  '" + hashB + "'", []string{hashA, hashB}},
		{"https://vk.com/call/join/" + hashA + "?x=1", []string{hashA}},
		{"vk.ru/call/join/" + hashA + "/", []string{hashA}},
		{"HTTPS://M.VK.COM/CALL/JOIN/" + hashA + "#frag", []string{hashA}},
		// Иная ссылка в интернете — не хеш.
		{"https://vps.example/" + hashA + "," + hashB, []string{hashB}},
		{hashA + "/tail", []string{hashA}},
	}
	for _, tt := range cases {
		if got := ParseHashes(tt.in); !slices.Equal(got, tt.want) {
			t.Errorf("ParseHashes(%q) = %q, ожидалось %q", tt.in, got, tt.want)
		}
	}
}
