//go:build windows

package main

import (
	"bytes"
	"strings"
	"testing"
)

// TestRunOffline прогоняет снимок по настоящей системе без сети: все
// разделы на месте, сетевых нет. Без прав администратора раздел WFP —
// с ошибкой, это нормально.
func TestRunOffline(t *testing.T) {
	var b bytes.Buffer
	run(&b, options{label: "тест", name: "csqtt-cli", offline: true})
	out := b.String()
	for _, s := range []string{"— тест ===", "-- Процессы csqtt-cli.exe", "-- Адаптеры", "-- Маршруты /0 и /1",
		"-- WFP: защита DNS", "-- Реестр", "NRPT", "{3307B858-2357-4C8A-8349-A5F7FD1A0255}"} {
		if !strings.Contains(out, s) {
			t.Errorf("в выводе нет %q:\n%s", s, out)
		}
	}
	for _, s := range []string{"Внешний адрес", "Резолвер"} {
		if strings.Contains(out, s) {
			t.Errorf("с offline в выводе есть %q", s)
		}
	}
	t.Log("\n" + out)
}
