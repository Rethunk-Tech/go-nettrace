package nettrace

import (
	"bytes"
	"strings"
	"testing"
)

func FuzzParse(f *testing.F) {
	trace := readFixture(f)
	f.Add(trace)
	f.Add(trace[:len(trace)/2])
	f.Add(trace[:200])
	f.Add([]byte("Nettrace"))
	mangled := bytes.Clone(trace[:16<<10])
	copy(mangled[12:32], strings.Repeat("x", 20))
	f.Add(mangled)
	zeroed := bytes.Clone(trace[:16<<10])
	clear(zeroed[8:64])
	f.Add(zeroed)
	f.Fuzz(func(_ *testing.T, data []byte) {
		_, _ = Parse(bytes.NewReader(data))
	})
}
