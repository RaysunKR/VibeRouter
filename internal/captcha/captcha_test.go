package captcha

import (
	"bytes"
	"image/png"
	"testing"
	"time"
)

func TestGenerateProducesValidPNG(t *testing.T) {
	s := NewStore()
	id, code, img, err := s.Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if id == "" {
		t.Fatal("empty captcha id")
	}
	if len(code) != codeLen {
		t.Fatalf("code length = %d, want %d", len(code), codeLen)
	}
	decoded, err := png.Decode(bytes.NewReader(img))
	if err != nil {
		t.Fatalf("decode png: %v", err)
	}
	if b := decoded.Bounds(); b.Dx() != imgWidth || b.Dy() != imgHeight {
		t.Fatalf("unexpected size %v", b)
	}
}

func TestVerifyCorrectCode(t *testing.T) {
	s := NewStore()
	id, code, _, err := s.Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !s.Verify(id, code) {
		t.Fatal("correct code rejected")
	}
}

func TestVerifyWrongCode(t *testing.T) {
	s := NewStore()
	id, code, _, err := s.Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	wrong := "0000"
	if wrong == code {
		wrong = "1111"
	}
	if s.Verify(id, wrong) {
		t.Fatal("wrong code accepted")
	}
}

func TestVerifyOneTimeUse(t *testing.T) {
	s := NewStore()
	id, code, _, err := s.Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !s.Verify(id, code) {
		t.Fatal("first verify failed")
	}
	if s.Verify(id, code) {
		t.Fatal("captcha reused successfully")
	}
}

func TestVerifyExpired(t *testing.T) {
	s := NewStore()
	id, code, _, err := s.Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	e := s.pending[id]
	e.expiresAt = time.Now().Add(-time.Minute)
	s.pending[id] = e
	if s.Verify(id, code) {
		t.Fatal("expired captcha accepted")
	}
}

func TestVerifyUnknownID(t *testing.T) {
	s := NewStore()
	if s.Verify("nonexistent", "1234") {
		t.Fatal("unknown id accepted")
	}
}
