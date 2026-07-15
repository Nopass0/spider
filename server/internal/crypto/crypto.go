// Package crypto реализует прикладной (поверх TLS) слой шифрования сообщений
// между сервером и клиентом Spider.
//
// Протокол:
//
//	1. Стороны обмениваются эфемерными ключами X25519 (ECDH) во время enrollment.
//	   Из общего секрета выводится симметричный ключ через HKDF-SHA256.
//	2. Каждое сообщение шифруется AES-256-GCM со случайным nonce (12 байт).
//	3. Сообщения переносятся в виде Envelope (см. ниже) как над TLS, так и внутри него.
//
// Все функции потокобезопасны, если использовать независимые *Session.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/hkdf"
	"golang.org/x/crypto/sha3"
)

// KeySize — размер симметричного ключа AES-256 (в байтах).
const KeySize = 32

// NonceSize — размер nonce для AES-GCM (в байтах).
const NonceSize = 12

// randRead переопределяется в тестах для детерминированных nonce.
var randRead = rand.Read

// ===========================================================================
// Генерация ключей
// ===========================================================================

// GenerateECDHPrivateKey создаёт новый эфемерный приватный ключ X25519.
// Возвращает ошибку, если источник энтропии недоступен.
func GenerateECDHPrivateKey() (*ecdh.PrivateKey, error) {
	return ecdh.X25519().GenerateKey(rand.Reader)
}

// PublicKeyBytes возвращает публичный ключ X25519 в сыром виде (32 байта).
func PublicKeyBytes(k *ecdh.PrivateKey) []byte {
	return k.PublicKey().Bytes()
}

// DeriveSharedKey выполняет ECDH и выводит из общего секрета симметричный ключ
// через HKDF-SHA256 с info, привязанным к контексту протокола Spider.
// Возвращает ключ длины KeySize.
func DeriveSharedKey(priv *ecdh.PrivateKey, peerPub []byte, context string) ([]byte, error) {
	if len(peerPub) != 32 {
		return nil, fmt.Errorf("crypto: peer public key must be 32 bytes, got %d", len(peerPub))
	}
	peer, err := ecdh.X25519().NewPublicKey(peerPub)
	if err != nil {
		return nil, fmt.Errorf("crypto: invalid peer public key: %w", err)
	}
	secret, err := priv.ECDH(peer)
	if err != nil {
		return nil, fmt.Errorf("crypto: ecdh: %w", err)
	}
	return hkdfDerive(secret, context)
}

// hkdfDerive выводит ключ из секрета через HKDF-SHA256.
func hkdfDerive(secret []byte, context string) ([]byte, error) {
	out := make([]byte, KeySize)
	r := hkdf.New(sha3.New256, secret, nil, []byte("spider/v1/"+context))
	if _, err := io.ReadFull(r, out); err != nil {
		return nil, fmt.Errorf("crypto: hkdf: %w", err)
	}
	return out, nil
}

// ===========================================================================
// Session — симметричный шифрованный канал
// ===========================================================================

// Session инкапсулирует симметричный ключ и шифрует/расшифровывает произвольные данные.
type Session struct {
	aead cipher.AEAD
}

// NewSession создаёт сессию из готового симметричного ключа (KeySize байт).
func NewSession(key []byte) (*Session, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("crypto: key must be %d bytes, got %d", KeySize, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("crypto: aes new: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("crypto: gcm new: %w", err)
	}
	return &Session{aead: aead}, nil
}

// Encrypt шифрует plaintext, возвращая nonce||ciphertext.
func (s *Session) Encrypt(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, NonceSize)
	if _, err := randRead(nonce); err != nil {
		return nil, fmt.Errorf("crypto: rand nonce: %w", err)
	}
	// Seal дописывает тег в конец; результат кладётся после nonce.
	ct := s.aead.Seal(nil, nonce, plaintext, nil)
	out := make([]byte, 0, NonceSize+len(ct))
	out = append(out, nonce...)
	out = append(out, ct...)
	return out, nil
}

// Decrypt расшифровывает данные формата nonce||ciphertext.
func (s *Session) Decrypt(blob []byte) ([]byte, error) {
	if len(blob) < NonceSize {
		return nil, errors.New("crypto: ciphertext too short")
	}
	nonce, ct := blob[:NonceSize], blob[NonceSize:]
	pt, err := s.aead.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, fmt.Errorf("crypto: gcm open: %w", err)
	}
	return pt, nil
}

// ===========================================================================
// Envelope — формат сообщения над транспортами (WS / long-poll)
// ===========================================================================

// Envelope — общая посылка: произвольный JSON-сериализуемый payload,
// зашифрованный в поле Data (base64). Один формат — оба транспорта (DRY).
//
// Формат wire: base64(Encrypt(json.Marshal(payload))).
type Envelope struct {
	// Type — тип сообщения (см. MessageType* константы).
	Type string `json:"type"`
	// Data — base64(Encrypt(json(payload))).
	Data string `json:"data"`
}

// SealEnvelope кодирует payload в JSON, шифрует и упаковывает в Envelope.
func SealEnvelope[T any](s *Session, msgType string, payload T) (Envelope, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return Envelope{}, fmt.Errorf("crypto: marshal payload: %w", err)
	}
	ct, err := s.Encrypt(raw)
	if err != nil {
		return Envelope{}, err
	}
	return Envelope{Type: msgType, Data: base64.StdEncoding.EncodeToString(ct)}, nil
}

// OpenEnvelope расшифровывает и декодирует payload из Envelope в target.
func OpenEnvelope[T any](s *Session, env Envelope, target *T) error {
	ct, err := base64.StdEncoding.DecodeString(env.Data)
	if err != nil {
		return fmt.Errorf("crypto: b64 decode: %w", err)
	}
	raw, err := s.Decrypt(ct)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return fmt.Errorf("crypto: unmarshal payload: %w", err)
	}
	return nil
}

// ===========================================================================
// Служебное
// ===========================================================================

// RandomBytes возвращает n случайных байт.
func RandomBytes(n int) ([]byte, error) {
	b := make([]byte, n)
	if _, err := randRead(b); err != nil {
		return nil, err
	}
	return b, nil
}

// RandomHex возвращает n случайных байт в hex-представлении (длина строки 2*n).
func RandomHex(n int) (string, error) {
	b, err := RandomBytes(n)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", b), nil
}
