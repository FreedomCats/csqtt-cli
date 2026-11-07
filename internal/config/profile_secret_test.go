package config

import (
	"errors"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	profileOldFileSecret    = "profile-old-file-test-only"
	profileOldKeyringSecret = "profile-old-keyring-test-only"
	profileNewSecret        = "profile-replacement-test-only"
	profileOtherSecret      = "profile-independent-test-only"
)

func preserveTestProfileSecrets(t *testing.T, secrets SecretStore, path string, refs map[string]string) (func(string, string) (string, error), func() error) {
	t.Helper()
	put, rollback, err := PreserveProfileSecrets(secrets, filepath.Join(filepath.Dir(path), FileName), refs)
	if err != nil {
		t.Fatal("cannot preserve test profile secrets")
	}
	return put, rollback
}

func putTestProfileSecret(t *testing.T, put func(string, string) (string, error), name, value string) string {
	t.Helper()
	ref, err := put(name, value)
	if err != nil {
		t.Fatal("cannot write test profile secret")
	}
	return ref
}

func testProfileSecretIndex(t *testing.T, s *store) map[string]secretEntry {
	t.Helper()
	data, err := s.read()
	if err != nil {
		t.Fatal("cannot read test profile secret index")
	}
	return data.Secrets
}

func checkTestProfileSecret(t *testing.T, s SecretStore, ref, want string) {
	t.Helper()
	if got, err := s.Get(ref); err != nil || got != want {
		t.Error("profile secret value or reference was not preserved")
	}
}

func checkProfileRollbackError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("rollback did not report its failure")
	}
	for _, value := range []string{profileOldFileSecret, profileOldKeyringSecret, profileNewSecret, profileOtherSecret} {
		if strings.Contains(err.Error(), value) {
			t.Error("rollback diagnostic contains a secret value")
		}
	}
}

func TestPreserveProfileSecretsRollbackKeepsUnrelatedIndex(t *testing.T) {
	for _, useKeyring := range []bool{false, true} {
		name := "file"
		if useKeyring {
			name = "keyring"
		}
		t.Run(name, func(t *testing.T) {
			s := tempStore(t)
			if useKeyring {
				s.keyring = newFakeKeyring().open
			}
			ref := putTestProfileSecret(t, s.Put, "home.password", profileOldFileSecret)
			putTestProfileSecret(t, s.Put, VKTokenSecret, profileOldKeyringSecret)
			before := testProfileSecretIndex(t, s)
			put, rollback := preserveTestProfileSecrets(t, s, s.path, map[string]string{"home.password": ""})
			if putTestProfileSecret(t, put, "home.password", profileNewSecret) != ref {
				t.Fatal("profile write changed a stable reference")
			}
			otherRef := putTestProfileSecret(t, s.Put, VKTokenSecret, profileOtherSecret)
			addedRef := putTestProfileSecret(t, s.Put, "other.password", profileOtherSecret)
			post := testProfileSecretIndex(t, s)
			if err := rollback(); err != nil {
				t.Fatal("rollback failed")
			}
			after := testProfileSecretIndex(t, s)
			_, id, _ := strings.Cut(ref, ":")
			if after[id] != before[id] {
				t.Error("rollback did not restore the exact destination entry")
			}
			for _, ref := range []string{otherRef, addedRef} {
				_, id, _ := strings.Cut(ref, ":")
				if after[id] != post[id] {
					t.Error("rollback replaced unrelated current index metadata")
				}
				checkTestProfileSecret(t, s, ref, profileOtherSecret)
			}
			checkTestProfileSecret(t, s, ref, profileOldFileSecret)
		})
	}
}

func TestPreserveProfileSecretsRollbackProfileBackendMismatch(t *testing.T) {
	for _, indexKeyring := range []bool{false, true} {
		for _, writeFile := range []bool{false, true} {
			name := "profile keyring/index file"
			if indexKeyring {
				name = "profile file/index keyring"
			}
			if writeFile {
				name += "/file write"
			} else {
				name += "/keyring write"
			}
			t.Run(name, func(t *testing.T) {
				disk := tempStore(t)
				fileRef := putTestProfileSecret(t, disk.Put, "home.password", profileOldFileSecret)
				_, id, _ := strings.Cut(fileRef, ":")
				keyringRef := keyringScheme + ":" + id
				k := newFakeKeyring()
				k.values[id] = profileOldKeyringSecret
				s := keyringStore(disk.path, k)
				profileRef := keyringRef
				if indexKeyring {
					data, err := s.read()
					if err != nil {
						t.Fatal("cannot read original destination")
					}
					entry := data.Secrets[id]
					entry.Store = keyringScheme
					data.Secrets[id] = entry
					if err := s.write(data); err != nil {
						t.Fatal("cannot prepare mismatched backend metadata")
					}
					profileRef = fileRef
				}
				before := testProfileSecretIndex(t, s)
				previous, err := s.Get(profileRef)
				if err != nil {
					t.Fatal("cannot read original profile reference")
				}
				put, rollback := preserveTestProfileSecrets(t, s, s.path, map[string]string{"home.password": profileRef})
				k.unavailable = writeFile
				writtenRef := putTestProfileSecret(t, put, "home.password", profileNewSecret)
				_, writtenID, _ := strings.Cut(writtenRef, ":")
				if writtenID != id {
					t.Fatal("profile write changed its stable destination ID")
				}
				k.unavailable = false
				if err := rollback(); err != nil {
					t.Fatal("rollback of mismatched backends failed")
				}
				if !maps.Equal(testProfileSecretIndex(t, s), before) || k.values[id] != profileOldKeyringSecret {
					t.Error("rollback did not restore both original backends and exact index metadata")
				}
				checkTestProfileSecret(t, s, profileRef, previous)
				checkTestProfileSecret(t, s, keyringRef, profileOldKeyringSecret)
				if !indexKeyring {
					checkTestProfileSecret(t, disk, fileRef, profileOldFileSecret)
				}
			})
		}
	}
}

func TestPreserveProfileSecretsRollbackKeepsDistinctProfileReference(t *testing.T) {
	s := tempStore(t)
	destination := putTestProfileSecret(t, s.Put, "home.password", profileOldFileSecret)
	profileRef := putTestProfileSecret(t, s.Put, "other.password", profileOldKeyringSecret)
	before := testProfileSecretIndex(t, s)
	put, rollback := preserveTestProfileSecrets(t, s, s.path, map[string]string{"home.password": profileRef})
	putTestProfileSecret(t, put, "home.password", profileNewSecret)
	if err := rollback(); err != nil {
		t.Fatal("rollback with a distinct profile reference failed")
	}
	if !maps.Equal(testProfileSecretIndex(t, s), before) {
		t.Error("rollback changed the original destination or profile reference metadata")
	}
	checkTestProfileSecret(t, s, destination, profileOldFileSecret)
	checkTestProfileSecret(t, s, profileRef, profileOldKeyringSecret)
}

func TestPreserveProfileSecretsCustomRollbackUsesProfileBackendValue(t *testing.T) {
	disk := tempStore(t)
	fileRef := putTestProfileSecret(t, disk.Put, "home.password", profileOldFileSecret)
	_, id, _ := strings.Cut(fileRef, ":")
	profileRef := keyringScheme + ":" + id
	k := newFakeKeyring()
	k.values[id] = profileOldKeyringSecret
	s := keyringStore(disk.path, k)
	before := testProfileSecretIndex(t, s)
	opaque := &opaqueProfileStore{SecretStore: s}
	put, rollback := preserveTestProfileSecrets(t, opaque, s.path, map[string]string{"home.password": profileRef})
	putTestProfileSecret(t, put, "home.password", profileNewSecret)
	if err := rollback(); err != nil {
		t.Fatal("custom rollback of a mismatched profile backend failed")
	}
	if !maps.Equal(testProfileSecretIndex(t, s), before) {
		t.Error("custom rollback did not restore the original destination metadata")
	}
	checkTestProfileSecret(t, s, profileRef, profileOldKeyringSecret)
	checkTestProfileSecret(t, disk, fileRef, profileOldFileSecret)
}

func TestPreserveProfileSecretsCustomRollbackRestoresFallbackMetadata(t *testing.T) {
	disk := tempStore(t)
	fileRef := putTestProfileSecret(t, disk.Put, "home.password", profileOldFileSecret)
	_, id, _ := strings.Cut(fileRef, ":")
	k := newFakeKeyring()
	k.values[id] = profileOldKeyringSecret
	s := keyringStore(disk.path, k)
	data, err := s.read()
	if err != nil {
		t.Fatal("cannot read original destination")
	}
	entry := data.Secrets[id]
	entry.Store = keyringScheme
	data.Secrets[id] = entry
	if err := s.write(data); err != nil {
		t.Fatal("cannot prepare mismatched backend metadata")
	}
	before := testProfileSecretIndex(t, s)
	opaque := &opaqueProfileStore{SecretStore: s, put: disk.Put}
	put, rollback := preserveTestProfileSecrets(t, opaque, s.path, map[string]string{"home.password": fileRef})
	putTestProfileSecret(t, put, "home.password", profileNewSecret)
	if err := rollback(); err != nil {
		t.Fatal("custom rollback of a file fallback failed")
	}
	if !maps.Equal(testProfileSecretIndex(t, s), before) {
		t.Error("custom rollback did not restore the original fallback metadata")
	}
	checkTestProfileSecret(t, s, fileRef, profileOldKeyringSecret)
}

// opaqueProfileStore exercises the fallback for a SecretStore without native
// backend-by-ID access, including failures whose text must never be forwarded.
type opaqueProfileStore struct {
	SecretStore
	putErr    map[string]error
	deleteErr map[string]error
	beforePut func()
	put       func(string, string) (string, error)
}

func (s *opaqueProfileStore) Put(name, value string) (string, error) {
	if s.beforePut != nil {
		s.beforePut()
	}
	if err := s.putErr[name]; err != nil {
		return "", err
	}
	if s.put != nil {
		return s.put(name, value)
	}
	return s.SecretStore.Put(name, value)
}

func (s *opaqueProfileStore) Delete(ref string) error {
	if err := s.deleteErr[ref]; err != nil {
		return err
	}
	return s.SecretStore.Delete(ref)
}

func TestPreserveProfileSecretsRollbackRejectsBackendSwitch(t *testing.T) {
	k := newFakeKeyring()
	s := keyringStore(filepath.Join(t.TempDir(), SecretsFileName), k)
	ref := putTestProfileSecret(t, s.Put, "home.password", profileOldKeyringSecret)
	opaque := &opaqueProfileStore{SecretStore: s}
	put, rollback := preserveTestProfileSecrets(t, opaque, s.path, map[string]string{"home.password": ref})
	putTestProfileSecret(t, put, "home.password", profileNewSecret)
	opaque.beforePut = func() { k.unavailable = true }
	err := rollback()
	checkProfileRollbackError(t, err)
	if !strings.Contains(err.Error(), "changed backend") {
		t.Error("rollback did not identify a backend switch")
	}
	_, id, _ := strings.Cut(ref, ":")
	entry := testProfileSecretIndex(t, s)[id]
	if entry.Store == keyringScheme || entry.Data == "" {
		t.Error("rollback restored keyring metadata despite a file fallback")
	}
	if k.values[id] != profileNewSecret {
		t.Error("backend-switch setup did not retain the unrestored keyring value")
	}
}

func TestPreserveProfileSecretsNativeRollbackDoesNotFallBack(t *testing.T) {
	k := newFakeKeyring()
	s := keyringStore(filepath.Join(t.TempDir(), SecretsFileName), k)
	ref := putTestProfileSecret(t, s.Put, "home.password", profileOldKeyringSecret)
	put, rollback := preserveTestProfileSecrets(t, s, s.path, map[string]string{"home.password": ref})
	putTestProfileSecret(t, put, "home.password", profileNewSecret)
	post := testProfileSecretIndex(t, s)
	k.unavailable = true
	err := rollback()
	checkProfileRollbackError(t, err)
	if !errors.Is(err, errKeyringUnavailable) {
		t.Error("rollback lost the unavailable-backend error")
	}
	if !maps.Equal(testProfileSecretIndex(t, s), post) {
		t.Error("native rollback migrated an unavailable keyring destination into the file")
	}
}

type failingProfileKeyring struct {
	*fakeKeyring
	putErr    map[string]error
	deleteErr map[string]error
	puts      map[string]int
	deletes   map[string]int
}

func (k *failingProfileKeyring) Put(id, label, value string) error {
	k.puts[id]++
	if err := k.putErr[id]; err != nil {
		return err
	}
	return k.fakeKeyring.Put(id, label, value)
}

func (k *failingProfileKeyring) Delete(id string) error {
	k.deletes[id]++
	if err := k.deleteErr[id]; err != nil {
		return err
	}
	return k.fakeKeyring.Delete(id)
}

func TestPreserveProfileSecretsRollbackContinuesAfterBackendErrors(t *testing.T) {
	disk := tempStore(t)
	passwordRef := putTestProfileSecret(t, disk.Put, "home.password", profileOldFileSecret)
	k := &failingProfileKeyring{
		fakeKeyring: newFakeKeyring(), putErr: map[string]error{}, deleteErr: map[string]error{},
		puts: map[string]int{}, deletes: map[string]int{},
	}
	s := newSecretStoreAt(disk.path)
	s.keyring = func() (keyring, error) { return k, nil }
	hashesRef := putTestProfileSecret(t, s.Put, "home.vk_hashes", profileOldKeyringSecret)
	tokenRef := putTestProfileSecret(t, s.Put, "home.vk_token", profileOldKeyringSecret)
	before := testProfileSecretIndex(t, s)
	put, rollback := preserveTestProfileSecrets(t, s, s.path, map[string]string{
		"home.password": passwordRef, "home.vk_hashes": hashesRef, "home.vk_token": tokenRef,
	})
	for _, name := range []string{"home.password", "home.vk_hashes", "home.vk_token"} {
		putTestProfileSecret(t, put, name, profileNewSecret)
	}
	_, passwordID, _ := strings.Cut(passwordRef, ":")
	_, hashesID, _ := strings.Cut(hashesRef, ":")
	_, tokenID, _ := strings.Cut(tokenRef, ":")
	deleteFailure, putFailure := errors.New(profileNewSecret), errors.New(profileOldKeyringSecret)
	k.deleteErr[passwordID], k.putErr[tokenID] = deleteFailure, putFailure
	putsBefore := maps.Clone(k.puts)
	err := rollback()
	checkProfileRollbackError(t, err)
	if !errors.Is(err, deleteFailure) || !errors.Is(err, putFailure) {
		t.Error("rollback did not aggregate independent backend failures")
	}
	if k.deletes[passwordID] != 1 || k.puts[hashesID] != putsBefore[hashesID]+1 || k.puts[tokenID] != putsBefore[tokenID]+1 {
		t.Error("one backend failure prevented independent restoration attempts")
	}
	if !maps.Equal(testProfileSecretIndex(t, s), before) {
		t.Error("backend failures prevented available index metadata restoration")
	}
	checkTestProfileSecret(t, disk, passwordRef, profileOldFileSecret)
	checkTestProfileSecret(t, s, hashesRef, profileOldKeyringSecret)
}

func TestPreserveProfileSecretsRollbackContinuesAfterCustomErrors(t *testing.T) {
	s := tempStore(t)
	passwordRef := putTestProfileSecret(t, s.Put, "home.password", profileOldFileSecret)
	hashesRef := putTestProfileSecret(t, s.Put, "home.vk_hashes", profileOldKeyringSecret)
	before := testProfileSecretIndex(t, s)
	opaque := &opaqueProfileStore{SecretStore: s, putErr: map[string]error{}, deleteErr: map[string]error{}}
	put, rollback := preserveTestProfileSecrets(t, opaque, s.path, map[string]string{
		"home.password": passwordRef, "home.vk_hashes": hashesRef, "home.vk_token": "",
	})
	putTestProfileSecret(t, put, "home.password", profileNewSecret)
	putTestProfileSecret(t, put, "home.vk_hashes", profileNewSecret)
	tokenRef := putTestProfileSecret(t, put, "home.vk_token", profileNewSecret)
	putFailure, deleteFailure := errors.New(profileNewSecret), errors.New(profileOldKeyringSecret)
	opaque.putErr["home.password"], opaque.deleteErr[tokenRef] = putFailure, deleteFailure
	err := rollback()
	checkProfileRollbackError(t, err)
	if !errors.Is(err, putFailure) || !errors.Is(err, deleteFailure) {
		t.Error("rollback did not aggregate independent custom-store failures")
	}
	if !maps.Equal(testProfileSecretIndex(t, s), before) {
		t.Error("custom-store failures prevented available index metadata restoration")
	}
	checkTestProfileSecret(t, s, passwordRef, profileOldFileSecret)
	checkTestProfileSecret(t, s, hashesRef, profileOldKeyringSecret)
	if _, err := s.Get(tokenRef); !errors.Is(err, os.ErrNotExist) {
		t.Error("rollback did not remove available new-record metadata")
	}
}

func TestPreserveProfileSecretsRollbackDetectsDestinationConflict(t *testing.T) {
	for _, useKeyring := range []bool{false, true} {
		for _, useOpaque := range []bool{false, true} {
			name := "file"
			if useKeyring {
				name = "keyring"
			}
			if useOpaque {
				name += "/custom store"
			}
			t.Run(name, func(t *testing.T) {
				s := tempStore(t)
				if useKeyring {
					s.keyring = newFakeKeyring().open
				}
				ref := putTestProfileSecret(t, s.Put, "home.password", profileOldFileSecret)
				hashesRef := putTestProfileSecret(t, s.Put, "home.vk_hashes", profileOldKeyringSecret)
				var secrets SecretStore = s
				if useOpaque {
					secrets = &opaqueProfileStore{SecretStore: s}
				}
				put, rollback := preserveTestProfileSecrets(t, secrets, s.path, map[string]string{
					"home.password": ref, "home.vk_hashes": hashesRef,
				})
				putTestProfileSecret(t, put, "home.password", profileNewSecret)
				putTestProfileSecret(t, put, "home.vk_hashes", profileNewSecret)
				putTestProfileSecret(t, s.Put, "home.password", profileOtherSecret)
				post := testProfileSecretIndex(t, s)
				err := rollback()
				checkProfileRollbackError(t, err)
				if !strings.Contains(err.Error(), "rollback conflict") {
					t.Error("rollback did not identify the destination conflict")
				}
				_, id, _ := strings.Cut(ref, ":")
				if testProfileSecretIndex(t, s)[id] != post[id] {
					t.Error("rollback overwrote another operation's destination metadata")
				}
				checkTestProfileSecret(t, s, ref, profileOtherSecret)
				checkTestProfileSecret(t, s, hashesRef, profileOldKeyringSecret)
			})
		}
	}
}

func TestPreserveProfileSecretsUnavailableBackupStaysInFile(t *testing.T) {
	disk := tempStore(t)
	ref := putTestProfileSecret(t, disk.Put, "home.password", profileOldFileSecret)
	_, id, _ := strings.Cut(ref, ":")
	k := newFakeKeyring()
	k.values[id] = profileOtherSecret
	k.unavailable = true
	s := keyringStore(disk.path, k)
	put, rollback := preserveTestProfileSecrets(t, s, s.path, map[string]string{"home.password": ref})
	k.unavailable = false
	if putTestProfileSecret(t, put, "home.password", profileNewSecret) != ref {
		t.Error("write migrated to a backend without a preserved destination")
	}
	if err := rollback(); err != nil {
		t.Fatal("file rollback failed after keyring availability changed")
	}
	checkTestProfileSecret(t, s, ref, profileOldFileSecret)
	if k.values[id] != profileOtherSecret {
		t.Error("profile update changed an unavailable backend's original value")
	}
}

func TestPreserveProfileSecretsRollbackDetectsMigrationConflict(t *testing.T) {
	disk := tempStore(t)
	ref := putTestProfileSecret(t, disk.Put, "home.password", profileOldFileSecret)
	k := newFakeKeyring()
	s := keyringStore(disk.path, k)
	put, rollback := preserveTestProfileSecrets(t, s, s.path, map[string]string{"home.password": ref})
	keyringRef := putTestProfileSecret(t, put, "home.password", profileNewSecret)
	putTestProfileSecret(t, s.Put, "home.password", profileOtherSecret)
	post := testProfileSecretIndex(t, s)
	err := rollback()
	checkProfileRollbackError(t, err)
	if !strings.Contains(err.Error(), "rollback conflict") {
		t.Error("rollback did not identify the migrated backend's conflict")
	}
	if !maps.Equal(testProfileSecretIndex(t, s), post) {
		t.Error("rollback replaced a conflicting migration's current metadata")
	}
	checkTestProfileSecret(t, s, keyringRef, profileOtherSecret)
}

func TestPreserveProfileSecretsRollbackAfterWriteVerificationError(t *testing.T) {
	k := newFakeKeyring()
	s := newSecretStoreAt(filepath.Join(t.TempDir(), SecretsFileName))
	opens, failOpen := 0, 0
	failure := errors.New(profileNewSecret)
	s.keyring = func() (keyring, error) {
		opens++
		if opens == failOpen {
			return nil, failure
		}
		return k, nil
	}
	ref := putTestProfileSecret(t, s.Put, "home.password", profileOldKeyringSecret)
	before := testProfileSecretIndex(t, s)
	put, rollback := preserveTestProfileSecrets(t, s, s.path, map[string]string{"home.password": ref})
	failOpen = opens + 2
	_, err := put("home.password", profileNewSecret)
	checkProfileRollbackError(t, err)
	if !errors.Is(err, failure) {
		t.Error("write verification did not preserve its backend error")
	}
	if err := rollback(); err != nil {
		t.Fatal("write verification failure prevented rollback")
	}
	if !maps.Equal(testProfileSecretIndex(t, s), before) {
		t.Error("write verification failure prevented index restoration")
	}
	checkTestProfileSecret(t, s, ref, profileOldKeyringSecret)
}

func TestPreserveProfileSecretsUnreadableBackendDoesNotWrite(t *testing.T) {
	for _, failure := range []error{ErrKeyringLocked, errors.New(profileOtherSecret), os.ErrNotExist} {
		t.Run("unreadable backend", func(t *testing.T) {
			disk := tempStore(t)
			ref := putTestProfileSecret(t, disk.Put, "home.password", profileOldFileSecret)
			before := testProfileSecretIndex(t, disk)
			k := newFakeKeyring()
			k.err = failure
			s := keyringStore(disk.path, k)
			_, _, err := PreserveProfileSecrets(s, filepath.Join(filepath.Dir(s.path), FileName), map[string]string{"home.password": ref})
			checkProfileRollbackError(t, err)
			if !maps.Equal(testProfileSecretIndex(t, s), before) || len(k.values) != 0 {
				t.Error("unreadable backend changed a profile secret destination")
			}
		})
	}
}
