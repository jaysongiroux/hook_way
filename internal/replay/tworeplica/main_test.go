package tworeplica

import (
	"os"
	"testing"

	"github.com/jaysongiroux/hook_way/internal/replay/replaytest"
)

func TestMain(m *testing.M) { os.Exit(replaytest.Main(m)) }
