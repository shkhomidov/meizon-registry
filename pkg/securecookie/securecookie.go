// Copyright (c) 2026 Meizon Inc.
//
// Permission to use, copy, modify, and/or distribute this software for any
// purpose with or without fee is hereby granted, provided that the above
// copyright notice and this permission notice appear in all copies.
//
// THE SOFTWARE IS PROVIDED "AS IS" AND THE AUTHOR DISCLAIMS ALL WARRANTIES WITH
// REGARD TO THIS SOFTWARE INCLUDING ALL IMPLIED WARRANTIES OF MERCHANTABILITY
// AND FITNESS. IN NO EVENT SHALL THE AUTHOR BE LIABLE FOR ANY SPECIAL, DIRECT,
// INDIRECT, OR CONSEQUENTIAL DAMAGES OR ANY DAMAGES WHATSOEVER RESULTING FROM
// LOSS OF USE, DATA OR PROFITS, WHETHER IN AN ACTION OF CONTRACT, NEGLIGENCE OR
// OTHER TORTIOUS ACTION, ARISING OUT OF OR IN CONNECTION WITH THE USE OR
// PERFORMANCE OF THIS SOFTWARE.

// Package securecookie mints and verifies tamper-proof session cookie values.
// A value is "payload|expiryUnix" authenticated with HMAC-SHA256 under a
// server-side secret; the whole thing is base64url-encoded. There is no
// encryption — the payload (an identity id) is not secret, only unforgeable.
package securecookie

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

var (
	// ErrInvalid is returned for a malformed, tampered or expired value.
	ErrInvalid = errors.New("invalid cookie")
)

// Codec signs and verifies cookie values.
type Codec struct {
	secret []byte
}

// NewCodec returns a codec. The secret must be non-empty.
func NewCodec(secret []byte) (*Codec, error) {
	if len(secret) < 16 {
		return nil, fmt.Errorf("cookie secret must be at least 16 bytes")
	}
	return &Codec{secret: secret}, nil
}

// Encode returns a signed value carrying payload, valid for ttl.
func (c *Codec) Encode(payload string, ttl time.Duration) string {
	exp := time.Now().Add(ttl).Unix()
	body := payload + "|" + strconv.FormatInt(exp, 10)
	mac := c.sign(body)
	return base64.RawURLEncoding.EncodeToString([]byte(body + "|" + mac))
}

// Decode verifies a value and returns its payload, or an error if it is invalid
// or expired.
func (c *Codec) Decode(value string) (string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return "", ErrInvalid
	}

	parts := strings.Split(string(raw), "|")
	if len(parts) != 3 {
		return "", ErrInvalid
	}
	payload, expStr, mac := parts[0], parts[1], parts[2]

	body := payload + "|" + expStr
	if !hmac.Equal([]byte(mac), []byte(c.sign(body))) {
		return "", ErrInvalid
	}

	exp, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil {
		return "", ErrInvalid
	}
	if time.Now().Unix() > exp {
		return "", ErrInvalid
	}

	return payload, nil
}

func (c *Codec) sign(body string) string {
	m := hmac.New(sha256.New, c.secret)
	m.Write([]byte(body))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}
