package datapath

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"time"

	"gvisor.dev/gvisor/pkg/tcpip"
)

// SOCKS5 по RFC 1928: без пароля и только CONNECT.

const socksVersion = 5

const (
	methodNone         = 0x00
	methodNoAcceptable = 0xff
)

const (
	cmdConnect = 0x01

	atypIPv4   = 0x01
	atypDomain = 0x03
	atypIPv6   = 0x04
)

// Коды ответа (RFC 1928, §6).
const (
	repSucceeded           = 0x00
	repFailure             = 0x01
	repNetworkUnreachable  = 0x03
	repHostUnreachable     = 0x04
	repConnectionRefused   = 0x05
	repCommandNotSupported = 0x07
	repAddressNotSupported = 0x08
)

const (
	// handshakeTimeout — на приветствие и запрос: молчащий клиент не держит
	// горутину вечно.
	handshakeTimeout = 10 * time.Second
	// connectTimeout — на поиск имени и подключение к цели.
	connectTimeout = 30 * time.Second
)

// socksRequest — запрос CONNECT: имя или адрес IPv4 и порт.
type socksRequest struct {
	host string
	addr netip.Addr
	port uint16
}

func (r socksRequest) String() string {
	switch {
	case r.host != "":
		return net.JoinHostPort(r.host, fmt.Sprint(r.port))
	case r.addr.IsValid():
		return netip.AddrPortFrom(r.addr, r.port).String()
	}
	return fmt.Sprintf("запрос к порту %d", r.port)
}

// socksError — запрос не выполнить; rep — код ответа клиенту.
type socksError struct {
	rep byte
	err error
}

func (e *socksError) Error() string { return e.err.Error() }
func (e *socksError) Unwrap() error { return e.err }

// errProtocol — клиент говорит не на SOCKS5: ответить ему нечем.
var errProtocol = errors.New("не SOCKS5")

// negotiate — приветствие: из предложенных методов годится только «без
// пароля».
func negotiate(c io.ReadWriter) error {
	var head [2]byte
	if _, err := io.ReadFull(c, head[:]); err != nil {
		return err
	}
	if head[0] != socksVersion {
		return errProtocol
	}
	methods := make([]byte, head[1])
	if _, err := io.ReadFull(c, methods); err != nil {
		return err
	}
	for _, m := range methods {
		if m == methodNone {
			_, err := c.Write([]byte{socksVersion, methodNone})
			return err
		}
	}
	_, _ = c.Write([]byte{socksVersion, methodNoAcceptable})
	return errors.New("клиент требует пароль")
}

// readRequest читает запрос. Ошибка *socksError — запрос прочитан целиком,
// но не поддерживается: клиенту уходит её код.
func readRequest(c io.Reader) (socksRequest, error) {
	var head [4]byte
	if _, err := io.ReadFull(c, head[:]); err != nil {
		return socksRequest{}, err
	}
	if head[0] != socksVersion {
		return socksRequest{}, errProtocol
	}
	var req socksRequest
	switch head[3] {
	case atypIPv4:
		var a [4]byte
		if _, err := io.ReadFull(c, a[:]); err != nil {
			return req, err
		}
		req.addr = netip.AddrFrom4(a)
	case atypDomain:
		var n [1]byte
		if _, err := io.ReadFull(c, n[:]); err != nil {
			return req, err
		}
		name := make([]byte, n[0])
		if _, err := io.ReadFull(c, name); err != nil {
			return req, err
		}
		req.host = string(name)
	case atypIPv6:
		var a [16]byte
		if _, err := io.ReadFull(c, a[:]); err != nil {
			return req, err
		}
		req.addr = netip.AddrFrom16(a)
	default:
		// Длина адреса неизвестна — дочитать запрос нельзя, но ответить можно.
		return req, &socksError{repAddressNotSupported, fmt.Errorf("тип адреса %#x", head[3])}
	}
	var port [2]byte
	if _, err := io.ReadFull(c, port[:]); err != nil {
		return req, err
	}
	req.port = binary.BigEndian.Uint16(port[:])

	if head[1] != cmdConnect {
		return req, &socksError{repCommandNotSupported, fmt.Errorf("команда %#x", head[1])}
	}
	// Сервер выдаёт туннелю только IPv4: адрес IPv6 отклоняется сразу, и
	// программа без ожидания переходит к IPv4.
	if a, err := netip.ParseAddr(req.host); err == nil {
		req.host, req.addr = "", a
	}
	if req.addr = req.addr.Unmap(); req.addr.Is6() {
		return req, &socksError{repAddressNotSupported, errors.New("IPv6 туннель не возит")}
	}
	if req.host == "" && !req.addr.IsValid() {
		return req, &socksError{repHostUnreachable, errors.New("пустое имя")}
	}
	return req, nil
}

// writeReply — ответ на запрос; bound — адрес соединения со стороны
// туннеля, при отказе — 0.0.0.0:0.
func writeReply(c io.Writer, rep byte, bound netip.AddrPort) error {
	b := make([]byte, 0, 10)
	b = append(b, socksVersion, rep, 0, atypIPv4)
	a := bound.Addr().Unmap()
	if !a.Is4() {
		a = netip.IPv4Unspecified()
	}
	b = append(b, a.AsSlice()...)
	b = binary.BigEndian.AppendUint16(b, bound.Port())
	_, err := c.Write(b)
	return err
}

// replyCode — код ответа на неудавшееся подключение.
func replyCode(err error) byte {
	var se *socksError
	switch terr := tcpipError(err); {
	case errors.As(err, &se):
		return se.rep
	case errors.Is(err, errNoDNS), errors.Is(err, errNotFound), errors.Is(err, errNoAddress):
		return repHostUnreachable
	case isTCPIP[*tcpip.ErrConnectionRefused](terr):
		return repConnectionRefused
	case isTCPIP[*tcpip.ErrNetworkUnreachable](terr):
		return repNetworkUnreachable
	case isTCPIP[*tcpip.ErrHostUnreachable](terr), isTCPIP[*tcpip.ErrTimeout](terr), errTimeout(err):
		return repHostUnreachable
	}
	return repFailure
}

func isTCPIP[T tcpip.Error](err tcpip.Error) bool {
	_, ok := err.(T)
	return ok
}

// relay гоняет данные в обе стороны до конца обеих. Конец одной стороны
// передаётся другой полузакрытием: HTTP/1.0 и подобные ждут его, чтобы
// ответить. Ошибка в одну сторону (сброс, закрытие) рвёт обе: иначе
// вторая ждала бы данных от уже мёртвого соединения.
func relay(a, b net.Conn) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		pass(b, a)
	}()
	pass(a, b)
	<-done
}

func pass(to, from net.Conn) {
	if _, err := io.Copy(to, from); err != nil {
		_ = to.Close()
		_ = from.Close()
		return
	}
	if cw, ok := to.(interface{ CloseWrite() error }); ok {
		_ = cw.CloseWrite()
		return
	}
	_ = to.Close()
}
