// Package captcha issues one-time, short-lived numeric image captchas for the
// admin login. Images are drawn with the standard library only (a built-in
// 3x5 dot-matrix digit font) so no third-party dependency is required.
package captcha

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"image"
	"image/color"
	"image/png"
	"sync"
	"time"
)

const (
	codeLen    = 4
	ttl        = 5 * time.Minute
	imgWidth   = 120
	imgHeight  = 40
	glyphScale = 5 // 3x5 glyph scaled up to 15x25
)

// digits is a 3x5 dot-matrix font for 0-9; each row uses the low 3 bits
// (bit 2 = leftmost pixel).
var digits = [10][5]uint8{
	{0b111, 0b101, 0b101, 0b101, 0b111}, // 0
	{0b010, 0b110, 0b010, 0b010, 0b111}, // 1
	{0b111, 0b001, 0b111, 0b100, 0b111}, // 2
	{0b111, 0b001, 0b111, 0b001, 0b111}, // 3
	{0b101, 0b101, 0b111, 0b001, 0b001}, // 4
	{0b111, 0b100, 0b111, 0b001, 0b111}, // 5
	{0b111, 0b100, 0b111, 0b101, 0b111}, // 6
	{0b111, 0b001, 0b001, 0b001, 0b001}, // 7
	{0b111, 0b101, 0b111, 0b101, 0b111}, // 8
	{0b111, 0b101, 0b111, 0b001, 0b111}, // 9
}

type entry struct {
	code      string
	expiresAt time.Time
}

// Store keeps pending captcha codes in memory. Codes are one-time use and
// expire after a fixed TTL; expired entries are purged lazily.
type Store struct {
	mu      sync.Mutex
	pending map[string]entry
}

func NewStore() *Store {
	return &Store{pending: map[string]entry{}}
}

// Generate creates a new captcha, returning its ID, the code itself (callers
// serving HTTP must not expose it), and a PNG image.
func (s *Store) Generate() (id, code string, img []byte, err error) {
	code, err = randomCode()
	if err != nil {
		return "", "", nil, err
	}
	idBytes := make([]byte, 16)
	if _, err := rand.Read(idBytes); err != nil {
		return "", "", nil, err
	}
	id = hex.EncodeToString(idBytes)

	s.mu.Lock()
	s.purgeLocked(time.Now())
	s.pending[id] = entry{code: code, expiresAt: time.Now().Add(ttl)}
	s.mu.Unlock()

	img, err = render(code)
	if err != nil {
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
		return "", "", nil, err
	}
	return id, code, img, nil
}

// Verify checks a code against a pending captcha. The captcha is consumed on
// every call — right or wrong — so each image can only be tried once.
func (s *Store) Verify(id, code string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.pending[id]
	if !ok {
		return false
	}
	delete(s.pending, id)
	s.purgeLocked(time.Now())
	return e.code == code && time.Now().Before(e.expiresAt)
}

func (s *Store) purgeLocked(now time.Time) {
	for id, e := range s.pending {
		if now.After(e.expiresAt) {
			delete(s.pending, id)
		}
	}
}

func randomCode() (string, error) {
	b := make([]byte, codeLen)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	out := make([]byte, codeLen)
	for i, v := range b {
		out[i] = '0' + v%10
	}
	return string(out), nil
}

// render draws the code as a noisy PNG: jittered dark digits on a light
// background with interference lines and dots.
func render(code string) ([]byte, error) {
	img := image.NewRGBA(image.Rect(0, 0, imgWidth, imgHeight))
	bg := color.RGBA{245, 246, 248, 255}
	for y := 0; y < imgHeight; y++ {
		for x := 0; x < imgWidth; x++ {
			img.Set(x, y, bg)
		}
	}

	// interference lines
	for i := 0; i < 4; i++ {
		x0, y0 := randInt(imgWidth), randInt(imgHeight)
		x1, y1 := randInt(imgWidth), randInt(imgHeight)
		drawLine(img, x0, y0, x1, y1, randomDarkColor(160))
	}
	// noise dots
	for i := 0; i < 60; i++ {
		img.Set(randInt(imgWidth), randInt(imgHeight), randomDarkColor(200))
	}

	// digits: 15px wide glyphs with 13px gaps, jittered vertically
	for i, ch := range code {
		d := ch - '0'
		if d < 0 || d > 9 {
			d = 0
		}
		xBase := 8 + i*28
		yBase := (imgHeight-5*glyphScale)/2 + randInt(9) - 4
		c := randomDarkColor(255)
		for row := 0; row < 5; row++ {
			for col := 0; col < 3; col++ {
				if digits[d][row]&(1<<(2-col)) == 0 {
					continue
				}
				// filled glyphScale x glyphScale block per font pixel
				for dy := 0; dy < glyphScale; dy++ {
					for dx := 0; dx < glyphScale; dx++ {
						x := xBase + col*glyphScale + dx
						y := yBase + row*glyphScale + dy
						if x >= 0 && x < imgWidth && y >= 0 && y < imgHeight {
							img.Set(x, y, c)
						}
					}
				}
			}
		}
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func drawLine(img *image.RGBA, x0, y0, x1, y1 int, c color.RGBA) {
	dx, dy := abs(x1-x0), -abs(y1-y0)
	sx, sy := 1, 1
	if x0 > x1 {
		sx = -1
	}
	if y0 > y1 {
		sy = -1
	}
	err := dx + dy
	for {
		img.Set(x0, y0, c)
		if x0 == x1 && y0 == y1 {
			return
		}
		e2 := 2 * err
		if e2 >= dy {
			err += dy
			x0 += sx
		}
		if e2 <= dx {
			err += dx
			y0 += sy
		}
	}
}

func randomDarkColor(alpha uint8) color.RGBA {
	return color.RGBA{
		uint8(randInt(120)),
		uint8(randInt(120)),
		uint8(randInt(120)),
		alpha,
	}
}

func randInt(n int) int {
	var b [1]byte
	for {
		if _, err := rand.Read(b[:]); err != nil {
			return 0
		}
		// reject to avoid modulo bias
		if int(b[0]) < 256/n*n {
			return int(b[0]) % n
		}
	}
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
