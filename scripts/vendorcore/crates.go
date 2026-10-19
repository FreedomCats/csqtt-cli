package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// Крейты crates.io, которые ядро получает исходниками в core/, чтобы их можно
// было править (правка П3 — primp-hyper-util, doc/architecture.md §06).
//
// Нетронутая копия крейта — его архив .crate с crates.io в cratesDir, как
// cargo кладёт его в свой кэш. Перенос берёт из Cargo.lock апстрима версию и
// контрольную сумму крейта, сверяет с ними архив, распаковывает его в core/,
// а дальше с файлами крейта обходится как с файлами апстрима: правила
// rules.go применяются к ним, -check сверяет их. Так своя правка остаётся
// отдельной от чужого кода (Р2), а сеть переносу и -check не нужна.
//
// Если апстрим поднимет версию крейта, перенос упадёт и назовёт архив, который
// нужно положить в cratesDir: иначе [patch.crates-io] ядра перестал бы
// действовать молча, и вместе с ним — правка.

// cratesDir — каталог репозитория с архивами вендоренных крейтов.
const cratesDir = "scripts/vendorcore/crates"

// vendoredCrate — крейт crates.io, переносимый из архива в cratesDir.
type vendoredCrate struct {
	// Name — имя пакета на crates.io; архив — <Name>-<версия>.crate.
	Name string
	// Dir — куда в core/ ложатся файлы крейта.
	Dir string
	// Lock — Cargo.lock апстрима, где записаны версия и сумма крейта.
	Lock string
	// Skip — файлы архива, которые не переносятся: путь в крейте → причина.
	Skip map[string]string
}

var vendoredCrates = []vendoredCrate{{
	Name: "primp-hyper-util",
	Dir:  "rust-client/vendor/primp-hyper-util",
	Lock: "rust-client/Cargo.lock",
	Skip: map[string]string{
		// В нём Cargo.lock: git не взял бы Cargo.lock крейта в репозиторий, и
		// -check в CI не нашёл бы его.
		".gitignore": "правила git крейта прятали бы его файлы от git репозитория",
	},
}}

// crateVersion — версия и контрольная сумма крейта, с которыми он перенесён.
type crateVersion struct {
	Name, Version, Checksum string
}

var lockPackage = regexp.MustCompile(`(?m)^name = "([^"]+)"\nversion = "([^"]+)"\nsource = "registry\+https://github\.com/rust-lang/crates\.io-index"\nchecksum = "([0-9a-f]{64})"$`)

// lockEntry — версия и сумма пакета name из crates.io в тексте Cargo.lock.
func lockEntry(lock []byte, file, name string) (version, checksum string, err error) {
	var found [][]string
	for _, m := range lockPackage.FindAllStringSubmatch(string(lock), -1) {
		if m[1] == name {
			found = append(found, m)
		}
	}
	switch len(found) {
	case 0:
		return "", "", fmt.Errorf("в %s апстрима нет %s с crates.io — апстрим перестал его использовать или сменил источник; поправьте vendoredCrates в scripts/vendorcore", file, name)
	case 1:
		return found[0][2], found[0][3], nil
	}
	return "", "", fmt.Errorf("в %s апстрима %d версий %s — правка П3 рассчитана на одну", file, len(found), name)
}

// addCrates кладёт в plan файлы вендоренных крейтов: из Cargo.lock плана
// (ещё без правил) — версия и сумма, из архива в root/cratesDir — файлы.
func addCrates(root string, plan map[string][]byte) ([]crateVersion, error) {
	var versions []crateVersion
	for _, c := range vendoredCrates {
		lock, ok := plan[c.Lock]
		if !ok {
			return nil, fmt.Errorf("крейт %s: в апстриме нет %s", c.Name, c.Lock)
		}
		version, sum, err := lockEntry(lock, c.Lock, c.Name)
		if err != nil {
			return nil, err
		}
		name := c.Name + "-" + version + ".crate"
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(cratesDir), name))
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("апстрим требует %s %s, а архива %s/%s нет.\n"+
				"Положите его туда (cargo кладёт его в ~/.cargo/registry/cache/…), сверьте правила П3 в scripts/vendorcore/rules.go\n"+
				"с новым кодом крейта и удалите архив прежней версии", c.Name, version, cratesDir, name)
		}
		if err != nil {
			return nil, err
		}
		if got := sha256.Sum256(raw); hex.EncodeToString(got[:]) != sum {
			return nil, fmt.Errorf("%s/%s: сумма sha256 %x, а в %s апстрима — %s", cratesDir, name, got, c.Lock, sum)
		}
		files, err := unpackCrate(raw, c.Name+"-"+version)
		if err != nil {
			return nil, fmt.Errorf("%s/%s: %w", cratesDir, name, err)
		}
		for rel, data := range files {
			if _, skip := c.Skip[rel]; skip {
				continue
			}
			to := c.Dir + "/" + rel
			if _, ok := plan[to]; ok {
				return nil, fmt.Errorf("%s есть и в апстриме, и в крейте %s — апстрим завёл свой файл с тем же путём", to, c.Name)
			}
			plan[to] = normalize(data)
		}
		versions = append(versions, crateVersion{c.Name, version, sum})
	}
	return versions, nil
}

// unpackCrate — файлы архива .crate (tar.gz, всё под каталогом prefix):
// путь внутри крейта через «/» → содержимое. Кроме обычных файлов и
// каталогов в архиве ничего быть не должно.
func unpackCrate(raw []byte, prefix string) (map[string][]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	files := map[string][]byte{}
	r := tar.NewReader(gz)
	for {
		h, err := r.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		rel, ok := strings.CutPrefix(h.Name, prefix+"/")
		clean := path.Clean(rel)
		if !ok || rel == "" || clean != strings.TrimSuffix(rel, "/") || clean == ".." || strings.HasPrefix(clean, "../") {
			return nil, fmt.Errorf("путь %q вне каталога %s", h.Name, prefix)
		}
		switch h.Typeflag {
		case tar.TypeDir:
		case tar.TypeReg:
			data, err := io.ReadAll(r)
			if err != nil {
				return nil, err
			}
			if _, dup := files[rel]; dup {
				return nil, fmt.Errorf("файл %s в архиве дважды", rel)
			}
			files[rel] = data
		default:
			return nil, fmt.Errorf("%s — не обычный файл (тип %q)", h.Name, h.Typeflag)
		}
	}
	return files, gz.Close()
}
