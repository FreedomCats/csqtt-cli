package config

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/FreedomCats/csqtt-cli/internal/sudo"
)

// fakeSudo подменяет вызвавшего sudo и chown на запись вызовов: настоящий
// chown требует root.
func fakeSudo(t *testing.T, owner *sudo.User, fail error) *[]string {
	t.Helper()
	var calls []string
	oldInvoker, oldChown := invoker, chown
	t.Cleanup(func() { invoker, chown = oldInvoker, oldChown })

	invoker = func() (*sudo.User, error) { return owner, nil }
	chown = func(path string, uid, gid int) error {
		if uid != 4242 || gid != 4343 {
			t.Errorf("chown %s: %d:%d, ожидалось 4242:4343", path, uid, gid)
		}
		calls = append(calls, path)
		return fail
	}
	return &calls
}

var sudoUser = &sudo.User{Name: "user", Uid: 4242, Gid: 4343, HomeDir: "/home/user"}

// Под sudo вызвавшему отдаётся всё, что csqtt-cli создал: недостающие каталоги
// сверху вниз и файлы до переименования. Существовавшее не трогается.
func TestWriteUnderSudoGivesFilesAway(t *testing.T) {
	calls := fakeSudo(t, sudoUser, nil)
	root := t.TempDir()
	dir := filepath.Join(root, "a", "b")
	store := NewSecretStoreFor(filepath.Join(dir, FileName))

	if _, err := store.Put("home/password", "секрет"); err != nil {
		t.Fatal(err)
	}
	secrets := filepath.Join(dir, SecretsFileName)
	want := []string{filepath.Join(root, "a"), dir, secrets + ".tmp"}
	if !slices.Equal(*calls, want) {
		t.Errorf("chown: %q, ожидалось %q", *calls, want)
	}

	*calls = nil
	if _, err := store.Put("home/vk_hashes", "другой"); err != nil {
		t.Fatal(err)
	}
	want = []string{secrets + ".tmp"}
	if !slices.Equal(*calls, want) {
		t.Errorf("chown при второй записи: %q, ожидалось %q", *calls, want)
	}
}

func TestWriteWithoutSudoKeepsOwner(t *testing.T) {
	calls := fakeSudo(t, nil, nil)
	path := filepath.Join(t.TempDir(), "a", FileName)

	if _, err := NewSecretStoreFor(path).Put("home/password", "секрет"); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 0 {
		t.Errorf("без sudo вызван chown: %q", *calls)
	}
}

// Отдать файл не вышло — запись не состоялась, и временного файла root'а
// не осталось.
func TestWriteUnderSudoChownFails(t *testing.T) {
	fakeSudo(t, sudoUser, errors.New("нельзя"))
	dir := t.TempDir()

	if _, err := NewSecretStoreFor(filepath.Join(dir, FileName)).Put("home/password", "секрет"); err == nil {
		t.Fatal("ошибка chown проглочена")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("после ошибки в каталоге остались файлы: %v", entries)
	}
}

// Запись профиля под sudo — так же: каталоги и временный файл до
// переименования.
func TestSaveUnderSudoGivesFilesAway(t *testing.T) {
	calls := fakeSudo(t, sudoUser, nil)
	root := t.TempDir()
	dir := filepath.Join(root, "a", AppDir)
	path := filepath.Join(dir, FileName)

	if err := Save(path, sampleFile()); err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(root, "a"), dir, path + ".tmp"}
	if !slices.Equal(*calls, want) {
		t.Errorf("chown: %q, ожидалось %q", *calls, want)
	}
}

func TestSaveUnderSudoChownFails(t *testing.T) {
	fakeSudo(t, sudoUser, errors.New("нельзя"))
	dir := t.TempDir()

	if err := Save(filepath.Join(dir, FileName), sampleFile()); err == nil {
		t.Fatal("ошибка chown проглочена")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("после ошибки в каталоге остались файлы: %v", entries)
	}
}
