package coreproc

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMaskLog(t *testing.T) {
	cases := map[string]string{
		"[STDIN] CAPTCHA_RESULT|abcdef":             "[STDIN] CAPTCHA_RESULT|<токен скрыт, 6 зн.>",
		"[STDIN] CAPTCHA_RESULT|abcdef (x2)":        "[STDIN] CAPTCHA_RESULT|<токен скрыт, 6 зн.> (x2)",
		"[STDIN] CAPTCHA_RESULT|error:closed":       "[STDIN] CAPTCHA_RESULT|error:closed",
		"[STDIN] CAPTCHA_RESULT|":                   "[STDIN] CAPTCHA_RESULT|",
		"[STDIN] PAUSE":                             "[STDIN] PAUSE",
		"CAPTCHA_RESULT|one CAPTCHA_RESULT|two-two": "CAPTCHA_RESULT|<токен скрыт, 3 зн.> CAPTCHA_RESULT|<токен скрыт, 7 зн.>",
		"[КАПЧА] Результат от Kotlin записан в канал":      "[КАПЧА] Результат от Kotlin записан в канал",
		"[STDIN] CAPTCHA_RESULT|tok.en_-with=signs\tхвост": "[STDIN] CAPTCHA_RESULT|<токен скрыт, 18 зн.>\tхвост",
	}
	for in, want := range cases {
		if got := maskLog(in); got != want {
			t.Errorf("maskLog(%q) = %q, want %q", in, got, want)
		}
	}
	// То же в stdout: неразобранная строка уходит в журнал скрытой.
	if got := ParseLine("[STDIN] CAPTCHA_RESULT|abcdef"); got != (Log{Text: "[STDIN] CAPTCHA_RESULT|<токен скрыт, 6 зн.>"}) {
		t.Errorf("ParseLine = %#v", got)
	}
}

func TestCaptchaError(t *testing.T) {
	inner := errors.New("окно капчи закрыто")
	err := error(&CaptchaError{Reason: "closed", Err: inner})
	if err.Error() != inner.Error() || !errors.Is(err, inner) {
		t.Errorf("CaptchaError = %q", err)
	}
	if got := (&CaptchaError{Reason: "limit"}).Error(); !strings.Contains(got, "limit") {
		t.Errorf("CaptchaError без Err = %q", got)
	}
}

// Сторож выключателя: ядро должно по-прежнему проверять CoreSolverOffEnv в
// начале решателя, до первого запроса к странице капчи. Новая версия ядра без
// этой проверки снова отдавала бы окну испорченную сессию (ERROR_LIMIT), и
// тест должен упасть при обновлении ядра, а не на живой капче.
func TestCoreSolverSwitchInCore(t *testing.T) {
	path := filepath.Join("..", "..", "core", "rust-client", "captcha.rs")
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("исходник ядра: %v", err)
	}
	s := string(src)
	fn := strings.Index(s, "async fn solve_once(")
	if fn < 0 {
		t.Fatal("в captcha.rs нет solve_once — решатель ядра устроен иначе, выключатель проверить заново")
	}
	body := s[fn:]
	check := strings.Index(body, `std::env::var_os("`+CoreSolverOffEnv+`")`)
	fetch := strings.Index(body, "fetch_html(")
	if check < 0 || fetch < 0 || check > fetch {
		t.Fatalf("solve_once не проверяет %s до запроса страницы капчи (проверка на %d, запрос на %d)",
			CoreSolverOffEnv, check, fetch)
	}
	if !strings.Contains(body[check:fetch], "bail!") {
		t.Errorf("проверка %s в solve_once не отказывает сразу", CoreSolverOffEnv)
	}
}
