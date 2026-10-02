package jsoncanon

import (
	"context"
	"os"
	"testing"
)

func BenchmarkRepresentativeEvent(b *testing.B) {
	body, err := os.ReadFile("../testdata/record-update.json")
	if err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := Stringify(context.Background(), body); err != nil {
				b.Fatal(err)
			}
		}
	})
}
