package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/FreedomCats/csqtt-cli/internal/secretservice"
)

// Служебная команда связки ключей — та, которую secretservice под sudo
// запускает от имени вызвавшего: запрос из stdin, ответ JSON в stdout. Связки
// в тестах нет (TestMain убирает шину сеанса), поэтому ответ — «недоступна»,
// но ответ есть. Прочие аргументы — не она: их разбирает обычный CLI.
func TestSecretHelperCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code, ok := runSecretHelper([]string{secretservice.HelperCommand},
		strings.NewReader(`{"op":"get","id":"csqtt-cli-test-нет-такого"}`), &stdout, &stderr)
	if !ok || code != 0 {
		t.Fatalf("служебная команда: ok %v, код %d, stderr %q", ok, code, stderr.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		t.Fatalf("ответ не JSON: %q", stdout.String())
	}
	if resp["unavailable"] != true {
		t.Errorf("ответ без связки: %v", resp)
	}

	stdout.Reset()
	if code, ok := runSecretHelper([]string{secretservice.HelperCommand}, strings.NewReader("не json"), &stdout, &stderr); !ok || code == 0 {
		t.Errorf("негодный запрос: ok %v, код %d", ok, code)
	}

	for _, args := range [][]string{nil, {"connect"}, {secretservice.HelperCommand, "лишний"}, {"profile", secretservice.HelperCommand}} {
		if _, ok := runSecretHelper(args, strings.NewReader(""), &stdout, &stderr); ok {
			t.Errorf("%q принят за служебную команду", args)
		}
	}
}

// Служебную команду принимает сам csqtt-cli: secretservice под sudo
// запускает исполняемый файл с ней одной, и Main отвечает JSON, а не
// «неизвестная команда».
func TestMainSecretHelper(t *testing.T) {
	cmd := exec.Command(os.Args[0], secretservice.HelperCommand)
	cmd.Env = append(os.Environ(), asMainEnv+"=1")
	cmd.Stdin = strings.NewReader(`{"op":"get","id":"csqtt-cli-test-нет-такого"}`)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("служебная команда: %v, stderr %q", err, stderr.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		t.Fatalf("ответ не JSON: %q, stderr %q", stdout.String(), stderr.String())
	}
	if resp["unavailable"] != true {
		t.Errorf("ответ без связки: %v", resp)
	}
}
