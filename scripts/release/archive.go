package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/flate"
	"compress/gzip"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/FreedomCats/csqtt-cli/scripts/internal/script"
)

// archiveTime — время всех записей zip и tar.gz: время файлов на машине
// сборки в архив не попадает, и одинаковые файлы дают одинаковый архив.
// 1 января 1980 — самая ранняя дата, которую умеет zip (формат DOS).
var archiveTime = time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)

// files — обычные файлы каталога dir в порядке script.SortNames. Вложенных
// каталогов в релизе нет; встретится — ошибка, а не тихий пропуск.
func files(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.Type().IsRegular() {
			return nil, fmt.Errorf("в %s не файл: %s", dir, e.Name())
		}
		names = append(names, e.Name())
	}
	script.SortNames(names)
	return names, nil
}

// zipCreator и zipAttrs — «чем создано» и атрибуты записи: Unix (3) и
// обычный файл 0644, на любой системе сборки. Windows права из zip не берёт,
// а unzip на Linux получит обычные файлы.
const (
	zipCreator = 3<<8 | 20
	zipAttrs   = (0o100000 | 0o644) << 16
)

// writeZip пишет каталог dir в path: внутри — каталог с именем dir, записи
// без отдельных записей каталогов, deflate, время — archiveTime.
func writeZip(dir, path string) (err error) {
	names, err := files(dir)
	if err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}()
	zw := zip.NewWriter(f)
	prefix := filepath.Base(dir)
	date, clock := dosTime(archiveTime)
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		var packed bytes.Buffer
		fw, err := flate.NewWriter(&packed, flate.BestCompression)
		if err != nil {
			return err
		}
		if _, err := fw.Write(data); err != nil {
			return err
		}
		if err := fw.Close(); err != nil {
			return err
		}

		// Размеры и CRC — в заголовке записи, без дескриптора после данных,
		// поэтому запись «сырая», сжатая заранее. Modified не задано: иначе
		// zip дописал бы расширенное время в поле extra.
		h := &zip.FileHeader{
			Name:               prefix + "/" + name,
			Method:             zip.Deflate,
			CreatorVersion:     zipCreator,
			ReaderVersion:      20,
			CRC32:              crc32.ChecksumIEEE(data),
			CompressedSize64:   uint64(packed.Len()),
			UncompressedSize64: uint64(len(data)),
			ExternalAttrs:      zipAttrs,
			ModifiedDate:       date,
			ModifiedTime:       clock,
		}
		w, err := zw.CreateRaw(h)
		if err != nil {
			return err
		}
		if _, err := w.Write(packed.Bytes()); err != nil {
			return err
		}
	}
	return zw.Close()
}

// dosTime — дата и время в формате DOS, с точностью до двух секунд.
func dosTime(t time.Time) (date, clock uint16) {
	if t.Year() < 1980 {
		t = time.Date(1980, 1, 1, 0, 0, 0, 0, t.Location())
	}
	date = uint16(t.Day() + int(t.Month())<<5 + (t.Year()-1980)<<9)
	clock = uint16(t.Second()/2 + t.Minute()<<5 + t.Hour()<<11)
	return date, clock
}

// writeTarGz пишет каталог dir в path: внутри — каталог prefix с файлами dir.
// executables получают 0755, остальные — 0644, владелец — 0:0 без имён, время
// — archiveTime, на какой бы системе ни шла сборка. Формат — ustar: pax
// дописал бы записям atime и ctime. В заголовке gzip ни имени, ни времени.
func writeTarGz(dir, path, prefix string, executables []string) (err error) {
	names, err := files(dir)
	if err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}()
	gz, err := gzip.NewWriterLevel(f, gzip.BestCompression)
	if err != nil {
		return err
	}
	gz.OS = 3 // Unix
	tw := tar.NewWriter(gz)

	header := func(name string, typeflag byte, mode, size int64) *tar.Header {
		return &tar.Header{Name: name, Typeflag: typeflag, Mode: mode, Size: size, ModTime: archiveTime, Format: tar.FormatUSTAR}
	}
	if err := tw.WriteHeader(header(prefix+"/", tar.TypeDir, 0o755, 0)); err != nil {
		return err
	}
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		mode := int64(0o644)
		if slices.Contains(executables, name) {
			mode = 0o755
		}
		if err := tw.WriteHeader(header(prefix+"/"+name, tar.TypeReg, mode, int64(len(data)))); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		if _, err := tw.Write(data); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}
