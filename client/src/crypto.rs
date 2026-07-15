//! Прикладной (поверх TLS) крипто-слой агента. Зеркало серверного пакета
//! `server/internal/crypto`:
//!
//! - X25519 ECDH → HKDF-SHA256 → симметричный ключ сессии;
//! - AES-256-GCM (nonce 12 байт) для каждого сообщения;
//! - универсальный [`Envelope`] для WS и long-poll.
//!
//! Формат wire идентичен серверному: `base64(Encrypt(json(payload)))`.

use aes_gcm::aead::{Aead, KeyInit, OsRng as AeadOsRng};
use aes_gcm::{AeadCore, Aes256Gcm, Key, Nonce};
use anyhow::{anyhow, Context, Result};
use base64::Engine;
use hkdf::Hkdf;
use serde::de::DeserializeOwned;
use serde::Serialize;
use sha2::Sha256;
use x25519_dalek::{PublicKey, StaticSecret};

/// Размер симметричного ключа AES-256 (байт).
pub const KEY_SIZE: usize = 32;
/// Размер nonce для AES-GCM (байт).
pub const NONCE_SIZE: usize = 12;

/// Сгенерировать новую эфемерную пару X25519.
///
/// Генерируем секретные байты сами (через rand) и строим StaticSecret через
/// `from([u8;32])` — это надёжнее межкрейтовых диссонансов random_from_rng.
pub fn generate_keypair() -> (StaticSecret, PublicKey) {
    use rand::RngCore;
    let mut bytes = [0u8; 32];
    rand::rngs::OsRng.fill_bytes(&mut bytes);
    let secret = StaticSecret::from(bytes);
    let public = PublicKey::from(&secret);
    (secret, public)
}

/// Вывести симметричный ключ из общего секрета ECDH через HKDF-SHA256.
pub fn derive_shared_key(
    our_secret: &StaticSecret,
    their_public: &[u8],
    context: &str,
) -> Result<[u8; KEY_SIZE]> {
    if their_public.len() != 32 {
        return Err(anyhow!(
            "crypto: peer public key must be 32 bytes, got {}",
            their_public.len()
        ));
    }
    let mut pk = [0u8; 32];
    pk.copy_from_slice(their_public);
    let their = PublicKey::from(pk);
    let shared = our_secret.diffie_hellman(&their);

    let info = format!("spider/v1/{}", context);
    let hk = Hkdf::<Sha256>::new(None, shared.as_bytes());
    let mut okm = [0u8; KEY_SIZE];
    hk.expand(info.as_bytes(), &mut okm)
        .map_err(|e| anyhow!("crypto: hkdf expand: {e}"))?;
    Ok(okm)
}

/// Шифрованная сессия поверх готового симметричного ключа.
pub struct Session {
    cipher: Aes256Gcm,
}

impl Session {
    /// Создать сессию из 32-байтного ключа.
    pub fn new(key: &[u8]) -> Result<Self> {
        if key.len() != KEY_SIZE {
            return Err(anyhow!(
                "crypto: key must be {KEY_SIZE} bytes, got {}",
                key.len()
            ));
        }
        let key = Key::<Aes256Gcm>::from_slice(key);
        Ok(Self {
            cipher: Aes256Gcm::new(key),
        })
    }

    /// Зашифровать: возвращает nonce||ciphertext.
    pub fn encrypt(&self, plaintext: &[u8]) -> Result<Vec<u8>> {
        let nonce = Aes256Gcm::generate_nonce(&mut AeadOsRng);
        let ct = self
            .cipher
            .encrypt(&nonce, plaintext)
            .map_err(|e| anyhow!("crypto: gcm encrypt: {e}"))?;
        let mut out = Vec::with_capacity(NONCE_SIZE + ct.len());
        out.extend_from_slice(&nonce.as_slice());
        out.extend_from_slice(&ct);
        Ok(out)
    }

    /// Расшифровать данные формата nonce||ciphertext.
    pub fn decrypt(&self, blob: &[u8]) -> Result<Vec<u8>> {
        if blob.len() < NONCE_SIZE {
            return Err(anyhow!("crypto: ciphertext too short"));
        }
        let (nonce_bytes, ct) = blob.split_at(NONCE_SIZE);
        let nonce = Nonce::from_slice(nonce_bytes);
        self.cipher
            .decrypt(nonce, ct)
            .map_err(|e| anyhow!("crypto: gcm decrypt: {e}"))
    }
}

/// Универсальный конверт сообщения (WS и long-poll).
#[derive(Debug, Clone, serde::Serialize, serde::Deserialize)]
pub struct Envelope {
    /// Тип сообщения (см. модуль [`crate::proto`]).
    #[serde(rename = "type")]
    pub ty: String,
    /// base64(Encrypt(json(payload))).
    pub data: String,
}

impl Envelope {
    /// Зашифровать и упаковать payload.
    pub fn seal<T: Serialize>(session: &Session, ty: &str, payload: &T) -> Result<Self> {
        let raw = serde_json::to_vec(payload).context("crypto: serialize payload")?;
        let ct = session.encrypt(&raw)?;
        Ok(Self {
            ty: ty.to_string(),
            data: base64::engine::general_purpose::STANDARD.encode(ct),
        })
    }

    /// Расшифровать и десериализовать payload.
    pub fn open<T: DeserializeOwned>(&self, session: &Session) -> Result<T> {
        let ct = base64::engine::general_purpose::STANDARD
            .decode(&self.data)
            .context("crypto: base64 decode")?;
        let raw = session.decrypt(&ct)?;
        serde_json::from_slice(&raw).context("crypto: deserialize payload")
    }
}

/// Сгенерировать n случайных байт.
pub fn random_bytes(n: usize) -> Result<Vec<u8>> {
    use rand::RngCore;
    let mut out = vec![0u8; n];
    rand::rngs::OsRng.fill_bytes(&mut out);
    Ok(out)
}

/// Декодировать base64-строку в байты.
pub fn b64_decode(s: &str) -> Result<Vec<u8>> {
    Ok(base64::engine::general_purpose::STANDARD.decode(s)?)
}

/// Закодировать байты в base64-строку.
pub fn b64_encode(b: &[u8]) -> String {
    base64::engine::general_purpose::STANDARD.encode(b)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn demo_key() -> [u8; KEY_SIZE] {
        let mut k = [0u8; KEY_SIZE];
        for (i, b) in k.iter_mut().enumerate() {
            *b = i as u8;
        }
        k
    }

    #[test]
    fn round_trip() {
        let s = Session::new(&demo_key()).unwrap();
        for pt in [b"".to_vec(), b"hello".to_vec(), vec![0xAB; 4096]] {
            let ct = s.encrypt(&pt).unwrap();
            assert_eq!(s.decrypt(&ct).unwrap(), pt);
        }
    }

    #[test]
    fn nonce_is_random() {
        let s = Session::new(&demo_key()).unwrap();
        let a = s.encrypt(b"x").unwrap();
        let b = s.encrypt(b"x").unwrap();
        assert_ne!(a, b, "nonce должен быть случайным");
    }

    #[test]
    fn tamper_detected() {
        let s = Session::new(&demo_key()).unwrap();
        let mut ct = s.encrypt(b"secret").unwrap();
        let last = ct.len() - 1;
        ct[last] ^= 0xFF;
        assert!(s.decrypt(&ct).is_err());
    }

    #[test]
    fn ecdh_keys_match() {
        let (sa, pa) = generate_keypair();
        let (sb, pb) = generate_keypair();
        let ka = derive_shared_key(&sa, pb.as_bytes(), "ctx").unwrap();
        let kb = derive_shared_key(&sb, pa.as_bytes(), "ctx").unwrap();
        assert_eq!(ka, kb, "производные ключи должны совпадать после ECDH");
        // шифруем на одной стороне, дешифруем на другой
        let ea = Session::new(&ka).unwrap();
        let eb = Session::new(&kb).unwrap();
        let ct = ea.encrypt(b"across").unwrap();
        assert_eq!(eb.decrypt(&ct).unwrap(), b"across");
    }

    #[test]
    fn envelope_round_trip() {
        let s = Session::new(&demo_key()).unwrap();
        #[derive(serde::Serialize, serde::Deserialize, PartialEq, Debug)]
        struct P {
            cmd: String,
            n: i32,
        }
        let env = Envelope::seal(&s, "command", &P { cmd: "ls".into(), n: 7 }).unwrap();
        assert_eq!(env.ty, "command");
        let got: P = env.open(&s).unwrap();
        assert_eq!(got, P { cmd: "ls".into(), n: 7 });
    }

    #[test]
    fn bad_key_rejected() {
        assert!(Session::new(b"short").is_err());
    }
}
