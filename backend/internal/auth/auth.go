package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/argon2"
)

// Параметры argon2id (рекомендации OWASP).
const (
	argonTime    = 2
	argonMemory  = 19 * 1024
	argonThreads = 1
	argonKeyLen  = 32
)

func HashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s", argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

func CheckPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, t, m, p, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// dummyHash используется, чтобы время ответа при неизвестном email совпадало с проверкой пароля.
var dummyHash, _ = HashPassword("dummy-password-for-timing")

func DummyCheck(password string) { CheckPassword(dummyHash, password) }

func NewToken() (raw string, hash []byte, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return
	}
	raw = base64.RawURLEncoding.EncodeToString(b)
	return raw, HashToken(raw), nil
}

func HashToken(raw string) []byte {
	h := sha256.Sum256([]byte(raw))
	return h[:]
}

// Signer подписывает короткоживущие токены вида payload.exp.sig (HMAC-SHA256).
type Signer struct{ key []byte }

func NewSigner(secret string) *Signer { return &Signer{key: []byte(secret)} }

func (s *Signer) mac(msg string) string {
	m := hmac.New(sha256.New, s.key)
	m.Write([]byte(msg))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func (s *Signer) Sign(purpose, payload string, ttl time.Duration) string {
	exp := strconv.FormatInt(time.Now().Add(ttl).Unix(), 10)
	p := base64.RawURLEncoding.EncodeToString([]byte(payload))
	return p + "." + exp + "." + s.mac(purpose+"|"+p+"|"+exp)
}

var ErrBadToken = errors.New("invalid or expired token")

func (s *Signer) Verify(purpose, token string) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", ErrBadToken
	}
	if !hmac.Equal([]byte(s.mac(purpose+"|"+parts[0]+"|"+parts[1])), []byte(parts[2])) {
		return "", ErrBadToken
	}
	exp, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return "", ErrBadToken
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", ErrBadToken
	}
	return string(payload), nil
}
