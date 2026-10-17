// Local runtime fixture for the builder's official-SDK interoperability test.
package main

import (
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"os/signal"
	"syscall"

	"github.com/jaysongiroux/hook_way/internal/config"
	"github.com/jaysongiroux/hook_way/internal/flowconfig"
	"github.com/jaysongiroux/hook_way/internal/httpapi"
)

func main() {
	cfg, issues := flowconfig.Load(os.Args[1])
	for _, issue := range issues {
		if issue.EffectiveSeverity() == config.SeverityError {
			panic(issue.Message)
		}
	}
	server, err := httpapi.NewFlowServer(cfg, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	if err != nil {
		panic(err)
	}
	defer func() { _ = server.Close() }()
	ts := httptest.NewServer(server)
	defer ts.Close()
	fmt.Println(ts.URL)
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, os.Interrupt)
	<-stop
}
