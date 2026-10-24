package winsys

import (
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// dnsProbeEnv — режим помощника: послать запрос DNS серверу из значения и
// выйти с кодом 0, если ответ пришёл.
const dnsProbeEnv = "CSQTT_TEST_DNS_PROBE"

// dnsProbeServer — DNS Яндекса, к которому ходит и ядро.
const dnsProbeServer = "77.88.8.8:53"

func TestMain(m *testing.M) {
	if server, ok := os.LookupEnv(dnsProbeEnv); ok {
		if err := dnsProbe(server); err != nil {
			os.Stderr.WriteString(err.Error())
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// dnsProbeTries — сколько раз проба шлёт запрос: одна датаграма до DNS
// Яндекса из облака CI теряется и без защиты.
const dnsProbeTries = 3

// dnsProbe спрашивает у server запись A для example.com и ждёт ответа с тем
// же номером запроса; без ответа повторяет, до dnsProbeTries раз.
func dnsProbe(server string) error {
	conn, err := net.DialTimeout("udp4", server, 3*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	query := []byte{0x51, 0x7e, 0x01, 0x00, 0, 1, 0, 0, 0, 0, 0, 0}
	for _, label := range []string{"example", "com"} {
		query = append(append(query, byte(len(label))), label...)
	}
	query = append(query, 0, 0, 1, 0, 1)
	reply := make([]byte, 512)
	for try := 1; ; try++ {
		if _, err := conn.Write(query); err != nil {
			return err
		}
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, err := conn.Read(reply)
		if err != nil {
			if try < dnsProbeTries {
				continue
			}
			return err
		}
		if n < 12 || binary.BigEndian.Uint16(reply) != 0x517e {
			return errors.New("чужой ответ DNS")
		}
		return nil
	}
}

// shortDir — каталог с длинным именем внутри t.TempDir() и его путь с
// короткими именами 8.3; short пуст, если на томе их не заводят.
func shortDir(t *testing.T) (long, short string) {
	t.Helper()
	long = filepath.Join(t.TempDir(), "csqtt long directory name")
	if err := os.Mkdir(long, 0o755); err != nil {
		t.Fatal(err)
	}
	p, err := windows.UTF16PtrFromString(long)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]uint16, windows.MAX_PATH)
	n, err := windows.GetShortPathName(p, &buf[0], uint32(len(buf)))
	if err != nil || n == 0 || int(n) >= len(buf) {
		return long, ""
	}
	if short = windows.UTF16ToString(buf[:n]); strings.EqualFold(short, long) {
		return long, ""
	}
	return long, short
}

// Путь с короткими именами 8.3 становится длинным; путь без файла
// остаётся как есть.
func TestLongPath(t *testing.T) {
	long, short := shortDir(t)
	if short == "" {
		t.Skip("на томе не заводятся короткие имена 8.3")
	}
	// Сравнивать с long нельзя: сам t.TempDir() бывает коротким (TEMP в CI).
	got := longPath(short)
	if _, err := os.Stat(got); err != nil || strings.Contains(got, "~") ||
		!strings.EqualFold(filepath.Base(got), filepath.Base(long)) {
		t.Errorf("longPath(%q) = %q (%v), ждали путь без коротких имён к %s", short, got, err, filepath.Base(long))
	}
	missing := filepath.Join(short, "нет такого.exe")
	if got := longPath(missing); got != missing {
		t.Errorf("longPath(%q) = %q, ждали путь как есть", missing, got)
	}
}

// Программа из programs узнаётся WFP по пути к exe: при «закрыто» её
// запрос DNS к DNS Яндекса проходит, а у той же программы под другим путём —
// нет. Так kill-switch пропускает ядро рядом с csqtt-cli.exe. Путь — с
// короткими именами 8.3, если том их заводит: процесс WFP знает по длинным
// (так в CI GitHub, где TEMP — «RUNNER~1»). Нужны права администратора и
// доступ к DNS Яндекса.
func TestDNSGuardPermitsPrograms(t *testing.T) {
	dir, short := shortDir(t)
	if short != "" {
		dir = short
	}
	allowed, other := filepath.Join(dir, "allowed.exe"), filepath.Join(dir, "other.exe")
	for _, path := range []string{allowed, other} {
		copyFile(t, os.Args[0], path)
	}
	probe := func(exe string) error {
		cmd := exec.Command(exe)
		cmd.Env = append(os.Environ(), dnsProbeEnv+"="+dnsProbeServer)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return errors.New(string(out))
		}
		return nil
	}
	for _, exe := range []string{allowed, other} {
		if err := probe(exe); err != nil {
			t.Skipf("DNS Яндекса недоступен и без защиты (%s): %v", filepath.Base(exe), err)
		}
	}

	g, err := OpenDNSGuard(allowed)
	if err != nil {
		t.Skipf("сессия WFP не открылась (нужны права администратора): %v", err)
	}
	defer g.Close()
	if len(g.appIDs) != 2 {
		t.Fatalf("идентификаторов приложений %d, ждали два", len(g.appIDs))
	}
	if err := g.Lockdown(); err != nil {
		t.Fatalf("Lockdown: %v", err)
	}
	if err := probe(allowed); err != nil {
		t.Errorf("при «закрыто» запрос разрешённой программы не прошёл: %v", err)
	}
	if err := probe(other); err == nil {
		t.Error("при «закрыто» запрос чужой программы прошёл")
	}
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	src, err := os.Open(from)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	dst, err := os.Create(to)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		t.Fatal(err)
	}
	if err := dst.Close(); err != nil {
		t.Fatal(err)
	}
}
