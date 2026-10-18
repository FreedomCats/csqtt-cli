package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/FreedomCats/csqtt-cli/internal/coreproc"
)

// hashes check на поддельном ядре: настоящий coreproc.CheckHashes, ядро
// отвечает по имени хеша (alive…, dead…, прочие — недоступны).
func TestHashesCheck(t *testing.T) {
	te := newTestEnv(t)
	te.addHome()
	te.core = "hashcheck"

	code, stdout, stderr := te.run("hashes", "check", "-v")
	if code != exitError {
		t.Fatalf("код %d (живы не все — ожидался %d), stderr:\n%s", code, exitError, stderr)
	}
	noSecrets(t, "вывод", stdout+stderr)
	for _, want := range []string{
		"  №1  живой\n",
		"  №2  мёртвый — ошибка VK 954: Invalid join link\n",
		"  №3  недоступен — request for link=<хеш №3> failed\n",
		"ядро: [VK] проверяю <хеш №1>",
		"Мёртвые: №2. Профиль не менялся",
		"profile set home -hashes",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("нет %q:\n%s", want, stdout)
		}
	}
	if !strings.Contains(stderr, "живых звонков 1 из 3") {
		t.Errorf("итог:\n%s", stderr)
	}

	// Профиль проверка не меняет: хеши в хранилище те же.
	file := loadTestFile(t, te)
	if got, _ := te.store.Get(file.Profiles["home"].VkHashesRef); got != testHashA+","+testHashB+","+testHashC {
		t.Errorf("хеши после проверки: %q", got)
	}
}

// Без -v журнал ядра не печатается.
func TestHashesCheckQuiet(t *testing.T) {
	te := newTestEnv(t)
	te.addHome()
	te.core = "hashcheck"
	_, stdout, _ := te.run("hashes", "check", "home")
	if strings.Contains(stdout, "ядро:") {
		t.Errorf("журнал ядра без -v:\n%s", stdout)
	}
}

func TestHashesCheckAllAlive(t *testing.T) {
	te := newTestEnv(t)
	te.addHome()
	var got coreproc.Config
	var gotPath string
	te.core = "не запускается" // путь к ядру есть, само ядро подменено ниже
	e := te.env(&syncBuffer{}, &syncBuffer{})
	e.hashCheck = func(_ context.Context, path string, cfg coreproc.Config, _ func(coreproc.Log)) ([]coreproc.HashCheck, error) {
		got, gotPath = cfg, path
		var res []coreproc.HashCheck
		for _, h := range cfg.Hashes {
			res = append(res, coreproc.HashCheck{Hash: h, Status: coreproc.HashValid})
		}
		return res, nil
	}
	var out syncBuffer
	e.stdout = &out
	if code := run(context.Background(), e, []string{"hashes", "check", "-config", te.configPath()}); code != exitOK {
		t.Fatalf("код %d:\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "Все звонки живы (3)") {
		t.Errorf("вывод:\n%s", out.String())
	}
	// Проверке нужен только список хешей: пароль ядру не отдаётся.
	if got.Password != "" || len(got.Hashes) != 3 || gotPath == "" {
		t.Errorf("проверке отдано: %+v, ядро %q", got, gotPath)
	}
}

// Ctrl+C во время проверки — «прервано», ядро завершено.
func TestHashesCheckCancel(t *testing.T) {
	te := newTestEnv(t)
	te.addHome()
	te.core = "не запускается"
	e := te.env(&syncBuffer{}, &syncBuffer{})
	e.hashCheck = func(ctx context.Context, _ string, _ coreproc.Config, _ func(coreproc.Log)) ([]coreproc.HashCheck, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	var errOut syncBuffer
	e.stderr = &errOut
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	if code := run(ctx, e, []string{"hashes", "check", "-config", te.configPath()}); code != exitError {
		t.Fatalf("код %d", code)
	}
	if !strings.Contains(errOut.String(), "прервано") {
		t.Errorf("stderr:\n%s", errOut.String())
	}
}

func TestHashesCheckWithoutCore(t *testing.T) {
	te := newTestEnv(t)
	te.addHome()
	code, _, stderr := te.run("hashes", "check")
	if code != exitError || !strings.Contains(stderr, "ядра в этом тесте нет") {
		t.Errorf("код %d, stderr:\n%s", code, stderr)
	}
}
