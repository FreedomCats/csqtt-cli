package captcha

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/FreedomCats/csqtt-cli/internal/coreproc"
)

// Сторож режима wv и срока окна (правка П5): оболочка запускает ядро с
// coreproc.CaptchaModeWebView и ждёт от него CAPTCHA_SOLVE|selected, а таймер
// окна (ManualTimeout) верит, что ядро ждёт ответа столько же. Новая версия
// ядра, где режим wv пропал, снова идёт через решатель или ждёт меньше, должна
// уронить тест при обновлении ядра, а не живую капчу: окно закрылось бы позже,
// чем ядро перестало ждать, и токен человека пропал бы.
func TestCoreCaptchaWindowInCore(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "core", "rust-client", "captcha.rs"))
	if err != nil {
		t.Fatalf("исходник ядра: %v", err)
	}
	s := strings.ReplaceAll(string(src), "\r\n", "\n")

	m := regexp.MustCompile(`const MANUAL_WEBVIEW_TIMEOUT: Duration = Duration::from_secs\((\d+)\);`).FindStringSubmatch(s)
	if m == nil {
		t.Fatal("в captcha.rs нет MANUAL_WEBVIEW_TIMEOUT — правка П5 не применена или ядро устроено иначе")
	}
	if secs, _ := strconv.Atoi(m[1]); time.Duration(secs)*time.Second != ManualTimeout {
		t.Errorf("ядро ждёт окно %s с, а таймер окна оболочки — %s", m[1], ManualTimeout)
	}

	mode := coreproc.CaptchaModeWebView
	if !strings.Contains(s, `"`+mode+`" => "`+mode+`",`) {
		t.Errorf("CaptchaSolver::new не знает режима %q — ядро взяло бы auto", mode)
	}
	// Ветка режима в solve: от «"wv" => {» до первой закрывающей скобки на
	// её отступе.
	arm := regexp.MustCompile(`(?s)\n( +)"` + mode + `" => \{\n(.*?)\n( +)\}\n`).FindStringSubmatch(s)
	if arm == nil || arm[1] != arm[3] {
		t.Fatalf("в CaptchaSolver::solve нет ветки режима %q", mode)
	}
	body := arm[2]
	if !strings.Contains(body, `self.webview(stream_id, captcha, "`+ModeSelected+`", MANUAL_WEBVIEW_TIMEOUT)`) {
		t.Errorf("ветка %q не открывает окно %q на MANUAL_WEBVIEW_TIMEOUT:\n%s", mode, ModeSelected, body)
	}
	if strings.Contains(body, "solve_attempts") {
		t.Errorf("ветка %q зовёт решатель ядра:\n%s", mode, body)
	}
	if !strings.Contains(s, `self.webview(stream_id, captcha, "`+ModeManual+`", MANUAL_WEBVIEW_TIMEOUT)`) {
		t.Errorf("ручное окно цепочки auto — не на MANUAL_WEBVIEW_TIMEOUT")
	}
}
