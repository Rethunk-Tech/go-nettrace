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
	f.Fuzz(func(t *testing.T, data []byte) {
		got, err := Parse(bytes.NewReader(data))
		if err != nil {
			if got != nil {
				t.Fatal("Parse returned a trace with an error")
			}
			return
		}
		if got == nil {
			t.Fatal("Parse returned nil without an error")
		}
		if got.Assemblies == nil || got.Modules == nil {
			t.Fatal("Parse returned nil maps")
		}
		for i := 1; i < len(got.Samples); i++ {
			if got.Samples[i].Timestamp < got.Samples[i-1].Timestamp {
				t.Fatalf("samples not ordered by timestamp at %d", i)
			}
		}
	})
}
