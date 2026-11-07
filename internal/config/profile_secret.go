package config

import (
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

type profileSecretValue struct {
	value   string
	present bool
}

var errProfileSecretConflict = errors.New("profile secret rollback conflict: destination changed")

type profileSecretWrite struct {
	name, id, writtenRef string
	writtenValue         string
	original             *secretEntry
	written              *secretEntry
	values               map[string]profileSecretValue
	keyring              profileSecretValue
	keyringKnown         bool
	needsKeyring         bool
	fileOnly             bool
}

// PreserveProfileSecrets prepares only storeProfile's writes. The returned Put
// records their post-images; rollback merges only those destinations into the
// current index. SecretStore and the on-disk format remain unchanged.
func PreserveProfileSecrets(secrets SecretStore, configPath string, references map[string]string) (
	put func(string, string) (string, error), rollback func() error, err error,
) {
	index := newSecretStoreAt(filepath.Join(filepath.Dir(configPath), SecretsFileName))
	data, err := index.read()
	if err != nil {
		return nil, nil, profileSecretFailure{"cannot preserve profile secret index", err}
	}
	native, _ := secrets.(*store)
	names := make([]string, 0, len(references))
	writes := make(map[string]*profileSecretWrite, len(references))
	for name, ref := range references {
		names = append(names, name)
		w := &profileSecretWrite{name: name, values: map[string]profileSecretValue{}}
		for id, entry := range data.Secrets {
			if entry.Name != name {
				continue
			}
			if id == "" || w.original != nil {
				return nil, nil, errors.New("cannot preserve ambiguous profile secret destination")
			}
			w.id, w.original = id, &entry
		}
		refs := []string{ref}
		if w.original != nil {
			refs = append(refs, profileSecretRef(w.id, *w.original))
		}
		for _, ref := range refs {
			if strings.TrimSpace(ref) == "" {
				continue
			}
			if _, ok := w.values[ref]; ok {
				continue
			}
			value, getErr := secrets.Get(ref)
			if getErr != nil && !errors.Is(getErr, os.ErrNotExist) {
				return nil, nil, profileSecretFailure{"cannot preserve previous profile secret", getErr}
			}
			w.values[ref] = profileSecretValue{value, getErr == nil}
			_, id, _ := strings.Cut(ref, ":")
			if id == w.id && (strings.HasPrefix(ref, keyringScheme+":") || w.original != nil && w.original.Store == keyringScheme) {
				w.needsKeyring = true
			}
		}
		if native != nil && w.id != "" && native.keyring != nil {
			value, getErr := native.Get(keyringScheme + ":" + w.id)
			if getErr == nil || errors.Is(getErr, os.ErrNotExist) {
				w.keyring, w.keyringKnown = profileSecretValue{value, getErr == nil}, true
			} else if !w.needsKeyring && errors.Is(getErr, errKeyringUnavailable) {
				// Do not migrate this destination into a newly available keyring:
				// its previous value there could not be preserved. Use the same
				// file fallback that Put would use while it is unavailable.
				w.fileOnly = true
			} else {
				return nil, nil, profileSecretFailure{"cannot preserve previous profile secret backend", getErr}
			}
		}
		writes[name] = w
	}
	slices.Sort(names)
	put = func(name, value string) (string, error) {
		w := writes[name]
		if w == nil {
			return "", errors.New("profile secret destination was not preserved")
		}
		expected := w.original
		if w.written != nil {
			expected = w.written
		}
		if _, err := w.current(index, expected); err != nil {
			return "", err
		}
		writer := secrets
		if w.fileOnly {
			writer = newSecretStoreAt(native.path)
		}
		ref, putErr := writer.Put(name, value)
		if putErr != nil {
			return "", profileSecretFailure{"cannot write profile secret", putErr}
		}
		w.writtenRef, w.writtenValue = ref, value
		_, id, ok := strings.Cut(ref, ":")
		if !ok || id == "" || w.id != "" && w.id != id {
			return "", errors.New("profile secret write changed its destination identity")
		}
		w.id = id
		current, readErr := index.read()
		if readErr != nil {
			return "", profileSecretFailure{"cannot record profile secret write", readErr}
		}
		entry, ok := current.Secrets[id]
		if !ok || entry.Name != name || profileSecretRef(id, entry) != ref {
			return "", errors.New("profile secret destination changed during write")
		}
		if strings.HasPrefix(ref, keyringScheme+":") {
			// Backend verification may itself fail after Put. Keep its known
			// index post-image so rollback can still attempt both parts.
			w.written = &entry
		}
		if native != nil {
			var got string
			var getErr error
			if strings.HasPrefix(ref, keyringScheme+":") {
				// The index has no value for a keyring destination.
				got, getErr = native.Get(ref)
			} else {
				// Decode this exact entry, not a second read of an index which
				// another operation might already have changed.
				var sealed, plain []byte
				if sealed, getErr = base64.StdEncoding.DecodeString(entry.Data); getErr == nil {
					plain, getErr = unprotect(sealed)
					got = string(plain)
				}
			}
			if getErr != nil {
				return "", profileSecretFailure{"cannot record profile secret backend write", getErr}
			}
			if got != value {
				return "", errors.New("profile secret destination changed during write")
			}
		}
		w.written = &entry
		return ref, nil
	}
	rollback = func() error {
		var failures []error
		for _, name := range names {
			w := writes[name]
			if w.writtenRef == "" {
				continue
			}
			if err := w.restore(secrets, native, index); err != nil {
				failures = append(failures, err)
			}
		}
		// A persisted profile can name a different backend or ID than the
		// index destination. Verify those references after all destinations
		// have been restored, including references shared by several updates.
		checked := map[string]bool{}
		for _, name := range names {
			for ref, old := range writes[name].values {
				if checked[ref] {
					continue
				}
				checked[ref] = true
				value, getErr := secrets.Get(ref)
				if getErr != nil && !errors.Is(getErr, os.ErrNotExist) {
					failures = append(failures, profileSecretFailure{"cannot verify previous profile secret reference", getErr})
				} else if (getErr == nil) != old.present || old.present && value != old.value {
					failures = append(failures, errors.New("previous profile secret reference was not restored"))
				}
			}
		}
		return errors.Join(failures...)
	}
	return put, rollback, nil
}

func profileSecretRef(id string, entry secretEntry) string {
	scheme := entry.Store
	if scheme == "" {
		scheme = secretScheme
	}
	return scheme + ":" + id
}

func (w *profileSecretWrite) current(index *store, expected *secretEntry) (*secretsData, error) {
	data, err := index.read()
	if err != nil {
		return nil, profileSecretFailure{"cannot merge profile secret rollback index", err}
	}
	for id, entry := range data.Secrets {
		if entry.Name == w.name && id != w.id {
			return nil, errors.New("profile secret rollback conflict: destination identity changed")
		}
	}
	entry, exists := data.Secrets[w.id]
	if expected == nil && exists || expected != nil && (!exists || entry != *expected) {
		return nil, errors.New("profile secret rollback conflict: destination index changed")
	}
	return data, nil
}

func (w *profileSecretWrite) restore(secrets SecretStore, native, index *store) error {
	if w.written == nil {
		return errors.New("cannot safely restore unrecorded profile secret write")
	}
	if _, err := w.current(index, w.written); err != nil {
		return err
	}
	var failures []error
	expected := w.written
	canMerge := true
	if native != nil {
		if strings.HasPrefix(w.writtenRef, keyringScheme+":") {
			// Only a keyring Put changes the keyring value. A file fallback
			// leaves it intact, even when the old profile refers to it.
			if err := w.restoreKeyring(native); err != nil {
				if errors.Is(err, errProfileSecretConflict) {
					failures = append(failures, errProfileSecretConflict)
					canMerge = false
				} else {
					failures = append(failures, profileSecretFailure{"cannot restore previous profile secret backend", err})
				}
			}
		}
	} else {
		value, getErr := secrets.Get(w.writtenRef)
		if errors.Is(getErr, os.ErrNotExist) || getErr == nil && value != w.writtenValue {
			return errors.New("profile secret rollback conflict: destination value changed")
		}
		var restoreErr error
		exactRef, wantRef := "", ""
		if getErr != nil {
			// The backend cannot be changed safely without a readable
			// post-image. Index data can still be restored independently.
			failures = append(failures, profileSecretFailure{"cannot check profile secret rollback destination", getErr})
		} else if old, known := w.values[w.writtenRef]; known {
			// Restore the backend Put actually changed. Its prior value can
			// come from the profile reference, not the destination's index.
			// The original file entry is restored separately below.
			if old.present {
				wantRef = w.writtenRef
				exactRef, restoreErr = secrets.Put(w.name, old.value)
			} else {
				restoreErr = secrets.Delete(w.writtenRef)
			}
		} else {
			restoreErr = secrets.Delete(w.writtenRef)
		}
		if restoreErr != nil {
			failures = append(failures, profileSecretFailure{"cannot restore previous profile secret destination", restoreErr})
		}
		if wantRef != "" && (restoreErr == nil || exactRef != "") && exactRef != wantRef {
			failures = append(failures, errors.New("profile secret rollback changed backend or identity"))
			canMerge = false
		}
		// The custom store has already changed its own index. Validate its
		// resulting identity before restoring the original entry's metadata.
		current, err := index.read()
		if err != nil {
			failures = append(failures, profileSecretFailure{"cannot merge profile secret rollback index", err})
			canMerge = false
		} else {
			expected = nil
			if entry, ok := current.Secrets[w.id]; ok {
				sameOriginal := w.original != nil && entry == *w.original
				sameRestored := restoreErr == nil && wantRef != "" && exactRef == wantRef && entry.Name == w.name && profileSecretRef(w.id, entry) == wantRef
				if entry != *w.written && !sameOriginal && !sameRestored {
					failures = append(failures, errors.New("profile secret rollback conflict: destination changed during restoration"))
					canMerge = false
				}
				expected = &entry
			}
		}
	}
	if canMerge {
		// Re-read after backend operations and replace only our destination.
		// Independent writes to other names must survive, even on partial failure.
		data, err := w.current(index, expected)
		if err != nil {
			failures = append(failures, err)
		} else {
			if w.original == nil {
				delete(data.Secrets, w.id)
			} else {
				data.Secrets[w.id] = *w.original
			}
			if err := index.write(data); err != nil {
				failures = append(failures, profileSecretFailure{"cannot merge profile secret rollback index", err})
			}
		}
	}
	return errors.Join(failures...)
}

func (w *profileSecretWrite) restoreKeyring(s *store) error {
	if !w.keyringKnown && w.original != nil {
		return errors.New("previous keyring destination was not preserved")
	}
	k, err := s.openKeyring()
	if err != nil {
		return err
	}
	defer k.Close()
	value, err := k.Get(w.id)
	if errors.Is(err, errKeyringNotFound) {
		return errProfileSecretConflict
	}
	if err != nil {
		return s.note(err)
	}
	if value != w.writtenValue {
		return errProfileSecretConflict
	}
	if w.keyring.present {
		if err := s.note(k.Put(w.id, "csqtt-cli "+w.name, w.keyring.value)); err != nil {
			return err
		}
		value, err := k.Get(w.id)
		if err != nil {
			return s.note(err)
		}
		if value != w.keyring.value {
			return errors.New("keyring destination was not restored")
		}
		return nil
	}
	if err := s.note(k.Delete(w.id)); err != nil && !errors.Is(err, errKeyringNotFound) {
		return err
	}
	return nil
}

type profileSecretFailure struct {
	context string
	err     error
}

func (e profileSecretFailure) Error() string { return e.context }
func (e profileSecretFailure) Unwrap() error { return e.err }
