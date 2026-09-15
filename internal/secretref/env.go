package secretref

import (
	"context"
	"fmt"
	"os"
)

type envFetcher struct{ name string }

func (e envFetcher) Fetch(context.Context) ([]byte, error) {
	v, ok := os.LookupEnv(e.name)
	if !ok {
		return nil, fmt.Errorf("environment variable %s is not set", e.name)
	}
	if v == "" {
		return nil, fmt.Errorf("environment variable %s is empty", e.name)
	}
	return []byte(v), nil
}
