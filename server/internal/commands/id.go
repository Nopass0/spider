package commands

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

// newID генерирует компактный уникальный идентификатор: hex(4 байта unix-секунд
// + 8 байт рандома). Достаточно уникален и лексикографически сортируем по времени.
func newID() string {
	var b [12]byte
	t := uint32(time.Now().Unix())
	b[0] = byte(t >> 24)
	b[1] = byte(t >> 16)
	b[2] = byte(t >> 8)
	b[3] = byte(t)
	if _, err := rand.Read(b[4:]); err != nil {
		// крайне маловероятно; fallback на чистый рандом-через-ошибку
		panic(fmt.Sprintf("commands: rand: %v", err))
	}
	return hex.EncodeToString(b[:])
}
