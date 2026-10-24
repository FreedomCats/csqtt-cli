package coreproc

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// epoch — эпоха подключения: номер (-gen) и соль (-salt), которые ядро шлёт
// серверу в GETCONF, а соль ещё и в DISCONNECT при остановке. Сервер держит
// на пароль одну эпоху: новая пара вытесняет сессии прежней, а отключение с
// пустой или чужой солью он отвергает, и сессии живут до тайм-аута. Поэтому
// каждый процесс ядра получает свою пару (Start), и это не настройка
// профиля: на диск она не попадает.
type epoch struct {
	gen  uint64
	salt string
}

// epochSaltBytes — длина соли в случайных байтах; в аргумент она идёт hex,
// вдвое длиннее. Сервер принимает соль до 128 байт.
const epochSaltBytes = 16

var (
	epochMu  sync.Mutex
	epochGen uint64
)

// newEpoch — новая эпоха: номер — время Unix в миллисекундах, но строго
// больше выданного раньше в этом процессе оболочки (часы могут стоять или
// идти назад), соль — из crypto/rand. Монотонности номера сервер не требует,
// он сравнивает пару на равенство; возрастание — чтобы пары двух запусков
// подряд не совпали и по номеру.
func newEpoch() epoch {
	salt := make([]byte, epochSaltBytes)
	_, _ = rand.Read(salt) // с Go 1.24 crypto/rand.Read ошибок не возвращает
	epochMu.Lock()
	defer epochMu.Unlock()
	epochGen = max(uint64(time.Now().UnixMilli()), epochGen+1)
	return epoch{gen: epochGen, salt: hex.EncodeToString(salt)}
}
