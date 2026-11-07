package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/FreedomCats/csqtt-cli/internal/config"
)

// testLink — выдуманная ссылка панели сервера: адрес из документационной
// сети, пароль и хеши — тестовые значения.
const testLink = "csqtt://connect?v=2&host=203.0.113.20&peer=46001&password=" + "link-password-0123" +
	"&hashes=" + testHashA + "+" + testHashB

func TestProfileAddByFields(t *testing.T) {
	te := newTestEnv(t)
	te.answer(testPassword, testPassword, testHashA+", https://vk.com/call/join/"+testHashB)
	code, stdout, stderr := te.run("profile", "add", "home", "-peer", "203.0.113.10", "-workers", "30", "-datapath", "socks")
	if code != exitOK {
		t.Fatalf("код %d, stderr:\n%s", code, stderr)
	}
	noSecrets(t, "stdout", stdout)
	noSecrets(t, "stderr", stderr)
	if !strings.Contains(stdout, "хешей:  2") {
		t.Errorf("нет числа хешей:\n%s", stdout)
	}
	// Пароль спросили дважды (повтор), хеши — один раз.
	if len(te.asked) != 3 || !strings.Contains(te.asked[1], "Повторите") {
		t.Errorf("вопросы: %q", te.asked)
	}

	file, err := config.Load(te.configPath())
	if err != nil {
		t.Fatalf("файл настроек не читается: %v", err)
	}
	p, err := file.Profile("")
	if err != nil {
		t.Fatal(err)
	}
	if p.Peer != "203.0.113.10" || p.Workers != 30 || p.Datapath != "socks" || p.DeviceID == "" {
		t.Errorf("профиль записан неверно: %+v", p)
	}
	// В файле — только ссылки; в хранилище — разобранные хеши.
	raw, _ := os.ReadFile(te.configPath())
	noSecrets(t, "profiles.toml", string(raw))
	if got, _ := te.store.Get(p.PasswordRef); got != testPassword {
		t.Errorf("пароль в хранилище: %q", got)
	}
	if got, _ := te.store.Get(p.VkHashesRef); got != testHashA+","+testHashB {
		t.Errorf("хеши в хранилище: %q", got)
	}
}

func TestProfileAddFromLink(t *testing.T) {
	te := newTestEnv(t)
	te.answer(testLink)
	code, stdout, stderr := te.run("profile", "add", "work", "-link", "-default")
	if code != exitOK {
		t.Fatalf("код %d, stderr:\n%s", code, stderr)
	}
	for _, text := range []string{stdout, stderr} {
		noSecrets(t, "вывод", text)
		if strings.Contains(text, "link-password") || strings.Contains(text, "csqtt://") {
			t.Errorf("в выводе ссылка или её пароль:\n%s", text)
		}
	}
	if len(te.asked) != 1 || !strings.Contains(te.asked[0], "не отображается") {
		t.Errorf("вопросы: %q", te.asked)
	}

	file, err := config.Load(te.configPath())
	if err != nil {
		t.Fatal(err)
	}
	p := file.Profiles["work"]
	if p == nil || p.Peer != "203.0.113.20:46001" || file.DefaultProfile != "work" {
		t.Fatalf("профиль из ссылки: %+v, default %q", p, file.DefaultProfile)
	}
	if got, _ := te.store.Get(p.PasswordRef); got != "link-password-0123" {
		t.Errorf("пароль из ссылки: %q", got)
	}
	if got, _ := te.store.Get(p.VkHashesRef); got != testHashA+","+testHashB {
		t.Errorf("хеши из ссылки: %q", got)
	}
}

// Ссылка старого вида — без хешей: их спрашивают отдельно.
func TestProfileAddFromLinkWithoutHashes(t *testing.T) {
	te := newTestEnv(t)
	te.answer("csqtt://old-password-1@203.0.113.21:46000", testHashC)
	code, _, stderr := te.run("profile", "add", "old", "-link")
	if code != exitOK {
		t.Fatalf("код %d, stderr:\n%s", code, stderr)
	}
	if len(te.asked) != 2 || !strings.Contains(te.asked[1], "Хеши") {
		t.Errorf("вопросы: %q", te.asked)
	}
}

// Негодная ссылка — отказ, и ссылка в сообщении не повторяется.
func TestProfileAddBadLinkNotEchoed(t *testing.T) {
	te := newTestEnv(t)
	bad := "csqtt://connect?v=3&host=203.0.113.20&peer=46001&password=bad-link-secret"
	te.answer(bad)
	code, stdout, stderr := te.run("profile", "add", "work", "-link")
	if code != exitError {
		t.Fatalf("код %d", code)
	}
	if strings.Contains(stdout+stderr, "bad-link-secret") || strings.Contains(stderr, "csqtt://connect") {
		t.Errorf("ссылка в выводе:\n%s%s", stdout, stderr)
	}
	if _, err := os.Stat(te.configPath()); err == nil {
		t.Error("файл настроек записан, хотя ссылка негодная")
	}
}

// Секрет в командной строке — отказ без записи и без повтора значения.
func TestProfileRefusesSecretArgs(t *testing.T) {
	cases := map[string][]string{
		"-password=значение":       {"profile", "set", "home", "-password=" + testPassword},
		"--hashes=значение":        {"profile", "set", "home", "--hashes=" + testHashA},
		"-link=значение":           {"profile", "add", "home", "-link=" + testHashA},
		"set -link=значение":       {"profile", "set", "home", "-link=" + testHashA},
		"set: ссылка аргументом":   {"profile", "set", "home", "-link", testLink},
		"значение после -password": {"profile", "set", "home", "-password", testPassword},
		"ссылка аргументом":        {"profile", "add", "home", testLink},
		"ссылка где угодно":        {"connect", testLink},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			te := newTestEnv(t)
			if args[1] == "set" {
				te.addHome()
			}
			before, _ := os.ReadFile(te.configPath())
			code, stdout, stderr := te.run(args...)
			if code != exitUsage {
				t.Errorf("код %d, ожидался %d; stderr:\n%s", code, exitUsage, stderr)
			}
			noSecrets(t, "вывод", stdout+stderr)
			if strings.Contains(stdout+stderr, "link-password") {
				t.Errorf("ссылка в выводе:\n%s", stderr)
			}
			if len(te.asked) > 3 {
				t.Errorf("секрет спросили, хотя команда отклонена: %q", te.asked)
			}
			after, _ := os.ReadFile(te.configPath())
			if string(before) != string(after) {
				t.Error("файл настроек изменился")
			}
		})
	}
}

func TestProfileAddUsage(t *testing.T) {
	cases := map[string]struct {
		args []string
		want string
	}{
		"без имени":         {[]string{"profile", "add"}, "не названо имя"},
		"без адреса":        {[]string{"profile", "add", "home"}, "-link"},
		"и ссылка, и адрес": {[]string{"profile", "add", "home", "-link", "-peer", "203.0.113.10"}, "не оба"},
		"имя кириллицей":    {[]string{"profile", "add", "дом", "-peer", "203.0.113.10"}, "латинские"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			te := newTestEnv(t)
			code, _, stderr := te.run(tc.args...)
			if code != exitUsage || !strings.Contains(stderr, tc.want) {
				t.Errorf("код %d, stderr:\n%s", code, stderr)
			}
			if len(te.asked) != 0 {
				t.Errorf("спросили секреты: %q", te.asked)
			}
		})
	}
}

// Флаги проверяются до вопросов о секретах.
func TestProfileAddChecksFlagsBeforeAsking(t *testing.T) {
	te := newTestEnv(t)
	code, _, stderr := te.run("profile", "add", "home", "-peer", "203.0.113.10", "-obfs", "аудио")
	if code != exitError || !strings.Contains(stderr, "obfs") {
		t.Errorf("код %d, stderr:\n%s", code, stderr)
	}
	if len(te.asked) != 0 {
		t.Errorf("спросили секреты: %q", te.asked)
	}
}

func TestProfileAddExisting(t *testing.T) {
	te := newTestEnv(t)
	te.addHome()
	code, _, stderr := te.run("profile", "add", "home", "-peer", "203.0.113.11")
	if code != exitError || !strings.Contains(stderr, "уже есть") || !strings.Contains(stderr, "profile set home") {
		t.Errorf("код %d, stderr:\n%s", code, stderr)
	}
}

func TestProfileAddPasswordMismatch(t *testing.T) {
	te := newTestEnv(t)
	te.answer(testPassword, "другой")
	code, _, stderr := te.run("profile", "add", "home", "-peer", "203.0.113.10")
	if code != exitError || !strings.Contains(stderr, "не совпали") {
		t.Errorf("код %d, stderr:\n%s", code, stderr)
	}
	noSecrets(t, "stderr", stderr)
}

func TestProfileAddTooManyHashes(t *testing.T) {
	te := newTestEnv(t)
	te.answer(testPassword, testPassword, "h1aaaaaa h2aaaaaa h3aaaaaa h4aaaaaa h5aaaaaa h6aaaaaa h7aaaaaa")
	code, _, stderr := te.run("profile", "add", "home", "-peer", "203.0.113.10")
	if code != exitError || !strings.Contains(stderr, "не больше 6") {
		t.Errorf("код %d, stderr:\n%s", code, stderr)
	}
}

// Из конвейера секреты читаются строками stdin, пароль — один раз.
func TestProfileAddFromPipe(t *testing.T) {
	te := newTestEnv(t)
	te.pipe = true
	te.stdin = strings.NewReader(testPassword + "\n" + testHashA + "\n")
	code, stdout, stderr := te.run("profile", "add", "home", "-peer", "203.0.113.10")
	if code != exitOK {
		t.Fatalf("код %d, stderr:\n%s", code, stderr)
	}
	noSecrets(t, "вывод", stdout+stderr)
	file, _ := config.Load(te.configPath())
	if got, _ := te.store.Get(file.Profiles["home"].VkHashesRef); got != testHashA {
		t.Errorf("хеши: %q", got)
	}
}

func TestProfileSet(t *testing.T) {
	te := newTestEnv(t)
	te.addHome()
	before, _ := config.Load(te.configPath())
	id := before.Profiles["home"].DeviceID

	te.asked = nil
	te.answer(testHashC)
	code, stdout, stderr := te.run("profile", "set", "home", "-workers", "10", "-hashes", "-socks-listen", "127.0.0.1:1081")
	if code != exitOK {
		t.Fatalf("код %d, stderr:\n%s", code, stderr)
	}
	noSecrets(t, "вывод", stdout+stderr)
	if len(te.asked) != 1 {
		t.Errorf("вопросы: %q — пароль спрашивать не просили", te.asked)
	}
	file, _ := config.Load(te.configPath())
	p := file.Profiles["home"]
	if p.Workers != 10 || p.Socks.Listen != "127.0.0.1:1081" || p.Peer != "203.0.113.10" || p.DeviceID != id {
		t.Errorf("профиль после set: %+v", p)
	}
	if got, _ := te.store.Get(p.VkHashesRef); got != testHashC {
		t.Errorf("хеши: %q", got)
	}
}

func TestProfileSetPassword(t *testing.T) {
	te := newTestEnv(t)
	te.addHome()
	te.answer("новый-пароль", "новый-пароль")
	if code, _, stderr := te.run("profile", "set", "home", "-password"); code != exitOK {
		t.Fatalf("код %d, stderr:\n%s", code, stderr)
	}
	file, _ := config.Load(te.configPath())
	if got, _ := te.store.Get(file.Profiles["home"].PasswordRef); got != "новый-пароль" {
		t.Errorf("пароль: %q", got)
	}
}

func TestProfileSetPeerPasswordSave(t *testing.T) {
	for _, failSave := range []bool{true, false} {
		name := "success"
		if failSave {
			name = "save failure"
		}
		t.Run(name, func(t *testing.T) {
			te := newTestEnv(t)
			te.addHome("-datapath", "socks")
			before := loadTestFile(t, te)
			old := before.Profiles["home"]
			rawBefore, err := os.ReadFile(te.configPath())
			if err != nil {
				t.Fatal("cannot read original profile file")
			}
			const password = "replacement-password-test-only"
			const peer = "198.51.100.20:46001"
			saveErr := errors.New("injected profile save failure")
			var attempted *config.File
			if failSave {
				te.saveProfile = func(_ string, file *config.File) error {
					attempted = file
					p := file.Profiles["home"]
					if got, err := te.store.Get(p.PasswordRef); err != nil || got != password {
						t.Error("save failure was not injected after the password write")
					}
					return saveErr
				}
			}
			te.asked = nil
			te.answer(password, password)
			code, stdout, stderr := te.run("profile", "set", "home", "-peer", peer, "-password", "-default")
			noSecrets(t, "stdout", stdout, password)
			noSecrets(t, "stderr", stderr, password)
			noSecrets(t, "prompts", strings.Join(te.asked, "\n"), password)
			if len(te.asked) != 2 || len(te.answers) != 0 {
				t.Error("peer/password update did not ask only for password confirmation")
			}
			file := loadTestFile(t, te)
			p := file.Profiles["home"]
			wantPeer, wantPassword := peer, password
			if failSave {
				if code != exitError || !strings.Contains(stderr, saveErr.Error()) {
					t.Error("profile set did not report the injected save failure")
				}
				if stdout != "" {
					t.Error("failed profile set printed success output")
				}
				if attempted == nil || !reflect.DeepEqual(attempted, before) {
					t.Error("failed profile set did not restore the in-memory file")
				}
				rawAfter, err := os.ReadFile(te.configPath())
				if err != nil || !bytes.Equal(rawBefore, rawAfter) {
					t.Error("failed profile set changed the persisted file")
				}
				wantPeer, wantPassword = old.Peer, testPassword
			} else {
				if code != exitOK || stderr != "" {
					t.Error("profile set failed")
				}
				for _, want := range []string{"Профиль \"home\" изменён:", "сервер: " + peer, "пароль: записан ("} {
					if !strings.Contains(stdout, want) {
						t.Error("profile set lost its success output")
					}
				}
				if file.DefaultProfile != "home" {
					t.Error("profile set did not save the requested default")
				}
			}
			if p.Peer != wantPeer || p.DeviceID != old.DeviceID || p.PasswordRef != old.PasswordRef || p.VkHashesRef != old.VkHashesRef {
				t.Error("profile set persisted incorrect profile fields or changed stable references")
			}
			if got, err := te.store.Get(p.PasswordRef); err != nil || got != wantPassword {
				t.Error("persisted peer and password are inconsistent")
			}
		})
	}
}

func TestStoreProfileSaveRollback(t *testing.T) {
	for _, tc := range []struct {
		name        string
		existing    bool
		dropVKToken bool
	}{
		{name: "overwrite all secrets", existing: true},
		{name: "create all secrets"},
		{name: "defer token deletion", existing: true, dropVKToken: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			te := newTestEnv(t)
			before := &config.File{}
			if tc.existing {
				te.addHome()
				before = loadTestFile(t, te)
				ref, err := te.store.Put("home.vk_token", serverToken)
				if err != nil {
					t.Fatal("cannot prepare server token")
				}
				before.Profiles["home"].VkTokenRef = ref
				if err := config.Save(te.configPath(), before); err != nil {
					t.Fatal("cannot prepare profile file")
				}
			}
			rawBefore, readErr := os.ReadFile(te.configPath())
			if tc.existing && readErr != nil || !tc.existing && !os.IsNotExist(readErr) {
				t.Fatal("unexpected original profile file state")
			}
			secretsBefore := maps.Clone(te.store.values)
			file := &config.File{}
			p := &config.Profile{Name: "home", Peer: "198.51.100.20:46001"}
			original := *p
			if tc.existing {
				file = loadTestFile(t, te)
				p = file.Profiles["home"]
				original = *p
				p.Peer = "198.51.100.20:46001"
			}
			file.DefaultProfile = "home"
			secrets := profileSecrets{password: "replacement-password-test-only", hashes: []string{newHash}, vkToken: "replacement-token-test-only"}
			if tc.dropVKToken {
				secrets.vkToken, secrets.dropVKToken = "", true
			}
			saveErr := errors.New("injected profile save failure")
			called := false
			te.saveProfile = func(_ string, file *config.File) error {
				called = true
				if got, err := te.store.Get(p.PasswordRef); err != nil || got != secrets.password {
					t.Error("save did not follow the password write")
				}
				if got, err := te.store.Get(p.VkHashesRef); err != nil || got != newHash {
					t.Error("save did not follow the hashes write")
				}
				if tc.dropVKToken {
					if p.VkTokenRef != "" || te.store.deletes != 0 {
						t.Error("server token was not retained until file save")
					}
				} else if got, err := te.store.Get(p.VkTokenRef); err != nil || got != secrets.vkToken {
					t.Error("save did not follow the server token write")
				}
				file.Profiles["home"].DeviceID = "0123456789abcdef"
				return saveErr
			}
			var stdout, stderr bytes.Buffer
			err := storeProfile(te.env(&stdout, &stderr), te.configPath(), file, p, tc.existing, secrets)
			if !called || !errors.Is(err, saveErr) {
				t.Fatal("storeProfile did not return the injected save error")
			}
			noSecrets(t, "rollback error and output", err.Error()+stdout.String()+stderr.String(), secrets.password, secrets.vkToken, newHash, serverToken)
			if stdout.Len() != 0 || stderr.Len() != 0 {
				t.Error("failed storeProfile printed output")
			}
			if !reflect.DeepEqual(file, before) || !reflect.DeepEqual(*p, original) {
				t.Error("rollback did not restore file/profile state")
			}
			if !maps.Equal(te.store.values, secretsBefore) {
				t.Error("rollback did not restore all previous secret records")
			}
			rawAfter, err := os.ReadFile(te.configPath())
			if tc.existing {
				if err != nil || !bytes.Equal(rawBefore, rawAfter) {
					t.Error("rollback changed the original profile file")
				}
			} else if !os.IsNotExist(err) {
				t.Error("failed profile creation left a profile file")
			}
		})
	}
}

func TestStoreProfileRollbackErrorsAreSecretFree(t *testing.T) {
	for _, existing := range []bool{true, false} {
		name := "delete new records"
		if existing {
			name = "restore old records"
		}
		t.Run(name, func(t *testing.T) {
			te := newTestEnv(t)
			before := &config.File{}
			file := &config.File{}
			p := &config.Profile{Name: "home", Peer: "198.51.100.20:46001"}
			if existing {
				te.addHome()
				before = loadTestFile(t, te)
				file = loadTestFile(t, te)
				p = file.Profiles["home"]
			}
			original := *p
			secrets := profileSecrets{password: "replacement-password-test-only", hashes: []string{newHash}, vkToken: "replacement-token-test-only"}
			saveErr := errors.New("injected profile save failure")
			puts, deletes := 0, 0
			te.saveProfile = func(string, *config.File) error {
				puts, deletes = te.store.puts, te.store.deletes
				// Deliberately hostile errors must not escape rollback diagnostics.
				storeErr := errors.New(strings.Join([]string{testPassword, testHashA, secrets.password, newHash, secrets.vkToken}, " "))
				te.store.putErr, te.store.deleteErr = storeErr, storeErr
				return saveErr
			}
			var stdout, stderr bytes.Buffer
			err := storeProfile(te.env(&stdout, &stderr), te.configPath(), file, p, existing, secrets)
			if !errors.Is(err, saveErr) || err == saveErr {
				t.Fatal("rollback did not retain the save error and add failure context")
			}
			noSecrets(t, "rollback error and output", err.Error()+stdout.String()+stderr.String(), secrets.password, newHash, secrets.vkToken)
			if existing {
				if te.store.puts-puts != 2 || te.store.deletes-deletes != 1 {
					t.Error("rollback stopped before attempting every secret restoration")
				}
			} else if te.store.deletes-deletes != 3 {
				t.Error("rollback stopped before attempting every new secret deletion")
			}
			if !reflect.DeepEqual(file, before) || !reflect.DeepEqual(*p, original) {
				t.Error("rollback errors prevented file/profile restoration")
			}
		})
	}
}

func TestStoreProfileUnreadableBackupDoesNotWrite(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"locked", config.ErrKeyringLocked},
		{"unavailable", errors.New("store unavailable")},
		{"damaged value", errors.New("damaged secret")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			te := newTestEnv(t)
			te.addHome()
			file := loadTestFile(t, te)
			before := loadTestFile(t, te)
			p := file.Profiles["home"]
			p.Peer = "198.51.100.20:46001"
			secretsBefore := maps.Clone(te.store.values)
			puts := te.store.puts
			te.store.getErr = tc.err
			te.saveProfile = func(string, *config.File) error {
				t.Error("save was called without a readable secret backup")
				return nil
			}
			var stdout, stderr bytes.Buffer
			err := storeProfile(te.env(&stdout, &stderr), te.configPath(), file, p, true, profileSecrets{password: "replacement-password-test-only"})
			if !errors.Is(err, tc.err) {
				t.Fatal("storeProfile did not report the unreadable backup")
			}
			noSecrets(t, "backup error and output", err.Error()+stdout.String()+stderr.String(), "replacement-password-test-only")
			if te.store.puts != puts || !maps.Equal(te.store.values, secretsBefore) || !reflect.DeepEqual(file, before) {
				t.Error("unreadable backup changed the profile or secrets")
			}
		})
	}
}

func TestStoreProfileDamagedIndexDoesNotWrite(t *testing.T) {
	te := newTestEnv(t)
	te.addHome()
	file := loadTestFile(t, te)
	before := loadTestFile(t, te)
	values := maps.Clone(te.store.values)
	puts := te.store.puts
	if err := os.WriteFile(te.store.path, []byte("{"), 0o600); err != nil {
		t.Fatal("cannot prepare damaged index")
	}
	te.saveProfile = func(string, *config.File) error {
		t.Error("save was called with a damaged secret index")
		return nil
	}
	var stdout, stderr bytes.Buffer
	err := storeProfile(te.env(&stdout, &stderr), te.configPath(), file, file.Profiles["home"], true, profileSecrets{password: sealedPassword})
	if err == nil {
		t.Fatal("damaged index did not block the update")
	}
	noSecrets(t, "damaged index error and output", err.Error()+stdout.String()+stderr.String(), sealedPassword)
	if te.store.puts != puts || !maps.Equal(te.store.values, values) || !reflect.DeepEqual(file, before) {
		t.Error("damaged index allowed a profile or secret write")
	}
}

func TestStoreProfileMissingIndexedValues(t *testing.T) {
	for _, failSave := range []bool{false, true} {
		name := "success"
		if failSave {
			name = "save failure"
		}
		t.Run(name, func(t *testing.T) {
			te := newTestEnv(t)
			te.addHome()
			file := loadTestFile(t, te)
			p := file.Profiles["home"]
			ref, err := te.store.Put("home.vk_token", serverToken)
			if err != nil {
				t.Fatal("cannot prepare indexed token")
			}
			p.VkTokenRef = ref
			if err := config.Save(te.configPath(), file); err != nil {
				t.Fatal("cannot prepare indexed references")
			}
			// Model removal from the keyring without removal of the file index.
			for _, ref := range []string{p.PasswordRef, p.VkHashesRef, p.VkTokenRef} {
				delete(te.store.values, ref)
			}
			indexBefore, err := os.ReadFile(te.store.path)
			if err != nil {
				t.Fatal("cannot read original secret index")
			}
			old := *p
			saveErr := errors.New("injected profile save failure")
			called := false
			if failSave {
				te.saveProfile = func(string, *config.File) error {
					called = true
					return saveErr
				}
			}
			secrets := profileSecrets{password: sealedPassword, hashes: []string{newHash}, vkToken: "replacement-token-test-only"}
			var stdout, stderr bytes.Buffer
			err = storeProfile(te.env(&stdout, &stderr), te.configPath(), file, p, true, secrets)
			if failSave {
				if !called || !errors.Is(err, saveErr) {
					t.Fatal("missing indexed values blocked replacement before save")
				}
				noSecrets(t, "rollback error", err.Error(), sealedPassword, newHash, secrets.vkToken)
				indexAfter, readErr := os.ReadFile(te.store.path)
				if readErr != nil || !equalSecretIndex(indexBefore, indexAfter) || !reflect.DeepEqual(*p, old) {
					t.Error("rollback lost indexed IDs or profile references")
				}
				for _, ref := range []string{old.PasswordRef, old.VkHashesRef, old.VkTokenRef} {
					if _, err := te.store.Get(ref); !errors.Is(err, os.ErrNotExist) {
						t.Error("rollback did not restore the absent indexed value")
					}
				}
			} else if err != nil {
				t.Fatal("storeProfile could not replace missing indexed values")
			}
			if !failSave {
				for _, secret := range []struct{ ref, value string }{
					{p.PasswordRef, secrets.password}, {p.VkHashesRef, newHash}, {p.VkTokenRef, secrets.vkToken},
				} {
					if got, err := te.store.Get(secret.ref); err != nil || got != secret.value {
						t.Error("replacement of an indexed value was not saved")
					}
				}
			}
			noSecrets(t, "output", stdout.String()+stderr.String(), sealedPassword, newHash, secrets.vkToken)
			if p.PasswordRef != old.PasswordRef || p.VkHashesRef != old.VkHashesRef || p.VkTokenRef != old.VkTokenRef {
				t.Error("replacement changed stable indexed references")
			}
		})
	}
}

func TestStoreProfileRollbackPreservesNamedDestinations(t *testing.T) {
	for _, withProfile := range []bool{false, true} {
		name := "no prior profile references"
		if withProfile {
			name = "references to other records"
		}
		t.Run(name, func(t *testing.T) {
			te := newTestEnv(t)
			store := config.NewSecretStoreFor(te.configPath())
			owner := &config.Profile{Name: "home", Peer: "203.0.113.10"}
			for _, secret := range []struct {
				kind, value string
				ref         *string
			}{
				{"password", testPassword, &owner.PasswordRef},
				{"vk_hashes", testHashA, &owner.VkHashesRef},
				{"vk_token", serverToken, &owner.VkTokenRef},
			} {
				ref, err := store.Put("home."+secret.kind, secret.value)
				if err != nil {
					t.Fatal("cannot prepare shared destination")
				}
				*secret.ref = ref
			}
			otherPath := filepath.Join(te.dir, "other.toml")
			if err := config.Save(otherPath, &config.File{Profiles: map[string]*config.Profile{"home": owner}}); err != nil {
				t.Fatal("cannot prepare other configuration")
			}
			otherBefore, err := os.ReadFile(otherPath)
			if err != nil {
				t.Fatal("cannot read other configuration")
			}
			file := &config.File{}
			p := &config.Profile{Name: "home", Peer: "198.51.100.20:46001"}
			var unrelatedRef string
			if withProfile {
				unrelatedRef, err = store.Put("other.password", sealedPassword)
				if err != nil {
					t.Fatal("cannot prepare unrelated record")
				}
				p.PasswordRef, p.VkHashesRef, p.VkTokenRef = unrelatedRef, unrelatedRef, unrelatedRef
				file.Profiles = map[string]*config.Profile{"home": p}
				if err := config.Save(te.configPath(), file); err != nil {
					t.Fatal("cannot prepare original profile")
				}
			}
			secrets := profileSecrets{password: "replacement-password-test-only", hashes: []string{newHash}, vkToken: "replacement-token-test-only"}
			saveErr := errors.New("injected profile save failure")
			called := false
			te.saveProfile = func(string, *config.File) error {
				called = true
				if got, err := store.Get(owner.PasswordRef); err != nil || got != secrets.password {
					t.Error("failure did not follow a write to the shared destination")
				}
				return saveErr
			}
			var stdout, stderr bytes.Buffer
			e := te.env(&stdout, &stderr)
			e.secretStore = func(string) config.SecretStore { return store }
			err = storeProfile(e, te.configPath(), file, p, withProfile, secrets)
			if !called || !errors.Is(err, saveErr) {
				t.Fatal("storeProfile did not reach the injected save failure")
			}
			noSecrets(t, "rollback error and output", err.Error()+stdout.String()+stderr.String(), secrets.password, newHash, secrets.vkToken, serverToken, sealedPassword)
			for _, secret := range []struct{ ref, value string }{
				{owner.PasswordRef, testPassword}, {owner.VkHashesRef, testHashA}, {owner.VkTokenRef, serverToken},
			} {
				if got, err := store.Get(secret.ref); err != nil || got != secret.value {
					t.Error("rollback damaged a shared destination or its stable reference")
				}
			}
			if withProfile {
				if got, err := store.Get(unrelatedRef); err != nil || got != sealedPassword {
					t.Error("rollback changed the unrelated profile reference's value")
				}
			}
			otherAfter, err := os.ReadFile(otherPath)
			if err != nil || !bytes.Equal(otherBefore, otherAfter) {
				t.Error("rollback changed the other configuration")
			}
		})
	}
}

func TestProfileSetReplacesMissingSecrets(t *testing.T) {
	for _, shared := range []bool{false, true} {
		for _, failSave := range []bool{false, true} {
			name := "missing destination"
			if shared {
				name = "shared destination with stale profile references"
			}
			if failSave {
				name += "/save failure"
			} else {
				name += "/success"
			}
			t.Run(name, func(t *testing.T) {
				te := newTestEnv(t)
				store := config.NewSecretStoreFor(te.configPath())
				passwordRef, err := store.Put("home.password", testPassword)
				if err != nil {
					t.Fatal("cannot prepare original password")
				}
				hashesRef, err := store.Put("home.vk_hashes", testHashA)
				if err != nil {
					t.Fatal("cannot prepare original hashes")
				}
				p := &config.Profile{Name: "home", Peer: "203.0.113.10", PasswordRef: passwordRef, VkHashesRef: hashesRef}
				file := &config.File{Profiles: map[string]*config.Profile{"home": p}}
				if shared {
					scheme, _, _ := strings.Cut(passwordRef, ":")
					p.PasswordRef, p.VkHashesRef = scheme+":missing-password", scheme+":missing-hashes"
				} else {
					for _, ref := range []string{p.PasswordRef, p.VkHashesRef} {
						if err := store.Delete(ref); err != nil {
							t.Fatal("cannot remove original secret")
						}
					}
				}
				if err := config.Save(te.configPath(), file); err != nil {
					t.Fatal("cannot prepare stale references")
				}
				before := loadTestFile(t, te)
				indexBefore, err := os.ReadFile(filepath.Join(te.dir, config.SecretsFileName))
				if err != nil {
					t.Fatal("cannot read original secret index")
				}
				saveErr := errors.New("injected profile save failure")
				called := false
				if failSave {
					te.saveProfile = func(_ string, file *config.File) error {
						called = true
						if got, err := store.Get(file.Profiles["home"].PasswordRef); err != nil || got != sealedPassword {
							t.Error("save failure did not follow replacement of a missing password")
						}
						return saveErr
					}
				}
				te.asked = nil
				te.answer(sealedPassword, sealedPassword, newHash)
				var out, errOut bytes.Buffer
				e := te.env(&out, &errOut)
				e.secretStore = func(string) config.SecretStore { return store }
				code := run(context.Background(), e, []string{"profile", "set", "home", "-peer", "198.51.100.20:46001", "-password", "-hashes", "-config", te.configPath()})
				stdout, stderr := out.String(), errOut.String()
				noSecrets(t, "stdout", stdout, sealedPassword, newHash)
				noSecrets(t, "stderr", stderr, sealedPassword, newHash)
				noSecrets(t, "prompts", strings.Join(te.asked, "\n"), sealedPassword, newHash)
				if failSave {
					if !called || code != exitError || !strings.Contains(stderr, saveErr.Error()) {
						t.Error("missing secrets blocked replacement before the save failure")
					}
					indexAfter, err := os.ReadFile(filepath.Join(te.dir, config.SecretsFileName))
					if !reflect.DeepEqual(loadTestFile(t, te), before) || err != nil || !equalSecretIndex(indexBefore, indexAfter) {
						t.Error("rollback changed the missing secrets or shared destination")
					}
					if shared {
						if got, err := store.Get(passwordRef); err != nil || got != testPassword {
							t.Error("rollback damaged the shared password destination")
						}
						if got, err := store.Get(hashesRef); err != nil || got != testHashA {
							t.Error("rollback damaged the shared hashes destination")
						}
					}
					return
				}
				if code != exitOK {
					t.Fatal("profile set could not replace missing secrets")
				}
				p = loadTestFile(t, te).Profiles["home"]
				if p.Peer != "198.51.100.20:46001" || p.DeviceID != before.Profiles["home"].DeviceID {
					t.Error("replacement of missing secrets changed preserved profile state")
				}
				if got, err := store.Get(p.PasswordRef); err != nil || got != sealedPassword {
					t.Error("replacement password was not saved")
				}
				if got, err := store.Get(p.VkHashesRef); err != nil || got != newHash {
					t.Error("replacement hashes were not saved")
				}
			})
		}
	}
}

func equalSecretIndex(before, after []byte) bool {
	var original, current any
	return json.Unmarshal(before, &original) == nil && json.Unmarshal(after, &current) == nil && reflect.DeepEqual(original, current)
}

func checkPasswordRejection(t *testing.T, text string) {
	t.Helper()
	for _, want := range []string{"password", "'|'", "server administrator", "without this character"} {
		if !strings.Contains(text, want) {
			t.Errorf("password rejection lacks %q:\n%s", want, text)
		}
	}
}

func noPasswordEcho(t *testing.T, text string, passwords ...string) {
	t.Helper()
	noSecrets(t, "output", text)
	for _, password := range passwords {
		if strings.Contains(text, password) {
			t.Errorf("output exposes the supplied password:\n%s", text)
		}
	}
}

func TestProfileManualPasswordValidation(t *testing.T) {
	routes := []struct {
		name string
		args []string
		link string
		set  bool
	}{
		{name: "add", args: []string{"profile", "add", "home", "-peer", "203.0.113.10"}},
		{name: "add after passwordless link", args: []string{"profile", "add", "home", "-link"}, link: sealedNoPasswordLink},
		{name: "set password", args: []string{"profile", "set", "home", "-password"}, set: true},
	}
	cases := []struct {
		name, password string
		reject         bool
	}{
		{"pipe", "fake|password", true},
		{"percent", "fake%password", false},
		{"literal escape text", "fake%7C%25%zzpassword", false},
	}
	for _, route := range routes {
		for _, pipe := range []bool{false, true} {
			for _, tc := range cases {
				mode := "interactive"
				if pipe {
					mode = "stdin"
				}
				t.Run(route.name+"/"+mode+"/"+tc.name, func(t *testing.T) {
					te := newTestEnv(t)
					if route.set {
						te.addHome()
					}
					before, err := os.ReadFile(te.configPath())
					if route.set && err != nil {
						t.Fatal(err)
					}
					secrets := maps.Clone(te.store.values)
					te.asked = nil
					var answers []string
					if route.link != "" {
						answers = append(answers, route.link)
					}
					answers = append(answers, tc.password)
					if !pipe {
						answers = append(answers, tc.password)
					}
					if !route.set && route.link == "" && !tc.reject {
						answers = append(answers, testHashA)
					}
					if pipe {
						te.pipe = true
						te.stdin = strings.NewReader(strings.Join(answers, "\n") + "\n")
					} else {
						te.answer(answers...)
					}

					code, stdout, stderr := te.run(route.args...)
					noPasswordEcho(t, stdout+stderr+strings.Join(te.asked, "\n"), tc.password)
					if tc.reject {
						if code != exitError {
							t.Fatalf("exit code %d; want %d; stderr:\n%s", code, exitError, stderr)
						}
						checkPasswordRejection(t, stderr)
						after, err := os.ReadFile(te.configPath())
						if route.set {
							if err != nil || string(after) != string(before) {
								t.Errorf("profile changed on rejection: %v", err)
							}
						} else if !os.IsNotExist(err) {
							t.Errorf("profile file created on rejection: %v", err)
						}
						if !maps.Equal(te.store.values, secrets) {
							t.Error("secrets changed on rejection")
						}
						return
					}
					if code != exitOK {
						t.Fatalf("exit code %d; stderr:\n%s", code, stderr)
					}
					p := loadTestFile(t, te).Profiles["home"]
					if got, err := te.store.Get(p.PasswordRef); err != nil || got != tc.password {
						t.Errorf("literal password changed: %q, error %v", got, err)
					}
				})
			}
		}
	}
}

func TestProfileAddLinkRejectsPipePassword(t *testing.T) {
	for _, input := range []string{
		strings.Replace(testLink, "link-password-0123", "fake%7Cpassword", 1),
		"csqtt://fake%7Cpassword@203.0.113.7:46000",
	} {
		te := newTestEnv(t)
		te.answer(input)
		code, stdout, stderr := te.run("profile", "add", "home", "-link")
		if code != exitError {
			t.Fatalf("exit code %d; stderr:\n%s", code, stderr)
		}
		checkPasswordRejection(t, stderr)
		noPasswordEcho(t, stdout+stderr+strings.Join(te.asked, "\n"), "fake|password", "fake%7Cpassword", input)
		if _, err := os.Stat(te.configPath()); !os.IsNotExist(err) {
			t.Errorf("profile file created on rejection: %v", err)
		}
		if len(te.store.values) != 0 {
			t.Error("secrets written on rejection")
		}
	}
}

func TestProfileSetUsage(t *testing.T) {
	te := newTestEnv(t)
	te.addHome()
	if code, _, stderr := te.run("profile", "set", "home"); code != exitUsage || !strings.Contains(stderr, "не названо, что менять") {
		t.Errorf("set без флагов: код %d, stderr:\n%s", code, stderr)
	}
	if code, _, stderr := te.run("profile", "set", "work", "-workers", "10"); code != exitError ||
		!strings.Contains(stderr, "в файле есть: home") || !strings.Contains(stderr, "profile add <имя>") {
		t.Errorf("set чужого профиля: код %d, stderr:\n%s", code, stderr)
	}
}

// Значение после булева -password или -hashes встаёт на место имени профиля.
// Такого профиля нет, и сообщение об этом имени не повторяет: это сам секрет.
func TestProfileSetSecretAsNameNotEchoed(t *testing.T) {
	te := newTestEnv(t)
	te.addHome()
	for _, args := range [][]string{
		{"profile", "set", "-password", testPassword},
		{"profile", "set", "-hashes", testHashA},
		{"profile", "set", "-workers", "10", "-hashes", testHashB},
	} {
		te.asked = nil
		code, stdout, stderr := te.run(args...)
		if code != exitError || !strings.Contains(stderr, "такого профиля нет") || !strings.Contains(stderr, "скрытый ввод") {
			t.Errorf("%q: код %d, stderr:\n%s", args, code, stderr)
		}
		noSecrets(t, "вывод", stdout+stderr)
		if len(te.asked) != 0 {
			t.Errorf("%q: спрошено %q, хотя профиля нет", args, te.asked)
		}
	}
}

// Имя ищется как названо: пустое не означает профиль по умолчанию, а пробелы
// не обрезаются. Иначе set падал на пустом профиле, а rm сообщал об успехе,
// ничего не удалив.
func TestProfileExactName(t *testing.T) {
	te := newTestEnv(t)
	te.addHome("-default")
	before, err := os.ReadFile(te.configPath())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"", " home", "home "} {
		for _, args := range [][]string{
			{"profile", "set", name, "-workers", "10"},
			{"profile", "show", name},
			{"profile", "rm", name, "-y"},
		} {
			code, stdout, stderr := te.run(args...)
			if code != exitError || !strings.Contains(stderr, "такого профиля нет; в файле есть: home") {
				t.Errorf("%q: код %d, stdout:\n%s\nstderr:\n%s", args, code, stdout, stderr)
			}
		}
	}
	after, err := os.ReadFile(te.configPath())
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Errorf("файл настроек изменён:\n%s", after)
	}
	if len(te.store.values) != 2 {
		t.Errorf("секреты тронуты: %d", len(te.store.values))
	}
}

func TestProfileShowHidesSecrets(t *testing.T) {
	te := newTestEnv(t)
	te.addHome("-workers", "100")
	code, stdout, stderr := te.run("profile", "show", "home")
	if code != exitOK {
		t.Fatalf("код %d, stderr:\n%s", code, stderr)
	}
	noSecrets(t, "show", stdout+stderr)
	for _, want := range []string{
		"203.0.113.10:46000",
		"tun", "(умолчание)",
		"password", "задан",
		"vk_hashes", "3 (",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("в show нет %q:\n%s", want, stdout)
		}
	}
	// obfs не задан — показано умолчание ядра 2.5.0, а не прежнее audio.
	if !regexp.MustCompile(`obfs\s+unified\s+\(умолчание\)`).MatchString(stdout) {
		t.Errorf("в show нет obfs с умолчанием unified:\n%s", stdout)
	}
	// Предела на хеш у ядра нет: 100 воркеров на трёх хешах — без предупреждения.
	if strings.Contains(stdout, "запустит") {
		t.Errorf("лишнее предупреждение о воркерах:\n%s", stdout)
	}
}

// Секрет не читается (связка заперта) — show говорит об этом, не падая.
func TestProfileShowUnreadableSecret(t *testing.T) {
	te := newTestEnv(t)
	te.addHome()
	te.store.getErr = config.ErrKeyringLocked
	code, stdout, _ := te.run("profile", "show", "home")
	if code != exitOK || strings.Count(stdout, "не чита") != 2 {
		t.Errorf("код %d:\n%s", code, stdout)
	}
}

func TestProfileList(t *testing.T) {
	te := newTestEnv(t)
	if code, _, stderr := te.run("profile", "list"); code != exitError || !strings.Contains(stderr, "файла настроек нет") {
		t.Errorf("list без файла: код %d, stderr:\n%s", code, stderr)
	}
	te.addHome()
	te.answer(testLink)
	if code, _, stderr := te.run("profile", "add", "work", "-link", "-datapath", "socks", "-default"); code != exitOK {
		t.Fatalf("add work: %s", stderr)
	}
	code, stdout, _ := te.run("profile", "list")
	if code != exitOK {
		t.Fatalf("код %d", code)
	}
	lines := strings.Split(stdout, "\n")
	if !strings.HasPrefix(lines[0], "  home") || !strings.HasPrefix(lines[1], "* work") ||
		!strings.Contains(lines[0], "203.0.113.10:46000") || !strings.Contains(lines[1], "socks") {
		t.Errorf("список:\n%s", stdout)
	}
	noSecrets(t, "list", stdout)
}

func TestProfileRm(t *testing.T) {
	te := newTestEnv(t)
	te.addHome("-default")

	te.stdin = strings.NewReader("n\n")
	if code, stdout, _ := te.run("profile", "rm", "home"); code != exitOK || !strings.Contains(stdout, "Отменено") {
		t.Fatalf("отказ: код %d:\n%s", code, stdout)
	}
	if len(te.store.values) != 2 {
		t.Fatalf("секреты удалены при отказе: %v", te.store.values)
	}

	te.stdin = strings.NewReader("да\n")
	code, stdout, stderr := te.run("profile", "rm", "home")
	if code != exitOK {
		t.Fatalf("код %d, stderr:\n%s", code, stderr)
	}
	if len(te.store.values) != 0 {
		t.Errorf("секреты остались: %d", len(te.store.values))
	}
	if !strings.Contains(stdout, "умолчания нет") {
		t.Errorf("про умолчание не сказано:\n%s", stdout)
	}
	file, _ := config.Load(te.configPath())
	if len(file.Profiles) != 0 || file.DefaultProfile != "" {
		t.Errorf("файл после rm: %+v", file)
	}
}

func TestProfileRmWithoutConfirmation(t *testing.T) {
	te := newTestEnv(t)
	te.addHome()
	if code, _, stderr := te.run("profile", "rm", "home", "-y"); code != exitOK {
		t.Fatalf("код %d, stderr:\n%s", code, stderr)
	}
	if code, _, stderr := te.run("profile", "rm", "home", "-y"); code != exitError || !strings.Contains(stderr, "нет") {
		t.Errorf("повторное удаление: код %d, stderr:\n%s", code, stderr)
	}
}

// -kill-switch=false записывает kill_switch = false, -kill-switch — true;
// без флага поле не трогается. show показывает значение, а пропуск — как
// умолчание true.
func TestProfileSetKillSwitch(t *testing.T) {
	te := newTestEnv(t)
	te.addHome()
	showKillSwitch := func() string {
		t.Helper()
		code, stdout, stderr := te.run("profile", "show", "home")
		if code != exitOK {
			t.Fatalf("show: код %d, stderr:\n%s", code, stderr)
		}
		for _, line := range strings.Split(stdout, "\n") {
			if strings.Contains(line, "kill_switch") {
				return strings.Join(strings.Fields(line), " ")
			}
		}
		t.Fatalf("в show нет kill_switch:\n%s", stdout)
		return ""
	}
	if got := showKillSwitch(); !strings.Contains(got, "true (умолчание)") {
		t.Errorf("show без поля: %q", got)
	}

	for _, tc := range []struct {
		args []string
		want *bool
		show string
	}{
		{[]string{"-kill-switch=false"}, boolPtr(false), "kill_switch false"},
		{[]string{"-workers", "10"}, boolPtr(false), "kill_switch false"},
		{[]string{"-kill-switch"}, boolPtr(true), "kill_switch true"},
	} {
		if code, _, stderr := te.run(append([]string{"profile", "set", "home"}, tc.args...)...); code != exitOK {
			t.Fatalf("%v: код %d, stderr:\n%s", tc.args, code, stderr)
		}
		file, _ := config.Load(te.configPath())
		if got := file.Profiles["home"].KillSwitch; got == nil || *got != *tc.want {
			t.Errorf("%v: kill_switch %v, ждали %v", tc.args, got, *tc.want)
		}
		if got := showKillSwitch(); got != tc.show {
			t.Errorf("%v: show %q, ждали %q", tc.args, got, tc.show)
		}
	}
}

// -routing-template записывает секцию routing с template = true,
// -routing-template=false — false; без флага секции нет вовсе. show
// показывает значение, а пропуск — как умолчание false.
func TestProfileSetRoutingTemplate(t *testing.T) {
	te := newTestEnv(t)
	te.addHome()
	showLine := func() string {
		t.Helper()
		code, stdout, stderr := te.run("profile", "show", "home")
		if code != exitOK {
			t.Fatalf("show: код %d, stderr:\n%s", code, stderr)
		}
		for _, line := range strings.Split(stdout, "\n") {
			if strings.Contains(line, "routing.template") {
				return strings.Join(strings.Fields(line), " ")
			}
		}
		t.Fatalf("в show нет routing.template:\n%s", stdout)
		return ""
	}
	if raw, _ := os.ReadFile(te.configPath()); strings.Contains(string(raw), "routing") {
		t.Errorf("новый профиль без флага записан с секцией routing:\n%s", raw)
	}
	if got := showLine(); got != "routing.template false (умолчание)" {
		t.Errorf("show без секции: %q", got)
	}

	for _, tc := range []struct {
		args []string
		want bool
		show string
	}{
		{[]string{"-routing-template"}, true, "routing.template true"},
		{[]string{"-workers", "10"}, true, "routing.template true"},
		{[]string{"-routing-template=false"}, false, "routing.template false"},
	} {
		if code, _, stderr := te.run(append([]string{"profile", "set", "home"}, tc.args...)...); code != exitOK {
			t.Fatalf("%v: код %d, stderr:\n%s", tc.args, code, stderr)
		}
		file, _ := config.Load(te.configPath())
		if got := file.Profiles["home"].Routing.Template; got == nil || *got != tc.want {
			t.Errorf("%v: routing.template %v, ждали %v", tc.args, got, tc.want)
		}
		if got := showLine(); got != tc.show {
			t.Errorf("%v: show %q, ждали %q", tc.args, got, tc.show)
		}
	}
}

// profile add принимает тот же флаг.
func TestProfileAddRoutingTemplate(t *testing.T) {
	te := newTestEnv(t)
	te.addHome("-routing-template")
	file, _ := config.Load(te.configPath())
	if p := file.Profiles["home"]; !p.RoutingTemplateOn() {
		t.Errorf("routing.template после add: %v", p.Routing.Template)
	}
}

// -routing-server — так же, отдельно от template: один флаг другой ключ не
// трогает. profile add принимает и его.
func TestProfileSetRoutingServer(t *testing.T) {
	te := newTestEnv(t)
	te.addHome()
	showLines := func() (template, server string) {
		t.Helper()
		code, stdout, stderr := te.run("profile", "show", "home")
		if code != exitOK {
			t.Fatalf("show: код %d, stderr:\n%s", code, stderr)
		}
		for _, line := range strings.Split(stdout, "\n") {
			switch fields := strings.Join(strings.Fields(line), " "); {
			case strings.HasPrefix(fields, "routing.template "):
				template = fields
			case strings.HasPrefix(fields, "routing.server "):
				server = fields
			}
		}
		return template, server
	}
	if _, got := showLines(); got != "routing.server false (умолчание)" {
		t.Errorf("show без секции: %q", got)
	}

	for _, tc := range []struct {
		args     []string
		want     bool
		template string
		server   string
	}{
		{[]string{"-routing-server"}, true, "routing.template false (умолчание)", "routing.server true"},
		{[]string{"-routing-template"}, true, "routing.template true", "routing.server true"},
		{[]string{"-routing-server=false"}, false, "routing.template true", "routing.server false"},
	} {
		if code, _, stderr := te.run(append([]string{"profile", "set", "home"}, tc.args...)...); code != exitOK {
			t.Fatalf("%v: код %d, stderr:\n%s", tc.args, code, stderr)
		}
		file, _ := config.Load(te.configPath())
		if got := file.Profiles["home"].Routing.Server; got == nil || *got != tc.want {
			t.Errorf("%v: routing.server %v, ждали %v", tc.args, got, tc.want)
		}
		if template, server := showLines(); template != tc.template || server != tc.server {
			t.Errorf("%v: show %q, %q; ждали %q, %q", tc.args, template, server, tc.template, tc.server)
		}
	}

	te = newTestEnv(t)
	te.addHome("-routing-server")
	file, _ := config.Load(te.configPath())
	if p := file.Profiles["home"]; !p.RoutingServerOn() || p.Routing.Template != nil {
		t.Errorf("после add -routing-server: server %v, template %v", p.Routing.Server, p.Routing.Template)
	}
}

func boolPtr(v bool) *bool { return &v }

// Профиль auto_js хешей не спрашивает и не требует; предупреждение о
// воркерах считает по пределу auto_js, а входа VK нет — подсказка.
func TestProfileAddAutoJS(t *testing.T) {
	te := newTestEnv(t)
	te.answer(testPassword, testPassword)
	code, stdout, stderr := te.run("profile", "add", "js", "-peer", "203.0.113.10", "-vk-hash-mode", "auto_js", "-workers", "100")
	if code != exitOK {
		t.Fatalf("код %d, stderr:\n%s", code, stderr)
	}
	if len(te.asked) != 2 {
		t.Errorf("вопросы: %q — хеши в auto_js не нужны", te.asked)
	}
	for _, want := range []string{"vk_hash_mode = auto_js", "в режиме auto_js ядро запустит 70", "vk login"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("в выводе нет %q:\n%s", want, stdout)
		}
	}
	file, err := config.Load(te.configPath())
	if err != nil {
		t.Fatal(err)
	}
	if p := file.Profiles["js"]; !p.AutoJS() || p.VkHashesRef != "" || p.Workers != 100 {
		t.Errorf("профиль: %+v", p)
	}
}

// Ссылка с хешами в auto_js: хеши записываются, но в работе не участвуют, и
// об этом сказано.
func TestProfileAddAutoJSFromLink(t *testing.T) {
	te := newTestEnv(t)
	te.answer(testLink)
	code, stdout, stderr := te.run("profile", "add", "js", "-link", "-vk-hash-mode", "auto_js")
	if code != exitOK {
		t.Fatalf("код %d, stderr:\n%s", code, stderr)
	}
	noSecrets(t, "вывод", stdout+stderr)
	if !strings.Contains(stdout, "auto_js не используются") {
		t.Errorf("нет предупреждения о хешах:\n%s", stdout)
	}
	file, _ := config.Load(te.configPath())
	if got, _ := te.store.Get(file.Profiles["js"].VkHashesRef); got != testHashA+","+testHashB {
		t.Errorf("хеши из ссылки: %q", got)
	}
}

// Переключение туда и обратно: хеши профиля остаются на месте; профиль без
// хешей, вернувшийся к manual, спрашивает их сам.
func TestProfileSetVKHashMode(t *testing.T) {
	te := newTestEnv(t)
	te.addHome()
	te.asked = nil
	if code, _, stderr := te.run("profile", "set", "home", "-vk-hash-mode", "auto_js"); code != exitOK {
		t.Fatalf("код %d, stderr:\n%s", code, stderr)
	}
	if code, _, stderr := te.run("profile", "set", "home", "-vk-hash-mode", "manual"); code != exitOK {
		t.Fatalf("код %d, stderr:\n%s", code, stderr)
	}
	if len(te.asked) != 0 {
		t.Errorf("вопросы: %q — хеши в профиле есть", te.asked)
	}
	file, _ := config.Load(te.configPath())
	if p := file.Profiles["home"]; p.AutoJS() || p.VkHashMode != "manual" || p.VkHashesRef == "" {
		t.Errorf("профиль: %+v", p)
	}

	te.answer(testPassword, testPassword)
	if code, _, stderr := te.run("profile", "add", "js", "-peer", "203.0.113.10", "-vk-hash-mode", "auto_js"); code != exitOK {
		t.Fatalf("код %d, stderr:\n%s", code, stderr)
	}
	te.asked = nil
	te.answer(testHashC)
	if code, _, stderr := te.run("profile", "set", "js", "-vk-hash-mode", ""); code != exitOK {
		t.Fatalf("код %d, stderr:\n%s", code, stderr)
	}
	if len(te.asked) != 1 || !strings.Contains(te.asked[0], "Хеши") {
		t.Errorf("вопросы: %q", te.asked)
	}
	file, _ = config.Load(te.configPath())
	if got, _ := te.store.Get(file.Profiles["js"].VkHashesRef); got != testHashC {
		t.Errorf("хеши: %q", got)
	}

	if code, _, stderr := te.run("profile", "set", "js", "-vk-hash-mode", "auto_api"); code != exitUsage && code != exitError || !strings.Contains(stderr, `"manual", "auto_js"`) {
		t.Errorf("auto_api: код %d, stderr:\n%s", code, stderr)
	}
}

func TestProfileShowAutoJS(t *testing.T) {
	te := newTestEnv(t)
	te.addHome("-vk-hash-mode", "auto_js", "-workers", "80")
	code, stdout, stderr := te.run("profile", "show", "home")
	if code != exitOK {
		t.Fatalf("код %d, stderr:\n%s", code, stderr)
	}
	noSecrets(t, "show", stdout+stderr)
	for _, want := range []string{"vk_hash_mode     auto_js", "в режиме auto_js не нужны", "запустит 70"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("в show нет %q:\n%s", want, stdout)
		}
	}
}
