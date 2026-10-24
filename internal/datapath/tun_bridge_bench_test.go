package datapath

import (
	"net"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

// Потолок моста без системы (W7): адаптер — подделка, которая отдаёт и
// принимает пакеты сразу, ядро — сокет UDP на loopback. Скорость настоящего
// туннеля ниже этого потолка — значит, узкое место не мост, а Wintun, ядро
// или канал.
//
//	go test -run '^$' -bench Bridge -benchtime 200000x ./internal/datapath

// benchPacket — пакет IPv4 размером с полный кадр туннеля.
const benchPacket = 1400

// floodDevice — адаптер, у которого пакет к чтению есть всегда; записанные
// пакеты он только считает.
type floodDevice struct {
	packet  []byte
	written atomic.Int64
	closed  chan struct{}
}

func newFloodDevice() *floodDevice {
	return &floodDevice{packet: ipv4(benchPacket, 1), closed: make(chan struct{})}
}

func (d *floodDevice) Read(bufs [][]byte, sizes []int, offset int) (int, error) {
	select {
	case <-d.closed:
		return 0, os.ErrClosed
	default:
	}
	sizes[0] = copy(bufs[0][offset:], d.packet)
	return 1, nil
}

func (d *floodDevice) Write(bufs [][]byte, offset int) (int, error) {
	select {
	case <-d.closed:
		return 0, os.ErrClosed
	default:
	}
	d.written.Add(int64(len(bufs)))
	return len(bufs), nil
}

// benchLink — Link и сокет на месте ядра.
func benchLink(b *testing.B) (*Link, *net.UDPConn) {
	b.Helper()
	link, err := ListenLink()
	if err != nil {
		b.Fatal(err)
	}
	core, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		b.Fatal(err)
	}
	_ = core.SetReadBuffer(4 << 20)
	link.SetCorePort(uint16(core.LocalAddr().(*net.UDPAddr).Port))
	return link, core
}

// reportRate — пакеты в секунду и Мбит/с за время замера.
func reportRate(b *testing.B, packets int64, elapsed time.Duration) {
	pps := float64(packets) / elapsed.Seconds()
	b.ReportMetric(pps, "pkt/s")
	b.ReportMetric(pps*benchPacket*8/1e6, "Mbit/s")
	b.ReportMetric(0, "ns/op")
}

// От адаптера к ядру: мост читает адаптер без пауз, ядро принимает b.N
// пакетов.
func BenchmarkBridgeToCore(b *testing.B) {
	link, core := benchLink(b)
	dev := newFloodDevice()
	br := startBridge(dev, link, nil, nil)
	buf := make([]byte, MaxPacket)
	b.ResetTimer()
	start := time.Now()
	for range b.N {
		if _, err := core.Read(buf); err != nil {
			b.Fatal(err)
		}
	}
	elapsed := time.Since(start)
	b.StopTimer()
	reportRate(b, int64(b.N), elapsed)
	br.stop()
	close(dev.closed)
	_ = link.Close()
	_ = core.Close()
	br.wait()
}

// От ядра к адаптеру: ядро шлёт b.N пакетов, сколько успевает, мост пишет их
// в адаптер. Потерянное на loopback (буфер сокета полон) — доля loss.
func BenchmarkBridgeToAdapter(b *testing.B) {
	link, core := benchLink(b)
	dev := newFloodDevice()
	// Чтение адаптера мосту здесь не нужно: подделка без пауз заняла бы
	// процессор. Закрытая — мост на ней сразу выходит, запись — нет.
	br := startBridge(&writeOnly{dev}, link, nil, nil)
	packet := ipv4(benchPacket, 2)
	to := link.Addr()
	b.ResetTimer()
	start := time.Now()
	for range b.N {
		if _, err := core.WriteToUDPAddrPort(packet, to); err != nil {
			b.Fatal(err)
		}
	}
	// Дождаться, пока мост допишет то, что уже в буфере сокета.
	last, still := dev.written.Load(), time.Now()
	for dev.written.Load() < int64(b.N) && time.Since(still) < 200*time.Millisecond {
		time.Sleep(time.Millisecond)
		if n := dev.written.Load(); n != last {
			last, still = n, time.Now()
		}
	}
	elapsed := time.Since(start)
	b.StopTimer()
	got := dev.written.Load()
	reportRate(b, got, elapsed)
	b.ReportMetric(100*float64(int64(b.N)-got)/float64(b.N), "loss%")
	br.stop()
	close(dev.closed)
	_ = link.Close()
	_ = core.Close()
	br.wait()
}

// writeOnly — адаптер, из которого нечего читать: Read ждёт закрытия.
type writeOnly struct{ *floodDevice }

func (d *writeOnly) Read([][]byte, []int, int) (int, error) {
	<-d.closed
	return 0, os.ErrClosed
}
