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

// Package gid implements the 24-byte globally unique identifier used across the
// registry. Every persisted entity is keyed by a GID that embeds its owning
// tenant, its entity type, a creation timestamp and random bytes, so tenant
// isolation and type dispatch can be derived from the id alone.
package gid

import (
	"crypto/rand"
	"database/sql/driver"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"time"
)

const (
	GIDSize = 24 // 192 bits total
)

type (
	GID [GIDSize]byte
)

var (
	Nil = GID{}

	EncodedGIDSize = base64.RawURLEncoding.EncodedLen(len(Nil))
)

// ParseGID parses a string representation of a GID.
func ParseGID(encoded string) (GID, error) {
	gid := GID{}

	err := gid.UnmarshalText([]byte(encoded))
	if err != nil {
		return Nil, err
	}

	return gid, nil
}

// MustParseGID parses a GID string and panics if it is invalid.
func MustParseGID(encoded string) GID {
	id, err := ParseGID(encoded)
	if err != nil {
		panic(fmt.Sprintf("invalid GID: %s", encoded))
	}

	return id
}

// New creates a new GID for the given tenant and entity type. It panics only if
// the system random source fails, which should never happen in practice.
func New(tenantID TenantID, entityType uint16) GID {
	id, err := NewGID(tenantID, entityType)
	if err != nil {
		panic(fmt.Sprintf("cannot generate GID: %v", err))
	}

	return id
}

// NewGID creates a new GID with the specified entity type and tenant ID.
// Structure:
//   - Bytes 0-7:   Tenant ID (8 bytes)
//   - Bytes 8-9:   Entity Type (uint16)
//   - Bytes 10-17: Timestamp (milliseconds since epoch)
//   - Bytes 18-23: Random data for uniqueness
func NewGID(tenantID TenantID, entityType uint16) (GID, error) {
	var id GID

	copy(id[0:8], tenantID[:])
	binary.BigEndian.PutUint16(id[8:10], entityType)

	now := time.Now().UnixMilli()
	binary.BigEndian.PutUint64(id[10:18], uint64(now))

	if _, err := rand.Read(id[18:24]); err != nil {
		return Nil, fmt.Errorf("cannot generate random bytes: %v", err)
	}

	return id, nil
}

// Value implements the database/sql/driver.Valuer interface.
func (gid GID) Value() (driver.Value, error) {
	return gid.String(), nil
}

// TenantID extracts the tenant ID from the GID.
func (gid GID) TenantID() TenantID {
	var tenantID TenantID
	copy(tenantID[:], gid[0:8])

	return tenantID
}

// EntityType extracts the entity type from the GID.
func (gid GID) EntityType() uint16 {
	return binary.BigEndian.Uint16(gid[8:10])
}

// Timestamp extracts the timestamp from the GID.
func (gid GID) Timestamp() time.Time {
	millis := binary.BigEndian.Uint64(gid[10:18])
	return time.UnixMilli(int64(millis))
}

// IsValid reports whether the GID is not the nil value.
func (gid GID) IsValid() bool {
	return gid != Nil
}

// Scan implements the database/sql/driver.Scanner interface.
func (gid *GID) Scan(value any) error {
	var str string

	switch v := value.(type) {
	case string:
		str = v
	case []byte:
		str = string(v)
	default:
		return fmt.Errorf("invalid type %T for GID", value)
	}

	id, err := base64.RawURLEncoding.DecodeString(str)
	if err != nil {
		return err
	}

	if len(id) != GIDSize {
		return fmt.Errorf("invalid length for GID: got %d, want %d", len(id), GIDSize)
	}

	copy((*gid)[:], id)

	return nil
}

// String returns the base64url encoded representation of the GID.
func (gid GID) String() string {
	return base64.RawURLEncoding.EncodeToString(gid[:])
}

// MarshalText returns the base64url encoded representation of the GID.
func (gid GID) MarshalText() ([]byte, error) {
	enc := base64.RawURLEncoding
	buf := make([]byte, enc.EncodedLen(len(gid)))
	enc.Encode(buf, gid[:])

	return buf, nil
}

// UnmarshalText decodes a base64url encoded GID.
func (gid *GID) UnmarshalText(encoded []byte) error {
	enc := base64.RawURLEncoding
	dst := make([]byte, enc.DecodedLen(len(encoded)))

	n, err := enc.Decode(dst, encoded)
	if err != nil {
		return err
	}

	if n != GIDSize {
		return fmt.Errorf("invalid length for GID: got %d, want %d", n, GIDSize)
	}

	copy((*gid)[:], dst)

	return nil
}
