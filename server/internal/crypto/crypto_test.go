package crypto

import (
	"bytes"
	"encoding/base64"
	"testing"
)

// TestSessionRoundTrip проверяет базовый encrypt → decrypt.
func TestSessionRoundTrip(t *testing.T) {
	t.Parallel()
	key, err := hkdfDerive([]byte("test-secret"), "ctx")
	if err != nil {
		t.Fatalf("derive key: %v", err)
	}
	s, err := NewSession(key)
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	for _, pt := range [][]byte{
		nil,
		[]byte("hello"),
		bytes.Repeat([]byte{0xAB}, 4096),
	} {
		ct, err := s.Encrypt(pt)
		if err != nil {
			t.Fatalf("encrypt: %v", err)
		}
		// Шифротекст должен отличаться для одинаковых plaintext (рандомный nonce).
		ct2, _ := s.Encrypt(pt)
		if bytes.Equal(ct, ct2) && len(pt) > 0 {
			t.Fatalf("nonce не случайный: одинаковый ciphertext для одного plaintext")
		}
		got, err := s.Decrypt(ct)
		if err != nil {
			t.Fatalf("decrypt: %v", err)
		}
		if !bytes.Equal(got, pt) {
			t.Fatalf("payload изменился: got %q want %q", got, pt)
		}
	}
}

// TestSessionRejectsTampered проверяет, что подмена байта детектится (GCM tag).
func TestSessionRejectsTampered(t *testing.T) {
	t.Parallel()
	s := mustSession(t)
	ct, err := s.Encrypt([]byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	ct[len(ct)-1] ^= 0xFF // ломаем тег
	if _, err := s.Decrypt(ct); err == nil {
		t.Fatal("ожидалась ошибка при подмене ciphertext")
	}
}

// TestSessionWrongKey проверяет, что чужой ключ не расшифровывает.
func TestSessionWrongKey(t *testing.T) {
	t.Parallel()
	a := mustSession(t)
	b, err := NewSession(mustKey(t, "other"))
	if err != nil {
		t.Fatal(err)
	}
	ct, _ := a.Encrypt([]byte("data"))
	if _, err := b.Decrypt(ct); err == nil {
		t.Fatal("ожидалась ошибка при расшифровке чужим ключом")
	}
}

// TestDeriveSharedKeyE2E имитирует обмен ECDH между двумя сторонами.
func TestDeriveSharedKeyE2E(t *testing.T) {
	t.Parallel()
	alice, err := GenerateECDHPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	bob, err := GenerateECDHPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	ka, err := DeriveSharedKey(alice, PublicKeyBytes(bob), "spider")
	if err != nil {
		t.Fatal(err)
	}
	kb, err := DeriveSharedKey(bob, PublicKeyBytes(alice), "spider")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(ka, kb) {
		t.Fatal("ключи сторон не совпали после ECDH")
	}
	// Шифруем на одной стороне, дешифруем на другой.
	sa, _ := NewSession(ka)
	sb, _ := NewSession(kb)
	ct, _ := sa.Encrypt([]byte("across parties"))
	got, err := sb.Decrypt(ct)
	if err != nil {
		t.Fatalf("cross-party decrypt: %v", err)
	}
	if string(got) != "across parties" {
		t.Fatalf("payload изменился")
	}
}

// TestEnvelopeRoundTripAnyType проверяет generic-обёртку Envelope.
func TestEnvelopeRoundTripAnyType(t *testing.T) {
	t.Parallel()
	s := mustSession(t)
	type payload struct {
		Cmd  string `json:"cmd"`
		Args []int  `json:"args"`
	}
	in := payload{Cmd: "echo", Args: []int{1, 2, 3}}
	env, err := SealEnvelope(s, MsgCommand, in)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if env.Type != MsgCommand {
		t.Fatalf("type: got %q", env.Type)
	}
	if _, err := base64.StdEncoding.DecodeString(env.Data); err != nil {
		t.Fatalf("data не base64: %v", err)
	}
	var out payload
	if err := OpenEnvelope(s, env, &out); err != nil {
		t.Fatalf("open: %v", err)
	}
	if out.Cmd != in.Cmd || len(out.Args) != 3 {
		t.Fatalf("payload не совпал: %+v", out)
	}
}

// TestNewSessionBadKey проверяет валидацию длины ключа.
func TestNewSessionBadKey(t *testing.T) {
	t.Parallel()
	if _, err := NewSession([]byte("short")); err == nil {
		t.Fatal("ожидалась ошибка для короткого ключа")
	}
}

func mustSession(t *testing.T) *Session {
	t.Helper()
	s, err := NewSession(mustKey(t, "k"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func mustKey(t *testing.T, context string) []byte {
	t.Helper()
	k, err := hkdfDerive([]byte("seed-"+context), context)
	if err != nil {
		t.Fatal(err)
	}
	return k
}
