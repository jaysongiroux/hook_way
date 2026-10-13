package secretref

import (
	"bytes"
	"context"
	"os"
)

type fileFetcher struct{ path string }

func (f fileFetcher) Fetch(context.Context) ([]byte, error) {
	data, err := os.ReadFile(f.path)
	if err != nil {
		return nil, err
	}
	return bytes.TrimRight(data, "\r\n"), nil
}
